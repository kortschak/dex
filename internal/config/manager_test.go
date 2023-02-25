// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/google/go-cmp/cmp"

	"github.com/kortschak/dex/config"
)

var changeStream = []struct {
	change  Change
	state   *System
	unified string
	remove  [][]string
	config  *System
}{
	0: {
		change: Change{
			Event: []fsnotify.Event{
				{
					Name: "/confdir/file1.toml",
					Op:   0x1,
				},
			},
			Config: &System{
				Kernel: &Kernel{
					Device: []Device{
						{PID: 0, Serial: ptr("dev1"), Default: nil},
						{PID: 0, Serial: ptr("dev2"), Default: nil},
					},
					Network: "unix",
				},
				Modules: map[string]*Module{
					"bar": {
						Path: "p",
					},
					"foo": {
						Path: "q",
						Options: map[string]any{
							"len": int64(1),
						},
					},
				},
				Services: map[string]*Service{
					"accept1": {
						Module: ptr("foo"),
						Serial: ptr("dev1"),
						Options: map[string]any{
							"len": int64(9),
						},
					},
				},
			},
		},
		unified: `{
	kernel: {
		device: [{
			serial: "dev1"
		}, {
			serial: "dev2"
		}]
		network: "unix"
		sum:     "0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"
	}
	module: {
		bar: {
			path: "p"
			sum:  "9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"
		}
		foo: {
			path: "q"
			options: {
				len: 1
			}
			sum: "9448119cd27cb5d28e6a9079862d1238f0c76292"
		}
	}
	service: {
		accept1: {
			module: "foo"
			serial: "dev1"
			options: {
				len: 9
			}
			sum: "c171c3521e5ab86695236550b5dc9bd54d4c6d30"
		}
	}
}`,
		remove: nil,
		config: &System{
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
					Path: "q",
					Options: map[string]any{
						"len": 1,
					},
					Sum: mustSum("9448119cd27cb5d28e6a9079862d1238f0c76292"),
				},
				"bar": {
					Path: "p",
					Sum:  mustSum("9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"),
				},
			},
			Services: map[string]*Service{
				"accept1": {
					Module: ptr("foo"),
					Serial: ptr("dev1"),
					Options: map[string]any{
						"len": 9,
					},
					Sum: mustSum("c171c3521e5ab86695236550b5dc9bd54d4c6d30"),
				},
			},
		},
	},
	1: {
		change: Change{
			Event: []fsnotify.Event{
				{
					Name: "/confdir/file2.toml",
					Op:   0x1,
				},
			},
			Config: &System{
				Services: map[string]*Service{
					"reject2": {
						Module: ptr("bar"),
						Serial: ptr("dev3"),
						Listen: []Button{},
					},
					"reject1": {
						Module: ptr("baz"),
						Serial: ptr("dev1"),
						Listen: []Button{},
					},
				},
			},
		},
		unified: `{
	kernel: {
		device: [{
			serial: "dev1"
		}, {
			serial: "dev2"
		}]
		network: "unix"
		sum:     "0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"
	}
	module: {
		bar: {
			path: "p"
			sum:  "9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"
		}
		foo: {
			path: "q"
			options: {
				len: 1
			}
			sum: "9448119cd27cb5d28e6a9079862d1238f0c76292"
		}
	}
	service: {
		accept1: {
			module: "foo"
			serial: "dev1"
			options: {
				len: 9
			}
			sum: "c171c3521e5ab86695236550b5dc9bd54d4c6d30"
		}
		reject1: {
			module: "baz"
			serial: "dev1"
			listen: []
			sum: "a96826ccd62221d1a75aa6f4e40ec1e0a59aada2"
		}
		reject2: {
			module: "bar"
			serial: "dev3"
			listen: []
			sum: "2ce1013fb90d521e9f73dfa6c402e554ab32095f"
		}
	}
}`,
		remove: [][]string{{"service", "reject1", "module"}, {"service", "reject2", "serial"}},
		config: &System{
			Kernel: &Kernel{
				Device: []Device{
					{PID: 0, Serial: ptr("dev1"), Default: nil},
					{PID: 0, Serial: ptr("dev2"), Default: nil},
				},
				Network: "unix",
				Sum:     mustSum("0384f61c9ded1788a24a7a2a5c5fa7dfe5838e7c"),
			},
			Modules: map[string]*Module{
				"bar": {
					Path: "p",
					Sum:  mustSum("9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"),
				},
				"foo": {
					Path: "q",
					Options: map[string]any{
						"len": 1,
					},
					Sum: mustSum("9448119cd27cb5d28e6a9079862d1238f0c76292"),
				},
			},
			Services: map[string]*Service{
				"accept1": {
					Module: ptr("foo"),
					Serial: ptr("dev1"),
					Options: map[string]any{
						"len": 9,
					},
					Sum: mustSum("c171c3521e5ab86695236550b5dc9bd54d4c6d30"),
				},
			},
		},
	},
	2: {
		change: Change{
			Event: []fsnotify.Event{
				{
					Name: "/confdir/file1.toml",
					Op:   0x2,
				},
			},
			Config: &System{
				Kernel: &Kernel{
					Device: []Device{
						{PID: 0, Serial: ptr("dev1"), Default: nil},
						{PID: 0, Serial: ptr("dev2"), Default: nil},
					},
					Network:  "unix",
					LogLevel: ptr(slog.Level(-4)),
				},
				Modules: map[string]*Module{
					"bar": {
						Path: "p",
						Args: []string{"a"},
					},
					"foo": {
						Path: "q",
						Options: map[string]any{
							"len": int64(1),
						},
					},
				},
				Services: map[string]*Service{
					"accept1": {
						Module: ptr("foo"),
						Serial: ptr("dev1"),
						Options: map[string]any{
							"len": int64(9),
						},
					},
				},
			},
		},
		unified: `{
	kernel: {
		device: [{
			serial: "dev1"
		}, {
			serial: "dev2"
		}]
		network:   "unix"
		log_level: "DEBUG"
		sum:       "b48f255cf677f2c1c30cf56f75b4b64adb0bb8bf"
	}
	module: {
		bar: {
			path: "p"
			args: ["a"]
			sum: "229afc4807228f4705e36cf937c46776770892ba"
		}
		foo: {
			path: "q"
			options: {
				len: 1
			}
			sum: "9448119cd27cb5d28e6a9079862d1238f0c76292"
		}
	}
	service: {
		accept1: {
			module: "foo"
			serial: "dev1"
			options: {
				len: 9
			}
			sum: "c171c3521e5ab86695236550b5dc9bd54d4c6d30"
		}
		reject1: {
			module: "baz"
			serial: "dev1"
			listen: []
			sum: "a96826ccd62221d1a75aa6f4e40ec1e0a59aada2"
		}
		reject2: {
			module: "bar"
			serial: "dev3"
			listen: []
			sum: "2ce1013fb90d521e9f73dfa6c402e554ab32095f"
		}
	}
}`,
		remove: [][]string{{"service", "reject1", "module"}, {"service", "reject2", "serial"}},
		config: &System{
			Kernel: &Kernel{
				Device: []Device{
					{PID: 0, Serial: ptr("dev1"), Default: nil},
					{PID: 0, Serial: ptr("dev2"), Default: nil},
				},
				Network:  "unix",
				LogLevel: ptr(slog.Level(-4)),
				Sum:      mustSum("b48f255cf677f2c1c30cf56f75b4b64adb0bb8bf"),
			},
			Modules: map[string]*Module{
				"bar": {
					Path: "p",
					Args: []string{"a"},
					Sum:  mustSum("229afc4807228f4705e36cf937c46776770892ba"),
				},
				"foo": {
					Path: "q",
					Options: map[string]any{
						"len": 1,
					},
					Sum: mustSum("9448119cd27cb5d28e6a9079862d1238f0c76292"),
				},
			},
			Services: map[string]*Service{
				"accept1": {
					Module: ptr("foo"),
					Serial: ptr("dev1"),
					Options: map[string]any{
						"len": 9,
					},
					Sum: mustSum("c171c3521e5ab86695236550b5dc9bd54d4c6d30"),
				},
			},
		},
	},
	3: {
		change: Change{
			Event: []fsnotify.Event{
				{
					Name: "/confdir/file1.toml",
					Op:   0x2,
				},
			},
			Config: &System{
				Kernel: &Kernel{
					Device: []Device{
						{PID: 0, Serial: ptr("dev1"), Default: nil},
						{PID: 0, Serial: ptr("dev2"), Default: nil},
					},
					Network:  "unix",
					LogLevel: ptr(slog.Level(8)),
				},
				Modules: map[string]*Module{
					"bar": {
						Path: "p",
					},
					"foo": {
						Path: "q",
						Options: map[string]any{
							"len": int64(1),
						},
					},
				},
				Services: map[string]*Service{
					"accept1": {
						Module: ptr("foo"),
						Serial: ptr("dev1"),
						Listen: []Button{
							{Row: 1, Col: 0, Page: "foo1", Change: ptr("press"), Do: ptr("notify"), Args: []any{1, "two"}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
						Options: map[string]any{
							"len": int64(9),
						},
					},
				},
			},
		},
		unified: `{
	kernel: {
		device: [{
			serial: "dev1"
		}, {
			serial: "dev2"
		}]
		network:   "unix"
		log_level: "ERROR"
		sum:       "3487723f808df5c567177cb073d712d4c6f20f16"
	}
	module: {
		bar: {
			path: "p"
			sum:  "9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"
		}
		foo: {
			path: "q"
			options: {
				len: 1
			}
			sum: "9448119cd27cb5d28e6a9079862d1238f0c76292"
		}
	}
	service: {
		accept1: {
			module: "foo"
			serial: "dev1"
			listen: [{
				row:    1
				col:    0
				page:   "foo1"
				change: "press"
				do:     "notify"
				args: [1, "two"]
			}, {
				row:    2
				col:    4
				change: "release"
				do:     "home"
			}]
			options: {
				len: 9
			}
			sum: "7b7605cd4fc4dacd4897c709f8f466cbc082d814"
		}
		reject1: {
			module: "baz"
			serial: "dev1"
			listen: []
			sum: "a96826ccd62221d1a75aa6f4e40ec1e0a59aada2"
		}
		reject2: {
			module: "bar"
			serial: "dev3"
			listen: []
			sum: "2ce1013fb90d521e9f73dfa6c402e554ab32095f"
		}
	}
}`,
		remove: [][]string{{"service", "reject1", "module"}, {"service", "reject2", "serial"}},
		config: &System{
			Kernel: &Kernel{
				Device: []Device{
					{PID: 0, Serial: ptr("dev1"), Default: nil},
					{PID: 0, Serial: ptr("dev2"), Default: nil},
				},
				Network:  "unix",
				LogLevel: ptr(slog.Level(8)),
				Sum:      mustSum("3487723f808df5c567177cb073d712d4c6f20f16"),
			},
			Modules: map[string]*Module{
				"bar": {
					Path: "p",
					Sum:  mustSum("9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"),
				},
				"foo": {
					Path: "q",
					Options: map[string]any{
						"len": 1,
					},
					Sum: mustSum("9448119cd27cb5d28e6a9079862d1238f0c76292"),
				},
			},
			Services: map[string]*Service{
				"accept1": {
					Module: ptr("foo"),
					Serial: ptr("dev1"),
					Listen: []Button{
						{Row: 1, Col: 0, Page: "foo1", Change: ptr("press"), Do: ptr("notify"), Args: []any{1, "two"}},
						{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
					},
					Options: map[string]any{
						"len": 9,
					},
					Sum: mustSum("7b7605cd4fc4dacd4897c709f8f466cbc082d814"),
				},
			},
		},
	},
	4: {
		change: Change{
			Event: []fsnotify.Event{
				{
					Name: "/confdir/file1.toml",
					Op:   0x8,
				},
				{
					Name: "/confdir/file3.toml",
					Op:   0x1,
				},
			},
			Config: &System{
				Kernel: &Kernel{
					Device: []Device{
						{PID: 0, Serial: ptr("dev1"), Default: nil},
						{PID: 0, Serial: ptr("dev2"), Default: nil},
					},
					Network:  "unix",
					LogLevel: ptr(slog.Level(8)),
				},
				Modules: map[string]*Module{
					"bar": {
						Path: "p",
					},
					"foo": {
						Path: "q",
						Options: map[string]any{
							"len": int64(1),
						},
					},
				},
				Services: map[string]*Service{
					"accept1": {
						Module: ptr("foo"),
						Serial: ptr("dev1"),
						Options: map[string]any{
							"len": int64(9),
						},
					},
				},
			},
		},
		unified: `{
	kernel: {
		device: [{
			serial: "dev1"
		}, {
			serial: "dev2"
		}]
		network:   "unix"
		log_level: "ERROR"
		sum:       "3487723f808df5c567177cb073d712d4c6f20f16"
	}
	module: {
		bar: {
			path: "p"
			sum:  "9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"
		}
		foo: {
			path: "q"
			options: {
				len: 1
			}
			sum: "9448119cd27cb5d28e6a9079862d1238f0c76292"
		}
	}
	service: {
		accept1: {
			module: "foo"
			serial: "dev1"
			options: {
				len: 9
			}
			sum: "c171c3521e5ab86695236550b5dc9bd54d4c6d30"
		}
		reject1: {
			module: "baz"
			serial: "dev1"
			listen: []
			sum: "a96826ccd62221d1a75aa6f4e40ec1e0a59aada2"
		}
		reject2: {
			module: "bar"
			serial: "dev3"
			listen: []
			sum: "2ce1013fb90d521e9f73dfa6c402e554ab32095f"
		}
	}
}`,
		remove: [][]string{{"service", "reject1", "module"}, {"service", "reject2", "serial"}},
		config: &System{
			Kernel: &Kernel{
				Device: []Device{
					{PID: 0, Serial: ptr("dev1"), Default: nil},
					{PID: 0, Serial: ptr("dev2"), Default: nil},
				},
				Network:  "unix",
				LogLevel: ptr(slog.Level(8)),
				Sum:      mustSum("3487723f808df5c567177cb073d712d4c6f20f16"),
			},
			Modules: map[string]*Module{
				"bar": {
					Path: "p",
					Sum:  mustSum("9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"),
				},
				"foo": {
					Path: "q",
					Options: map[string]any{
						"len": 1,
					},
					Sum: mustSum("9448119cd27cb5d28e6a9079862d1238f0c76292"),
				},
			},
			Services: map[string]*Service{
				"accept1": {
					Module: ptr("foo"),
					Serial: ptr("dev1"),
					Options: map[string]any{
						"len": 9,
					},
					Sum: mustSum("c171c3521e5ab86695236550b5dc9bd54d4c6d30"),
				},
			},
		},
	},
	5: {
		change: Change{
			Event: []fsnotify.Event{
				{
					Name: "/confdir/file2.toml",
					Op:   0x4,
				},
			},
		},
		unified: `{
	kernel: {
		device: [{
			serial: "dev1"
		}, {
			serial: "dev2"
		}]
		network:   "unix"
		log_level: "ERROR"
		sum:       "3487723f808df5c567177cb073d712d4c6f20f16"
	}
	module: {
		bar: {
			path: "p"
			sum:  "9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"
		}
		foo: {
			path: "q"
			options: {
				len: 1
			}
			sum: "9448119cd27cb5d28e6a9079862d1238f0c76292"
		}
	}
	service: {
		accept1: {
			module: "foo"
			serial: "dev1"
			options: {
				len: 9
			}
			sum: "c171c3521e5ab86695236550b5dc9bd54d4c6d30"
		}
	}
}`,
		config: &System{
			Kernel: &Kernel{
				Device: []Device{
					{PID: 0, Serial: ptr("dev1"), Default: nil},
					{PID: 0, Serial: ptr("dev2"), Default: nil},
				},
				Network:  "unix",
				LogLevel: ptr(slog.Level(8)),
				Sum:      mustSum("3487723f808df5c567177cb073d712d4c6f20f16"),
			},
			Modules: map[string]*Module{
				"bar": {
					Path: "p",
					Sum:  mustSum("9cbef6e3876a9ebe256e2c0f41105a6ed5f56ca0"),
				},
				"foo": {
					Path: "q",
					Options: map[string]any{
						"len": 1,
					},
					Sum: mustSum("9448119cd27cb5d28e6a9079862d1238f0c76292"),
				},
			},
			Services: map[string]*Service{
				"accept1": {
					Module: ptr("foo"),
					Serial: ptr("dev1"),
					Options: map[string]any{
						"len": 9,
					},
					Sum: mustSum("c171c3521e5ab86695236550b5dc9bd54d4c6d30"),
				},
			},
		},
	},
}

func TestManager(t *testing.T) {
	var logBuf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{
		Level:     slog.LevelDebug,
		AddSource: *lines,
	}))
	defer func() {
		if *verbose {
			t.Logf("log:\n%s\n", &logBuf)
		}
	}()

	m := NewManager(log)
	for i, step := range changeStream {
		log.LogAttrs(context.Background(), slog.LevelDebug, "step", slog.Int("number", i))
		err := m.Apply(step.change)
		if err != nil {
			t.Fatalf("unexpected apply error at step: %d: %v", i, err)
		}

		cfg, unified, _, _, err := m.Unify(config.Schema)
		if err != nil {
			t.Log(unified)
			t.Fatalf("unexpected unify error at step: %d: %v", i, err)
		}

		if got := fmt.Sprint(unified); got != step.unified {
			t.Errorf("unexpected unify result at step: %d:\n--- want:\n+++ got:\n%s", i, cmp.Diff(step.unified, got))
		}

		paths, err := Vet(cfg)
		if (err == nil) != (step.remove == nil) {
			t.Errorf("unexpected vet error at step: %d: %v", i, err)
		}
		if !cmp.Equal(paths, step.remove) {
			t.Errorf("unexpected remove paths error at step: %d:\ngot: %v\nwant:%v", i, paths, step.remove)
		}

		cfg, err = Repair(cfg, paths)
		if err != nil {
			t.Errorf("unexpected unify error at step: %d: %v", i, err)
		}
		if !cmp.Equal(cfg, step.config) {
			t.Errorf("unexpected unify result at step: %d:\n--- want:\n+++ got:\n%s", i, cmp.Diff(step.config, cfg))
		}
	}
}
