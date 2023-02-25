// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package animation provides animated image support.
package animation

import (
	"context"
	"image"

	"golang.org/x/image/draw"
)

// Animator is an image that can animate frames.
type Animator interface {
	// Animate renders the frames into dst and calls
	// fn on each rendered frame.
	Animate(ctx context.Context, dst draw.Image, fn func(image.Image) error) error
	image.Image
}
