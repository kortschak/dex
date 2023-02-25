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

	"github.com/kortschak/dex/internal/animation"
)

// change displays togglable device change/model text.
type change struct {
	Plugin

	toPage string

	pal    color.Palette
	bounds image.Rectangle
	text   *animation.GIF
}

func (p *change) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	p.Plugin = plugin

	var err error
	p.bounds, err = plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	p.pal = color.Palette{color.Black, color.White}
	p.text, err = animation.Text(p.toPage).GIF(p.bounds, p.pal, 1, 0)
	if err != nil {
		return nil, err
	}
	return p.text, err
}

func (p *change) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	err := p.dev.SetDisplayTo(ctx, p.toPage)
	if err == nil {
		return nil
	}
	text, err := animation.Text(err.Error()).GIF(p.bounds, p.pal, 1, 0)
	if err != nil {
		p.log.LogAttrs(ctx, slog.LevelError, "draw error failed", slog.Any("error", err))
	}
	if len(text.Delay) == 1 {
		text.Delay[0] = 200
	}
	text.Delay = append(text.Delay, p.text.Delay[0])
	text.Image = append(text.Image, p.text.Image[0])
	if text.Disposal != nil && p.text.Disposal != nil {
		text.Disposal = append(text.Disposal, p.text.Disposal[0])
	}
	text.LoopCount = p.text.LoopCount
	p.button.Draw(ctx, text)
	return err
}
