// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package device provides types for managing a dex device.
package device

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kortschak/ardilla"
	"github.com/kortschak/jsonrpc2"
	"golang.org/x/exp/slices"

	"github.com/kortschak/dex/internal/animation"
	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"
)

// DefaultPage is the initial name of the default page.
const DefaultPage = "default"

// Controller is an event-driven El Gato device controller.
type Controller struct {
	deck   locked
	kernel sys.Kernel
	log    *slog.Logger

	rows, cols int

	pMu         sync.Mutex
	displayed   Page
	pages       map[string]Page
	current     string
	defaultPage string

	sleeping    atomic.Bool
	state       state
	cancelSleep context.CancelFunc

	cancel context.CancelFunc

	model  ardilla.PID
	serial string
}

// state represents a sleep/wake state.
type state int

const (
	awake = iota
	cleared
	blanked
)

// locked is a lock-protected [ardilla.Deck].
type locked struct {
	mu sync.Mutex
	*ardilla.Deck
}

// Resets the Stream Deck, clearing all button images and showing the standby
// image.
func (d *locked) Reset() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Deck.Reset()
}

// ResetKeyStream sends a blank key report to the Stream Deck, resetting the
// key image streamer in the device. This prevents previously started partial
// writes from corrupting images sent later.
func (d *locked) ResetKeyStream() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Deck.ResetKeyStream()
}

// SetBrightness sets the global screen brightness of the Stream Deck, across
// all the device's buttons.
func (d *locked) SetBrightness(percent int) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Deck.SetBrightness(percent)
}

// SetImage renders the provided image on the button at the given row and
// column. If img is a *RawImage the internal representation will be used
// directly. RawImage values may not be shared between different products.
func (d *locked) SetImage(row, col int, img image.Image) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.Deck.SetImage(row, col, img)
}

// NewController returns a new device controller. The pid and serial parameters
// are interpreted according to the documentation for [ardilla.NewDeck].
func NewController(ctx context.Context, kernel sys.Kernel, pid ardilla.PID, serial string, log *slog.Logger) (*Controller, error) {
	deck, err := ardilla.NewDeck(pid, serial)
	if err != nil {
		return nil, err
	}
	if serial == "" {
		serial, err = deck.Serial()
		if err != nil {
			return nil, err
		}
	}
	rows, cols := deck.Layout()
	ctx, cancel := context.WithCancel(ctx)
	c := &Controller{
		deck:        locked{Deck: deck},
		kernel:      kernel,
		log:         log,
		rows:        rows,
		cols:        cols,
		displayed:   Page{buttons: make([]*Button, rows*cols)},
		current:     DefaultPage,
		defaultPage: DefaultPage,
		cancel:      cancel,
		model:       deck.PID(),
		serial:      serial,
	}
	log.LogAttrs(ctx, slog.LevelInfo, "opened deck", slog.String("pid", fmt.Sprintf("0x%04x", uint16(c.model))), slog.String("model", c.model.String()), slog.String("serial", serial))
	c.displayed.controller = c
	buttons := make([]Button, len(c.displayed.buttons))
	for i := range buttons {
		b := &buttons[i]
		c.displayed.buttons[i] = b
		b.deck = &c.deck
		b.row, b.col = i/cols, i%cols
		b.redraw.Store(c.background(color.Black))
		b.log = log
	}
	c.pages = map[string]Page{DefaultPage: c.displayed}
	c.displayed.Redraw(ctx)
	go c.watchButtons(ctx)
	return c, nil
}

// PID returns the model PID of the device.
func (c *Controller) PID() ardilla.PID {
	return c.model
}

// Serial returns the serial number of the device.
func (c *Controller) Serial() string {
	return c.serial
}

// Conn returns a connection from the controller's kernel to the daemon with
// the given UID.
func (c *Controller) conn(ctx context.Context, uid string) (rpc.Connection, time.Time, bool) {
	return c.kernel.Conn(ctx, uid)
}

// handle performs a direct call to the controller kernel's Handle method.
func (c *Controller) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	return c.kernel.Handle(ctx, req)
}

// id is the global internal RPC ID source.
var id atomic.Int64

// nextKernelID returns the next internal kernel RPC ID.
func nextKernelID() jsonrpc2.ID {
	return jsonrpc2.StringID(fmt.Sprintf("kernel-%d", id.Add(1)))
}

// NewPage inserts a new page into the controller. NewPage returns an error
// if the name already exists.
func (c *Controller) NewPage(name string) error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	if _, exists := c.pages[name]; exists {
		return fmt.Errorf("page %q exists", name)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "new page", slog.String("page", name))
	page := Page{controller: c, buttons: make([]*Button, c.rows*c.cols)}
	buttons := make([]Button, len(page.buttons))
	for i := range buttons {
		b := &buttons[i]
		page.buttons[i] = b
		b.deck = &c.deck
		b.row, b.col = i/c.cols, i%c.cols
		b.redraw.Store(c.background(color.Black))
		b.log = c.log
	}
	c.pages[name] = page
	page.Pause()
	return nil
}

// background returns a uniformly colored image bounded by the size of the
// device's buttons if the device is visual.
func (c *Controller) background(col color.Color) redraw {
	bounds, err := c.deck.Bounds()
	if err != nil {
		return redraw{nil, false}
	}
	return redraw{swatch{&image.Uniform{col}, bounds}, true}
}

// swatch is a subimage of a uniform color.
type swatch struct {
	*image.Uniform
	bounds image.Rectangle
}

func (i swatch) Bounds() image.Rectangle { return i.bounds }

// DefaultName returns the name of the default page.
func (c *Controller) DefaultName() string {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	return c.defaultPage
}

// SetDefaultName sets the name of the default page.
func (c *Controller) SetDefaultName(name string) error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	if _, ok := c.pages[name]; !ok {
		return fmt.Errorf("page %q not found", name)
	}
	c.defaultPage = name
	return nil
}

// PageNames returns a list of the device's page names.
func (c *Controller) PageNames() []string {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	names := make([]string, 0, len(c.pages))
	for n := range c.pages {
		names = append(names, n)
	}
	return names
}

// CurrentPage returns the currently displayed page.
func (c *Controller) CurrentPage() Page {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	return c.displayed
}

// CurrentName returns the name of the currently displayed page.
func (c *Controller) CurrentName() string {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	return c.current
}

// Page returns the named page.
func (c *Controller) Page(name string) (p Page, ok bool) {
	c.pMu.Lock()
	p, ok = c.pages[name]
	c.pMu.Unlock()
	return p, ok
}

// SetDisplayTo sets the current display the named page. SetDisplayTo returns
// an error if the page does not exist.
func (c *Controller) SetDisplayTo(ctx context.Context, name string) error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	if name == c.current {
		return nil
	}
	return c.setDisplayTo(ctx, name)
}

func (c *Controller) setDisplayTo(ctx context.Context, name string) error {
	if name == c.current {
		return nil
	}
	c.log.LogAttrs(ctx, slog.LevelDebug, "set page", slog.String("from", c.current), slog.String("to", name))
	page, ok := c.pages[name]
	if !ok {
		return fmt.Errorf("page %q not found", name)
	}
	c.displayed.Pause()
	c.displayed = page
	c.displayed.Unpause()
	c.displayed.Redraw(ctx)
	c.current = name
	return nil
}

// Delete removes the named page. Delete returns an error if the page is the
// default display. If the named page is the current display, the display is
// set to the default page.
func (c *Controller) Delete(ctx context.Context, name string) error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	if name == c.defaultPage {
		return fmt.Errorf("page %q is default page", name)
	}
	var err error
	if name == c.current {
		err = c.setDisplayTo(ctx, c.defaultPage)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "delete page", slog.String("page", name))
	for _, b := range c.pages[name].buttons {
		b.Stop()
	}
	delete(c.pages, name)
	return err
}

// Rename renames a page from an old name to a new name. Rename returns an
// error if the old name does not exist or the new name already exists.
func (c *Controller) Rename(old, new string) error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	if _, ok := c.pages[old]; !ok {
		return fmt.Errorf("page %q not found", old)
	}
	if _, exists := c.pages[new]; exists {
		return fmt.Errorf("page %q exists", new)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "rename page", slog.String("from", old), slog.String("to", new))
	c.pages[new] = c.pages[old]
	delete(c.pages, old)
	if old == c.current {
		c.current = new
	}
	if old == c.defaultPage {
		c.defaultPage = new
	}
	return nil
}

// Key returns the key number corresponding to the given row and column.
// It panics if row or col are out of bounds.
func (c *Controller) Key(row, col int) int {
	return c.deck.Key(row, col)
}

// Layout returns the number of rows and columns of buttons on the device.
func (c *Controller) Layout() (rows, cols int) {
	return c.rows, c.cols
}

// Bounds returns the image bounds for buttons on the device. If the device
// is not visual an error is returned.
func (c *Controller) Bounds() (image.Rectangle, error) {
	return c.deck.Bounds()
}

// RawImage returns an image.Image has had the internal image representation
// pre-computed after resizing to fit the Deck's button size. The original image
// is retained in the returned image.
func (c *Controller) RawImage(img image.Image) (*ardilla.RawImage, error) {
	return c.deck.RawImage(img)
}

// ResetKeyStream sends a blank key report to the Stream Deck, resetting the
// key image streamer in the device. This prevents previously started partial
// writes from corrupting images sent later.
func (c *Controller) ResetKeyStream() error {
	return c.deck.ResetKeyStream()
}

// Resets the Stream Deck, clearing all button images and showing the standby
// image.
func (c *Controller) Reset() error {
	return c.deck.Reset()
}

// SetBrightness sets the global screen brightness of the Stream Deck, across
// all the device's buttons.
func (c *Controller) SetBrightness(percent int) error {
	return c.deck.SetBrightness(percent)
}

// Wake unpauses the current page and redraws it.
func (c *Controller) Wake(ctx context.Context) {
	c.log.LogAttrs(ctx, slog.LevelDebug, "wake", slog.Bool("sleeping", c.sleeping.Load()))
	if !c.sleeping.Load() {
		return
	}
	c.cancelSleep()
	c.pMu.Lock()
	c.displayed.Unpause()
	c.displayed.Redraw(ctx)
	c.sleeping.Store(false)
	c.state = awake
	c.pMu.Unlock()
}

// GoUntilWake runs fn in a separate goroutine. The context.Context passed to
// fn is cancelled when Wake is called. The function should return when the
// context is cancelled.
func (c *Controller) GoUntilWake(ctx context.Context, fn func(ctx context.Context)) {
	ctx, c.cancelSleep = context.WithCancel(ctx)
	go fn(ctx)
}

// Blank pauses the current page and blanks it.
func (c *Controller) Blank() error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "blank", slog.Bool("blanked", c.state == blanked))
	if c.state == blanked {
		return nil
	}
	c.sleeping.Store(true)
	c.state = blanked
	c.displayed.Pause()
	return c.displayed.blank()
}

// blank blanks all buttons on the page. It is not c.pMu locked and does
// not replace the buttons' stored images.
func (p Page) blank() error {
	var img image.Image
	for i, b := range p.buttons {
		if i == 0 {
			bounds, err := b.deck.Bounds()
			if err != nil {
				return err
			}
			img = swatch{Uniform: &image.Uniform{C: color.Black}, bounds: bounds}
			img, err = b.deck.RawImage(img)
			if err != nil {
				return err
			}
		}
		err := b.deck.SetImage(b.row, b.col, img)
		if err != nil {
			return err
		}
	}
	return nil
}

// Clear pauses the current page and clears it.
func (c *Controller) Clear() error {
	c.pMu.Lock()
	defer c.pMu.Unlock()
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "clear", slog.Bool("cleared", c.state == cleared))
	if c.state == cleared {
		return nil
	}
	c.sleeping.Store(true)
	c.state = cleared
	c.displayed.Pause()
	return c.Reset()
}

// Close resets and closes the device.
func (c *Controller) Close() error {
	c.cancel()
	c.deck.Reset()
	return c.deck.Close()
}

// watchButtons waits on button press events and call's the corresponding
// button's functions registered by OnPress and OnRelease as appropriate.
func (c *Controller) watchButtons(ctx context.Context) {
	log := c.log.WithGroup("watch buttons")
	log.LogAttrs(ctx, slog.LevelDebug, "start")

	type buttonEvents struct {
		buttons []bool
		time    time.Time
	}
	s := make(chan buttonEvents)
	go func() {
		for {
			states, err := c.deck.KeyStates()
			if err != nil {
				if err == io.EOF {
					log.LogAttrs(ctx, slog.LevelDebug, "key states closed")
					return
				}
				select {
				case <-ctx.Done():
					log.LogAttrs(ctx, slog.LevelDebug, "watch buttons cancelled")
					return
				default:
				}
				log.LogAttrs(ctx, slog.LevelError, "failed to get states", slog.Any("error", err))
				continue
			}
			s <- buttonEvents{buttons: states, time: time.Now()}
		}
	}()

	last := buttonEvents{buttons: make([]bool, c.rows*c.cols)}
	var woke bool
	for {
		select {
		case <-ctx.Done():
			log.LogAttrs(ctx, slog.LevelDebug, "stop")
			return
		case states := <-s:

			// Wake and discard events if we are sleeping. Also discard
			// the release that follows a waking press.
			if c.sleeping.Load() || woke {
				if slices.Contains(states.buttons, true) {
					if !woke {
						c.log.LogAttrs(ctx, slog.LevelDebug, "wake", slog.String("reason", "button"))
						woke = true
						c.Wake(ctx)
					}
					continue
				}
				c.log.LogAttrs(ctx, slog.LevelDebug, "ignoring release during wake")
				woke = false
				continue
			}

			for i, s := range states.buttons {
				if s != last.buttons[i] {
					var (
						action string
						do     func(ctx context.Context, page string, row, col int, t time.Time) error
					)

					c.pMu.Lock()
					b := c.displayed.buttons[i]
					page := c.current
					c.pMu.Unlock()
					b.mu.Lock()
					row, col := b.row, b.col
					if s {
						action = "press"
						do = b.onPress
					} else {
						action = "release"
						do = b.onRelease
					}
					b.mu.Unlock()

					p := slog.String("page", page)
					r := slog.Int("row", row)
					c := slog.Int("col", col)
					t := slog.Time("triggered", states.time)
					log.LogAttrs(ctx, slog.LevelDebug, action, p, r, c, t)
					if do != nil {
						action = "running on-" + action
						log.LogAttrs(ctx, slog.LevelDebug, action, p, r, c)
						err := do(ctx, page, row, col, states.time)
						if err != nil {
							log.LogAttrs(ctx, slog.LevelError, action, p, r, c, slog.Any("error", err))
						}
					}
				}
			}
			last = states
		}
	}
}

// Page is a collection of buttons displaying at the same time.
type Page struct {
	controller keyer
	buttons    []*Button
}

type keyer interface {
	Key(row, col int) int
}

// Button returns the button at the provided position.
// It panics if row or col are out of bounds.
func (p Page) Button(row, col int) *Button {
	return p.buttons[p.controller.Key(row, col)]
}

// Pause pauses a page's draw operations.
func (p Page) Pause() {
	for i := range p.buttons {
		p.buttons[i].Pause()
	}
}

// Unpause unpauses a pages's paused draw operations.
func (p Page) Unpause() {
	for i := range p.buttons {
		p.buttons[i].Unpause()
	}
}

// Redraw redraws all still images.
func (p Page) Redraw(ctx context.Context) {
	for i := range p.buttons {
		p.buttons[i].Redraw(ctx)
	}
}

// Button is an interface to an individual button on a device.
type Button struct {
	deck     *locked
	row, col int
	log      *slog.Logger

	// mu protects onPress, onRelease and cancelAnimation.
	mu sync.Mutex

	// onPress and onRelease are the action to perform
	// when the button is pressed or released.
	onPress   func(ctx context.Context, page string, row, col int, t time.Time) error
	onRelease func(ctx context.Context, page string, row, col int, t time.Time) error

	cancelAnimation func() // last cancellable image cancellation.

	// pauser controls animation and drawing pauses.
	pauser pauser

	// dMu protects drawing operations.
	dMu sync.Mutex

	// redraw is the image to draw when the button
	// is made active and whether a redraw is needed
	// when Redraw is called.
	redraw atomicRedraw
}

type redraw struct {
	// img is the image to redraw.
	img image.Image
	// needRedraw is whether a redraw is needed.
	needRedraw bool
}

type atomicRedraw struct {
	v atomic.Value
}

func (x *atomicRedraw) Load() redraw {
	v, _ := x.v.Load().(redraw)
	return v
}

func (x *atomicRedraw) Store(v redraw) {
	x.v.Store(v)
}

// OnPress registers a function to call when the button is pressed. The
// function must not block.
func (b *Button) OnPress(do func(ctx context.Context, page string, row, col int, t time.Time) error) {
	b.mu.Lock()
	b.onPress = do
	b.mu.Unlock()
}

// OnRelease registers a function to call when the button is releases. The
// function must not block.
func (b *Button) OnRelease(do func(ctx context.Context, page string, row, col int, t time.Time) error) {
	b.mu.Lock()
	b.onRelease = do
	b.mu.Unlock()
}

// Draw draws the provided image to the button using the [ardilla.Deck.SetImage]
// method. If the image satisfies [animation.Animator] the animation will be
// run on the button's screen.
func (b *Button) Draw(ctx context.Context, img image.Image) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.cancelAnimation != nil {
		b.cancelAnimation()
	}
	switch img := img.(type) {
	case animation.Animator:
		ctx, b.cancelAnimation = context.WithCancel(ctx)
		b.dMu.Lock()
		// Animations do not need a redraw since the animation
		// goroutine will be paused when swapped out, unless...
		b.redraw.Store(redraw{img, false}) // img is stored for Button.Image.
		go func() {
			defer b.dMu.Unlock()
			dst := image.NewRGBA(img.Bounds())
			err := img.Animate(ctx, dst, func(img image.Image) error {
				err := b.pauser.wait(ctx)
				if err != nil {
					return err
				}
				b.mu.Lock()
				err = b.deck.SetImage(b.row, b.col, img)
				b.mu.Unlock()
				if err != nil {
					return err
				}
				return ctx.Err()
			})
			if err == nil {
				/// ... they are terminating. In which case we
				// grab a raw image to prevent reanimating.
				img, err := b.deck.RawImage(img)
				if err != nil {
					b.log.LogAttrs(ctx, slog.LevelError, "animation last frame", slog.Int("row", b.row), slog.Int("col", b.col), slog.Any("error", err))
					return
				}
				b.redraw.Store(redraw{img, true})
			} else if !errors.Is(err, context.Canceled) {
				b.log.LogAttrs(ctx, slog.LevelError, "animation", slog.Int("row", b.row), slog.Int("col", b.col), slog.Any("error", err))
			}
		}()
	default:
		b.cancelAnimation = nil
		err := b.pauser.wait(ctx)
		if err != nil {
			return
		}
		img, err = b.deck.RawImage(img)
		if err != nil {
			b.log.LogAttrs(ctx, slog.LevelError, "make raw image", slog.Int("row", b.row), slog.Int("col", b.col), slog.Any("error", err))
		}
		b.dMu.Lock()
		err = b.deck.SetImage(b.row, b.col, img)
		b.dMu.Unlock()
		b.redraw.Store(redraw{img, true})
		if err != nil {
			b.log.LogAttrs(ctx, slog.LevelError, "set image", slog.Int("row", b.row), slog.Int("col", b.col), slog.Any("error", err))
		}
	}
}

// Stop stops a button, releasing any resources it holds.
func (b *Button) Stop() {
	b.mu.Lock()
	b.Pause()
	b.onPress = nil
	b.onRelease = nil
	if b.cancelAnimation != nil {
		b.cancelAnimation()
	}
	b.redraw.Store(redraw{nil, false})
	b.Unpause()
	b.mu.Unlock()
}

// Pause pauses a button's draw operations.
func (b *Button) Pause() {
	b.pauser.pause()
}

// Unpause unpauses a button's paused draw operations.
func (b *Button) Unpause() {
	b.pauser.unpause()
}

// Redraw redraws the buttons image if required.
func (b *Button) Redraw(ctx context.Context) {
	redraw := b.redraw.Load()
	if !redraw.needRedraw {
		return
	}
	b.Draw(ctx, redraw.img)
}

// Image returns the buttons image.
func (b *Button) Image() image.Image {
	return b.redraw.Load().img
}

// pauser is a pause sentinel. The zero value for a pauser is in the unpaused
// state.
type pauser struct {
	paused atomic.Bool
	mu     sync.Mutex
	signal chan struct{}
}

// pause puts the receiver into the paused state.
func (p *pauser) pause() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.paused.CompareAndSwap(false, true) {
		p.signal = make(chan struct{})
	}
}

// unpause puts the receiver into the unpaused state.
func (p *pauser) unpause() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.paused.CompareAndSwap(true, false) {
		close(p.signal)
	}
}

// wait will block when the receiver is in the paused state unless the
// context has been cancelled. It returns the value of ctx.Err() if
// context cancellation is the reason for unblocking.
func (p *pauser) wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	} else if !p.paused.Load() {
		return nil
	}

	p.mu.Lock()
	signal := p.signal
	paused := p.paused.Load()
	p.mu.Unlock()

	if !paused {
		return nil
	}

	select {
	case <-signal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
