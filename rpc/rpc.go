// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rpc provides a kernel message passing RPC system.
package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Daemon methods.
const (
	Who       = "who"       // call Message[None] → Message[None]
	Configure = "configure" // call Message[any] → Message[string]
	Stop      = "stop"      // notify any → nil
)

// Kernel methods. Call methods that are invoked with notify
// are downgraded to notify and the downgrade is logged.
const (
	Call       = "call"       // call Message[Forward[any]] → Message[Message[any]]
	State      = "state"      // call Message[None] → Message[SysState]
	Notify     = "notify"     // notify Message[Forward[any]] → nil
	Unregister = "unregister" // notify Message[None] → nil
	Heartbeat  = "heartbeat"  // notify Message[Deadline] → nil
)

// Message is the message passing container.
type Message[T any] struct {
	Time time.Time `json:"time"`
	UID  UID       `json:"uid,omitempty"`
	Body T         `json:"body,omitempty"`
}

// UID is a component's UID.
type UID struct {
	Module  string `json:"module,omitempty"`
	Service string `json:"service,omitempty"`
}

// IsKernel returns whether the UID refers to a kernel service or component.
func (u UID) IsKernel() bool {
	return u.Module == ""
}

func (u UID) String() string {
	if u.IsKernel() {
		// Special name for kernel.
		u.Module = "*"
	}
	if u.Service == "" {
		return u.Module
	}
	return u.Module + "." + u.Service
}

func (u UID) IsZero() bool {
	return u == UID{}
}

// NewMessage is a convenience Message constructor. It populates the Time
// field and ensures that the daemon's UID is included in the message.
func NewMessage[T any](uid UID, body T) *Message[T] {
	return &Message[T]{
		Time: time.Now(),
		UID:  uid,
		Body: body,
	}
}

// Deadline is the response expected for a heartbeat. The Deadline field
// indicates when the daemon expects to be able to provide the next beat.
type Deadline struct {
	Deadline *time.Time `json:"deadline"`
}

// SysState is the system state returned by a state call.
type SysState struct {
	Network string                 `json:"network"`
	Addr    string                 `json:"addr"`
	Sock    *string                `json:"sock,omitempty"`
	Daemons map[string]DaemonState `json:"daemons,omitempty"`
	Funcs   []string               `json:"funcs,omitempty"`
}

// DaemonState is the state of individual daemons in the system
type DaemonState struct {
	UID           string     `json:"uid"`
	Command       *string    `json:"command,omitempty"` // Command used to start the daemon.
	Builtin       *string    `json:"builtin,omitempty"` // UID of a built-in.
	LastHeartbeat *time.Time `json:"last_heartbeat,omitempty"`
	Deadline      *time.Time `json:"deadline,omitempty"`
	HasDrop       bool       `json:"has_drop"` // Whether the daemon has a drop function registered.
}

// Forward is a message body for call and notify methods that are to be
// passed on to another daemon. UID is the destination daemon, Method is
// the call or notify method and Params is the call or notify parameters.
type Forward[T any] struct {
	UID    UID         `json:"uid,omitempty"`
	Method string      `json:"method,omitempty"`
	Params *Message[T] `json:"params,omitempty"`
}

// Button indicates a button location.
type Button struct {
	Row  int    `json:"row"`
	Col  int    `json:"col"`
	Page string `json:"page,omitempty"`
}

// UnmarshalMessage is a strict equivalent of [json.Unmarshal].
func UnmarshalMessage[T any](data []byte, v *Message[T]) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err != nil {
		return err
	}
	if dec.More() {
		off := dec.InputOffset()
		return fmt.Errorf("invalid character "+quoteChar(data[off])+" after top-level value at offset %d", off)
	}
	return nil
}

// quoteChar formats c as a quoted character literal.
func quoteChar(c byte) string {
	// special cases - different from quoted strings
	if c == '\'' {
		return `'\''`
	}
	if c == '"' {
		return `'"'`
	}

	// use quoted string with different quotation marks
	s := strconv.Quote(string(c))
	return "'" + s[1:len(s)-1] + "'"
}

// None is an empty parameter or response slot.
type None struct{}

// Duration is a helper for duration fields.
type Duration struct {
	time.Duration
}

func (d *Duration) MarshalJSON() ([]byte, error) {
	if d == nil {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	err := json.Unmarshal(data, &text)
	if err != nil {
		return err
	}
	d.Duration, err = time.ParseDuration(text)
	return err
}
