// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package sys manages the state of a running dex system.
package sys

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/ardilla"
	"github.com/kortschak/jsonrpc2"
	"golang.org/x/exp/slices"
	"golang.org/x/sys/unix"

	"github.com/kortschak/dex/internal/config"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

// Manager is a dex system manager.
type Manager[K Kernel, D Device[B], B Button] struct {
	mu sync.Mutex

	// kernel is the current RPC kernel.
	kernel K
	// running indicates whether the
	// RPC kernel is running.
	running bool

	// newKernel is the kernel constructor.
	newKernel NewKernel[K]
	options   jsonrpc2.NetListenOptions
	// builtins and funcs are the builtins
	// and functions added to kernels.
	builtins map[string]jsonrpc2.Binder
	funcs    rpc.Funcs

	// devices is the set of devices configured
	// to send events to services.
	devices map[string]D
	// serviceSerial is the serial number
	// of the device the service is registered
	// to use.
	serviceSerial map[rpc.UID]string
	// missingSerial is the set of devices that
	// were not available at start up but were
	// not required.
	missingSerial map[string]bool
	// missingService is the set of services that
	// depend on missing devices.
	missingService map[rpc.UID]bool

	// newDevice is the dex device constructor.
	newDevice NewDevice[K, D, B]

	// current is the currently running
	// configuration state.
	current *config.System
	// spawned is the set of spawned modules.
	spawned map[string]bool

	// parentLog is the original log handed to
	// Manager. This is passed to constructed
	// RPC kernels and device managers.
	parentLog *slog.Logger
	// log is the active logger used by the
	// Manager.
	log *slog.Logger
	// level controls the logging level of all
	// loggers.
	level *slog.LevelVar
	// addSource controls whether logging includes
	// the source code logging call site.
	addSource *atomic.Bool

	// store and datadir are the common persistent
	// data store and common data directory path
	// for modules and the kernel.
	store   Store
	datadir string
}

// NewManager returns a new system manager.
func NewManager[K Kernel, D Device[B], B Button](kernel NewKernel[K], device NewDevice[K, D, B], store Store, datadir string, log *slog.Logger, level *slog.LevelVar, addSource *atomic.Bool) (*Manager[K, D, B], error) {
	datadir, err := filepath.Abs(datadir)
	if err != nil {
		return nil, err
	}
	return &Manager[K, D, B]{
		newKernel: kernel,
		options:   jsonrpc2.NetListenOptions{}, // TODO: Make this a config option.
		newDevice: device,
		spawned:   make(map[string]bool),
		parentLog: log,
		log:       log.With(slog.String("component", kernelUID.String())),
		level:     level,
		addSource: addSource,
		store:     store,
		datadir:   datadir,
	}, nil
}

var kernelUID = rpc.UID{Module: "kernel", Service: "sys"}

// Store returns the manager's persistent data store which may be nil.
func (m *Manager[K, D, B]) Store() Store {
	return m.store
}

// Datadir returns the manager's data directory.
func (m *Manager[K, D, B]) Datadir() string {
	return m.datadir
}

// NewKernel is a new RPC kernel constructor.
type NewKernel[K Kernel] func(ctx context.Context, network string, options jsonrpc2.NetListenOptions, log *slog.Logger) (K, error)

// Kernel is the RPC kernel interface.
type Kernel interface {
	Builtin(ctx context.Context, uid string, dialer net.Dialer, binder jsonrpc2.Binder) error
	Funcs(funcs rpc.Funcs)
	jsonrpc2.Handler

	Conn(ctx context.Context, uid string) (rpc.Connection, time.Time, bool)

	Spawn(ctx context.Context, stdout, stderr io.Writer, done func(), uid, name string, args ...string) error
	SetDrop(uid string, drop func() error) error
	Kill(uid string)

	Close() error
}

// NewDevice is a new dex device constructor.
type NewDevice[K Kernel, D Device[B], B Button] func(ctx context.Context, pid ardilla.PID, serial string, kernel K, log *slog.Logger) (D, error)

// Device is the dex device interface.
type Device[B Button] interface {
	Serial() string

	SetPages(ctx context.Context, deflt *string, pages []string) error
	SendTo(service rpc.UID, listen []config.Button) error

	Layout() (rows, cols int)
	Key(row, col int) int

	CurrentName() string
	Page(name string) (p Page[B], ok bool)
	PageNames() []string
	PageDetails() map[string][]config.Button

	Bounds() (image.Rectangle, error)
	RawImage(img image.Image) (*ardilla.RawImage, error)

	SetDisplayTo(ctx context.Context, name string) error
	SetBrightness(percent int) error

	SleepState() string
	Last() time.Time
	Wake(ctx context.Context)
	Blank() error
	Clear() error

	Close() error
}

// Page is a dex device page of buttons.
type Page[B Button] interface {
	Button(row, col int) B
}

// Button wraps the button drawing method.
type Button interface {
	Draw(ctx context.Context, img image.Image)
}

// ErrNotFound is returned by Store.Get if the item is not found.
var ErrNotFound = jsonrpc2.NewError(rpc.ErrCodeNotFound, "item not found")

// Store is a common persistent data store held by the manager.
type Store interface {
	Set(owner rpc.UID, item string, val []byte) error
	Get(owner rpc.UID, item string) (val []byte, err error)
	Put(owner rpc.UID, item string, new []byte) (old []byte, written bool, err error)
	Delete(owner rpc.UID, item string) error
	Drop(owner rpc.UID) error
	DropModule(module string) error
}

// Configure changes a managed system's state to match the provided
// configuration. If the system is not yet running, it will be started.
func (m *Manager[K, D, B]) Configure(ctx context.Context, cfg *config.System) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.log.LogAttrs(ctx, slog.LevelDebug, "set config", slog.Any("config", slogext.PrivateRedact{Val: cfg, Tag: "json"}))

	paths, err := config.Vet(cfg)
	if err != nil {
		m.log.LogAttrs(ctx, slog.LevelInfo, "invalid config", slog.Any("error", err), slog.Any("paths", paths), slog.Any("config", cfg))
		return nil
	}
	// Fill name into service configurations.
	for name, svc := range cfg.Services {
		svc.Name = name
	}

	if cfg.Kernel.LogLevel != nil {
		m.level.Set(*cfg.Kernel.LogLevel)
	}
	if cfg.Kernel.AddSource != nil {
		m.addSource.Store(*cfg.Kernel.AddSource)
	}

	// No running config, so boot a system.
	if m.current == nil {
		return m.boot(ctx, cfg)
	}

	// The running config is operating on a different network,
	// so pull it down and start again.
	if cfg.Kernel.Network != m.current.Kernel.Network {
		err := m.kernel.Close()
		if err != nil {
			m.log.LogAttrs(ctx, slog.LevelWarn, "failed close previous kernel", slog.Any("error", err))
		}
		return m.boot(ctx, cfg)
	}

	// Make any kernel configuration changes here.

	// Update active devices.
	m.log.LogAttrs(ctx, slog.LevelDebug, "set devices", slog.Any("devices", cfg.Kernel.Device))
	wantDevices := make(map[string]config.Device)
	for _, dev := range cfg.Kernel.Device {
		if dev.Serial != nil {
			wantDevices[*dev.Serial] = dev
		}
	}
	// Stop dropped devices.
	for serial, d := range m.devices {
		if _, ok := wantDevices[serial]; !ok {
			err = d.Close()
			if err != nil {
				m.log.LogAttrs(ctx, slog.LevelWarn, "failed close device", slog.String("serial", serial), slog.Any("error", err))
			}
		}
	}
	// Start new devices.
	err = m.setDevices(ctx, cfg.Kernel.Device)
	if err != nil {
		return err
	}

	// Remove modules and deconfigure services that are currently running.
	configured := moduleInstances(cfg)
	running := moduleInstances(m.current)
	pages := make(map[string][]string)
	removeOrder := make([]string, 0, len(running))
	for uid := range running {
		removeOrder = append(removeOrder, uid)
	}
	sort.Strings(removeOrder)
	for _, uid := range removeOrder {
		cfgs := running[uid]
		if len(cfgs) == 0 {
			continue
		}
		if uid == "" {
			// Kernel service.
			continue
		}

		wantModule := cfg.Modules[uid] != nil
		want := make(map[string]bool)
		for _, a := range configured[uid] {
			want[a.Name] = true
		}

		conn, _, connOK := m.kernel.Conn(ctx, uid)
		if !connOK {
			m.log.LogAttrs(ctx, slog.LevelWarn, "no conn to module", slog.String("uid", uid))
		}
		for _, c := range cfgs {
			if want[c.Name] {
				continue
			}
			if c.Serial != nil {
				pages[*c.Serial] = nil
			}
			svc := rpc.UID{Module: uid, Service: c.Name}

			// Stop sending events to service and remove
			// it from its device's handler list,
			m.log.LogAttrs(ctx, slog.LevelDebug, "unset send to service", slog.Any("uid", svc))
			if c.Serial != nil && !m.missingSerial[*c.Serial] {
				err = m.devices[*c.Serial].SendTo(rpc.UID{Module: uid, Service: c.Name}, nil)
				if err != nil {
					m.log.LogAttrs(ctx, slog.LevelWarn, "failed unset send to service", slog.Any("uid", svc), slog.Any("error", err))
				}
			}
			m.log.LogAttrs(ctx, slog.LevelDebug, "remove service from devices handler list", slog.Any("uid", svc))
			delete(m.serviceSerial, svc)

			if !wantModule {
				m.log.LogAttrs(ctx, slog.LevelDebug, "kill module", slog.Any("uid", uid))
				m.kernel.Kill(uid)
				delete(m.spawned, uid)
				// No need to deconfigure service; it will be terminated.
				continue
			}
			if connOK {
				m.log.LogAttrs(ctx, slog.LevelDebug, "deconfigure service", slog.Any("uid", svc))
				err = conn.Notify(ctx, "configure", rpc.NewMessage(kernelUID, &config.Service{
					Name:   c.Name,
					Active: ptr(false),
				}))
				if err != nil {
					m.log.LogAttrs(ctx, slog.LevelWarn, "failed configure service", slog.Any("uid", svc), slog.Any("error", err))
				}
			}
		}
	}

	m.configureModules(ctx, cfg.Kernel.Device, cfg.Modules, configured, pages)

	m.current = cfg
	return nil
}

// boot starts a managed system with the provided configuration.
func (m *Manager[K, D, B]) boot(ctx context.Context, cfg *config.System) error {
	err := m.startNetwork(ctx, cfg.Kernel.Network, m.options)
	if err != nil {
		return err
	}

	m.devices = make(map[string]D)
	m.serviceSerial = make(map[rpc.UID]string)
	m.missingSerial = make(map[string]bool)
	m.missingService = make(map[rpc.UID]bool)
	m.log.LogAttrs(ctx, slog.LevelDebug, "set devices", slog.Any("devices", cfg.Kernel.Device))
	err = m.setDevices(ctx, cfg.Kernel.Device)
	if err != nil {
		return err
	}

	m.configureModules(ctx, cfg.Kernel.Device, cfg.Modules, moduleInstances(cfg), make(map[string][]string))

	m.current = cfg
	return nil
}

func moduleInstances(cfg *config.System) map[string][]config.Service {
	services := make(map[string][]config.Service)
	for _, cfg := range cfg.Services {
		mod := ""
		if cfg.Module != nil {
			mod = *cfg.Module
		}
		services[mod] = append(services[mod], *cfg)
	}
	return services
}

// setDevices starts any device managers in devices that are not already running.
func (m *Manager[K, D, B]) setDevices(ctx context.Context, devices []config.Device) error {
	for _, dev := range devices {
		if dev.Serial == nil {
			continue
		}
		if _, exists := m.devices[*dev.Serial]; exists {
			continue
		}
		d, err := m.newDevice(ctx, dev.PID, *dev.Serial, m.kernel, m.parentLog)
		if err != nil {
			if !dev.Required {
				m.log.LogAttrs(ctx, slog.LevelWarn, "non-required device missing", slog.String("pid", fmt.Sprintf("0x%04x", uint16(dev.PID))), slog.Any("model", slogext.Stringer{Stringer: dev.PID}), slog.String("serial", *dev.Serial), slog.Any("error", err))
				m.missingSerial[*dev.Serial] = true
				continue
			}
			// Clean up opened devices.
			for _, d := range m.devices {
				err = d.Close()
				if err != nil {
					m.log.LogAttrs(ctx, slog.LevelWarn, "failed close device", slog.String("serial", *dev.Serial), slog.Any("error", err))
				}
			}
			return err
		}
		m.initBrightness(ctx, d)
		m.devices[*dev.Serial] = d
	}
	return nil
}

func (m *Manager[K, D, B]) initBrightness(ctx context.Context, d D) {
	if m.store == nil {
		m.log.LogAttrs(ctx, slog.LevelDebug, "no store for brightness")
		return
	}
	var val []byte
	devUID := rpc.UID{Module: "kernel", Service: d.Serial()}
	val, err := m.store.Get(devUID, "brightness")
	if err != nil {
		if err == ErrNotFound {
			m.log.LogAttrs(ctx, slog.LevelDebug, "no stored brightness", slog.Any("uid", devUID))
		} else {
			m.log.LogAttrs(ctx, slog.LevelDebug, "error getting stored brightness", slog.Any("uid", devUID), slog.Any("error", err))
		}
		return
	}
	if len(val) != 1 {
		m.log.LogAttrs(ctx, slog.LevelError, "unexpected brightness value length", slog.Any("uid", devUID), slog.String("brightness", string(val)))
		return
	}
	percent := int(val[0])
	switch {
	case percent < 0:
		percent = 0
	case percent > 100:
		percent = 100
	}
	err = d.SetBrightness(percent)
	if err != nil {
		m.log.LogAttrs(ctx, slog.LevelError, "failed to set brightness", slog.Any("uid", devUID), slog.Any("error", err))
	}
}

func (m *Manager[K, D, B]) configureModules(ctx context.Context, devices []config.Device, modules map[string]*config.Module, configured map[string][]config.Service, pages map[string][]string) {
	// Handle kernel module services.
	for _, cfg := range configured[""] {
		uid := ""
		svc := rpc.UID{Module: uid, Service: cfg.Name}
		if cfg.Serial != nil {
			if m.missingSerial[*cfg.Serial] {
				m.missingService[svc] = true
				continue
			}
			m.log.LogAttrs(ctx, slog.LevelDebug, "add service to devices handler list", slog.Any("uid", svc), slog.String("serial", *cfg.Serial))
			m.serviceSerial[svc] = *cfg.Serial
		}
		delete(m.missingService, svc)
		if cfg.Serial != nil {
			// Register requested button events.
			m.log.LogAttrs(ctx, slog.LevelDebug, "request notifications", slog.String("uid", uid), slog.String("name", cfg.Name), slog.Any("listen", cfg.Listen))
			err := m.devices[*cfg.Serial].SendTo(rpc.UID{Module: uid, Service: cfg.Name}, cfg.Listen)
			if err != nil {
				m.log.LogAttrs(ctx, slog.LevelWarn, "failed set send to service", slog.Any("uid", svc), slog.Any("error", err))
			}
		}

		ps := make(map[string]struct{})
		for _, l := range cfg.Listen {
			ps[l.Page] = struct{}{}
		}
		if cfg.Serial != nil {
			for p := range ps {
				pages[*cfg.Serial] = append(pages[*cfg.Serial], p)
			}
		}
	}

	// Handle module configurations.
	initOrder := make([]string, 0, len(modules))
	for uid := range modules {
		initOrder = append(initOrder, uid)
	}
	sort.Strings(initOrder)
	for _, uid := range initOrder {
		mod := modules[uid]
		if !m.spawned[uid] {
			m.log.LogAttrs(ctx, slog.LevelDebug, "spawn module", slog.String("uid", uid))
			// log modes (see config.Module.LogMode):
			//   log:         stdout → stderr (or perhaps in future wherever we are logging to)
			//                stderr → capture and log with slog
			//   passthrough: stdout → stdout
			//                stderr → stderr
			//   none:        stdout → /dev/null
			//                stderr → /dev/null
			var stdout, stderr io.Writer
			args := mod.Args
			switch mod.LogMode {
			case "log":
				if !slices.Contains(args, "-log_stdout") {
					args = make([]string, len(args)+1)
					args[0] = "-log_stdout"
					copy(args[1:], mod.Args)
				}
				stdout = os.Stderr
				stderr = &logWriter{
					ctx:     ctx,
					log:     m.log,
					level:   slog.LevelError,
					msg:     uid + " error logging",
					label:   "stderr",
					maxLine: 80,
				}
			case "none":
				// Discard.
			case "passthrough":
				fallthrough
			default:
				stdout = os.Stdout
				stderr = os.Stderr
			}

			enoughFileDescriptors := func() bool {
				fds, ok, err := withinUlimit(0.95)
				switch err {
				case nil, errNotSupported:
				default:
					// If we should be able to get a limit, but can't, fail safely.
					m.log.LogAttrs(ctx, slog.LevelError, "failed to get resource limits", slog.Any("error", err), slog.String("uid", uid))
					return false
				}
				if !ok {
					m.log.LogAttrs(ctx, slog.LevelWarn, "exceeded file descriptor limit", slog.Int("open_files", fds), slog.String("uid", uid))
				}
				return ok
			}

			var done func()
			done = func() {
				// Check whether the daemon is in m.spawned and if it is,
				// restart the executable. Removal of the uid from m.spawned
				// is done in Configure when a module is not wanted.

				m.mu.Lock()
				defer m.mu.Unlock()
				if !m.spawned[uid] {
					return
				}
				m.log.LogAttrs(ctx, slog.LevelWarn, "daemon died", slog.String("uid", uid))
				if !enoughFileDescriptors() {
					m.log.LogAttrs(ctx, slog.LevelWarn, "not restarting", slog.String("uid", uid))
					return
				}
				err := m.kernel.Spawn(ctx, stdout, stderr, done, uid, mod.Path, args...)
				if err != nil {
					m.log.LogAttrs(ctx, slog.LevelError, "failed to restart", slog.String("uid", uid), slog.Any("error", err))
				} else {
					m.log.LogAttrs(ctx, slog.LevelInfo, "daemon respawned", slog.String("uid", uid))
				}
			}
			if !enoughFileDescriptors() {
				continue
			}
			err := m.kernel.Spawn(ctx, stdout, stderr, done, uid, mod.Path, args...)
			if err != nil {
				m.log.LogAttrs(ctx, slog.LevelWarn, "failed to start", slog.String("uid", uid), slog.Any("error", err))
				continue
			}
			m.spawned[uid] = true
		} else {
			m.log.LogAttrs(ctx, slog.LevelDebug, "module already spawned", slog.String("uid", uid))
		}
		conn, _, ok := m.kernel.Conn(ctx, uid)
		if !ok {
			m.log.LogAttrs(ctx, slog.LevelWarn, "no conn to module", slog.String("uid", uid))
		} else {
			// Configure module.
			if !m.sameModConfig(uid, mod) {
				m.log.LogAttrs(ctx, slog.LevelDebug, "configure module", slog.String("uid", uid))
				err := conn.Notify(ctx, "configure", rpc.NewMessage(kernelUID, &config.Module{
					LogLevel:  mod.LogLevel,
					AddSource: mod.AddSource,
					Options:   mod.Options,
				}))
				if err != nil {
					m.log.LogAttrs(ctx, slog.LevelWarn, "failed configure module", slog.String("uid", uid), slog.Any("error", err))
				}
			}

			// Configure each service. Only register with a device
			// if the serial is present. Other services may be
			// non-deck services.
			for _, cfg := range configured[uid] {
				svc := rpc.UID{Module: uid, Service: cfg.Name}
				if cfg.Serial != nil {
					m.log.LogAttrs(ctx, slog.LevelDebug, "add service to devices handler list", slog.Any("uid", svc), slog.String("serial", *cfg.Serial))
					m.serviceSerial[svc] = *cfg.Serial
				}
				if !m.sameInstConfig(cfg.Name, &cfg, ignoreListen) {
					// Reconfigure config.
					m.log.LogAttrs(ctx, slog.LevelDebug, "configure service", slog.Any("uid", svc))
					err := conn.Notify(ctx, "configure", rpc.NewMessage(kernelUID, &config.Service{
						Name:    cfg.Name,
						Active:  ptr(true),
						Serial:  cfg.Serial,
						Options: cfg.Options,
					}))
					if err != nil {
						m.log.LogAttrs(ctx, slog.LevelWarn, "failed configure service", slog.Any("uid", svc), slog.Any("error", err))
					}
				}
				if cfg.Serial != nil && m.missingSerial[*cfg.Serial] {
					continue
				}
				if cfg.Serial != nil {
					// Register requested button events.
					m.log.LogAttrs(ctx, slog.LevelDebug, "request notifications", slog.String("uid", uid), slog.String("name", cfg.Name), slog.Any("listen", cfg.Listen))
					err := m.devices[*cfg.Serial].SendTo(rpc.UID{Module: uid, Service: cfg.Name}, cfg.Listen)
					if err != nil {
						m.log.LogAttrs(ctx, slog.LevelWarn, "failed set send to service", slog.Any("uid", svc), slog.Any("error", err))
					}
				}

				ps := make(map[string]struct{})
				for _, l := range cfg.Listen {
					ps[l.Page] = struct{}{}
				}
				if cfg.Serial != nil {
					for p := range ps {
						pages[*cfg.Serial] = append(pages[*cfg.Serial], p)
					}
				}
			}
		}
	}

	defaultFor := make(map[string]*string)
	for _, d := range devices {
		if d.Serial != nil {
			defaultFor[*d.Serial] = d.Default
		}
	}
	for serial, p := range pages {
		p = unique(p)
		m.log.LogAttrs(ctx, slog.LevelDebug, "set pages", slog.Any("serial", serial), slog.Any("pages", p))
		err := m.devices[serial].SetPages(ctx, defaultFor[serial], p)
		if err != nil {
			m.log.LogAttrs(ctx, slog.LevelWarn, "failed set pages", slog.Any("serial", serial), slog.Any("pages", p), slog.Any("error", err))
		}
	}
}

var errNotSupported = errors.New("not supported")

func withinUlimit(factor float64) (fds int, ok bool, err error) {
	var fdPath string
	switch runtime.GOOS {
	default:
		return -1, true, errNotSupported
	case "darwin":
		fdPath = "/dev/fd"
	case "linux":
		fdPath = "/proc/self/fd"
	}
	f, err := os.Open(fdPath)
	if err != nil {
		return -1, false, fmt.Errorf("could not open file descriptors directory: %w", err)
	}
	defer f.Close()
	d, err := f.Readdirnames(0)
	if err != nil {
		return -1, false, fmt.Errorf("could not read file descriptors directory: %w", err)
	}
	fds = len(d)

	var lim unix.Rlimit
	err = unix.Getrlimit(unix.RLIMIT_NOFILE, &lim)
	if err != nil {
		return fds, false, err
	}
	ulimit := lim.Cur

	return fds, fds < int(factor*float64(ulimit)), nil
}

func unique(pages []string) []string {
	if len(pages) < 2 {
		return pages
	}
	sort.Slice(pages, func(i, j int) bool {
		return pages[i] < pages[j]
	})
	curr := 0
	for i, p := range pages {
		if p == pages[curr] {
			continue
		}
		curr++
		if curr < i {
			pages[curr], pages[i] = pages[i], ""
		}
	}
	return pages[:curr+1]
}

var ignoreListen = cmp.FilterValues(
	func(_, _ []config.Button) bool { return true },
	cmp.Ignore(),
)

var (
	ignoreOptions = cmp.FilterPath(
		func(p cmp.Path) bool {
			return p.Last().Type() == mapStringAny && p.String() == "Options"
		},
		cmp.Ignore(),
	)
	mapStringAny = reflect.TypeOf(map[string]any{})
)

func (m *Manager[K, D, B]) sameModConfig(uid string, mod *config.Module) bool {
	oldIsNil := m.current == nil
	newIsNil := mod == nil
	if oldIsNil != newIsNil {
		return false
	}
	return cmp.Equal(mod, m.current.Modules[uid], ignoreDynamic)
}

func (m *Manager[K, D, B]) sameInstConfig(uid string, mod *config.Service, option cmp.Option) bool {
	oldIsNil := m.current == nil
	newIsNil := mod == nil
	if oldIsNil != newIsNil {
		return false
	}
	return cmp.Equal(mod, m.current.Services[uid], ignoreDynamic, option)
}

var ignoreDynamic = cmp.FilterValues(
	func(a, b any) bool {
		switch a.(type) {
		default:
			return false
		case *config.Sum:
			_, ok := b.(*config.Sum)
			return ok
		case error:
			_, ok := b.(error)
			return ok
		}
	},
	cmp.Ignore(),
)

func ptr[T any](v T) *T { return &v }

// Kernel returned the RPC kernel held by the manager and whether the
// kernel is running.
func (m *Manager[K, D, B]) Kernel() (kernel K, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.kernel, m.running
}

// SetBuiltins inserts the provided builtins into the RPC kernel's handler.
// If builtins is nil the entire kernel mapping table is reset.
func (m *Manager[K, D, B]) SetBuiltins(ctx context.Context, builtins map[string]jsonrpc2.Binder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.setBuiltins(ctx, builtins)
}

func (m *Manager[K, D, B]) setBuiltins(ctx context.Context, builtins map[string]jsonrpc2.Binder) error {
	if m.running {
		for uid := range m.builtins {
			// Terminate builtins that have been registered in
			// case the uid is replacing or removing an existing
			// daemon.
			if _, ok := builtins[uid]; ok {
				m.kernel.Kill(uid)
			}
		}
		for uid, d := range builtins {
			if d == nil {
				continue
			}
			err := m.kernel.Builtin(ctx, uid, m.options.NetDialer, d)
			if err != nil {
				return err
			}
		}
	}
	if builtins == nil {
		m.builtins = nil
		return nil
	}
	if m.builtins == nil {
		m.builtins = make(map[string]jsonrpc2.Binder)
	}
	for uid, d := range builtins {
		if d != nil {
			m.builtins[uid] = d
		} else {
			delete(m.builtins, uid)
		}
	}
	return nil
}

// ErrAllowedMissingDevice is returned by [Manager.DeviceFor] when a device
// is not available for a service, but the device has been configured with
// [config.Device.Required] set to false.
var ErrAllowedMissingDevice = errors.New("allowed missing device")

// DeviceFor returns the device the service is registered to.
func (m *Manager[K, D, B]) DeviceFor(svc rpc.UID) (D, error) {
	m.log.LogAttrs(context.Background(), slog.LevelDebug, "request device", slog.Any("uid", svc))
	var device D
	m.mu.Lock()
	defer m.mu.Unlock()
	serial, ok := m.serviceSerial[svc]
	if !ok {
		if m.missingService[svc] {
			m.log.LogAttrs(context.Background(), slog.LevelDebug, "no serial", slog.Any("uid", svc))
			return device, ErrAllowedMissingDevice
		}
		m.log.LogAttrs(context.Background(), slog.LevelWarn, "no serial", slog.Any("uid", svc))
		return device, rpc.NewError(rpc.ErrCodeInvalidData,
			fmt.Sprintf("no serial for %s", svc),
			map[string]any{
				"type": rpc.ErrCodeNoDevice,
				"uid":  svc,
			},
		)
	}
	if m.missingSerial[serial] {
		m.log.LogAttrs(context.Background(), slog.LevelWarn, "no device", slog.Any("uid", svc), slog.Any("serial", serial))
		return device, rpc.NewError(rpc.ErrCodeInvalidData,
			fmt.Sprintf("no device for %s serial %s", svc, serial),
			map[string]any{
				"type":   rpc.ErrCodeNoDevice,
				"uid":    svc,
				"serial": serial,
			},
		)
	}
	device, ok = m.devices[serial]
	if !ok {
		m.log.LogAttrs(context.Background(), slog.LevelWarn, "no device", slog.Any("uid", svc), slog.String("serial", serial))
		return device, rpc.NewError(rpc.ErrCodeInvalidData,
			fmt.Sprintf("no device for %s serial %s", svc, serial),
			map[string]any{
				"type":   rpc.ErrCodeNoDevice,
				"uid":    svc,
				"serial": serial,
			},
		)
	}
	return device, nil
}

// SetFuncs inserts the provided functions into the RPC kernel's handler.
// If funcs is nil the entire kernel mapping table is reset.
func (m *Manager[K, D, B]) SetFuncs(funcs rpc.Funcs) {
	m.mu.Lock()
	m.setFuncs(funcs)
	m.mu.Unlock()
}

func (m *Manager[K, D, B]) setFuncs(funcs rpc.Funcs) {
	m.funcs = funcs
	if m.running {
		m.kernel.Funcs(m.funcs)
	}
}

// startNetwork starts the RPC network.
func (m *Manager[K, D, B]) startNetwork(ctx context.Context, network string, options jsonrpc2.NetListenOptions) error {
	m.log.LogAttrs(ctx, slog.LevelDebug, "start rpc kernel", slog.String("network", network), slog.Any("options", listenOptionsValue{options}))
	k, err := m.newKernel(ctx, network, options, m.parentLog)
	if err != nil {
		return err
	}
	m.kernel = k
	m.running = true
	m.setFuncs(m.funcs)
	err = m.setBuiltins(ctx, m.builtins)
	if err != nil {
		m.Close()
		return err
	}
	return nil
}

// Close terminates the kernel and all devices.
func (m *Manager[K, D, B]) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.close()
	var none K
	m.kernel = none
	m.devices = nil
	m.missingSerial = nil
	m.missingService = nil
	return err
}

// close terminates the kernel and all devices but retains the final state
// of the kernel and devices. It is intended to be used for testing.
func (m *Manager[K, D, B]) close() error {
	if !m.running {
		return nil
	}
	for _, d := range m.devices {
		d.Close()
	}
	m.spawned = nil // Prevent daemons from attempting to respawn.
	err := m.kernel.Close()
	m.running = false
	return err
}
