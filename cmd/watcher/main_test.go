// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/kortschak/jsonrpc2"
	"golang.org/x/sys/execabs"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func TestActive(t *testing.T) {
	err := execabs.Command("go", "build", "-o", "./tester/tester", "./tester").Run()
	if err != nil {
		t.Fatalf("failed to build tester: %v", err)
	}
	t.Cleanup(func() {
		err := os.Remove("./tester/tester")
		if err != nil {
			t.Errorf("failed to clean up tester: %v", err)
		}
	})

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
					t.Logf("watcher log:\n%s", &watcherLogBuf)
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
					t.Logf("watcher log:\n%s", &watcherLogBuf)
				}
			}()

			uid := rpc.UID{Module: "watcher"}
			err = kernel.Spawn(ctx, os.Stdout, &watcherLogBuf, uid.Module,
				"go", "run", "-race", ".", "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn watcher: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatal("failed to get daemon conn")
			}

			var (
				gotChanges int
				changeWg   sync.WaitGroup
			)
			kernel.Funcs(rpc.Funcs{
				"change": func(ctx context.Context, id jsonrpc2.ID, m json.RawMessage) (*rpc.Message[any], error) {
					var v rpc.Message[map[string]any]
					err := rpc.UnmarshalMessage(m, &v)
					if err != nil {
						return nil, err
					}
					gotChanges++
					changeWg.Done()
					return nil, nil
				},
				// store is a simulation. In practice this would use an [rpc.Forward]
				// call to an activity store.
				"store": func(ctx context.Context, id jsonrpc2.ID, m json.RawMessage) (*rpc.Message[any], error) {
					var v rpc.Message[map[string]any]
					err := rpc.UnmarshalMessage(m, &v)
					if err != nil {
						return nil, err
					}
					log.LogAttrs(ctx, slog.LevelInfo, "store", slog.Any("details", v.Body))
					return nil, nil
				},
			})

			const changes = 5
			changeWg.Add(changes)
			go func() {
				for i := 0; i < changes; i++ {
					time.Sleep(time.Second)
					cmd := execabs.Command("./tester/tester", "-title", fmt.Sprintf("tester:%d", i))
					err := cmd.Start()
					if err != nil {
						t.Errorf("failed to start terminal %d", i)
					}
					t.Cleanup(func() {
						cmd.Process.Kill()
					})
				}
			}()

			t.Run("configure", func(t *testing.T) {
				period := &rpc.Duration{Duration: time.Second}
				beat := &rpc.Duration{Duration: 5 * time.Second}

				var resp rpc.Message[string]

				type options struct {
					Polling   *rpc.Duration     `json:"polling,omitempty"`
					Heartbeat *rpc.Duration     `json:"heartbeat,omitempty"`
					Rules     map[string]string `json:"rules,omitempty"`
				}
				err := conn.Call(ctx, "configure", rpc.NewMessage(uid, watcher.Config{
					Options: options{
						Polling:   period,
						Heartbeat: beat,
						Rules: map[string]string{
							"change": `
								name.contains('tester') && window_id != last.window_id ?
									[{"method":"change","params":{"page":"tester","details":{"name":name,"window":window}}}]
								:
									[{}]`,
							"activity": `
								!locked && last_input != last.last_input ?
									{"method":"store","params":{"name":name,"window":window,"last_input":last_input}}
								:
									{}`,
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

			time.Sleep(10 * time.Second) // Let some updates and heartbeats past.

			t.Run("stop", func(t *testing.T) {
				err := conn.Notify(ctx, "stop", rpc.NewMessage(uid, rpc.None{}))
				if err != nil {
					t.Errorf("failed stop call: %v", err)
				}
			})

			done := make(chan struct{})
			go func() {
				changeWg.Wait()
				close(done)
			}()
			select {
			case <-time.After(time.Second):
			case <-done:
			}
			const wantChanges = changes
			if gotChanges < wantChanges {
				t.Errorf("failed to get %d changes: got:%d", wantChanges, gotChanges)
			}

			time.Sleep(time.Second) // Let kernel complete final logging.
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}
