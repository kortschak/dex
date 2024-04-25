// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package api defines RPC messages used to communicate with the runner module.
package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
	"github.com/kortschak/dex/rpc"
)

// Config defines configuration options.
type Config struct {
	LogLevel  *slog.Level `json:"log_level,omitempty"`
	AddSource *bool       `json:"log_add_source,omitempty"`
	Options   struct {
		Web         *Web            `json:"web,omitempty"`
		DatabaseDir string          `json:"database_dir,omitempty"` // Relative to XDG_STATE_HOME.
		Hostname    string          `json:"hostname,omitempty"`
		Heartbeat   *rpc.Duration   `json:"heartbeat,omitempty"`
		Rules       map[string]Rule `json:"rules,omitempty"`
	} `json:"options,omitempty"`
}

type Rule struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Src  string `json:"src"`
}

type Web struct {
	// Addr is the dashboard and dump server address.
	Addr string `json:"addr"`

	// HTML is the path to custom HTML for dashboard rendering.
	HTML string `json:"html,omitempty"`

	// Rules is the set of transformation rules for rendering
	// events on a dashboard. The map keys are source bucket
	// and destination bucket.
	Rules map[string]map[string]WebRule `json:"rules,omitempty"`

	// AllowModification enables the amend and load endpoints
	// on the server, allowing direct modification of the
	// bucket contents. For this configuration to be effective
	// Addr must be a loopback device.
	AllowModification bool `json:"can_modify"`
}

type WebRule struct {
	Name string `json:"name"`
	Src  string `json:"src"`
}

type BucketMetadata struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Type     string         `json:"type"`
	Client   string         `json:"client"`
	Hostname string         `json:"hostname"`
	Created  time.Time      `json:"created"`
	Data     map[string]any `json:"data,omitempty"`
	Events   []Event        `json:"events,omitempty"`
}

type Event struct {
	Bucket   string         `json:"bucket,omitempty"`
	ID       int64          `json:"id,omitempty"`
	Start    time.Time      `json:"start"`
	End      time.Time      `json:"end"`
	Data     map[string]any `json:"data,omitempty"`
	Continue *bool          `json:"continue,omitempty"`
}

type Amendment struct {
	Bucket  string        `json:"bucket"`
	Time    time.Time     `json:"time"`
	Message string        `json:"msg"`
	Replace []Replacement `json:"replace,omitempty"`
}

type Replacement struct {
	Start time.Time      `json:"start"`
	End   time.Time      `json:"end"`
	Data  map[string]any `json:"data"`
}

type Activity struct {
	Bucket string         `json:"bucket"`
	Data   map[string]any `json:"data"`
}

type Report struct {
	Time    time.Time    `json:"time"`
	Period  rpc.Duration `json:"period"`
	Details DetailMapper `json:"details"`
}

// UnmarshalJSON unmarshals data into the receiver, detecting the type of
// the details. If the details field in data can be unmarshalled into a
// WatcherDetails, this type will be used, otherwise it will be unmarshalled
// into a MapDetailer.
func (r *Report) UnmarshalJSON(data []byte) error {
	var report struct {
		Time    time.Time       `json:"time"`
		Period  rpc.Duration    `json:"period"`
		Details json.RawMessage `json:"details"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	err := dec.Decode(&report)
	if err != nil {
		return err
	}
	if dec.More() {
		off := dec.InputOffset()
		return fmt.Errorf("invalid character "+quoteChar(data[off])+" after top-level value at offset %d", off)
	}
	*r = Report{
		Time:   report.Time,
		Period: report.Period,
	}

	var errs []error
	for _, m := range []DetailMapper{
		&WatcherDetails{},
		&MapDetails{},
	} {
		err = json.Unmarshal(report.Details, m)
		if err == nil {
			r.Details = m
			return nil
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
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

func (r *Report) Map() map[string]any {
	if r.Details == nil {
		return map[string]any{"time": r.Time}
	}
	m := r.Details.DetailMap()
	m["time"] = r.Time
	return m
}

// DetailMapper is the report details interface.
type DetailMapper interface {
	DetailMap() map[string]any
}

// WatcherDetails is a set of variables provided to rules from a watcher
// event message. Each field is provided to the CEL environment as a field
// of the global, curr, and the previous evaluation's values are available
// as fields of the global, last. See [watcher.Details] for the names used
// for fields in the CEL environment.
type WatcherDetails watcher.Details

func (d *WatcherDetails) DetailMap() map[string]any {
	return map[string]any{
		"wid":        d.WindowID,
		"pid":        d.ProcessID,
		"name":       d.Name,
		"class":      d.Class,
		"window":     d.WindowName,
		"last_input": d.LastInput,
		"locked":     d.Locked,
	}
}

// MapDetails is a generic detailer. Type information is may not be present
// so it is the CEL code's responsibility to ensure that values are converted
// to appropriate types.
type MapDetails map[string]any

func (d MapDetails) DetailMap() map[string]any {
	return d
}
