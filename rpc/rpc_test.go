// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"os"
	"testing"

	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/xdg"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func TestPingPongForward(t *testing.T) {
	t.Cleanup(func() {
		dir, err := xdg.Runtime(RuntimeDir)
		if err == nil {
			os.RemoveAll(dir)
		}
	})
	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			var logBuf locked.BytesBuffer
			log := slog.New(slogext.NewJSONHandler(&logBuf, &slogext.HandlerOptions{
				Level:     slog.LevelDebug,
				AddSource: slogext.NewAtomicBool(*lines),
			}))

			ctx := context.Background()

			kernel, err := NewKernel(ctx, network, jsonrpc2.NetListenOptions{}, log)
			if err != nil {
				t.Fatalf("failed to start kernel: %v", err)
			}
			defer func() {
				err = kernel.Close()
				if err != nil {
					t.Errorf("failed to close kernel: %v", err)
				}

				if *verbose {
					t.Logf("log:\n%s\n", &logBuf)
				}
			}()

			err = kernel.Builtin(ctx, "ping-server", net.Dialer{}, jsonrpc2.BinderFunc(func(ctx context.Context, _ *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
				uid := UID{Module: "ping-server"}
				log := log.With(slog.String("component", uid.String()))
				return jsonrpc2.ConnectionOptions{
					Handler: jsonrpc2.HandlerFunc(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
						log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("req", slogext.Request{Request: req}))
						if !req.IsCall() {
							return nil, jsonrpc2.ErrNotHandled
						}
						if req.Method == Who {
							return NewMessage(uid, None{}), nil
						}
						if req.Method != "ping" {
							return nil, jsonrpc2.ErrNotHandled
						}
						var m Message[string]
						err := UnmarshalMessage(req.Params, &m)
						if err != nil {
							return nil, err
						}
						answer := "pong"
						if m.Body != "" {
							answer += ":" + m.Body
						}
						resp := NewMessage(uid, answer)
						log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("resp", resp))
						return resp, nil
					}),
				}
			}))
			if err != nil {
				t.Fatalf("failed to install ping-server built-in: %v", err)
			}

			err = kernel.Builtin(ctx, "ping-client", net.Dialer{}, jsonrpc2.BinderFunc(func(ctx context.Context, kernel *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
				uid := UID{Module: "ping-client"}
				log := log.With(slog.String("component", uid.String()))
				return jsonrpc2.ConnectionOptions{
					Handler: jsonrpc2.HandlerFunc(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
						log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("req", slogext.Request{Request: req}))
						if !req.IsCall() {
							return nil, jsonrpc2.ErrNotHandled
						}
						if req.Method == Who {
							return NewMessage(uid, None{}), nil
						}
						if req.Method != "ping-call" {
							return nil, jsonrpc2.ErrNotHandled
						}
						var m Message[string]
						err := UnmarshalMessage(req.Params, &m)
						if err != nil {
							return nil, err
						}
						call := NewMessage(uid, Forward[string]{
							UID:    UID{Module: "ping-server"},
							Method: "ping",
							Params: NewMessage(m.UID, "forwarded"),
						})
						log.LogAttrs(ctx, slog.LevelDebug, "call", slog.Any("call", call))
						var resp Message[Message[string]]
						err = kernel.Call(ctx, Call, call).Await(ctx, &resp)
						if err != nil {
							return nil, err
						}
						log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("resp", resp))
						return resp, nil
					}),
				}
			}))
			if err != nil {
				t.Fatalf("failed to install ping-client built-in: %v", err)
			}

			t.Run("ping_direct", func(t *testing.T) {
				conn, _, ok := kernel.Conn(ctx, "ping-server")
				if ok {
					var pong Message[string]
					err := conn.Call(ctx, "ping", NewMessage(UID{Module: "testing"}, "direct")).Await(ctx, &pong)
					if err != nil {
						t.Fatalf("failed ping call: %v", err)
					}
					if want := "pong:direct"; pong.Body != want {
						t.Errorf("unexpected pong: got:%q want:%q", pong.Body, want)
					}
				}
			})

			t.Run("ping_forwarded", func(t *testing.T) {
				conn, _, ok := kernel.Conn(ctx, "ping-client")
				if ok {
					var pong Message[Message[string]]
					err := conn.Call(ctx, "ping-call", NewMessage(UID{Module: "testing"}, "forwarded")).Await(ctx, &pong)
					if err != nil {
						t.Fatalf("failed ping call: %v", err)
					}

					// Forwarded calls retain all the message envelopes
					// so we expect the first response UID to be kernel
					if want := kernelUID; pong.UID != want {
						t.Errorf("unexpected pong: got:%q want:%q", pong.UID, want)
					}
					// and the contained message to have the ping-server
					// uid.
					if want := (UID{Module: "ping-server"}); pong.Body.UID != want {
						t.Errorf("unexpected pong: got:%q want:%q", pong.Body.UID, want)
					}
					if want := "pong:forwarded"; pong.Body.Body != want {
						t.Errorf("unexpected pong: got:%q want:%q", pong.Body.Body, want)
					}
				}
			})
		})
	}
}
