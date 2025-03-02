// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rogpeppe/go-internal/gotooltest"
	"github.com/rogpeppe/go-internal/testscript"
)

var (
	update = flag.Bool("update", false, "update tests")
	keep   = flag.Bool("keep", false, "keep $WORK directory after tests")

	// postgres indicates tests were invoked with -tags postgres.
	postgres bool
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
			"expand":         expand,
			"createdb":       createDB,
			"grant_read":     grantReadAccess,
		},
		Setup: func(e *testscript.Env) error {
			pwd, err := os.Getwd()
			if err != nil {
				return err
			}
			e.Setenv("PKG_ROOT", pwd)
			for _, k := range []string{
				"PGUSER", "PGPASSWORD",
				"PGHOST", "PGPORT",
				"POSTGRES_DB",
			} {
				if v, ok := os.LookupEnv(k); ok {
					e.Setenv(k, v)
				}
			}
			return nil
		},
		Condition: func(cond string) (bool, error) {
			switch cond {
			case "postgres":
				return postgres, nil
			default:
				return false, fmt.Errorf("unknown condition: %s", cond)
			}
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

func expand(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! expand")
	}
	if len(args) != 2 {
		ts.Fatalf("usage: expand src dst")
	}
	src, err := os.ReadFile(ts.MkAbs(args[0]))
	ts.Check(err)
	src = []byte(os.Expand(string(src), func(key string) string {
		return ts.Getenv(key)
	}))
	err = os.WriteFile(ts.MkAbs(args[1]), src, 0o644)
	ts.Check(err)
}

func createDB(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! createdb")
	}
	if len(args) != 2 {
		ts.Fatalf("usage: createdb postgres://user:password@host:port/server new_db")
	}
	u, err := url.Parse(args[0])
	ts.Check(err)
	createTestDB(ts, u, args[1])
}

func createTestDB(ts *testscript.TestScript, u *url.URL, dbname string) {
	ctx := context.Background()
	db, err := pgx.Connect(ctx, u.String())
	if err != nil {
		ts.Fatalf("failed to open admin database: %v", err)
	}
	_, err = db.Exec(ctx, "create database "+dbname)
	if err != nil {
		ts.Fatalf("failed to create test database: %v", err)
	}
	err = db.Close(ctx)
	if err != nil {
		ts.Fatalf("failed to close admin connection: %v", err)
	}

	ts.Defer(func() {
		dropTestDB(ts, ctx, u, dbname)
	})
}

func dropTestDB(ts *testscript.TestScript, ctx context.Context, u *url.URL, dbname string) {
	db, err := pgx.Connect(ctx, u.String())
	if err != nil {
		ts.Logf("failed to open admin database: %v", err)
		return
	}
	_, err = db.Exec(ctx, "drop database if exists "+dbname)
	if err != nil {
		ts.Logf("failed to drop test database: %v", err)
		return
	}
	err = db.Close(ctx)
	if err != nil {
		ts.Logf("failed to close admin connection: %v", err)
	}
}

func grantReadAccess(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! grant_read")
	}
	if len(args) != 2 {
		ts.Fatalf("usage: grant_read postgres://user:password@host:port/dbname target_user")
	}

	u, err := url.Parse(args[0])
	ts.Check(err)
	ctx := context.Background()
	db, err := pgx.Connect(ctx, u.String())
	if err != nil {
		ts.Fatalf("failed to open database: %v", err)
	}

	target := args[1]
	statements := []string{
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", strings.TrimLeft(u.Path, "/"), target),
		fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s", target),
		fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s", target),
	}
	for _, s := range statements {
		_, err = db.Exec(ctx, s)
		if err != nil {
			ts.Logf("failed to execute grant: %q %v", s, err)
			break
		}
	}

	err = db.Close(ctx)
	if err != nil {
		ts.Fatalf("failed to close connection: %v", err)
	}
}

func get() int {
	jsonData := flag.Bool("json", false, "data from GET is JSON")
	headers := flag.String("header", "", "destination for headers")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: GET [-json] [-header <out-file>] <url>")
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
	if *headers != "" {
		var buf bytes.Buffer
		resp.Header.WriteSubset(&buf, map[string]bool{"Date": true})
		err = os.WriteFile(*headers, buf.Bytes(), 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed writing response headers: %v\n", err)
			return 1
		}
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
	headers := flag.String("header", "", "destination for headers")
	content := flag.String("content", "", "data content-type")
	flag.Parse()
	if flag.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: POST [-json] [-header <out-file>] [-content <content-type>] <body-path> <url>")
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
	if *headers != "" {
		var buf bytes.Buffer
		resp.Header.WriteSubset(&buf, map[string]bool{"Date": true})
		err = os.WriteFile(*headers, buf.Bytes(), 0o600)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed writing response headers: %v\n", err)
			return 1
		}
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
