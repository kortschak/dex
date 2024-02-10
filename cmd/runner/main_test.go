// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kortschak/jsonrpc2"
	"golang.org/x/sys/execabs"

	runner "github.com/kortschak/dex/cmd/runner/api"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func TestDaemon(t *testing.T) {
	exePath := filepath.Join(t.TempDir(), "runner")
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

			uid := rpc.UID{Module: "runner"}
			err = kernel.Spawn(ctx, os.Stdout, g.NewHandler("🔶 "), nil, uid.Module,
				exePath, "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn runner: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatalf("failed to get daemon conn: %v: %v", ctx.Err(), context.Cause(ctx))
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

				// Get the command output directly to avoid
				// system differences causing problems.
				var stdout strings.Builder
				cmd := execabs.Command("ls", ".")
				cmd.Stdout = &stdout
				err = cmd.Run()
				if err != nil {
					t.Errorf("failed run exec: %v", err)
				}
				want := runner.Return{
					Stdout: stdout.String(),
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
