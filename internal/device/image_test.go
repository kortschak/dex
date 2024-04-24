// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package device

import (
	"bytes"
	"errors"
	"flag"
	"image"
	"image/gif"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/image/draw"

	"github.com/kortschak/dex/internal/animation"
)

var update = flag.Bool("update", false, "regenerate golden images")

var writeTests = []struct {
	name string
	data string
	typ  string
	err  error

	skip string
}{
	{
		name: "message",
		data: "data:text/plain,message",
		typ:  "text",
	},
	{
		name: "long_message",
		data: "data:text/plain,a long message that spans more than a single screen",
		typ:  "text",
	},
	{
		name: "long_message_fg_bg_web",
		data: "data:text/plain;fg=black;bg=hiyellow,a long message that spans more than a single screen",
		typ:  "text",
	},
	{
		name: "long_message_fg_bg_hex",
		data: "data:text/plain;fg=#000000;bg=#ffff00,a long message that spans more than a single screen",
		typ:  "text",
	},
	{
		name: "gif_file",
		data: "data:text/filename,gopher-dance-long.gif",
		typ:  "image_file",
	},
	{
		name: "gif_file_title",
		data: "data:text/filename;title=gopher,gopher-dance-long.gif",
		typ:  "image_file",
		skip: "darwin:fails due to scaling",
	},
	{
		name: "png_file",
		data: "data:text/filename,gopher.png",
		typ:  "image_file",
	},
	{
		name: "png_file_title",
		data: "data:text/filename;title=gopher;dy=0.1,gopher.png",
		typ:  "image_file",
	},
	{
		name: "png_file_prescaled",
		data: "data:text/filename,gopher-72x72.png",
		typ:  "image_file",
	},
	{
		name: "png_file_prescaled_title",
		data: "data:text/filename;title=gopher;dy=0.5,gopher-72x72.png",
		typ:  "image_file",
	},
	{
		name: "invalid_path",
		data: "data:text/filename,no-file-with-this-name.png",
		err:  errors.New("file: open testdata/no-file-with-this-name.png: no such file or directory"),
	},
	{
		name: "unknown_text_type",
		data: "data:text/mp4,message",
		err:  errors.New("unknown text mime type: data:text/mp4,message"),
	},
	{
		name: "png_image",
		data: "data:image/*;base64,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",
		typ:  "image",
	},
	{
		name: "png_image_title",
		data: "data:image/*;title=gopher;base64,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",
		typ:  "image",
	},
	{
		name: "name_black_image",
		data: "data:image/color;name,black",
		typ:  "named_color",
	},
	{
		name: "name_black_image_title",
		data: "data:image/color;title=black;fg=white;bg=hiblue;name,black",
		typ:  "named_color",
	},
	{
		name: "name_red_image",
		data: "data:image/color;name,red",
		typ:  "named_color",
	},
	{
		name: "name_green_image",
		data: "data:image/color;name,green",
		typ:  "named_color",
	},
	{
		name: "name_yellow_image",
		data: "data:image/color;name,yellow",
		typ:  "named_color",
	},
	{
		name: "name_blue_image",
		data: "data:image/color;name,blue",
		typ:  "named_color",
	},
	{
		name: "name_magenta_image",
		data: "data:image/color;name,magenta",
		typ:  "named_color",
	},
	{
		name: "name_cyan_image",
		data: "data:image/color;name,cyan",
		typ:  "named_color",
	},
	{
		name: "name_white_image",
		data: "data:image/color;name,white",
		typ:  "named_color",
	},
	{
		name: "name_hiblack_image",
		data: "data:image/color;name,hiblack",
		typ:  "named_color",
	},
	{
		name: "name_hired_image",
		data: "data:image/color;name,hired",
		typ:  "named_color",
	},
	{
		name: "name_higreen_image",
		data: "data:image/color;name,higreen",
		typ:  "named_color",
	},
	{
		name: "name_hiyellow_image",
		data: "data:image/color;name,hiyellow",
		typ:  "named_color",
	},
	{
		name: "name_hiblue_image",
		data: "data:image/color;name,hiblue",
		typ:  "named_color",
	},
	{
		name: "name_himagenta_image",
		data: "data:image/color;name,himagenta",
		typ:  "named_color",
	},
	{
		name: "name_hicyan_image",
		data: "data:image/color;name,hicyan",
		typ:  "named_color",
	},
	{
		name: "name_hiwhite_image",
		data: "data:image/color;name,hiwhite",
		typ:  "named_color",
	},
	{
		name: "web_black_image",
		data: "data:image/color;web,#000000",
		typ:  "web_color",
	},
	{
		name: "web_red_image",
		data: "data:image/color;web,#ff0000",
		typ:  "web_color",
	},
	{
		name: "invalid_base6",
		data: "data:image/*;base64,iVBOR",
		err:  errors.New("base64: illegal base64 data at input byte 4"),
	},
	{
		name: "invalid_image",
		data: "data:image/*;base64,dGhpcyBpcyBub3QgYW4gaW1hZ2UK",
		err:  errors.New("image: unknown format"),
	},
	{
		name: "invalid_scheme",
		data: "text/plain,message",
		err:  errors.New("invalid scheme: text/plain,message"),
	},
	{
		name: "unknown_mime_type",
		data: "data:video/mp4,message",
		err:  errors.New("unknown mime type: data:video/mp4,message"),
	},
}

func TestWrite(t *testing.T) {
	// Validations patterns from the CUE definition in config.
	patterns := map[string]*regexp.Regexp{
		"text":        regexp.MustCompile("^data:text/plain(?:;[^;]+=[^;]*)*,.*$"),
		"image":       regexp.MustCompile("^data:image/\\*(?:;[^;]+=[^;]*)*;base64,.*$"), //lint:ignore S1007 Using double quotes to match CUE.
		"image_file":  regexp.MustCompile("^data:text/filename(?:;[^;]+=[^;]*)*,.*$"),
		"named_color": regexp.MustCompile("^data:image/color(?:;[^;]+=[^;]*)*;name,(?:hi)?(?:black|red|green|yellow|blue|magenta|cyan|white)$"),
		"web_color":   regexp.MustCompile("^data:image/color(?:;[^;]+=[^;]*)*;web,#[0-9a-fA-F]{6}$"),
	}

	for _, test := range writeTests {
		t.Run(test.name, func(t *testing.T) {
			skip, reason, ok := strings.Cut(test.skip, ":")
			if ok && slices.Contains(strings.Split(skip, ","), runtime.GOOS) {
				t.Skip(reason)
			}
			rect := image.Rectangle{Max: image.Point{X: 72, Y: 72}}

			img, err := DecodeImage(rect, test.data, "testdata")
			if !sameError(err, test.err) {
				t.Errorf("unexpected error encoding image: got:%v want:%v", err, test.err)
			}
			if test.err == nil {
				for typ, re := range patterns {
					if re.MatchString(test.data) {
						if typ != test.typ {
							t.Errorf("unexpected type: got:%v want:%v", typ, test.typ)
						}
					}
				}
			}

			var (
				buf  bytes.Buffer
				name string
			)
			switch img := img.(type) {
			case *animation.GIF:
				err = gif.EncodeAll(&buf, img.GIF)
				if err != nil {
					t.Fatalf("unexpected error encoding image: %v", err)
				}
				name = test.name + ".gif"
			default:
				err = png.Encode(&buf, img)
				if err != nil {
					t.Fatalf("unexpected error encoding image: %v", err)
				}
				name = test.name + ".png"
			}

			path := filepath.Join("testdata", name)
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
				err = os.WriteFile(filepath.Join("testdata", "failed-"+name), buf.Bytes(), 0o644)
				if err != nil {
					t.Fatalf("unexpected error writing failed file: %v", err)
				}
				t.Errorf("image mismatch: %s", path)
			}
		})
	}
}

func sameError(a, b error) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil, b == nil, a.Error() != b.Error():
		return false
	default:
		return true
	}
}

func TestGIFScale(t *testing.T) {
	f, err := os.Open("testdata/gopher-dance-long.gif")
	if err != nil {
		t.Fatalf("failed to read image data: %v", err)
	}
	g, err := gif.DecodeAll(f)
	if err != nil {
		t.Fatalf("failed to decode image data: %v", err)
	}
	rect := image.Rectangle{Max: image.Point{X: 72, Y: 72}}
	for i, frame := range g.Image {
		g.Image[i] = image.NewPaletted(rect, g.Image[0].Palette)
		draw.BiLinear.Scale(g.Image[i], rect, frame, frame.Bounds(), draw.Src, nil)
	}
	g.Config.Width = rect.Dx()
	g.Config.Height = rect.Dy()

	var buf bytes.Buffer
	err = gif.EncodeAll(&buf, g)
	if err != nil {
		t.Fatalf("unexpected error encoding image: %v", err)
	}

	path := "testdata/gopher-dance-long-scaled.gif"
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
		t.Errorf("image mismatch:\n- want:\n+ got:\n%s", cmp.Diff(want, buf.Bytes()))
	}
}
