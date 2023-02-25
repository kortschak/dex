// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package plugins

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"time"

	// For gopher image data.
	_ "embed"

	"github.com/kortschak/dex/internal/animation"
)

// gopher displays a togglable dancing gopher.
type gopher struct {
	Plugin

	gopher image.Image
	black  image.Image

	on bool
}

//go:embed gopher-dance-long.gif
var imgData []byte

func (p *gopher) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	p.Plugin = plugin

	var err error
	p.gopher, err = animation.DecodeGIF(bytes.NewReader(imgData))
	if err != nil {
		return nil, err
	}
	if gopher, ok := p.gopher.(*animation.GIF); ok {
		gopher.Cache = animation.NewCache(plugin.dev)
	}
	b, err := plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	p.black, err = plugin.dev.RawImage(Swatch(b, color.Black))
	return p.black, err
}

func (p *gopher) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	if !p.on {
		p.on = true
		p.button.Draw(ctx, p.gopher)
	} else {
		p.on = false
	}
	return nil
}

func (p *gopher) Release(ctx context.Context, page string, r, c int, t time.Time) error {
	if !p.on {
		p.button.Draw(ctx, p.black)
	}
	return nil
}
