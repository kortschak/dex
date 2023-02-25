// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"log/slog"
	"time"
	"unsafe"

	"github.com/kortschak/dex/internal/animation"
	"github.com/kortschak/dex/rpc"
)

// brightness is a deck brightness adjusting plugin.
type logging struct {
	Plugin

	add slog.Level

	pal    color.Palette
	bounds image.Rectangle
	text   *animation.GIF
}

const sizeUint64 = unsafe.Sizeof(uint64(0))

func (p *logging) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	const rng = slog.LevelError - slog.LevelDebug
	var text string
	switch {
	case p.add <= -rng, rng <= p.add:
		return nil, errors.New("invalid logging change")
	case p.add < 0:
		text = "more log"
	case p.add > 0:
		text = "less log"
	default:
		return nil, errors.New("invalid logging change")
	}

	serial := plugin.dev.Serial()
	p.Plugin = plugin
	var val []byte
	val, err := p.store.Get(rpc.UID{Module: "logging", Service: serial}, "level")
	if err != nil {
		return nil, err
	}
	level := plugin.level.Level()
	if len(val) == int(sizeUint64) {
		level = slog.Level(binary.LittleEndian.Uint64(val))
	}
	p.level.Set(level)
	var buf [sizeUint64]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(level))
	err = p.store.Set(rpc.UID{Module: "logging", Service: serial}, "level", buf[:])
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

func (p *logging) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	serial := p.dev.Serial()
	val, err := p.store.Get(rpc.UID{Module: "logging", Service: serial}, "level")
	if err != nil {
		return err
	}
	if len(val) != int(sizeUint64) {
		return fmt.Errorf("invalid logging length: %d", len(val))
	}
	level := slog.Level(binary.LittleEndian.Uint64(val)) + p.add
	if level < slog.LevelDebug {
		level = slog.LevelDebug
	}
	if level > slog.LevelError {
		level = slog.LevelError
	}
	p.level.Set(level)

	text, err := animation.Text(fmt.Sprint(level)).GIF(p.bounds, p.pal, 1, 0)
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

	binary.LittleEndian.PutUint64(val, uint64(level))
	return p.store.Set(rpc.UID{Module: "logging", Service: serial}, "level", val)
}
