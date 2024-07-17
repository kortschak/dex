// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package celext provides extensions to ease use of maps in CEL programs.
package celext

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common"
	"github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/common/types/traits"
	"github.com/google/cel-go/parser"
	"github.com/kortschak/jsonrpc2"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/kortschak/dex/internal/state"
	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"
)

// Lib returns a cel.EnvOption to configure extended functions to ease
// use of maps and timestamps.
//
// As (Macro)
//
// The as macro is syntactic sugar for [val].map(var, function)[0].
//
// Examples:
//
//	{"a":1, "b":2}.as(v, v.a == 1)         // return true
//	{"a":1, "b":2}.as(v, v)                // return {"a":1, "b":2}
//	{"a":1, "b":2}.as(v, v.with({"c":3}))  // return {"a":1, "b":2, "c":3}
//	{"a":1, "b":2}.as(v, [v, v])           // return [{"a":1, "b":2}, {"a":1, "b":2}]
//
// # With
//
// Returns the receiver's value with the value of the parameter updating
// or adding fields:
//
//	<map<K,V>>.with(<map<K,V>>) -> <map<K,V>>
//
// Examples:
//
//	{"a":1, "b":2}.with({"a":10, "c":3})  // return {"a":10, "b":2, "c":3}
//
// # With Replace
//
// Returns the receiver's value with the value of the parameter replacing
// existing fields:
//
//	<map<K,V>>.with_replace(<map<K,V>>) -> <map<K,V>>
//
// Examples:
//
//	{"a":1, "b":2}.with_replace({"a":10, "c":3})  // return {"a":10, "b":2}
//
// # With Update
//
// Returns the receiver's value with the value of the parameter updating
// the map without replacing any existing fields:
//
//	<map<K,V>>.with_update(<map<K,V>>) -> <map<K,V>>
//
// Examples:
//
//	{"a":1, "b":2}.with_update({"a":10, "c":3})  // return {"a":1, "b":2, "c":3}
//
// # Is Zero
//
// Returns whether the receiver is the zero time:
//
//	<timestamp>.is_zero() -> <bool>
//
// Examples:
//
//	timestamp("0001-01-01T00:00:00Z").is_zero()  // return true
//	timestamp("0001-01-01T00:00:01Z").is_zero()  // return false
//
// # Debug
//
// The second parameter is returned unaltered and the value is logged to the
// lib's logger:
//
//	debug(<string>, <dyn>) -> <dyn>
//
// Examples:
//
//	debug("tag", expr) // return expr even if it is an error and logs with "tag".
func Lib(log *slog.Logger) cel.EnvOption {
	return cel.Lib(lib{log: log})
}

type lib struct {
	log *slog.Logger
}

func (l lib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Macros(parser.NewReceiverMacro("as", 2, makeAs)),
		cel.Function("is_zero",
			cel.MemberOverload(
				"timestamp_is_zero",
				[]*cel.Type{cel.TimestampType},
				cel.BoolType,
				cel.UnaryBinding(isZero),
			),
		),
		cel.Function("with",
			cel.MemberOverload(
				"map_with_map",
				[]*cel.Type{mapKV, mapKV},
				mapKV,
				cel.BinaryBinding(withAll),
			),
		),
		cel.Function("with_update",
			cel.MemberOverload(
				"map_with_update_map",
				[]*cel.Type{mapKV, mapKV},
				mapKV,
				cel.BinaryBinding(withUpdate),
			),
		),
		cel.Function("with_replace",
			cel.MemberOverload(
				"map_with_replace_map",
				[]*cel.Type{mapKV, mapKV},
				mapKV,
				cel.BinaryBinding(withReplace),
			),
		),
		cel.Function("debug",
			cel.Overload(
				"debug_string_dyn",
				[]*cel.Type{cel.StringType, cel.DynType},
				cel.DynType,
				cel.BinaryBinding(l.logDebug),
				cel.OverloadIsNonStrict(),
			),
		),
	}
}

var mapKV = cel.MapType(cel.TypeParamType("K"), cel.TypeParamType("V"))

func (lib) ProgramOptions() []cel.ProgramOption { return nil }

func makeAs(eh parser.ExprHelper, target ast.Expr, args []ast.Expr) (ast.Expr, *common.Error) {
	ident := args[0]
	if ident.Kind() != ast.IdentKind {
		return nil, &common.Error{Message: "argument is not an identifier"}
	}
	label := ident.AsIdent()
	expr := args[1]
	const unused = "_"
	return eh.NewComprehension(eh.NewList(), unused, label, target, eh.NewLiteral(types.False), eh.NewIdent(label), expr), nil
}

func isZero(arg ref.Val) ref.Val {
	ts, ok := arg.(types.Timestamp)
	if !ok {
		return types.ValOrErr(ts, "no such overload")
	}
	return types.Bool(ts.IsZeroValue())
}

func withAll(dst, src ref.Val) ref.Val {
	new, other, err := with(dst, src)
	if err != nil {
		return err
	}
	for k, v := range other {
		new[k] = v
	}
	return types.NewRefValMap(types.DefaultTypeAdapter, new)
}

func withUpdate(dst, src ref.Val) ref.Val {
	new, other, err := with(dst, src)
	if err != nil {
		return err
	}
	for k, v := range other {
		if _, ok := new[k]; ok {
			continue
		}
		new[k] = v
	}
	return types.NewRefValMap(types.DefaultTypeAdapter, new)
}

func withReplace(dst, src ref.Val) ref.Val {
	new, other, err := with(dst, src)
	if err != nil {
		return err
	}
	for k, v := range other {
		if _, ok := new[k]; !ok {
			continue
		}
		new[k] = v
	}
	return types.NewRefValMap(types.DefaultTypeAdapter, new)
}

var refValMap = reflect.TypeOf(map[ref.Val]ref.Val(nil))

func with(dst, src ref.Val) (res, other map[ref.Val]ref.Val, maybe ref.Val) {
	obj, ok := dst.(traits.Mapper)
	if !ok {
		return nil, nil, types.ValOrErr(obj, "no such overload")
	}
	val, ok := src.(traits.Mapper)
	if !ok {
		return nil, nil, types.ValOrErr(src, "unsupported src type")
	}

	new := make(map[ref.Val]ref.Val)
	m, err := obj.ConvertToNative(refValMap)
	if err != nil {
		return nil, nil, types.NewErr("unable to convert dst to native: %v", err)
	}
	for k, v := range m.(map[ref.Val]ref.Val) {
		new[k] = v
	}
	m, err = val.ConvertToNative(refValMap)
	if err != nil {
		return nil, nil, types.NewErr("unable to convert src to native: %v", err)
	}
	return new, m.(map[ref.Val]ref.Val), nil
}

func (l lib) logDebug(arg0, arg1 ref.Val) ref.Val {
	tag, ok := arg0.(types.String)
	if !ok {
		return types.ValOrErr(tag, "no such overload")
	}
	if l.log == nil {
		return arg1
	}
	val, err := arg1.ConvertToNative(reflect.TypeOf((*structpb.Value)(nil)))
	if err != nil {
		l.log.LogAttrs(context.Background(), slog.LevelError, "cel debug log error", slog.String("tag", string(tag)), slog.Any("error", err))
	} else {
		l.log.LogAttrs(context.Background(), slog.LevelDebug, "cel debug log", slog.String("tag", string(tag)), slog.Any("value", val))
	}
	return arg1
}

// StateLib returns a cel.EnvOption to configure extended functions to allow
// access to the rpc kernel state store get RPC call method over the provided
// JSON-RPC2.0 connection.
//
// # Get State
//
// The parameter is the key to the value stored in the state store for the
// service configured with the UID:
//
//	get_state(<string>) -> <optional<bytes>>
//
// Examples:
//
//	get_state("key") -> optional.of(b"value")
//	get_state("missing key") -> optional.none
func StateLib(ctx context.Context, uid rpc.UID, conn *jsonrpc2.Connection, log *slog.Logger) cel.EnvOption {
	return cel.Lib(stateLib{
		ctx:  ctx,
		uid:  uid,
		conn: conn,
		log:  log,
	})
}

type stateLib struct {
	ctx  context.Context
	uid  rpc.UID
	conn *jsonrpc2.Connection

	log *slog.Logger
}

func (l stateLib) CompileOptions() []cel.EnvOption {
	return []cel.EnvOption{
		cel.Function("get_state",
			cel.Overload(
				"get_state_string_bytes",
				[]*cel.Type{cel.StringType},
				cel.OptionalType(cel.BytesType),
				cel.UnaryBinding(l.getState),
				cel.OverloadIsNonStrict(),
			),
		),
	}
}

func (stateLib) ProgramOptions() []cel.ProgramOption { return nil }

func (l stateLib) getState(arg ref.Val) ref.Val {
	key, ok := arg.(types.String)
	if !ok {
		return types.ValOrErr(key, "no such overload")
	}
	if l.conn == nil {
		l.log.LogAttrs(l.ctx, slog.LevelError, "failed get", slog.String("key", string(key)), slog.String("error", "no conn to kernel"))
		return types.OptionalOf(types.NewErr("failed get: no conn to kernel"))
	}
	var resp rpc.Message[state.GetResult]
	err := l.conn.Call(l.ctx, "get", rpc.Message[state.GetMessage]{
		Time: time.Now(), UID: l.uid,
		Body: state.GetMessage{Item: string(key)},
	}).Await(l.ctx, &resp)
	if err != nil {
		if !errors.Is(err, sys.ErrNotFound) {
			return types.NewErr("failed get: %w", err)
		}
		l.log.LogAttrs(l.ctx, slog.LevelInfo, "failed get", slog.String("key", string(key)), slog.Any("error", err))
		return types.OptionalNone
	}
	return types.OptionalOf(types.Bytes(resp.Body.Value))
}
