// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package api defines RPC messages used to communicate with the runner module.
package api

import (
	"log/slog"

	"github.com/kortschak/dex/rpc"
)

// Config defines configuration options.
type Config struct {
	LogLevel  *slog.Level `json:"log_level,omitempty"`
	AddSource *bool       `json:"log_add_source,omitempty"`
	Options   struct {
		Heartbeat *rpc.Duration `json:"heartbeat,omitempty"`
		// Module-level server definitions.
		Servers map[string]Server `json:"server,omitempty"`
	} `json:"options,omitempty"`
}

// Service defines service configuration options.
type Service struct {
	Name    string `json:"name,omitempty"`
	Active  *bool  `json:"active,omitempty"`
	Options struct {
		// Service-level server definition.
		Server Server `json:"server,omitempty"`
	} `json:"options,omitempty"`
}

type Server struct {
	// Addr is the dashboard and dump server address.
	Addr string `json:"addr"`
	// Request is the CEL source for the REST end point.
	Request string `json:"request"`
	// Response is the CEL source to transform the response.
	Response string `json:"response"`
}

// Notification is the RPC parameter for a change call.
type Notification struct {
	// UID is the destination UID.
	// If UID is set, the notify is forwarded.
	UID rpc.UID `json:"uid"`
	// Method is the JSON RPC method to call.
	Method string `json:"method"`
	// Params is the JSON RPC parameters for the call.
	Params map[string]any `json:"params,omitempty"`
	// From is the sender UID if not nil.
	// Otherwise the sender is the service's
	// UID.
	From *rpc.UID `json:"from,omitempty"`
}
