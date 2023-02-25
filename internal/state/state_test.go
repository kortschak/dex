// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"
)

const workDir = "testdata"

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
	keep    = flag.Bool("keep", false, "keep workdir after tests")
)

func Test(t *testing.T) {
	err := os.Mkdir(workDir, 0o755)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatalf("failed to make dir: %v", err)
	}
	if !*keep {
		t.Cleanup(func() {
			os.RemoveAll(workDir)
		})
	}

	t.Run("db", func(t *testing.T) {
		const dbPath = "test.db"

		path := filepath.Join(workDir, dbPath)
		err = os.Remove(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("failed to clean dir: %v", err)
		}

		var logBuf bytes.Buffer
		log := slog.New(slogext.NewJSONHandler(&logBuf, &slogext.HandlerOptions{
			Level:     slog.LevelDebug,
			AddSource: slogext.NewAtomicBool(*lines),
		}))
		defer func() {
			if *verbose && logBuf.Len() != 0 {
				t.Logf("log:\n%s\n", &logBuf)
			}
		}()

		db, err := Open(path, log)
		if err != nil {
			t.Fatalf("failed to create db: %v", err)
		}

		mapDB := make(map[dbKey][]byte)

		owner0 := rpc.UID{Module: "state"}
		owner1 := rpc.UID{Module: "state", Service: "test-1"}
		owner2 := rpc.UID{Module: "state", Service: "test-2"}
		owner3 := rpc.UID{Module: "other", Service: "test-3"}
		owner4 := rpc.UID{Module: "other", Service: "test-4"}
		owner5 := rpc.UID{Module: "other", Service: "test-5"}

		testSet(t, db, mapDB, owner0, "a", []byte("val-0"))
		testSet(t, db, mapDB, owner1, "a", []byte("val-1"))
		testGet(t, db, mapDB, owner1, "a")
		testGet(t, db, mapDB, owner1, "x")
		testPut(t, db, mapDB, owner1, "a", []byte("val-1"))
		testPut(t, db, mapDB, owner1, "a", []byte("val-2"))
		testPut(t, db, mapDB, owner1, "a", []byte("val-2"))
		testSet(t, db, mapDB, owner1, "b", []byte("val-3"))
		testSet(t, db, mapDB, owner2, "a", []byte("val-4"))
		testSet(t, db, mapDB, owner2, "b", []byte("val-5"))
		testSet(t, db, mapDB, owner3, "b", []byte("val-6"))
		testSet(t, db, mapDB, owner3, "c", []byte("val-7"))
		testSet(t, db, mapDB, owner4, "b", []byte("val-8"))
		testSet(t, db, mapDB, owner5, "d", []byte("val-9"))
		testDrop(t, db, mapDB, owner3)
		testDropAll(t, db, mapDB, "other")
		testDelete(t, db, mapDB, owner2, "b")

		// When service can be NULL, these fail.
		testSet(t, db, mapDB, owner0, "item", []byte("val-x"))
		testGet(t, db, mapDB, owner0, "item")
		testSet(t, db, mapDB, owner0, "item", []byte("val-y"))
		testGet(t, db, mapDB, owner0, "item")
		testSet(t, db, mapDB, owner0, "item", []byte("val-x"))
		testDelete(t, db, mapDB, owner0, "item")

		// Test set fails with empty module or item.
		err = db.set(db.store, rpc.UID{Service: "-"}, "item", []byte{0})
		if err == nil {
			t.Error("expected error for empty module name")
		}
		err = db.set(db.store, rpc.UID{Module: "-"}, "", []byte{0})
		if err == nil {
			t.Error("expected error for empty item name")
		}

		gotDump, err := db.Dump()
		if err != nil {
			t.Errorf("failed to dump db: %v", err)
		}

		wantDump := map[rpc.UID]map[string][]uint8{
			{Module: "state"}: {
				"a": {
					0x76, 0x61, 0x6c, 0x2d, 0x30, // |val-0|
				},
			},
			{Module: "state", Service: "test-1"}: {
				"a": {
					0x76, 0x61, 0x6c, 0x2d, 0x32, // |val-2|
				},
				"b": {
					0x76, 0x61, 0x6c, 0x2d, 0x33, // |val-3|
				},
			},
			{Module: "state", Service: "test-2"}: {
				"a": {
					0x76, 0x61, 0x6c, 0x2d, 0x34, // |val-4|
				},
			},
		}
		if !cmp.Equal(gotDump, wantDump) {
			t.Errorf("unexpected dump result:\n--- want:\n+++ got:\n%s",
				cmp.Diff(wantDump, gotDump))
		}

		for o, want := range wantDump {
			got, err := db.GetAll(o)
			if err != nil {
				t.Errorf("failed to get all for %s db: %v", o, err)
			}
			if !cmp.Equal(got, want) {
				t.Errorf("unexpected get all result:\n--- want:\n+++ got:\n%s",
					cmp.Diff(want, got))
			}

		}

		gotModDump, err := db.DumpModule(owner0.Module)
		if err != nil {
			t.Errorf("failed to get all module for %s db: %v", owner0.Module, err)
		}
		if !cmp.Equal(gotModDump, wantDump) {
			t.Errorf("unexpected dump module result:\n--- want:\n+++ got:\n%s",
				cmp.Diff(wantDump, gotModDump))
		}

		wantJSON := []byte(`{
	"state": {
		"a": "dmFsLTA="
	},
	"state.test-1": {
		"a": "dmFsLTI=",
		"b": "dmFsLTM="
	},
	"state.test-2": {
		"a": "dmFsLTQ="
	}
}`)
		gotJSON, err := JSON(gotDump)
		if err != nil {
			t.Errorf("failed to marshal dump: %v", err)
		}
		var buf bytes.Buffer
		json.Indent(&buf, gotJSON, "", "\t")
		if err != nil {
			t.Errorf("failed to indent dump: %v", err)
		}

		if !cmp.Equal(buf.Bytes(), wantJSON) {
			t.Errorf("unexpected json result:\n--- want:\n+++ got:\n%s",
				cmp.Diff(wantJSON, buf.Bytes()))
		}

		err = db.Close()
		if err != nil {
			t.Errorf("failed to close db: %v", err)
		}

		db, err = Open(filepath.Join(workDir, dbPath), log)
		if err != nil {
			t.Fatalf("failed to create db: %v", err)
		}
		t.Cleanup(func() {
			err = db.Close()
			if err != nil {
				t.Errorf("failed to close db: %v", err)
			}
		})

		gotDump, err = db.Dump()
		if err != nil {
			t.Errorf("failed to dump db: %v", err)
		}
		if !cmp.Equal(gotDump, wantDump) {
			t.Errorf("unexpected dump result:\n--- want:\n+++ got:\n%s",
				cmp.Diff(wantDump, gotDump))
		}
	})

	t.Run("concurrent_access", func(t *testing.T) {
		const dbPath = "test-concurrent.db"

		path := filepath.Join(workDir, dbPath)
		err = os.Remove(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("failed to clean dir: %v", err)
		}

		log := slog.New(slogext.NewJSONHandler(io.Discard, &slogext.HandlerOptions{
			Level:     slog.LevelDebug,
			AddSource: slogext.NewAtomicBool(*lines),
		}))

		db, err := Open(filepath.Join(workDir, dbPath), log)
		if err != nil {
			t.Fatalf("failed to create db: %v", err)
		}
		t.Cleanup(func() {
			err = db.Close()
			if err != nil {
				t.Errorf("failed to close db: %v", err)
			}
		})

		owner := rpc.UID{Module: "state", Service: "test"}
		const n = 1000
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			if t.Failed() {
				break
			}
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := db.Set(owner, fmt.Sprintf("%03d", i), []byte{0})
				if err != nil {
					t.Errorf("failed during iteration %d", i)
				}
			}()
		}
		wg.Wait()
		d, err := db.Dump()
		if err != nil {
			t.Errorf("failed to dump db: %v", err)
		}
		if got := len(d[owner]); got != n {
			t.Errorf("unexpected number of items: got:%d want:%d", got, n)
		}
	})
}

func testSet(t *testing.T, db *DB, m map[dbKey][]byte, owner rpc.UID, item string, val []byte) {
	t.Helper()
	err := db.Set(owner, item, val)
	if err != nil {
		t.Errorf("failed to set %s.%s to %q: %v", owner, item, val, err)
		return
	}
	m[dbKey{Owner: owner, Item: item}] = bytes.Clone(val)
}

func testDelete(t *testing.T, db *DB, m map[dbKey][]byte, owner rpc.UID, item string) {
	t.Helper()
	err := db.Delete(owner, item)
	if err != nil {
		t.Errorf("failed to delete %s.%s: %v", owner, item, err)
		return
	}
	delete(m, dbKey{Owner: owner, Item: item})
}

func testGet(t *testing.T, db *DB, m map[dbKey][]byte, owner rpc.UID, item string) {
	t.Helper()
	dbVal, err := db.Get(owner, item)
	if err != nil && err != sys.ErrNotFound {
		t.Errorf("failed to get %s.%s: %v", owner, item, err)
		return
	}
	mapVal := m[dbKey{Owner: owner, Item: item}]
	if !bytes.Equal(dbVal, mapVal) {
		t.Errorf("value mismatch %s.%s: %q != %q", owner, item, dbVal, mapVal)
	}
}

func testPut(t *testing.T, db *DB, m map[dbKey][]byte, owner rpc.UID, item string, new []byte) {
	t.Helper()
	dbOld, dbWritten, err := db.Put(owner, item, new)
	if err != nil {
		t.Errorf("failed to put %q to %s.%s: %v", new, owner, item, err)
		return
	}
	mapOld, ok := m[dbKey{Owner: owner, Item: item}]
	var mapWritten bool
	if !ok || !bytes.Equal(mapOld, new) {
		m[dbKey{Owner: owner, Item: item}] = bytes.Clone(new)
		mapWritten = true
	}
	if !bytes.Equal(dbOld, mapOld) {
		t.Errorf("value mismatch %s.%s: %q != %q", owner, item, dbOld, mapOld)
	}
	if dbWritten != mapWritten {
		t.Errorf("written mismatch %s.%s: %t != %t", owner, item, dbWritten, mapWritten)
	}
}

func testDrop(t *testing.T, db *DB, m map[dbKey][]byte, owner rpc.UID) {
	t.Helper()
	err := db.Drop(owner)
	if err != nil {
		t.Errorf("failed to drop %s: %v", owner, err)
		return
	}
	for k := range m {
		if k.Owner == owner {
			delete(m, k)
		}
	}
}

func testDropAll(t *testing.T, db *DB, m map[dbKey][]byte, module string) {
	t.Helper()
	err := db.DropModule(module)
	if err != nil {
		t.Errorf("failed to drop all %s: %v", module, err)
		return
	}
	for k := range m {
		if k.Owner.Module == module {
			delete(m, k)
		}
	}
}
