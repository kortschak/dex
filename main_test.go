// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/tls"
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
		"dex":  Main,
		"GET":  get,
		"POST": post,
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
		Setup: func(e *testscript.Env) error {
			pwd, err := os.Getwd()
			if err != nil {
				return err
			}
			e.Setenv("PKG_ROOT", pwd)
			return nil
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
	cli := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := cli.Get(flag.Arg(0))
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

func post() int {
	jsonData := flag.Bool("json", false, "response data from POST is JSON")
	content := flag.String("content", "", "data content-type")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: POST [-json] [-content <content-type>] <body-path> <url>")
		return 2
	}
	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read request body: %v\n", err)
		return 1
	}
	defer f.Close()
	cli := http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	resp, err := cli.Post(flag.Arg(1), *content, f)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed post: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "failed response body close: %v\n", err)
		return 1
	}
	if *jsonData {
		var dst bytes.Buffer
		err = json.Indent(&dst, buf.Bytes(), "", "\t")
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed format response JSON data: %v\n", err)
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
