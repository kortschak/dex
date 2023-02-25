// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package store

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"time"

	"golang.org/x/exp/slices"
)

// TODO(kortschak): Add support for simple and some aggregate functions and
// SELECT sub-expressions.

// Query represents an SQL SELECT query.
//
// The SQL expressible with Query is a subset of the SQLite SQL language.
// Result columns may be literal columns, count(*) or * and the WHERE clause
// does not support SELECT sub-expressions.
//
// WHERE expressions are represented as a single-entry map. For example,
// 'a = 1 and b != 2' would be represented as
//
//	{"and": [
//		{"=": {"a": 1}},
//		{"!=": {"b": 2}}
//	]}
//
// in JSON.
//
// Language operators that are supported in the WHERE clause are:
//
//   - "or" and "and": take an array of sub-expressions
//   - "not": takes a single sub-expression
//   - "in" and "not in": takes an array of literals and cannot use columns
//   - "isnull", "notnull" and "not null": take a single sub-expression
//   - "=", "!=", "<", ">", "<=", ">=", "is", "is not", "like" and "glob": take
//     a single entry sub-expression where the key is the column name and the value is the comparison argument.
//
// # JSON fields
//
// In addition to fields defined within the DB schema, fields within the datastr
// JSON field are accessible by prefixing the field name with "$.", so to select
// records where datastr.title is not null the expression in JSON would be
//
//	{"notnull": "$.title"}
//
// or to select a set of applications, "terminal" and "firefox" the expression
// would be
//
//	{"in": {"$.app": [
//		"terminal",
//		"firefox"
//	]}}
type Query struct {
	// Fields is the result columns to return.
	// It may be a string, a []string or a []any
	// containing strings. If fields is nil
	// the * result is returned.
	Fields any

	// From is the table to query from. It must
	// be "events" or "buckets".
	From string

	// Where is the SQL WHERE expression. If it
	// is empty, the constructed SQL query will
	// have no WHERE clause.
	Where map[string]any

	// Limit is the result limit. If it is nil,
	// no LIMIT clause will be added to the query.
	Limit *int
}

// Dynamic returns the results of a SELECT query constructed from q.
func (db *DB) Dynamic(q Query) ([]map[string]any, error) {
	allow, ok := db.allow[q.From]
	if !ok {
		if q.From == "" {
			return nil, errors.New("query missing from clause")
		}
		return nil, fmt.Errorf("query from %q not allowed", q.From)
	}
	sql, args, json, err := buildQuery(q, allow)
	if err != nil {
		return nil, err
	}

	rows, err := db.query(sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}

	var e []map[string]any
	for rows.Next() {
		cols := make([]any, len(types))
		for i, t := range types {
			if _, ok := json[t.Name()]; ok {
				// JSON field types may be unknown,
				// so fall back to any.
				var a any
				cols[i] = &a
			} else {
				cols[i] = reflect.New(t.ScanType()).Interface()
			}
		}
		err = rows.Scan(cols...)
		if err != nil {
			return nil, err
		}
		result := make(map[string]any)
		for i := range cols {
			name := types[i].Name()
			if n, ok := json[name]; ok {
				name = n
			}
			result[name] = reflect.ValueOf(cols[i]).Elem().Interface()
		}
		e = append(e, result)
	}
	return e, rows.Err()
}

func buildQuery(q Query, allow map[string]bool) (sql string, args []any, json map[string]string, err error) {
	defer func() {
		switch r := recover().(type) {
		case nil:
		case runtime.Error:
			panic(r)
		case error:
			err = r
		default:
			panic(r)
		}
	}()

	var buf strings.Builder
	buf.WriteString("select")

	fields := q.Fields
	var fieldArgs []any
	switch f := fields.(type) {
	case nil:
		buf.WriteString(" *")
	case string:
		if strings.HasPrefix(f, "$.") {
			buf.WriteString(" json_extract(datastr, ?1)")
			fieldArgs = []any{f}
			json = map[string]string{"json_extract(datastr, ?1)": f}
			break
		}
		stripped := strings.ReplaceAll(f, " ", "")
		if f != "*" && !allow[f] && stripped != "count(*)" {
			panic(fmt.Errorf("invalid fields %s", f))
		}
		if stripped == "count(*)" {
			buf.WriteString(" count(*)")
			break
		}
		buf.WriteString(" " + f)
	case []string:
		f = slices.Clone(f)
		for i, v := range f {
			if strings.HasPrefix(v, "$.") {
				if json == nil {
					json = make(map[string]string)
				}
				j := fmt.Sprintf("json_extract(datastr, ?%d)", len(json)+1)
				f[i] = j
				fieldArgs = append(fieldArgs, v)
				json[j] = v
				continue
			}
			if !allow[v] {
				panic(fmt.Errorf("invalid fields %s", f))
			}
		}
		buf.WriteString(" " + strings.Join(f, ", "))
	case []any:
		fields := make([]string, len(f))
		for i, v := range f {
			field := fmt.Sprint(v)
			if strings.HasPrefix(field, "$.") {
				if json == nil {
					json = make(map[string]string)
				}
				j := fmt.Sprintf("json_extract(datastr, ?%d)", len(json)+1)
				fields[i] = j
				fieldArgs = append(fieldArgs, field)
				json[j] = field
				continue
			}
			if !allow[field] {
				panic(fmt.Errorf("invalid fields %s", f))
			}
			fields[i] = field
		}
		buf.WriteString(" " + strings.Join(fields, ", "))
	default:
		panic(fmt.Errorf("invalid fields type: %T", f))
	}
	fmt.Fprintf(&buf, " from %s", q.From)

	where, args := walk(allow, "", q.Where)
	if where != "" {
		fmt.Fprintf(&buf, " where %s", where)
	}

	if q.Limit != nil {
		fmt.Fprintf(&buf, " limit %d", *q.Limit)
	}

	if len(fieldArgs) != 0 {
		args = append(fieldArgs, args...)
	}
	return buf.String(), args, json, nil
}

func walk(allow map[string]bool, op string, node any) (expr string, args []any) {
	if node == nil {
		return "", nil
	}
	switch node := node.(type) {
	case map[string]any:
		if len(node) == 0 {
			return "", nil
		}
		op, val := keyVal(node)

		// https://www.sqlite.org/lang_expr.html
		var next func(allow map[string]bool, op string, node any) (expr string, args []any)
		switch op {
		case "or", "and":
			next = join
		case "not":
			next = not
		case "in", "not in":
			next = inList
		case "isnull", "notnull", "not null":
			next = null
		case "=", "!=", "<", ">", "<=", ">=", "is", "is not", "like", "glob":
			next = binary
		default:
			panic(fmt.Errorf("no state for %s", op))
		}
		return next(allow, op, val)
	default:
		panic(fmt.Errorf("unknown node type: %#v", node))
	}
}

func join(allow map[string]bool, op string, node any) (expr string, args []any) {
	list, ok := node.([]any)
	if !ok {
		panic(fmt.Errorf("join node is not slice: %t", node))
	}
	exprs := make([]string, len(list))
	for i, e := range list {
		var arg []any
		exprs[i], arg = walk(allow, "", e)
		args = append(args, arg...)
	}
	return fmt.Sprintf("(%s)", strings.Join(exprs, " "+op+" ")), args
}

func binary(allow map[string]bool, op string, node any) (expr string, args []any) {
	inExpr, ok := node.(map[string]any)
	if !ok {
		panic(fmt.Errorf("in node is not map: %t", node))
	}
	if len(inExpr) != 1 {
		panic(fmt.Errorf("in node map is not unit length: %v", node))
	}
	col, param := keyVal(inExpr)
	switch param := param.(type) {
	case int, string, bool, float64, time.Time:
		// No need to check whether col is valid for
		// json_extract since it comes in as a parameter.
		if strings.HasPrefix(col, "$.") {
			return fmt.Sprintf("(json_extract(datastr, ?) %s ?)", op), []any{col, param}
		}
		if !allow[col] {
			panic(fmt.Errorf("invalid column: %s", col))
		}
		return fmt.Sprintf("(%s %s ?)", col, op), []any{param}
	default:
		panic(fmt.Errorf("invalid type for binary param: %T", param))
	}
}

func not(allow map[string]bool, op string, node any) (expr string, args []any) {
	switch notExpr := node.(type) {
	case map[string]any:
		if len(notExpr) != 1 {
			panic(fmt.Errorf("not node map is not unit length: %v", node))
		}
		expr, args = walk(allow, "", notExpr)
		return fmt.Sprintf("(not %s)", expr), args
	case string:
		if strings.HasPrefix(notExpr, "$.") {
			return "(not json_extract(datastr, ?))", []any{notExpr}
		}
		if !allow[notExpr] {
			panic(fmt.Errorf("invalid column: %s", notExpr))
		}
		return fmt.Sprintf("(not %s)", notExpr), args
	default:
		panic(fmt.Errorf("invalid not arg: %v", notExpr))
	}
}

func null(allow map[string]bool, op string, node any) (expr string, args []any) {
	switch nullExpr := node.(type) {
	case map[string]any:
		if len(nullExpr) != 1 {
			panic(fmt.Errorf("not node map is not unit length: %v", node))
		}
		expr, args = walk(allow, "", nullExpr)
		return fmt.Sprintf("((%s) %s)", expr, op), args
	case string:
		if strings.HasPrefix(nullExpr, "$.") {
			return fmt.Sprintf("(json_extract(datastr, ?) %s)", op), []any{nullExpr}
		}
		if !allow[nullExpr] {
			panic(fmt.Errorf("invalid column: %s", nullExpr))
		}
		return fmt.Sprintf("(%s %s)", nullExpr, op), args
	default:
		panic(fmt.Errorf("invalid not arg: %v", nullExpr))
	}
}

func inList(allow map[string]bool, op string, node any) (expr string, args []any) {
	inExpr, ok := node.(map[string]any)
	if !ok {
		panic(fmt.Errorf("in node is not map: %t", node))
	}
	if len(inExpr) != 1 {
		panic(fmt.Errorf("in node map is not unit length: %v", node))
	}
	col, list := keyVal(inExpr)
	var jsonArgs []any
	if strings.HasPrefix(col, "$.") {
		jsonArgs = []any{col}
		col = "json_extract(datastr, ?)"
	} else if !allow[col] {
		panic(fmt.Errorf("invalid column: %s", col))
	}
	switch args := list.(type) {
	case []any:
		return fmt.Sprintf("(%s %s (%s))", col, op, params(len(args))), append(jsonArgs, args...)
	default:
		return fmt.Sprintf("(%s %s (?))", col, op), append(jsonArgs, args)
	}
}

func keyVal[T any](node map[string]T) (string, T) {
	if len(node) != 1 {
		panic(errors.New("invalid node"))
	}
	for k, v := range node {
		return k, v
	}
	panic("unreachable")
}

func params(n int) string {
	p := strings.Repeat("?, ", n)
	return p[:len(p)-2]
}
