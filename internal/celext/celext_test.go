// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package celext

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/interpreter"
	"github.com/kortschak/jsonrpc2"
	"github.com/rogpeppe/go-internal/testscript"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/state"
	"github.com/kortschak/dex/internal/version"
	"github.com/kortschak/dex/rpc"
)

var update = flag.Bool("update", false, "update testscript output files")

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"cel": celMain,
	}))
}

func TestScripts(t *testing.T) {
	t.Parallel()

	p := testscript.Params{
		Dir:           filepath.Join("testdata"),
		UpdateScripts: *update,
	}
	testscript.Run(t, p)
}

const root = "data"

func celMain() (status int) {
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), `Usage of %s:

  %[1]s [-data <data.json>] <src.cel>

`, os.Args[0])
		flag.PrintDefaults()
	}
	data := flag.String("data", "", "path to a JSON object holding input (exposed as the label "+root+")")
	flag.Parse()
	if len(flag.Args()) != 1 {
		flag.Usage()
		return 2
	}

	b, err := os.ReadFile(flag.Args()[0])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	var input any
	if *data != "" {
		b, err := os.ReadFile(*data)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		err = json.Unmarshal(b, &input)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 2
		}
		input = map[string]any{root: input}
	}

	ctx := context.Background()
	klog := slog.New(slogext.NewJSONHandler(os.Stderr, &slogext.HandlerOptions{
		Level: slog.LevelError,
	}))
	kernel, err := rpc.NewKernel(ctx, "tcp", jsonrpc2.NetListenOptions{}, klog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start kernel: %v\n", err)
		return 1
	}
	defer func() {
		err = kernel.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to close kernel: %v\n", err)
			status = 1
		}
	}()
	clientUID := rpc.UID{Module: "test-module"}
	var kernelConn *jsonrpc2.Connection
	err = kernel.Builtin(ctx, "test-module", net.Dialer{}, jsonrpc2.BinderFunc(func(ctx context.Context, kernel *jsonrpc2.Connection) jsonrpc2.ConnectionOptions {
		kernelConn = kernel
		uid := clientUID
		return jsonrpc2.ConnectionOptions{
			Handler: jsonrpc2.HandlerFunc(func(ctx context.Context, req *jsonrpc2.Request) (any, error) {
				if !req.IsCall() {
					return nil, jsonrpc2.ErrNotHandled
				}
				if req.Method == rpc.Who {
					version, err := version.String()
					if err != nil {
						version = err.Error()
					}
					return rpc.NewMessage(uid, version), nil
				}
				return nil, jsonrpc2.ErrNotHandled
			}),
		}
	}))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to install test-module: %v\n", err)
		return 1
	}

	store, err := state.Open("test_db.sqlite3", klog)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to open store: %v\n", err)
		return 1
	}
	defer store.Close()
	err = store.Set(clientUID, "key", []byte("exciting value"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to store sample item: %v\n", err)
		return 1
	}

	storeUID := rpc.UID{Module: "test", Service: "store"}
	kernel.Funcs(rpc.Funcs{
		"get": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[state.GetMessage]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				return nil, err
			}
			val, err := store.Get(m.UID, m.Body.Item)
			if err != nil {
				return nil, err
			}
			return rpc.NewMessage[any](storeUID, state.GetResult{Value: val}), nil
		},
	})

	log := slog.New(slogext.NewJSONHandler(os.Stderr, &slogext.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	res, err := eval(ctx, clientUID, kernelConn, string(b), root, input, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(res)

	return 0
}

func eval(ctx context.Context, uid rpc.UID, conn *jsonrpc2.Connection, src, root string, input any, log *slog.Logger) (string, error) {
	env, err := cel.NewEnv(
		cel.Declarations(decls.NewVar(root, decls.Dyn)),
		cel.OptionalTypes(cel.OptionalTypesVersion(1)),
		Lib(log),
		StateLib(ctx, uid, conn, log),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create env: %v", err)
	}

	ast, iss := env.Compile(src)
	if iss.Err() != nil {
		return "", fmt.Errorf("failed compilation: %v", iss.Err())
	}

	prg, err := env.Program(ast)
	if err != nil {
		return "", fmt.Errorf("failed program instantiation: %v", err)
	}

	if input == nil {
		input = interpreter.EmptyActivation()
	}
	out, _, err := prg.Eval(input)
	if err != nil {
		return "", fmt.Errorf("failed eval: %v", err)
	}

	v, err := out.ConvertToNative(reflect.TypeOf(&structpb.Value{}))
	if err != nil {
		return "", fmt.Errorf("failed proto conversion: %v", err)
	}
	b, err := protojson.MarshalOptions{Indent: "\t"}.Marshal(v.(proto.Message))
	if err != nil {
		return "", fmt.Errorf("failed native conversion: %v", err)
	}
	var res any
	err = json.Unmarshal(b, &res)
	if err != nil {
		return "", fmt.Errorf("failed json conversion: %v", err)
	}
	var buf strings.Builder
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "\t")
	err = enc.Encode(res)
	return strings.TrimRight(buf.String(), "\n"), err
}
