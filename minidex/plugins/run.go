// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"log/slog"
	"os"
	"time"

	// For errorGIF.
	_ "embed"

	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/sys/execabs"

	"github.com/kortschak/dex/internal/animation"
	"github.com/kortschak/dex/internal/text"
)

// https://craftpix.net/freebies/free-animated-explosion-sprite-pack/
// https://craftpix.net/file-licenses/
//
//go:embed error.gif
var errorGIF []byte

// command is a arbitrary command button.
type command struct {
	Plugin

	name string
	cmd  string
	args []string
	icon string

	pal    color.Palette
	bounds image.Rectangle

	image    image.Image
	errorGIF *animation.GIF
}

func (p *command) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	if p.name == "" {
		p.name = p.cmd
	}
	p.Plugin = plugin

	var err error
	p.bounds, err = plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	p.pal = color.Palette{color.Black, color.White}
	if p.icon != "" {
		f, err := os.Open(p.icon)
		if err != nil {
			goto text
		}
		defer f.Close()
		gopher, _, err := image.Decode(f)
		if err != nil {
			goto text
		}
		dst := text.Outlined[*image.RGBA]{
			Text:         image.NewRGBA(p.bounds),
			Background:   image.NewRGBA(p.bounds),
			OutlineColor: color.White,
		}
		draw.BiLinear.Scale(dst.Background, text.KeepAspectRatio(dst, gopher), gopher, gopher.Bounds(), draw.Src, nil)
		text.Draw(text.Shrink{Image: dst, Margin: 1}, p.name, color.Black, basicfont.Face7x13, 0.5, 0.9, true)
		p.image = dst
	}
text:
	if p.image == nil {
		p.image, err = animation.Text(p.name).GIF(p.bounds, p.pal, 1, 0)
		if err != nil {
			return nil, err
		}
	}

	bang, err := animation.DecodeGIF(bytes.NewReader(errorGIF))
	if err != nil {
		return nil, err
	}
	var ok bool
	p.errorGIF, ok = bang.(*animation.GIF)
	if !ok {
		return nil, errors.New("error GIF is not an animation")
	}
	p.errorGIF.Cache = animation.NewCache(plugin.dev)

	return p.image, err
}

func (p *command) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	go func() {
		err := execabs.CommandContext(ctx, p.cmd, p.args...).Run()
		if err == nil {
			return
		}
		text, err := animation.Text(err.Error()).GIF(p.bounds, p.pal, 1, 0)
		if err != nil {
			p.log.LogAttrs(ctx, slog.LevelError, "draw error failed", slog.Any("error", err))
		}
		text.LoopCount = -1 // Only loop through frames once.
		if len(text.Delay) == 1 {
			text.Delay[0] = 300
		}
		p.button.Draw(ctx, &animation.Images{
			Image:     []image.Image{p.errorGIF, text, p.image},
			LoopCount: -1,
			Delay:     time.Second / 10,
		})
	}()
	return nil
}
