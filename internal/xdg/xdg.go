// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package xdg provides functions for handling cross-platform configuration,
// data and state directories.
package xdg

import (
	"os"
	"path/filepath"
	"syscall"
)

// Data returns the path to the named file found first in the list of
// data directories obtained from DataHome, and DataDirs if local is false.
// If no file is found Data returns ENOENT.
func Data(name string, local bool) (string, error) {
	return find(name,
		key_XDG_DATA_HOME, def_XDG_DATA_HOME,
		key_XDG_DATA_DIRS, def_XDG_DATA_DIRS,
		_HOME, local)
}

// DataHome returns the path corresponding to XDG_DATA_HOME.
func DataHome() (string, bool) {
	return envOrDefault(key_XDG_DATA_HOME, def_XDG_DATA_HOME, _HOME)
}

// DataDirs returns the path list corresponding to XDG_DATA_DIRS.
func DataDirs() (string, bool) {
	return envOrDefault(key_XDG_DATA_DIRS, def_XDG_DATA_DIRS, "")
}

// Config returns the path to the named file found first in the list of
// config directories obtained from ConfigHome, and ConfigDirs if local is
// false. If no file is found Config returns ENOENT.
func Config(name string, local bool) (string, error) {
	return find(name,
		key_XDG_CONFIG_HOME, def_XDG_CONFIG_HOME,
		key_XDG_CONFIG_DIRS, def_XDG_CONFIG_DIRS,
		_HOME, local)
}

// ConfigHome returns the path corresponding to XDG_CONFIG_HOME.
func ConfigHome() (string, bool) {
	return envOrDefault(key_XDG_CONFIG_HOME, def_XDG_CONFIG_HOME, _HOME)
}

// ConfigDirs returns the path list corresponding to XDG_CONFIG_DIRS.
func ConfigDirs() (string, bool) {
	return envOrDefault(key_XDG_CONFIG_DIRS, def_XDG_CONFIG_DIRS, "")
}

// State returns the path to the named file found in the state directory
// obtained from StateHome. If no file is found State returns ENOENT.
func State(name string) (string, error) {
	return find(name, key_XDG_STATE_HOME, def_XDG_STATE_HOME, "", "", _HOME, true)
}

// StateHome returns the path corresponding to XDG_STATE_HOME.
func StateHome() (string, bool) {
	return envOrDefault(key_XDG_STATE_HOME, def_XDG_STATE_HOME, _HOME)
}

// Cache returns the path to the named file found in the cache directory
// obtained from CacheHome. If no file is found Cache returns ENOENT.
func Cache(name string) (string, error) {
	return find(name, key_XDG_CACHE_HOME, def_XDG_CACHE_HOME, "", "", _HOME, true)
}

// CacheHome returns the path corresponding to XDG_CACHE_HOME.
func CacheHome() (string, bool) {
	return envOrDefault(key_XDG_CACHE_HOME, def_XDG_CACHE_HOME, _HOME)
}

// Runtime returns the path to the named file found in the runtime
// directory obtained from RuntimeDir. If no file is found Runtime
// returns ENOENT.
func Runtime(name string) (string, error) {
	return find(name, key_XDG_RUNTIME_DIR, def_XDG_RUNTIME_DIR, "", "", _HOME, true)
}

// RuntimeDir returns the path corresponding to XDG_RUNTIME_DIR.
func RuntimeDir() (string, bool) {
	return envOrDefault(key_XDG_RUNTIME_DIR, def_XDG_RUNTIME_DIR, _HOME)
}

// finc returns the path to the named file found first in the list of paths
// in the keyed environment variable, or default, prepending home where
// necessary. If local is false, paths outside the user's home are included.
func find(name, keyLocal, defLocal, keyGlobal, defGlobal, home string, local bool) (string, error) {
	base, ok := envOrDefault(keyLocal, defLocal, home)
	if ok {
		path := filepath.Join(base, name)
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}
	}
	if local {
		return "", syscall.ENOENT
	}
	list, ok := envOrDefault(keyGlobal, defGlobal, "")
	if !ok {
		return "", syscall.ENOENT
	}
	for _, base := range filepath.SplitList(list) {
		path := filepath.Join(base, name)
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}
	}
	return "", syscall.ENOENT
}

// envOrDefault return the path or path list corresponding to the provided
// key and default. If home is not empty, the default is treated as an absolute
// path or path list and returned unaltered, otherwise the default is returned
// relative to home.
func envOrDefault(key, def, home string) (string, bool) {
	if key != "" {
		val, ok := os.LookupEnv(key)
		if ok {
			return val, true
		}
	}
	if def == "" {
		return "", false
	}
	if home == "" || filepath.IsAbs(def) {
		return def, true
	}
	base, ok := os.LookupEnv(home)
	if !ok {
		return "", false
	}
	return filepath.Join(base, def), true
}
