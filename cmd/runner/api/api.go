// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package api defines RPC messages used to communicate with the runner module.
package api

import (
	"log/slog"
	"time"

	"github.com/kortschak/dex/config"
	"github.com/kortschak/dex/rpc"
)

// Params defines the RPC messages passed to start an executable by the runner
// module. Fields correspond to fields in [os/exec.Cmd].
type Params struct {
	Path      string        `json:"path"`
	Args      []string      `json:"args,omitempty"`
	Env       []string      `json:"env,omitempty"`
	Dir       string        `json:"dir,omitempty"`
	Stdin     string        `json:"stdin,omitempty"`
	Timeout   time.Duration `json:"timeout,omitempty"`
	WaitDelay time.Duration `json:"wait_delay,omitempty"`
}

// Return defines the returned results from an executable run by the runner
// using an RPC call method. Fields correspond to fields in [os/exec.Cmd]
// except for Err which holds the formatted error status after [os/exec.Cmd.Run]
// returns.
type Return struct {
	Stdout string `json:"stdout,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	Err    string `json:"err,omitempty"`
}

// Config defines configuration options.
type Config struct {
	LogLevel  *slog.Level `json:"log_level,omitempty"`
	AddSource *bool       `json:"log_add_source,omitempty"`
	Options   struct {
		Heartbeat *rpc.Duration `json:"heartbeat,omitempty"`
	} `json:"options"`
}

// Service defines service configuration options.
// This is currently only used for config validation.
type Service struct {
	Name   string          `json:"name,omitempty"`
	Active *bool           `json:"active,omitempty"`
	Serial *string         `json:"serial,omitempty"`
	Listen []config.Button `json:"listen,omitempty"`
}
