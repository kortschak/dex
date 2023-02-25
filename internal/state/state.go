// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package state provides state persistence.
package state

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"

	// For sql.DB registration.
	_ "modernc.org/sqlite"
)

// DB is a persistent state store.
type DB struct {
	mu    sync.Mutex
	store *sql.DB
	log   *slog.Logger
}

// Schema is the DB schema. Module and service columns correspond to rpc.UID.
const Schema = `
create table if not exists state(
	module  TEXT NOT NULL,
	service TEXT NOT NULL,
	item    TEXT NOT NULL,
	value   BLOB NOT NULL,
	PRIMARY KEY(module, service, item)
);
`

const (
	upsert = `
insert into state values(?, ?, ?, ?)
  on conflict do update set value=?;
`

	get = `
select value from state where module is ? and service is ? and item is ?;
`

	getAll = `
select item, value from state where module is ? and service is ?;
`

	delet = `
delete from state where module is ? and service is ? and item is ?;
`

	drop = `
delete from state where module is ? and service is ?;
`

	dropModule = `
delete from state where module is ?;
`

	dumpModule = `
select * from state where module is ?;
`

	dump = `
select * from state;
`
)

var kernelUID = rpc.UID{Module: "kernel", Service: "db"}

// Open opens a DB, creating the tables if required.
// See https://pkg.go.dev/modernc.org/sqlite#Driver.Open for name handling
// details.
func Open(name string, log *slog.Logger) (*DB, error) {
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(Schema)
	if err != nil {
		return nil, err
	}
	return &DB{store: db, log: log.With(slog.String("component", kernelUID.String()))}, nil
}

type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

// Set sets the owner's named item to the provided value.
func (db *DB) Set(owner rpc.UID, item string, val []byte) error {
	ctx := context.Background()
	key := dbKey{owner, item}
	db.log.LogAttrs(ctx, slog.LevelDebug, "set", slog.Any("key", key), slog.Any("val", val))
	db.mu.Lock()
	err := db.set(db.store, owner, item, val)
	db.mu.Unlock()
	if err != nil {
		db.log.LogAttrs(ctx, slog.LevelError, "set", slog.Any("key", key), slog.Any("error", err))
	}
	return err
}

func (*DB) set(db querier, owner rpc.UID, item string, val []byte) error {
	_, err := db.Exec(upsert, nilEmpty(owner.Module), owner.Service, nilEmpty(item), val, val)
	return err
}

// Get returns the owner's named item. Get returns sys.ErrNotFound if no item
// is found.
func (db *DB) Get(owner rpc.UID, item string) (val []byte, err error) {
	ctx := context.Background()
	key := dbKey{owner, item}
	db.log.LogAttrs(ctx, slog.LevelDebug, "get", slog.Any("key", key))
	db.mu.Lock()
	val, err = db.get(db.store, owner, item)
	db.mu.Unlock()
	if err != nil && err != sys.ErrNotFound {
		db.log.LogAttrs(ctx, slog.LevelError, "get", slog.Any("key", key), slog.Any("error", err))
	}
	return val, err
}

func (*DB) get(db querier, owner rpc.UID, item string) ([]byte, error) {
	rows, err := db.Query(get, owner.Module, owner.Service, item)
	if err != nil {
		return nil, err
	}
	if !rows.Next() {
		return nil, sys.ErrNotFound
	}
	var val []byte
	err = rows.Scan(&val)
	if err != nil {
		return nil, err
	}
	if rows.Next() {
		return val, errors.New("unexpected item")
	}
	rows.Close()
	return val, rows.Err()
}

// GetAll returns all the owner's items.
func (db *DB) GetAll(owner rpc.UID) (vals map[string][]byte, err error) {
	ctx := context.Background()
	db.log.LogAttrs(ctx, slog.LevelDebug, "get all", slog.Any("key", owner))
	db.mu.Lock()
	vals, err = db.getAll(db.store, owner)
	db.mu.Unlock()
	if err != nil {
		db.log.LogAttrs(ctx, slog.LevelError, "get all", slog.Any("key", owner), slog.Any("error", err))
	}
	return vals, err
}

func (*DB) getAll(db querier, owner rpc.UID) (map[string][]byte, error) {
	rows, err := db.Query(getAll, owner.Module, owner.Service)
	if err != nil {
		return nil, err
	}
	var (
		item string
		val  []byte
	)
	vals := make(map[string][]byte)
	for rows.Next() {
		err = rows.Scan(&item, &val)
		if err != nil {
			return nil, err
		}
		vals[item] = val
	}
	rows.Close()
	return vals, rows.Err()
}

// Put returns the owner's named item and sets it to the new provided value if
// the values differ. It returns whether a write was performed.
func (db *DB) Put(owner rpc.UID, item string, new []byte) (old []byte, written bool, err error) {
	ctx := context.Background()
	key := dbKey{owner, item}
	db.log.LogAttrs(ctx, slog.LevelDebug, "put", slog.Any("key", key), slog.Any("val", new))
	db.mu.Lock()
	defer func() {
		db.mu.Unlock()
		if err != nil {
			db.log.LogAttrs(ctx, slog.LevelError, "put", slog.Any("key", owner), slog.Any("error", err))
		}
	}()
	tx, err := db.store.Begin()
	if err != nil {
		return old, written, err
	}
	old, err = db.get(tx, owner, item)
	if err != nil {
		return old, written, tx.Rollback()
	}
	err = db.set(tx, owner, item, new)
	if err != nil {
		return old, written, tx.Rollback()
	}
	return old, !bytes.Equal(old, new), tx.Commit()
}

// Delete removes the owner's named item.
func (db *DB) Delete(owner rpc.UID, item string) error {
	ctx := context.Background()
	key := dbKey{owner, item}
	db.log.LogAttrs(ctx, slog.LevelDebug, "delete", slog.Any("key", key))
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.store.Query(delet, owner.Module, owner.Service, item)
	if err != nil {
		db.log.LogAttrs(ctx, slog.LevelError, "delete", slog.Any("key", key), slog.Any("error", err))
	}
	return err
}

// Drop deletes all entries owned by the owner.
func (db *DB) Drop(owner rpc.UID) error {
	ctx := context.Background()
	db.log.LogAttrs(ctx, slog.LevelDebug, "drop", slog.Any("key", owner))
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.store.Query(drop, owner.Module, owner.Service)
	if err != nil {
		db.log.LogAttrs(ctx, slog.LevelError, "delete", slog.Any("key", owner), slog.Any("error", err))
	}
	return err
}

// DropModule deletes all entries owned by the module.
func (db *DB) DropModule(module string) error {
	ctx := context.Background()
	db.log.LogAttrs(ctx, slog.LevelDebug, "drop module", slog.String("key", module))
	db.mu.Lock()
	defer db.mu.Unlock()
	_, err := db.store.Query(dropModule, module)
	if err != nil {
		db.log.LogAttrs(ctx, slog.LevelError, "delete", slog.Any("key", module), slog.Any("error", err))
	}
	return err
}

// DumpModule returns a Go map with all the module's items.
func (db *DB) DumpModule(module string) (map[rpc.UID]map[string][]byte, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.dump(dumpModule, module)
}

// Dump returns a Go map with the contents of the database.
func (db *DB) Dump() (map[rpc.UID]map[string][]byte, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.dump(dump)
}

func (db *DB) dump(query string, args ...any) (map[rpc.UID]map[string][]byte, error) {
	rows, err := db.store.Query(query, args...)
	if err != nil {
		return nil, err
	}
	var (
		module,
		service,
		item string
		val []byte
	)
	d := make(map[rpc.UID]map[string][]byte)
	for rows.Next() {
		var svc *string
		rows.Scan(&module, &svc, &item, &val)
		if svc != nil {
			service = *svc
		}
		key := rpc.UID{Module: module, Service: service}
		m := d[key]
		if m == nil {
			m = make(map[string][]byte)
			d[key] = m
		}
		m[item] = val
	}
	rows.Close()
	return d, rows.Err()
}

// JSON returns a JSON representation of a DB map dump returned by Dump or
// DumpModule.
func JSON(db map[rpc.UID]map[string][]byte) ([]byte, error) {
	d := make(map[string]map[string][]byte)
	for owner, items := range db {
		d[owner.String()] = items
	}
	return json.Marshal(d)
}

// Close closes the database.
func (db *DB) Close() error {
	return db.store.Close()
}

func nilEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

type dbKey struct {
	Owner rpc.UID `json:"owner"`
	Item  string  `json:"item"`
}

func (k dbKey) marshal() (string, error) {
	b, err := json.Marshal(k)
	return string(b), err
}
