// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/jsonrpc2"
	"golang.org/x/sys/execabs"
	"golang.org/x/tools/txtar"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
	"github.com/kortschak/dex/cmd/worklog/store"
	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	update  = flag.Bool("update", false, "update tests")
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
	keep    = flag.Bool("keep", false, "keep database directories after tests")
)

func TestDaemon(t *testing.T) {
	exePath := filepath.Join(t.TempDir(), "worklog")
	out, err := execabs.Command("go", "build", "-o", exePath, "-race").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build daemon: %v\n%s", err, out)
	}

	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			verbose := slogext.NewAtomicBool(*verbose)
			var (
				level slog.LevelVar
				buf   bytes.Buffer
			)
			h := slogext.NewJSONHandler(&buf, &slogext.HandlerOptions{
				Level:     &level,
				AddSource: slogext.NewAtomicBool(*lines),
			})
			g := slogext.NewPrefixHandlerGroup(&buf, h)
			level.Set(slog.LevelDebug)
			log := slog.New(g.NewHandler("🔷 "))

			ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Second, errors.New("test waited too long"))
			defer cancel()

			kernel, err := rpc.NewKernel(ctx, network, jsonrpc2.NetListenOptions{}, log)
			if err != nil {
				t.Fatalf("failed to start kernel: %v", err)
			}
			// Catch failures to terminate.
			closed := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case <-ctx.Done():
					t.Error("failed to close server")
					verbose.Store(true)
				case <-closed:
				}
			}()
			defer func() {
				err = kernel.Close()
				if err != nil {
					t.Errorf("failed to close kernel: %v", err)
				}
				close(closed)
				wg.Wait()
				if verbose.Load() {
					t.Logf("log:\n%s\n", &buf)
				}
			}()

			uid := rpc.UID{Module: "worklog"}
			err = kernel.Spawn(ctx, os.Stdout, g.NewHandler("🔶 "), uid.Module,
				exePath, "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn worklog: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatalf("failed to get daemon conn: %v: %v", ctx.Err(), context.Cause(ctx))
			}

			t.Run("configure", func(t *testing.T) {
				beat := &rpc.Duration{Duration: time.Second / 2}

				var resp rpc.Message[string]

				type options struct {
					Web         *worklog.Web            `json:"web,omitempty"`
					DatabaseDir string                  `json:"database_dir,omitempty"` // Relative to XDG_STATE_HOME.
					Hostname    string                  `json:"hostname,omitempty"`
					Heartbeat   *rpc.Duration           `json:"heartbeat,omitempty"`
					Rules       map[string]worklog.Rule `json:"rules,omitempty"`
				}
				err := conn.Call(ctx, "configure", rpc.NewMessage(uid, worklog.Config{
					Options: options{
						Heartbeat: beat,
						Rules: map[string]worklog.Rule{
							"afk":    {Name: "afk-watcher", Type: "afkstatus", Src: `{"bucket":"afk","end":new.last_input}`},
							"app":    {Name: "app-watcher", Type: "app", Src: `{"bucket":"app","end":new.time,"data":{"name":new.name}}`},
							"window": {Name: "window-watcher", Type: "currentwindow", Src: `{"bucket":"window","end":new.time,"data":{"window":new.window}}`},
						},
					},
				})).Await(ctx, &resp)
				if err != nil {
					t.Errorf("failed configure call: %v", err)
				}
				if resp.Body != "done" {
					t.Errorf("unexpected response body: got:%s want:done", resp.Body)
				}
			})

			clock := time.Date(2023, time.May, 14, 15, 3, 31, 0, time.UTC)
			now := func(delta time.Duration) time.Time {
				clock = clock.Add(delta)
				return clock
			}
			events := []worklog.Report{
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program1", WindowName: "Task", LastInput: now(0).Add(-time.Second)}},
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program1", WindowName: "Task1", LastInput: now(0).Add(-time.Second)}},
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program2", WindowName: "Task2", LastInput: now(0).Add(-2 * time.Second)}},
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program2", WindowName: "Task3", LastInput: now(0).Add(-time.Second)}},
			}
			for _, e := range events {
				err := conn.Notify(ctx, "record", rpc.NewMessage(uid, e))
				if err != nil {
					t.Errorf("failed run notify: %v", err)
				}
			}

			time.Sleep(1 * time.Second) // Let some updates and heartbeats past.

			t.Run("stop", func(t *testing.T) {
				err := conn.Notify(ctx, "stop", rpc.NewMessage(uid, rpc.None{}))
				if err != nil {
					t.Errorf("failed stop call: %v", err)
				}
			})

			time.Sleep(time.Second) // Let kernel complete final logging.
		})
	}
}

func TestContinuation(t *testing.T) {
	tests, err := filepath.Glob(filepath.Join("testdata", "*.txtar"))
	if err != nil {
		t.Fatalf("failed to get tests: %v", err)
	}
	for _, path := range tests {
		ext := filepath.Ext(path)
		base := strings.TrimSuffix(path, ext)
		name := strings.TrimSuffix(filepath.Base(path), ext)
		t.Run(name, func(t *testing.T) {
			var (
				level     slog.LevelVar
				addSource = slogext.NewAtomicBool(*lines)
				buf       locked.BytesBuffer
			)
			log := slog.New(slogext.NewJSONHandler(&buf, &slogext.HandlerOptions{
				Level:     slog.LevelDebug,
				AddSource: addSource,
			}))
			defer func() {
				if *verbose {
					t.Logf("log:\n%s\n", &buf)
				}
				if !*keep {
					os.RemoveAll(base)
				}
			}()

			a, err := txtar.ParseFile(path)
			if err != nil {
				t.Fatalf("failed to read test data: %v", err)
			}
			var src, data, want []byte
			for _, f := range a.Files {
				switch f.Name {
				case "src.cel":
					src = f.Data
				case "data.json":
					data = f.Data
				case "want.json":
					want = f.Data
				}
			}
			if want == nil && !*update {
				t.Fatal("no want file in test")
			}

			err = os.RemoveAll(base)
			if err != nil && !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("failed to clean db: %v", err)
			}
			err = os.Mkdir(base, 0o755)
			if err != nil {
				t.Fatalf("failed to make db directory: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			d := newDaemon("worklog", log, &level, addSource, ctx, cancel)
			err = d.openDB(ctx, nil, filepath.Join(base, "db.sqlite3"), "localhost")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			d.configureRules(ctx, map[string]worklog.Rule{
				"afk": {
					Name: "afk",
					Type: "afk",
					Src:  string(src),
				},
			})
			db := d.db.Load().(*store.DB)
			defer db.Close()
			d.configureDB(ctx, db)

			dec := json.NewDecoder(bytes.NewReader(data))
			var last, curr worklog.Report
			for {
				err = dec.Decode(&curr)
				if err != nil {
					if err == io.EOF {
						break
					}
					t.Fatalf("unexpected error reading test data: %v", err)
				}
				d.record(ctx, rpc.UID{Module: "watcher"}, curr, last)
				last = curr
			}

			dump, err := db.Dump()
			if err != nil {
				t.Fatalf("failed to dump db: %v", err)
			}
			var got bytes.Buffer
			enc := json.NewEncoder(&got)
			for _, b := range dump {
				for _, e := range b.Events {
					enc.Encode(e)
				}
			}
			if *update {
				if want == nil {
					a.Files = append(a.Files, txtar.File{
						Name: "want.json",
						Data: got.Bytes(),
					})
				} else {
					for i, f := range a.Files {
						if f.Name == "want.json" {
							a.Files[i].Data = got.Bytes()
						}
					}
				}
				err = os.WriteFile(path, txtar.Format(a), 0o644)
				if err != nil {
					t.Fatalf("failed to write updated test: %v", err)
				}
				return
			}
			if !bytes.Equal(want, got.Bytes()) {
				t.Errorf("unexpected dump result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got.Bytes()))
			}
		})
	}
}
