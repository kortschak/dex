// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var vetTests = []struct {
	name      string
	config    *System
	wantPaths [][]string
	wantErr   error
}{
	{
		// Specify that no device is registered, but have a service that
		// requires one. This should fail.
		name: "no_device_listening",
		config: &System{
			Kernel: &Kernel{
				Network: "unix",
				Device:  []Device{}, // No device.
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Module: ptr("foo"),
					Listen: []Button{
						{Row: 1, Page: "foo", Change: ptr("press"), Do: ptr("notify")},
					},
				},
			},
		},
		wantPaths: [][]string{
			{"service", "foo1", "serial"},
		},
		wantErr: errors.New("service.foo1.serial: field is required but not present (and 1 more errors)"),
	},
	{
		// Specify that no device is registered, but have a service that
		// that requires one with a serial specified. This should fail.
		name: "no_device_listening_with_serial",
		config: &System{
			Kernel: &Kernel{
				Network: "unix",
				Device:  []Device{}, // No device.
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Serial: ptr(""), // Any serial.
					Module: ptr("foo"),
					Listen: []Button{}, // Any listener.
				},
			},
		},
		wantPaths: [][]string{{"service", "foo1", "serial"}},
		wantErr:   errors.New("service.foo1.serial: 2 errors in empty disjunction: (and 3 more errors)"),
	},
	{
		// Specify that no device is registered, and have a service that
		// that does not requires one. This should succeed.
		name: "not_listening",
		config: &System{
			Kernel: &Kernel{
				Network: "unix",
				Device:  []Device{},
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Module: ptr("foo"),
					Listen: nil, // Not listening.
				},
			},
		},
		wantPaths: nil,
		wantErr:   nil,
	},
	{
		// Specify a button with a missing change transition and a
		// non-empty do string. This should fail.
		name: "no_change_do",
		config: &System{
			Kernel: &Kernel{
				Network: "unix",
				Device:  nil, // Default device.
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Serial: ptr(""), // Default serial.
					Module: ptr("foo"),
					Listen: []Button{
						{Change: nil, Do: ptr("invalid method")},
					},
				},
			},
		},
		wantPaths: [][]string{
			{"service", "foo1", "listen", "0", "do"},
		},
		wantErr: errors.New(`service.foo1.listen.0.do: conflicting values "" and "invalid method" (and 1 more errors)`),
	},
	{
		// Specify a button with a missing change transition and a
		// non-empty args. This should fail.
		name: "no_change_args",
		config: &System{
			Kernel: &Kernel{
				Network: "unix",
				Device:  nil, // Default device.
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Serial: ptr(""), // Default serial.
					Module: ptr("foo"),
					Listen: []Button{
						{Change: nil, Args: "invalid args"},
					},
				},
			},
		},
		wantPaths: [][]string{
			{"service", "foo1", "listen", "0", "args"},
		},
		wantErr: errors.New(`service.foo1.listen.0.args: conflicting values "invalid args" and null (mismatched types string and null) (and 1 more errors)`),
	},
	{
		// Specify a button with a missing change transition and a
		// non-empty do string and args. This should fail.
		name: "no_change_do_args",
		config: &System{
			Kernel: &Kernel{
				Network: "unix",
				Device:  nil, // Default device.
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Serial: ptr(""), // Default serial.
					Module: ptr("foo"),
					Listen: []Button{
						{Change: nil, Do: ptr("invalid method"), Args: "invalid args"},
					},
				},
			},
		},
		wantPaths: [][]string{
			{"service", "foo1", "listen", "0", "args"},
			{"service", "foo1", "listen", "0", "do"},
		},
		wantErr: errors.New(`service.foo1.listen.0.do: conflicting values "" and "invalid method" (and 2 more errors)`),
	},
	{
		// Specify default device, but have a service that requires one.
		// This should fail.
		name: "nil_devices",
		config: &System{
			Kernel: &Kernel{
				// Device will be assumed to be first found.
				Network: "unix",
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Module: ptr("foo"),
					Listen: []Button{
						{Row: 1, Page: "foo", Change: ptr("press"), Do: ptr("notify")},
					},
				},
			},
		},
		wantPaths: [][]string{{"service", "foo1", "serial"}},
		wantErr:   errors.New("service.foo1.serial: field is required but not present (and 1 more errors)"),
	},
	{
		// Specify default device is registered, and have a service that
		// that does not requires one. This should succeed.
		name: "nil_devices_not_listening",
		config: &System{
			Kernel: &Kernel{
				// Device will be assumed to be first found.
				Network: "unix",
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Module: ptr("foo"),
					Listen: nil, // Not listening.
				},
			},
		},
		wantPaths: nil,
		wantErr:   nil,
	},
	{
		// Specify default device is registered, and have a service that
		// that uses the default device. This should succeed.
		name: "nil_devices_listening_with_default",
		config: &System{
			Kernel: &Kernel{
				// Device will be assumed to be first found.
				Network: "unix",
			},
			Modules: map[string]*Module{
				"foo": {Path: "foo"},
			},
			Services: map[string]*Service{
				"foo1": {
					Module: ptr("foo"),
					Serial: ptr(""), // Default serial.
					Listen: []Button{
						{Row: 1, Page: "foo", Change: ptr("press"), Do: ptr("notify")},
					},
				},
			},
		},
		wantPaths: nil,
		wantErr:   nil,
	},
}

func TestVet(t *testing.T) {
	for _, test := range vetTests {
		t.Run(test.name, func(t *testing.T) {
			paths, err := Vet(test.config)
			if !sameError(err, test.wantErr) {
				t.Errorf("unexpected error: got:%v want:%v", err, test.wantErr)
			}
			if !cmp.Equal(test.wantPaths, paths) {
				t.Errorf("unexpected paths:\n--- want:\n+++ got:\n%s", cmp.Diff(test.wantPaths, paths))
			}
		})
	}
}

func sameError(a, b error) bool {
	switch {
	case a == b:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Error() == b.Error()
	}
}
