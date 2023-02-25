// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package animation

import (
	"errors"
	"image"
	"image/color"
	"image/gif"
	"strings"
	"unicode/utf8"

	"github.com/bbrks/wrap/v2"
	"golang.org/x/image/draw"
	"golang.org/x/image/font/basicfont"

	"github.com/kortschak/dex/internal/text"
)

// Text is a scrolling text animator.
type Text string

// GIF returns a GIF containing animation frames required to present the full
// length of the receiver within the given bounds using [basicfont.Face7x13].
// The provided palette must have at least two colors, which will be indexed
// by fg and bg to provide the foreground and background colors for the
// text animation.
func (t Text) GIF(bound image.Rectangle, pal color.Palette, fg, bg byte) (*GIF, error) {
	rows, cols := text.Size(bound, basicfont.Face7x13)
	s := string(t)
	frames := 1
	if utf8.RuneCountInString(s) > rows*cols {
		if rows*cols < 4 {
			return nil, errors.New("bound too small")
		}
		s = strings.Repeat(" ", rows*cols-4) + s
		frames = len(s)
	} else {
		wrapper := wrap.NewWrapper()
		wrapper.StripTrailingNewline = true
		wrapper.CutLongWords = true
		lines := strings.Split(wrapper.Wrap(s, cols), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimSpace(l)
		}
		if len(lines) > rows || (len(lines) == rows && len(lines[len(lines)-1]) > cols) {
			if rows*cols < 4 {
				return nil, errors.New("bound too small")
			}
			s = strings.Repeat(" ", rows*cols-4) + s
			frames = len(s)
		}
	}
	g := &gif.GIF{
		Image: make([]*image.Paletted, 0, frames),
		Delay: make([]int, 0, frames),
		Config: image.Config{
			ColorModel: pal,
			Width:      bound.Dx(),
			Height:     bound.Dy(),
		},
		BackgroundIndex: bg,
	}
	singleFrame := frames == 1 // We will center and word break the text in this case.
	var delta float64
	if singleFrame {
		g.LoopCount = -1
		delta = 0.5
	}
	background := &image.Uniform{pal[bg]}
	for i := range s[:frames] {
		dst := image.NewPaletted(bound, pal)
		draw.Draw(dst, dst.Bounds(), background, image.Point{}, draw.Src)
		text.Draw(dst, s[i:], pal[fg], basicfont.Face7x13, delta, delta, singleFrame)
		g.Image = append(g.Image, dst)
		g.Delay = append(g.Delay, 15)
	}
	return &GIF{GIF: g}, nil
}
