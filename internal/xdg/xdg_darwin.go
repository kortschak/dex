// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package xdg

// Table 1-1 https://developer.apple.com/library/archive/documentation/General/Conceptual/MOSXAppProgrammingGuide/AppRuntime/AppRuntime.html
const (
	_HOME = "HOME"

	key_XDG_DATA_HOME = ""
	def_XDG_DATA_HOME = "Library/Application Support"

	key_XDG_DATA_DIRS = ""
	def_XDG_DATA_DIRS = "/Library/Application Support"

	key_XDG_CONFIG_HOME = ""
	def_XDG_CONFIG_HOME = "Library/Application Support"

	key_XDG_CONFIG_DIRS = ""
	def_XDG_CONFIG_DIRS = "/Library/Application Support"

	key_XDG_STATE_HOME = ""
	def_XDG_STATE_HOME = "Library/Application Support"

	key_XDG_CACHE_HOME = ""
	def_XDG_CACHE_HOME = "Library/Caches"

	key_XDG_RUNTIME_DIR = ""
	def_XDG_RUNTIME_DIR = "Library/Application Support"
)
