// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package store provides the worklog data storage layer.
package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
)

// DB is a persistent store.
type DB struct {
	name    string
	host    string
	mu      sync.Mutex
	store   *sql.DB
	roStore *sql.DB

	allow map[string]map[string]bool
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func txDone(tx *sql.Tx, err *error) {
	if *err == nil {
		*err = tx.Commit()
	} else {
		*err = errors.Join(*err, tx.Rollback())
	}
}

// Open opens an existing DB. See https://pkg.go.dev/modernc.org/sqlite#Driver.Open
// for name handling details. Two connections to the database are created, one
// with mode=rwc and one with mode=ro. Any mode in the provided name will be
// ignored. Open attempts to get the CNAME for the host, which may wait
// indefinitely, so a timeout context can be provided to fall back to the
// kernel-provided hostname.
func Open(ctx context.Context, name, host string) (*DB, error) {
	u, err := url.Parse(name)
	if err != nil {
		return nil, err
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, err
	}
	u.Scheme = "file"
	// URL URIs confuse SQLite. If the path is left in u.Path
	// the relative path is interpreted by SQLite as an absolute
	// path and will most likely end up being in a directory that
	// cannot be read or written to. This results in an "SQL logic
	// error: out of memory (1)".
	if u.Opaque == "" {
		u.Opaque = u.Path
		u.Path = ""
	}

	q.Set("mode", "rwc")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(Schema)
	if err != nil {
		return nil, err
	}
	if host == "" {
		host, err = hostname(ctx)
		if err != nil {
			return nil, errors.Join(err, db.Close())
		}
	}
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	dbRO, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &DB{name: u.Opaque, host: host, store: db, roStore: dbRO, allow: allow}, nil
}

// hostname returns the FQDN of the local host, falling back to the hostname
// reported by the kernel if CNAME lookup fails.
func hostname(ctx context.Context) (string, error) {
	host, err := os.Hostname()
	if err != nil {
		return "", err
	}
	cname, err := net.DefaultResolver.LookupCNAME(ctx, host)
	if err != nil {
		return host, nil
	}
	return strings.TrimSuffix(cname, "."), nil
}

// Name returns the name of the database as provided to Open.
func (db *DB) Name() string {
	if db == nil {
		return ""
	}
	return db.name
}

// Close closes the database.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return errors.Join(db.store.Close(), db.roStore.Close())
}

// Schema is the DB schema.
const Schema = `
create table if not exists buckets (
	rowid    INTEGER PRIMARY KEY AUTOINCREMENT,
	id       TEXT UNIQUE NOT NULL,
	name     TEXT,
	type     TEXT NOT NULL,
	client   TEXT NOT NULL,
	hostname TEXT NOT NULL,
	created  TEXT NOT NULL, -- unix micro
	datastr  TEXT NOT NULL  -- JSON text
);
create table if not exists events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	bucketrow INTEGER NOT NULL,
	starttime INTEGER NOT NULL, -- unix micro
	endtime   INTEGER NOT NULL, -- unix micro
	datastr   TEXT NOT NULL,    -- JSON text
	FOREIGN KEY (bucketrow) REFERENCES buckets(rowid)
);
create index if not exists event_index_id ON events(id);
create index if not exists event_index_starttime ON events(bucketrow, starttime);
create index if not exists event_index_endtime ON events(bucketrow, endtime);
pragma journal_mode=WAL;
`

// allow is the set of names allowed in dynamic queries.
var allow = map[string]map[string]bool{
	"buckets": {
		"rowid":    true,
		"id":       true,
		"name":     true,
		"type":     true,
		"client":   true,
		"hostname": true,
		"created":  true,
		"datastr":  true,
	},
	"events": {
		"id":        true,
		"bucketrow": true,
		"starttime": true,
		"endtime":   true,
		"datastr":   true,
	},
}

// BucketID returns the internal bucket ID for the provided bucket uid.
func (db *DB) BucketID(uid string) string {
	return fmt.Sprintf("%s_%s", uid, db.host)
}

const CreateBucket = `insert into buckets(id, name, type, client, hostname, created, datastr) values (?, ?, ?, ?, ?, ?, ?)`

// CreateBucket creates a new entry in the bucket table. If the entry already
// exists it will return an sqlite.Error with the code sqlite3.SQLITE_CONSTRAINT_UNIQUE.
// The SQL command run is [CreateBucket].
func (db *DB) CreateBucket(uid, name, typ, client string, created time.Time, data map[string]any) (m *worklog.BucketMetadata, err error) {
	bid := db.BucketID(uid)
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.store.Begin()
	if err != nil {
		return nil, err
	}
	defer txDone(tx, &err)
	return createBucket(tx, bid, name, typ, client, db.host, created, data)
}

func createBucket(tx *sql.Tx, bid, name, typ, client, host string, created time.Time, data map[string]any) (*worklog.BucketMetadata, error) {
	var (
		msg = []byte{} // datastr has a NOT NULL constraint.
		err error
	)
	if data != nil {
		msg, err = json.Marshal(data)
		if err != nil {
			return nil, err
		}
	}
	_, err = tx.Exec(CreateBucket, bid, name, typ, client, host, created.Format(time.RFC3339Nano), msg)
	var sqlErr *sqlite.Error
	if errors.As(err, &sqlErr) && sqlErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
		return nil, err
	}
	m, err := bucketMetadata(tx, bid)
	if err != nil {
		return nil, err
	}
	if sqlErr != nil {
		return m, sqlErr
	}
	return m, nil
}

const BucketMetadata = `select id, name, type, client, hostname, created, datastr from buckets where id = ?`

// BucketMetadata returns the metadata for the bucket with the provided internal
// bucket ID.
// The SQL command run is [BucketMetadata].
func (db *DB) BucketMetadata(bid string) (*worklog.BucketMetadata, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return bucketMetadata(db.store, bid)
}

func bucketMetadata(db querier, bid string) (*worklog.BucketMetadata, error) {
	rows, err := db.Query(BucketMetadata, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, io.EOF
	}
	var (
		m       worklog.BucketMetadata
		created string
		msg     []byte
	)
	err = rows.Scan(&m.ID, &m.Name, &m.Type, &m.Client, &m.Hostname, &created, &msg)
	if err != nil {
		return nil, err
	}
	m.Created, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return &m, err
	}
	if len(msg) != 0 {
		err = json.Unmarshal(msg, &m.Data)
		if err != nil {
			return &m, err
		}
	}
	if rows.Next() {
		return &m, errors.New("unexpected item")
	}
	return &m, rows.Close()
}

const InsertEvent = `insert into events(bucketrow, starttime, endtime, datastr) values ((select rowid from buckets where id = ?), ?, ?, ?)`

// InsertEvent inserts a new event into the events table.
// The SQL command run is [InsertEvent].
func (db *DB) InsertEvent(e *worklog.Event) (sql.Result, error) {
	bid := fmt.Sprintf("%s_%s", e.Bucket, db.host)
	db.mu.Lock()
	defer db.mu.Unlock()
	return insertEvent(db.store, bid, e)
}

func insertEvent(db execer, bid string, e *worklog.Event) (sql.Result, error) {
	msg, err := json.Marshal(e.Data)
	if err != nil {
		return nil, err
	}
	return db.Exec(InsertEvent, bid, e.Start.Format(time.RFC3339Nano), e.End.Format(time.RFC3339Nano), msg)
}

const UpdateEvent = `update events set starttime = ?, endtime = ?, datastr = ? where id = ? and bucketrow = (
	select rowid from buckets where id = ?
)`

// UpdateEvent updates the event in the store corresponding to the provided
// event.
// The SQL command run is [UpdateEvent].
func (db *DB) UpdateEvent(e *worklog.Event) (sql.Result, error) {
	msg, err := json.Marshal(e.Data)
	if err != nil {
		return nil, err
	}
	bid := fmt.Sprintf("%s_%s", e.Bucket, db.host)
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.store.Exec(UpdateEvent, e.Start.Format(time.RFC3339Nano), e.End.Format(time.RFC3339Nano), msg, e.ID, bid)
}

const LastEvent = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?1
) and datetime(endtime) = (
	select max(datetime(endtime)) from events where bucketrow = (
		select rowid from buckets where id = ?1
	) limit 1
) limit 1`

// LastEvent returns the last event in the named bucket.
// The SQL command run is [LastEvent].
func (db *DB) LastEvent(uid string) (*worklog.Event, error) {
	bid := db.BucketID(uid)
	db.mu.Lock()
	rows, err := db.store.Query(LastEvent, bid)
	db.mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, io.EOF
	}
	var (
		e worklog.Event

		start, end string
		msg        []byte
	)
	err = rows.Scan(&e.ID, &start, &end, &msg)
	if err != nil {
		return nil, err
	}
	e.Start, err = time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return &e, err
	}
	e.End, err = time.Parse(time.RFC3339Nano, end)
	if err != nil {
		return &e, err
	}
	if len(msg) != 0 {
		err = json.Unmarshal(msg, &e.Data)
		if err != nil {
			return &e, err
		}
	}
	if rows.Next() {
		return &e, errors.New("unexpected item")
	}
	return &e, rows.Close()
}

// Dump dumps the complete database into a slice of [worklog.BucketMetadata].
func (db *DB) Dump() ([]worklog.BucketMetadata, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	m, err := db.buckets()
	if err != nil {
		return nil, err
	}
	for i, b := range m {
		bucket, ok := strings.CutSuffix(b.ID, "_"+b.Hostname)
		if !ok {
			return m, fmt.Errorf("invalid bucket ID at %d: %s", i, b.ID)
		}
		e, err := db.events(b.ID)
		if err != nil {
			return m, err
		}
		for j := range e {
			e[j].Bucket = bucket
		}
		m[i].Events = e
	}
	return m, nil
}

// DumpRange dumps the database spanning the specified time range into a slice
// of [worklog.BucketMetadata].
func (db *DB) DumpRange(start, end time.Time) ([]worklog.BucketMetadata, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	m, err := db.buckets()
	if err != nil {
		return nil, err
	}
	for i, b := range m {
		bucket, ok := strings.CutSuffix(b.ID, "_"+b.Hostname)
		if !ok {
			return m, fmt.Errorf("invalid bucket ID at %d: %s", i, b.ID)
		}
		e, err := db.dumpEventsRange(b.ID, start, end, -1)
		if err != nil {
			return m, err
		}
		for j := range e {
			e[j].Bucket = bucket
		}
		m[i].Events = e
	}
	return m, nil
}

const (
	dumpEventsRange = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and datetime(endtime) >= datetime(?) and datetime(starttime) <= datetime(?) limit ?`

	dumpEventsRangeUntil = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and datetime(starttime) <= datetime(?) limit ?`

	dumpEventsRangeFrom = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and datetime(endtime) >= datetime(?) limit ?`

	dumpEventsLimit = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) limit ?`
)

func (db *DB) dumpEventsRange(bid string, start, end time.Time, limit int) ([]worklog.Event, error) {
	var e []worklog.Event
	err := db.eventsRangeFunc(bid, start, end, limit, func(m worklog.Event) error {
		e = append(e, m)
		return nil
	}, false)
	return e, err
}

// Load loads a complete database from a slice of [worklog.BucketMetadata].
// Event IDs will be regenerated by the backing database and so will not
// match the input data. If replace is true and a bucket already exists matching
// the bucket in the provided buckets slice, the existing events will be
// deleted and replaced. If replace is false, the new events will be added to
// the existing events in the store.
func (db *DB) Load(buckets []worklog.BucketMetadata, replace bool) (err error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	tx, err := db.store.Begin()
	if err != nil {
		return err
	}
	defer txDone(tx, &err)
	for _, m := range buckets {
		var b *worklog.BucketMetadata
		b, err = createBucket(tx, m.ID, m.Name, m.Type, m.Client, m.Hostname, m.Created, m.Data)
		if err != nil {
			var sqlErr *sqlite.Error
			if errors.As(err, &sqlErr) && sqlErr.Code() != sqlite3.SQLITE_CONSTRAINT_UNIQUE {
				return err
			}
			if !sameBucket(&m, b) {
				return err
			}
			if replace {
				_, err = tx.Exec(DeleteBucketEvents, m.ID)
				if err != nil {
					return err
				}
			}
		}
		for i, e := range m.Events {
			bid := fmt.Sprintf("%s_%s", e.Bucket, m.Hostname)
			_, err = insertEvent(tx, bid, &m.Events[i])
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func sameBucket(a, b *worklog.BucketMetadata) bool {
	return a.ID == b.ID &&
		a.Name == b.Name &&
		a.Type == b.Type &&
		a.Client == b.Client &&
		a.Hostname == b.Hostname
}

const Buckets = `select id, name, type, client, hostname, created, datastr from buckets`

// Buckets returns the full set of bucket metadata.
// The SQL command run is [Buckets].
func (db *DB) Buckets() ([]worklog.BucketMetadata, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.buckets()
}

func (db *DB) buckets() ([]worklog.BucketMetadata, error) {
	rows, err := db.store.Query(Buckets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var b []worklog.BucketMetadata
	for rows.Next() {
		var (
			m       worklog.BucketMetadata
			msg     []byte
			created string
		)
		err = rows.Scan(&m.ID, &m.Name, &m.Type, &m.Client, &m.Hostname, &created, &msg)
		if err != nil {
			return nil, err
		}
		m.Created, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return b, err
		}
		if len(msg) != 0 {
			err = json.Unmarshal(msg, &m.Data)
			if err != nil {
				return b, err
			}
		}
		b = append(b, m)
	}
	return b, rows.Close()
}

const Event = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and id = ? limit 1`

const Events = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
)`

// Buckets returns the full set of events in the bucket with the provided
// internal bucket ID.
// The SQL command run is [Events].
func (db *DB) Events(bid string) ([]worklog.Event, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.events(bid)
}

func (db *DB) events(bid string) ([]worklog.Event, error) {
	rows, err := db.store.Query(Events, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var e []worklog.Event
	for rows.Next() {
		var (
			m worklog.Event

			start, end string
			msg        []byte
		)
		err = rows.Scan(&m.ID, &start, &end, &msg)
		if err != nil {
			return nil, err
		}
		m.Start, err = time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return e, err
		}
		m.End, err = time.Parse(time.RFC3339Nano, end)
		if err != nil {
			return e, err
		}
		if len(msg) != 0 {
			err = json.Unmarshal(msg, &m.Data)
			if err != nil {
				return e, err
			}
		}
		e = append(e, m)
	}
	return e, rows.Close()
}

const (
	EventsRange = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and datetime(endtime) >= datetime(?) and datetime(starttime) <= datetime(?) order by datetime(endtime) desc limit ?`

	EventsRangeUntil = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and datetime(starttime) <= datetime(?) order by datetime(endtime) desc limit ?`

	EventsRangeFrom = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) and datetime(endtime) >= datetime(?) order by datetime(endtime) desc limit ?`

	EventsLimit = `select id, starttime, endtime, datastr from events where bucketrow = (
	select rowid from buckets where id = ?
) order by datetime(endtime) desc limit ?`
)

// EventsRange returns the events in the bucket with the provided bucket ID
// within the specified time range, sorted descending by end time.
// The SQL command run is [EventsRange], [EventsRangeUntil], [EventsRangeFrom]
// or [EventsLimit] depending on whether start and end are zero.
func (db *DB) EventsRange(bid string, start, end time.Time, limit int) ([]worklog.Event, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	var e []worklog.Event
	err := db.eventsRangeFunc(bid, start, end, limit, func(m worklog.Event) error {
		e = append(e, m)
		return nil
	}, true)
	return e, err
}

// EventsRange calls fn on all the events in the bucket with the provided
// bucket ID within the specified time range, sorted descending by end time.
// The SQL command run is [EventsRange], [EventsRangeUntil], [EventsRangeFrom]
// or [EventsLimit] depending on whether start and end are zero.
func (db *DB) EventsRangeFunc(bid string, start, end time.Time, limit int, fn func(worklog.Event) error) error {
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.eventsRangeFunc(bid, start, end, limit, fn, true)
}

func (db *DB) eventsRangeFunc(bid string, start, end time.Time, limit int, fn func(worklog.Event) error, order bool) error {
	var (
		query string
		rows  *sql.Rows
		err   error
	)
	switch {
	case !start.IsZero() && !end.IsZero():
		query = EventsRange
		if !order {
			query = dumpEventsRange
		}
		rows, err = db.store.Query(query, bid, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), limit)
	case !start.IsZero():
		query = EventsRangeFrom
		if !order {
			query = dumpEventsRangeFrom
		}
		rows, err = db.store.Query(query, bid, start.Format(time.RFC3339Nano), limit)
	case !end.IsZero():
		query = EventsRangeUntil
		if !order {
			query = dumpEventsRangeUntil
		}
		rows, err = db.store.Query(query, bid, end.Format(time.RFC3339Nano), limit)
	default:
		query = EventsLimit
		if !order {
			query = dumpEventsLimit
		}
		rows, err = db.store.Query(query, bid, limit)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			m worklog.Event

			start, end string
			msg        []byte
		)
		err = rows.Scan(&m.ID, &start, &end, &msg)
		if err != nil {
			return err
		}
		m.Start, err = time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return err
		}
		m.End, err = time.Parse(time.RFC3339Nano, end)
		if err != nil {
			return err
		}
		if len(msg) != 0 {
			err = json.Unmarshal(msg, &m.Data)
			if err != nil {
				return err
			}
		}
		err = fn(m)
		if err != nil {
			return err
		}
	}
	return rows.Close()
}

// Select allows running an SQLite SELECT query. The query is run on a read-only
// connection to the database.
func (db *DB) Select(query string) ([]map[string]any, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	rows, err := db.roStore.Query(query)
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
			typ := t.ScanType()
			if typ == nil {
				var v any
				typ = reflect.TypeOf(&v).Elem()
			}
			cols[i] = reflect.New(reflect.PointerTo(typ)).Interface()
		}
		err = rows.Scan(cols...)
		if err != nil {
			return nil, err
		}
		result := make(map[string]any)
		for i := range cols {
			elem := reflect.ValueOf(cols[i]).Elem()
			if elem.IsNil() {
				result[types[i].Name()] = nil
			} else {
				result[types[i].Name()] = elem.Elem().Interface()
			}
		}
		e = append(e, result)
	}
	return e, rows.Err()
}

const AmendEvents = `begin transaction;
	-- ensure we have an amend array.
	update events set datastr = json_insert(datastr, '$.amend', json('[]'))
	where
		datetime(starttime) < datetime(?5) and datetime(endtime) > datetime(?4) and bucketrow = (
			select rowid from buckets where id = ?1
		);
	update events set datastr = json_insert(datastr, '$.amend[#]', json_object('time', ?2, 'msg', ?3, 'replace', (
		-- trim amendments down to original event bounds.
		select json_group_array(json_replace(value,
			'$.start', case
				when datetime(starttime) > datetime(json_extract(value, '$.start')) then
					starttime
				else
					json_extract(value, '$.start')
				end,
			'$.end', case
				when datetime(endtime) < datetime(json_extract(value, '$.end')) then
					endtime
				else
					json_extract(value, '$.end')
				end
		))
		from
			json_each(?6)
		where
			datetime(json_extract(value, '$.start')) < datetime(endtime) and datetime(json_extract(value, '$.end')) > datetime(starttime)
	)))
	where
		datetime(starttime) < datetime(?5) and datetime(endtime) > datetime(?4) and bucketrow = (
			select rowid from buckets where id = ?1
		);
commit;`

// AmendEvents adds amendment notes to the data for events in the store
// overlapping the note. On return the note.Replace slice will be sorted.
//
// The SQL command run is [AmendEvents].
func (db *DB) AmendEvents(ts time.Time, note *worklog.Amendment) (sql.Result, error) {
	if len(note.Replace) == 0 {
		return driver.RowsAffected(0), nil
	}
	sort.Slice(note.Replace, func(i, j int) bool {
		return note.Replace[i].Start.Before(note.Replace[j].Start)
	})
	start := note.Replace[0].Start
	end := note.Replace[0].End
	for i, r := range note.Replace[1:] {
		if note.Replace[i].End.After(r.Start) {
			return nil, fmt.Errorf("overlapping replacements: [%d].end (%s) is after [%d].start (%s)",
				i, note.Replace[i].End.Format(time.RFC3339), i+1, r.Start.Format(time.RFC3339))
		}
		if r.End.After(end) {
			end = r.End
		}
	}
	replace, err := json.Marshal(note.Replace)
	if err != nil {
		return nil, err
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	return db.store.Exec(AmendEvents, db.BucketID(note.Bucket), ts.Format(time.RFC3339Nano), note.Message, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), replace)
}

const DeleteEvent = `delete from events where bucketrow = (
	select rowid from buckets where id = ?
) and id = ?`

const DeleteBucketEvents = `delete from events where bucketrow in (
	select rowid from buckets where id = ?
)`

const DeleteBucket = `delete from buckets where id = ?`
