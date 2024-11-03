// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The dex command is the minidex integrator.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"

	"github.com/kortschak/ardilla"

	"github.com/kortschak/dex/internal/device"
	"github.com/kortschak/dex/internal/state"
	"github.com/kortschak/dex/internal/xdg"
	"github.com/kortschak/dex/minidex/plugins"
)

func main() {
	os.Exit(Main())
}

func Main() int {
	pids := []ardilla.PID{
		ardilla.StreamDeckMini,
		ardilla.StreamDeckMiniV2,
		ardilla.StreamDeckOriginal,
		ardilla.StreamDeckOriginalV2,
		ardilla.StreamDeckMK2,
		ardilla.StreamDeckXL,
		ardilla.StreamDeckPedal,
	}

	logging := flag.String("log", "info", "logging level (debug, info, warn or error)")
	flag.Parse()

	var level slog.LevelVar
	err := level.UnmarshalText([]byte(*logging))
	if err != nil {
		flag.Usage()
		os.Exit(2)
	}

	log := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level:     &level,
		AddSource: true,
	}))

	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, "cancel", cancel) //lint:ignore SA1029 We are the only people here.
	defer cancel()

	statedir, err := xdg.State("minidex")
	if err != nil {
		state, ok := xdg.StateHome()
		if !ok {
			fmt.Fprintln(os.Stderr, "failed to find XDG state directory")
			return 1
		}
		statedir = filepath.Join(state, "minidex")
		err = os.MkdirAll(statedir, 0o750)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create data store directory: %v\n", err)
			return 1
		}
	}
	datapath := filepath.Join(statedir, "state.sqlite")

	store, err := state.Open(datapath, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open data store: %v\n", err)
		return 1
	}
	defer store.Close()

	for dev, pages := range plugins.Actions {
		var pid ardilla.PID
		for _, id := range pids {
			if dev.Model == id.String() {
				pid = id
				break
			}
		}

		// We can pass a nil kernel into device.NewController since
		// minidex is monolithic.
		controller, err := device.NewController(ctx, nil, pid, dev.Serial, log)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open manager: %v\n", err)
			return 1
		}
		defer controller.Close()

		rows, cols := controller.Layout()
		for name, page := range pages {
			name := string(name)
			controller.NewPage(name)
			p, ok := controller.Page(name)
			if !ok {
				log.LogAttrs(ctx, slog.LevelError, "missing page", slog.String("page", name))
				return 1
			}
			for pos, action := range page {
				if pos.Row < 0 || rows <= pos.Row {
					fmt.Fprintf(os.Stderr, "invalid row: %d\n", pos.Row)
					return 1
				}
				if pos.Col < 0 || cols <= pos.Col {
					fmt.Fprintf(os.Stderr, "invalid col: %d\n", pos.Col)
					return 1
				}
				button := p.Button(pos.Row, pos.Col)
				plug := plugins.New(controller, button, store, log, &level)
				img, err := action.Init(ctx, plug)
				if err != nil {
					fmt.Fprintf(os.Stderr, "init failure for row %d col %d: %v\n", pos.Row, pos.Col, err)
					return 1
				}
				button.Draw(ctx, img)
				if err != nil {
					fmt.Fprintf(os.Stderr, "failed to init plugin: %v\n", err)
					return 1
				}
				if a, ok := action.(plugins.Presser); ok {
					button.OnPress(a.Press)
				}
				if a, ok := action.(plugins.Releaser); ok {
					button.OnRelease(a.Release)
				}
			}
		}
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	select {
	case <-c:
	case <-ctx.Done():
	}

	return 0
}
