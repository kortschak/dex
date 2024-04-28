// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package dl implements dlopen and related functionality.
package dl

import (
	"errors"
	"unsafe"
)

var ErrNotFound = errors.New("so lib not found")

// Lib represents an open handle to a dynamically loaded library.
type Lib struct {
	handle unsafe.Pointer
	name   string
}

// Name returns the resolved name of the library.
func (l *Lib) Name() string { return l.name }

// Error is a dlerror error message.
type Error string

func (e Error) Error() string { return string(e) }
