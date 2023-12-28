// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/google/go-cmp/cmp"

	"github.com/kortschak/dex/internal/slogext"
)

var operations = []struct {
	name string
	want map[string]Change
	fn   func(dir string) error
}{
	{
		name: "kernel", fn: func(dir string) error {
			return create(dir, "file1.toml", 0o644, `[kernel]
device = [
	{serial = "dev1"},
	{serial = "dev2"}
]
network = "unix"
`)
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Create,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Create,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
				},
			},
		},
	},
	{
		name: "kernel_no_semantic_change", fn: func(dir string) error {
			return create(dir, "file1.toml", 0o644, `[kernel]
device = [
	{serial = "dev1"},
	{serial = "dev2"}
]

network = "unix"
`)
		},
	},
	{
		name: "non-config", fn: func(dir string) error {
			return create(dir, "file1.yaml", 0o644, "config: value")
		},
	},
	{
		name: "module_foo", fn: func(dir string) error {
			return create(dir, "file1.toml", 0o644, `[kernel]
device = [
	{serial = "dev1"},
	{serial = "dev2"}
]
network = "unix"

[module.foo]
path = "/path/to/foo"
options.len = 1
`)
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Write,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
					Modules: map[string]*Module{
						"foo": {
							Path: "/path/to/foo",
							Options: map[string]any{
								"len": int64(1),
							},
							Sum: mustSum("ca587faa828122bc4df3558fcec7a318bd26ce6a"),
						},
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Write,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
					Modules: map[string]*Module{
						"foo": {
							Path: "/path/to/foo",
							Options: map[string]any{
								"len": int64(1),
							},
							Sum: mustSum("ca587faa828122bc4df3558fcec7a318bd26ce6a"),
						},
					},
				},
			},
		},
	},
	{
		name: "reject_instance", fn: func(dir string) error {
			return create(dir, "file2.toml", 0o644, `[service.reject1]
module = "baz"
serial = "dev1"
`)
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "file2.toml",
						Op:   fsnotify.Create,
					},
				},
				Config: &System{
					Services: map[string]*Service{
						"reject1": {
							Module: ptr("baz"),
							Serial: ptr("dev1"),
							Sum:    mustSum("a96826ccd62221d1a75aa6f4e40ec1e0a59aada2"),
						},
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "file2.toml",
						Op:   fsnotify.Write,
					},
				},
				Config: &System{
					Services: map[string]*Service{
						"reject1": {
							Module: ptr("baz"),
							Serial: ptr("dev1"),
							Sum:    mustSum("a96826ccd62221d1a75aa6f4e40ec1e0a59aada2"),
						},
					},
				},
			},
		},
	},
	{
		name: "delete_reject", fn: func(dir string) error {
			return rm(dir, "file2.toml")
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "file2.toml",
						Op:   fsnotify.Remove,
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "file2.toml",
						Op:   fsnotify.Remove,
					},
				},
			},
		},
	},
	{
		name: "rename_kernel", fn: func(dir string) error {
			return mv(dir, "file1.toml", "kernel.toml")
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "kernel.toml",
						Op:   fsnotify.Create,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
					Modules: map[string]*Module{
						"foo": {
							Path: "/path/to/foo",
							Options: map[string]any{
								"len": int64(1),
							},
							Sum: mustSum("ca587faa828122bc4df3558fcec7a318bd26ce6a"),
						},
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Rename,
					},
					{
						Name: "kernel.toml",
						Op:   fsnotify.Create,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
					Modules: map[string]*Module{
						"foo": {
							Path: "/path/to/foo",
							Options: map[string]any{
								"len": int64(1),
							},
							Sum: mustSum("ca587faa828122bc4df3558fcec7a318bd26ce6a"),
						},
					},
				},
			},
		},
	},
	{
		name: "delete_kernel", fn: func(dir string) error {
			return rm(dir, "kernel.toml")
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "kernel.toml",
						Op:   fsnotify.Remove,
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "kernel.toml",
						Op:   fsnotify.Remove,
					},
				},
			},
		},
	},
	{name: "delete_extraneous", fn: func(dir string) error {
		return rm(dir, "file1.yaml")
	}},
	{
		name: "delete_confdir", fn: func(dir string) error {
			return rm(dir, "")
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: ".",
						Op:   fsnotify.Remove,
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: ".",
						Op:   fsnotify.Remove,
					},
				},
			},
		},
	},
	{
		name: "replace_kernel", fn: func(dir string) error {
			// Make sure the directory has time to be recreated.
			time.Sleep(100 * time.Millisecond)
			return create(dir, "file1.toml", 0o644, `[kernel]
device = [
	{serial = "dev1"},
	{serial = "dev2"}
]
network = "unix"
`)
		},
		want: map[string]Change{
			"darwin": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Create,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
				},
			},
			"linux": {
				Event: []fsnotify.Event{
					{
						Name: "file1.toml",
						Op:   fsnotify.Write,
					},
				},
				Config: &System{
					Kernel: &Kernel{
						Device: []Device{
							{PID: 0, Serial: ptr("dev1"), Default: nil},
							{PID: 0, Serial: ptr("dev2"), Default: nil},
						},
						Network: "unix",
						Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
					},
				},
			},
		},
	},
}

func (c Change) isZero() bool {
	return c.Event == nil && c.Config == nil && c.Err == nil
}

func create(dir, name string, perm fs.FileMode, data string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(data), perm)
}

func mv(dir, from, to string) error {
	return os.Rename(filepath.Join(dir, from), filepath.Join(dir, to))
}

func rm(dir, name string) error {
	return os.RemoveAll(filepath.Join(dir, name))
}

func TestWatcher(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slogext.NewJSONHandler(&logBuf, &slogext.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: slogext.NewAtomicBool(*lines),
	}))
	defer func() {
		if *verbose {
			t.Logf("log:\n%s\n", &logBuf)
		}
	}()

	dir := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := make(chan Change)
	go func() {
		w, err := NewWatcher(ctx, dir, stream, -1, log)
		if err != nil {
			t.Errorf("unexpected error returned by Watch: %v", err)
		}
		w.Watch(ctx)
	}()

	for _, op := range operations {
		err := op.fn(dir)
		if err != nil {
			t.Errorf("unexpected error running operation %q: %v", op.name, err)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		var got Change
		select {
		case <-timer.C:
		case got = <-stream:
			timer.Stop()
		}
		if got.isZero() != op.want[runtime.GOOS].isZero() {
			if got.isZero() {
				t.Errorf("did not receive %q event in time", op.name)
			} else {
				t.Errorf("unexpected %q event", op.name)
			}
		}
		if got.isZero() {
			continue
		}

		for i, e := range got.Event {
			got.Event[i].Name, err = filepath.Rel(dir, e.Name)
			if err != nil {
				t.Errorf("unexpected error removing dir from event name for %q %d: %v", op.name, i, err)
			}
		}

		if !cmp.Equal(op.want[runtime.GOOS], got) {
			t.Errorf("unexpected result for %q:\n--- want:\n+++ got:\n%s", op.name, cmp.Diff(op.want[runtime.GOOS], got))
		}
	}
}

var sumTests = []struct {
	a, b *Sum
	want bool
}{
	{a: nil, b: nil, want: true},
	{a: nil, b: &Sum{}, want: false},
	{a: &Sum{}, b: nil, want: false},
	{a: &Sum{}, b: &Sum{}, want: true},
	{a: &Sum{0: 1}, b: &Sum{}, want: false},
	{a: &Sum{}, b: &Sum{0: 1}, want: false},
}

func TestSum(t *testing.T) {
	for _, test := range sumTests {
		got := test.a.Equal(test.b)
		if got != test.want {
			t.Errorf("unexpected result for %q.equal(%q): got:%t want:%t", test.a, test.b, got, test.want)
		}
	}
}
