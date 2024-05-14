// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package localtime provides a dynamically updated time.Local value.
package localtime

import (
	"time"
)

// Static is a handle to obtain the runtime's static local timezone. It is safe
// for concurrent use.
type Static struct{}

// Location returns time.Local. It does not fail.
func (Static) Location() (*time.Location, error) {
	return time.Local, nil
}
