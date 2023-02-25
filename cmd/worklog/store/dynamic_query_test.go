// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package store

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var buildQueryTests = []struct {
	name     string
	query    string
	allow    map[string]bool
	wantSQL  string
	wantJSON map[string]string
	wantArgs []any
	wantErr  error
}{
	{
		name: "kitchen_all_explicit",
		query: `{
	"from": "table",
	"fields": "*",
	"where": {
		"or": [
			{"not": {"and": [
				{"like": {"$.a": 1}},
				{"=": {"b": 2}}
			]}},
			{"=": {"c": 3}},
			{"!=": {"d": "4"}},
			{"isnull": "d"},
			{"in": {"b": ["what","how"]}},
			{"not": "e"}
		]
	},
	"limit": 2
}`,
		allow: map[string]bool{
			"a": true,
			"b": true,
			"c": true,
			"d": true,
			"e": true,
		},
		wantSQL:  `select * from table where ((not ((json_extract(datastr, ?) like ?) and (b = ?))) or (c = ?) or (d != ?) or (d isnull) or (b in (?, ?)) or (not e)) limit 2`,
		wantArgs: []any{"$.a", 1.0, 2.0, 3.0, "4", "what", "how"},
		wantJSON: nil,
		wantErr:  nil,
	},
	{
		name: "kitchen_all_implicit",
		query: `{
	"from": "table",
	"where": {
		"or": [
			{"not": {"and": [
				{"like": {"$.a": 1}},
				{"=": {"b": 2}}
			]}},
			{"=": {"c": 3}},
			{"!=": {"d": "4"}},
			{"isnull": "d"},
			{"in": {"b": ["what","how"]}},
			{"not": "e"}
		]
	},
	"limit": 2
}`,
		allow: map[string]bool{
			"a": true,
			"b": true,
			"c": true,
			"d": true,
			"e": true,
		},
		wantSQL:  `select * from table where ((not ((json_extract(datastr, ?) like ?) and (b = ?))) or (c = ?) or (d != ?) or (d isnull) or (b in (?, ?)) or (not e)) limit 2`,
		wantArgs: []any{"$.a", 1.0, 2.0, 3.0, "4", "what", "how"},
		wantJSON: nil,
		wantErr:  nil,
	},
	{
		name: "json_fields",
		query: `{
	"fields": ["$.field1", "$.field2"],
	"from": "table"
}`,
		wantSQL:  `select json_extract(datastr, ?1), json_extract(datastr, ?2) from table`,
		wantArgs: []any{"$.field1", "$.field2"},
		wantJSON: map[string]string{
			"json_extract(datastr, ?1)": "$.field1",
			"json_extract(datastr, ?2)": "$.field2",
		},
		wantErr: nil,
	},
	{
		name: "not_null_json_field",
		query: `{
	"from": "table",
	"where": {"notnull": "$.field"}
}`,
		wantSQL:  `select * from table where (json_extract(datastr, ?) notnull)`,
		wantArgs: []any{"$.field"},
		wantJSON: nil,
		wantErr:  nil,
	},
	{
		name: "json_field_in",
		query: `{
	"from": "table",
	"where": {"in": {"$.field": ["a", "b"]}}
}`,
		wantSQL:  `select * from table where (json_extract(datastr, ?) in (?, ?))`,
		wantArgs: []any{"$.field", "a", "b"},
		wantJSON: nil,
		wantErr:  nil,
	},
}

func TestBuildQuery(t *testing.T) {
	for _, test := range buildQueryTests {
		t.Run(test.name, func(t *testing.T) {
			var q struct {
				Fields any            `json:"fields,omitempty"`
				From   string         `json:"from,omitempty"`
				Where  map[string]any `json:"where,omitempty"`
				Limit  *int           `json:"limit,omitempty"`
			}
			err := json.Unmarshal([]byte(test.query), &q)
			if err != nil {
				t.Fatalf("failed to unmarshal query: %v", err)
			}

			gotSQL, gotArgs, gotJSON, err := buildQuery(Query(q), test.allow)
			if !sameError(err, test.wantErr) {
				t.Errorf("unexpected error: got:%v want:%v", err, test.wantErr)
			}
			if err != nil {
				return
			}
			if gotSQL != test.wantSQL {
				t.Errorf("unexpected SQL generated:\ngot: %s\nwant:%s", gotSQL, test.wantSQL)
			}
			if !cmp.Equal(test.wantArgs, gotArgs) {
				t.Errorf("unexpected arguments:\n--- want:\n+++ got:\n%s", cmp.Diff(test.wantArgs, gotArgs))
			}
			if !cmp.Equal(test.wantJSON, gotJSON) {
				t.Errorf("unexpected arguments:\n--- want:\n+++ got:\n%s", cmp.Diff(test.wantJSON, gotJSON))
			}
		})
	}
}

func sameError(a, b error) bool {
	switch {
	case a != nil && b != nil:
		return a.Error() == b.Error()
	default:
		return a == b
	}
}
