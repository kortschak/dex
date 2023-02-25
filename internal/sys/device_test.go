// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sys

import (
	"context"
	"fmt"
	"image"
	"log/slog"

	"github.com/kortschak/ardilla"

	"github.com/kortschak/dex/internal/config"
	"github.com/kortschak/dex/rpc"
)

var _ Device[*testButton] = (*testDevice)(nil)

type testDevice struct {
	*recorder
}

func newTestDevice() *testDevice {
	return &testDevice{&recorder{}}
}

func (d *testDevice) newDevice(ctx context.Context, pid ardilla.PID, serial string, kernel *testKernel, log *slog.Logger) (*testDevice, error) {
	d.log = log.With(slog.String("component", kernelUID.String()))
	return d, nil
}

func (d *testDevice) Serial() string {
	d.addAction("serial")
	return "testdevice"
}

func (d *testDevice) SetPages(_ context.Context, deflt *string, pages []string) error {
	return d.addAction("set pages", deflt, pages)
}

func (d *testDevice) SendTo(service rpc.UID, listen []config.Button) error {
	return d.addAction("send to", service, listen)
}

func (d *testDevice) Layout() (rows, cols int) {
	d.addAction("layout")
	return 1, 1
}

func (d *testDevice) Key(row, col int) int {
	d.addAction("key", row, col)
	return 0
}

func (d *testDevice) CurrentName() string {
	d.addAction("current page")
	return "test_page"
}

func (d *testDevice) Page(name string) (p Page[*testButton], ok bool) {
	d.addAction("page", name)
	return &testPage{name: name, recorder: d.recorder}, true
}

func (d *testDevice) Bounds() (image.Rectangle, error) {
	d.addAction("bounds")
	return image.Rectangle{}, nil
}

func (d *testDevice) RawImage(img image.Image) (*ardilla.RawImage, error) {
	d.addAction("raw image", img.Bounds())
	return &ardilla.RawImage{}, nil
}

func (d *testDevice) SetBrightness(percent int) error {
	d.addAction("set brightness", percent)
	return nil
}

func (d *testDevice) Wake(ctx context.Context) {
	d.addAction("wake")
}

func (d *testDevice) Sleep() error {
	d.addAction("sleep")
	return nil
}

func (d *testDevice) Clear() error {
	d.addAction("clear")
	return nil
}

func (d *testDevice) Close() error {
	return d.addAction("close")
}

var _ Page[*testButton] = (*testPage)(nil)

type testPage struct {
	name string
	*recorder
}

func (p *testPage) Button(row, col int) *testButton {
	p.addAction("button", p.name, row, col)
	return &testButton{testPage: p, row: row, col: col}
}

type testButton struct {
	*testPage
	row, col int
}

func (b *testButton) Draw(ctx context.Context, img image.Image) {
	b.addAction("draw", b.name, b.row, b.col, fmt.Sprintf("%T", img))
}
