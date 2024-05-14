// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build darwin

package localtime

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Foundation
#include <Foundation/Foundation.h>

char *currentTimezone()
{
	@autoreleasepool {
		CFTimeZoneResetSystem();
		CFTimeZoneRef tz = CFTimeZoneCopySystem();
		const char *id = CFStringGetCStringPtr(CFTimeZoneGetName(tz), kCFStringEncodingUTF8);
		if (id != NULL) {
			return strdup(id);
		}
		return NULL;
	}
}
*/
import "C"

import (
	"errors"
	"time"
	"unsafe"
)

// Dynamic is a handle to obtain the current local timezone. It is safe for
// concurrent use.
type Dynamic struct{}

// NewDynamic returns a Dynamic for the current system local timezone.
func NewDynamic() (*Dynamic, error) {
	return &Dynamic{}, nil
}

// Location returns the current system local timezone. If Location fails it
// returns time.UTC and a non-nil error.
func (*Dynamic) Location() (*time.Location, error) {
	tz := C.currentTimezone()
	if tz == nil {
		return time.UTC, errors.New("could not get current timezone")
	}
	loc, err := time.LoadLocation(C.GoString(tz))
	C.free(unsafe.Pointer(tz))
	if err != nil {
		return time.UTC, err
	}
	return loc, nil
}

// Close is a no-op.
func (*Dynamic) Close() error {
	return nil
}
