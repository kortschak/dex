// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package pgstore provides a worklog data storage layer using PostgreSQL.
package pgstore

import (
	"bufio"
	"bytes"
	"compress/gzip"
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
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sys/execabs"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
)

// DB is a persistent store.
type DB struct {
	name    string
	host    string
	store   *pgx.Conn
	roStore *pgx.Conn

	roWarn error
}

type querier interface {
	Query(ctx context.Context, query string, args ...any) (pgx.Rows, error)
}

type result struct {
	n      int64
	lastID int64
}

func (r result) RowsAffected() (int64, error) {
	return r.n, nil
}

func (r result) LastInsertId() (int64, error) {
	return r.lastID, nil
}

func txDone(ctx context.Context, tx pgx.Tx, err *error) {
	if *err == nil {
		*err = tx.Commit(ctx)
	} else {
		*err = errors.Join(*err, tx.Rollback(ctx))
	}
}

// Open opens a PostgresSQL DB. See [pgx.Connect] for name handling details.
// Two connections to the database are created, one using the username within
// the name parameter and one with the same username, but "_ro" appended. If
// the second user does not have SELECT role_table_grants for the buckets' and
// 'events' the database, or has any non-SELECT role_table_grants, the second
// connection will be closed and a [Warning] error will be returned. If the
// second connection is closed, the [DB.Select] method will return a non-nil
// error and perform no DB operation. Open attempts to get the CNAME for the
// host, which may wait indefinitely, so a timeout context can be provided to
// fall back to the kernel-provided hostname.
func Open(ctx context.Context, name, host string) (*DB, error) {
	u, err := url.Parse(name)
	if err != nil {
		return nil, err
	}
	if u.User == nil {
		return nil, fmt.Errorf("missing user info: %s", u)
	}

	if host == "" {
		host, err = hostname(ctx)
		if err != nil {
			return nil, err
		}
	}

	pgHost, pgPort, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, err
	}
	if _, ok := u.User.Password(); !ok {
		userInfo, err := pgUserinfo(u.User.Username(), pgHost, pgPort, os.Getenv("PGPASSWORD"))
		if err != nil {
			return nil, err
		}
		u.User = userInfo
	}

	db, err := pgx.Connect(ctx, name)
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(ctx, Schema)
	if err != nil {
		return nil, errors.Join(err, db.Close(ctx))
	}

	var dbRO *pgx.Conn
	roUser := u.User.Username() + "_ro"
	userInfo, warn := pgUserinfo(roUser, pgHost, pgPort, "")
	if warn != nil {
		warn = Warning{error: fmt.Errorf("ro user failed to get password: %w", warn)}
	} else {
		u.User = userInfo

		// Check that the ro user can read the stores tables
		// and has no other grants. If either of these checks
		// fail, return a Warning error. If the user has other
		// grants, deny its use.
		const (
			nonSelects = `select
				count(*) = 0 
			from
				information_schema.role_table_grants 
			where
				not privilege_type ilike 'SELECT'
				and grantee = $1`
			otherSelects = `select
				count(distinct table_name) = 0
			from
				information_schema.role_table_grants
			where
				privilege_type ilike 'SELECT' and not table_name in ('buckets', 'events')
				and grantee = $1`
			selects = `select
				count(distinct table_name) = 2
			from
				information_schema.role_table_grants
			where
				privilege_type ilike 'SELECT' and table_name in ('buckets', 'events')
				and grantee = $1`
		)
		for _, check := range []struct {
			statement string
			warn      Warning
		}{
			{
				statement: nonSelects,
				warn: Warning{
					error: errors.New("ro user failed capability restriction checks"),
					allow: false,
				},
			},
			{
				statement: otherSelects,
				warn: Warning{
					error: errors.New("ro user failed table read capability restriction checks"),
					allow: false,
				},
			},
			{
				statement: selects,
				warn: Warning{
					error: errors.New("ro user failed read capability checks"),
					allow: true,
				},
			},
		} {
			var ok bool
			err = db.QueryRow(ctx, check.statement, roUser).Scan(&ok)
			if err != nil {
				db.Close(ctx)
				return nil, err
			}
			if !ok {
				warn = check.warn
				break
			}
		}
		if w, ok := warn.(Warning); err == nil || (ok || w.allow) {
			dbRO, err = pgx.Connect(ctx, u.String())
			if err != nil {
				warn = Warning{error: err}
				dbRO = nil
			}
		}
	}
	u.User = nil

	return &DB{name: u.String(), host: host, store: db, roStore: dbRO, roWarn: warn}, warn
}

func pgUserinfo(pgUser, pgHost, pgPort, pgPassword string) (*url.Userinfo, error) {
	if pgPassword != "" {
		return url.UserPassword(pgUser, pgPassword), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not get home directory: %w", err)
	}
	pgpass, err := os.Open(filepath.Join(home, ".pgpass"))
	if err != nil {
		return nil, fmt.Errorf("could not open .pgpass: %w", err)
	}
	defer pgpass.Close()
	fi, err := pgpass.Stat()
	if err != nil {
		return nil, fmt.Errorf("could not stat .pgpass: %v", err)
	}
	if fi.Mode()&0o077 != 0o000 {
		return nil, fmt.Errorf(".pgpass permissions too relaxed: %s", fi.Mode())
	}
	sc := bufio.NewScanner(pgpass)
	found := false
	var e pgPassEntry
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		e, err = parsePgPassLine(line)
		if err != nil {
			return nil, fmt.Errorf("could not parse .pgpass: %w", err)
		}
		if e.match(pgUser, pgHost, pgPort, "*") {
			found = true
			break
		}
	}
	if sc.Err() != nil {
		return nil, fmt.Errorf("unexpected error reading .pgpass: %w", err)
	}
	if !found {
		return nil, errors.New("must have postgres password in $PGPASSWORD or .pgpass")
	}

	return url.UserPassword(pgUser, e.password), nil
}

type pgPassEntry struct {
	host     string
	port     string
	database string
	user     string
	password string
}

func (e pgPassEntry) match(user, host, port, database string) bool {
	return user == e.user &&
		(host == e.host || e.host == "*") &&
		(port == e.port || e.port == "*") &&
		(database == e.database || e.database == "*")
}

func parsePgPassLine(text string) (pgPassEntry, error) {
	var (
		entry  pgPassEntry
		field  int
		last   int
		escape bool
	)
	for i, r := range text {
		switch r {
		case '\\':
			escape = !escape
			continue
		case ':':
			if escape {
				break
			}
			switch field {
			case 0:
				entry.host = text[last:i]
			case 1:
				entry.port = text[last:i]
			case 2:
				entry.database = text[last:i]
			case 3:
				entry.user = text[last:i]
			default:
				return entry, errors.New("too many fields")
			}
			last = i + 1
			field++
		}
		escape = false
	}
	entry.password = text[last:]
	return entry, nil
}

// Warning is a warning-only error.
type Warning struct {
	error
	allow bool
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

// Backup creates a backup of the DB using the pg_dump command into the provided
// directory as a gzip file. It returns the path of the backup.
func (db *DB) Backup(ctx context.Context, dir string) (string, error) {
	u, err := url.Parse(db.name)
	if err != nil {
		return "", err
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		return "", err
	}
	dbname := path.Base(u.Path)

	dst := filepath.Join(dir, dbname+"_"+time.Now().In(time.UTC).Format("20060102150405")+".gz")
	cmd := execabs.Command("pg_dump", "-h", host, "-p", port, dbname)
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	w := gzip.NewWriter(f)
	cmd.Stdout = w
	var buf bytes.Buffer
	cmd.Stderr = &buf
	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s: %w", bytes.TrimSpace(buf.Bytes()), err)
	}
	err = errors.Join(w.Close(), f.Sync(), f.Close())
	return dst, err
}

// Close closes the database.
func (db *DB) Close(ctx context.Context) error {
	var roErr error
	if db.roStore != nil {
		roErr = db.roStore.Close(ctx)
	}
	return errors.Join(db.store.Close(ctx), roErr)
}

// Schema is the DB schema.
const Schema = `
create table if not exists buckets (
	rowid    SERIAL PRIMARY KEY,
	id       TEXT UNIQUE NOT NULL,
	name     TEXT NOT NULL,
	type     TEXT NOT NULL,
	client   TEXT NOT NULL,
	hostname TEXT NOT NULL,
	created  TIMESTAMP WITH TIME ZONE NOT NULL,
	timezone TEXT NOT NULL, -- tz of created, not mutated after first write
	datastr  JSONB NOT NULL
);
create table if not exists events (
	id        SERIAL PRIMARY KEY,
	bucketrow INTEGER NOT NULL,
	starttime TIMESTAMP WITH TIME ZONE NOT NULL,
	endtime   TIMESTAMP WITH TIME ZONE NOT NULL,
	timezone  TEXT NOT NULL, -- tz of starttime, not mutated after first write
	datastr   JSONB NOT NULL,
	FOREIGN KEY (bucketrow) REFERENCES buckets(rowid)
);
create index if not exists event_index_id ON events(id);
create index if not exists event_index_starttime ON events(bucketrow, starttime);
create index if not exists event_index_endtime ON events(bucketrow, endtime);
`

const tzFormat = "-07:00"

// BucketID returns the internal bucket ID for the provided bucket uid.
func (db *DB) BucketID(uid string) string {
	return fmt.Sprintf("%s_%s", uid, db.host)
}

const CreateBucket = `insert into buckets(id, name, type, client, hostname, created, timezone, datastr) values ($1, $2, $3, $4, $5, $6, $7, $8)
on conflict (id) do nothing;`

// CreateBucket creates a new entry in the bucket table.
// The SQL command run is [CreateBucket].
func (db *DB) CreateBucket(ctx context.Context, uid, name, typ, client string, created time.Time, data map[string]any) (m *worklog.BucketMetadata, err error) {
	bid := db.BucketID(uid)
	tx, err := db.store.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer txDone(ctx, tx, &err)
	return createBucket(ctx, tx, bid, name, typ, client, db.host, created, data)
}

func createBucket(ctx context.Context, tx pgx.Tx, bid, name, typ, client, host string, created time.Time, data map[string]any) (*worklog.BucketMetadata, error) {
	if data == nil {
		// datastr has a NOT NULL constraint.
		data = make(map[string]any)
	}
	_, err := tx.Exec(ctx, CreateBucket, bid, name, typ, client, host, created, created.Format(tzFormat), data)
	if err != nil {
		return nil, err
	}
	m, err := bucketMetadata(ctx, tx, bid)
	if err != nil {
		return nil, err
	}
	return m, nil
}

const BucketMetadata = `select id, name, type, client, hostname, created, timezone, datastr from buckets where id = $1`

// BucketMetadata returns the metadata for the bucket with the provided internal
// bucket ID.
// The SQL command run is [BucketMetadata].
func (db *DB) BucketMetadata(ctx context.Context, bid string) (*worklog.BucketMetadata, error) {
	return bucketMetadata(ctx, db.store, bid)
}

func bucketMetadata(ctx context.Context, db querier, bid string) (*worklog.BucketMetadata, error) {
	rows, err := db.Query(ctx, BucketMetadata, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, io.EOF
	}
	var (
		m  worklog.BucketMetadata
		tz string
	)
	err = rows.Scan(&m.ID, &m.Name, &m.Type, &m.Client, &m.Hostname, &m.Created, &tz, &m.Data)
	if err != nil {
		return nil, err
	}
	timezone, err := time.Parse(tzFormat, tz)
	if err != nil {
		return &m, fmt.Errorf("invalid timezone for %s bucket %s: %w", m.Name, m.ID, err)
	}
	m.Created = m.Created.In(timezone.Location())
	if rows.Next() {
		return &m, errors.New("unexpected item")
	}
	return &m, nil
}

const InsertEvent = `insert into events(bucketrow, starttime, endtime, timezone, datastr) values ((select rowid from buckets where id = $1), $2, $3, $4, $5) returning id`

// InsertEvent inserts a new event into the events table.
// The SQL command run is [InsertEvent].
func (db *DB) InsertEvent(ctx context.Context, e *worklog.Event) (sql.Result, error) {
	bid := fmt.Sprintf("%s_%s", e.Bucket, db.host)
	return insertEvent(ctx, db.store, bid, e)
}

func insertEvent(ctx context.Context, db querier, bid string, e *worklog.Event) (sql.Result, error) {
	rows, err := db.Query(ctx, InsertEvent, bid, e.Start, e.End, e.Start.Format(tzFormat), e.Data)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res result
	for rows.Next() {
		res.n++
		err = rows.Scan(&res.lastID)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

const UpdateEvent = `update events set starttime = $1, endtime = $2, datastr = $3 where id = $4 and bucketrow = (
	select rowid from buckets where id = $5
) returning id`

// UpdateEvent updates the event in the store corresponding to the provided
// event.
// The SQL command run is [UpdateEvent].
func (db *DB) UpdateEvent(ctx context.Context, e *worklog.Event) (sql.Result, error) {
	bid := fmt.Sprintf("%s_%s", e.Bucket, db.host)
	rows, err := db.store.Query(ctx, UpdateEvent, e.Start, e.End, e.Data, e.ID, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var res result
	for rows.Next() {
		res.n++
		err = rows.Scan(&res.lastID)
		if err != nil {
			return nil, err
		}
	}
	return res, nil
}

const LastEvent = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and endtime = (
	select max(endtime) from events where bucketrow = (
		select rowid from buckets where id = $1
	) limit 1
) limit 1`

// LastEvent returns the last event in the named bucket.
// The SQL command run is [LastEvent].
func (db *DB) LastEvent(ctx context.Context, uid string) (*worklog.Event, error) {
	bid := db.BucketID(uid)
	rows, err := db.store.Query(ctx, LastEvent, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, io.EOF
	}
	var (
		e  worklog.Event
		tz string
	)
	err = rows.Scan(&e.ID, &e.Start, &e.End, &tz, &e.Data)
	if err != nil {
		return nil, err
	}
	timezone, err := time.Parse(tzFormat, tz)
	if err != nil {
		return &e, fmt.Errorf("invalid timezone for event %d: %w", e.ID, err)
	}
	loc := timezone.Location()
	e.Start = e.Start.In(loc)
	e.End = e.End.In(loc)
	if rows.Next() {
		return &e, errors.New("unexpected item")
	}
	return &e, nil
}

// Dump dumps the complete database into a slice of [worklog.BucketMetadata].
func (db *DB) Dump(ctx context.Context) ([]worklog.BucketMetadata, error) {
	m, err := db.buckets(ctx)
	if err != nil {
		return nil, err
	}
	for i, b := range m {
		bucket, ok := strings.CutSuffix(b.ID, "_"+b.Hostname)
		if !ok {
			return m, fmt.Errorf("invalid bucket ID at %d: %s", i, b.ID)
		}
		e, err := db.events(ctx, b.ID)
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
func (db *DB) DumpRange(ctx context.Context, start, end time.Time) ([]worklog.BucketMetadata, error) {
	m, err := db.buckets(ctx)
	if err != nil {
		return nil, err
	}
	for i, b := range m {
		bucket, ok := strings.CutSuffix(b.ID, "_"+b.Hostname)
		if !ok {
			return m, fmt.Errorf("invalid bucket ID at %d: %s", i, b.ID)
		}
		e, err := db.dumpEventsRange(ctx, b.ID, start, end, nil)
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
	dumpEventsRange = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and endtime >= $2 and starttime <= $3 limit $4`

	dumpEventsRangeUntil = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and starttime <= $2 limit $3`

	dumpEventsRangeFrom = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and endtime >= $2 limit $3`

	dumpEventsLimit = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) limit $2`
)

func (db *DB) dumpEventsRange(ctx context.Context, bid string, start, end time.Time, limit *int) ([]worklog.Event, error) {
	var e []worklog.Event
	err := db.eventsRangeFunc(ctx, bid, start, end, limit, func(m worklog.Event) error {
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
func (db *DB) Load(ctx context.Context, buckets []worklog.BucketMetadata, replace bool) (err error) {
	tx, err := db.store.Begin(ctx)
	if err != nil {
		return err
	}
	defer txDone(ctx, tx, &err)
	for _, m := range buckets {
		var b *worklog.BucketMetadata
		b, err = createBucket(ctx, tx, m.ID, m.Name, m.Type, m.Client, m.Hostname, m.Created, m.Data)
		if !sameBucket(&m, b) {
			return fmt.Errorf("mismatched bucket: %s != %s", bucketString(&m), bucketString(b))
		}
		if replace {
			_, err = tx.Exec(ctx, DeleteBucketEvents, m.ID)
			if err != nil {
				return err
			}
		}
		for i, e := range m.Events {
			bid := fmt.Sprintf("%s_%s", e.Bucket, m.Hostname)
			_, err = insertEvent(ctx, tx, bid, &m.Events[i])
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

func bucketString(b *worklog.BucketMetadata) string {
	return fmt.Sprintf("{id=%s name=%s type=%s client=%s hostname=%s}", b.ID,
		b.Name,
		b.Type,
		b.Client,
		b.Hostname)
}

const Buckets = `select id, name, type, client, hostname, created, timezone, datastr from buckets`

// Buckets returns the full set of bucket metadata.
// The SQL command run is [Buckets].
func (db *DB) Buckets(ctx context.Context) ([]worklog.BucketMetadata, error) {
	return db.buckets(ctx)
}

func (db *DB) buckets(ctx context.Context) ([]worklog.BucketMetadata, error) {
	rows, err := db.store.Query(ctx, Buckets)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var b []worklog.BucketMetadata
	for rows.Next() {
		var (
			m  worklog.BucketMetadata
			tz string
		)
		err = rows.Scan(&m.ID, &m.Name, &m.Type, &m.Client, &m.Hostname, &m.Created, &tz, &m.Data)
		if err != nil {
			return nil, err
		}
		timezone, err := time.Parse(tzFormat, tz)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone for %s bucket %s: %w", m.Name, m.ID, err)
		}
		m.Created = m.Created.In(timezone.Location())
		b = append(b, m)
	}
	return b, nil
}

const Event = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and id = $2 limit 1`

const Events = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
)`

// Buckets returns the full set of events in the bucket with the provided
// internal bucket ID.
// The SQL command run is [Events].
func (db *DB) Events(ctx context.Context, bid string) ([]worklog.Event, error) {
	return db.events(ctx, bid)
}

func (db *DB) events(ctx context.Context, bid string) ([]worklog.Event, error) {
	rows, err := db.store.Query(ctx, Events, bid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var e []worklog.Event
	for rows.Next() {
		var (
			m  worklog.Event
			tz string
		)
		err = rows.Scan(&m.ID, &m.Start, &m.End, &tz, &m.Data)
		if err != nil {
			return nil, err
		}
		timezone, err := time.Parse(tzFormat, tz)
		if err != nil {
			return nil, fmt.Errorf("invalid timezone for event %d: %w", m.ID, err)
		}
		loc := timezone.Location()
		m.Start = m.Start.In(loc)
		m.End = m.End.In(loc)
		e = append(e, m)
	}
	return e, nil
}

const (
	EventsRange = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and endtime >= $2 and starttime <= $3 order by endtime desc limit $4`

	EventsRangeUntil = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and starttime <= $2 order by endtime desc limit $3`

	EventsRangeFrom = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) and endtime >= $2 order by endtime desc limit $3`

	EventsLimit = `select id, starttime, endtime, timezone, datastr from events where bucketrow = (
	select rowid from buckets where id = $1
) order by endtime desc limit $2`
)

// EventsRange returns the events in the bucket with the provided bucket ID
// within the specified time range, sorted descending by end time.
// The SQL command run is [EventsRange], [EventsRangeUntil], [EventsRangeFrom]
// or [EventsLimit] depending on whether start and end are zero.
func (db *DB) EventsRange(ctx context.Context, bid string, start, end time.Time, limit int) ([]worklog.Event, error) {
	var lim *int
	if limit >= 0 {
		lim = &limit
	}
	var e []worklog.Event
	err := db.eventsRangeFunc(ctx, bid, start, end, lim, func(m worklog.Event) error {
		e = append(e, m)
		return nil
	}, true)
	return e, err
}

// EventsRange calls fn on all the events in the bucket with the provided
// bucket ID within the specified time range, sorted descending by end time.
// The SQL command run is [EventsRange], [EventsRangeUntil], [EventsRangeFrom]
// or [EventsLimit] depending on whether start and end are zero.
func (db *DB) EventsRangeFunc(ctx context.Context, bid string, start, end time.Time, limit int, fn func(worklog.Event) error) error {
	var lim *int
	if limit >= 0 {
		lim = &limit
	}
	return db.eventsRangeFunc(ctx, bid, start, end, lim, fn, true)
}

func (db *DB) eventsRangeFunc(ctx context.Context, bid string, start, end time.Time, limit *int, fn func(worklog.Event) error, order bool) error {
	var (
		query string
		rows  pgx.Rows
		err   error
	)
	switch {
	case !start.IsZero() && !end.IsZero():
		query = EventsRange
		if !order {
			query = dumpEventsRange
		}
		rows, err = db.store.Query(ctx, query, bid, start, end, limit)
	case !start.IsZero():
		query = EventsRangeFrom
		if !order {
			query = dumpEventsRangeFrom
		}
		rows, err = db.store.Query(ctx, query, bid, start, limit)
	case !end.IsZero():
		query = EventsRangeUntil
		if !order {
			query = dumpEventsRangeUntil
		}
		rows, err = db.store.Query(ctx, query, bid, end, limit)
	default:
		query = EventsLimit
		if !order {
			query = dumpEventsLimit
		}
		rows, err = db.store.Query(ctx, query, bid, limit)
	}
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			m  worklog.Event
			tz string
		)
		err = rows.Scan(&m.ID, &m.Start, &m.End, &tz, &m.Data)
		if err != nil {
			return err
		}
		timezone, err := time.Parse(tzFormat, tz)
		if err != nil {
			return fmt.Errorf("invalid timezone for event %d: %w", m.ID, err)
		}
		loc := timezone.Location()
		m.Start = m.Start.In(loc)
		m.End = m.End.In(loc)
		err = fn(m)
		if err != nil {
			return err
		}
	}
	return nil
}

// Select allows running an PostgreSQL SELECT query. The query is run on the
// store's read-only connection to the database.
func (db *DB) Select(ctx context.Context, query string) ([]map[string]any, error) {
	if db.roStore == nil {
		return nil, db.wrapROWarn(errors.New("no read-only connection"))
	}

	rows, err := db.roStore.Query(ctx, query)
	if err != nil {
		return nil, db.wrapROWarn(err)
	}
	defer rows.Close()

	descs := rows.FieldDescriptions()
	var e []map[string]any
	for rows.Next() {
		cols, err := rows.Values()
		if err != nil {
			return nil, db.wrapROWarn(err)
		}
		result := make(map[string]any)
		for i, v := range cols {
			result[descs[i].Name] = v
		}
		e = append(e, result)
	}
	return e, db.wrapROWarn(rows.Err())
}

func (db *DB) wrapROWarn(err error) error {
	if err == nil || db.roWarn == nil {
		return err
	}
	return fmt.Errorf("%w: %w", err, db.roWarn)
}

const AmendEventsPrepare = `update events set datastr = jsonb_set(datastr, '{amend}', '[]')
	where
		starttime < $3 and
		endtime > $2 and
		not datastr::jsonb ? 'amend' and
		bucketrow = (
			select rowid from buckets where id = $1
		)`
const AmendEventsUpdate = `update events set datastr = jsonb_set(
		datastr,
		'{amend}',
		datastr->'amend' || jsonb_build_object(
			'time', $2::text,
			'msg', $3::text,
			'replace', (
				with replace as (
					select jsonb($6::text) replacements
				)
				select
					jsonb_agg(new order by idx) trimmed_replacements
				from
					replace, lateral (
						select idx, jsonb_object_agg(key,
							case
								when key = 'start'
									then to_jsonb(greatest(old::text::timestamptz, starttime))
								when key = 'end'
									then to_jsonb(least(old::text::timestamptz, endtime))
								else old
							end 
						)
						from
							jsonb_array_elements(replacements)
								with ordinality rs(r, idx),
							jsonb_each(r) each(key, old)
						where
							(r->>'start')::timestamptz < endtime and 
							(r->>'end')::timestamptz > starttime
						group BY idx
					) news(idx, new)
			)
		)
	)
	where
		starttime < $5 and
		endtime > $4 and
		bucketrow = (
			select rowid from buckets where id = $1
		)
	returning id`

// AmendEvents adds amendment notes to the data for events in the store
// overlapping the note. On return the note.Replace slice will be sorted.
//
// The SQL commands run are [AmendEventsPrepare] and [AmendEventsUpdate]
// in a transaction.
func (db *DB) AmendEvents(ctx context.Context, ts time.Time, note *worklog.Amendment) (sql.Result, error) {
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
	var res result
	err = pgx.BeginFunc(ctx, db.store, func(tx pgx.Tx) error {
		_, err = tx.Exec(ctx, AmendEventsPrepare, db.BucketID(note.Bucket), start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano))
		if err != nil {
			return fmt.Errorf("prepare amendment list: %w", err)
		}
		var rows pgx.Rows
		rows, err = tx.Query(ctx, AmendEventsUpdate, db.BucketID(note.Bucket), ts.Format(time.RFC3339Nano), note.Message, start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), replace)
		if err != nil {
			return fmt.Errorf("add amendments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			res.n++
			err = rows.Scan(&res.lastID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, err
}

const DeleteEvent = `delete from events where bucketrow = (
	select rowid from buckets where id = $1
) and id = $2`

const DeleteBucketEvents = `delete from events where bucketrow in (
	select rowid from buckets where id = $1
)`

const DeleteBucket = `delete from buckets where id = $1`
