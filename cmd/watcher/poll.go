// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"

	"github.com/kortschak/dex/rpc"
)

func newDetailer(strategy string) (detailer, error) {
	if strategy == "none" {
		return noDetails{}, nil
	}
	if strategy == "" {
		var err error
		strategy, err = defaultStrategy()
		if err != nil {
			return noDetails{}, err
		}
	}
	d, ok := detailers[strategy]
	if !ok {
		return noDetails{}, rpc.NewError(rpc.ErrCodeInvalidData,
			fmt.Sprintf("no detailer strategy: %q", strategy),
			map[string]any{
				"type":     rpc.ErrCodeBounds,
				"strategy": strategy,
			},
		)
	}
	return d()
}

func defaultStrategy() (string, error) {
	if runtime.GOOS == "darwin" {
		return "macos", nil
	}
	switch typ := os.Getenv("XDG_SESSION_TYPE"); typ {
	default:
		return "", fmt.Errorf("unknown session type: %q", typ)
	case "x11":
		return "xorg", nil
	case "wayland":
		xdgCurrentDesktop := os.Getenv("XDG_CURRENT_DESKTOP")
		switch de := strings.Split(xdgCurrentDesktop, ":"); {
		case slices.Contains(de, "GNOME"):
			return "gnome/mutter", nil
		default:
			return "", fmt.Errorf("unknown desktop environment: %q", xdgCurrentDesktop)
		}
	}
}

var detailers = map[string]func() (detailer, error){}
