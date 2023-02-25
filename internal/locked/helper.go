// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package locked provides concurrency-safe helpers.
package locked

import (
	"bytes"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// BytesBuffer is a locked bytes.Buffer.
type BytesBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *BytesBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *BytesBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// Mutex is a deadlock debugging lock. It will panic in a deadlock of longer
// than a defined grace period.
type Mutex struct {
	lock  chan struct{}
	grace time.Duration
	file  string
	line  int
	ok    bool
}

// NewMutex returns a usable sync.Locker with the specified grace period.
func NewMutex(grace time.Duration) sync.Locker {
	return &Mutex{lock: make(chan struct{}, 1), grace: grace}
}

// Lock locks the lock or panics after the grace period indicating the position
// of the last successful lock call.
func (m *Mutex) Lock() {
	timer := time.NewTimer(m.grace)
	select {
	case <-timer.C:
		panic(fmt.Sprintf("last successful lock at %s:%d valid=%t", m.file, m.line, m.ok))
	case m.lock <- struct{}{}:
		timer.Stop()
		_, m.file, m.line, m.ok = runtime.Caller(1)
	}
}

// Unlock unlocks the lock.
func (m *Mutex) Unlock() {
	select {
	default:
		panic("unlock of unlocked mutex")
	case <-m.lock:
	}
}
