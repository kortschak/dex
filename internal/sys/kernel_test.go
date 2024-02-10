// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sys

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"time"

	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/rpc"
)

type testKernel struct {
	*recorder
}

func newTestKernel() *testKernel {
	return &testKernel{
		recorder: &recorder{},
	}
}

func (k *testKernel) newKernel(ctx context.Context, network string, options jsonrpc2.NetListenOptions, log *slog.Logger) (*testKernel, error) {
	k.log = log.With(slog.String("component", kernelUID.String()))
	return k, nil
}

func (k *testKernel) Builtin(ctx context.Context, uid string, dialer net.Dialer, binder jsonrpc2.Binder) error {
	return k.addAction("builtin", uid)
}

func (k *testKernel) Funcs(funcs rpc.Funcs) {
	var names []any
	if len(funcs) != 0 {
		names = make([]any, 0, len(funcs))
		for n := range funcs {
			names = append(names, n)
		}
	}
	k.addAction("funcs", names...)
}

func (k *testKernel) Handle(ctx context.Context, req *jsonrpc2.Request) (result any, err error) {
	k.addAction("handle", req.Method, req.Params)
	return nil, nil
}

func (k *testKernel) Conn(ctx context.Context, uid string) (rpc.Connection, time.Time, bool) {
	k.addAction("conn", uid)
	return &testConn{uid: uid, recorder: k.recorder}, time.Time{}, true
}

func (k *testKernel) Spawn(ctx context.Context, stdout, stderr io.Writer, _ func(), uid, name string, args ...string) error {
	return k.addAction("spawn", uid, name, args)
}

func (k *testKernel) SetDrop(uid string, drop func() error) error {
	val := "none"
	if drop != nil {
		val = fmt.Sprint(drop())
	}
	return k.addAction("set drop", uid, val)
}

func (k *testKernel) Kill(uid string) {
	k.addAction("kill", uid)
}

func (k *testKernel) Close() error {
	return k.addAction("close")
}

type testConn struct {
	uid string
	*recorder
}

func (c *testConn) Call(ctx context.Context, method string, params any) *jsonrpc2.AsyncCall {
	c.addAction("call", c.uid, method, params)
	return &jsonrpc2.AsyncCall{}
}

func (c *testConn) Respond(id jsonrpc2.ID, result any, err error) error {
	return c.addAction("respond", c.uid, fmt.Sprint(id.Raw()), result)
}

func (c *testConn) Cancel(id jsonrpc2.ID) {
	c.addAction("cancel", c.uid, fmt.Sprint(id.Raw()))
}

func (c *testConn) Notify(ctx context.Context, method string, params any) error {
	return c.addAction("notify", c.uid, method, params)
}

type binder string

func (binder) Bind(context.Context, *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
	panic("not implemented")
}
