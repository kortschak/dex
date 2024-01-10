// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/kortschak/ardilla"
	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/config"
	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"
)

var (
	_ sys.NewDevice[*rpc.Kernel, *Manager, *Button] = NewManager[*rpc.Kernel]

	_ sys.Device[*Button] = (*Manager)(nil)
	_ sys.Page[*Button]   = Page{}
)

// Manager controls a set of El Gato device pages and manages their
// interactions.
type Manager struct {
	controller *Controller

	mu    sync.Mutex
	pages pageManager
}

var kernelUID = rpc.UID{Module: "kernel", Service: "dev"}

// NewManager returns a new device manager controlling the physical device
// specified by pid and serial and communicating via the provided RPC kernel.
func NewManager[K sys.Kernel](ctx context.Context, pid ardilla.PID, serial string, kernel K, log *slog.Logger) (*Manager, error) {
	c, err := NewController(ctx, kernel, pid, serial, log)
	if err != nil {
		return nil, err
	}
	return &Manager{
		controller: c,
		pages:      newPageManager(log),
	}, nil
}

// Serial returns the serial number of the underlying dex device.
func (m *Manager) Serial() string {
	return m.controller.serial
}

// SendTo queues requests to send button actions to the specified service.
// requests are not acted on until a subsequent call to SetPages.
func (m *Manager) SendTo(service rpc.UID, actions []config.Button) error {
	if m == nil {
		return fmt.Errorf("no device for service: %s", service)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pages.sendTo(m.controller, service, actions)
}

// SetPages specifies which pages will be active and the name of the default
// page. If deflt is nil the default page name is unchanged. When SetPages is
// called, all pending requests queued by SendTo as installed. Any existing
// button actions that were not in the pending requests are removed.
func (m *Manager) SetPages(ctx context.Context, deflt *string, pages []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.pages.setPages(ctx, m.controller, deflt, pages)
	if err != nil {
		return err
	}
	m.pages.notify = nil
	return nil
}

// Layout returns the number of rows and columns of buttons on the managed
// device.
func (m *Manager) Layout() (rows, cols int) {
	return m.controller.Layout()
}

// Key returns the key number corresponding to the given row and column.
// It panics if row or col are out of bounds.
func (m *Manager) Key(row, col int) int {
	return m.controller.Key(row, col)
}

// CurrentName returns the name of the currently displayed page.
func (m *Manager) CurrentName() string {
	return m.controller.CurrentName()
}

// SetDisplayTo sets the current display the named page. SetDisplayTo returns
// an error if the page does not exist.
func (m *Manager) SetDisplayTo(ctx context.Context, name string) error {
	return m.controller.SetDisplayTo(ctx, name)
}

// Page returns the named page.
func (m *Manager) Page(name string) (p sys.Page[*Button], ok bool) {
	return m.controller.Page(name)
}

// PageNames returns a list of the device's page names.
func (m *Manager) PageNames() []string {
	return m.controller.PageNames()
}

// PageDetails returns the device's page details.
func (m *Manager) PageDetails() map[string][]config.Button {
	pages := make(map[string][]config.Button, len(m.pages.state))
	for name, page := range m.pages.state {
		for _, module := range page {
			for _, actions := range module {
				pages[name] = append(pages[name], actions...)
			}
		}
	}
	for name, buttons := range pages {
		pages[name] = unique(buttons)
	}
	return pages
}

// Bounds returns the image bounds for buttons on the managed device. If the
// device is not visual an error is returned.
func (m *Manager) Bounds() (image.Rectangle, error) {
	return m.controller.Bounds()
}

// RawImage returns an image.Image has had the internal image representation
// pre-computed after resizing to fit the Deck's button size. The original image
// is retained in the returned image.
func (m *Manager) RawImage(img image.Image) (*ardilla.RawImage, error) {
	return m.controller.RawImage(img)
}

// Wake unpauses the current page and redraws it.
func (m *Manager) Wake(ctx context.Context) {
	m.controller.Wake(ctx)
}

// Sleep pauses the current page and blanks it.
func (m *Manager) Sleep() error {
	return m.controller.Blank()
}

// Clear pauses the current page and clears it.
func (m *Manager) Clear() error {
	return m.controller.Clear()
}

// SetBrightness sets the global screen brightness of the Stream Deck, across
// all the device's buttons.
func (m *Manager) SetBrightness(percent int) error {
	return m.controller.SetBrightness(percent)
}

// Close closes the manager and releases its resources.
func (m *Manager) Close() error {
	return m.controller.Close()
}

// pageManager handles the logic of page and button action updates.
type pageManager struct {
	log *slog.Logger

	// sendTo requests keyed by page name.
	last map[string][]sendToRequest
	reqs map[string][]sendToRequest

	state  map[string]map[pos]map[rpc.UID][]config.Button
	notify map[svcConn]Notification
}

// pos is a button position on a page.
type pos struct{ row, col int }

// svcConn is an rpc.Connection with a label to the service it is connecting
// to. It is primarily used for testing purposes.
type svcConn struct {
	uid rpc.UID
	rpc.Connection
}

// newPageManager returns a new pageManager.
func newPageManager(log *slog.Logger) pageManager {
	return pageManager{log: log, last: make(map[string][]sendToRequest)}
}

// sendToRequest represents a listener's send-to request.
type sendToRequest struct {
	Service rpc.UID         `json:"service"`
	Actions []config.Button `json:"actions"`
}

// device is wraps the Controller's method set for testing purposes.
type device interface {
	Layout() (rows, cols int)
	Key(row, col int) int

	DefaultName() string
	SetDefaultName(name string) error

	Page(name string) (p Page, ok bool)
	PageNames() []string
	NewPage(name string) error
	Delete(ctx context.Context, name string) error
	Rename(old, new string) error

	conn(ctx context.Context, uid string) (rpc.Connection, time.Time, bool)
	handle(ctx context.Context, req *jsonrpc2.Request) (any, error)
}

// sendTo queues a set of actions to be added.
func (p *pageManager) sendTo(dev device, service rpc.UID, actions []config.Button) error {
	if p.reqs == nil {
		p.reqs = make(map[string][]sendToRequest)
	}
	if len(actions) == 0 {
		// Mark for deletion from all pages it is found in.
		for name, page := range p.state {
			for _, module := range page {
				if _, ok := module[service]; !ok {
					continue
				}
				p.reqs[name] = append(p.reqs[name], sendToRequest{
					Service: service,
				})
			}
		}
		return nil
	}
	deflt := dev.DefaultName()
	for _, a := range actions {
		if a.Page == "" {
			a.Page = deflt
		}
		p.reqs[a.Page] = append(p.reqs[a.Page], sendToRequest{
			Service: service, Actions: []config.Button{a},
		})
	}
	return nil
}

// setPages reconciles the current state of the device with the requested
// state determined from the specified pages and enqueued actions.
// It makes the changes concrete in the physical device and sends notification
// to the services that have requested actions that they will be available,
// or that they have been stopped. A service does not need to take notice
// of the notifications.
func (p *pageManager) setPages(ctx context.Context, dev device, deflt *string, pages []string) error {
	p.log.LogAttrs(ctx, slog.LevelDebug, "set pages", slog.Any("pages", pages))

	defer func() {
		p.reqs = nil
	}()
	noChange := reflect.DeepEqual(p.last, p.reqs)

	defaultName := dev.DefaultName()
	if noChange {
		sameDefault := deflt == nil || *deflt == defaultName
		if !sameDefault {
			p.log.LogAttrs(ctx, slog.LevelDebug, "change default", slog.Any("from", defaultName), slog.Any("to", *deflt))
			err := dev.SetDefaultName(*deflt)
			if err != nil {
				return err
			}
			p.last[*deflt] = p.last[defaultName]
			delete(p.last, defaultName)
			p.state[*deflt] = p.state[defaultName]
			delete(p.state, defaultName)
		}
		return nil
	}

	// Find which are the same and start, reconfigure or stop others.
	if deflt == nil {
		deflt = &defaultName
	}
	want := getState(p.state, p.reqs, pages, *deflt)
	p.notify = make(map[svcConn]Notification) // Retain temporarily for testing.
	for name := range p.state {
		if len(want[name]) != 0 {
			continue
		}

		p.log.LogAttrs(ctx, slog.LevelDebug, "delete page", slog.String("page", name))
		err := dev.Delete(ctx, name)
		if err != nil {
			return err
		}

		// share below: factor out.
		p.log.LogAttrs(ctx, slog.LevelDebug, "notify pages drop")
		notify := make(map[svcConn]struct{})
		for _, module := range p.state[name] {
			for uid := range module {
				conn, _, ok := dev.conn(ctx, uid.Module)
				if !ok {
					return fmt.Errorf("failed to get conn to %s", uid.Module)
				}
				notify[svcConn{uid, conn}] = struct{}{}
			}
		}
		for conn := range notify {
			p.notify[conn] = Notification{Service: conn.uid, Buttons: nil}
			err := conn.Notify(ctx, "state", rpc.NewMessage(kernelUID, Notification{Service: conn.uid}))
			if err != nil {
				p.log.LogAttrs(ctx, slog.LevelError, "failed to notify drop state", slog.Any("uid", conn.uid), slog.Any("error", err))
			}
		}
	}

	for name, buttons := range want {
		page, exists := dev.Page(name)
		if !exists {
			err := dev.NewPage(name)
			if err != nil {
				return err
			}
			page, _ = dev.Page(name)
		}

		for pos, module := range buttons {
			var haveButton, wantButton bool
			for _, actions := range module {
				if len(actions) != 0 {
					wantButton = true
					break
				}
			}
			for _, actions := range p.state[name][pos] {
				if len(actions) != 0 {
					haveButton = true
					break
				}
			}
			switch {
			case !wantButton:
				if haveButton {
					p.log.LogAttrs(ctx, slog.LevelDebug, "stop button", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
					page.Button(pos.row, pos.col).Stop()

					// shared above: factor out.
					p.log.LogAttrs(ctx, slog.LevelDebug, "notify pages drop buttons")
					notify := make(map[svcConn]struct{})
					for uid := range p.state[name][pos] {
						if uid.IsKernel() {
							continue
						}
						conn, _, ok := dev.conn(ctx, uid.Module)
						if !ok {
							return fmt.Errorf("failed to get conn to %s", uid.Module)
						}
						notify[svcConn{uid, conn}] = struct{}{}
					}
					for conn := range notify {
						err := conn.Notify(ctx, "state", rpc.NewMessage(kernelUID, Notification{Service: conn.uid}))
						if err != nil {
							p.log.LogAttrs(ctx, slog.LevelError, "failed to notify drop state", slog.Any("uid", conn.uid), slog.Any("error", err))
						}
					}
				}
			case reflect.DeepEqual(p.state[name][pos], module):
				p.log.LogAttrs(ctx, slog.LevelDebug, "no change", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
			default:
				if haveButton {
					p.log.LogAttrs(ctx, slog.LevelDebug, "stop button", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
					page.Button(pos.row, pos.col).Stop()
				}

				var press, release []func(ctx context.Context, page string, row, col int, t time.Time) error
				for uid, actions := range module {
					var (
						conn rpc.Connection
						ok   bool
					)
					if !uid.IsKernel() {
						conn, _, ok = dev.conn(ctx, uid.Module)
						if !ok {
							return fmt.Errorf("failed to get conn to %s", uid.Module)
						}
						note := p.notify[svcConn{uid, conn}]
						note.Service = uid
						note.Buttons = append(note.Buttons, actions...)
						p.notify[svcConn{uid, conn}] = note
					}
					for _, a := range actions {
						a := a // TODO: Remove this when loopvar behaviour is changed in go1.22.
						if a.Change == nil {
							// Draw image if it exists.
							if a.Image != "" {
								p.drawImage(ctx, dev, rpc.NewMessage(uid, DrawMessage{
									Page: a.Page, Row: a.Row, Col: a.Col, Image: a.Image,
								}))
							}
							continue
						}
						do := func(ctx context.Context, page string, row, col int, t time.Time) error {
							// Draw image if it exists.
							if a.Image != "" {
								p.drawImage(ctx, dev, rpc.NewMessage(uid, DrawMessage{
									Page: a.Page, Row: a.Row, Col: a.Col, Image: a.Image,
								}))
							}
							if a.Do != nil && *a.Do != "" {
								if uid.IsKernel() {
									// Button's service belongs to kernel, so
									// this is a direct kernel call.
									return p.doKernel(ctx, *a.Do, dev, rpc.NewMessage(uid, a.Args))
								}
								return conn.Notify(ctx, *a.Do, rpc.NewMessage(kernelUID, a.Args))
							}
							return nil
						}
						switch *a.Change {
						case "press":
							press = append(press, do)
						case "release":
							release = append(release, do)
						default:
							return fmt.Errorf("invalid state change: %s", *a.Change)
						}
					}
				}

				p.log.LogAttrs(ctx, slog.LevelDebug, "restart button", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
				page.Button(pos.row, pos.col).OnPress(bundleActions(press))
				page.Button(pos.row, pos.col).OnRelease(bundleActions(release))
			}
		}
	}

	p.last = p.reqs
	p.state = want

	p.log.LogAttrs(ctx, slog.LevelDebug, "notify pages")
	for conn, note := range p.notify {
		p.log.LogAttrs(ctx, slog.LevelDebug, "notify service", slog.Any("notification", note))
		err := conn.Notify(ctx, "state", rpc.NewMessage(kernelUID, note))
		if err != nil {
			p.log.LogAttrs(ctx, slog.LevelError, "failed to notify state", slog.Any("uid", conn.uid), slog.Any("error", err))
		}
	}

	return nil
}

func (p *pageManager) drawImage(ctx context.Context, dev device, draw *rpc.Message[DrawMessage]) {
	err := p.doKernel(ctx, "draw", dev, draw)
	if err != nil {
		p.log.LogAttrs(ctx, slog.LevelError, "draw image handle", slog.Any("error", err), slog.Int("row", draw.Body.Row), slog.Int("col", draw.Body.Col), slog.String("page", draw.Body.Page), slog.String("image", draw.Body.Image))
	}
}

func (p *pageManager) doKernel(ctx context.Context, method string, dev device, msg any) error {
	req, err := jsonrpc2.NewNotification(method, msg)
	if err != nil {
		p.log.LogAttrs(ctx, slog.LevelDebug, "do kernel notification", slog.Any("error", err), slog.Any("message", msg))
		return err
	}
	_, err = dev.handle(ctx, req)
	return err
}

// bundleActions wraps a set of functions into a calling loop, returning a
// joined error from all calls.
func bundleActions(funcs []func(ctx context.Context, page string, row, col int, t time.Time) error) func(ctx context.Context, page string, row, col int, t time.Time) error {
	var fn func(ctx context.Context, page string, row, col int, t time.Time) error
	switch len(funcs) {
	case 0:
		// nil
	case 1:
		fn = funcs[0]
	default:
		fn = func(ctx context.Context, page string, row, col int, t time.Time) error {
			var errs []error
			for _, do := range funcs {
				err := do(ctx, page, row, col, t)
				if err != nil {
					errs = append(errs, err)
				}
			}
			return errors.Join(errs...)
		}
	}
	return fn
}

// Notification is an RPC message type sent to services when buttons have had
// their state changed by the Manager.
type Notification struct {
	Service rpc.UID         `json:"uid"`
	Buttons []config.Button `json:"buttons"`
}

// getState returns a device state goal based on a set of requests, the list of
// pages, and the default page name.
func getState(state map[string]map[pos]map[rpc.UID][]config.Button, reqs map[string][]sendToRequest, pages []string, deflt string) map[string]map[pos]map[rpc.UID][]config.Button {
	if reqs == nil {
		return state
	}

	validPage := make(map[string]bool, len(pages))
	for _, p := range pages {
		if p == "" {
			p = deflt
		}
		validPage[p] = true
	}

	want := cloneState(state)
	for name, req := range reqs {
		for _, r := range req {
			for _, module := range want[name] {
				if len(r.Actions) == 0 || !validPage[name] {
					delete(module, r.Service)
					continue
				}
			}
			for _, a := range r.Actions {
				actions, ok := want[a.Page]
				if !ok {
					actions = make(map[pos]map[rpc.UID][]config.Button)
					want[a.Page] = actions
				}
				svc, ok := actions[pos{a.Row, a.Col}]
				if !ok {
					svc = make(map[rpc.UID][]config.Button)
					actions[pos{a.Row, a.Col}] = svc
				}
				svc[r.Service] = append(svc[r.Service], a)
			}
		}
	}
	for name, page := range want {
		if !validPage[name] {
			delete(want, name)
			continue
		}
		for pos, module := range page {
			if len(module) == 0 {
				delete(page, pos)
				continue
			}
			for svc, actions := range module {
				module[svc] = unique(actions)
			}
		}
		if len(page) == 0 {
			delete(want, name)
		}
	}
	return want
}

// cloneState returns a deep copy of orig.
func cloneState(orig map[string]map[pos]map[rpc.UID][]config.Button) map[string]map[pos]map[rpc.UID][]config.Button {
	if orig == nil {
		return make(map[string]map[pos]map[rpc.UID][]config.Button)
	}
	state := maps.Clone(orig)
	for name, page := range state {
		page := maps.Clone(page)
		for pos, module := range page {
			module := maps.Clone(module)
			for svc, actions := range module {
				// Fields in elements of actions must not be mutated
				// in the resulting clone as they may be shared.
				module[svc] = slices.Clone(actions)
			}
			page[pos] = module
		}
		state[name] = page
	}
	return state
}

// unique returns paths lexically sorted in ascending order and with repeated
// and nil elements omitted.
func unique(actions []config.Button) []config.Button {
	if len(actions) < 2 {
		return actions
	}
	sort.Sort(lexicalButtons(actions))
	var zero config.Button
	curr := 0
	for i, a := range actions {
		if reflect.DeepEqual(a, actions[curr]) {
			continue
		}
		curr++
		if curr < i {
			actions[curr], actions[i] = actions[i], zero
		}
	}
	// Remove any zero actions.
	var s int
	for i, a := range actions {
		if a != zero {
			s = i
			break
		}
	}
	return actions[s : curr+1]
}

// lexicalButtons sorts a slice of config.Button lexically.
type lexicalButtons []config.Button

func (l lexicalButtons) Len() int      { return len(l) }
func (l lexicalButtons) Swap(i, j int) { l[i], l[j] = l[j], l[i] }
func (l lexicalButtons) Less(i, j int) bool {
	bi := l[i]
	bj := l[j]
	switch {
	case bi.Row < bj.Row:
		return true
	case bi.Row > bj.Row:
		return false
	}
	switch {
	case bi.Col < bj.Col:
		return true
	case bi.Col > bj.Col:
		return false
	}
	switch {
	case bi.Page < bj.Page:
		return true
	case bi.Page > bj.Page:
		return false
	}
	switch {
	case bi.Do == nil && bj.Do != nil:
		return true
	case bi.Do != nil && bj.Do == nil:
		return false
	case bi.Do == nil && bj.Do == nil:
		break
	case *bi.Do < *bj.Do:
		return true
	case *bi.Do > *bj.Do:
		return false
	}
	ai := mustJSON(bi.Args)
	aj := mustJSON(bj.Args)
	switch {
	case ai < aj:
		return true
	case ai > aj:
		return false
	}
	return bi.Image < bj.Image
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
