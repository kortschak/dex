// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package animation

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"time"

	"golang.org/x/image/draw"
)

// IsGIF returns whether the data held by r is a GIF image.
func IsGIF(r ReadPeeker) bool {
	return hasMagic("GIF8?a", r)
}

// ReadPeeker is an io.Reader that can also peek n bytes ahead.
type ReadPeeker interface {
	io.Reader
	Peek(n int) ([]byte, error)
}

// AsReadPeeker converts an io.Reader to a ReadPeeker.
func AsReadPeeker(r io.Reader) ReadPeeker {
	if r, ok := r.(ReadPeeker); ok {
		return r
	}
	return bufio.NewReader(r)
}

// hasMagic returns whether r starts with the provided magic bytes.
func hasMagic(magic string, r ReadPeeker) bool {
	b, err := r.Peek(len(magic))
	if err != nil || len(b) != len(magic) {
		return false
	}
	for i, c := range b {
		if magic[i] != c && magic[i] != '?' {
			return false
		}
	}
	return true
}

// GIF is an animated GIF.
//
// The GIF [image.Image] implementation is conditional on whether an animation
// The image is either from the first or last frame; if an terminates the
// last frame is used, if it does not terminate the first is used. GIF
// animations that terminate should ensure that the final frame is a complete
// renderable image.
//
// GIF pointer values must not be shared between goroutines. It is safe to
// share the backing gif.GIF and Cache values, so if a GIF is to be used
// concurrently, each goroutine should have a pointer to a copy of a parent
// concrete GIF value.
type GIF struct {
	*gif.GIF

	// Cache caches GIF frames in the internal format
	// of the device. Cache must not be changed while
	// an animation is running.
	Cache *Cache

	// complete indicates that the last animation was
	// not terminated.
	complete bool
}

// DecodeGIF returns a [GIF] or [gif.GIF] decoded from the provided io.Reader.
// If the GIF data encodes a single frame, the image returned is a gif.GIF,
// otherwise a GIF is returned. When the result is a GIF, GIF delay,
// disposal and global background index values are checked for validity.
func DecodeGIF(r io.Reader) (image.Image, error) {
	g, err := gif.DecodeAll(r)
	if err != nil {
		return nil, err
	}
	if len(g.Image) == 1 {
		return g.Image[0], nil
	}
	if len(g.Image) != len(g.Delay) && g.Delay != nil {
		return nil, fmt.Errorf("mismatched image count and delay count: %d != %d", len(g.Image), len(g.Delay))
	}
	if len(g.Image) != len(g.Disposal) && g.Disposal != nil {
		return nil, fmt.Errorf("mismatched image count and disposal count: %d != %d", len(g.Image), len(g.Disposal))
	}
	pal, ok := g.Config.ColorModel.(color.Palette)
	if idx := int(g.BackgroundIndex); ok && idx >= len(pal) {
		return nil, fmt.Errorf("global background colour index not in palette: %d", idx)
	}
	return &GIF{GIF: g}, nil
}

// ColorModel implements the image.Image interface. If the GIF has a global
// color table, its color model is returned, otherwise the first or last frame's
// model is used.
func (img *GIF) ColorModel() color.Model {
	if img.Config.ColorModel != nil {
		return img.Config.ColorModel
	}
	var idx int
	if img.complete {
		idx = len(img.Image) - 1
	}
	return img.GIF.Image[idx].ColorModel()
}

// Bounds implements the image.Image interface. The frame image used
func (img *GIF) Bounds() image.Rectangle {
	var idx int
	if img.complete {
		idx = len(img.Image) - 1
	}
	return img.GIF.Image[idx].Bounds()
}

// At implements the image.Image interface. The frame image used
func (img *GIF) At(x, y int) color.Color {
	var idx int
	if img.complete {
		idx = len(img.Image) - 1
	}
	return img.GIF.Image[idx].At(x, y)
}

// Animate renders the receiver's frames into dst and calls fn on each
// rendered frame.
func (img *GIF) Animate(ctx context.Context, dst draw.Image, fn func(image.Image) error) error {
	img.complete = false
	const (
		restoreBackground = 2
		restorePrevious   = 3
	)
	var background image.Image
	pal, ok := img.Config.ColorModel.(color.Palette)
	if idx := int(img.BackgroundIndex); ok {
		background = &image.Uniform{pal[idx]}
	}

	loopCount := img.LoopCount
	if loopCount <= 0 {
		loopCount = -loopCount - 1
	}
	// Make sure we have a final image if this is a
	// terminating GIF. Without this, the non-cached
	// case works correctly, but the cached case never
	// sets dst and so an empty image is set on exit.
	last := image.Image(dst)
	for i := 0; i <= loopCount || loopCount == -1; i++ {
		for f, frame := range img.Image {
			// Fast path.
			if r, ok := img.Cache.get(frame); ok {
				err := fn(r)
				if err != nil {
					return err
				}
				last = r
				if img.Delay != nil {
					delay := time.NewTimer(10 * time.Duration(img.Delay[f]) * time.Millisecond)
					select {
					case <-ctx.Done():
						delay.Stop()
						return ctx.Err()
					case <-delay.C:
					}
				} else {
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
					}
				}
				continue
			}

			// Slow path.
			var restore *image.Paletted
			if img.Disposal != nil && img.Disposal[f] == restorePrevious {
				restore = image.NewPaletted(frame.Bounds(), frame.Palette)
				draw.Copy(restore, restore.Bounds().Min, dst, frame.Bounds(), draw.Over, nil)
			}
			draw.Copy(dst, frame.Bounds().Min, frame, frame.Bounds(), draw.Over, nil)
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			r, err := img.Cache.put(frame, dst)
			if err != nil {
				return err
			}
			err = fn(r)
			if err != nil {
				return err
			}
			if img.Delay != nil {
				delay := time.NewTimer(10 * time.Duration(img.Delay[f]) * time.Millisecond)
				select {
				case <-ctx.Done():
					delay.Stop()
					return ctx.Err()
				case <-delay.C:
				}
			} else {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
			}
			if img.Disposal != nil {
				switch img.Disposal[f] {
				case restoreBackground:
					if background == nil {
						if idx := int(img.BackgroundIndex); idx < len(frame.Palette) {
							background = &image.Uniform{frame.Palette[idx]}
						} else {
							// No available background, so make this
							// clear in the rendered image.
							background = &image.Uniform{color.RGBA{R: 0xff, A: 0xff}}
						}
					}
					draw.Copy(dst, frame.Bounds().Min, background, frame.Bounds(), draw.Over, nil)
				case restorePrevious:
					draw.Copy(dst, frame.Bounds().Min, restore, restore.Bounds(), draw.Over, nil)
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			last = dst
		}
	}
	img.complete = true
	return fn(last)
}

// GIFs is an animator that runs through a set of GIF animations in order.
// Each of the GIFs must have the same bounds and only the last GIF in the
// sequence may be non-terminating. As with the GIF type, if the final GIF
// in the sequence has completed, it's final frame is used as the complete
// image.Image, otherwise the first frame of the first GIF in the sequence
// is used.
type GIFs []*GIF

// Animate renders the receiver's GIFs' frames into dst and calls fn on each
// rendered frame.
func (img GIFs) Animate(ctx context.Context, dst draw.Image, fn func(image.Image) error) error {
	var b image.Rectangle
	for i, g := range img {
		if i == 0 {
			b = g.Bounds()
		} else if b != g.Bounds() {
			return fmt.Errorf("mismatched bounds at %d: %v != %v", i, g.Bounds(), b)
		}
		if i < len(img)-1 && g.LoopCount == 0 {
			return fmt.Errorf("non-terminating GIF before last position: %d", i)
		}
		err := g.Animate(ctx, dst, fn)
		if err != nil {
			return err
		}
	}
	return nil
}

// At implements the image.Image interface.
func (img GIFs) At(x, y int) color.Color {
	if len(img) == 0 {
		return nil
	}
	var idx int
	if img[len(img)-1].complete {
		idx = len(img) - 1
	}
	return img[idx].At(x, y)
}

// Bounds implements the image.Image interface.
func (img GIFs) Bounds() image.Rectangle {
	if len(img) == 0 {
		return image.Rectangle{}
	}
	var idx int
	if img[len(img)-1].complete {
		idx = len(img) - 1
	}
	return img[idx].Bounds()
}

// ColorModel implements the image.Image interface.
func (img GIFs) ColorModel() color.Model {
	if len(img) == 0 {
		return nil
	}
	var idx int
	if img[len(img)-1].complete {
		idx = len(img) - 1
	}
	return img[idx].ColorModel()
}
