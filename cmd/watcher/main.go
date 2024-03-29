// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The watcher executable is a dex module for notifying dex of details of
// the active running application and user input activity.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/kortschak/jsonrpc2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
	"github.com/kortschak/dex/internal/celext"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/version"
	"github.com/kortschak/dex/rpc"
)

// Exit status codes.
const (
	success       = 0
	internalError = 1 << (iota - 1)
	invocationError
)

func main() { os.Exit(Main()) }

func Main() int {
	network := flag.String("network", "", "network for communication (unix or tcp)")
	addr := flag.String("addr", "", "address for communication")
	uid := flag.String("uid", "", "unique ID")
	logging := flag.String("log", "info", "logging level (debug, info, warn or error)")
	lines := flag.Bool("lines", false, "display source line details in logs")
	logStdout := flag.Bool("log_stdout", false, "log to stdout instead of stderr")
	v := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *v {
		err := version.Print()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(internalError)
		}
		os.Exit(success)
	}

	switch *network {
	case "unix", "tcp":
	default:
		flag.Usage()
		return invocationError
	}

	switch "" {
	case *addr, *uid:
		flag.Usage()
		return invocationError
	default:
	}

	var level slog.LevelVar
	err := level.UnmarshalText([]byte(*logging))
	if err != nil {
		flag.Usage()
		return invocationError
	}
	addSource := slogext.NewAtomicBool(*lines)
	logDst := os.Stderr
	if *logStdout {
		logDst = os.Stdout
	}
	log := slog.New(slogext.GoID{Handler: slogext.NewJSONHandler(logDst, &slogext.HandlerOptions{
		Level:     &level,
		AddSource: addSource,
	})}).With(
		slog.String("component", *uid),
	)

	ctx, cancel := context.WithCancel(context.Background())
	waitParent(func() {
		log.LogAttrs(ctx, slog.LevelError, "dex died")
		cancel()
	})

	h := newDaemon(*uid, log, &level, addSource, cancel)
	err = h.dial(ctx, *network, *addr, net.Dialer{})
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, err.Error())
		return internalError
	}
	defer h.close()

	log.LogAttrs(ctx, slog.LevelInfo, "start")
	<-ctx.Done()
	log.LogAttrs(ctx, slog.LevelInfo, "exit")

	return success
}

func waitParent(fn func()) {
	go func() {
		os.Stdin.Read([]byte{0})
		fn()
	}()
}

func newDaemon(uid string, log *slog.Logger, level *slog.LevelVar, addSource *atomic.Bool, cancel context.CancelFunc) *daemon {
	return &daemon{
		uid:       uid,
		log:       log,
		level:     level,
		addSource: addSource,
		cancel:    cancel,
	}
}

func (d *daemon) dial(ctx context.Context, network, addr string, dialer net.Dialer) error {
	d.log.LogAttrs(ctx, slog.LevelDebug, "dial", slog.String("network", network), slog.String("addr", addr))
	var err error
	d.conn, err = jsonrpc2.Dial(ctx, jsonrpc2.NetDialer(network, addr, dialer), d)
	if err != nil {
		return err
	}
	return nil
}

func (d *daemon) close() error {
	d.conn.Notify(context.Background(), rpc.Unregister, rpc.NewMessage(rpc.UID{Module: d.uid}, rpc.None{}))
	return d.conn.Close()
}

type daemon struct {
	uid string

	// conn is the connection to the kernel.
	conn *jsonrpc2.Connection

	log       *slog.Logger
	level     *slog.LevelVar
	addSource *atomic.Bool
	cancel    context.CancelFunc

	pMu     sync.Mutex
	polling time.Duration
	pStop   chan struct{}
	rules   atomic.Value // map[string]cel.Program

	hMu       sync.Mutex
	heartbeat time.Duration
	hStop     chan struct{}
}

func (d *daemon) Bind(ctx context.Context, conn *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
	d.conn = conn
	d.log.LogAttrs(ctx, slog.LevelDebug, "bind")
	return jsonrpc2.ConnectionOptions{
		Handler: d,
	}
}

func (d *daemon) Handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	d.log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("req", slogext.Request{Request: req}))

	switch req.Method {
	case rpc.Who:
		return rpc.NewMessage(rpc.UID{Module: d.uid}, rpc.None{}), nil

	case rpc.Configure:
		var m rpc.Message[watcher.Config]
		err := rpc.UnmarshalMessage(req.Params, &m)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
			return nil, err
		}

		if m.Body.LogLevel != nil {
			d.level.Set(*m.Body.LogLevel)
		}
		if m.Body.AddSource != nil {
			d.addSource.Store(*m.Body.AddSource)
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "configure", slog.Any("details", m))

		if m.Body.Options.Rules != nil {
			rules := make(map[string]cel.Program)
			for name, src := range m.Body.Options.Rules {
				prg, err := compile(src, d.log)
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelError, "compiling rule", slog.String("name", name), slog.Any("error", err))
				} else {
					rules[name] = prg
				}
			}
			d.rules.Store(rules)
		}
		if m.Body.Options.Polling != nil {
			d.poll(ctx, m.Body.Options.Polling.Duration)
		}
		if m.Body.Options.Heartbeat != nil {
			d.beat(ctx, m.Body.Options.Heartbeat.Duration)
		}

		if !req.ID.IsValid() {
			return nil, nil
		}
		return rpc.NewMessage(m.UID, "done"), nil

	case rpc.Stop:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop")
		d.cancel()
		return nil, nil

	default:
		return nil, jsonrpc2.ErrNotHandled
	}
}

func (d *daemon) poll(ctx context.Context, p time.Duration) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	d.pMu.Lock()
	defer d.pMu.Unlock()

	if p == d.polling {
		return
	}

	if d.pStop != nil {
		close(d.pStop)
		d.pStop = nil
	}

	switch {
	case p == 0:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop polling")
		d.polling = p

	case p > 0:
		d.log.LogAttrs(ctx, slog.LevelInfo, "update polling", slog.Duration("old", d.polling), slog.Duration("new", p))
		stop := make(chan struct{})
		d.pStop = stop
		go func() {
			ticker := time.NewTicker(p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var last watcher.Details
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
					return
				case <-stop:
					ticker.Stop()
					return
				case t := <-ticker.C:
					d.log.LogAttrs(ctx, slog.LevelDebug, "polling", slog.Any("tick", t))
					details, err := activeWindow()
					if err != nil {
						var warn warning
						if errors.As(err, &warn) {
							d.log.LogAttrs(ctx, slog.LevelWarn, "polling", slog.Any("error", warn.error))
						} else {
							d.log.LogAttrs(ctx, slog.LevelError, "polling", slog.Any("error", err))
						}
					}
					d.log.LogAttrs(ctx, slog.LevelDebug, "watcher details", slog.Any("details", details))
					rules, _ := d.rules.Load().(map[string]cel.Program)
					if rules == nil {
						break
					}
					for name, prg := range rules {
						notes, err := eval(prg, t, details, last, p)
						if err != nil {
							d.log.LogAttrs(ctx, slog.LevelError, "polling evaluation", slog.Any("name", name), slog.Any("error", err))
							continue
						}
						for i, note := range notes {
							if note.Method == "" {
								continue
							}
							d.log.LogAttrs(ctx, slog.LevelDebug, "note details", slog.String("rule", name), slog.Any("number", i), slog.Any("note", note))
							if note.UID.IsZero() {
								err = d.conn.Notify(ctx, note.Method,
									rpc.Message[map[string]any]{
										Time: t, UID: rpc.UID{Module: d.uid},
										Body: note.Params,
									},
								)
							} else {
								err = d.conn.Notify(ctx, rpc.Notify,
									rpc.Message[rpc.Forward[map[string]any]]{
										Time: t, UID: rpc.UID{Module: d.uid},
										Body: rpc.Forward[map[string]any]{
											UID:    note.UID,
											Method: note.Method,
											Params: rpc.NewMessage(rpc.UID{Module: d.uid}, note.Params),
										},
									},
								)
							}
							if err != nil {
								d.log.LogAttrs(ctx, slog.LevelError, "polling", slog.Any("error", err))
							}
						}
					}
					last = details
				}
			}
		}()
		d.polling = p

	default:
		d.log.LogAttrs(ctx, slog.LevelError, "update polling", slog.Duration("invalid duration", p))
	}
}

func compile(src string, log *slog.Logger) (cel.Program, error) {
	env, err := cel.NewEnv(
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		celext.Lib(log),
		cel.Declarations(
			decls.NewVar("time", decls.Timestamp),
			decls.NewVar("period", decls.Duration),
			decls.NewVar("window_id", decls.Int),
			decls.NewVar("name", decls.String),
			decls.NewVar("class", decls.String),
			decls.NewVar("window", decls.String),
			decls.NewVar("last_input", decls.Timestamp),
			decls.NewVar("locked", decls.Bool),
			decls.NewVar("last", decls.NewMapType(decls.String, decls.Dyn)),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create env: %v", err)
	}

	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return nil, fmt.Errorf("failed compilation: %v", iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("failed program instantiation: %v", err)
	}
	return prg, nil
}

func eval(prg cel.Program, ts time.Time, curr, last watcher.Details, period time.Duration) ([]watcher.Notification, error) {
	out, _, err := prg.Eval(map[string]any{
		"time":   ts,
		"period": period,

		"window_id":  curr.WindowID,
		"name":       curr.Name,
		"class":      curr.Class,
		"window":     curr.WindowName,
		"last_input": curr.LastInput,
		"locked":     curr.Locked,
		"last": map[string]any{
			"window_id":  last.WindowID,
			"name":       last.Name,
			"class":      last.Class,
			"window":     last.WindowName,
			"last_input": last.LastInput,
			"locked":     last.Locked,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed eval: %v", err)
	}

	v, err := out.ConvertToNative(reflect.TypeOf((*structpb.Value)(nil)))
	if err != nil {
		return nil, fmt.Errorf("failed proto conversion: %v", err)
	}
	b, err := protojson.MarshalOptions{}.Marshal(v.(proto.Message))
	if err != nil {
		return nil, fmt.Errorf("failed native conversion: %v", err)
	}
	var note watcher.Notification
	errNote := json.Unmarshal(b, &note)
	if errNote == nil {
		return []watcher.Notification{note}, nil
	}
	var notes []watcher.Notification
	errNotes := json.Unmarshal(b, &notes)
	if errNotes == nil {
		return notes, nil
	}
	return nil, fmt.Errorf("failed json conversion: %v", errors.Join(errNote, errNotes))
}

func (d *daemon) beat(ctx context.Context, p time.Duration) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	d.hMu.Lock()
	defer d.hMu.Unlock()

	if p == d.heartbeat {
		return
	}

	if d.hStop != nil {
		close(d.hStop)
		d.hStop = nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	switch {
	case p == 0:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop heartbeat")
		err := d.conn.Notify(ctx, "heartbeat",
			rpc.NewMessage(rpc.UID{Module: d.uid}, rpc.Deadline{}),
		)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "heartbeat", slog.Any("error", err))
		}
		d.heartbeat = p

	case p > 0:
		d.log.LogAttrs(ctx, slog.LevelInfo, "update heartbeat", slog.Duration("old", d.heartbeat), slog.Duration("new", p))
		stop := make(chan struct{})
		d.hStop = stop
		go func() {
			ticker := time.NewTicker(p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
					return
				case <-stop:
					ticker.Stop()
					return
				case t := <-ticker.C:
					d.log.LogAttrs(ctx, slog.LevelDebug, "heartbeat", slog.Any("tick", t))
					deadline := t.Add(2 * p)
					err := d.conn.Notify(ctx, "heartbeat",
						rpc.Message[rpc.Deadline]{
							Time: t, UID: rpc.UID{Module: d.uid},
							Body: rpc.Deadline{Deadline: &deadline},
						},
					)
					if err != nil {
						d.log.LogAttrs(ctx, slog.LevelError, "heartbeat", slog.Any("error", err))
					}
				}
			}
		}()
		d.heartbeat = p

	default:
		d.log.LogAttrs(ctx, slog.LevelError, "update heartbeat", slog.Duration("invalid duration", p))
	}
}
