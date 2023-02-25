// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"image"
	"image/color"
	"log/slog"
	"sync"
	"time"

	"github.com/kortschak/dex/internal/animation"
)

// date displays an updating clock.
type date struct {
	Plugin

	pal    color.Palette
	bounds image.Rectangle

	formats []string
	mu      sync.Mutex
	format  int
	last    string
}

func (p *date) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	p.Plugin = plugin

	var err error
	p.bounds, err = plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	p.pal = color.Palette{color.Black, color.White}
	text, err := animation.Text(time.Now().Format(p.formats[p.format])).GIF(p.bounds, p.pal, 1, 0)
	if err != nil {
		return nil, err
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case t := <-ticker.C:
				p.mu.Lock()
				formatted := t.Format(p.formats[p.format])
				if formatted == p.last {
					p.mu.Unlock()
					continue
				}
				p.last = formatted
				p.mu.Unlock()
				text, err := animation.Text(formatted).GIF(p.bounds, p.pal, 1, 0)
				if err != nil {
					p.log.LogAttrs(ctx, slog.LevelError, "draw date failed", slog.Any("error", err))
				}
				p.button.Draw(ctx, text)
			}
		}
	}()
	return text, err
}

func (p *date) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	p.mu.Lock()
	p.format = (p.format + 1) % len(p.formats)
	format := p.formats[p.format]
	p.mu.Unlock()
	text, err := animation.Text(t.Format(format)).GIF(p.bounds, p.pal, 1, 0)
	if err != nil {
		p.log.LogAttrs(ctx, slog.LevelError, "draw date failed", slog.Any("error", err))
	}
	p.button.Draw(ctx, text)
	return nil
}
