// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"github.com/gofrs/flock"

	public "github.com/kortschak/dex/config"
	"github.com/kortschak/dex/internal/config"
	"github.com/kortschak/dex/internal/device"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/state"
	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/internal/version"
	"github.com/kortschak/dex/internal/xdg"
	"github.com/kortschak/dex/rpc"
)

func main() {
	logging := flag.String("log", "info", "logging level (debug, info, warn or error)")
	lines := flag.Bool("lines", false, "display source line details in logs")
	v := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *v {
		err := version.Print()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	var level slog.LevelVar
	err := level.UnmarshalText([]byte(*logging))
	if err != nil {
		flag.Usage()
		os.Exit(2)
	}
	addSource := slogext.NewAtomicBool(*lines)

	// log is the root logger.
	log := slog.New(slogext.GoID{Handler: slogext.NewJSONHandler(os.Stderr, &slogext.HandlerOptions{
		Level:     &level,
		AddSource: addSource,
	})})
	// mlog is the logger for main.
	mlog := log.With(slog.String("component", "dex.main"))

	runtimeDir, err := xdg.Runtime(rpc.RuntimeDir)
	if err != nil {
		if err != syscall.ENOENT {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var ok bool
		runtimeDir, ok = xdg.RuntimeDir()
		if !ok {
			fmt.Fprintln(os.Stderr, "no xdg runtime directory")
			os.Exit(1)
		}
		runtimeDir = filepath.Join(runtimeDir, rpc.RuntimeDir)
		err = os.Mkdir(runtimeDir, 0o700)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	pidFile := filepath.Join(runtimeDir, "pid")
	fl := flock.New(pidFile)
	ok, err := fl.TryLock()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "dex is already running")
		os.Exit(1)
	}
	defer func() {
		fl.Unlock()
		os.Remove(pidFile)
	}()
	pid := fmt.Sprintln(os.Getpid())
	err = os.WriteFile(pidFile, []byte(pid), 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		<-c
		log.LogAttrs(ctx, slog.LevelInfo, "terminating")
		cancel()
	}()

	cfgdir, err := xdg.Config("dex", true)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var ok bool
		cfgdir, ok = xdg.ConfigHome()
		if !ok {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		err := os.Mkdir(filepath.Join(cfgdir, "dex"), 0o755)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		mlog.LogAttrs(ctx, slog.LevelInfo, "created config dir", slog.String("path", cfgdir))
	}
	mlog.LogAttrs(ctx, slog.LevelInfo, "config dir", slog.String("path", cfgdir))

	datadir, err := xdg.State("dex")
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		var ok bool
		datadir, ok = xdg.StateHome()
		if !ok {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		cfgdir = filepath.Join(datadir, "dex")
		err := os.Mkdir(cfgdir, 0o755)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		mlog.LogAttrs(ctx, slog.LevelInfo, "created data dir", slog.String("path", datadir))
	}
	mlog.LogAttrs(ctx, slog.LevelInfo, "data dir", slog.String("path", datadir))

	datapath := filepath.Join(datadir, "state.sqlite3")
	store, err := state.Open(datapath, log)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open data store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	sysman, err := sys.NewManager[*rpc.Kernel, *device.Manager, *device.Button](
		rpc.NewKernel, device.NewManager[*rpc.Kernel],
		store, datadir, log, &level, addSource)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start manager: %v\n", err)
		os.Exit(1)
	}
	defer sysman.Close()

	funcs, err := mergeFuncs(sysman, log, sys.Funcs, device.Funcs, state.Funcs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to configure kernel plugins: %v\n", err)
		os.Exit(1)
	}
	sysman.SetFuncs(funcs)

	cfgman := config.NewManager(log)

	changes := make(chan config.Change)
	go func() {
		err := config.Watch(ctx, cfgdir, changes, -1, log)
		if err != nil {
			mlog.LogAttrs(ctx, slog.LevelError, err.Error())
			os.Exit(1)
		}
	}()

	for cfg := range changes {
		if cfg.Err != nil {
			mlog.LogAttrs(ctx, slog.LevelWarn, "config stream error", slog.Any("error", cfg.Err))
			continue
		}
		mlog.LogAttrs(ctx, slog.LevelDebug, "config stream element", slog.Any("config", cfg.Config), slog.Any("events", cfg.Event))
		err = cfgman.Apply(cfg)
		if err != nil {
			mlog.LogAttrs(ctx, slog.LevelWarn, "config manager apply error", slog.Any("error", err))
			continue
		}
		unified, cue, included, remain, err := cfgman.Unify(public.Schema)
		if err != nil {
			mlog.LogAttrs(ctx, slog.LevelWarn, "config manager unify error", slog.Any("error", err), slog.Any("cue", cue))
			continue
		}
		mlog.LogAttrs(ctx, slog.LevelDebug, "config manager files", slog.Any("included", included), slog.Any("remain", remain))
		err = sysman.Configure(ctx, unified)
		if err != nil {
			mlog.LogAttrs(ctx, slog.LevelWarn, "manager configure error", slog.Any("error", err))
		}
	}
}

func mergeFuncs[K sys.Kernel, D sys.Device[B], B sys.Button](
	manager *sys.Manager[K, D, B], log *slog.Logger, list ...func(*sys.Manager[K, D, B], *slog.Logger) rpc.Funcs,
) (rpc.Funcs, error) {
	switch len(list) {
	case 0:
		return nil, nil
	case 1:
		return list[0](manager, log), nil
	}
	var intersect []string
	dst := maps.Clone(list[0](manager, log))
	for _, mk := range list[1:] {
		funcs := mk(manager, log)
		for k := range funcs {
			if _, ok := dst[k]; ok {
				intersect = append(intersect, k)
			}
		}
		if len(intersect) != 0 {
			sort.Strings(intersect)
			return nil, fmt.Errorf("kernel func name collisions: %s", strings.Join(intersect, " "))
		}
		maps.Copy(dst, funcs)
	}
	return dst, nil
}
