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
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"golang.org/x/image/draw"
	"golang.org/x/image/font/basicfont"

	"github.com/kortschak/dex/internal/animation"
	"github.com/kortschak/dex/internal/text"
)

// DecodeImage decodes image data from a data uri corresponding to the CUE
// _#data_uri definitions in the config package rendered to the size of rect
// if it is text. Image files are opened relative to the datadir path unless
// the filename is an absolute path. Any error is rendered as a text image and
// returned as an error.
func DecodeImage(rect image.Rectangle, data, datadir string) (image.Image, error) {
	pal := color.Palette{color.Black, color.White}
	typ, mtyp, par, val, enc, err := parseDataURI(data)
	if err != nil {
		return errorImage(err, rect, pal, 1, 0)
	}
	param, err := getParams(par)
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
			pal[1], pal[0], err = fgbg(pal[1], pal[0], param)
			if err != nil {
				return errorImage(err, rect, pal, 1, 0)
			}
			return animation.Text(val).GIF(rect, pal, 1, 0)
		case "text/filename":
			val, ok := strings.CutPrefix(val, "~/")
			if ok {
				home, err := os.UserHomeDir()
				if err != nil {
					return errorImage(fmt.Errorf("file: %w", err), rect, pal, 1, 0)
				}
				val = filepath.Join(home, val)
			}
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
			return addTitle(swatch{Uniform: &image.Uniform{col}, bounds: rect}, rect, pal, param)
		case "web":
			col, err := webColor(val)
			if err != nil {
				return errorImage(err, rect, pal, 1, 0)
			}
			return addTitle(swatch{Uniform: &image.Uniform{col}, bounds: rect}, rect, pal, param)
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
	return addTitle(img, rect, pal, param)
}

func getParams(par string) (map[string]string, error) {
	if par == "" {
		return nil, nil
	}
	param := make(map[string]string)
	var err error
	for _, kv := range strings.Split(par, ";") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), "=")
		if !ok {
			return nil, fmt.Errorf("invalid params: %s", par)
		}
		param[strings.TrimSpace(k)], err = url.PathUnescape(strings.TrimSpace(v))
		if err != nil {
			return nil, err
		}
	}
	return param, nil
}

func errorImage(err error, rect image.Rectangle, pal color.Palette, fg, bg byte) (image.Image, error) {
	img, gifErr := animation.Text(err.Error()).GIF(rect, pal, fg, bg)
	if gifErr != nil {
		return nil, errors.Join(err, gifErr)
	}
	return img, err
}

// handle data URIs in the form "^data:(?:text/(?:filename|plain)|image/\*;base64),.*$"
func parseDataURI(uri string) (typ, mtyp, par, val, enc string, err error) {
	u, ok := strings.CutPrefix(uri, "data:")
	if !ok {
		return "", "", "", "", "", fmt.Errorf("invalid scheme: %s", uri)
	}
	mtyp, val, ok = strings.Cut(u, ",")
	if !ok {
		return "", "", "", "", "", fmt.Errorf("invalid data uri: %s", uri)
	}
	typ, _, ok = strings.Cut(mtyp, "/")
	if !ok {
		return "", "", "", "", "", fmt.Errorf("invalid data uri: %s", uri)
	}
	switch typ {
	case "text":
		mtyp, par, _ := strings.Cut(mtyp, ";")
		return typ, mtyp, par, val, "", nil
	case "image":
		mtyp, enc, ok = cutLast(mtyp, ";")
		if !ok {
			return "", "", "", "", "", fmt.Errorf("invalid image data uri: %s", uri)
		}
		switch enc {
		case "base64", "name", "web":
			mtyp, par, _ := strings.Cut(mtyp, ";")
			return typ, mtyp, par, val, enc, nil
		default:
			return "", "", "", "", "", fmt.Errorf("invalid encoding in image uri: %s", uri)
		}
	default:
		return "", "", "", "", "", fmt.Errorf("unknown mime type: %s", uri)
	}
}

func cutLast(s, sep string) (before, after string, found bool) {
	if i := strings.LastIndex(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}

func addTitle(src image.Image, rect image.Rectangle, pal color.Palette, param map[string]string) (img image.Image, err error) {
	title, ok := param["title"]
	if !ok {
		return src, nil
	}
	defer func() {
		if err != nil {
			img, err = errorImage(err, rect, pal, 1, 0)
		}
	}()
	fg, bg, err := fgbg(color.Black, color.White, param)
	if err != nil {
		return nil, err
	}
	switch src := src.(type) {
	case swatch:
		dx, dy, err := dxdy(0.5, 0.5, param)
		if err != nil {
			return nil, err
		}
		dst := text.Outlined[*image.RGBA]{
			Text:         image.NewRGBA(rect),
			Background:   image.NewRGBA(rect),
			OutlineColor: bg,
		}
		if rect == src.Bounds() {
			draw.Copy(dst.Background, rect.Min, src, src.Bounds(), draw.Over, nil)
		} else {
			panic("invalid swatch bounds")
		}
		text.Draw(text.Shrink{Image: dst, Margin: 1}, title, fg, basicfont.Face7x13, dx, dy, true)
		return dst, nil
	case *animation.GIF:
		if len(src.Image) == 0 {
			return src, nil
		}
		dx, dy, err := dxdy(0.5, 0.9, param)
		if err != nil {
			return nil, err
		}
		mask := text.Outlined[*image.RGBA]{
			Text:         image.NewRGBA(rect),
			Background:   image.NewRGBA(rect),
			OutlineColor: bg,
		}
		text.Draw(text.Shrink{Image: mask, Margin: 1}, title, fg, basicfont.Face7x13, dx, dy, true)
		for i, frame := range src.Image {
			src.Image[i] = image.NewPaletted(rect, src.Image[0].Palette)
			draw.BiLinear.Scale(src.Image[i], text.KeepAspectRatio(rect, frame), frame, frame.Bounds(), draw.Src, nil)
			draw.Copy(src.Image[i], rect.Min, mask, mask.Bounds(), draw.Over, nil)
		}
		src.Config.Width = rect.Dx()
		src.Config.Height = rect.Dy()
		return src, nil
	default:
		dx, dy, err := dxdy(0.5, 0.9, param)
		if err != nil {
			return nil, err
		}
		dst := text.Outlined[*image.RGBA]{
			Text:         image.NewRGBA(rect),
			Background:   image.NewRGBA(rect),
			OutlineColor: bg,
		}
		if rect == src.Bounds() {
			draw.Copy(dst.Background, rect.Min, src, src.Bounds(), draw.Over, nil)
		} else {
			draw.BiLinear.Scale(dst.Background, text.KeepAspectRatio(dst, src), src, src.Bounds(), draw.Src, nil)
		}
		text.Draw(text.Shrink{Image: dst, Margin: 1}, title, fg, basicfont.Face7x13, dx, dy, true)
		return dst, nil
	}
}

func dxdy(dx, dy float64, param map[string]string) (_dx, _dy float64, err error) {
	if v, ok := param["dx"]; ok {
		_dx, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return dx, dy, err
		}
	} else {
		_dx = dx
	}
	if v, ok := param["dy"]; ok {
		_dy, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return dx, dy, err
		}
	} else {
		_dy = dy
	}
	return _dx, _dy, nil
}

func fgbg(fg, bg color.Color, param map[string]string) (_fg, _bg color.Color, err error) {
	if v, ok := param["fg"]; ok {
		_fg, err = paramColor(v)
		if err != nil {
			return fg, bg, err
		}
	} else {
		_fg = fg
	}
	if v, ok := param["bg"]; ok {
		_bg, err = paramColor(v)
		if err != nil {
			return fg, bg, err
		}
	} else {
		_bg = bg
	}
	return _fg, _bg, nil
}

func paramColor(val string) (color.Color, error) {
	var (
		col color.Color
		err error
	)
	if strings.HasPrefix(val, "#") {
		col, err = webColor(val)
		if err != nil {
			return nil, err
		}
	} else {
		var ok bool
		col, ok = ansiColor[val]
		if !ok {
			return nil, fmt.Errorf("invalid color name: %s", val)
		}
	}
	return col, nil
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

func webColor(val string) (color.Color, error) {
	val, ok := strings.CutPrefix(val, "#")
	if !ok {
		return nil, fmt.Errorf("invalid web color: %s", val)
	}
	c, err := strconv.ParseUint(val, 16, 24)
	if err != nil {
		return nil, err
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(c))
	return color.NRGBA{R: b[1], G: b[2], B: b[3], A: 0xff}, nil
}
