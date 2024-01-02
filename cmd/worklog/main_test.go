// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/jsonrpc2"
	"github.com/rogpeppe/go-internal/testscript"
	"golang.org/x/sys/execabs"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
	"github.com/kortschak/dex/cmd/worklog/store"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	update  = flag.Bool("update", false, "update tests")
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
	keep    = flag.Bool("keep", false, "keep database directories after tests")
)

func TestDaemon(t *testing.T) {
	exePath := filepath.Join(t.TempDir(), "worklog")
	out, err := execabs.Command("go", "build", "-o", exePath, "-race").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build daemon: %v\n%s", err, out)
	}

	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			verbose := slogext.NewAtomicBool(*verbose)
			var (
				level slog.LevelVar
				buf   bytes.Buffer
			)
			h := slogext.NewJSONHandler(&buf, &slogext.HandlerOptions{
				Level:     &level,
				AddSource: slogext.NewAtomicBool(*lines),
			})
			g := slogext.NewPrefixHandlerGroup(&buf, h)
			level.Set(slog.LevelDebug)
			log := slog.New(g.NewHandler("🔷 "))

			ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Second, errors.New("test waited too long"))
			defer cancel()

			kernel, err := rpc.NewKernel(ctx, network, jsonrpc2.NetListenOptions{}, log)
			if err != nil {
				t.Fatalf("failed to start kernel: %v", err)
			}
			// Catch failures to terminate.
			closed := make(chan struct{})
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case <-ctx.Done():
					t.Error("failed to close server")
					verbose.Store(true)
				case <-closed:
				}
			}()
			defer func() {
				err = kernel.Close()
				if err != nil {
					t.Errorf("failed to close kernel: %v", err)
				}
				close(closed)
				wg.Wait()
				if verbose.Load() {
					t.Logf("log:\n%s\n", &buf)
				}
			}()

			uid := rpc.UID{Module: "worklog"}
			err = kernel.Spawn(ctx, os.Stdout, g.NewHandler("🔶 "), uid.Module,
				exePath, "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn worklog: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatalf("failed to get daemon conn: %v: %v", ctx.Err(), context.Cause(ctx))
			}

			t.Run("configure", func(t *testing.T) {
				beat := &rpc.Duration{Duration: time.Second / 2}

				var resp rpc.Message[string]

				type options struct {
					Web         *worklog.Web            `json:"web,omitempty"`
					DatabaseDir string                  `json:"database_dir,omitempty"` // Relative to XDG_STATE_HOME.
					Hostname    string                  `json:"hostname,omitempty"`
					Heartbeat   *rpc.Duration           `json:"heartbeat,omitempty"`
					Rules       map[string]worklog.Rule `json:"rules,omitempty"`
				}
				err := conn.Call(ctx, "configure", rpc.NewMessage(uid, worklog.Config{
					Options: options{
						Heartbeat: beat,
						Rules: map[string]worklog.Rule{
							"afk":    {Name: "afk-watcher", Type: "afkstatus", Src: `{"bucket":"afk","end":new.last_input}`},
							"app":    {Name: "app-watcher", Type: "app", Src: `{"bucket":"app","end":new.time,"data":{"name":new.name}}`},
							"window": {Name: "window-watcher", Type: "currentwindow", Src: `{"bucket":"window","end":new.time,"data":{"window":new.window}}`},
						},
					},
				})).Await(ctx, &resp)
				if err != nil {
					t.Errorf("failed configure call: %v", err)
				}
				if resp.Body != "done" {
					t.Errorf("unexpected response body: got:%s want:done", resp.Body)
				}
			})

			clock := time.Date(2023, time.May, 14, 15, 3, 31, 0, time.UTC)
			now := func(delta time.Duration) time.Time {
				clock = clock.Add(delta)
				return clock
			}
			events := []worklog.Report{
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program1", WindowName: "Task", LastInput: now(0).Add(-time.Second)}},
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program1", WindowName: "Task1", LastInput: now(0).Add(-time.Second)}},
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program2", WindowName: "Task2", LastInput: now(0).Add(-2 * time.Second)}},
				{Time: now(time.Second), Details: &worklog.WatcherDetails{Name: "program2", WindowName: "Task3", LastInput: now(0).Add(-time.Second)}},
			}
			for _, e := range events {
				err := conn.Notify(ctx, "record", rpc.NewMessage(uid, e))
				if err != nil {
					t.Errorf("failed run notify: %v", err)
				}
			}

			time.Sleep(1 * time.Second) // Let some updates and heartbeats past.

			t.Run("stop", func(t *testing.T) {
				err := conn.Notify(ctx, "stop", rpc.NewMessage(uid, rpc.None{}))
				if err != nil {
					t.Errorf("failed stop call: %v", err)
				}
			})

			time.Sleep(time.Second) // Let kernel complete final logging.
		})
	}
}

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"merge_afk":      mergeAfk,
		"dashboard_data": dashboardData,
	}))
}

func TestContinuation(t *testing.T) {
	t.Parallel()

	p := testscript.Params{
		Dir:           filepath.Join("testdata", "continuation"),
		UpdateScripts: *update,
		TestWork:      *keep,
	}
	testscript.Run(t, p)
}

func mergeAfk() int {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage of %s:

  %[1]s [-verbose] -data <data.json> <src.cel>

`, os.Args[0])
		flag.PrintDefaults()
	}
	dataPath := flag.String("data", "", "path to JSON stream data holding input events")
	verbose := flag.Bool("verbose", false, "print full logging")
	flag.Parse()
	if *dataPath == "" {
		flag.Usage()
		return 2
	}
	if len(flag.Args()) != 1 {
		flag.Usage()
		return 2
	}

	src, err := os.ReadFile(flag.Args()[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	data, err := os.ReadFile(*dataPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	var (
		level     slog.LevelVar
		addSource = slogext.NewAtomicBool(*lines)
		logDst    = io.Discard
	)
	if *verbose {
		logDst = os.Stderr
	}
	log := slog.New(slogext.NewJSONHandler(logDst, &slogext.HandlerOptions{
		Level:     slog.LevelDebug - 1,
		AddSource: addSource,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := newDaemon("worklog", log, &level, addSource, ctx, cancel)
	err = d.openDB(ctx, nil, "db.sqlite3", "localhost")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create db: %v\n", err)
		return 1
	}
	d.configureRules(ctx, map[string]worklog.Rule{
		"afk": {
			Name: "afk",
			Type: "afk",
			Src:  string(src),
		},
	})
	db := d.db.Load().(*store.DB)
	defer db.Close()
	d.configureDB(ctx, db)

	dec := json.NewDecoder(bytes.NewReader(data))
	var last, curr worklog.Report
	for {
		err = dec.Decode(&curr)
		if err != nil {
			if err == io.EOF {
				break
			}
			fmt.Fprintf(os.Stderr, "unexpected error reading test data: %v\n", err)
			return 1
		}
		d.record(ctx, rpc.UID{Module: "watcher"}, curr, last)
		last = curr
	}

	dump, err := db.Dump()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to dump db: %v\n", err)
		return 1
	}
	enc := json.NewEncoder(os.Stdout)
	for _, b := range dump {
		for _, e := range b.Events {
			err := enc.Encode(e)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to encode event: %v\n", err)
				return 1
			}
		}
	}
	return 0
}

func TestDashboardData(t *testing.T) {
	t.Parallel()

	p := testscript.Params{
		Dir:           filepath.Join("testdata", "dashboard"),
		UpdateScripts: *update,
		TestWork:      *keep,
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"gen_testdata": generateData,
		},
	}
	testscript.Run(t, p)
}

func dashboardData() int {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage of %s:

  %[1]s [-verbose] -rules <rules.toml> -raw <bool> -data <dump.json> <date>

`, os.Args[0])
		flag.PrintDefaults()
	}
	rulesPath := flag.String("rules", "", "path to a TOML file holding dashboard rules")
	raw := flag.Bool("raw", false, "collect raw event data")
	datePath := flag.String("data", "", "path to JSON data holding a worklog store db dump")
	tz := flag.String("tz", "", "timezone for date")
	verbose := flag.Bool("verbose", false, "print full logging")
	flag.Parse()
	if *rulesPath == "" {
		flag.Usage()
		return 2
	}
	if *datePath == "" {
		flag.Usage()
		return 2
	}
	if len(flag.Args()) != 1 || flag.Args()[0] == "" {
		flag.Usage()
		return 2
	}

	data, err := os.ReadFile(*datePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	ruleBytes, err := os.ReadFile(*rulesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	loc := time.UTC
	if *tz != "" {
		loc, err = locationFor(*tz)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
	}
	date, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(flag.Args()[0]), loc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse date: %v\n", err)
		return 2
	}

	var (
		level     slog.LevelVar
		addSource = slogext.NewAtomicBool(*lines)
		logDst    = io.Discard
	)
	if *verbose {
		logDst = os.Stderr
	}
	log := slog.New(slogext.NewJSONHandler(logDst, &slogext.HandlerOptions{
		Level:     slog.LevelDebug - 1,
		AddSource: addSource,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d := newDaemon("worklog", log, &level, addSource, ctx, cancel)
	err = d.openDB(ctx, nil, "db.sqlite3", "localhost")
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create db: %v\n", err)
		return 1
	}
	db := d.db.Load().(*store.DB)
	defer db.Close()
	d.configureDB(ctx, db)

	var buckets []worklog.BucketMetadata
	err = json.Unmarshal(data, &buckets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to unmarshal db dump: %v\n", err)
		return 1
	}
	err = db.Load(buckets)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load db dump: %v\n", err)
		return 1
	}

	var rules map[string]map[string]worklog.WebRule
	err = toml.Unmarshal(ruleBytes, &rules)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to unmarshal rules: %v\n", err)
		return 1
	}
	d.configureWebRule(ctx, rules)

	var webRules map[string]map[string]ruleDetail
	switch rules := d.dashboardRules.Load().(type) {
	case map[string]map[string]ruleDetail:
		webRules = rules
	default:
		fmt.Fprintf(os.Stderr, "invalid web rule type: %T\n", rules)
		return 1
	}
	events, err := d.eventData(ctx, db, webRules, date, *raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to get event data: %v\n", err)
		return 1
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "\t")
	err = enc.Encode(events)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode events: %v\n", err)
		return 1
	}
	return 0
}

func generateData(ts *testscript.TestScript, neg bool, args []string) {
	if neg {
		ts.Fatalf("unsupported: ! gen_data")
	}

	flags := flag.NewFlagSet("", flag.ContinueOnError)
	cen := flags.String("cen", "", "centre date of generated data")
	tz := flags.String("tz", "UTC", "timezone for date")
	radius := flags.Int("radius", 7, "radius of generated data")
	tmplt := flags.String("tmplt", "", "generated data template")
	err := flags.Parse(args)
	if err != nil {
		ts.Check(err)
	}
	if *tmplt == "" || len(flags.Args()) != 1 {
		ts.Fatalf(`Usage of gen_data:

  gen_data -cen <date> -tz <timezone> -radius <days> -tmplt <template.json> <outfile>

`)
	}
	path := flags.Arg(0)

	loc, err := locationFor(*tz)
	ts.Check(err)
	t, err := time.ParseInLocation(time.DateOnly, *cen, loc)
	ts.Check(err)
	start := t.AddDate(0, 0, -*radius)

	data, err := os.ReadFile(ts.MkAbs(*tmplt))
	ts.Check(err)
	var template struct {
		Buckets map[string]worklog.BucketMetadata `json:"buckets"`
		Offsets []time.Duration                   `json:"offsets"`
		Pattern []map[string]any                  `json:"pattern"`
	}
	ts.Check(json.Unmarshal(data, &template))
	names := make([]string, 0, len(template.Buckets))
	for n, b := range template.Buckets {
		names = append(names, n)
		b.Created = start
		template.Buckets[n] = b
	}
	sort.Strings(names)
	buckets := make([]worklog.BucketMetadata, len(names))
	bucketIndex := make(map[string]int, len(names))
	for i, n := range names {
		bucketIndex[n] = i
		buckets[i] = template.Buckets[n]
	}

	for d := start; d.Before(start.AddDate(0, 0, *radius*2)); d = d.AddDate(0, 0, 1) {
		for _, h := range template.Offsets {
			for _, e := range template.Pattern {
				var delta time.Duration
				ed, ok := e["duration"]
				if ok {
					switch d := ed.(type) {
					case time.Duration:
						delta = d
					case float64:
						delta = time.Duration(d)
					default:
						ts.Fatalf("duration is not time.Duration: %T", ed)
					}
				}
				for _, n := range names {
					if _, ok := e[n]; !ok {
						continue
					}
					data, ok := e[n].(map[string]any)
					if !ok {
						ts.Fatalf("%s is not map[string]any: %T", n, e[n])
					}
					event := worklog.Event{
						Bucket: n,
						Start:  d.Add(h),
						End:    d.Add(h + delta),
						Data:   data,
					}
					buckets[bucketIndex[n]].Events = append(buckets[bucketIndex[n]].Events, event)
				}
				h += delta
			}
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "\t")
	err = enc.Encode(buckets)
	ts.Check(err)
	ts.Check(os.WriteFile(ts.MkAbs(path), buf.Bytes(), 0o640))
}

var amendmentTests = []struct {
	name  string
	event worklog.Event
	want  []worklog.Event
}{
	{
		name: "none",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
			},
		},
		want: []worklog.Event{{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
			},
		}},
	},
	{
		name: "before",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{{
						Start: time.Date(2024, time.January, 1, 7, 15, 0, 0, time.UTC),
						End:   time.Date(2024, time.January, 1, 8, 15, 0, 0, time.UTC),
						Data: map[string]any{
							"key": "new_value",
						},
					}},
				}},
			},
		},
		want: []worklog.Event{{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
			},
		}},
	},
	{
		name: "after",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{{
						Start: time.Date(2024, time.January, 1, 11, 15, 0, 0, time.UTC),
						End:   time.Date(2024, time.January, 1, 12, 15, 0, 0, time.UTC),
						Data: map[string]any{
							"key": "new_value",
						},
					}},
				}},
			},
		},
		want: []worklog.Event{{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
			},
		}},
	},
	{
		name: "cover",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{{
						Start: time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
						End:   time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
						Data: map[string]any{
							"key": "new_value",
						},
					}},
				}},
			},
		},
		want: []worklog.Event{{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "new_value",
			},
		}},
	},
	{
		name: "left",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{{
						Start: time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
						End:   time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
						Data: map[string]any{
							"key": "new_value",
						},
					}},
				}},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value",
				},
			},
		},
	},
	{
		name: "right",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{{
						Start: time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
						End:   time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
						Data: map[string]any{
							"key": "new_value",
						},
					}},
				}},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "middle",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{{
						Start: time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
						End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
						Data: map[string]any{
							"key": "new_value",
						},
					}},
				}},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "outer",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{{
					Bucket:  "test",
					Message: "test",
					Replace: []worklog.Replacement{
						{
							Start: time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value",
							},
						},
						{
							Start: time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value",
							},
						},
					},
				}},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value",
				},
			},
		},
	},
	{
		name: "two_layer_middle_then_left",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_middle",
							},
						}},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_left",
							},
						}},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_middle",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_left",
				},
			},
		},
	},
	{
		name: "two_layer_right_then_before",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_right",
							},
						}},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_left",
							},
						}},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_right",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_left",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "two_layer_right_then_before_overlapping",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_right",
							},
						}},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 9, 50, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_left",
							},
						}},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 50, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_right",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 50, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_left",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "two_layer_left_then_after",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_left",
							},
						}},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_right",
							},
						}},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_right",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_left",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "two_layer_left_then_before_and_after",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{
							{
								Start: time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
								End:   time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
								Data: map[string]any{
									"key": "new_value_middle",
								},
							},
						},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{
							{
								Start: time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
								End:   time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
								Data: map[string]any{
									"key": "new_value_left",
								},
							},
							{
								Start: time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
								End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
								Data: map[string]any{
									"key": "new_value_right",
								},
							},
						},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_right",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_middle",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_left",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "two_layer_left_and_right_then_long_between",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{
							{
								Start: time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
								End:   time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
								Data: map[string]any{
									"key": "new_value_left",
								},
							},
							{
								Start: time.Date(2024, time.January, 1, 9, 45, 0, 0, time.UTC),
								End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
								Data: map[string]any{
									"key": "new_value_right",
								},
							},
						},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{
							{
								Start: time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
								End:   time.Date(2024, time.January, 1, 9, 50, 0, 0, time.UTC),
								Data: map[string]any{
									"key": "new_value_middle",
								},
							},
						},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 50, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_right",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 50, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_middle",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_left",
				},
			},
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 9, 20, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "value",
				},
			},
		},
	},
	{
		name: "two_layer_middle_then_cover",
		event: worklog.Event{
			Bucket: "test",
			Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
			End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
			Data: map[string]any{
				"key": "value",
				"amend": []worklog.Amendment{
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 30, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 0, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_middle",
							},
						}},
					},
					{
						Bucket:  "test",
						Message: "test",
						Replace: []worklog.Replacement{{
							Start: time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
							End:   time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
							Data: map[string]any{
								"key": "new_value_cover",
							},
						}},
					},
				},
			},
		},
		want: []worklog.Event{
			{
				Bucket: "test",
				Start:  time.Date(2024, time.January, 1, 9, 15, 0, 0, time.UTC),
				End:    time.Date(2024, time.January, 1, 10, 15, 0, 0, time.UTC),
				Data: map[string]any{
					"key": "new_value_cover",
				},
			},
		},
	},
}

func TestAmendments(t *testing.T) {
	for _, test := range amendmentTests {
		t.Run(test.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slogext.NewJSONHandler(&buf, &slogext.HandlerOptions{
				Level: slog.LevelDebug - 1,
			}))
			d := daemon{log: log}
			got := d.applyAmendments(context.Background(), test.event)
			if !cmp.Equal(test.want, got) {
				t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(test.want, got))
			}
			t.Logf("log:\n%s", &buf)
		})
	}
}
