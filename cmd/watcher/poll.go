// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"

	"github.com/kortschak/dex/rpc"
)

func newDetailer(strategy string) (detailer, error) {
	if strategy == "none" {
		return noDetails{}, nil
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

var detailers = map[string]func() (detailer, error){}
