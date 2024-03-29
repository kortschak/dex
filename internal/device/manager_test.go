// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package device

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sort"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/config"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var pageTransactionTests = []struct {
	name       string
	device     device
	manager    *pageManager
	reqs       []sendToRequest
	pages      setPages
	want       *pageManager
	wantDevice device
}{
	{
		name: "no_change_implicit",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default":  true,
				"foo-page": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "foo", Service: "foo-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "", Change: ptr("press"), Do: ptr("go-foo")},
					{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
				},
			},
		},
		pages: setPages{deflt: nil, pages: []string{"", "foo-page"}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default":  true,
				"foo-page": true,
			},
		},
	},
	{
		name: "no_change_explicit",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default":  true,
				"foo-page": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "foo", Service: "foo-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "", Change: ptr("press"), Do: ptr("go-foo")},
					{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
				},
			},
		},
		pages: setPages{deflt: ptr("default"), pages: []string{"", "foo-page"}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default":  true,
				"foo-page": true,
			},
		},
	},
	{
		name: "change_default",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"bar-page": true,
				"foo-page": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"bar-page": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"bar-page": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "bar", Service: "bar-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
				},
			},
			{
				Service: rpc.UID{Module: "foo", Service: "foo-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
				},
			},
		},
		pages: setPages{deflt: ptr("foo-page"), pages: []string{"foo-page", "bar-page"}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"bar-page": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": nil,
			},
			last: map[string][]sendToRequest{
				"bar-page": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": nil,
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "foo-page",
			pages: map[string]bool{
				"bar-page": true,
				"foo-page": true,
			},
		},
	},
	{
		name: "change_default_modified",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"bar-page": true,
				"foo-page": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"bar-page": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"bar-page": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "bar", Service: "bar-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
				},
			},
			{
				Service: rpc.UID{Module: "foo", Service: "foo-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
				},
			},
		},
		pages: setPages{deflt: ptr("baz-page"), pages: []string{"foo-page", "baz-page"}},
		want: &pageManager{
			last: map[string][]sendToRequest{
				"baz-page": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"baz-page": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			notify: map[svcConn]Notification{
				{rpc.UID{Module: "bar", Service: "bar-1"}, testConn("bar-conn")}: {
					Service: rpc.UID{Module: "bar", Service: "bar-1"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
					},
				},
				{rpc.UID{Module: "foo", Service: "foo-1"}, testConn("foo-conn")}: {
					Service: rpc.UID{Module: "foo", Service: "foo-1"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"foo-page": true,
				"baz-page": true,
			},
		},
	},
	{
		name: "change_default_adding",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"bar-page": true,
				"foo-page": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"bar-page": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"bar-page": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "bar-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "bar", Service: "bar-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
				},
			},
			{
				Service: rpc.UID{Module: "foo", Service: "foo-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
				},
			},
			{
				Service: rpc.UID{Module: "baz", Service: "baz-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-baz")},
				},
			},
		},
		pages: setPages{deflt: ptr("baz-page"), pages: []string{"foo-page", "baz-page"}},
		want: &pageManager{
			last: map[string][]sendToRequest{
				"baz-page": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
					{
						Service: rpc.UID{Module: "baz", Service: "baz-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-baz")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"baz-page": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
						},
						{Module: "baz", Service: "baz-1"}: {
							{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-baz")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
						},
					},
				},
			},
			notify: map[svcConn]Notification{
				{rpc.UID{Module: "bar", Service: "bar-1"}, testConn("bar-conn")}: {
					Service: rpc.UID{Module: "bar", Service: "bar-1"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-foo")},
					},
				},
				{rpc.UID{Module: "baz", Service: "baz-1"}, testConn("baz-conn")}: {
					Service: rpc.UID{Module: "baz", Service: "baz-1"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "baz-page", Change: ptr("press"), Do: ptr("go-baz")},
					},
				},
				{rpc.UID{Module: "foo", Service: "foo-1"}, testConn("foo-conn")}: {
					Service: rpc.UID{Module: "foo", Service: "foo-1"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-bar")},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"baz-page": true,
				"foo-page": true,
			},
		},
	},
	{
		name: "delete",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default":  true,
				"foo-page": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{row: 1, col: 1}: {
						{Module: "foo", Service: "foo-1"}: {
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
				"foo-page": {
					{
						Service: rpc.UID{Module: "foo", Service: "foo-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "foo-page", Change: ptr("press"), Do: ptr("go-default")},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "bar", Service: "bar-1"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
				},
			},
		},
		pages: setPages{deflt: nil, pages: []string{"", "foo-page"}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "bar", Service: "bar-1"}: {
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "bar", Service: "bar-1"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
						},
					},
				},
			},
			notify: map[svcConn]Notification{
				{uid: rpc.UID{Module: "foo", Service: "foo-1"}, Connection: testConn("foo-conn")}: {
					Service: rpc.UID{Module: "foo", Service: "foo-1"},
					Buttons: nil,
				},
				{uid: rpc.UID{Module: "bar", Service: "bar-1"}, Connection: testConn("bar-conn")}: {
					Service: rpc.UID{Module: "bar", Service: "bar-1"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("go-foo")},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
	},
	{
		name: "change_location",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "runner", Service: "subl"}: {
							{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{row: 1, col: 2}: {
						{Module: "runner", Service: "smerge"}: {
							{Row: 1, Col: 2, Page: "default", Image: "data:text/plain,smerge"},
							{Row: 1, Col: 2, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 1, Col: 2, Page: "default", Image: "data:text/plain,smerge"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 1, Col: 2, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "runner", Service: "subl"},
				Actions: []config.Button{
					{Row: 0, Col: 0, Image: "data:text/plain,subl"},
					{Row: 0, Col: 0, Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
				},
			},
			{
				Service: rpc.UID{Module: "runner", Service: "smerge"},
				Actions: []config.Button{
					{Row: 0, Col: 1, Image: "data:text/plain,smerge"},
					{Row: 0, Col: 1, Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
				},
			},
		},
		pages: setPages{deflt: nil, pages: []string{""}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 0, col: 0}: {
						{Module: "runner", Service: "subl"}: {
							{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
							{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{row: 0, col: 1}: {
						{Module: "runner", Service: "smerge"}: {
							{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
							{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			notify: map[svcConn]Notification{
				{uid: rpc.UID{Module: "runner", Service: "subl"}, Connection: testConn("runner-conn")}: {
					Service: rpc.UID{Module: "runner", Service: "subl"},
					Buttons: []config.Button{
						{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
						{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
					},
				},
				{uid: rpc.UID{Module: "runner", Service: "smerge"}, Connection: testConn("runner-conn")}: {
					Service: rpc.UID{Module: "runner", Service: "smerge"},
					Buttons: []config.Button{
						{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
						{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
	},
	{
		name: "change_location_cleanup",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 0, col: 0}: {
						{Module: "runner", Service: "subl"}: {
							{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
							{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{row: 0, col: 1}: {
						{Module: "runner", Service: "smerge"}: {
							{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
							{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 1, Col: 2, Page: "default"},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "runner", Service: "subl"},
				Actions: []config.Button{
					{Row: 0, Col: 0, Image: "data:text/plain,subl"},
					{Row: 0, Col: 0, Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
				},
			},
			{
				Service: rpc.UID{Module: "runner", Service: "smerge"},
				Actions: []config.Button{
					{Row: 0, Col: 1, Image: "data:text/plain,smerge"},
					{Row: 0, Col: 1, Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
				},
			},
		},
		pages: setPages{deflt: nil, pages: []string{""}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 0, col: 0}: {
						{Module: "runner", Service: "subl"}: {
							{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
							{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{row: 0, col: 1}: {
						{Module: "runner", Service: "smerge"}: {
							{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
							{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			notify: map[svcConn]Notification{
				{uid: rpc.UID{Module: "runner", Service: "smerge"}, Connection: testConn("runner-conn")}: {
					Service: rpc.UID{Module: "runner", Service: "smerge"},
					Buttons: []config.Button{
						{Row: 0, Col: 1, Page: "default", Image: "data:text/plain,smerge"},
						{Row: 0, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
					},
				},
				{uid: rpc.UID{Module: "runner", Service: "subl"}, Connection: testConn("runner-conn")}: {
					Service: rpc.UID{Module: "runner", Service: "subl"},
					Buttons: []config.Button{
						{Row: 0, Col: 0, Page: "default", Image: "data:text/plain,subl"},
						{Row: 0, Col: 0, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
	},
	{
		name: "remove_button",
		device: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
		manager: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "runner", Service: "subl"}: {
							{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{row: 1, col: 2}: {
						{Module: "runner", Service: "smerge"}: {
							{Row: 1, Col: 2, Page: "default", Image: "data:text/plain,smerge"},
							{Row: 1, Col: 2, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 1, Col: 2, Page: "default", Image: "data:text/plain,smerge"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "smerge"},
						Actions: []config.Button{
							{Row: 1, Col: 2, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "smerge"}},
						},
					},
				},
			},
		},
		reqs: []sendToRequest{
			{
				Service: rpc.UID{Module: "runner", Service: "subl"},
				Actions: []config.Button{
					{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
					{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
				},
			},
		},
		pages: setPages{deflt: nil, pages: []string{""}},
		want: &pageManager{
			state: map[string]map[pos]map[rpc.UID][]config.Button{
				"default": {
					{row: 1, col: 1}: {
						{Module: "runner", Service: "subl"}: {
							{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
				},
			},
			last: map[string][]sendToRequest{
				"default": {
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
						},
					},
					{
						Service: rpc.UID{Module: "runner", Service: "subl"},
						Actions: []config.Button{
							{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
						},
					},
				},
			},
			notify: map[svcConn]Notification{
				{uid: rpc.UID{Module: "runner", Service: "smerge"}, Connection: testConn("runner-conn")}: {
					Service: rpc.UID{Module: "runner", Service: "smerge"},
					Buttons: nil,
				},
				{uid: rpc.UID{Module: "runner", Service: "subl"}, Connection: testConn("runner-conn")}: {
					Service: rpc.UID{Module: "runner", Service: "subl"},
					Buttons: []config.Button{
						{Row: 1, Col: 1, Page: "default", Image: "data:text/plain,subl"},
						{Row: 1, Col: 1, Page: "default", Change: ptr("press"), Do: ptr("run"), Args: map[string]string{"path": "subl"}},
					},
				},
			},
		},
		wantDevice: &testDevice{
			rows: 3, cols: 5,
			defaultPage: "default",
			pages: map[string]bool{
				"default": true,
			},
		},
	},
}

type setPages struct {
	deflt *string
	pages []string
}

func TestPageTransaction(t *testing.T) {
	for _, test := range pageTransactionTests {
		m := test.manager
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()

			var logBuf bytes.Buffer
			log := slog.New(slogext.NewJSONHandler(&logBuf, &slogext.HandlerOptions{
				Level:     slog.LevelDebug,
				AddSource: slogext.NewAtomicBool(*lines),
			}))
			defer func() {
				if *verbose && logBuf.Len() != 0 {
					t.Logf("log:\n%s\n", &logBuf)
				}
			}()

			m.log = log
			for _, req := range test.reqs {
				m.sendTo(test.device, req.Service, req.Actions)
			}
			m.setPages(ctx, test.device, test.pages.deflt, test.pages.pages)
			m.log = nil

			// Canonicalise button notification ordering.
			for _, n := range m.notify {
				sort.Sort(lexicalButtons(n.Buttons))
			}
			for _, n := range test.want.notify {
				sort.Sort(lexicalButtons(n.Buttons))
			}

			allow := cmp.AllowUnexported(pageManager{}, testDevice{}, sendToRequest{})
			if !cmp.Equal(m, test.want, allow) {
				t.Errorf("unexpected pages result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.want, m, allow))
			}

			if !cmp.Equal(test.device, test.wantDevice, allow) {
				t.Errorf("unexpected device result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(test.wantDevice, test.device, allow))
			}
		})
	}
}

var layoutSetTests = []struct {
	name          string
	a, b          layout
	wantIntersect layout
	wantSubtract  layout
}{
	// Sanity.
	{
		name: "empty",
		a:    nil, b: nil, wantIntersect: layout{}, wantSubtract: layout{},
	},
	{
		name:          "identical",
		a:             layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		b:             layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{},
	},
	{
		name:          "a_empty",
		a:             nil,
		b:             layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "b"}}}}},
		wantIntersect: layout{},
		wantSubtract:  layout{},
	},
	{
		name:          "b_empty",
		a:             layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		b:             nil,
		wantIntersect: layout{},
		wantSubtract:  layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
	},

	// Page level sets.
	{
		name: "a⊂b_page_level",
		a:    layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		b: layout{
			"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}},
			"b": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "b"}}}},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{},
	},
	{
		name: "a⊃b_page_level",
		a: layout{
			"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}},
			"b": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "b"}}}},
		},
		b:             layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{"b": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "b"}}}}},
	},
	{
		name: "patial_overlap_page_level",
		a: layout{
			"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}},
			"b": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "b"}}}},
		},
		b: layout{
			"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}},
			"c": {{3, 3}: {rpc.UID{Module: "c", Service: "c"}: []config.Button{{Page: "c"}}}},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{"b": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "b"}}}}},
	},

	// Position level sets.
	{
		name: "a⊂b_position_level",
		a: layout{
			"a": {
				{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}},
		},
		b: layout{
			"a": {
				{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}},
				{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "b"}}},
			},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{},
	},
	{
		name: "a⊃b_position_level",
		a: layout{
			"a": {
				{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}},
				{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}}},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}},
			},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{"a": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}}}}},
	},
	{
		name: "patial_overlap_position_level",
		a: layout{
			"a": {
				{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}},
				{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}}},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}},
				{3, 3}: {rpc.UID{Module: "c", Service: "c"}: []config.Button{{Page: "a"}}},
			},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{"a": {{2, 2}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}}}}},
	},

	// Module level sets.
	{
		name: "a⊂b_module_level",
		a: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}},
				},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}},
					rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}},
				},
			},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{},
	},
	{
		name: "a⊃b_module_level",
		a: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}},
					rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}},
				},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}},
				},
			},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{"a": {{1, 1}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}}}}},
	},
	{
		name: "patial_overlap_module_level",
		a: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}},
					rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}},
				},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}},
					rpc.UID{Module: "c", Service: "c"}: []config.Button{{Page: "a"}},
				},
			},
		},
		wantIntersect: layout{"a": {{1, 1}: {rpc.UID{Module: "a", Service: "a"}: []config.Button{{Page: "a"}}}}},
		wantSubtract:  layout{"a": {{1, 1}: {rpc.UID{Module: "b", Service: "b"}: []config.Button{{Page: "a"}}}}},
	},

	// Button level sets.
	{
		name: "a⊂b_button_level",
		a: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{
						{Page: "a"},
					},
				},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{
						{Page: "a"},
						{Page: "a", Do: ptr("diff")},
					},
				},
			},
		},
		wantIntersect: layout{ /* no match due to difference in []config.Button */ },
		wantSubtract:  layout{ /* path match */ },
	},
	{
		name: "a⊃b_button_level",
		a: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{
						{Page: "a"},
						{Page: "a", Do: ptr("diff")},
					},
				},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{
						{Page: "a"},
					},
				},
			},
		},
		wantIntersect: layout{ /* no match due to difference in []config.Button */ },
		wantSubtract:  layout{ /* path match */ },
	},
	{
		name: "patial_overlap_button_level",
		a: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{
						{Page: "a"},
						{Page: "a", Do: ptr("diff")},
					},
				},
			},
		},
		b: layout{
			"a": {
				{1, 1}: {
					rpc.UID{Module: "a", Service: "a"}: []config.Button{
						{Page: "a"},
						{Page: "a", Do: ptr("other_diff")},
					},
				},
			},
		},
		wantIntersect: layout{ /* no match due to difference in []config.Button */ },
		wantSubtract:  layout{ /* path match */ },
	},
}

func TestLayoutSets(t *testing.T) {
	for _, test := range layoutSetTests {
		t.Run(test.name, func(t *testing.T) {
			t.Run("intersect", func(t *testing.T) {
				got := intersectLayouts(test.a, test.b)
				if !cmp.Equal(got, test.wantIntersect) {
					t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s",
						cmp.Diff(test.wantIntersect, got))
				}
			})
			t.Run("subtract", func(t *testing.T) {
				got := subtractLayout(test.a, test.b)
				if !cmp.Equal(got, test.wantSubtract) {
					t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s",
						cmp.Diff(test.wantSubtract, got))
				}
			})
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}

type testDevice struct {
	defaultPage string
	pages       map[string]bool
	rows, cols  int
}

func (d *testDevice) Layout() (row, col int) {
	return d.rows, d.cols
}

func (d *testDevice) Key(row, col int) int {
	return row*d.cols + col
}

func (d *testDevice) DefaultName() string {
	return d.defaultPage
}

func (d *testDevice) SetDefaultName(name string) error {
	d.defaultPage = name
	return nil
}

func (d *testDevice) Page(name string) (p Page, ok bool) {
	_, ok = d.pages[name]
	if ok {
		p = Page{d, make([]*Button, d.rows*d.cols)}
		for i := range p.buttons {
			p.buttons[i] = &Button{}
		}
	}
	return p, ok
}

func (d *testDevice) PageNames() []string {
	names := make([]string, 0, len(d.pages))
	for p := range d.pages {
		names = append(names, p)
	}
	sort.Strings(names)
	return names
}

func (d *testDevice) NewPage(name string) error {
	if d.pages[name] {
		return fmt.Errorf("page %q exists", name)
	}
	d.pages[name] = true
	return nil
}

func (d *testDevice) Rename(old, new string) error {
	if d.pages[old] {
		return fmt.Errorf("page %q not found", old)
	}
	if d.pages[new] {
		return fmt.Errorf("page %q exists", new)
	}
	delete(d.pages, old)
	d.pages[new] = true
	return nil
}

func (d *testDevice) Delete(ctx context.Context, name string) error {
	if name == d.defaultPage {
		return fmt.Errorf("page %q is default page", name)
	}
	delete(d.pages, name)
	return nil
}

func (d *testDevice) conn(ctx context.Context, uid string) (rpc.Connection, time.Time, bool) {
	return testConn(uid + "-conn"), time.Time{}, true
}

func (d *testDevice) handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	return nil, nil
}

type testConn string

func (c testConn) Call(ctx context.Context, method string, params any) *jsonrpc2.AsyncCall {
	return nil
}
func (c testConn) Respond(id jsonrpc2.ID, result any, err error) error         { return nil }
func (c testConn) Cancel(id jsonrpc2.ID)                                       {}
func (c testConn) Notify(ctx context.Context, method string, params any) error { return nil }
