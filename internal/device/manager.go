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
	_ sys.NewDevice[*rpc.Kernel, *Manager, *Button] = NewManager

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
func NewManager(ctx context.Context, pid ardilla.PID, serial string, kernel *rpc.Kernel, log *slog.Logger) (*Manager, error) {
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
	current := m.controller.CurrentName() // Save the current page name.
	err := m.pages.setPages(ctx, m.controller, deflt, pages)
	if err != nil {
		return err
	}
	// Attempt to set display back to the previous current page,
	// falling back to the default if it no longer exists.
	err = m.SetDisplayTo(ctx, current)
	if err != nil {
		m.controller.log.LogAttrs(ctx, slog.LevelDebug, "setting page to last current", slog.Any("error", err))
		err = m.SetDisplayTo(ctx, m.controller.DefaultName())
		if err != nil {
			m.controller.log.LogAttrs(ctx, slog.LevelWarn, "setting page to default", slog.Any("error", err))
		}
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

// SleepState returns the current sleep state.
func (m *Manager) SleepState() string {
	return m.controller.SleepState().String()
}

// Last returns the time of the last button press or release. If the returned
// time.Time is zero, no button action has occurred.
func (m *Manager) Last() time.Time {
	return m.controller.Last()
}

// Wake unpauses the current page and redraws it.
func (m *Manager) Wake(ctx context.Context) {
	m.controller.Wake(ctx)
}

// Blank pauses the current page and blanks it.
func (m *Manager) Blank() error {
	return m.controller.Blank()
}

// Clear pauses the current page and clears it to the default image.
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

	state  layout
	notify map[svcConn]Notification
}

// layout is a device button layout:
//
//	page.position.module.[actions]
type layout map[string]map[pos]map[rpc.UID][]config.Button

// pos is a button position on a page.
type pos struct{ row, col int }

func (p pos) isValidFor(dev device) bool {
	rows, cols := dev.Layout()
	return uint(p.row) < uint(rows) && uint(p.col) < uint(cols)
}

// pagePos is a global button position.
type pagePos struct {
	page string
	pos
}

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

// sendTo queues a set of actions to be added to the next device update by
// setPages.
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

	// Construct target layout.
	want := mkLayout(ctx, dev, p.reqs, pages, *deflt, p.log)
	unchanged := intersectLayouts(p.state, want)

	p.notify = make(map[svcConn]Notification) // Retain temporarily for testing.
	// Stop pages that no longer exist in the target layout.
	for name := range p.state {
		if len(want[name]) != 0 {
			continue
		}

		p.log.LogAttrs(ctx, slog.LevelDebug, "delete page", slog.String("page", name))
		err := dev.Delete(ctx, name)
		if err != nil {
			return err
		}

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
		}
	}

	// Stop buttons that are changing or are no longer wanted.
	stopped := make(map[pagePos]bool)
	for name, buttons := range subtractLayout(p.state, unchanged) {
		page, exists := dev.Page(name)
		if !exists {
			// This should never happen.
			p.log.LogAttrs(ctx, slog.LevelWarn, "attempt to remove absent page", slog.String("page", name))
			continue
		}
		for pos, module := range buttons {
			if !stopped[pagePos{name, pos}] {
				p.log.LogAttrs(ctx, slog.LevelDebug, "stop button", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
				page.Button(pos.row, pos.col).Stop()
				stopped[pagePos{name, pos}] = true
			}

			p.log.LogAttrs(ctx, slog.LevelDebug, "notify pages drop buttons")
			notify := make(map[svcConn]struct{})
			for uid := range module {
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
				p.notify[conn] = Notification{Service: conn.uid, Buttons: nil}
			}
		}
	}

	// Start buttons that are new in the layout target.
	for name, buttons := range subtractLayout(want, unchanged) {
		page, exists := dev.Page(name)
		if !exists {
			err := dev.NewPage(name)
			if err != nil {
				return err
			}
			page, _ = dev.Page(name)
		}
		// Prevent the images from being rendered if we
		// are not the active page. This pause will be
		// released for the desired active page by the
		// caller.
		page.Pause()
		for pos, module := range buttons {
			if !stopped[pagePos{name, pos}] {
				p.log.LogAttrs(ctx, slog.LevelDebug, "stop button", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
				page.Button(pos.row, pos.col).Stop()
				stopped[pagePos{name, pos}] = true
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
						// Draw image if it exists, or stop the button if not active.
						if a.Image != "" {
							// We are running as a single locked thread here, so
							// defer the drawing of the button image until after
							// the page is made active.
							go p.drawImage(ctx, dev, rpc.NewMessage(uid, DrawMessage{
								Page: a.Page, Row: a.Row, Col: a.Col, Image: a.Image,
							}))
						} else if !isActive(a) {
							delete(want[name][pos], uid)
							if len(want[name][pos]) == 0 {
								delete(want[name], pos)
							}
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

			if len(press) != 0 || len(release) != 0 {
				p.log.LogAttrs(ctx, slog.LevelDebug, "start button", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
			}
			page.Button(pos.row, pos.col).OnPress(bundleActions(press))
			page.Button(pos.row, pos.col).OnRelease(bundleActions(release))
		}
	}

	for name, buttons := range unchanged {
		for pos, module := range buttons {
			p.log.LogAttrs(ctx, slog.LevelDebug, "no change", slog.String("page", name), slog.Int("row", pos.row), slog.Int("col", pos.col))
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
			}
		}
	}

	p.last = p.reqs
	p.state = want

	p.log.LogAttrs(ctx, slog.LevelDebug, "notify pages")
	for conn, note := range p.notify {
		note.Buttons = unique(note.Buttons)
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

// mkLayout returns a device layout goal based on a set of requests, the list of
// pages, and the default page name.
func mkLayout(ctx context.Context, dev device, reqs map[string][]sendToRequest, pages []string, deflt string, log *slog.Logger) layout {
	validPage := make(map[string]bool, len(pages))
	for _, p := range pages {
		if p == "" {
			p = deflt
		}
		validPage[p] = true
	}
	want := make(layout)
	for name, req := range reqs {
		if !validPage[name] {
			continue
		}
		for _, r := range req {
			for _, a := range r.Actions {
				actions, ok := want[a.Page]
				if !ok {
					actions = make(map[pos]map[rpc.UID][]config.Button)
					want[a.Page] = actions
				}
				p := pos{a.Row, a.Col}
				if !p.isValidFor(dev) {
					rows, cols := dev.Layout()
					log.LogAttrs(ctx, slog.LevelError, "invalid button position", slog.String("page", name), slog.Int("row", p.row), slog.Int("col", p.col), slog.Int("layout_rows", rows), slog.Int("layout_cols", cols))
					continue
				}
				svc, ok := actions[p]
				if !ok {
					svc = make(map[rpc.UID][]config.Button)
					actions[p] = svc
				}
				if isActive(a) {
					svc[r.Service] = append(svc[r.Service], a)
				}
			}
		}
	}
	for _, page := range want {
		for _, module := range page {
			for svc, actions := range module {
				module[svc] = unique(actions)
			}
		}
	}
	return want
}

// intersectLayouts returns the intersection of layouts a and b. Intersection is
// defined based on equality of key path and leaf value.
func intersectLayouts(a, b layout) layout {
	n := make(layout)
	for name := range keyIntersect(a, b) {
		for button := range keyIntersect(a[name], b[name]) {
			actions := intersect(a[name][button], b[name][button])
			if len(actions) == 0 {
				continue
			}
			page, ok := n[name]
			if !ok {
				page = make(map[pos]map[rpc.UID][]config.Button)
				n[name] = page
			}
			// Fields in elements of actions must not be mutated
			// in the resulting clone as they may be shared.
			page[button] = actions
		}
	}
	return n
}

// subtractLayout returns layout a with elements of b removed recursively.
// Subtraction is defined based on key path equality.
func subtractLayout(a, b layout) layout {
	n := make(layout)
	for name, page := range a {
		for button, module := range page {
			for svc, actions := range module {
				_, ok := b[name][button][svc]
				if ok {
					continue
				}
				p, ok := n[name]
				if !ok {
					p = make(map[pos]map[rpc.UID][]config.Button)
					n[name] = p
				}
				m, ok := p[button]
				if !ok {
					m = make(map[rpc.UID][]config.Button)
					p[button] = m
				}
				// Fields in elements of actions must not be mutated
				// in the resulting clone as they may be shared.
				m[svc] = actions
			}
		}
	}
	return n
}

// keyIntersect returns the key set intersection of a and b, a∩b.
func keyIntersect[M ~map[K]V, K comparable, V any](a, b M) map[K]struct{} {
	if len(a) > len(b) {
		a, b = b, a
	}
	n := make(map[K]struct{}, len(a))
	for k := range a {
		n[k] = struct{}{}
	}
	for k := range n {
		if _, ok := b[k]; !ok {
			delete(n, k)
		}
	}
	return n
}

// intersect returns the set intersection of a and b, a∩b. Set elements
// are the concatenation of a key and its corresponding value.
func intersect[M ~map[K]V, K comparable, V any](a, b M) M {
	if len(a) > len(b) {
		a, b = b, a
	}
	n := maps.Clone(a)
	for k, av := range n {
		if bv, ok := b[k]; !ok || !reflect.DeepEqual(av, bv) {
			delete(n, k)
		}
	}
	return n
}

func isActive(b config.Button) bool {
	return (b.Change != nil && b.Do != nil) || b.Image != ""
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
