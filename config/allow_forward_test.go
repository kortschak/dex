// Copyright ©2026 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/google/go-cmp/cmp"

	"github.com/kortschak/dex/rpc"
)

var allowForwardAllowedTests = []struct {
	name                     string
	rules                    *AllowForward
	origin, identity, target rpc.UID
	method                   string
	want                     bool
}{
	{
		name:   "nil",
		rules:  nil,
		origin: rpc.UID{Module: "rest"}, identity: rpc.UID{Module: "worklog"}, target: rpc.UID{Module: "kernel"}, method: "set",
		want: false,
	},
	{
		name:   "wildcard",
		rules:  &AllowForward{All: true},
		origin: rpc.UID{Module: "rest"}, identity: rpc.UID{Module: "worklog"}, target: rpc.UID{Module: "kernel"}, method: "set",
		want: true,
	},
	{
		name:   "empty",
		rules:  &AllowForward{},
		origin: rpc.UID{Module: "rest"}, identity: rpc.UID{Module: "worklog"}, target: rpc.UID{Module: "kernel"}, method: "set",
		want: false,
	},
	{
		name: "exact",
		rules: &AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
		origin: rpc.UID{Module: "rest"}, identity: rpc.UID{Module: "worklog"}, target: rpc.UID{Module: "kernel"}, method: "set",
		want: true,
	},
	{
		name: "no_match_wrong_method",
		rules: &AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
		origin: rpc.UID{Module: "rest"}, identity: rpc.UID{Module: "worklog"}, target: rpc.UID{Module: "kernel"}, method: "get",
		want: false,
	},
	{
		name: "no_match_wrong_origin",
		rules: &AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
		origin: rpc.UID{Module: "watcher"}, identity: rpc.UID{Module: "worklog"}, target: rpc.UID{Module: "kernel"}, method: "set",
		want: false,
	},
	{
		name: "multiple_rules",
		rules: &AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
			{Origin: rpc.UID{Module: "watcher"}, Identity: rpc.UID{Module: "runner"}, Target: rpc.UID{Module: "kernel"}, Method: "get"},
		}},
		origin: rpc.UID{Module: "watcher"}, identity: rpc.UID{Module: "runner"}, target: rpc.UID{Module: "kernel"}, method: "get",
		want: true,
	},
}

func TestAllowForwardAllowed(t *testing.T) {
	for _, test := range allowForwardAllowedTests {
		t.Run(test.name, func(t *testing.T) {
			got := test.rules.Allowed(test.origin, test.identity, test.target, test.method)
			if got != test.want {
				t.Errorf("Allowed(%q, %q, %q, %q) = %v, want %v",
					test.origin, test.identity, test.target, test.method, got, test.want)
			}
		})
	}
}

var allowForwardJSONTests = []struct {
	name    string
	json    string
	want    AllowForward
	wantErr error
}{
	{
		name: "wildcard",
		json: `"*"`,
		want: AllowForward{All: true},
	},
	{
		name: "empty_array",
		json: `[]`,
		want: AllowForward{Rules: []ForwardRule{}},
	},
	{
		name: "rules",
		json: `[{"origin":{"module":"rest"},"identity":{"module":"worklog"},"target":{"module":"kernel"},"method":"set"}]`,
		want: AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
	},
	{
		name:    "invalid_string",
		json:    `"all"`,
		wantErr: errors.New(`invalid allow_forward value: "all"`),
	},
	{
		name: "null",
		json: "null",
		want: AllowForward{},
	},
}

func TestAllowForwardJSON(t *testing.T) {
	for _, test := range allowForwardJSONTests {
		t.Run(test.name, func(t *testing.T) {
			var got AllowForward
			err := json.Unmarshal([]byte(test.json), &got)
			if !sameError(err, test.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				return
			}
			if !cmp.Equal(test.want, got) {
				t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.want, got))
			}
		})
	}
}

var jsonRoundTripTests = []struct {
	name string
	val  AllowForward
	want string
}{
	{
		name: "wildcard",
		val:  AllowForward{All: true},
		want: `"*"`,
	},
	{
		name: "rules",
		val: AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
		want: `[{"origin":{"module":"rest"},"identity":{"module":"worklog"},"target":{"module":"kernel"},"method":"set"}]`,
	},
	{
		name: "nil_rules",
		val:  AllowForward{},
		want: "null",
	},
}

func TestAllowForwardJSONRoundTrip(t *testing.T) {
	for _, test := range jsonRoundTripTests {
		t.Run(test.name, func(t *testing.T) {
			b, err := json.Marshal(&test.val)
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			if string(b) != test.want {
				t.Errorf("marshal: got %s, want %s", b, test.want)
			}

			var got AllowForward
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatalf("unmarshal error: %v", err)
			}
			if !cmp.Equal(test.val, got) {
				t.Errorf("round-trip mismatch:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.val, got))
			}
		})
	}
}

var allowForwardTOMLTests = []struct {
	name    string
	toml    string
	want    AllowForward
	wantErr error
}{
	{
		name: "wildcard",
		toml: `allow_forward = "*"`,
		want: AllowForward{All: true},
	},
	{
		name: "rules",
		toml: `
[[allow_forward]]
origin = {module = "rest"}
identity = {module = "worklog"}
target = {module = "kernel"}
method = "set"
`,
		want: AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
	},
	{
		name: "rules_short",
		toml: `
[[allow_forward]]
origin = "rest"
identity = "worklog"
target = "kernel"
method = "set"
`,
		want: AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
	},
	{
		name: "rules_dotted",
		toml: `
[[allow_forward]]
origin = "rest.source"
identity = "worklog.log"
target = "kernel.service"
method = "set"
`,
		want: AllowForward{Rules: []ForwardRule{
			{
				Origin:   rpc.UID{Module: "rest", Service: "source"},
				Identity: rpc.UID{Module: "worklog", Service: "log"},
				Target:   rpc.UID{Module: "kernel", Service: "service"},
				Method:   "set",
			},
		}},
	},
	{
		name: "rules_dotted_star",
		toml: `
[[allow_forward]]
origin = "rest.*"
identity = "worklog.*"
target = "kernel.*"
method = "set"
`,
		want: AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
		}},
	},
	{
		name: "multiple_rules",
		toml: `
[[allow_forward]]
origin = {module = "rest"}
identity = {module = "worklog"}
target = {module = "kernel"}
method = "set"

[[allow_forward]]
origin = {module = "watcher"}
identity = {module = "runner"}
target = {module = "kernel"}
method = "get"
`,
		want: AllowForward{Rules: []ForwardRule{
			{Origin: rpc.UID{Module: "rest"}, Identity: rpc.UID{Module: "worklog"}, Target: rpc.UID{Module: "kernel"}, Method: "set"},
			{Origin: rpc.UID{Module: "watcher"}, Identity: rpc.UID{Module: "runner"}, Target: rpc.UID{Module: "kernel"}, Method: "get"},
		}},
	},
}

func TestAllowForwardTOML(t *testing.T) {
	for _, test := range allowForwardTOMLTests {
		t.Run(test.name, func(t *testing.T) {
			var got struct {
				AllowForward AllowForward `toml:"allow_forward"`
			}
			err := toml.Unmarshal([]byte(test.toml), &got)
			if !sameError(err, test.wantErr) {
				t.Fatalf("unexpected error: %v", err)
			}
			if err != nil {
				return
			}
			if !cmp.Equal(test.want, got.AllowForward) {
				t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.want, got.AllowForward))
			}
		})
	}
}

func sameError(a, b error) bool {
	switch {
	case a != nil && b != nil:
		return a.Error() == b.Error()
	default:
		return a == b
	}
}
