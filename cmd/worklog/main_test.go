// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/kortschak/jsonrpc2"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func TestActive(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			var (
				level         slog.LevelVar
				kernLogBuf    locked.BytesBuffer
				watcherLogBuf locked.BytesBuffer
			)
			log := slog.New(slogext.NewJSONHandler(&kernLogBuf, &slogext.HandlerOptions{
				Level:     slog.LevelDebug,
				AddSource: slogext.NewAtomicBool(*lines),
			}))

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			kernel, err := rpc.NewKernel(ctx, network, jsonrpc2.NetListenOptions{}, log)
			if err != nil {
				t.Fatalf("failed to start kernel: %v", err)
			}
			// Catch failures to terminate.
			closed := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					t.Error("failed to close server")
					*verbose = true
					t.Logf("kernel log:\n%s\n", &kernLogBuf)
					t.Logf("worklog log:\n%s", &watcherLogBuf)
				case <-closed:
				}
			}()
			defer func() {
				err = kernel.Close()
				if err != nil {
					t.Errorf("failed to close kernel: %v", err)
				}
				close(closed)

				if *verbose {
					t.Logf("kernel log:\n%s\n", &kernLogBuf)
					t.Logf("worklog log:\n%s", &watcherLogBuf)
				}
			}()

			uid := rpc.UID{Module: "worklog"}
			err = kernel.Spawn(ctx, os.Stdout, &watcherLogBuf, uid.Module,
				"go", "run", "-race", ".", "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn worklog: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatal("failed to get daemon conn")
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

			clock := time.Date(2023, 5, 14, 15, 03, 31, 0, time.UTC)
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
