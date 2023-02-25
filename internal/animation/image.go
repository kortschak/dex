// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package animation

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"sync"
	"time"

	"github.com/kortschak/ardilla"
	"golang.org/x/image/draw"
)

// Images is an animator that animates through a set of images in order similar
// to the GIFs type. If an image in the set is an Animator, it is animated. No
// checks are made for non-terminating animations with the set.
type Images struct {
	// The successive images.
	Image []image.Image

	// Delay is the delay between images.
	Delay time.Duration

	// LoopCount controls the number of times an animation will be
	// restarted during display.
	// A LoopCount of 0 means to loop forever.
	// A LoopCount of -1 means to show each frame only once.
	// Otherwise, the animation is looped LoopCount+1 times.
	LoopCount int

	// Cache caches frames in the internal format
	// of the device. Cache must not be changed while
	// an animation is running.
	Cache *Cache

	// complete indicates that the last animation was
	// not terminated.
	complete bool
}

// Animate renders the receiver's images into dst and calls fn on each rendered
// image.
func (img *Images) Animate(ctx context.Context, dst draw.Image, fn func(image.Image) error) error {
	img.complete = false

	var b image.Rectangle
	loopCount := img.LoopCount
	if loopCount <= 0 {
		loopCount = -loopCount - 1
	}
	// Make sure we have a final image if this is a
	// terminating animation. Without this, the non-cached
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
				delay := time.NewTimer(img.Delay)
				select {
				case <-ctx.Done():
					delay.Stop()
					return ctx.Err()
				case <-delay.C:
				}
				continue
			}

			if i == 0 && f == 0 {
				b = frame.Bounds()
			} else if b != frame.Bounds() {
				return fmt.Errorf("mismatched bounds at %d: %v != %v", i, frame.Bounds(), b)
			}

			if a, ok := frame.(Animator); ok {
				err := a.Animate(ctx, dst, fn)
				if err != nil {
					return err
				}
				continue
			}

			// Slow path.
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
			delay := time.NewTimer(img.Delay)
			select {
			case <-ctx.Done():
				delay.Stop()
				return ctx.Err()
			case <-delay.C:
			}
			last = dst
		}
	}
	img.complete = true
	return fn(last)
}

// At implements the image.Image interface.
func (img *Images) At(x, y int) color.Color {
	if len(img.Image) == 0 {
		return nil
	}
	var idx int
	if img.complete {
		idx = len(img.Image) - 1
	}
	return img.Image[idx].At(x, y)
}

// Bounds implements the image.Image interface.
func (img *Images) Bounds() image.Rectangle {
	if len(img.Image) == 0 {
		return image.Rectangle{}
	}
	var idx int
	if img.complete {
		idx = len(img.Image) - 1
	}
	return img.Image[idx].Bounds()
}

// ColorModel implements the image.Image interface.
func (img *Images) ColorModel() color.Model {
	if len(img.Image) == 0 {
		return nil
	}
	var idx int
	if img.complete {
		idx = len(img.Image) - 1
	}
	return img.Image[idx].ColorModel()
}

// Cache is an ardilla.RawFrame cache.
type Cache struct {
	miss func(image.Image) (*ardilla.RawImage, error)

	mu    sync.Mutex
	cache map[image.Image]*ardilla.RawImage
}

// RawImager wraps the RawImage method.
type RawImager interface {
	RawImage(img image.Image) (*ardilla.RawImage, error)
}

func NewCache(deck RawImager) *Cache {
	return &Cache{
		miss:  deck.RawImage,
		cache: make(map[image.Image]*ardilla.RawImage),
	}
}

// get returns the cached RawImage for the provided key image.
func (c *Cache) get(key image.Image) (image.Image, bool) {
	if c == nil {
		return key, false
	}
	c.mu.Lock()
	r, ok := c.cache[key]
	c.mu.Unlock()
	return r, ok
}

// put calculates and returns an ardilla RawImage for the provided
// image and caches the result for key.
func (c *Cache) put(key, img image.Image) (image.Image, error) {
	if c == nil {
		return img, nil
	}
	r, err := c.miss(img)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.cache[key] = r
	c.mu.Unlock()
	return r, nil
}
