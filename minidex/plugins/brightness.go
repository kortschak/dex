// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"time"

	"github.com/kortschak/dex/internal/animation"
	"github.com/kortschak/dex/rpc"
)

// brightness is a deck brightness adjusting plugin.
type brightness struct {
	Plugin

	add int

	pal    color.Palette
	bounds image.Rectangle
	text   *animation.GIF
}

func (p *brightness) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	var text string
	switch {
	case p.add <= -100, 100 <= p.add:
		return nil, errors.New("invalid brightness change")
	case p.add < 0:
		text = "darker"
	case p.add > 0:
		text = "brighter"
	default:
		return nil, errors.New("invalid brightness change")
	}

	serial := plugin.dev.Serial()
	p.Plugin = plugin
	var val []byte
	val, err := p.store.Get(rpc.UID{Module: "brightness", Service: serial}, "level")
	if err != nil {
		return nil, err
	}
	bright := 50
	if len(val) == 1 {
		bright = int(val[0])
	}
	err = p.dev.SetBrightness(bright)
	if err != nil {
		return nil, err
	}
	err = p.store.Set(rpc.UID{Module: "brightness", Service: serial}, "level", []byte{byte(bright)})
	if err != nil {
		return nil, err
	}

	p.bounds, err = plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	p.pal = color.Palette{color.Black, color.White}

	p.text, err = animation.Text(text).GIF(p.bounds, p.pal, 1, 0)
	if err != nil {
		return nil, err
	}
	return p.text, err
}

func (p *brightness) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	serial := p.dev.Serial()
	val, err := p.store.Get(rpc.UID{Module: "brightness", Service: serial}, "level")
	if err != nil {
		return err
	}
	if len(val) != 1 {
		return fmt.Errorf("invalid brightness length: %d", len(val))
	}
	bright := int(val[0]) + p.add
	if bright < 0 {
		bright = 0
	}
	if bright > 100 {
		bright = 100
	}
	err = p.dev.SetBrightness(bright)
	if err != nil {
		return err
	}

	text, err := animation.Text(fmt.Sprint(bright)).GIF(p.bounds, p.pal, 1, 0)
	if err != nil {
		return err
	}
	text.Delay[0] = 150
	text.Delay = append(text.Delay, p.text.Delay[0])
	text.Image = append(text.Image, p.text.Image[0])
	if text.Disposal != nil && p.text.Disposal != nil {
		text.Disposal = append(text.Disposal, p.text.Disposal[0])
	}
	text.LoopCount = p.text.LoopCount
	p.button.Draw(ctx, text)

	return p.store.Set(rpc.UID{Module: "brightness", Service: serial}, "level", []byte{byte(bright)})
}
