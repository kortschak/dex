// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"encoding/hex"
	"flag"
	"fmt"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func ptr[T any](v T) *T { return &v }

func mustSum(text string) *Sum {
	if len(text) != hex.EncodedLen(len(Sum{})) {
		panic(fmt.Sprintf("invalid length: %d != %d", len(text), hex.EncodedLen(len(Sum{}))))
	}
	b, err := hex.DecodeString(text)
	if err != nil {
		panic(err)
	}
	return (*Sum)(b)
}
