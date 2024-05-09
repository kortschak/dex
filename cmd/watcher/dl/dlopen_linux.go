// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package dl

/*
#include <dlfcn.h>
*/
import "C"

const RTLD_DEEPBIND = int(C.RTLD_DEEPBIND)
