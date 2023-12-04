// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/kortschak/jsonrpc2"

	runner "github.com/kortschak/dex/cmd/runner/api"
	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func TestRunner(t *testing.T) {
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			var (
				level        slog.LevelVar
				kernLogBuf   locked.BytesBuffer
				runnerLogBuf locked.BytesBuffer
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
					t.Logf("runner log:\n%s", &runnerLogBuf)
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
					t.Logf("runner log:\n%s", &runnerLogBuf)
				}
			}()

			uid := rpc.UID{Module: "runner"}
			err = kernel.Spawn(ctx, os.Stdout, &runnerLogBuf, uid.Module,
				"go", "run", "-race", ".", "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn runner: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatal("failed to get daemon conn")
			}

			t.Run("configure", func(t *testing.T) {
				beat := &rpc.Duration{Duration: time.Second / 2}
				level := slog.LevelDebug

				var resp rpc.Message[string]

				type options struct {
					Heartbeat *rpc.Duration `json:"heartbeat,omitempty"`
				}
				err := conn.Call(ctx, "configure", rpc.NewMessage(uid, runner.Config{
					LogLevel: &level,
					Options: options{
						Heartbeat: beat,
					},
				})).Await(ctx, &resp)
				if err != nil {
					t.Errorf("failed configure call: %v", err)
				}
				if resp.Body != "done" {
					t.Errorf("unexpected response body: got:%s want:done", resp.Body)
				}
			})

			t.Run("run_notify", func(t *testing.T) {
				const targetFile = "testfile"

				err := os.Remove(targetFile)
				if err != nil && !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("failed to remove testfile: %v", err)
				}

				err = conn.Notify(ctx, "run", rpc.NewMessage(uid, runner.Params{
					Path: "touch",
					Args: []string{targetFile},
				}))
				if err != nil {
					t.Errorf("failed run notify: %v", err)
				}

				var ls rpc.Message[runner.Return]
				err = conn.Call(ctx, "run", rpc.NewMessage(uid, runner.Params{
					Path: "ls",
					Args: []string{"."},
				})).Await(ctx, &ls)
				if err != nil {
					t.Errorf("failed run call: %v", err)
				}

				want := runner.Return{
					// Keep this in sync with the contents of the directory.
					Stdout: `api
main.go
main_test.go
README.md
` + targetFile + "\n",
					Stderr: "",
					Err:    "",
				}
				if ls.Body != want {
					t.Errorf("unexpected result:\ngot: %#v\nwant:%#v", ls.Body, want)
				}
			})

			time.Sleep(time.Second) // Let some heartbeats past.

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
