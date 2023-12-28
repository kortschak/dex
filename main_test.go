// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/gotooltest"
	"github.com/rogpeppe/go-internal/testscript"
)

var (
	update = flag.Bool("update", false, "update tests")
	keep   = flag.Bool("keep", false, "keep $WORK directory after tests")
)

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"dex": Main,
		"GET": get,
	}))
}

func TestScripts(t *testing.T) {
	t.Parallel()

	p := testscript.Params{
		Dir:           filepath.Join("testdata"),
		UpdateScripts: *update,
		TestWork:      *keep,
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"sleep":          sleep,
			"grep_from_file": grep,
		},
	}
	if err := gotooltest.Setup(&p); err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, p)
}

func sleep(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! sleep")
	}
	if len(args) != 1 {
		ts.Fatalf("usage: sleep duration")
	}
	d, err := time.ParseDuration(args[0])
	ts.Check(err)
	time.Sleep(d)
}

func grep(ts *testscript.TestScript, neg bool, args []string) {
	if len(args) != 2 {
		ts.Fatalf("usage: grep_from_file pattern_file data")
	}
	pattern, err := os.ReadFile(ts.MkAbs(args[0]))
	ts.Check(err)
	data, err := os.ReadFile(ts.MkAbs(args[1]))
	ts.Check(err)
	re, err := regexp.Compile("(?m)" + string(pattern))
	ts.Check(err)

	if neg {
		if re.Match(data) {
			ts.Logf("[grep_from_file]\n%s\n", data)
			ts.Fatalf("unexpected match for %#q found in grep_from_file: %s\n", pattern, re.Find(data))
		}
	} else {
		if !re.Match(data) {
			ts.Logf("[grep_from_file]\n%s\n", data)
			ts.Fatalf("no match for %#q found in grep_from_file", pattern)
		}
	}
}

func get() int {
	jsonData := flag.Bool("json", false, "data from GET is JSON")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: GET [-json] <url>")
		return 2
	}
	resp, err := http.Get(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed get: %v\n", err)
		return 1
	}
	var buf bytes.Buffer
	_, err = io.Copy(&buf, resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed copy buffer: %v\n", err)
		return 1
	}
	err = resp.Body.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed body close: %v\n", err)
		return 1
	}
	if *jsonData {
		var dst bytes.Buffer
		err = json.Indent(&dst, buf.Bytes(), "", "\t")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed format JSON data: %v\n", err)
			return 1
		}
		buf = dst
	}
	os.Stdout.Write(buf.Bytes())
	if !bytes.HasSuffix(buf.Bytes(), []byte{'\n'}) {
		fmt.Println()
	}
	return 0
}
