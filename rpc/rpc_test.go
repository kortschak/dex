// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"encoding/json"
	"flag"
	"log/slog"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/version"
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
							version, err := version.String()
							if err != nil {
								version = err.Error()
							}
							return NewMessage(uid, version), nil
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
							version, err := version.String()
							if err != nil {
								version = err.Error()
							}
							return NewMessage(uid, version), nil
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

var unmarshalMessageTests = []struct {
	name    string
	data    string
	want    Message[Button] // Any type will do.
	wantErr error
}{
	{
		name: "empty",
		data: "",
		wantErr: &jsonrpc2.WireError{
			Code:    1,
			Message: "EOF",
			Data:    json.RawMessage(`{"type":13,"msg":""}`),
		},
	},
	{
		name: "missing_close",
		data: `{"time":"2006-01-02T15:04:05Z","uid":{"module":"m","service":"s"},"body":{}`,
		wantErr: &jsonrpc2.WireError{
			Code:    1,
			Message: "unexpected EOF",
			Data:    json.RawMessage(`{"type":13,"msg":"eyJ0aW1lIjoiMjAwNi0wMS0wMlQxNTowNDowNVoiLCJ1aWQiOnsibW9kdWxlIjoibSIsInNlcnZpY2UiOiJzIn0sImJvZHkiOnt9"}`),
		},
	},
	{
		name: "extra_field",
		data: `{"time":"2006-01-02T15:04:05Z","uid":{"module":"m","service":"s"},"body":{"book":9}}`,
		wantErr: &jsonrpc2.WireError{
			Code:    1,
			Message: `json: unknown field "book"`,
			Data:    json.RawMessage(`{"type":12,"msg":"eyJ0aW1lIjoiMjAwNi0wMS0wMlQxNTowNDowNVoiLCJ1aWQiOnsibW9kdWxlIjoibSIsInNlcnZpY2UiOiJzIn0sImJvZHkiOnsiYm9vayI6OX19"}`),
		},
	},
	{
		name: "missing_open",
		data: `"time":"2006-01-02T15:04:05Z","uid":{"module":"m","service":"s"},"body":{"book":9}}`,
		wantErr: &jsonrpc2.WireError{
			Code:    1,
			Message: "json: cannot unmarshal string into Go value of type rpc.Message[github.com/kortschak/dex/rpc.Button]",
			Data:    json.RawMessage(`{"type":14,"offset":6,"msg":"InRpbWUiOiIyMDA2LTAxLTAyVDE1OjA0OjA1WiIsInVpZCI6eyJtb2R1bGUiOiJtIiwic2VydmljZSI6InMifSwiYm9keSI6eyJib29rIjo5fX0="}`),
		},
	},
	{
		name: "syntax_error",
		data: "not json",
		wantErr: &jsonrpc2.WireError{
			Code:    1,
			Message: "invalid character 'o' in literal null (expecting 'u')",
			Data:    json.RawMessage(`{"type":11,"offset":2,"msg":"bm90IGpzb24="}`),
		},
	},
	{
		name: "valid",
		data: `{"time":"2006-01-02T15:04:05Z","uid":{"module":"m","service":"s"},"body":{"row":1,"col":2,"page":"three"}}`,
		want: Message[Button]{
			Time: time.Date(2006, time.January, 02, 15, 4, 5, 0, time.UTC),
			UID:  UID{Module: "m", Service: "s"},
			Body: Button{Row: 1, Col: 2, Page: "three"},
		},
	},
}

func TestUnmarshalMessage(t *testing.T) {
	for _, test := range unmarshalMessageTests {
		t.Run(test.name, func(t *testing.T) {
			var got Message[Button]
			err := UnmarshalMessage[Button]([]byte(test.data), &got)
			if !cmp.Equal(test.wantErr, err) {
				t.Errorf("unexpected error:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.wantErr, err))
			}
			if err != nil {
				var data struct {
					Massage []byte `json:"msg"`
				}
				err := json.Unmarshal(err.(*jsonrpc2.WireError).Data, &data)
				if err != nil {
					t.Fatalf("unexpected error recovering error data: %v", err)
				}
				if string(data.Massage) != test.data {
					t.Errorf("unexpected error data message:\ngot: %s\nwant:%s", data.Massage, test.data)
				}
				return
			}
			if !cmp.Equal(test.want, got) {
				t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.want, got))
			}
		})
	}
}
