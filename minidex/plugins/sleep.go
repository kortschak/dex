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

// sleep is a sleep button.
type sleep struct {
	Plugin

	blankAfter time.Duration
}

func (p *sleep) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	p.Plugin = plugin

	bounds, err := plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	pal := color.Palette{color.Black, color.White}
	text, err := animation.Text("sleep").GIF(bounds, pal, 1, 0)
	if err != nil {
		return nil, err
	}
	return text, err
}

func (p *sleep) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	err := p.dev.Clear()
	if err != nil {
		return err
	}
	p.dev.GoUntilWake(ctx, func(ctx context.Context) {
		t := time.NewTimer(p.blankAfter)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
			err = p.dev.Blank()
			if err != nil {
				p.log.LogAttrs(ctx, slog.LevelError, "blank failed", slog.Any("error", err))
			}
		}
	})
	return nil
}
