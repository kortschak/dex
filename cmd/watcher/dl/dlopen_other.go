// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !unix

package dl

import (
	"errors"
	"unsafe"
)

const (
	RTLD_LAZY     = 0
	RTLD_NOW      = 0
	RTLD_GLOBAL   = 0
	RTLD_LOCAL    = 0
	RTLD_NODELETE = 0
	RTLD_NOLOAD   = 0
	RTLD_DEEPBIND = 0
)

var errNotImplemented = errors.New("not implemented")

// Open is not implemented on this platform.
func Open(_ int, _ ...string) (*Lib, error) {
	return nil, errNotImplemented
}

// Symbol is not implemented on this platform.
func (l *Lib) Symbol(name string) (unsafe.Pointer, error) {
	return nil, errNotImplemented
}

// Close is not implemented on this platform.
func (l *Lib) Close() error {
	return errNotImplemented
}
