// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package api defines RPC messages used to communicate with the runner module.
package api

import (
	"log/slog"
	"time"

	"github.com/kortschak/dex/rpc"
)

// Config defines configuration options.
type Config struct {
	LogLevel  *slog.Level `json:"log_level,omitempty"`
	AddSource *bool       `json:"log_add_source,omitempty"`
	Options   struct {
		Strategy  string            `json:"strategy,omitempty"`
		Polling   *rpc.Duration     `json:"polling,omitempty"`
		Heartbeat *rpc.Duration     `json:"heartbeat,omitempty"`
		Rules     map[string]string `json:"rules,omitempty"`
	} `json:"options,omitempty"`
}

// Notification is the RPC parameter for a change call.
type Notification struct {
	UID    rpc.UID        `json:"uid"` // If UID is set, the notify is forwarded.
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

// Details is the set of variable provided to rules. Each field is
// provided to the CEL environment as a global and the previous
// evaluation's values are available as fields of the global, last.
type Details struct {
	WindowID   int       `json:"wid"`
	ProcessID  int       `json:"pid"`
	Name       string    `json:"name"`
	Class      string    `json:"class"`
	WindowName string    `json:"window"`
	LastInput  time.Time `json:"last_input"`
	Locked     bool      `json:"locked"`
}
