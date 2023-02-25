// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package celext

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/checker/decls"
	"github.com/google/cel-go/interpreter"
	"github.com/rogpeppe/go-internal/testscript"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kortschak/dex/internal/slogext"
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

func celMain() int {
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

	log := slog.New(slogext.NewJSONHandler(os.Stderr, &slogext.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	res, err := eval(string(b), root, input, log)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(res)
	return 0
}

func eval(src, root string, input any, log *slog.Logger) (string, error) {
	env, err := cel.NewEnv(
		cel.Declarations(decls.NewVar(root, decls.Dyn)),
		Lib(log),
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
