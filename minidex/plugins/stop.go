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

// stop is a minidex exit button.
type stop struct {
	Plugin
}

func (*stop) Init(ctx context.Context, plugin Plugin) (image.Image, error) {
	bounds, err := plugin.dev.Bounds()
	if err != nil {
		return nil, err
	}
	pal := color.Palette{color.Black, color.White}
	text, err := animation.Text("stop").GIF(bounds, pal, 1, 0)
	if err != nil {
		return nil, err
	}
	return text, err
}

func (*stop) Press(ctx context.Context, page string, r, c int, t time.Time) error {
	cancel, ok := ctx.Value("cancel").(context.CancelFunc)
	if ok {
		cancel()
	}
	return nil
}
