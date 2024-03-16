// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"log/slog"

	"github.com/fsnotify/fsnotify"

	"github.com/kortschak/dex/internal/private"
)

type changeValue struct {
	Change
}

func (v changeValue) LogValue() slog.Value {
	events := make([]eventValue, len(v.Event))
	for i, e := range v.Event {
		events[i] = eventValue{
			Name: e.Name,
			Op:   e.Op.String(),
			Code: int(e.Op),
		}
	}
	cfg, err := private.Redact(v.Config, "json")
	if err != nil {
		return slog.AnyValue(struct {
			Event  []eventValue `json:"event"`
			Config string       `json:"config"`
			Err    error        `json:"err"`
		}{
			Event:  events,
			Config: err.Error(),
			Err:    v.Err,
		})
	}
	return slog.AnyValue(struct {
		Event  []eventValue `json:"event"`
		Config *System      `json:"config"`
		Err    error        `json:"err"`
	}{
		Event:  events,
		Config: cfg,
		Err:    v.Err,
	})
}

type eventValue struct {
	Name string `json:"name"`
	Op   string `json:"op"`
	Code int    `json:"op_code"`
}

type hashesValue struct {
	m map[string]Sum
}

func (v hashesValue) LogValue() slog.Value {
	m := make(map[string]string, len(v.m))
	for p, h := range v.m {
		m[p] = h.String()
	}
	return slog.AnyValue(m)
}

type renamesValue struct {
	m map[Sum]fsnotify.Event
}

func (v renamesValue) LogValue() slog.Value {
	m := make(map[string]fsnotify.Event, len(v.m))
	for s, e := range v.m {
		m[s.String()] = e
	}
	return slog.AnyValue(m)
}
