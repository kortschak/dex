// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build unix

package dl

/*
#cgo LDFLAGS: -ldl
#include <stdlib.h>
#include <dlfcn.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

const (
	RTLD_LAZY     = int(C.RTLD_LAZY)
	RTLD_NOW      = int(C.RTLD_NOW)
	RTLD_GLOBAL   = int(C.RTLD_GLOBAL)
	RTLD_LOCAL    = int(C.RTLD_LOCAL)
	RTLD_NODELETE = int(C.RTLD_NODELETE)
	RTLD_NOLOAD   = int(C.RTLD_NOLOAD)
	RTLD_DEEPBIND = int(C.RTLD_DEEPBIND)
)

// Open opens the dynamic library corresponding to the first found library name
// in names. See man 3 dlopen for details.
func Open(flags int, names ...string) (*Lib, error) {
	for _, n := range names {
		filename := C.CString(n)
		h := C.dlopen(filename, C.int(flags))
		C.free(unsafe.Pointer(filename))
		if h != nil {
			return &Lib{
				handle: h,
				name:   n,
			}, nil
		}
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, names)
}

// Symbol takes a symbol name and returns a pointer to the symbol.
func (l *Lib) Symbol(name string) (unsafe.Pointer, error) {
	C.dlerror()

	sym := C.CString(name)
	s := C.dlsym(l.handle, sym)
	C.free(unsafe.Pointer(sym))
	dlErrMsg := C.dlerror()
	if dlErrMsg != nil {
		return nil, fmt.Errorf("could not find %s: %w", name, Error(C.GoString(dlErrMsg)))
	}
	return s, nil
}

// Close closes the receiver, unloading the library. Symbols must not be used
// after Close has been called.
func (l *Lib) Close() error {
	if l == nil || l.handle == nil {
		return nil
	}
	C.dlerror()

	C.dlclose(l.handle)
	l.handle = nil
	dlErrMsg := C.dlerror()
	if dlErrMsg != nil {
		return fmt.Errorf("error closing: %w", Error(C.GoString(dlErrMsg)))
	}
	return nil
}
