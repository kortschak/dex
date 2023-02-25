// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"net"

	"github.com/kortschak/jsonrpc2"
)

// Daemon is a kernel-managed process that communicates via JSON RPC 2 calls with
// the kernel.
type Daemon struct {
	uid    string
	kernel *jsonrpc2.Connection
}

// NewDaemon returns a new daemon communicating on the provided network and managed
// by the kernel at the given address.
func NewDaemon(ctx context.Context, network, addr, uid string, dialer net.Dialer, binder jsonrpc2.Binder) (*Daemon, error) {
	d := Daemon{uid: uid}
	var err error
	d.kernel, err = jsonrpc2.Dial(ctx, jsonrpc2.NetDialer(network, addr, dialer), binder)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// UID returns the daemons unique ID.
func (d *Daemon) UID() string {
	return d.uid
}

// Call invokes the target method and returns an object that can be used to await
// the response.
// See [jsonrpc2.Connection.Call].
func (d *Daemon) Call(ctx context.Context, method string, params any) *jsonrpc2.AsyncCall {
	return d.kernel.Call(ctx, method, params)
}

// Cancel cancels the Context passed to the Handle call for the inbound message
// with the given ID.
// See [jsonrpc2.Connection.Cancel].
func (d *Daemon) Cancel(id jsonrpc2.ID) {
	d.kernel.Cancel(id)
}

// Notify invokes the target method but does not wait for a response.
// See [jsonrpc2.Connection.Notify].
func (d *Daemon) Notify(ctx context.Context, method string, params any) error {
	return d.kernel.Notify(ctx, method, params)
}

// Respond delivers a response to an incoming Call.
// See [jsonrpc2.Connection.Respond].
func (d *Daemon) Respond(id jsonrpc2.ID, result any, err error) error {
	return d.kernel.Respond(id, result, err)
}

// Wait blocks until the connection is fully closed, but does not close it.
// See [jsonrpc2.Connection.Wait].
func (d *Daemon) Wait() {
	d.kernel.Wait()
}

// Close sends an "unregister" notification to the daemon's managing kernel,
// stops listening to requests and closes its connection.
// See [jsonrpc2.Connection.Close].
func (d *Daemon) Close() error {
	d.Notify(context.Background(), Unregister, NewMessage(UID{Module: d.uid}, None{}))
	return d.kernel.Close()
}
