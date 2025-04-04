// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// The rest executable is a dex module for forwarding REST API calls to
// dex modules over JSON RPC-2.0.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/kortschak/jsonrpc2"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	rest "github.com/kortschak/dex/cmd/rest/api"
	"github.com/kortschak/dex/config"
	"github.com/kortschak/dex/internal/celext"
	"github.com/kortschak/dex/internal/mtls"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/version"
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
	return &daemon{
		uid:       uid,
		log:       log,
		level:     level,
		addSource: addSource,
		ctx:       ctx,
		cancel:    cancel,
	}
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
	servers := d.servers.Load()
	for _, srv := range servers {
		if srv.serverCancel != nil {
			srv.serverCancel()
		}
	}
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

	servers atomicValue[map[rpc.UID]serverDetail]

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

type serverDetail struct {
	name string

	// euid is the UID reported to the kernel.
	euid            rpc.UID
	server          rest.Server
	serverCancel    context.CancelFunc
	reqPrg, respPrg cel.Program
}

func (d *daemon) Bind(ctx context.Context, conn *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
	d.conn = conn
	d.log.LogAttrs(ctx, slog.LevelDebug, "bind")
	return jsonrpc2.ConnectionOptions{
		Handler: d,
	}
}

func (d *daemon) Handle(ctx context.Context, req *jsonrpc2.Request) (any, error) {
	d.log.LogAttrs(ctx, slog.LevelDebug, "handle", slog.Any("req", slogext.RequestRedactPrivate{Request: req}))

	uid := rpc.UID{Module: d.uid}

	switch req.Method {
	case rpc.Who:
		version, err := version.String()
		if err != nil {
			version = err.Error()
		}
		return rpc.NewMessage(rpc.UID{Module: d.uid}, version), nil

	case rpc.Configure:
		isService, err := config.IsService(req)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
			return nil, err
		}
		if isService {
			var m rpc.Message[rest.Service]
			err = rpc.UnmarshalMessage(req.Params, &m)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "configure", slog.Any("error", err))
				return nil, err
			}
			uid.Service = m.Body.Name
			if m.Body.Active == nil {
				// Nothing to do.
				if !req.ID.IsValid() {
					return nil, nil
				}
				return rpc.NewMessage(uid, "done"), nil
			}
			active := *m.Body.Active

			servers := d.servers.Load()
			newServers := maps.Clone(servers)
			curr := servers[uid]
			if !active {
				if curr.serverCancel != nil {
					curr.serverCancel()
				}
				delete(newServers, uid)
			} else {
				server := m.Body.Options.Server
				curr.name = uid.String()
				curr.euid = uid
				iuid := uid

				curr = d.mkServer(ctx, server, iuid, curr)
				if curr.server.Addr != "" {
					newServers[iuid] = curr
				}
			}
			d.servers.Store(newServers)
		} else {
			var m rpc.Message[rest.Config]
			err = rpc.UnmarshalMessage(req.Params, &m)
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
			d.log.LogAttrs(ctx, slog.LevelDebug, "configure", slog.Any("details", slogext.PrivateRedact{Val: m, Tag: "json"}))

			if m.Body.Options.Heartbeat != nil {
				d.beat(ctx, m.Body.Options.Heartbeat.Duration)
			}

			servers := d.servers.Load()
			newServers := make(map[rpc.UID]serverDetail)
			for uid, srv := range servers {
				_, ok := m.Body.Options.Servers[uid.Service]
				if !ok && srv.serverCancel != nil {
					srv.serverCancel()
				}
			}
			for name, server := range m.Body.Options.Servers {
				euid := uid
				// iuid here is not valid for external use as it
				// has no Module set and so would be interpreted
				// as a kernel service. We are using it here as a
				// way to distinguish service-level servers from
				// module-level servers.
				iuid := rpc.UID{Service: name}
				curr := servers[iuid]
				curr.name = name
				curr.euid = euid

				curr = d.mkServer(ctx, server, iuid, curr)
				if curr.server.Addr != "" {
					newServers[iuid] = curr
				}
			}
			d.servers.Store(newServers)
		}

		if !req.ID.IsValid() {
			return nil, nil
		}
		return rpc.NewMessage(uid, "done"), nil

	case rpc.Stop:
		d.log.LogAttrs(ctx, slog.LevelInfo, "stop")
		servers := d.servers.Load()
		for _, srv := range servers {
			if srv.serverCancel != nil {
				srv.serverCancel()
			}
		}
		d.cancel()
		return nil, nil

	default:
		return nil, jsonrpc2.ErrNotHandled
	}
}

// mkServer makes and starts a new REST server based on the provided config and
// identified internally with the iuid. curr is the currently running server
// with the same internal uid, or a skeleton server detail if no server is
// currently running.
func (d *daemon) mkServer(ctx context.Context, cfg rest.Server, iuid rpc.UID, curr serverDetail) serverDetail {
	if cfg.Request != curr.server.Request {
		decls := cel.Declarations(
			decls.NewVar("time", decls.Timestamp),
			decls.NewVar("request", decls.NewMapType(decls.String, decls.Dyn)),
		)
		reqPrg, err := d.compile(curr.euid, cfg.Request, decls, d.log)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "compiling server rule", slog.String("name", curr.name), slog.Any("error", err))
		} else {
			curr.reqPrg = reqPrg
			curr.server.Request = cfg.Request
		}
	}
	if cfg.Response != curr.server.Response {
		if cfg.Response == "" {
			curr.server.Response = ""
			curr.respPrg = nil
		} else {
			decls := cel.Declarations(
				decls.NewVar("time", decls.Timestamp),
				decls.NewVar("response", decls.NewMapType(decls.String, decls.Dyn)),
			)
			respPrg, err := d.compile(curr.euid, cfg.Response, decls, d.log)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "compiling server rule", slog.String("name", curr.name), slog.Any("error", err))
			} else {
				curr.respPrg = respPrg
				curr.server.Response = cfg.Response
			}
		}
	}
	if cfg.Addr != curr.server.Addr || !sameTLS(cfg, curr.server) {
		if curr.serverCancel != nil {
			curr.serverCancel()
		}
		err := checkServerSecurityPolicy(ctx, cfg)
		switch err := err.(type) {
		case nil:
		case *addrError:
			if err.Err == errInsecureAllowed {
				d.log.LogAttrs(ctx, slog.LevelWarn, "configure web server tls", slog.Any("error", err))
			} else {
				d.log.LogAttrs(ctx, slog.LevelError, "refusing to start insecure server", slog.Any("error", err))
				return curr
			}
		default:
			d.log.LogAttrs(ctx, slog.LevelError, "configure web server tls", slog.Any("error", err))
			return curr
		}
		tlsConfig, err := mkTLSConfig(cfg)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure web server tls", slog.Any("error", err))
			return curr
		}
		curr.server.Addr, curr.serverCancel, err = d.serve(cfg.Addr, iuid, tlsConfig)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "configure web server", slog.Any("error", err))
		}
	}
	return curr
}

func sameTLS(a, b rest.Server) bool {
	return samePtrString(a.RootCA, b.RootCA) &&
		samePtrString(a.CertPEMBlock, b.CertPEMBlock) &&
		samePtrString(a.KeyPEMBlock, b.KeyPEMBlock)
}

func samePtrString(a, b *string) bool {
	switch {
	case a == b:
		return true
	case a == nil && b != nil, a != nil && b == nil:
		return false
	default:
		return *a == *b
	}
}

var (
	errInsecureAllowed = errors.New("insecure server allowed")
	errInsecure        = errors.New("must use mTLS connection on non-loopback device")
)

func checkServerSecurityPolicy(ctx context.Context, cfg rest.Server) error {
	if cfg.RootCA != nil {
		return nil
	}
	secErr := errInsecure
	if cfg.Insecure {
		secErr = errInsecureAllowed
	}

	host, _, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return err
	}
	if host == "" {
		return &addrError{Err: secErr, Addr: cfg.Addr}
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return &addrError{Err: secErr, Addr: cfg.Addr}
		}
	}
	return nil
}

type addrError struct {
	Err  error
	Addr string
}

func (e *addrError) Error() string {
	if e == nil || e.Err == nil {
		return "<nil>"
	}
	s := e.Err.Error()
	if e.Addr != "" {
		s = "address " + e.Addr + ": " + s
	}
	return s
}

func (e *addrError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func mkTLSConfig(cfg rest.Server) (*tls.Config, error) {
	if cfg.RootCA == nil && cfg.CertPEMBlock == nil && cfg.KeyPEMBlock == nil {
		return nil, nil
	}
	var rootPEM, certPEMBlock, keyPEMBlock []byte
	if cfg.RootCA != nil {
		rootPEM = []byte(*cfg.RootCA)
	}
	if cfg.CertPEMBlock != nil {
		certPEMBlock = []byte(*cfg.CertPEMBlock)
	}
	if cfg.KeyPEMBlock != nil {
		keyPEMBlock = []byte(*cfg.KeyPEMBlock)
	}
	return mtls.NewServerConfig(rootPEM, certPEMBlock, keyPEMBlock)
}

func (d *daemon) compile(uid rpc.UID, src string, decls cel.EnvOption, log *slog.Logger) (cel.Program, error) {
	env, err := cel.NewEnv(
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		celext.Lib(log),
		celext.StateLib(d.ctx, uid, d.conn, log),
		cel.Lib(extLib{}),
		decls,
	)
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

type extLib struct{}

func (extLib) ProgramOptions() []cel.ProgramOption { return nil }

func (l extLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("decode_json",
			cel.Overload(
				"decode_json_bytes",
				[]*cel.Type{cel.BytesType},
				cel.DynType,
				cel.UnaryBinding(l.decode),
			),
			cel.MemberOverload(
				"bytes_decode_json",
				[]*cel.Type{cel.BytesType},
				cel.DynType,
				cel.UnaryBinding(l.decode),
			),
		),
		cel.Function("base64",
			cel.MemberOverload(
				"bytes_base64_string",
				[]*cel.Type{cel.BytesType},
				cel.StringType,
				cel.UnaryBinding(l.base64),
			),
		),
	}
}

func (extLib) decode(arg ref.Val) ref.Val {
	msg, ok := arg.(types.Bytes)
	if !ok {
		return types.ValOrErr(msg, "no such overload")
	}
	var val any
	err := json.Unmarshal(msg, &val)
	if err != nil {
		return types.NewErr("%w", err)
	}
	return types.DefaultTypeAdapter.NativeToValue(val)
}

func (extLib) base64(arg ref.Val) ref.Val {
	src, ok := arg.(types.Bytes)
	if !ok {
		return types.ValOrErr(src, "no such overload")
	}
	return types.String(base64.StdEncoding.EncodeToString(src))
}

// currently only JSON RPC notify is handled
func (d *daemon) serve(addr string, uid rpc.UID, tlsConfig *tls.Config) (string, context.CancelFunc, error) {
	ctx := d.ctx
	srv := &http.Server{
		Addr:      addr,
		TLSConfig: tlsConfig,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			servers := d.servers.Load()
			detail, ok := servers[uid]
			if !ok || detail.reqPrg == nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			name := detail.name
			t := time.Now()
			note, err := evalReq(detail.reqPrg, t, req)
			if err != nil {
				d.log.LogAttrs(ctx, slog.LevelError, "eval error", slog.Any("name", name), slog.Any("error", err))
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			for k, v := range note.Header {
				w.Header()[textproto.CanonicalMIMEHeaderKey(k)] = v
			}
			d.log.LogAttrs(ctx, slog.LevelDebug, "note details", slog.String("name", name), slog.Any("note", note))
			status := http.StatusOK
			if note.StatusCode != 0 {
				status = note.StatusCode
			}
			if note.Method == "" {
				w.WriteHeader(status)
				return
			}
			uid := detail.euid
			if note.From != nil {
				uid = *note.From
			}
			if detail.respPrg != nil {
				var resp rpc.Message[any]
				if note.UID.IsZero() {
					err = d.conn.Call(ctx, note.Method,
						rpc.Message[map[string]any]{
							Time: t, UID: uid,
							Body: note.Params,
						},
					).Await(ctx, &resp)
				} else {
					err = d.conn.Call(ctx, rpc.Notify,
						rpc.Message[rpc.Forward[map[string]any]]{
							Time: t, UID: uid,
							Body: rpc.Forward[map[string]any]{
								UID:    note.UID,
								Method: note.Method,
								Params: rpc.NewMessage(uid, note.Params),
							},
						},
					).Await(ctx, &resp)
				}
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelError, "call error", slog.Any("name", name), slog.Any("error", err))
					w.WriteHeader(http.StatusInternalServerError)
				} else {
					d.log.LogAttrs(ctx, slog.LevelDebug, "call response", slog.Any("name", name), slog.Any("response", resp))
				}
				b, err := evalResp(detail.respPrg, t, &resp)
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelError, "eval error", slog.Any("name", name), slog.Any("error", err))
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.WriteHeader(status)
				w.Write(b)
			} else {
				if note.UID.IsZero() {
					err = d.conn.Notify(ctx, note.Method,
						rpc.Message[map[string]any]{
							Time: t, UID: uid,
							Body: note.Params,
						},
					)
				} else {
					err = d.conn.Notify(ctx, rpc.Notify,
						rpc.Message[rpc.Forward[map[string]any]]{
							Time: t, UID: uid,
							Body: rpc.Forward[map[string]any]{
								UID:    note.UID,
								Method: note.Method,
								Params: rpc.NewMessage(uid, note.Params),
							},
						},
					)
				}
				if err != nil {
					d.log.LogAttrs(ctx, slog.LevelError, "notify error", slog.Any("name", name), slog.Any("error", err))
					w.WriteHeader(http.StatusInternalServerError)
				}
			}
		}),
		ErrorLog: slog.NewLogLogger(d.log.Handler(), slog.LevelError),
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	addr = ln.Addr().String()

	d.log.LogAttrs(ctx, slog.LevelInfo, "web server listening", slog.String("addr", addr))
	go func() {
		if tlsConfig == nil {
			err = srv.Serve(ln)
		} else {
			err = srv.ServeTLS(ln, "", "")
		}
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

func evalReq(prg cel.Program, ts time.Time, req *http.Request) (*rest.Notification, error) {
	rm, err := reqToMap(req)
	if err != nil {
		return nil, fmt.Errorf("failed request conversion: %v", err)
	}
	out, _, err := prg.Eval(map[string]any{
		"time":    ts,
		"request": rm,
	})
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
	var note rest.Notification
	err = json.Unmarshal(b, &note)
	if err != nil {
		return nil, fmt.Errorf("%w: data: %q", err, errorContext(err, 10, b))
	}
	return &note, nil
}

func errorContext(err error, window int, data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	var pos int
	switch err := err.(type) {
	default:
		return data
	case *json.SyntaxError:
		pos = int(err.Offset)
	case *json.UnmarshalTypeError:
		pos = int(err.Offset)
	}
	const dots = "…"
	left := max(0, pos-window)
	right := min(pos+window+1, len(data))
	view := make([]byte, right-left+2*len(dots))
	copy(view[len(dots):], data[left:right])
	if left != 0 {
		copy(view, dots)
		left = 0
	} else {
		left = len(dots)
	}
	if right != len(data) {
		copy(view[len(view)-len(dots):], dots)
		right = len(view)
	} else {
		right = len(view) - len(dots)
	}
	return view[left:right]
}

func reqToMap(req *http.Request) (map[string]any, error) {
	rm := map[string]any{
		"Method":        req.Method,
		"URL":           urlMap(req.URL),
		"Proto":         req.Proto,
		"ProtoMajor":    req.ProtoMajor,
		"ProtoMinor":    req.ProtoMinor,
		"Header":        req.Header,
		"ContentLength": req.ContentLength,
		"Close":         req.Close,
		"Host":          req.Host,
	}
	var err error
	rm["Body"], err = reqBody(req.Body)
	if err != nil {
		return nil, err
	}
	if req.RequestURI != "" {
		rm["RequestURI"] = req.RequestURI
	}
	if req.TransferEncoding != nil {
		rm["TransferEncoding"] = req.TransferEncoding
	}
	if req.Trailer != nil {
		rm["Trailer"] = req.Trailer
	}
	return rm, nil
}

func urlMap(u *url.URL) map[string]any {
	um := map[string]any{
		"Scheme":      u.Scheme,
		"Opaque":      u.Opaque,
		"Host":        u.Host,
		"Path":        u.Path,
		"RawPath":     u.RawPath,
		"ForceQuery":  u.ForceQuery,
		"RawQuery":    u.RawQuery,
		"Fragment":    u.Fragment,
		"RawFragment": u.RawFragment,
	}
	if u.User != nil {
		password, passwordSet := u.User.Password()
		um["User"] = map[string]any{
			"Username":    u.User.Username(),
			"Password":    password,
			"PasswordSet": passwordSet,
		}
	}
	return um
}

func reqBody(r io.ReadCloser) ([]byte, error) {
	var buf bytes.Buffer
	_, err := io.Copy(&buf, r)
	r.Close()
	return buf.Bytes(), err
}

func evalResp(prg cel.Program, ts time.Time, resp *rpc.Message[any]) ([]byte, error) {
	out, _, err := prg.Eval(respToMap(resp))
	if err != nil {
		return nil, fmt.Errorf("failed eval: %v", err)
	}

	switch out := out.(type) {
	case types.Bytes:
		return []byte(out), nil
	case types.String:
		return []byte(out), nil
	case types.Int:
		return strconv.AppendInt(nil, int64(out), 10), nil
	case types.Uint:
		return strconv.AppendUint(nil, uint64(out), 10), nil
	case types.Timestamp:
		return []byte(out.Format(time.RFC3339Nano)), nil
	case types.Duration:
		return strconv.AppendInt(nil, int64(out.Duration), 10), nil
	case types.Bool:
		return strconv.AppendBool(nil, bool(out)), nil
	case types.Double:
		return strconv.AppendFloat(nil, float64(out), 'f', -1, 64), nil
	default:
		v, err := out.ConvertToNative(reflect.TypeOf((*structpb.Value)(nil)))
		if err != nil {
			return nil, fmt.Errorf("failed proto conversion: %v", err)
		}
		b, err := protojson.MarshalOptions{}.Marshal(v.(proto.Message))
		if err != nil {
			return nil, fmt.Errorf("failed native conversion: %v", err)
		}
		return b, nil
	}
}

func respToMap(resp *rpc.Message[any]) map[string]any {
	rm := map[string]any{
		"body": resp.Body,
	}
	if !resp.UID.IsZero() {
		uid := make(map[string]any)
		if resp.UID.Module != "" {
			uid["module"] = resp.UID.Module
		}
		if resp.UID.Service != "" {
			uid["service"] = resp.UID.Service
		}
		rm["uid"] = uid
	}
	return map[string]any{
		"time":     resp.Time,
		"response": rm,
	}
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
