// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sys

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/config"
	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

var managerTests = []struct {
	name    string
	actions []any
	closed  bool

	wantKernel []action

	wantDevices map[string][]action
	wantSerials map[rpc.UID]string
	wantConfig  *config.System
}{
	{
		name: "oneshot",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"path",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
			},
		},
		wantSerials: map[rpc.UID]string{
			{Module: "foo", Service: "action"}: "",
		},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"foo": {
					Path:    "path",
					Args:    []string{"one", "two"},
					Options: map[string]any{"opt": 1},
				},
			},
			Services: map[string]*config.Service{
				"action": {
					Name:    "action",
					Module:  ptr("foo"),
					Serial:  ptr(""),
					Options: map[string]any{"opt": 'a'},
					Listen: []config.Button{
						{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
						{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
					},
				},
			},
		},
	},
	{
		name: "oneshot_closed",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			3: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.close()
			},
		},
		closed: true,
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"path",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
			{
				Name: "close",
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
				{
					Name: "close",
				},
			},
		},
		wantSerials: map[rpc.UID]string{
			{Module: "foo", Service: "action"}: "",
		},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"foo": {
					Path:    "path",
					Args:    []string{"one", "two"},
					Options: map[string]any{"opt": 1},
				},
			},
			Services: map[string]*config.Service{
				"action": {
					Name:    "action",
					Module:  ptr("foo"),
					Serial:  ptr(""),
					Options: map[string]any{"opt": 'a'},
					Listen: []config.Button{
						{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
						{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
					},
				},
			},
		},
	},
	{
		name: "change_net",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
			},
			3: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "tcp",
					LogLevel: ptr(slog.LevelDebug),
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "close",
			},
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
		},
		wantDevices: map[string][]action{"": nil},
		wantSerials: map[rpc.UID]string{},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "tcp",
				LogLevel: ptr(slog.LevelDebug),
			},
		},
	},
	{
		name: "progressive",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
			},
			3: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
			},
			4: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"path",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
			},
		},
		wantSerials: map[rpc.UID]string{
			{Module: "foo", Service: "action"}: "",
		},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"foo": {
					Path:    "path",
					Args:    []string{"one", "two"},
					Options: map[string]any{"opt": 1},
				},
			},
			Services: map[string]*config.Service{
				"action": {
					Name:    "action",
					Module:  ptr("foo"),
					Serial:  ptr(""),
					Options: map[string]any{"opt": 'a'},
					Listen: []config.Button{
						{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
						{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
					},
				},
			},
		},
	},
	{
		name: "reconfigure_module",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			3: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 2},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"path",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 2,
							},
						},
					},
				},
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
			},
		},
		wantSerials: map[rpc.UID]string{
			{Module: "foo", Service: "action"}: "",
		},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"foo": {
					Path:    "path",
					Args:    []string{"one", "two"},
					Options: map[string]any{"opt": 2},
				},
			},
			Services: map[string]*config.Service{
				"action": {
					Name:    "action",
					Module:  ptr("foo"),
					Serial:  ptr(""),
					Options: map[string]any{"opt": 'a'},
					Listen: []config.Button{
						{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
						{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
					},
				},
			},
		},
	},
	{
		name: "reconfigure_service_config",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			3: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'b'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"path",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'b',
							},
						},
					},
				},
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
			},
		},
		wantSerials: map[rpc.UID]string{
			{Module: "foo", Service: "action"}: "",
		},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"foo": {
					Path:    "path",
					Args:    []string{"one", "two"},
					Options: map[string]any{"opt": 1},
				},
			},
			Services: map[string]*config.Service{
				"action": {
					Name:    "action",
					Module:  ptr("foo"),
					Serial:  ptr(""),
					Options: map[string]any{"opt": 'b'},
					Listen: []config.Button{
						{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
						{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
					},
				},
			},
		},
	},
	{
		name: "reconfigure_service_device",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			3: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "path",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
						},
					},
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"path",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
			{
				Name: "conn",
				Args: []any{
					"foo",
				},
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"foo-1"}}},
			},
		},
		wantSerials: map[rpc.UID]string{
			{Module: "foo", Service: "action"}: "",
		},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"foo": {
					Path:    "path",
					Args:    []string{"one", "two"},
					Options: map[string]any{"opt": 1},
				},
			},
			Services: map[string]*config.Service{
				"action": {
					Name:    "action",
					Module:  ptr("foo"),
					Serial:  ptr(""),
					Options: map[string]any{"opt": 'a'},
					Listen: []config.Button{
						{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
					},
				},
			},
		},
	},
	{
		name: "two_component_reconfigure",
		actions: []any{
			0: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetFuncs(rpc.Funcs{
					"none_func": noneFunc,
				})
			},
			1: func(ctx context.Context, m *Manager[*testKernel, *testDevice, *testButton]) {
				m.SetBuiltins(ctx, map[string]jsonrpc2.Binder{
					"none_builtin": binder("none_builtin"),
				})
			},
			2: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "/path/to/foo",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
				},
				Services: map[string]*config.Service{
					"action1": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			3: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "/path/to/foo",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
					"bar": {
						Path:    "/path/to/bar",
						Args:    []string{"three", "four"},
						Options: map[string]any{"opt": 2},
					},
				},
				Services: map[string]*config.Service{
					"action1": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
					"action2": {
						Module:  ptr("bar"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'b'},
						Listen: []config.Button{
							{Row: 1, Col: 1, Page: "bar-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 0, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			4: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"foo": {
						Path:    "/path/to/foo",
						Args:    []string{"one", "two"},
						Options: map[string]any{"opt": 1},
					},
					"bar": {
						Path:    "/path/to/bar",
						Args:    []string{"three", "four"},
						Options: map[string]any{"opt": 2},
					},
				},
				Services: map[string]*config.Service{
					"action1": {
						Module:  ptr("foo"),
						Serial:  ptr(""),
						Options: map[string]any{"opt": 'a'},
						Listen: []config.Button{
							{Row: 1, Col: 0, Page: "foo-1", Change: ptr("press"), Do: ptr("notify"), Args: []int{1, 2}},
							{Row: 2, Col: 4, Change: ptr("release"), Do: ptr("home")},
						},
					},
				},
			},
			5: &config.System{
				Kernel: &config.Kernel{
					Device:   []config.Device{{Serial: ptr("")}},
					Network:  "unix",
					LogLevel: ptr(slog.LevelDebug),
				},
				Modules: map[string]*config.Module{
					"bar": {
						Path:    "/path/to/bar",
						Args:    []string{"three", "four"},
						Options: map[string]any{"opt": 2},
					},
				},
			},
		},
		wantKernel: []action{
			{
				Name: "funcs",
				Args: []any{
					"none_func",
				},
			},
			{
				Name: "kill",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "builtin",
				Args: []any{
					"none_builtin",
				},
			},
			{
				Name: "spawn",
				Args: []any{
					"foo",
					"/path/to/foo",
					[]string{
						"one",
						"two",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{"foo"},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 1,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"foo",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action1",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'a',
							},
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{"foo"},
			},
			{
				Name: "spawn",
				Args: []any{
					"bar",
					"/path/to/bar",
					[]string{
						"three",
						"four",
					},
				},
			},
			{
				Name: "conn",
				Args: []any{"bar"},
			},
			{
				Name: "notify",
				Args: []any{
					"bar",
					"configure",
					&rpc.Message[*config.Module]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Module{
							Options: map[string]any{
								"opt": 2,
							},
						},
					},
				},
			},
			{
				Name: "notify",
				Args: []any{
					"bar",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:   "action2",
							Active: ptr(true),
							Serial: ptr(""),
							Options: map[string]any{
								"opt": 'b',
							},
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{"foo"},
			},
			{
				Name: "conn",
				Args: []any{"bar"},
			},
			{
				Name: "notify",
				Args: []any{
					"bar",
					"configure",
					&rpc.Message[*config.Service]{
						Time: time.Time{},
						UID:  kernelUID,
						Body: &config.Service{
							Name:    "action2",
							Active:  ptr(false),
							Options: nil,
						},
					},
				},
			},
			{
				Name: "conn",
				Args: []any{"foo"},
			},
			{
				Name: "conn",
				Args: []any{"bar"},
			},
			{
				Name: "conn",
				Args: []any{"foo"},
			},
			{
				Name: "conn",
				Args: []any{"foo"},
			},
			{
				Name: "kill",
				Args: []any{"foo"},
			},
			{
				Name: "conn",
				Args: []any{"bar"},
			},
		},
		wantDevices: map[string][]action{
			"": {
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action1"},
						[]config.Button{
							{
								Col:    0,
								Row:    1,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{
							Module:  "bar",
							Service: "action2",
						},
						[]config.Button{
							{
								Row:    1,
								Col:    1,
								Page:   "bar-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    0,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action1"},
						[]config.Button{
							{
								Row:    1,
								Col:    0,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "bar-1", "foo-1"}}},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "bar", Service: "action2"},
						[]config.Button(nil),
					},
				},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{
							Module:  "foo",
							Service: "action1",
						},
						[]config.Button{
							{
								Row:    1,
								Col:    0,
								Page:   "foo-1",
								Change: ptr("press"),
								Do:     ptr("notify"),
								Args: []int{
									1,
									2,
								},
							},
							{
								Row:    2,
								Col:    4,
								Change: ptr("release"),
								Do:     ptr("home"),
							},
						},
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string{"", "foo-1"}}},
				{
					Name: "send to",
					Args: []any{
						rpc.UID{Module: "foo", Service: "action1"},
						[]config.Button(nil),
					},
				},
				{Name: "set pages", Args: []any{(*string)(nil), []string(nil)}},
			},
		},
		wantSerials: map[rpc.UID]string{},
		wantConfig: &config.System{
			Kernel: &config.Kernel{
				Device:   []config.Device{{Serial: ptr("")}},
				Network:  "unix",
				LogLevel: ptr(slog.LevelDebug),
			},
			Modules: map[string]*config.Module{
				"bar": {
					Path:    "/path/to/bar",
					Args:    []string{"three", "four"},
					Options: map[string]any{"opt": 2},
				},
			},
			Services: nil,
		},
	},
}

func noneFunc(_ context.Context, _ jsonrpc2.ID, _ json.RawMessage) (*rpc.Message[any], error) {
	return rpc.NewMessage[any](rpc.UID{Module: "testing"}, "none"), nil
}

func TestNewManager(t *testing.T) {
	for _, test := range managerTests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var (
				logBuf locked.BytesBuffer
				level  slog.LevelVar
			)
			addSource := slogext.NewAtomicBool(*lines)
			log := slog.New(slogext.NewJSONHandler(&logBuf, &slogext.HandlerOptions{
				Level:     &level,
				AddSource: addSource,
			}))

			k := newTestKernel()
			d := newTestDevice()
			m, err := NewManager[*testKernel, *testDevice, *testButton](k.newKernel, d.newDevice, nil, "datadir", log, &level, addSource)
			if err != nil {
				t.Fatalf("failed to make manager: %v", err)
			}
			defer func() {
				err := m.Close()
				if err != nil {
					t.Errorf("failed to close kernel: %v", err)
				}

				if *verbose {
					t.Logf("log:\n%s\n", &logBuf)
				}
			}()

			for i, action := range test.actions {
				switch action := action.(type) {
				case *config.System:
					err := m.Configure(ctx, action)
					if err != nil {
						t.Errorf("unexpected error step %d", i)
					}
				case func(context.Context, *Manager[*testKernel, *testDevice, *testButton]):
					action(ctx, m)
				default:
					panic(fmt.Sprintf("invalid test action in %s: %d", t.Name(), i))
				}
			}
			gotKernel := m.kernel.actions
			ignoreTime := cmp.FilterValues(
				func(_, _ time.Time) bool { return true },
				cmp.Ignore(),
			)
			if !cmp.Equal(gotKernel, test.wantKernel, ignoreTime) {
				t.Errorf("unexpected kernel result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.wantKernel, gotKernel, ignoreTime))
			}

			gotDevices := make(map[string][]action)
			for serial, dev := range m.devices {
				gotDevices[serial] = dev.actions
			}
			if !cmp.Equal(gotDevices, test.wantDevices) {
				t.Errorf("unexpected devices result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.wantDevices, gotDevices))
			}

			gotSerials := m.serviceSerial
			if !cmp.Equal(gotSerials, test.wantSerials) {
				t.Errorf("unexpected serial table result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.wantSerials, gotSerials))
			}

			gotConfig := m.current
			if !cmp.Equal(gotConfig, test.wantConfig) {
				t.Errorf("unexpected config result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.wantConfig, gotConfig))
			}
		})
	}
}

type recorder struct {
	mu      sync.Mutex
	log     *slog.Logger
	actions []action
}

type action struct {
	Name string
	Args []any
}

func (r *recorder) addAction(name string, args ...any) error {
	r.log.LogAttrs(context.Background(), slog.LevelDebug, "add action", slog.String("action", name), slog.Any("args", args))
	r.mu.Lock()
	r.actions = append(r.actions, action{Name: name, Args: args})
	r.mu.Unlock()
	return nil
}

var sameInstConfigTests = []struct {
	name     string
	old, new *config.Service
	option   cmp.Option
	want     bool
}{
	{
		name:   "boot_nil_cmp_ignoreListen",
		old:    nil,
		new:    &config.Service{},
		option: ignoreListen,
		want:   false,
	},
	{
		name:   "boot_nil_cmp_ignoreOptions",
		old:    nil,
		new:    &config.Service{},
		option: ignoreOptions,
		want:   false,
	},
	{
		name:   "boot_nil_cmp_ignoreDynamic",
		old:    nil,
		new:    &config.Service{},
		option: ignoreDynamic,
		want:   false,
	},
	{
		name: "same_ignoreListen",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreListen,
		want:   true,
	},
	{
		name: "same_ignoreOptions",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreOptions,
		want:   true,
	},
	{
		name: "same_ignoreDynamic",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreDynamic,
		want:   true,
	},
	{
		name: "different_listen_pos_ignoreListen",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 2, Col: 2, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreListen,
		want:   true,
	},
	{
		name: "different_listen_pos_ignoreOptions",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 2, Col: 2, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreOptions,
		want:   false,
	},
	{
		name: "different_listen_pos_ignoreDynamic",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 2, Col: 2, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreDynamic,
		want:   false,
	},
	{
		name: "different_listen_args_ignoreListen",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"one", "two"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreListen,
		want:   true,
	},
	{
		name: "different_listen_args_ignoreOptions",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"one", "two"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreOptions,
		want:   false,
	},
	{
		name: "different_listen_args_ignoreDynamic",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"one", "two"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreDynamic,
		want:   false,
	},
	{
		name: "different_serial_ignoreListen",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("cereal"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"one", "two"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreListen,
		want:   false,
	},
	{
		name: "different_serial_ignoreOptions",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("cereal"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"one", "two"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreOptions,
		want:   false,
	},
	{
		name: "different_serial_ignoreDynamic",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("cereal"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"one", "two"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		option: ignoreDynamic,
		want:   false,
	},
	{
		name: "different_sum_ignoreListen",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{0: 1},
		},
		option: ignoreListen,
		want:   true,
	},
	{
		name: "different_sum_ignoreOptions",
		old: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{},
		},
		new: &config.Service{
			Name:    "svc",
			Active:  ptr(true),
			Module:  ptr("mod"),
			Serial:  ptr("serial"),
			Listen:  []config.Button{{Row: 1, Col: 1, Change: ptr("press"), Do: ptr("action"), Args: map[string]any{"path": "p", "args": []string{"1", "2"}}}},
			Options: map[string]any{"key1": map[string]any{"key2": "val"}},
			Sum:     &config.Sum{0: 1},
		},
		option: ignoreOptions,
		want:   true,
	},
}

func TestSameInstConfig(t *testing.T) {
	for _, test := range sameInstConfigTests {
		t.Run(test.name, func(t *testing.T) {
			const svcKey = "key"
			m := &Manager[*testKernel, *testDevice, *testButton]{}
			if test.old != nil {
				m.current = &config.System{Services: map[string]*config.Service{
					svcKey: test.old,
				}}
			}
			got := m.sameInstConfig(svcKey, test.new, test.option)
			if got != test.want {
				t.Errorf("unexpected result from sameInstConfig: got:%t want:%t", got, test.want)
			}
		})
	}
}
