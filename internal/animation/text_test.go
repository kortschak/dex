// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package animation

import (
	"bytes"
	"flag"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"
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
	{
		name:  "full_screen",
		text:  "ipsum dolor sit amet, consectetur adipisci elit, s",
		rect:  image.Rectangle{Max: image.Point{X: 72, Y: 72}},
		color: color.White,
	},
}

func TestWrite(t *testing.T) {
	for _, test := range writeTests {
		t.Run(test.name, func(t *testing.T) {
			pal := color.Palette{color.Black, color.White}
			g, err := Text(test.text).GIF(test.rect, pal, 1, 0)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var buf bytes.Buffer
			err = gif.EncodeAll(&buf, g.GIF)
			if err != nil {
				t.Fatalf("unexpected error encoding image: %v", err)
			}

			path := filepath.Join("testdata", test.name+".gif")
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
				err = os.WriteFile(filepath.Join("testdata", "failed-"+test.name+".gif"), buf.Bytes(), 0o644)
				if err != nil {
					t.Fatalf("unexpected error writing failed file: %v", err)
				}
				t.Errorf("image mismatch: %s", path)
			}
		})
	}
}
