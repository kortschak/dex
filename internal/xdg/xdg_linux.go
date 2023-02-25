// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package xdg

// https://specifications.freedesktop.org/basedir-spec/basedir-spec-0.8.html
const (
	_HOME = "HOME"

	// $XDG_DATA_HOME or $HOME/.local/share
	key_XDG_DATA_HOME = "XDG_DATA_HOME"
	def_XDG_DATA_HOME = ".local/share"

	// $XDG_DATA_DIRS or /usr/local/share/:/usr/share/
	key_XDG_DATA_DIRS = "XDG_DATA_DIRS"
	def_XDG_DATA_DIRS = "/usr/local/share/:/usr/share/"

	// $XDG_CONFIG_HOME or $HOME/.config
	key_XDG_CONFIG_HOME = "XDG_CONFIG_HOME"
	def_XDG_CONFIG_HOME = ".config"

	// $XDG_CONFIG_DIRS or /etc/xdg
	key_XDG_CONFIG_DIRS = "XDG_CONFIG_DIRS"
	def_XDG_CONFIG_DIRS = "/etc/xdg"

	// $XDG_STATE_HOME or $HOME/.local/state
	key_XDG_STATE_HOME = "XDG_STATE_HOME"
	def_XDG_STATE_HOME = ".local/state"

	// $XDG_CACHE_HOME or $HOME/.cache
	key_XDG_CACHE_HOME = "XDG_CACHE_HOME"
	def_XDG_CACHE_HOME = ".cache"

	// $XDG_RUNTIME_DIR
	// Fail rather than construct.
	key_XDG_RUNTIME_DIR = "XDG_RUNTIME_DIR"
	def_XDG_RUNTIME_DIR = ""
)
