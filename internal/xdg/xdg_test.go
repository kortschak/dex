// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xdg

import (
	"fmt"
	"os"
	"testing"
)

var envOrDefaultTests = []struct {
	set map[string]string

	key, def, home string

	want   string
	wantOK bool
}{
	0: {
		set: map[string]string{
			"test_HOME": "testdata/home",
			"testkey":   "testdata/home/dir",
		},
		key:  "testkey",
		def:  "testdata/global_dir",
		home: "test_HOME",

		want:   "testdata/home/dir",
		wantOK: true,
	},
	1: {
		set: map[string]string{
			"test_HOME": "testdata/home",
		},
		key:  "testkey",
		def:  "testdata/global_dir",
		home: "test_HOME",

		want:   "testdata/home/testdata/global_dir",
		wantOK: true,
	},
	2: {
		set: map[string]string{
			"test_HOME": "testdata/home",
		},
		key:  "testkey",
		def:  "",
		home: "test_HOME",

		want:   "",
		wantOK: false,
	},
	3: {
		set: map[string]string{
			"test_HOME": "testdata/home",
		},
		key:  "testkey",
		def:  "testdata/global_dir",
		home: "",

		want:   "testdata/global_dir",
		wantOK: true,
	},
	4: {
		set: map[string]string{
			"test_HOME": "testdata/home",
		},
		key:  "testkey",
		def:  "testdata/global_dir",
		home: "invalid",

		want:   "",
		wantOK: false,
	},
}

func TestEnvOrDefault(t *testing.T) {

	for i, test := range envOrDefaultTests {
		for k, v := range test.set {
			if _, ok := os.LookupEnv(k); ok {
				panic(fmt.Sprintf("already set in env: %s", k))
			}
			if k == "test_HOME" && test.home == "" {
				continue
			}
			os.Setenv(k, v)
		}

		got, gotOK := envOrDefault(test.key, test.def, test.home)
		if gotOK != test.wantOK {
			t.Errorf("unexpected ok for %d: got:%t want:%t", i, gotOK, test.wantOK)
		}
		if got != test.want {
			t.Errorf("unexpected result for %d: got:%q want:%q", i, got, test.want)
		}

		for k := range test.set {
			os.Unsetenv(k)
		}
	}
}
