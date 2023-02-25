// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package version prints the build version.
package version

import (
	"errors"
	"fmt"
	"runtime/debug"
)

// Print prints the build version.
func Print() error {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return errors.New("no build info")
	}
	var revision, modified string
	for _, bs := range bi.Settings {
		switch bs.Key {
		case "vcs.revision":
			revision = bs.Value
		case "vcs.modified":
			modified = bs.Value
		}
	}
	if revision == "" {
		fmt.Println(bi.Main.Version)
		return nil
	}
	switch modified {
	case "true":
		fmt.Println(bi.Main.Version, revision, "(modified)")
	case "false":
		fmt.Println(bi.Main.Version, revision)
	default:
		// This should never happen.
		fmt.Println(bi.Main.Version, revision, modified)
	}
	return nil
}
