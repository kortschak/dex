// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/kortschak/jsonrpc2"
	"golang.org/x/sys/execabs"

	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/xdg"
)

// RuntimeDir is the path within XDG_RUNTIME_DIR that unix sockets
// are created in if the unix network is used for communication.
const RuntimeDir = "dex"

// Kernel is a JSON RPC 2 based message passing kernel.
type Kernel struct {
	listener *netListener
	server   *jsonrpc2.Server
	network  string
	sock     string

	log *slog.Logger

	dMu     sync.Mutex
	daemons map[string]*daemon

	fMu   sync.Mutex
	funcs Funcs
}

type daemon struct {
	uid       string
	cmd       *execabs.Cmd
	keepalive *os.File
	builtin   *Daemon

	// conn is the connection to the daemon
	// from the kernel.
	conn *jsonrpc2.Connection
	// receive from ready will not block when
	// conn is ready to use.
	ready chan struct{}

	lastHeartbeat time.Time
	deadline      time.Time

	// drop is called when the daemon is
	// unregistered if not nil.
	drop func() error
}

// NewKernel returns a new Kernel communicating over the provided network
// which may be either "unix" or "tcp".
func NewKernel(ctx context.Context, network string, options jsonrpc2.NetListenOptions, log *slog.Logger) (*Kernel, error) {
	k := Kernel{
		network: network,
		daemons: make(map[string]*daemon),
		funcs:   make(Funcs),
		log:     log.With(slog.String("component", kernelUID.String())),
	}
	var err error

	laddr := "localhost:0"
	if k.network == "unix" {
		dir, err := xdg.Runtime(RuntimeDir)
		if err != nil {
			if err != syscall.ENOENT {
				return nil, err
			}
			var ok bool
			dir, ok = xdg.RuntimeDir()
			if !ok {
				return nil, errors.New("no xdg runtime directory")
			}
			dir = filepath.Join(dir, RuntimeDir)
			err = os.Mkdir(dir, 0o700)
			if err != nil {
				return nil, fmt.Errorf("failed to create runtime directory: %w", err)
			}
		}
		k.sock, err = os.MkdirTemp(dir, fmt.Sprintf("sock-%d-*", os.Getpid()))
		if err != nil {
			return nil, err
		}
		laddr = filepath.Join(k.sock, "kernel")
		k.log.LogAttrs(ctx, slog.LevelDebug, "kernel socket", slog.String("path", laddr))
	}

	k.listener, err = newNetListener(ctx, k.network, laddr, options)
	if err != nil {
		return nil, err
	}
	k.server = jsonrpc2.NewServer(ctx, k.listener, &k)

	k.log.LogAttrs(ctx, slog.LevelDebug, "new kernel", slog.String("network", k.network), slog.Any("addr", slogext.Stringer{Stringer: k.listener.Addr()}))
	return &k, nil
}

var kernelUID = UID{Module: "kernel", Service: "rpc"}

// Addr returns the listener address of the kernel.
func (k *Kernel) Addr() net.Addr {
	return k.listener.Addr()
}

// Funcs is a mapping from method names to insertable functions. A name with
// a nil function removes the mapping.
//
// If the ID is valid, the function must return either a non-nil, JSON-marshalable
// result, or a non-nil error. If it is not valid, the functions must return a nil
// result.
type Funcs map[string]func(context.Context, jsonrpc2.ID, json.RawMessage) (*Message[any], error)

// Funcs inserts the provided functions into the kernel's handler. If funcs is nil
// the entire kernel mapping table is reset.
func (k *Kernel) Funcs(funcs Funcs) {
	k.fMu.Lock()
	defer k.fMu.Unlock()
	if funcs == nil {
		k.funcs = make(Funcs)
		return
	}
	for name, fn := range funcs {
		if fn != nil {
			k.funcs[name] = fn
		} else {
			delete(k.funcs, name)
		}
	}
}

// dial returns a new direct unmanaged connection to the kernel server.
func (k *Kernel) dial(ctx context.Context, dialer net.Dialer) (*jsonrpc2.Connection, error) {
	return jsonrpc2.Dial(ctx, jsonrpc2.NetDialer(k.network, k.listener.Addr().String(), dialer), jsonrpc2.ConnectionOptions{})
}

// Bind binds the kernel's handler to a connection and the reverse connection
// to a daemon's UID.
func (k *Kernel) Bind(ctx context.Context, conn *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
	k.log.LogAttrs(ctx, slog.LevelDebug, "binding")
	go k.bind(ctx, conn)
	return jsonrpc2.ConnectionOptions{
		Handler: k,
	}
}

func (k *Kernel) bind(ctx context.Context, conn *jsonrpc2.Connection) {
	var daemon Message[None]
	err := conn.Call(ctx, Who, NewMessage(kernelUID, None{})).Await(ctx, &daemon)
	k.log.LogAttrs(ctx, slog.LevelDebug, "binding response", slog.Any("message", daemon))
	switch {
	case err == nil:
	case errors.Is(err, context.Canceled):
		return
	case errors.Is(err, jsonrpc2.ErrClientClosing):
		k.log.LogAttrs(ctx, slog.LevelInfo, "closing", slog.Any("error", err))
		return
	case errors.Is(err, jsonrpc2.ErrNotHandled), errors.Is(err, jsonrpc2.ErrMethodNotFound):
		k.log.LogAttrs(ctx, slog.LevelDebug, "not a daemon", slog.Any("error", err))
		return
	default:
		k.log.LogAttrs(ctx, slog.LevelError, "failed uid call", slog.Any("error", err))
		return
	}

	k.log.LogAttrs(ctx, slog.LevelInfo, "binding", slog.Any("uid", daemon.UID))
	k.dMu.Lock()
	defer k.dMu.Unlock()
	d := k.daemons[daemon.UID.Module]
	if d == nil {
		k.log.LogAttrs(ctx, slog.LevelError, "unexpected connection", slog.Any("uid", daemon.UID))
		return
	}
	if d.conn != nil {
		// UID is already registered, log and ask the second to stop.
		k.log.LogAttrs(ctx, slog.LevelError, "duplicate uid", slog.String("uid", daemon.UID.Module))
		err := conn.Notify(ctx, Stop, NewMessage(kernelUID, "duplicate"))
		if err != nil {
			k.log.LogAttrs(ctx, slog.LevelError, "failed stop", slog.Any("error", err))
		}
		return
	}
	k.daemons[daemon.UID.Module].conn = conn
	close(k.daemons[daemon.UID.Module].ready)
}

// Handle is the kernel's message handler.
func (k *Kernel) Handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	k.log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("req", slogext.Request{Request: req}))

	switch req.Method {
	case Call, Notify:
		var m Message[Forward[any]]
		err := UnmarshalMessage(req.Params, &m)
		if err != nil {
			k.log.LogAttrs(ctx, slog.LevelError, req.Method, slog.Any("error", err))
			return nil, err
		}
		return k.call(ctx, req, m)

	case Unregister:
		var m Message[None]
		err := UnmarshalMessage(req.Params, &m)
		if err != nil {
			k.log.LogAttrs(ctx, slog.LevelError, req.Method, slog.Any("error", err))
			return nil, err
		}
		return k.unregister(ctx, req, m)

	case Heartbeat:
		var m Message[Deadline]
		err := UnmarshalMessage(req.Params, &m)
		if err != nil {
			k.log.LogAttrs(ctx, slog.LevelError, req.Method, slog.Any("error", err))
			return nil, err
		}
		return k.heartbeat(ctx, req, m)

	case State:
		var m Message[None]
		err := UnmarshalMessage(req.Params, &m)
		if err != nil {
			k.log.LogAttrs(ctx, slog.LevelError, req.Method, slog.Any("error", err))
			return nil, err
		}
		return k.state(ctx, req, m)

	default:
		k.fMu.Lock()
		fn, ok := k.funcs[req.Method]
		k.fMu.Unlock()
		if !ok {
			return nil, jsonrpc2.ErrNotHandled
		}

		res, err := fn(ctx, req.ID, req.Params)
		var ret any
		// Convert *Message[any] to any without type if nil.
		if res != nil {
			ret = res
		}
		if !req.IsCall() {
			if ret != nil {
				k.log.LogAttrs(ctx, slog.LevelWarn, "dropping func result", slog.String("method", req.Method), slog.Any("result", ret))
			}
			if err != nil {
				k.log.LogAttrs(ctx, slog.LevelError, "func notify error", slog.String("method", req.Method), slog.Any("error", err))
			}
			return nil, err
		}
		if err != nil {
			ret = nil
		} else if ret == nil {
			// Make sure a call has a return if there is no error.
			ret = NewMessage(kernelUID, "ok")
		}
		return ret, err

	}
}

func (k *Kernel) call(ctx context.Context, req *jsonrpc2.Request, m Message[Forward[any]]) (any, error) {
	k.log.LogAttrs(ctx, slog.LevelDebug, req.Method, slog.Any("message", m))
	conn, heartbeat, ok := k.Conn(ctx, m.Body.UID.Module)
	if !ok {
		if heartbeat.IsZero() {
			return nil, NewError(ErrCodeInvalidData,
				fmt.Sprintf("no daemon %s", m.Body.UID),
				map[string]any{
					"type": ErrCodeNoDaemon,
					"uid":  m.Body.UID,
				},
			)
		}
		return nil, NewError(ErrCodeInvalidData,
			fmt.Sprintf("no daemon %s: last heartbeat: %v (%v)",
				m.Body.UID, heartbeat, time.Since(heartbeat)),
			map[string]any{
				"type":      ErrCodeNoDaemon,
				"heartbeat": heartbeat,
				"uid":       m.Body.UID,
			},
		)
	}
	if !req.IsCall() {
		if req.Method == Call {
			k.log.LogAttrs(ctx, slog.LevelWarn, "downgrading call to notify", slog.Any("message", m))
		}
		err := conn.Notify(ctx, m.Body.Method, m.Body.Params)
		if err != nil {
			if heartbeat.IsZero() {
				return nil, err
			}
			return nil, AddWireErrorDetail(err, map[string]any{
				"heartbeat": heartbeat,
				"uid":       m.Body.UID,
			})
		}
		return nil, nil
	}

	var resp Message[any]
	err := conn.Call(ctx, m.Body.Method, m.Body.Params).Await(ctx, &resp)
	if err != nil {
		if heartbeat.IsZero() {
			return nil, err
		}
		return nil, AddWireErrorDetail(err, map[string]any{
			"heartbeat": heartbeat,
			"uid":       m.Body.UID,
		})
	}
	return NewMessage(kernelUID, resp), nil
}

// SetDrop sets a call-back that will be called when the daemon with the
// specified UID is unregistered. This should be used if the daemon uses any
// kernel-adjacent storage that needs to be released when the daemon terminates.
// Conventionally, the kernel-adjacent store will insert a "register" method
// using the Funcs method that will accept the data to be stored and set the
// drop call-back. Subsequent calls to SetDrop will over-write previous drop
// call-backs, and a nil drop is a no-op.
func (k *Kernel) SetDrop(uid string, drop func() error) error {
	k.dMu.Lock()
	defer k.dMu.Unlock()
	d, ok := k.daemons[uid]
	if !ok {
		return fmt.Errorf("unable to set unregister function: %s not known", uid)
	}
	d.drop = drop
	return nil
}

func (k *Kernel) unregister(ctx context.Context, req *jsonrpc2.Request, m Message[None]) (any, error) {
	k.log.LogAttrs(ctx, slog.LevelDebug, req.Method, slog.Any("message", m))
	k.dMu.Lock()
	if d := k.daemons[m.UID.Module]; d != nil {
		const concurrently = true
		k.close(ctx, m.UID.Module, d, concurrently)
	}
	delete(k.daemons, m.UID.Module)
	k.dMu.Unlock()
	return nil, nil
}

func (k *Kernel) heartbeat(ctx context.Context, req *jsonrpc2.Request, m Message[Deadline]) (any, error) {
	k.log.LogAttrs(ctx, slog.LevelDebug, req.Method, slog.Any("message", m))
	k.dMu.Lock()
	d, ok := k.daemons[m.UID.Module]
	if ok {
		if m.Body.Deadline != nil {
			k.log.LogAttrs(ctx, slog.LevelDebug, req.Method, slog.Any("last heartbeat", time.Since(d.lastHeartbeat)))
			d.lastHeartbeat = m.Time
			d.deadline = *m.Body.Deadline
		} else {
			k.log.LogAttrs(ctx, slog.LevelDebug, "stop heartbeat monitor")
			d.lastHeartbeat = time.Time{}
			d.deadline = time.Time{}
		}
	}
	k.dMu.Unlock()
	return nil, nil
}

func (k *Kernel) state(ctx context.Context, req *jsonrpc2.Request, m Message[None]) (any, error) {
	k.log.LogAttrs(ctx, slog.LevelDebug, req.Method, slog.Any("message", m))
	state := SysState{
		Network: k.network,
		Addr:    k.listener.Addr().String(),
	}
	if k.sock != "" {
		sock := k.sock
		state.Sock = &sock
	}
	k.dMu.Lock()
	if len(k.daemons) != 0 {
		state.Daemons = make(map[string]DaemonState)
	}
	for uid, d := range k.daemons {
		ds := DaemonState{
			UID:     d.uid,
			HasDrop: d.drop != nil,
		}
		if d.cmd != nil {
			cmd := d.cmd.String()
			ds.Command = &cmd
		}
		if d.builtin != nil {
			bid := d.builtin.UID()
			ds.Builtin = &bid
		}
		if hb := d.lastHeartbeat; !hb.IsZero() {
			ds.LastHeartbeat = &hb
		}
		if dl := d.deadline; !dl.IsZero() {
			ds.Deadline = &dl
		}
		state.Daemons[uid] = ds
	}
	k.dMu.Unlock()
	k.fMu.Lock()
	for fn := range k.funcs {
		state.Funcs = append(state.Funcs, fn)
	}
	k.fMu.Unlock()
	sort.Strings(state.Funcs)
	if req.IsCall() {
		return NewMessage(kernelUID, state), nil
	}
	k.log.LogAttrs(ctx, slog.LevelInfo, "state request", slog.Any("state", state))
	return nil, nil
}

// Spawn starts a new client daemon with the provided UID. The daemon executable
// is started by executing name with the provided args. The new process is given
// stdout and stderr as redirects for those output streams. Spawned daemons are
// expected to send a "register" JSON RPC 2 call to the kernel on start and a
// "deregister" notification before exiting. The child process is passed the
// read end of a pipe on stdin. No writes are ever made by the parent, but the
// child may use the pipe to detect termination of the parent.
func (k *Kernel) Spawn(ctx context.Context, stdout, stderr io.Writer, done func(), uid, name string, args ...string) error {
	k.dMu.Lock()
	defer k.dMu.Unlock()

	_, exists := k.daemons[uid]
	if exists {
		return fmt.Errorf("attempt to reuse UID: %q", uid)
	}

	args = append(args[:len(args):len(args)],
		"-uid", uid,
		"-network", k.network,
		"-addr", k.listener.Addr().String())
	cmd := execabs.CommandContext(ctx, name, args...)
	k.log.LogAttrs(ctx, slog.LevelInfo, "spawn", slog.Any("command", slogext.Stringer{Stringer: cmd}), slog.String("uid", uid))
	lifeline, keepalive, err := os.Pipe()
	if err != nil {
		return err
	}
	cmd.Stdin = lifeline
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Start()
	if err != nil {
		return err
	}
	k.log.LogAttrs(ctx, slog.LevelDebug, "started", slog.String("uid", uid), slog.Int("pid", cmd.Process.Pid))

	d := &daemon{uid: uid, cmd: cmd, keepalive: keepalive, ready: make(chan struct{})}
	k.daemons[uid] = d

	// Watch the daemon process in case it fails to deregister.
	go func() {
		// Use the process's wait method to avoid data races in
		// the exec.Cmd type.
		k.log.LogAttrs(ctx, slog.LevelDebug, "waiting for termination", slog.String("uid", uid))
		cmd.Process.Wait()
		k.log.LogAttrs(ctx, slog.LevelDebug, "terminating", slog.String("uid", uid))

		k.dMu.Lock()
		defer k.dMu.Unlock()
		d := k.daemons[uid]
		delete(k.daemons, uid)
		if d == nil {
			// The daemon already unregistered.
			return
		}
		k.log.LogAttrs(ctx, slog.LevelInfo, "cleanup zombie", slog.Any("uid", uid))
		const concurrently = false
		k.close(ctx, uid, d, concurrently)
		if done != nil {
			go done()
		}
	}()
	return nil
}

// Builtin starts a new virtual client daemon with the provided UID using the
// provided binder.
func (k *Kernel) Builtin(ctx context.Context, uid string, dialer net.Dialer, binder jsonrpc2.Binder) error {
	k.dMu.Lock()
	defer k.dMu.Unlock()

	_, exists := k.daemons[uid]
	if exists {
		return fmt.Errorf("attempt to reuse UID: %q", uid)
	}

	k.log.LogAttrs(ctx, slog.LevelDebug, "built-in", slog.String("uid", uid))
	builtin, err := NewDaemon(ctx, k.network, k.listener.Addr().String(), uid, dialer, binder)
	if err != nil {
		return err
	}
	k.daemons[uid] = &daemon{uid: uid, builtin: builtin, ready: make(chan struct{})}

	return nil
}

// Kill terminates the daemon identified by uid. If no corresponding daemon
// exists, it is a no-op.
func (k *Kernel) Kill(uid string) {
	k.log.LogAttrs(context.Background(), slog.LevelDebug, "kill", slog.String("uid", uid))
	k.dMu.Lock()
	if d, ok := k.daemons[uid]; ok {
		k.kill(uid, d)
	}
	k.dMu.Unlock()
}

// Close closes the kernel, terminating all spawned daemons.
func (k *Kernel) Close() error {
	k.log.LogAttrs(context.Background(), slog.LevelDebug, "close")
	k.dMu.Lock()
	for uid, d := range k.daemons {
		k.kill(uid, d)
	}
	k.dMu.Unlock()

	k.server.Shutdown()
	err := k.server.Wait()
	if k.sock != "" {
		k.log.LogAttrs(context.Background(), slog.LevelDebug, "remove sockets dir", slog.String("dir", k.sock))
		err := os.RemoveAll(k.sock)
		if err != nil {
			k.log.LogAttrs(context.Background(), slog.LevelWarn, "failed to remove sockets dir", slog.Any("error", err))
		}
	}
	return err
}

// grace is the amount of time given to children to terminate cleanly.
const grace = time.Second

func (k *Kernel) kill(uid string, d *daemon) {
	ctx := context.Background()
	k.log.LogAttrs(ctx, slog.LevelDebug, "sending stop", slog.String("uid", uid))
	if d.conn == nil {
		k.log.LogAttrs(ctx, slog.LevelDebug, "connection to stop not established", slog.String("uid", uid))
	} else {
		err := d.conn.Notify(ctx, "stop", NewMessage(kernelUID, None{}))
		if err != nil {
			level := slog.LevelError
			if errors.Is(err, jsonrpc2.ErrNotHandled) || errors.Is(err, jsonrpc2.ErrMethodNotFound) || errors.Is(err, jsonrpc2.ErrClientClosing) {
				level = slog.LevelWarn
			}
			k.log.LogAttrs(ctx, level, "sending stop", slog.String("uid", uid), slog.Any("error", err))
		}
	}

	k.log.LogAttrs(ctx, slog.LevelDebug, "closing connection", slog.String("uid", uid))
	const concurrently = false
	k.close(ctx, uid, d, concurrently)

	if d.builtin != nil {
		k.log.LogAttrs(ctx, slog.LevelDebug, "closing build-in", slog.String("uid", uid))
		err := d.builtin.Close()
		if err != nil && !errors.Is(err, context.Canceled) {
			k.log.LogAttrs(ctx, slog.LevelError, "closing built-in", slog.String("uid", uid), slog.Any("error", err))
		}
	}

	if d.cmd != nil {
		k.log.LogAttrs(ctx, slog.LevelDebug, "terminating child", slog.String("uid", uid))

		// Don't allow close to be permanently
		// delayed by badly behaving children.
		timer := time.NewTimer(grace)
		done := make(chan struct{})
		go func() {
			// Use the process's wait method to avoid data races in
			// the exec.Cmd type.
			d.cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
			timer.Stop()
		case <-timer.C:
			ctx := context.Background()
			pid := d.cmd.Process.Pid
			k.log.LogAttrs(ctx, slog.LevelWarn, "slow child",
				slog.Int("pid", pid),
				slog.Any("cmd", slogext.Stringer{Stringer: d.cmd}),
				slog.Any("killed", d.cmd.Process.Kill()),
			)
		}
	}
	delete(k.daemons, uid)
}

func (k *Kernel) close(ctx context.Context, uid string, d *daemon, concurrently bool) {
	close := func() {
		defer d.keepalive.Close()
		k.log.LogAttrs(ctx, slog.LevelDebug, "closing connection", slog.String("uid", uid))
		if d.conn == nil {
			k.log.LogAttrs(ctx, slog.LevelDebug, "connection to close not established", slog.String("uid", uid))
			return
		}
		err := d.conn.Close()
		if err != nil && !errors.Is(err, context.Canceled) {
			k.log.LogAttrs(ctx, slog.LevelError, "closing conn", slog.String("uid", uid), slog.Any("error", err))
		}
	}
	if concurrently {
		// We can end up deadlocked if we wait for the conn to close
		// in some circumstances, so do this is a separate goroutine.
		// In particular, during unregister we would end up deadlocked
		// on conn.Close() and the processing of the unregister call
		// within jsonrpc2 since *Connection.updateInFlight holds the
		// state lock for both.
		go close()
	} else {
		close()
	}
	if d.drop != nil {
		k.log.LogAttrs(ctx, slog.LevelDebug, "dropping", slog.String("uid", uid))
		err := d.drop()
		if err != nil {
			k.log.LogAttrs(ctx, slog.LevelError, "drop", slog.Any("error", err))
		}
	}
}

// Connection is a connection to a managed daemon.
type Connection interface {
	Call(ctx context.Context, method string, params any) *jsonrpc2.AsyncCall
	Respond(id jsonrpc2.ID, result any, err error) error
	Cancel(id jsonrpc2.ID)
	Notify(ctx context.Context, method string, params any) error
}

// Conn returns a connection to the daemon with the given UID.
func (k *Kernel) Conn(ctx context.Context, uid string) (Connection, time.Time, bool) {
	k.dMu.Lock()
	k.log.LogAttrs(ctx, slog.LevelDebug, "conn", slog.Any("want", uid), slog.Any("available", k.daemons))
	d, ok := k.daemons[uid]
	k.dMu.Unlock()
	if !ok {
		return nil, time.Time{}, false
	}
	select {
	case <-ctx.Done():
		return nil, d.lastHeartbeat, false
	case <-d.ready:
		return d.conn, d.lastHeartbeat, ok
	}
}
