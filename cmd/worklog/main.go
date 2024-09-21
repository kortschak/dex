// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The worklog executable is a dex module for logging user activity.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/interpreter"
	"github.com/kortschak/jsonrpc2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
	"github.com/kortschak/dex/cmd/worklog/store"
	"github.com/kortschak/dex/internal/celext"
	"github.com/kortschak/dex/internal/localtime"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/version"
	"github.com/kortschak/dex/internal/xdg"
	"github.com/kortschak/dex/rpc"
)

// Exit status codes.
const (
	success       = 0
	internalError = 1 << (iota - 1)
	invocationError
)

func main() { os.Exit(Main()) }

func Main() int {
	network := flag.String("network", "", "network for communication (unix or tcp)")
	addr := flag.String("addr", "", "address for communication")
	uid := flag.String("uid", "", "unique ID")
	logging := flag.String("log", "info", "logging level (debug, info, warn or error)")
	lines := flag.Bool("lines", false, "display source line details in logs")
	logStdout := flag.Bool("log_stdout", false, "log to stdout instead of stderr")
	v := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *v {
		err := version.Print()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(internalError)
		}
		os.Exit(success)
	}

	switch *network {
	case "unix", "tcp":
	default:
		flag.Usage()
		return invocationError
	}

	switch "" {
	case *addr, *uid:
		flag.Usage()
		return invocationError
	default:
	}

	var level slog.LevelVar
	err := level.UnmarshalText([]byte(*logging))
	if err != nil {
		flag.Usage()
		return invocationError
	}
	addSource := slogext.NewAtomicBool(*lines)
	logDst := os.Stderr
	if *logStdout {
		logDst = os.Stdout
	}
	log := slog.New(slogext.GoID{Handler: slogext.NewJSONHandler(logDst, &slogext.HandlerOptions{
		Level:     &level,
		AddSource: addSource,
	})}).With(
		slog.String("component", *uid),
	)

	ctx, cancel := context.WithCancel(context.Background())
	waitParent(func() {
		log.LogAttrs(ctx, slog.LevelError, "dex died")
		cancel()
	})

	h := newDaemon(*uid, log, &level, addSource, ctx, cancel)
	err = h.dial(ctx, *network, *addr, net.Dialer{})
	if err != nil {
		log.LogAttrs(ctx, slog.LevelError, err.Error())
		return internalError
	}
	defer h.close()

	log.LogAttrs(ctx, slog.LevelInfo, "start")
	<-ctx.Done()
	log.LogAttrs(ctx, slog.LevelInfo, "exit")

	return success
}

func waitParent(fn func()) {
	go func() {
		os.Stdin.Read([]byte{0})
		fn()
	}()
}

func newDaemon(uid string, log *slog.Logger, level *slog.LevelVar, addSource *atomic.Bool, ctx context.Context, cancel context.CancelFunc) *daemon {
	d := &daemon{
		uid:       uid,
		log:       log,
		level:     level,
		addSource: addSource,
		ctx:       ctx,
		cancel:    cancel,

		lastReport: make(map[rpc.UID]worklog.Report),
	}
	d.timezone.Store(localtime.Static{})
	return d
}

func (d *daemon) dial(ctx context.Context, network, addr string, dialer net.Dialer) error {
	d.log.LogAttrs(ctx, slog.LevelDebug, "dial", slog.String("network", network), slog.String("addr", addr))
	var err error
	d.conn, err = jsonrpc2.Dial(ctx, jsonrpc2.NetDialer(network, addr, dialer), d)
	if err != nil {
		return err
	}
	return nil
}

func (d *daemon) close() error {
	d.conn.Notify(context.Background(), rpc.Unregister, rpc.NewMessage(rpc.UID{Module: d.uid}, rpc.None{}))
	return d.conn.Close()
}

type daemon struct {
	uid string

	// conn is the connection to the kernel.
	conn *jsonrpc2.Connection

	log       *slog.Logger
	level     *slog.LevelVar
	addSource *atomic.Bool
	ctx       context.Context // Global cancellation context.
	cancel    context.CancelFunc

	rules atomicValue[map[string]ruleDetail]

	rMu        sync.Mutex
	lastEvents map[string]*worklog.Event
	db         atomic.Pointer[store.DB]

	lastReport map[rpc.UID]worklog.Report

	tMu      sync.Mutex // tMu is only used for protecting configuration of timezone.
	timezone atomicIfaceValue[current]

	serverAddr     string
	htmlSrc        string
	canModify      bool
	dashboardRules atomicValue[map[string]map[string]ruleDetail]
	serverCancel   context.CancelFunc

	hMu       sync.Mutex
	heartbeat time.Duration
	hStop     chan struct{}
}

type atomicValue[T any] struct {
	val atomic.Value
}

func (v *atomicValue[T]) Store(val T) {
	v.val.Store(val)
}

func (v *atomicValue[T]) Load() T {
	val, _ := v.val.Load().(T) // Zero value is usable.
	return val
}

type atomicIfaceValue[T any] struct {
	val atomic.Pointer[T]
}

func (v *atomicIfaceValue[T]) Store(val T) {
	v.val.Store(&val)
}

func (v *atomicIfaceValue[T]) Load() T {
	return *v.val.Load()
}

type current interface {
	Location() (*time.Location, error)
}

type ruleDetail struct {
	name, typ string
	prg       cel.Program
}

func (d *daemon) Bind(ctx context.Context, conn *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
	d.conn = conn
	d.log.LogAttrs(ctx, slog.LevelDebug, "bind")
	return jsonrpc2.ConnectionOptions{
		Handler: d,
	}
}

func (d *daemon) Handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	d.log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("req", slogext.Request{Request: req}))

	switch req.Method {
	case rpc.Who:
		version, err := version.String()
		if err != nil {
			version = err.Error()
		}
		return rpc.NewMessage(rpc.UID{Module: d.uid}, version), nil

	case "record":
		var m rpc.Message[worklog.Report]
		err := rpc.UnmarshalMessage(req.Params, &m)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "record", slog.Any("error", err))
			return nil, err
		}
		m.Body.Time = m.Time
		d.rMu.Lock()
		d.record(ctx, m.UID, m.Body, d.lastReport[m.UID])
		d.lastReport[m.UID] = m.Body
		d.rMu.Unlock()
		return nil, nil

	case rpc.Configure:
		var m rpc.Message[worklog.Config]
		err := rpc.UnmarshalMessage(req.Params, &m)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
			return nil, err
		}

		if m.Body.LogLevel != nil {
			d.level.Set(*m.Body.LogLevel)
		}
		if m.Body.AddSource != nil {
			d.addSource.Store(*m.Body.AddSource)
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "configure", slog.Any("details", m))

		d.replaceTimezone(ctx, m.Body.Options.DynamicLocation)

		if m.Body.Options.Heartbeat != nil {
			d.beat(ctx, m.Body.Options.Heartbeat.Duration)
		}

		if m.Body.Options.Web == nil {
			if d.serverCancel != nil {
				d.log.LogAttrs(ctx, slog.LevelDebug, "stop web server")
				d.serverCancel()
				d.serverCancel = nil
			}
		} else {
			if m.Body.Options.Web.Addr != d.serverAddr || m.Body.Options.Web.HTML != d.htmlSrc || m.Body.Options.Web.AllowModification != d.canModify {
				if d.serverCancel != nil {
					d.serverCancel()
				}
				d.htmlSrc = ""
				d.canModify = false
				d.log.LogAttrs(ctx, slog.LevelDebug, "configure web server")
				d.serverAddr, d.serverCancel, err = d.serve(m.Body.Options.Web.Addr, m.Body.Options.Web.HTML, m.Body.Options.Web.AllowModification)
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelError, "configure web server", slog.Any("error", err))
				} else {
					d.htmlSrc = m.Body.Options.Web.HTML
					d.canModify = m.Body.Options.Web.AllowModification
				}
			}
			if m.Body.Options.Web.Rules != nil {
				d.configureWebRule(ctx, m.Body.Options.Web.Rules)
			}
		}

		databaseDir, err := dbDir(m.Body)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure database", slog.Any("error", err))
			return nil, rpc.NewError(rpc.ErrCodeInvalidMessage,
				err.Error(),
				map[string]any{
					"type":         rpc.ErrCodeParameters,
					"database":     m.Body.Options.Database,
					"database_dir": m.Body.Options.DatabaseDir,
				},
			)
		}
		if databaseDir != "" {
			dir, err := xdg.State(databaseDir)
			switch err {
			case nil:
			case syscall.ENOENT:
				var ok bool
				dir, ok = xdg.StateHome()
				if !ok {
					d.log.LogAttrs(ctx, slog.LevelError, "configure database", slog.String("error", "no XDG_STATE_HOME"))
					return nil, err
				}
				dir = filepath.Join(dir, databaseDir)
				err = os.Mkdir(dir, 0o750)
				if err != nil {
					err := err.(*os.PathError) // See godoc for os.Mkdir for why this is safe.
					d.log.LogAttrs(ctx, slog.LevelError, "create database dir", slog.Any("error", err))
					return nil, rpc.NewError(rpc.ErrCodeInternal,
						err.Error(),
						map[string]any{
							"type": rpc.ErrCodePath,
							"op":   err.Op,
							"path": err.Path,
							"err":  fmt.Sprint(err.Err),
						},
					)
				}
			default:
				d.log.LogAttrs(ctx, slog.LevelError, "configure database", slog.Any("error", err))
				return nil, jsonrpc2.NewError(
					rpc.ErrCodeInternal,
					err.Error(),
				)
			}

			path := filepath.Join(dir, "db.sqlite")
			if db := d.db.Load(); db == nil || path != db.Name() {
				err = d.openDB(ctx, db, path, m.Body.Options.Hostname)
				if err != nil {
					return nil, rpc.NewError(rpc.ErrCodeInternal,
						err.Error(),
						map[string]any{
							"type": rpc.ErrCodeStoreErr,
							"op":   "open",
							"path": path,
						},
					)
				}
			}

			if m.Body.Options.Rules != nil {
				d.configureRules(ctx, m.Body.Options.Rules)
			}

			if db := d.db.Load(); db != nil {
				d.configureDB(ctx, db)
			}
		}

		if !req.ID.IsValid() {
			return nil, nil
		}
		return rpc.NewMessage(m.UID, "done"), nil

	case rpc.Stop:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop")
		d.cancel()
		return nil, nil

	default:
		return nil, jsonrpc2.ErrNotHandled
	}
}

func dbDir(cfg worklog.Config) (string, error) {
	opt := cfg.Options
	if opt.Database == "" {
		return opt.DatabaseDir, nil
	}
	u, err := url.Parse(opt.Database)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "":
		return "", errors.New("missing scheme in database configuration")
	case "sqlite":
		if opt.DatabaseDir != "" && u.Opaque != opt.DatabaseDir {
			return "", fmt.Errorf("inconsistent database directory configuration: (%s:)%s != %s", u.Scheme, u.Opaque, opt.DatabaseDir)
		}
		if u.Opaque == "" {
			return "", fmt.Errorf("sqlite configuration missing opaque data: %s", opt.Database)
		}
		return u.Opaque, nil
	default:
		if opt.DatabaseDir != "" {
			return "", fmt.Errorf("inconsistent database configuration: both %s database and sqlite directory configured", u.Scheme)
		}
		return "", nil
	}
}

func (d *daemon) replaceTimezone(ctx context.Context, dynamic *bool) {
	if dynamic == nil {
		return
	}

	d.tMu.Lock()
	defer d.tMu.Unlock()

	if is[*localtime.Dynamic](d.timezone.Load()) == *dynamic {
		return
	}

	var (
		tz  current
		err error
	)
	if *dynamic {
		tz, err = localtime.NewDynamic()
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
			return
		}
	} else {
		tz = localtime.Static{}
	}
	err = closeCloser(d.timezone.Load())
	if err != nil {
		d.log.LogAttrs(ctx, slog.LevelWarn, "configure", slog.Any("error", err))
	}

	var strategy string
	switch tz.(type) {
	case localtime.Static:
		strategy = "static"
	case *localtime.Dynamic:
		strategy = "dynamic"
	default:
		strategy = "unknown"
		d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.String("error", "unknown timezone strategy"))
	}
	d.log.LogAttrs(ctx, slog.LevelInfo, "configure", slog.String("timezone", strategy))
	d.timezone.Store(tz)
}

func is[T any](v any) bool {
	_, ok := v.(T)
	return ok
}

func closeCloser(x any) error {
	c, ok := x.(io.Closer)
	if !ok {
		return nil
	}
	return c.Close()
}

func (d *daemon) configureWebRule(ctx context.Context, rules map[string]map[string]worklog.WebRule) {
	opts := []cel.EnvOption{
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		celext.Lib(d.log),
		cel.Declarations(
			decls.NewVar("bucket", decls.String),
			decls.NewVar("data", decls.NewMapType(decls.String, decls.Dyn)),
		),
	}
	ruleDetails := make(map[string]map[string]ruleDetail)
	for srcBucket, ruleSet := range rules {
		ruleDetails[srcBucket] = make(map[string]ruleDetail)
		for dstBucket, rule := range ruleSet {
			prg, err := compile(rule.Src, opts)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "compiling rule", slog.String("src_bucket", srcBucket), slog.String("dst_bucket", dstBucket), slog.Any("error", err))
			} else {
				ruleDetails[srcBucket][dstBucket] = ruleDetail{name: rule.Name, prg: prg}
			}
		}
	}
	d.dashboardRules.Store(ruleDetails)
}

func (d *daemon) configureRules(ctx context.Context, rules map[string]worklog.Rule) {
	opts := []cel.EnvOption{
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		celext.Lib(d.log),
		celext.StateLib(ctx, rpc.UID{Module: d.uid}, d.conn, d.log),
		cel.Declarations(
			decls.NewVar("bucket", decls.String),
			decls.NewVar("data_src", decls.NewMapType(decls.String, decls.String)),
			decls.NewVar("period", decls.Duration),
			decls.NewVar("curr", decls.NewMapType(decls.String, decls.Dyn)),
			decls.NewVar("last", decls.NewMapType(decls.String, decls.Dyn)),
			decls.NewVar("last_event", decls.NewMapType(decls.String, decls.Dyn)),
		),
	}
	ruleDetails := make(map[string]ruleDetail)
	for bucket, rule := range rules {
		prg, err := compile(rule.Src, opts)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "compiling rule", slog.String("bucket", bucket), slog.Any("error", err))
		} else {
			ruleDetails[bucket] = ruleDetail{name: rule.Name, typ: rule.Type, prg: prg}
		}
	}
	d.rules.Store(ruleDetails)
}

func (d *daemon) openDB(ctx context.Context, db *store.DB, path, hostname string) error {
	if db != nil {
		d.log.LogAttrs(ctx, slog.LevelInfo, "close database", slog.String("path", db.Name()))
		d.db.Store((*store.DB)(nil))
		db.Close()
	}
	// store.Open may need to get the hostname, which may
	// wait indefinitely due to network unavailability.
	// So make a timeout and allow the fallback to the
	// kernel-provided hostname. This fallback is
	// implemented by store.Open.
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	db, err := store.Open(ctx, path, hostname)
	if err != nil {
		d.log.LogAttrs(ctx, slog.LevelError, "open database", slog.Any("error", err))
		return err
	}
	d.db.Store(db)
	d.log.LogAttrs(ctx, slog.LevelInfo, "open database", slog.String("path", path))
	return nil
}

func (d *daemon) configureDB(ctx context.Context, db *store.DB) {
	rules := d.rules.Load()
	for bucket, rule := range rules {
		d.log.LogAttrs(ctx, slog.LevelDebug, "create bucket", slog.Any("bucket", bucket))
		m, err := db.CreateBucket(bucket, rule.name, rule.typ, d.uid, time.Now(), nil)
		var sqlErr *sqlite.Error
		switch {
		case err == nil:
			d.log.LogAttrs(ctx, slog.LevelInfo, "create bucket", slog.Any("metadata", m))
		case errors.As(err, &sqlErr):
			if sqlErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE {
				d.log.LogAttrs(ctx, slog.LevelInfo, "bucket exists", slog.Any("bucket", bucket), slog.Any("metadata", m), slog.Any("error", err))
				break
			}
			fallthrough
		default:
			d.log.LogAttrs(ctx, slog.LevelError, "failed to create bucket", slog.Any("error", err), slog.Any("metadata", m))
		}
	}
}

func (d *daemon) record(ctx context.Context, src rpc.UID, curr, last worklog.Report) {
	d.log.LogAttrs(ctx, slog.LevelDebug, "record", slog.Any("report", curr))
	rules := d.rules.Load()
	if rules == nil {
		return
	}
	if d.lastEvents == nil {
		d.lastEvents = make(map[string]*worklog.Event)
	}

	act := map[string]any{
		"data_src": asMap(src),
		"period":   curr.Period.Duration,
		"curr":     curr.Map(),
		"last":     last.Map(),
	}
	for bucket, rule := range rules {
		act["bucket"] = bucket

		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "no database", slog.Any("act", act))
			continue
		}

		lastEvent, ok := d.lastEvents[bucket]
		if ok {
			d.log.LogAttrs(ctx, slog.LevelDebug, "last event in cache", slog.String("bucket", bucket), slog.Any("last", lastEvent))
		} else {
			var err error
			lastEvent, err = db.LastEvent(bucket)
			if err == nil {
				d.log.LogAttrs(ctx, slog.LevelDebug, "last event from store", slog.String("bucket", bucket), slog.Any("last", lastEvent))
			} else {
				d.log.LogAttrs(ctx, slog.LevelWarn, "no last event", slog.String("bucket", bucket), slog.Any("error", err))
				lastEvent = &worklog.Event{
					Bucket:   bucket,
					Start:    curr.Time,
					End:      curr.Time,
					Continue: new(bool),
				}
			}
		}
		act["last_event"] = map[string]any{
			"bucket":   lastEvent.Bucket,
			"id":       lastEvent.ID,
			"start":    lastEvent.Start,
			"end":      lastEvent.End,
			"data":     lastEvent.Data,
			"continue": lastEvent.Continue,
		}

		note, err := eval[worklog.Event](rule.prg, act)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "activity evaluation", slog.Any("bucket", bucket), slog.Any("error", err), slog.Any("act", act))
			continue
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "note evaluation", slog.String("bucket", bucket), slog.Any("act", act), slog.Any("note", note))
		if note.Bucket != bucket {
			continue
		}
		// Stored as pointer, so ID updates below are retained.
		d.lastEvents[bucket] = note

		// Massage times if the CEL did not handle them correctly.
		if note.End.IsZero() {
			d.log.LogAttrs(ctx, slog.LevelWarn, "zero end time", slog.Any("note", note))
			note.End = curr.Time
		}
		if note.Start.IsZero() {
			d.log.LogAttrs(ctx, slog.LevelWarn, "zero start time", slog.Any("note", note))
			note.Start = note.End
		}

		var isNew bool
		if lastEvent.ID != 0 && note.Continue != nil && *note.Continue {
			note.ID = lastEvent.ID
			_, err = db.UpdateEvent(note)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "failed update event", slog.Any("error", err), slog.Any("note", note))
				continue
			}
		} else {
			res, err := db.InsertEvent(note)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "failed insert event", slog.Any("error", err), slog.Any("note", note))
				continue
			}
			id, err := res.LastInsertId()
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "no last insert ID", slog.Any("error", err), slog.Any("note", note))
				continue
			}
			note.ID = id
			isNew = true
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "note", slog.Bool("new", isNew), slog.String("bucket", bucket), slog.Any("act", act), slog.Any("note", note))
	}
	for bucket := range d.lastEvents {
		if _, ok := rules[bucket]; !ok {
			delete(d.lastEvents, bucket)
		}
	}
}

func asMap(uid rpc.UID) map[string]string {
	m := make(map[string]string)
	if uid.Module != "" {
		m["module"] = uid.Module
	}
	if uid.Service != "" {
		m["service"] = uid.Service
	}
	return m
}

func compile(src string, opts []cel.EnvOption) (cel.Program, error) {
	env, err := cel.NewEnv(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create env: %v", err)
	}

	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return nil, fmt.Errorf("failed compilation: %v", iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return nil, fmt.Errorf("failed program instantiation: %v", err)
	}
	return prg, nil
}

func eval[T any](prg cel.Program, input any) (*T, error) {
	if input == nil {
		input = interpreter.EmptyActivation()
	}
	out, _, err := prg.Eval(input)
	if err != nil {
		return nil, fmt.Errorf("failed eval: %v", err)
	}

	v, err := out.ConvertToNative(reflect.TypeOf((*structpb.Value)(nil)))
	if err != nil {
		return nil, fmt.Errorf("failed proto conversion: %v", err)
	}
	b, err := protojson.MarshalOptions{}.Marshal(v.(proto.Message))
	if err != nil {
		return nil, fmt.Errorf("failed native conversion: %v", err)
	}
	var note T
	err = json.Unmarshal(b, &note)
	if err != nil {
		return nil, fmt.Errorf("failed json conversion: %v", err)
	}
	return &note, nil
}

func (d *daemon) beat(ctx context.Context, p time.Duration) {
	select {
	case <-ctx.Done():
		return
	default:
	}

	d.hMu.Lock()
	defer d.hMu.Unlock()

	if p == d.heartbeat {
		return
	}

	if d.hStop != nil {
		close(d.hStop)
		d.hStop = nil
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	switch {
	case p == 0:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop heartbeat")
		err := d.conn.Notify(ctx, "heartbeat",
			rpc.NewMessage(rpc.UID{Module: d.uid}, rpc.Deadline{}),
		)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "heartbeat", slog.Any("error", err))
		}
		d.heartbeat = p

	case p > 0:
		d.log.LogAttrs(ctx, slog.LevelInfo, "update heartbeat", slog.Duration("old", d.heartbeat), slog.Duration("new", p))
		stop := make(chan struct{})
		d.hStop = stop
		go func() {
			ticker := time.NewTicker(p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			for {
				select {
				case <-ctx.Done():
					ticker.Stop()
					return
				case <-stop:
					ticker.Stop()
					return
				case t := <-ticker.C:
					d.log.LogAttrs(ctx, slog.LevelDebug, "heartbeat", slog.Any("tick", t))
					deadline := t.Add(2 * p)
					err := d.conn.Notify(ctx, "heartbeat",
						rpc.Message[rpc.Deadline]{
							Time: t, UID: rpc.UID{Module: d.uid},
							Body: rpc.Deadline{Deadline: &deadline},
						},
					)
					if err != nil {
						d.log.LogAttrs(ctx, slog.LevelError, "heartbeat", slog.Any("error", err))
					}
				}
			}
		}()
		d.heartbeat = p

	default:
		d.log.LogAttrs(ctx, slog.LevelError, "update heartbeat", slog.Duration("invalid duration", p))
	}
}

//go:embed ui
var ui embed.FS

func (d *daemon) serve(addr, path string, canModify bool) (string, context.CancelFunc, error) {
	ctx := d.ctx

	mux := http.NewServeMux()
	ui, err := fs.Sub(ui, "ui")
	if err != nil {
		return "", nil, err
	}
	if path != "" {
		ui = os.DirFS(path)
	}
	mux.Handle("/", http.FileServer(http.FS(ui)))
	mux.HandleFunc("/dump/", d.dump(ctx))
	mux.HandleFunc("/data/", d.dashboardData(ctx))
	mux.HandleFunc("/summary/", d.summaryData(ctx))
	isLocalAddr, err := isLoopback(ctx, addr)
	if err != nil {
		return "", nil, err
	}
	if isLocalAddr {
		mux.HandleFunc("/query", d.query(ctx))
		mux.HandleFunc("/query/", d.query(ctx))
		mux.HandleFunc("/backup", d.backup(ctx))
		mux.HandleFunc("/backup/", d.backup(ctx))
	}
	if canModify && isLocalAddr {
		mux.HandleFunc("/amend/", d.amend(ctx))
		mux.HandleFunc("/load/", d.load(ctx))
	}
	srv := &http.Server{
		Addr:     addr,
		Handler:  mux,
		ErrorLog: slog.NewLogLogger(d.log.Handler(), slog.LevelError),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	addr = ln.Addr().String()

	d.log.LogAttrs(ctx, slog.LevelInfo, "web server listening", slog.Any("addr", addr))
	go func() {
		err = srv.Serve(ln)
		var lvl slog.Level
		switch err {
		case nil:
			return
		case http.ErrServerClosed:
			lvl = slog.LevelInfo
		default:
			lvl = slog.LevelError
		}
		d.log.LogAttrs(ctx, lvl, "web server closed", slog.Any("error", err))
	}()
	cancel := func() { srv.Shutdown(ctx) }

	return addr, cancel, nil
}

func isLoopback(ctx context.Context, addr string) (bool, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false, err
	}
	if host == "" {
		return false, nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return false, err
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false, nil
		}
	}
	return true, nil
}

func (d *daemon) amend(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		now := time.Now()
		var buf bytes.Buffer
		_, err := io.Copy(&buf, req.Body)
		req.Body.Close()
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch typ := req.Header.Get("content-type"); typ {
		case "", "application/json":
		default:
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("content-type", typ), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var note worklog.Amendment
		err = json.Unmarshal(buf.Bytes(), &note)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if len(note.Replace) == 0 {
			http.ServeContent(w, req, "amended.json", time.Now(), strings.NewReader("[]"))
			return
		}
		_, err = db.AmendEvents(now, &note)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var amended []worklog.Event
		for _, r := range mergeReplacement(note.Replace) {
			e, err := db.EventsRange(db.BucketID(note.Bucket), r.start, r.end, -1)
			amended = append(amended, e...)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			}
		}
		b, err := json.Marshal(amended)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, req, "amended.json", time.Now(), bytes.NewReader(b))
	}
}

type timeRange struct {
	start, end time.Time
}

func mergeReplacement(replace []worklog.Replacement) []timeRange {
	switch len(replace) {
	case 0:
		return nil
	case 1:
		return []timeRange{{start: replace[0].Start, end: replace[0].End}}
	}
	sort.Slice(replace, func(i, j int) bool {
		switch {
		case replace[i].Start.Before(replace[j].Start):
			return true
		case replace[i].Start.After(replace[j].Start):
			return false
		default:
			// Choose longer intervals first.
			return replace[i].End.After(replace[j].End)
		}
	})
	merged := []timeRange{{start: replace[0].Start, end: replace[0].End}}
	last := &merged[0]
	for _, r := range replace[1:] {
		if !r.End.After(last.end) {
			// Not an extension or a new interval.
			continue
		}
		if !r.Start.After(last.end) {
			// Not a new interval.
			last.end = r.End
			continue
		}
		merged = append(merged, timeRange{r.Start, r.End})
		last = &merged[len(merged)-1]
	}
	return merged
}

func (d *daemon) dump(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		u, err := url.Parse(req.RequestURI)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var dump []worklog.BucketMetadata
		if s, e := u.Query().Has("start"), u.Query().Has("end"); s || e {
			var start, end time.Time
			if s {
				start, err = time.Parse(time.RFC3339Nano, u.Query().Get("start"))
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			if e {
				end, err = time.Parse(time.RFC3339Nano, u.Query().Get("end"))
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
					w.WriteHeader(http.StatusBadRequest)
					return
				}
			}
			dump, err = db.DumpRange(start, end)
		} else {
			dump, err = db.Dump()
		}
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, err := json.Marshal(struct {
			Buckets []worklog.BucketMetadata `json:"buckets"`
		}{dump})
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, req, "dump.json", time.Now(), bytes.NewReader(b))
	}
}

func (d *daemon) backup(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var n int
		if req.URL.Query().Has("pages_per_step") {
			var err error
			n, err = strconv.Atoi(req.URL.Query().Get("pages_per_step"))
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"error":%q}`, err)
				return
			}
		}
		var sleep time.Duration
		if req.URL.Query().Has("sleep") {
			var err error
			n, err = strconv.Atoi(req.URL.Query().Get("sleep"))
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
				w.WriteHeader(http.StatusBadRequest)
				fmt.Fprintf(w, `{"error":%q}`, err)
				return
			}
		}

		path, err := db.Backup(ctx, n, sleep)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		now := time.Now()
		b, err := json.Marshal(struct {
			Path string    `json:"path"`
			Time time.Time `json:"time"`
		}{path, now})
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, req, "backup.json", now, bytes.NewReader(b))
	}
}

func (d *daemon) load(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodPost {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var replace bool
		r := req.URL.Query().Get("replace")
		if r != "" {
			var err error
			replace, err = strconv.ParseBool(r)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
		}
		var buf bytes.Buffer
		_, err := io.Copy(&buf, req.Body)
		req.Body.Close()
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch typ := req.Header.Get("content-type"); typ {
		case "", "application/json":
		default:
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("content-type", typ), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		var dump struct {
			Buckets []worklog.BucketMetadata `json:"buckets"`
		}
		err = json.Unmarshal(buf.Bytes(), &dump)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		err = db.Load(dump.Buckets, replace)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

func (d *daemon) query(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			io.Copy(io.Discard, req.Body)
			req.Body.Close()
		}()

		switch req.Method {
		case http.MethodGet, http.MethodPost:
		default:
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("content-type", "application/json")

		var body bytes.Buffer
		io.Copy(&body, req.Body)
		switch req.Header.Get("content-type") {
		case "":
			if req.Method != http.MethodGet {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body.Reset()
			body.WriteString(req.URL.Query().Get("sql"))
			fallthrough
		case "application/sql":
			resp, err := db.Select(body.String())
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]any{"err": err.Error()})
				return
			}
			for _, row := range resp {
				b, ok := row["datastr"].([]byte)
				if ok && len(b) != 0 {
					var d any
					err := json.Unmarshal(b, &d)
					if err != nil {
						w.WriteHeader(http.StatusInternalServerError)
						fmt.Fprintln(w, err)
						return
					}
					row["datastr"] = d
				}
			}
			json.NewEncoder(w).Encode(resp)
		case "application/cel":
			prg, err := compile(body.String(), []cel.EnvOption{
				cel.OptionalTypes(cel.OptionalTypesVersion(1)),
				celext.Lib(d.log),
				cel.Lib(dbLib{db: db, log: d.log}),
			})
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"err": err.Error()})
				return
			}
			resp, err := eval[any](prg, nil)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]any{"err": err.Error()})
				return
			}
			json.NewEncoder(w).Encode(resp)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}
}

func queryError(dec *json.Decoder, body []byte, err error) any {
	offset := int(dec.InputOffset())
	var syntax *json.SyntaxError
	switch {
	case errors.As(err, &syntax):
		offset = int(syntax.Offset)
	case errors.Is(err, io.ErrUnexpectedEOF):
		offset = len(body)
	}
	off := offset
	var line []byte
	for _, l := range bytes.Split(body, []byte{'\n'}) {
		if off-(len(l)+1) <= 0 {
			line = l
			break
		}
		off -= len(l) + 1
	}
	return map[string]any{
		"err":    err.Error(),
		"query":  string(body),
		"offset": offset,
		"detail": map[string]string{
			"line": string(line),
			"mark": strings.Repeat(" ", off) + "^",
		},
	}
}

type dbLib struct {
	db  *store.DB
	log *slog.Logger
}

func (dbLib) ProgramOptions() []cel.ProgramOption { return nil }

var mapStringDyn = cel.MapType(cel.StringType, cel.DynType)

func (l dbLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("query",
			cel.Overload(
				"query_string",
				[]*cel.Type{cel.StringType, mapStringDyn},
				cel.DynType,
				cel.UnaryBinding(l.query),
			),
		),
	}
}

func (l dbLib) query(arg ref.Val) ref.Val {
	sql, ok := arg.(types.String)
	if !ok {
		return types.ValOrErr(sql, "no such overload")
	}
	resp, err := l.db.Select(string(sql))
	if err != nil {
		return types.NewErr(err.Error())
	}
	for _, row := range resp {
		b, ok := row["datastr"].([]byte)
		if ok && len(b) != 0 {
			var d any
			err := json.Unmarshal(b, &d)
			if err != nil {
				return types.NewErr(err.Error())
			}
			row["datastr"] = d
		}
	}
	return types.NewDynamicList(types.DefaultTypeAdapter, resp)
}
