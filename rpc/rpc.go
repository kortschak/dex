// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package rpc provides a kernel message passing RPC system.
package rpc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/kortschak/jsonrpc2"
)

// Daemon methods.
const (
	Who       = "who"       // call Message[None] → Message[string] (version)
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

// JSON RPC error codes.
const (
	ErrCodeInvalidMessage = 1 // an RPC message is invalid
	// Invalid message sub-codes:
	ErrCodeMessageSyntax       = 11 // syntax
	ErrCodeMessageUnknownField = 12 // unknown field
	ErrCodeShortMessage        = 13 // truncation
	ErrCodeMessageType         = 14 // type mismatch
	ErrCodeMethod              = 15 // method mismatch
	ErrCodeParameters          = 16 // invalid parameters

	ErrCodeDevice = 2 // an error was returned by a device

	ErrCodeInvalidData = 3 // data sent in a call was invalid
	// Invalid data sub-codes:
	ErrCodeNoDaemon  = 31 // missing daemon
	ErrCodeNoPage    = 32 // missing page
	ErrCodeNoDevice  = 33 // missing device
	ErrCodeNoDisplay = 34 // non-graphical device
	ErrCodeBounds    = 35 // out of bounds
	ErrCodeImage     = 36 // image data

	ErrCodeInternal = 4  // an internal error happened
	ErrCodeNoStore  = 41 // no data store
	ErrCodeStoreErr = 42 // store operation error
	ErrCodeProcess  = 43 // child process error
	ErrCodePath     = 44 // path error

	ErrCodeNotFound = 5 // a state key was not present
)

// Message is the message passing container.
type Message[T any] struct {
	Time   time.Time `json:"time"`
	UID    UID       `json:"uid,omitempty"`
	Button *Button   `json:"button,omitempty"`
	Body   T         `json:"body,omitempty"`
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
	Version       string     `json:"version"`
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
		return &jsonrpc2.WireError{
			Code:    ErrCodeInvalidMessage,
			Message: err.Error(),
			Data:    encodeErrData(err, data),
		}
	}
	if dec.More() {
		off := dec.InputOffset()
		return &jsonrpc2.WireError{
			Code:    ErrCodeInvalidMessage,
			Message: fmt.Sprintf("invalid character "+quoteChar(data[off])+" after top-level value at offset %d", off),
			Data:    encodeErrData(&json.SyntaxError{Offset: off}, data),
		}
	}
	return nil
}

// encodeErrData return the JSON encoding for an error's extra data.
func encodeErrData(err error, data []byte) json.RawMessage {
	type extra struct {
		Type    int    `json:"type,omitempty"`
		Offset  int64  `json:"offset,omitempty"`
		Message []byte `json:"msg"`
	}
	e := extra{
		Message: data,
	}
	switch err := err.(type) {
	case nil:
		return nil
	case *json.SyntaxError:
		e.Type = ErrCodeMessageSyntax
		e.Offset = err.Offset
	case *json.UnmarshalTypeError:
		e.Type = ErrCodeMessageType
		e.Offset = err.Offset
	default:
		switch {
		case err == io.EOF, err == io.ErrUnexpectedEOF:
			e.Type = ErrCodeShortMessage
		case strings.HasPrefix(err.Error(), "json: unknown field"):
			e.Type = ErrCodeMessageUnknownField
		}
	}
	var buf bytes.Buffer
	dec := json.NewEncoder(&buf)
	dec.SetEscapeHTML(false)
	dec.Encode(e)
	return bytes.TrimSpace(buf.Bytes())
}

// NewError returns an error that will be encoded correctly in the RPC protocol.
func NewError(code int64, message string, data any) error {
	e := &jsonrpc2.WireError{
		Code:    code,
		Message: message,
	}
	e.Data = wireErrorData(data)
	return e
}

// AddWireErrorDetail updates the Data field of a [jsonrpc2.WireError] with the
// fields in details, overwriting fields if they already exist. If err is not a
// [jsonrpc2.WireError] or the Data field does not encode a map, the error  is
// returned unmodified.
func AddWireErrorDetail(err error, details map[string]any) error {
	if err, ok := err.(*jsonrpc2.WireError); ok {
		var data map[string]any
		if json.Unmarshal(err.Data, &data) != nil {
			return err
		}
		for k, v := range details {
			data[k] = v
		}
		err.Data = wireErrorData(data)
		return err
	}
	return err
}

func wireErrorData(data any) json.RawMessage {
	if data == nil {
		return nil
	}
	var buf bytes.Buffer
	dec := json.NewEncoder(&buf)
	dec.SetEscapeHTML(false)
	err := dec.Encode(data)
	if err != nil {
		b, _ := json.Marshal("!" + err.Error())
		return b
	}
	return bytes.TrimSpace(buf.Bytes())
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
