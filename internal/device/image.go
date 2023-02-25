// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package device

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/kortschak/dex/internal/animation"
)

// DecodeImage decodes image data from a data uri corresponding to the CUE
// _#data_uri definitions in the config package rendered to the size of rect
// if it is text. Image files are opened relative to the datadir path unless
// the filename is an absolute path. Any error is rendered as a text image and
// returned as an error.
func DecodeImage(rect image.Rectangle, data, datadir string) (image.Image, error) {
	pal := color.Palette{color.Black, color.White}
	typ, mtyp, val, enc, err := parseDataURI(data)
	if err != nil {
		return errorImage(err, rect, pal, 1, 0)
	}
	var r animation.ReadPeeker
	switch typ {
	case "text":
		switch mtyp {
		default:
			return errorImage(fmt.Errorf("unknown text mime type: %s", data), rect, pal, 1, 0)
		case "text/plain":
			return animation.Text(val).GIF(rect, pal, 1, 0)
		case "text/filename":
			if !filepath.IsAbs(val) {
				val = filepath.Join(datadir, val)
			}
			f, err := os.Open(val)
			if err != nil {
				return errorImage(fmt.Errorf("file: %w", err), rect, pal, 1, 0)
			}
			defer f.Close()
			r = animation.AsReadPeeker(f)
		}
	case "image":
		switch enc {
		case "name":
			col, ok := ansiColor[val]
			if !ok {
				return errorImage(fmt.Errorf("invalid color name: %s", val), rect, pal, 1, 0)
			}
			return swatch{Uniform: &image.Uniform{col}, bounds: rect}, nil
		case "web":
			val, ok := strings.CutPrefix(val, "#")
			if !ok {
				return errorImage(fmt.Errorf("invalid web color: %s", val), rect, pal, 1, 0)
			}
			c, err := strconv.ParseUint(val, 16, 24)
			if err != nil {
				return errorImage(err, rect, pal, 1, 0)
			}
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(c))
			col := color.NRGBA{R: b[1], G: b[2], B: b[3], A: 0xff}
			return swatch{Uniform: &image.Uniform{col}, bounds: rect}, nil
		case "base64":
			b, err := base64.StdEncoding.DecodeString(val)
			if err != nil {
				return errorImage(fmt.Errorf("base64: %w", err), rect, pal, 1, 0)
			}
			r = animation.AsReadPeeker(bytes.NewReader(b))
		}
	default:
		panic("unreachable")
	}
	var img image.Image
	if animation.IsGIF(r) {
		img, err = animation.DecodeGIF(r)
	} else {
		img, _, err = image.Decode(r)
	}
	if err != nil {
		return errorImage(err, rect, pal, 1, 0)
	}
	return img, nil
}

func errorImage(err error, rect image.Rectangle, pal color.Palette, fg, bg byte) (image.Image, error) {
	img, gifErr := animation.Text(err.Error()).GIF(rect, pal, fg, bg)
	if gifErr != nil {
		return nil, errors.Join(err, gifErr)
	}
	return img, err
}

// handle data URIs in the form "^data:(?:text/(?:filename|plain)|image/\*;base64),.*$"
func parseDataURI(uri string) (typ, mtyp, val, enc string, err error) {
	u, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return "", "", "", "", fmt.Errorf("invalid scheme: %s", uri)
	}
	mtyp, val, ok = strings.Cut(u, ",")
	if !ok {
		return "", "", "", "", fmt.Errorf("invalid data uri: %s", uri)
	}
	typ, _, ok = strings.Cut(mtyp, "/")
	if !ok {
		return "", "", "", "", fmt.Errorf("invalid data uri: %s", uri)
	}
	switch typ {
	case "text":
		return typ, mtyp, val, "", nil
	case "image":
		mtyp, enc, ok = strings.Cut(mtyp, ";")
		if !ok {
			return "", "", "", "", fmt.Errorf("invalid image data uri: %s", uri)
		}
		switch enc {
		case "base64", "name", "web":
			return typ, mtyp, val, enc, nil
		default:
			return "", "", "", "", fmt.Errorf("invalid encoding in image uri: %s", uri)
		}
	default:
		return "", "", "", "", fmt.Errorf("unknown mime type: %s", uri)
	}
}

var ansiColor = map[string]*image.Uniform{
	"black":     {C: color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xff}},
	"red":       {C: color.RGBA{R: 0x80, G: 0x00, B: 0x00, A: 0xff}},
	"green":     {C: color.RGBA{R: 0x00, G: 0x80, B: 0x00, A: 0xff}},
	"yellow":    {C: color.RGBA{R: 0x80, G: 0x80, B: 0x00, A: 0xff}},
	"blue":      {C: color.RGBA{R: 0x00, G: 0x00, B: 0x80, A: 0xff}},
	"magenta":   {C: color.RGBA{R: 0x80, G: 0x00, B: 0x80, A: 0xff}},
	"cyan":      {C: color.RGBA{R: 0x00, G: 0x80, B: 0x80, A: 0xff}},
	"white":     {C: color.RGBA{R: 0xc0, G: 0xc0, B: 0xc0, A: 0xff}},
	"hiblack":   {C: color.RGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xff}},
	"hired":     {C: color.RGBA{R: 0xff, G: 0x00, B: 0x00, A: 0xff}},
	"higreen":   {C: color.RGBA{R: 0x00, G: 0xff, B: 0x00, A: 0xff}},
	"hiyellow":  {C: color.RGBA{R: 0xff, G: 0xff, B: 0x00, A: 0xff}},
	"hiblue":    {C: color.RGBA{R: 0x00, G: 0x00, B: 0xff, A: 0xff}},
	"himagenta": {C: color.RGBA{R: 0xff, G: 0x00, B: 0xff, A: 0xff}},
	"hicyan":    {C: color.RGBA{R: 0x00, G: 0xff, B: 0xff, A: 0xff}},
	"hiwhite":   {C: color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
}
