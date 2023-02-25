// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sys

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/kortschak/jsonrpc2"
)

type listenOptionsValue struct {
	options jsonrpc2.NetListenOptions
}

type netListenOptions struct {
	ListenConfig listenConfig `json:"listen_config"`
	Dialer       dialer       `json:"dialer"`
}

type listenConfig struct {
	KeepAlive time.Duration `json:"keep_alive"`
}

type dialer struct {
	Timeout       time.Duration `json:"timeout,omitempty"`
	Deadline      time.Time     `json:"deadline,omitempty"`
	LocalAddr     net.Addr      `json:"addr,omitempty"`
	DualStack     bool          `json:"dual_stack,omitempty"`
	FallbackDelay time.Duration `json:"fall_back_delay,omitempty"`
	KeepAlive     time.Duration `json:"keep_alive,omitempty"`
	Resolver      *resolver     `json:"resolver,omitempty"`
}

type resolver struct {
	PreferGo     bool `json:"prefer_go,omitempty"`
	StrictErrors bool `json:"strict_errors,omitempty"`
}

func (v listenOptionsValue) LogValue() slog.Value {
	var resolver *resolver
	if v.options.NetDialer.Resolver != nil {
		resolver.PreferGo = v.options.NetDialer.Resolver.PreferGo
		resolver.StrictErrors = v.options.NetDialer.Resolver.StrictErrors
	}
	return slog.AnyValue(netListenOptions{
		ListenConfig: listenConfig{
			v.options.NetListenConfig.KeepAlive,
		},
		Dialer: dialer{
			Timeout:       v.options.NetDialer.Timeout,
			Deadline:      v.options.NetDialer.Deadline,
			LocalAddr:     v.options.NetDialer.LocalAddr,
			DualStack:     v.options.NetDialer.DualStack, //lint:ignore SA1019 Kept for compatibility.
			FallbackDelay: v.options.NetDialer.FallbackDelay,
			KeepAlive:     v.options.NetDialer.KeepAlive,
			Resolver:      resolver,
		},
	})
}

type logWriter struct {
	ctx     context.Context
	log     *slog.Logger
	level   slog.Level
	msg     string
	label   string
	maxLine int
}

func (l *logWriter) Write(b []byte) (int, error) {
	if !l.log.Enabled(l.ctx, l.level) {
		return len(b), nil
	}
	n := len(b)
	for len(b) != 0 {
		n := l.maxLine
		if n > len(b) {
			n = len(b)
		}
		l.log.LogAttrs(l.ctx, l.level, l.msg, slog.String(l.label, string(b[:n])))
		b = b[n:]
	}
	return n, nil
}
