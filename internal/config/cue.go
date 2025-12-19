// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"fmt"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/encoding/gocode/gocodec"
	"golang.org/x/exp/constraints"
)

// Validate performs a validation of the provided configuration value, returning
// a list of invalid paths and a CUE errors.Error explaining the issues found if
// the configuration is invalid according to the provided schema.
func Validate(schema string, cfg any) (paths [][]string, err error) {
	ctx := cuecontext.New()

	v := ctx.CompileString(schema)
	codec := gocodec.New(ctx, nil)

	w, err := codec.Decode(cfg)
	if err != nil {
		return nil, err
	}

	u := v.Unify(w)
	err = u.Validate(cue.Concrete(true), cue.Final())
	errs := cerrors.Errors(err)
	if len(errs) != 0 {
		paths = make([][]string, 0, len(errs))
		err = cerrors.Append(
			cerrors.Promote(err, ""),
			cerrors.Promote(fmt.Errorf("%s", u), "not concrete"),
		)
	}
	for _, err := range errs {
		p := cerrors.Path(err)
		if p != nil {
			paths = append(paths, p)
		}
	}

	return unique(paths), err
}

// unique returns paths lexically sorted in ascending order and with repeated
// and nil elements omitted.
func unique(paths [][]string) [][]string {
	if len(paths) < 2 {
		return paths
	}
	sort.Slice(paths, func(i, j int) bool {
		return compare(paths[i], paths[j]) < 0
	})
	curr := 0
	for i, p := range paths {
		if compare(p, paths[curr]) == 0 {
			continue
		}
		curr++
		if curr < i {
			paths[curr], paths[i] = paths[i], nil
		}
	}
	// Remove any nil paths.
	var s int
	for i, p := range paths {
		if p != nil {
			s = i
			break
		}
	}
	return paths[s : curr+1]
}

func compare[T constraints.Ordered](a, b []T) int {
	l := len(a)
	if len(b) < l {
		l = len(b)
	}
	if l == 0 || &a[0] == &b[0] {
		goto same
	}
	for i := 0; i < l; i++ {
		e1, e2 := a[i], b[i]
		if e1 < e2 {
			return -1
		}
		if e1 > e2 {
			return +1
		}
	}
same:
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return +1
	}
	return 0
}
