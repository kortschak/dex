// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"time"

	"github.com/kortschak/dex/internal/animation"
	"github.com/kortschak/dex/internal/state"
)

// command is a arbitrary command button.
type dump struct {
	Plugin

	pal    color.Palette
	bounds image.Rectangle
	text   *animation.GIF
}

func (p *dump) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	p.Plugin = plugin

	bounds, err := plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	pal := color.Palette{color.Black, color.White}
	p.text, err = animation.Text("dump").GIF(bounds, pal, 1, 0)
	if err != nil {
		return nil, err
	}
	return p.text, err
}

func (p *dump) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	db, err := p.store.Dump()
	if err != nil {
		p.error(ctx, err)
		return err
	}
	var msg []byte
	msg, err = state.JSON(db)
	if err != nil {
		p.error(ctx, err)
		err = errors.Join(err)
		return err
	}
	var buf bytes.Buffer
	json.Indent(&buf, msg, "", "\t")
	fmt.Println(&buf)
	return nil
}

func (p *dump) error(ctx context.Context, err error) {
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
}
