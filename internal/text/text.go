// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package text provides functions for rendering [basicfont.Face] fonts to a
// an image.
package text

import (
	"image"
	"image/color"
	"image/draw"
	"strings"

	"github.com/bbrks/wrap/v2"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Size returns the size, in font rows and columns, of the bounding rectangle.
func Size(bound image.Rectangle, fnt *basicfont.Face) (rows, cols int) {
	rows = bound.Dx() / fnt.Height
	cols = bound.Dy() / (fnt.Width + 1)
	return rows, cols
}

// KeepAspectRatio returns a draw rectangle that can be used in a call to
// a draw.Scaler to maintain the src aspect ratio in the dst image.
//
//	draw.BiLinear.Scale(dst, KeepAspectRatio(dst, src), src, src.Bounds(), op, opts)
func KeepAspectRatio(dst, src image.Image) image.Rectangle {
	b := dst.Bounds()
	dx, dy := src.Bounds().Dx(), src.Bounds().Dy()
	switch {
	case dx < dy:
		dx, dy = dx*b.Max.X/dy, b.Max.Y
	case dx > dy:
		dx, dy = b.Max.
			X, dy*b.Max.Y/dx
	default:
		return b
	}
	offset := image.Point{X: (b.Dx() - dx) / 2, Y: (b.Dy() - dy) / 2}
	return image.Rectangle{Max: image.Point{X: dx, Y: dy}}.Add(offset)
}

// Draw draws the provided text to the destination in the provided color.
// Relative position of the text is specified by dx and dy which must be
// in the range [0, 1]. If words is true, text spanning lines will be broken
// at word boundaries where possible.
func Draw(dst draw.Image, text string, col color.Color, fnt *basicfont.Face, dx, dy float64, words bool) {
	rows, cols := Size(dst.Bounds(), fnt)

	var lines []string
	if words {
		wrapper := wrap.NewWrapper()
		wrapper.StripTrailingNewline = true
		wrapper.CutLongWords = true
		lines = strings.Split(wrapper.Wrap(text, cols), "\n")
		if len(lines) < 2 || lines[0] != "" {
			for i, l := range lines {
				lines[i] = strings.TrimSpace(l)
			}
		}
	} else {
		t := []rune(text)
		for len(t) != 0 {
			n := cols
			if n > len(t) {
				n = len(t)
			}
			lines = append(lines, string(t[:n]))
			t = t[n:]
		}
	}

	if len(lines) > rows {
		lines = lines[:rows]
		if len(lines[rows-1]) > cols-len("...") {
			lines[rows-1] = lines[rows-1][:cols-len("...")]
		}
		lines[rows-1] += "..."
	}

	if dx != 0 || dy != 0 {
		mp := newBounds(dst)
		min := dst.Bounds().Min
		for i, l := range lines {
			mp.drawString(l, fnt, fixed.P(min.X, min.Y+fnt.Ascent+(fnt.Height)*i))
		}
		dst = mp.offset(dst, dx, dy)
	}
	fg := &image.Uniform{col}
	min := dst.Bounds().Min
	for i, l := range lines {
		drawer := font.Drawer{
			Dst:  dst,
			Src:  fg,
			Face: fnt,
			Dot:  fixed.P(min.X, min.Y+fnt.Ascent+(fnt.Height)*i),
		}
		drawer.DrawString(l)
	}
}

// Outlined is an image that renders a single pixel width outline
// around a drawing.
type Outlined[T draw.Image] struct {
	Text       T
	Background T

	OutlineColor color.Color
}

func (o Outlined[T]) Set(x, y int, c color.Color) {
	o.Text.Set(x, y, c)
	for i := -1; i <= 1; i++ {
		o.Background.Set(x+i, y, o.OutlineColor)
	}
	for i := -1; i <= 1; i += 2 {
		o.Background.Set(x, y+i, o.OutlineColor)
	}
}

func (o Outlined[T]) At(x, y int) color.Color {
	// m is the maximum color value returned by image.Color.RGBA.
	const m = 1<<16 - 1

	rT, gT, bT, aT := o.Text.At(x, y).RGBA()
	rO, gO, bO, aO := o.Background.At(x, y).RGBA()
	a := (m - aT)
	a |= a << 8
	return color.RGBA{
		R: uint8((rO*a/m + rT) >> 8),
		G: uint8((gO*a/m + gT) >> 8),
		B: uint8((bO*a/m + bT) >> 8),
		A: uint8((aO*a/m + aT) >> 8),
	}
}

func (o Outlined[T]) Bounds() image.Rectangle {
	return o.Text.Bounds().Intersect(o.Background.Bounds())
}

func (o Outlined[T]) ColorModel() color.Model {
	return o.Text.ColorModel()
}

// Shrink reduces the bounds of an Image by a margin.
type Shrink struct {
	draw.Image

	// Margin is the margin size in pixels.
	Margin int
}

func (s Shrink) Bounds() image.Rectangle {
	b := s.Image.Bounds()
	b.Min = b.Min.Add(image.Point{X: s.Margin, Y: s.Margin})
	b.Max = b.Max.Sub(image.Point{X: s.Margin, Y: s.Margin})
	return b
}

type bounds image.Rectangle

func newBounds(dst draw.Image) *bounds {
	b := bounds(image.Rectangle{Min: dst.Bounds().Max, Max: dst.Bounds().Min})
	return &b
}

func (b *bounds) drawString(s string, fnt font.Face, dot fixed.Point26_6) {
	prevC := rune(-1)
	for _, c := range s {
		if prevC >= 0 {
			dot.X += fnt.Kern(prevC, c)
		}
		dr, _, _, advance, ok := fnt.Glyph(dot, c)
		if !ok {
			continue
		}
		b.set(dr.Min.X, dr.Min.Y)
		b.set(dr.Max.X, dr.Max.Y)
		dot.X += advance
		prevC = c
	}
}

func (b *bounds) set(x, y int) {
	if x < b.Min.X {
		b.Min.X = x
	}
	if y < b.Min.Y {
		b.Min.Y = y
	}
	if x > b.Max.X {
		b.Max.X = x
	}
	if y > b.Max.Y {
		b.Max.Y = y
	}
}

func (b *bounds) offset(img draw.Image, dx, dy float64) draw.Image {
	d := img.Bounds().Max.Sub(b.Max)
	return offset{Image: img, offset: image.Point{X: int(float64(d.X) * dx), Y: int(float64(d.Y) * dy)}}
}

type offset struct {
	draw.Image
	offset image.Point
}

func (o offset) Set(x, y int, c color.Color) {
	o.Image.Set(x+o.offset.X, y+o.offset.Y, c)
}

func (o offset) At(x, y int) color.Color {
	return o.Image.At(x+o.offset.X, y+o.offset.Y)
}
