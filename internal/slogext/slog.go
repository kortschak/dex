// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package slogext provides slog helpers.
package slogext

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/kortschak/goroutine"
	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/private"
)

// GoID is a slog.Handler that adds the calling goroutine's goid.
type GoID struct {
	slog.Handler
}

func (h GoID) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.Int64("goid", goroutine.ID()))
	return h.Handler.Handle(ctx, r)
}

func (h GoID) WithAttrs(attrs []slog.Attr) slog.Handler {
	return GoID{h.Handler.WithAttrs(attrs)}
}

func (h GoID) WithGroup(name string) slog.Handler {
	return GoID{h.Handler.WithGroup(name)}
}

// Stringer implements slog.LogValuer for [fmt.Stringer].
type Stringer struct {
	fmt.Stringer
}

func (v Stringer) LogValue() slog.Value {
	if v.Stringer == nil {
		return slog.StringValue("<nil>")
	}
	return slog.StringValue(v.String())
}

// Request implements slog.LogValuer for [jsonrpc2.Request].
type Request struct {
	*jsonrpc2.Request
}

func (v Request) LogValue() slog.Value {
	return slog.AnyValue(request{ID: v.Request.ID.Raw(), Method: v.Method, Params: v.Params})
}

// RequestRedactPrivate implements slog.LogValuer for [jsonrpc2.Request],
// redacting fields using [private.Redact].
type RequestRedactPrivate struct {
	*jsonrpc2.Request
}

func (v RequestRedactPrivate) LogValue() slog.Value {
	var p any
	err := json.Unmarshal(v.Request.Params, &p)
	if err != nil {
		return slog.AnyValue(request{ID: v.Request.ID.Raw(), Method: v.Method, Params: json.RawMessage(`"INVALID"`), Err: err.Error()})
	}
	p, err = private.Redact(p, "")
	if err != nil {
		return slog.AnyValue(request{ID: v.Request.ID.Raw(), Method: v.Method, Params: json.RawMessage(`"INVALID"`), Err: err.Error()})
	}
	b, err := json.Marshal(p)
	if err != nil {
		return slog.AnyValue(request{ID: v.Request.ID.Raw(), Method: v.Method, Params: json.RawMessage(`"INVALID"`), Err: err.Error()})
	}
	return slog.AnyValue(request{ID: v.Request.ID.Raw(), Method: v.Method, Params: b})
}

type request struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Err    string          `json:"error,omitempty"`
}

type PrivateRedact struct {
	Val any
	Tag string
}

func (v PrivateRedact) LogValue() slog.Value {
	val, err := private.Redact(v.Val, v.Tag)
	if err != nil {
		return slog.AnyValue(err)
	}
	return slog.AnyValue(val)
}

// JSONHandler is a slog.Handler that writes Records to an io.Writer as
// line-delimited JSON objects. It differs from the standard library JSONHandler
// by allowing alteration of the AddSource behaviour after construction.
type JSONHandler struct {
	addSource     *atomic.Bool
	withSource    *slog.JSONHandler
	withoutSource *slog.JSONHandler
}

// NewJSONHandler creates a JSONHandler that writes to w, using the given
// options.
// If opts is nil, the default options are used.
func NewJSONHandler(w io.Writer, opts *HandlerOptions) *JSONHandler {
	if opts == nil {
		opts = &HandlerOptions{}
	}
	if opts.AddSource == nil {
		opts.AddSource = &atomic.Bool{}
	}
	return &JSONHandler{
		addSource: opts.AddSource,
		withSource: slog.NewJSONHandler(w, &slog.HandlerOptions{
			AddSource:   true,
			Level:       opts.Level,
			ReplaceAttr: opts.ReplaceAttr,
		}),
		withoutSource: slog.NewJSONHandler(w, &slog.HandlerOptions{
			AddSource:   false,
			Level:       opts.Level,
			ReplaceAttr: opts.ReplaceAttr,
		}),
	}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower.
func (h *JSONHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.withSource.Enabled(ctx, level)
}

// WithAttrs returns a new JSONHandler whose attributes consists
// of h's attributes followed by attrs.
func (h *JSONHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &JSONHandler{
		addSource:     h.addSource,
		withSource:    h.withSource.WithAttrs(attrs).(*slog.JSONHandler),
		withoutSource: h.withoutSource.WithAttrs(attrs).(*slog.JSONHandler),
	}
}

// WithGroup returns a new Handler with the given group appended to
// h's existing groups.
func (h *JSONHandler) WithGroup(name string) slog.Handler {
	return &JSONHandler{
		addSource:     h.addSource,
		withSource:    h.withSource.WithGroup(name).(*slog.JSONHandler),
		withoutSource: h.withoutSource.WithGroup(name).(*slog.JSONHandler),
	}
}

// Handle formats its argument Record as a JSON object on a single line.
//
// See [slog.JSONHandler.Handle] for details.
func (h *JSONHandler) Handle(ctx context.Context, r slog.Record) error {
	if h.addSource.Load() {
		return h.withSource.Handle(ctx, r)
	}
	return h.withoutSource.Handle(ctx, r)
}

// HandlerOptions are options for a JSONHandler. It is derived from the
// [slog.HandlerOptions] with a changed AddSource field type to allow
// dynamically changing AddSource behaviour during run time.
// A zero HandlerOptions consists entirely of default values.
type HandlerOptions struct {
	// AddSource causes the handler to compute the source code position
	// of the log statement and add a SourceKey attribute to the output.
	// A nil AddSource is false.
	AddSource *atomic.Bool

	// Level reports the minimum record level that will be logged.
	Level slog.Leveler

	// ReplaceAttr is called to rewrite each non-group attribute before
	// it is logged.
	ReplaceAttr func(groups []string, a slog.Attr) slog.Attr
}

// NewAtomicBool is a convenience function to returns an atomic.Bool with a
// specified state.
func NewAtomicBool(t bool) *atomic.Bool {
	var x atomic.Bool
	x.Store(t)
	return &x
}

// PrefixHandlerGroup is a coordinated set of slog.Handlers sharing
// a single io.Writer.
type PrefixHandlerGroup struct {
	mu sync.Mutex
	w  io.Writer
	h  slog.Handler
}

// NewPrefixHandlerGroup returns a handler group sharing w and h. w must be the
// io.Writer used to initialise h.
func NewPrefixHandlerGroup(w io.Writer, h slog.Handler) *PrefixHandlerGroup {
	return &PrefixHandlerGroup{w: w, h: h}
}

// Prefix is a slog.Handler that logs messages with a prefix.
type PrefixHandler struct {
	prefix string

	mu *sync.Mutex
	w  io.Writer
	h  slog.Handler
}

// NewHandler returns a PrefixHandler within g's group. Log messages written
// to the handler will be prefixed with the provided string.
func (g *PrefixHandlerGroup) NewHandler(prefix string) *PrefixHandler {
	return &PrefixHandler{
		prefix: prefix,
		mu:     &g.mu,
		w:      g.w,
		h:      g.h,
	}
}

// Enabled reports whether the handler handles records at the given level.
// The handler ignores records whose level is lower.
func (h *PrefixHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.h.Enabled(ctx, level)
}

// WithAttrs returns a new JSONHandler whose attributes consists
// of h's attributes followed by attrs.
func (h *PrefixHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *h
	c.h = h.h.WithAttrs(attrs)
	return &c
}

// WithGroup returns a new Handler with the given group appended to
// h's existing groups.
func (h *PrefixHandler) WithGroup(name string) slog.Handler {
	c := *h
	c.h = h.h.WithGroup(name)
	return &c
}

// Handle formats its according to the group's handler, but prefixed with the
// prefix used to initialise h.
//
// See [slog.JSONHandler.Handle] for details.
func (h *PrefixHandler) Handle(ctx context.Context, rec slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.w.Write([]byte(h.prefix))
	return h.h.Handle(ctx, rec)
}

// Write implements the io.Writer interface, writing to the group's io.Writer.
// Each write to h is prefixed with the receiver's prefix string. If a write
// spans multiple lines each line is prefixed.
func (h *PrefixHandler) Write(b []byte) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	lines := bytes.Split(bytes.TrimSuffix(b, []byte{'\n'}), []byte{'\n'})
	var (
		n   int
		err error
	)
	for i, l := range lines {
		_, err = h.w.Write([]byte(h.prefix))
		if err != nil {
			break
		}
		n += len(l)
		_, err = h.w.Write(l)
		if err != nil {
			break
		}
		_, err = h.w.Write([]byte{'\n'})
		if err != nil {
			break
		}
		if i != len(lines)-1 {
			n++
		}
	}
	if bytes.HasSuffix(b, []byte{'\n'}) {
		n++
	}
	return n, err
}
