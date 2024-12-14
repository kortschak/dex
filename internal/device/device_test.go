// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package device

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
)

func TestPauser(t *testing.T) {
	allow := cmp.AllowUnexported(
		pauser{},
		atomic.Bool{},
		sync.Mutex{},
	)
	comparable := cmpopts.EquateComparable(
		sync.Mutex{},
	)
	ignore := cmp.FilterValues(
		func(_, _ chan struct{}) bool { return true },
		cmp.Ignore(),
	)

	t.Run("pause_is_idempotent", func(t *testing.T) {
		var p1, p2 pauser
		p1.pause()
		p2.pause()
		p2.pause()

		if !cmp.Equal(&p1, &p2, allow, ignore, comparable) {
			t.Errorf("pause is not idempotent:\n--- p1:\n+++ p2:\n%s", cmp.Diff(&p1, &p2, allow, ignore, comparable))
		}
	})
	t.Run("unpause_no_panic", func(t *testing.T) {
		defer func() {
			r := recover()
			if r != nil {
				l := strings.Split(fmt.Sprint(r), "\n")[0]
				t.Errorf("unexpected panic: %s", l)
			}
		}()
		var p pauser
		p.unpause()
	})
	t.Run("unpause_is_idempotent", func(t *testing.T) {
		var p1, p2 pauser
		p1.pause()
		p2.pause()

		p1.unpause()
		p2.unpause()
		p2.unpause()

		if !cmp.Equal(&p1, &p2, allow, ignore, comparable) {
			t.Errorf("unpause is not idempotent:\n--- p1:\n+++ p2:\n%s", cmp.Diff(&p1, &p2, allow, ignore, comparable))
		}
	})
	t.Run("pause_pauses", func(t *testing.T) {
		var p pauser
		p.pause()
		wait := make(chan struct{})
		go func() {
			p.wait(context.Background())
			close(wait)
		}()
		select {
		case <-time.After(time.Second):
		case <-wait:
			t.Error("unexpected non-wait")
		}
	})
	t.Run("unpause_unpauses", func(t *testing.T) {
		var p pauser
		p.pause()
		p.unpause()
		wait := make(chan struct{})
		go func() {
			p.wait(context.Background())
			close(wait)
		}()
		select {
		case <-time.After(time.Millisecond):
			t.Error("unexpected wait")
		case <-wait:
		}
	})
	t.Run("concurrent_use", func(t *testing.T) {
		defer func() {
			r := recover()
			if r != nil {
				l := strings.Split(fmt.Sprint(r), "\n")[0]
				t.Errorf("unexpected panic: %s", l)
			}
		}()
		var p pauser
		var wg sync.WaitGroup
		for i := 0; i < 1000; i++ {
			wg.Add(2)
			go func() {
				defer wg.Done()
				p.pause()
			}()
			go func() {
				defer wg.Done()
				p.unpause()
			}()
		}
	})
}
