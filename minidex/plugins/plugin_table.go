// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package plugins abuses the go build chain to use code as configuration.
package plugins

import "time"

// Actions is the device configuration used by minidex.
var Actions = map[Device]map[Page]map[Button]Action{
	{Model: "", Serial: ""}: {
		"default": {
			{Row: 0, Col: 0}: &gopher{},
			{Row: 0, Col: 1}: &date{formats: []string{time.TimeOnly, time.DateOnly, "Monday"}},
			{Row: 0, Col: 2}: &serial{},
			{Row: 0, Col: 4}: &change{toPage: "debug"},
			{Row: 1, Col: 0}: &command{
				cmd: "subl", args: []string{"-n"},
				icon: "/opt/sublime_text/Icon/128x128/sublime-text.png",
			},
			{Row: 1, Col: 1}: &command{
				cmd:  "smerge",
				icon: "/opt/sublime_merge/Icon/128x128/sublime-merge.png",
			},
			{Row: 1, Col: 2}: &command{
				name: "terminal", cmd: "gnome-terminal", args: []string{"--working-directory=~"},
				icon: "/usr/share/icons/gnome/256x256/apps/gnome-terminal.png",
			},
			{Row: 1, Col: 3}: &command{cmd: "bad"},
			{Row: 2, Col: 0}: &brightness{add: +5},
			{Row: 2, Col: 1}: &brightness{add: -5},
			{Row: 1, Col: 4}: &sleep{blankAfter: time.Minute},
		},
		"debug": {
			{Row: 0, Col: 4}: &change{toPage: "default"},
			{Row: 0, Col: 0}: &dump{},
			{Row: 1, Col: 0}: &logging{add: -1},
			{Row: 2, Col: 0}: &logging{add: +1},
			{Row: 1, Col: 4}: &sleep{blankAfter: time.Minute},
			{Row: 2, Col: 4}: &stop{},
		},
	},
}
