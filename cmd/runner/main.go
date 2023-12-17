// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The runner executable is a dex module for running arbitrary programs
// using dex.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kortschak/jsonrpc2"
	"golang.org/x/sys/execabs"

	runner "github.com/kortschak/dex/cmd/runner/api"
	"github.com/kortschak/dex/config"
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

func newDaemon(uid string, log *slog.Logger, level *slog.LevelVar, addSource *atomic.Bool, cancel context.CancelFunc) *daemon {
	return &daemon{
		uid:       uid,
		log:       log,
		level:     level,
		addSource: addSource,
		cancel:    cancel,
		waiting:   make(map[*execabs.Cmd]context.CancelFunc),
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

	wMu     sync.Mutex
	waiting map[*execabs.Cmd]context.CancelFunc

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

	case "run":
		var m rpc.Message[runner.Params]
		err := rpc.UnmarshalMessage(req.Params, &m)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "run", slog.Any("error", err))
			return nil, err
		}
		typ := "notify"
		if req.IsCall() {
			typ = "call"
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "run", slog.Any("type", typ), slog.Any("details", m))

		p := m.Body
		if p.Path == "" {
			err = errors.New("missing executable path")
			d.log.LogAttrs(ctx, slog.LevelError, err.Error())
			return nil, err
		}

		// Currently the parent context is cancelled for a notify
		// when the handle call completes, so we need a separate
		// context to control the child process.
		// We keep a list of waiting running notify-started children
		// to cancel when runner exits.
		cctx := context.Background()
		// Cannot defer cancel Timeout, otherwise notifies will be
		// cancelled on handle completion. Make sure it is called
		// in all paths and in the case of a notify, that it is
		// called after cmd.Wait returns.
		var cancel context.CancelFunc
		if p.Timeout > 0 {
			cctx, cancel = context.WithTimeout(cctx, p.Timeout)
		} else {
			cctx, cancel = context.WithCancel(cctx)
		}

		cmd := execabs.CommandContext(cctx, p.Path, p.Args...)
		cmd.Env = p.Env
		cmd.Dir = p.Dir
		cmd.WaitDelay = p.WaitDelay
		if p.Stdin != "" {
			cmd.Stdin = strings.NewReader(p.Stdin)
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "run", slog.Any("command", cmd.String()))
		if !req.IsCall() {
			err = cmd.Start()
			if err != nil {
				cancel()
				return nil, err
			}
			go func() {
				d.wMu.Lock()
				d.waiting[cmd] = cancel
				d.wMu.Unlock()
				cmd.Wait()
				d.wMu.Lock()
				delete(d.waiting, cmd)
				d.wMu.Unlock()
				cancel()
			}()
			return nil, nil
		}
		defer cancel() // Mop up remaining cases.
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err = cmd.Run()
		var exitErr *execabs.ExitError
		if errors.As(err, &exitErr) {
			d.log.LogAttrs(ctx, slog.LevelError, err.Error(), slog.Any("cmd", p))
			return nil, err
		}
		resp := runner.Return{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
		}
		if err != nil {
			resp.Err = err.Error()
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "run", slog.Any("cmd", p), slog.Any("result", resp))
		return rpc.NewMessage(m.UID, resp), nil

	case rpc.Configure:
		isService, err := config.IsService(req)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
			return nil, err
		}
		var uid rpc.UID
		if isService {
			// This is currently only for config validation since service
			// configs are handled by the kernel for runner, being only
			// button actions. This may change.
			var m rpc.Message[runner.Service]
			err = rpc.UnmarshalMessage(req.Params, &m)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
				return nil, err
			}
			uid = m.UID
		} else {
			var m rpc.Message[runner.Config]
			err := rpc.UnmarshalMessage(req.Params, &m)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
				return nil, err
			}
			uid = m.UID

			if m.Body.LogLevel != nil {
				d.level.Set(*m.Body.LogLevel)
			}
			if m.Body.AddSource != nil {
				d.addSource.Store(*m.Body.AddSource)
			}
			d.log.LogAttrs(ctx, slog.LevelDebug, "configure", slog.Any("details", m))

			if m.Body.Options.Heartbeat != nil {
				d.beat(ctx, m.Body.Options.Heartbeat.Duration)
			}
		}

		if !req.ID.IsValid() {
			return nil, nil
		}
		return rpc.NewMessage(uid, "done"), nil

	case rpc.Stop:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop")

		// Clean up any still running children.
		d.wMu.Lock()
		for _, cancel := range d.waiting {
			cancel()
		}
		d.wMu.Unlock()

		d.cancel()
		return nil, nil

	default:
		return nil, jsonrpc2.ErrNotHandled
	}
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
