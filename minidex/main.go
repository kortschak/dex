// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The minidex command builds and runs a minidex/dex controller.
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"golang.org/x/sys/execabs"
)

func main() {
	ctx := context.Background()

	gopath, err := goenv(ctx, "GOPATH")
	if err != nil {
		log.Fatal(err)
	}
	var path string
	if info, ok := debug.ReadBuildInfo(); ok {
		path = filepath.Join(gopath, "src", info.Path, "dex")
	}

	args := append([]string{"run", "."}, os.Args[1:]...)
	err = cmd(ctx, os.Stdout, os.Stderr, path, args...).Run()
	if err != nil {
		log.Fatal(err)
	}
}

// goenv returns the requested go env variable.
func goenv(ctx context.Context, name string) (string, error) {
	var stdout, stderr bytes.Buffer
	err := cmd(ctx, &stdout, &stderr, ".", "env", name).Run()
	if err != nil {
		return "", fmt.Errorf("%s: %w", &stderr, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// cmd is a go command runner helper.
func cmd(ctx context.Context, stdout, stderr io.Writer, wd string, args ...string) *execabs.Cmd {
	cmd := execabs.CommandContext(ctx, "go", args...)
	cmd.Dir = wd
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd
}
