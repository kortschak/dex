// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package text

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/draw"
	"golang.org/x/image/font/basicfont"
)

var update = flag.Bool("update", false, "regenerate golden images")

var writeTests = []struct {
	name  string
	text  string
	rect  image.Rectangle
	color color.Color
}{
	{
		name:  "small",
		text:  "text",
		rect:  image.Rectangle{Max: image.Point{X: 72, Y: 72}},
		color: color.White,
	},
	{
		name:  "long",
		text:  "reallylongword",
		rect:  image.Rectangle{Max: image.Point{X: 72, Y: 72}},
		color: color.White,
	},
	{
		name:  "sentence",
		text:  "Lorem ipsum dolor sit amet, consectetur adipisci elit, sed eiusmod tempor incidunt ut labore et dolore magna aliqua.",
		rect:  image.Rectangle{Max: image.Point{X: 72, Y: 72}},
		color: color.White,
	},
	{
		name:  "sentence_compress",
		text:  "Lorem ipsum dolor sit amet, consectetur adipisci elit, sed eiusmod tempor incidunt ut labore et dolore magna aliqua.",
		rect:  image.Rectangle{Max: image.Point{X: 144, Y: 144}},
		color: color.White,
	},
}

func TestDraw(t *testing.T) {
	for _, test := range writeTests {
		t.Run(test.name, func(t *testing.T) {
			for _, centering := range []struct {
				dx, dy float64
				name   string
			}{
				{dx: 0, dy: 0, name: "topleft"},
				{dx: 1, dy: 0, name: "topright"},
				{dx: 0.5, dy: 0.5, name: "centered"},
				{dx: 0.5, dy: 1, name: "centerbottom"},
				{dx: 0.5, dy: 0.75, name: "centerbottomquarter"},
			} {
				suffix := centering.name
				for _, outline := range []bool{false, true} {
					if outline {
						suffix += "_outlined"
					}
					t.Run(suffix, func(t *testing.T) {
						dst := draw.Image(image.NewRGBA(test.rect))
						var margin int
						if outline {
							out := Outlined[*image.RGBA]{
								Text:         image.NewRGBA(test.rect),
								Background:   image.NewRGBA(test.rect),
								OutlineColor: color.RGBA{R: 0xff, G: 0xff, A: 0xff},
							}
							dst = out
							margin = 1
							draw.Draw(out.Background, dst.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
						} else {
							draw.Draw(dst, dst.Bounds(), &image.Uniform{color.Black}, image.Point{}, draw.Src)
						}
						Draw(Shrink{dst, margin}, test.text, test.color, basicfont.Face7x13, centering.dx, centering.dy, true)
						var buf bytes.Buffer
						err := png.Encode(&buf, dst)
						if err != nil {
							t.Fatalf("unexpected error encoding image: %v", err)
						}

						path := filepath.Join("testdata", test.name+"_"+suffix+".png")
						if *update {
							err = os.WriteFile(path, buf.Bytes(), 0o644)
							if err != nil {
								t.Fatalf("unexpected error writing golden file: %v", err)
							}
						}

						want, err := os.ReadFile(path)
						if err != nil {
							t.Fatalf("unexpected error reading golden file: %v", err)
						}

						if !bytes.Equal(buf.Bytes(), want) {
							err = os.WriteFile(filepath.Join("testdata", "failed-"+test.name+"_"+suffix+".png"), buf.Bytes(), 0o644)
							if err != nil {
								t.Fatalf("unexpected error writing failed file: %v", err)
							}
							t.Errorf("image mismatch: %s", path)
						}
					})
				}
			}
		})
	}
}

func TestOverlay(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "gopher.png"))
	if err != nil {
		t.Fatalf("failed to open gopher image: %v", err)
	}
	defer f.Close()
	gopher, err := png.Decode(f)
	if err != nil {
		t.Fatalf("failed to decode gopher image: %v", err)
	}
	bounds := image.Rectangle{Max: image.Point{X: 72, Y: 72}}
	dst := Outlined[*image.RGBA]{
		Text:         image.NewRGBA(bounds),
		Background:   image.NewRGBA(bounds),
		OutlineColor: color.White,
	}
	draw.BiLinear.Scale(dst.Background, KeepAspectRatio(dst, gopher), gopher, gopher.Bounds(), draw.Src, nil)
	Draw(Shrink{dst, 1}, "Gopher", color.Black, basicfont.Face7x13, 0.5, 0.9, true)

	var buf bytes.Buffer
	err = png.Encode(&buf, dst)
	if err != nil {
		t.Fatalf("unexpected error encoding image: %v", err)
	}

	path := filepath.Join("testdata", "gopher_text.png")
	if *update {
		err = os.WriteFile(path, buf.Bytes(), 0o644)
		if err != nil {
			t.Fatalf("unexpected error writing golden file: %v", err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("unexpected error reading golden file: %v", err)
	}

	if !bytes.Equal(buf.Bytes(), want) {
		err = os.WriteFile(filepath.Join("testdata", "failed-gopher_text.png"), buf.Bytes(), 0o644)
		if err != nil {
			t.Fatalf("unexpected error writing failed file: %v", err)
		}
		t.Errorf("image mismatch: %s", path)
	}
}
