// Copyright ©2026 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package rpc

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/kortschak/jsonrpc2"
)

func testKernelWith(rules ForwardRules) *Kernel {
	k := &Kernel{
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	k.allowForward = rules
	return k
}

var checkIdentityTests = []struct {
	name    string
	rules   ForwardRules
	connUID UID
	msgUID  UID
	target  UID
	method  string
	wantErr error
}{
	{
		name:    "zero_conn_allows",
		connUID: UID{},
		msgUID:  UID{Module: "rest"},
		method:  "set",
	},
	{
		name:    "matching_uid",
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "rest"},
		method:  "set",
	},
	{
		name:    "mismatch_no_rules",
		rules:   nil,
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "worklog"},
		method:  "set",
		wantErr: errors.New("identity mismatch: connection rest sent as worklog calling set"),
	},
	{
		name: "mismatch_allowed_by_rules",
		rules: &testRules{allowed: map[rule]bool{
			{UID{Module: "rest"}, UID{Module: "worklog"}, UID{Module: "kernel"}, "set"}: true,
		}},
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "worklog"},
		method:  "set",
	},
	{
		name: "mismatch_wrong_method_in_rules",
		rules: &testRules{allowed: map[rule]bool{
			{UID{Module: "rest"}, UID{Module: "worklog"}, UID{Module: "kernel"}, "set"}: true,
		}},
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "worklog"},
		method:  "get",
		wantErr: errors.New("identity mismatch: connection rest sent as worklog calling get"),
	},
	{
		name:    "mismatch_with_target",
		rules:   nil,
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "worklog"},
		target:  UID{Module: "runner"},
		method:  "run",
		wantErr: errors.New("identity mismatch: connection rest sent as worklog calling run"),
	},
	{
		name: "mismatch_with_target_allowed",
		rules: &testRules{allowed: map[rule]bool{
			{UID{Module: "rest"}, UID{Module: "worklog"}, UID{Module: "runner"}, "run"}: true,
		}},
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "worklog"},
		target:  UID{Module: "runner"},
		method:  "run",
	},
	{
		name:    "empty_target_maps_to_kernel",
		rules:   nil,
		connUID: UID{Module: "rest"},
		msgUID:  UID{Module: "worklog"},
		target:  UID{},
		method:  "set",
		wantErr: errors.New("identity mismatch: connection rest sent as worklog calling set"),
	},
}

func TestCheckIdentity(t *testing.T) {
	for _, test := range checkIdentityTests {
		t.Run(test.name, func(t *testing.T) {
			k := testKernelWith(test.rules)
			err := k.checkIdentity(test.connUID, test.msgUID, test.target, test.method)
			if !sameError(err, test.wantErr) {
				t.Fatalf("checkIdentity() error = %v, wantErr %v", err, test.wantErr)
			}
			if err != nil {
				var wErr *jsonrpc2.WireError
				if ok := err.(*jsonrpc2.WireError); ok != nil {
					wErr = ok
				}
				if wErr == nil || wErr.Code != ErrCodeForbidden {
					t.Errorf("expected ErrCodeForbidden, got %v", err)
				}
			}
		})
	}
}

var checkFuncIdentityTests = []struct {
	name    string
	rules   ForwardRules
	connUID UID
	method  string
	params  string
	wantErr error
}{
	{
		name:    "zero_conn_allows",
		connUID: UID{},
		method:  "set",
		params:  `{"uid":{"module":"rest"}}`,
	},
	{
		name:    "non_actor_method_skipped",
		connUID: UID{Module: "rest"},
		method:  "ping",
		params:  `{"uid":{"module":"other"}}`,
	},
	{
		name:    "actor_method_matching",
		connUID: UID{Module: "rest"},
		method:  "set",
		params:  `{"uid":{"module":"rest"}}`,
	},
	{
		name:    "actor_method_mismatch",
		rules:   nil,
		connUID: UID{Module: "rest"},
		method:  "set",
		params:  `{"uid":{"module":"worklog"}}`,
		wantErr: errors.New("identity mismatch: connection rest sent as worklog calling set"),
	},
	{
		name: "actor_method_mismatch_allowed",
		rules: &testRules{allowed: map[rule]bool{
			{UID{Module: "rest"}, UID{Module: "worklog"}, UID{Module: "kernel"}, "set"}: true,
		}},
		connUID: UID{Module: "rest"},
		method:  "set",
		params:  `{"uid":{"module":"worklog"}}`,
	},
	{
		name:    "invalid_params_ignored",
		connUID: UID{Module: "rest"},
		method:  "set",
		params:  `not json`,
	},
}

type testRules struct {
	allowed map[rule]bool
}

type rule struct {
	origin, identity, target UID
	method                   string
}

func (p *testRules) Allowed(origin, identity, target UID, method string) bool {
	return p.allowed[rule{origin, identity, target, method}]
}

func TestCheckFuncIdentity(t *testing.T) {
	for _, test := range checkFuncIdentityTests {
		t.Run(test.name, func(t *testing.T) {
			k := testKernelWith(test.rules)
			err := k.checkFuncIdentity(test.connUID, test.method, json.RawMessage(test.params))
			if !sameError(err, test.wantErr) {
				t.Errorf("checkFuncIdentity() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}

func TestBoundHandlerConnUID(t *testing.T) {
	h := &boundHandler{
		kernel: testKernelWith(nil),
	}

	if got := h.connUID(); !got.IsZero() {
		t.Errorf("initial connUID = %v, want zero", got)
	}

	want := UID{Module: "test"}
	h.uid.Store(&want)
	if got := h.connUID(); got != want {
		t.Errorf("connUID after store = %v, want %v", got, want)
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
