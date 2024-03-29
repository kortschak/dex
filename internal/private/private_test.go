// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package private

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type redactTest struct {
	name    string
	in      any
	tag     string
	want    any
	wantErr error
}

var redactTests = []redactTest{
	{
		name: "map_string",
		in: map[string]any{
			"private":    "secret",
			"secret":     "1",
			"not_secret": "2",
		},
		want: map[string]any{
			"private":    "secret",
			"not_secret": "2",
		},
	},
	{
		name: "map_string_inner",
		in: map[string]any{
			"inner": map[string]any{
				"private":    "secret",
				"secret":     "1",
				"not_secret": "2",
			}},
		want: map[string]any{
			"inner": map[string]any{
				"private":    "secret",
				"not_secret": "2",
			}},
	},
	{
		name: "map_slice",
		in: map[string]any{
			"private":    []string{"secret"},
			"secret":     "1",
			"not_secret": "2",
		},
		want: map[string]any{
			"private":    []string{"secret"},
			"not_secret": "2",
		},
	},
	{
		name: "map_cycle",
		in: func() any {
			m := map[string]any{
				"private":    "secret",
				"secret":     "1",
				"not_secret": "2",
			}
			m["loop"] = m
			return m
		}(),
		want:    map[string]any(nil),
		wantErr: cycle{reflect.TypeOf(map[string]any(nil))},
	},
	func() redactTest {
		type s struct {
			Private   string
			Secret    string
			NotSecret string
		}
		return redactTest{
			name: "struct_string",
			in: s{
				Private:   "Secret",
				Secret:    "1",
				NotSecret: "2",
			},
			tag: "",
			want: s{
				Private:   "Secret",
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   []string
			Secret    string
			NotSecret string
		}
		return redactTest{
			name: "struct_slice",
			in: s{
				Private:   []string{"Secret"},
				Secret:    "1",
				NotSecret: "2",
			},
			tag: "",
			want: s{
				Private:   []string{"Secret"},
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   string
			Secret    string
			NotSecret string
			Loop      *s
		}
		v := s{
			Private:   "Secret",
			Secret:    "1",
			NotSecret: "2",
		}
		v.Loop = &v
		return redactTest{
			name:    "struct_loop",
			in:      v,
			tag:     "",
			want:    s{},
			wantErr: cycle{reflect.TypeOf(&s{})},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   string `json:"private"`
			Secret    string `json:"secret"`
			NotSecret string `json:"not_secret"`
		}
		return redactTest{
			name: "struct_string_json",
			in: s{
				Private:   "secret",
				Secret:    "1",
				NotSecret: "2",
			},
			tag: "json",
			want: s{
				Private:   "secret",
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   struct{} `private:"secret"`
			Secret    string   `json:"secret"`
			NotSecret string   `json:"not_secret"`
		}
		return redactTest{
			name: "struct_string_on_tag_json",
			in: s{
				Secret:    "1",
				NotSecret: "2",
			},
			tag: "json",
			want: s{
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   struct{} `private:"secret1,secret2"`
			Secret1   string   `json:"secret1"`
			Secret2   string   `json:"secret2"`
			NotSecret string   `json:"not_secret"`
		}
		return redactTest{
			name: "struct_string_list_on_tag_json",
			in: s{
				Secret1:   "1",
				Secret2:   "1",
				NotSecret: "2",
			},
			tag: "json",
			want: s{
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   string `json:"private"`
			Secret    string
			NotSecret string `json:"not_secret"`
		}
		return redactTest{
			name: "struct_string_json_missing_tag",
			in: s{
				Private:   "Secret",
				Secret:    "1",
				NotSecret: "2",
			},
			tag: "json",
			want: s{
				Private:   "Secret",
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   []string `json:"private"`
			Secret    string   `json:"secret"`
			NotSecret string   `json:"not_secret"`
		}
		return redactTest{
			name: "struct_slice_json",
			in: s{
				Private:   []string{"secret"},
				Secret:    "1",
				NotSecret: "2",
			},
			tag: "json",
			want: s{
				Private:   []string{"secret"},
				NotSecret: "2",
			},
		}
	}(),
	func() redactTest {
		type s struct {
			Private   string `json:"private"`
			Secret    string `json:"secret"`
			NotSecret string `json:"not_secret"`
			Loop      *s     `json:"loop"`
		}
		v := s{
			Private:   "secret",
			Secret:    "1",
			NotSecret: "2",
		}
		v.Loop = &v
		return redactTest{
			name:    "struct_loop_json",
			in:      v,
			tag:     "json",
			want:    s{},
			wantErr: cycle{reflect.TypeOf(&s{})},
		}
	}(),
}

func TestRedact(t *testing.T) {
	allow := cmp.AllowUnexported()

	for _, test := range redactTests {
		t.Run(test.name, func(t *testing.T) {
			var before []byte
			_, isCycle := test.wantErr.(cycle)
			if !isCycle {
				var err error
				before, err = json.Marshal(test.in)
				if err != nil {
					t.Fatalf("failed to get before state: %v", err)
				}
			}
			got, err := Redact(test.in, test.tag)
			if err != test.wantErr {
				t.Fatalf("unexpected error from Redact: %v", err)
			}
			if !isCycle {
				after, err := json.Marshal(test.in)
				if err != nil {
					t.Fatalf("failed to get after state: %v", err)
				}
				if !bytes.Equal(before, after) {
					t.Errorf("unexpected change in input:\n---:\n+++:\n%s", cmp.Diff(before, after))
				}
			}
			if !cmp.Equal(test.want, got, allow) {
				t.Errorf("unexpected paths:\n--- want:\n+++ got:\n%s", cmp.Diff(test.want, got, allow))
			}
		})
	}
}
