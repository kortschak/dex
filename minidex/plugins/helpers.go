// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"time"

	"github.com/kortschak/dex/internal/device"
	"github.com/kortschak/dex/internal/state"
)

// Device is a plugin table device key.
type Device struct {
	Model, Serial string
}

// Page is a plugin table page key.
type Page string

// Button is a plugin table button key.
type Button struct {
	Row, Col int
}

// Action is a plugin definition.
type Action interface {
	Init(context.Context, Plugin) (image.Image, error)
}

type Presser interface {
	Press(ctx context.Context, page string, r, c int, t time.Time) error
}

type Releaser interface {
	Release(ctx context.Context, page string, r, c int, t time.Time) error
}

// Plugin holds system state useful for a plugin.
type Plugin struct {
	dev    *device.Controller
	button *device.Button
	store  *state.DB
	log    *slog.Logger
	level  *slog.LevelVar
}

// New returns a new initialised plugin.
func New(dev *device.Controller, button *device.Button, store *state.DB, log *slog.Logger, level *slog.LevelVar) Plugin {
	return Plugin{
		dev:    dev,
		button: button,
		store:  store,
		log:    log,
		level:  level,
	}
}

// Swatch returns a bounded uniform color image.
func Swatch(rect image.Rectangle, col color.Color) image.Image {
	return swatch{Uniform: &image.Uniform{C: col}, Rect: rect}
}

type swatch struct {
	*image.Uniform
	Rect image.Rectangle
}

func (i swatch) Bounds() image.Rectangle { return i.Rect }
