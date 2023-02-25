// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"image"
	"image/color"
	"time"

	"github.com/kortschak/dex/internal/animation"
)

// serial displays togglable device serial/model text.
type serial struct {
	Plugin

	pal color.Palette

	serial, model image.Image

	pid bool
}

func (p *serial) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	p.Plugin = plugin

	b, err := plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	p.pal = color.Palette{color.Black, color.White}
	p.serial, err = animation.Text(p.dev.Serial()).GIF(b, p.pal, 1, 0)
	if err != nil {
		return nil, err
	}
	p.model, err = animation.Text(p.dev.PID().String()).GIF(b, p.pal, 1, 0)
	if err != nil {
		return nil, err
	}
	return p.serial, err
}

func (p *serial) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	if p.pid {
		p.button.Draw(ctx, p.serial)
		p.pid = false
	} else {
		p.button.Draw(ctx, p.model)
		p.pid = true
	}
	return nil
}
