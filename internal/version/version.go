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
	v, err := String()
	if err != nil {
		return err
	}
	fmt.Println(v)
	return nil
}

// String returns the build version.
func String() (string, error) {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", errors.New("no build info")
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
		return bi.Main.Version, nil
	}
	switch modified {
	case "true":
		return fmt.Sprintf("%s %s (modified)", bi.Main.Version, revision), nil
	case "false":
		return fmt.Sprintf("%s %s", bi.Main.Version, revision), nil
	default:
		// This should never happen.
		return fmt.Sprintf("%s %s %s", bi.Main.Version, revision, modified), nil
	}
}
