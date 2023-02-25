// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"context"
	"io"
	"net"
	"os"

	"github.com/kortschak/jsonrpc2"
)

// newNetListener returns a new Listener that listens on a socket using the net package.
func newNetListener(ctx context.Context, network, address string, options jsonrpc2.NetListenOptions) (*netListener, error) {
	ln, err := options.NetListenConfig.Listen(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &netListener{net: ln}, nil
}

// netListener is the implementation of jsonrpc2.Listener for connections made using the net package.
type netListener struct {
	net net.Listener
}

// Addr returns the NetListener's network address.
func (l *netListener) Addr() net.Addr {
	return l.net.Addr()
}

// Accept blocks waiting for an incoming connection to the listener.
func (l *netListener) Accept(context.Context) (io.ReadWriteCloser, error) {
	return l.net.Accept()
}

// Close will cause the listener to stop listening. It will not close any connections that have
// already been accepted.
func (l *netListener) Close() error {
	addr := l.net.Addr()
	err := l.net.Close()
	if addr.Network() == "unix" {
		rerr := os.Remove(addr.String())
		if rerr != nil && err == nil {
			err = rerr
		}
	}
	return err
}

// Dialer returns a nil jsonrpc2.Dialer.
func (l *netListener) Dialer() jsonrpc2.Dialer {
	return nil
}
