// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pgstore

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5"
	"golang.org/x/exp/slices"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
	keep    = flag.Bool("keep", false, "keep test database after tests")
)

const testDir = "testdata"

func TestDB(t *testing.T) {
	if !*keep {
		t.Cleanup(func() {
			os.RemoveAll(testDir)
		})
	}
	const dbBaseName = "test_worklog_database"

	pgHost := os.Getenv("PGHOST")
	if pgHost == "" {
		t.Fatal("must have postgres host in $PGHOST")
	}
	pgPort := os.Getenv("PGPORT")
	if pgPort == "" {
		t.Fatal("must have postgres port number in $PGPORT")
	}
	pgHostPort := pgHost + ":" + pgPort

	pgUser := os.Getenv("PGUSER")
	if pgUser == "" {
		t.Fatal("must have postgres user in $PGUSER")
	}
	pgUserinfo, err := pgUserinfo(pgUser, pgHost, pgPort, os.Getenv("PGPASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	pgPassword, ok := pgUserinfo.Password()
	if !ok {
		t.Fatal("did not get password")
	}

	ctx := context.Background()
	for _, interval := range []struct {
		name     string
		duration time.Duration
	}{
		{name: "second", duration: time.Second},
		{name: "subsecond", duration: time.Second / 10},
	} {
		dbName := dbBaseName + "_" + interval.name
		dropTestDB(t, ctx, pgUserinfo, pgHostPort, dbName)
		drop := createTestDB(t, ctx, pgUserinfo, pgHostPort, dbName)
		if !*keep {
			defer drop()
		}

		t.Run(interval.name, func(t *testing.T) {
			t.Run("db", func(t *testing.T) {
				u := url.URL{
					Scheme: "postgres",
					User:   url.UserPassword(pgUser, pgPassword),
					Host:   pgHostPort,
					Path:   dbName,
				}
				dbURL := u.String()

				db, err := Open(ctx, dbURL, "test_host")
				if err != nil && !errors.As(err, &Warning{}) {
					t.Fatalf("failed to open db: %v", err)
				}
				err = db.Close(ctx)
				if err != nil {
					t.Fatalf("failed to close db: %v", err)
				}

				now := time.Now().Round(time.Millisecond)
				bucket := "test_bucket"
				data := []worklog.BucketMetadata{
					{
						ID:       "test_bucket_test_host",
						Name:     "test_bucket_name",
						Type:     "test_bucket_type",
						Client:   "testing",
						Hostname: "test_host",
						Created:  now,
						Data:     map[string]any{"key0": "value0"},
						Events: []worklog.Event{
							{
								Bucket: bucket,
								Start:  now.Add(1 * interval.duration),
								End:    now.Add(2 * interval.duration),
								Data:   map[string]any{"key1": "value1"},
							},
							{
								Bucket: bucket,
								Start:  now.Add(3 * interval.duration),
								End:    now.Add(4 * interval.duration),
								Data:   map[string]any{"key2": "value2"},
							},
							{
								Bucket: bucket,
								Start:  now.Add(5 * interval.duration),
								End:    now.Add(6 * interval.duration),
								Data:   map[string]any{"key3": "value3"},
							},
						},
					},
				}

				// worklog.Event.ID is the only int64 field, so
				// this is easier than filtering on field name.
				ignoreID := cmp.FilterValues(
					func(_, _ int64) bool { return true },
					cmp.Ignore(),
				)

				db, err = Open(ctx, dbURL, "test_host")
				if err != nil && !errors.As(err, &Warning{}) {
					t.Fatalf("failed to open db: %v", err)
				}
				defer func() {
					err = db.Close(ctx)
					if err != nil {
						t.Errorf("failed to close db: %v", err)
					}
				}()

				t.Run("load_dump", func(t *testing.T) {
					err = db.Load(ctx, data, false)
					if err != nil {
						t.Errorf("failed to load data no replace: %v", err)
					}
					err = db.Load(ctx, data, true)
					if err != nil {
						t.Errorf("failed to load data replace: %v", err)
					}

					got, err := db.Dump(ctx)
					if err != nil {
						t.Errorf("failed to dump data: %v", err)
					}

					want := data
					if !cmp.Equal(want, got, ignoreID) {
						t.Errorf("unexpected dump result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
					}

					gotRange, err := db.DumpRange(ctx, now.Add(3*interval.duration), now.Add(4*interval.duration))
					if err != nil {
						t.Errorf("failed to dump data: %v", err)
					}

					wantRange := slices.Clone(data)
					wantRange[0].Events = wantRange[0].Events[1:2]
					if !cmp.Equal(wantRange, gotRange, ignoreID) {
						t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRange, gotRange, ignoreID))
					}

					gotRangeFrom, err := db.DumpRange(ctx, now.Add(3*interval.duration), time.Time{})
					if err != nil {
						t.Errorf("failed to dump data: %v", err)
					}

					wantRangeFrom := slices.Clone(data)
					wantRangeFrom[0].Events = wantRangeFrom[0].Events[1:]
					if !cmp.Equal(wantRangeFrom, gotRangeFrom, ignoreID) {
						t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRangeFrom, gotRangeFrom, ignoreID))
					}

					gotRangeUntil, err := db.DumpRange(ctx, time.Time{}, now.Add(4*interval.duration))
					if err != nil {
						t.Errorf("failed to dump data: %v", err)
					}

					wantRangeUntil := slices.Clone(data)
					wantRangeUntil[0].Events = wantRangeUntil[0].Events[:2]
					if !cmp.Equal(wantRangeUntil, gotRangeUntil, ignoreID) {
						t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRangeUntil, gotRangeUntil, ignoreID))
					}

					gotRangeAll, err := db.DumpRange(ctx, time.Time{}, time.Time{})
					if err != nil {
						t.Errorf("failed to dump data: %v", err)
					}

					wantRangeAll := slices.Clone(data)
					if !cmp.Equal(wantRangeAll, gotRangeAll, ignoreID) {
						t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRangeAll, gotRangeAll, ignoreID))
					}

				})

				t.Run("backup", func(t *testing.T) {
					workDir := filepath.Join(testDir, interval.name)
					err := os.MkdirAll(workDir, 0o755)
					if err != nil && !errors.Is(err, fs.ErrExist) {
						t.Fatalf("failed to make dir: %v", err)
					}

					path, err := db.Backup(ctx, workDir)
					if err != nil {
						if strings.Contains(err.Error(), "aborting because of server version mismatch") {
							// On CI, we may have a different local version
							// to the version on the service container.
							// Skip if that's the case.
							t.Skip(err.Error())
						}
						t.Fatalf("failed to backup database: %v", err)
					}
					f, err := os.Open(path)
					if errors.Is(err, fs.ErrNotExist) {
						t.Fatal("did not create backup")
					} else if err != nil {
						t.Fatalf("unexpected error opening backup file: %v", err)
					}
					r, err := gzip.NewReader(f)
					if err != nil {
						t.Fatalf("unexpected error opening gzip backup file: %v", err)
					}
					_, err = io.Copy(io.Discard, r)
					if err != nil {
						t.Errorf("unexpected error gunzipping backup file: %v", err)
					}
				})

				t.Run("last_event", func(t *testing.T) {
					got, err := db.LastEvent(ctx, bucket)
					if err != nil {
						t.Fatalf("failed to get last event: %v", err)
					}
					got.Bucket = bucket

					want := &data[0].Events[len(data[0].Events)-1]
					if !cmp.Equal(want, got, ignoreID) {
						t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
					}
				})

				t.Run("update_last_event", func(t *testing.T) {
					e := data[0].Events[len(data[0].Events)-1]
					e.End = e.End.Add(interval.duration)
					last, err := db.LastEvent(ctx, e.Bucket)
					if err != nil {
						t.Fatalf("failed to get last event: %v", err)
					}
					e.ID = last.ID
					_, err = db.UpdateEvent(ctx, &e)
					if err != nil {
						t.Fatalf("failed to update event: %v", err)
					}
					if err != nil {
						t.Errorf("failed to update event: %v", err)
					}
					got, err := db.LastEvent(ctx, bucket)
					if err != nil {
						t.Errorf("failed to get last event: %v", err)
					}
					got.Bucket = bucket

					want := &e
					if !cmp.Equal(want, got, ignoreID) {
						t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
					}
				})

				t.Run("events_range", func(t *testing.T) {
					bid := db.BucketID(bucket)
					for _, loc := range []*time.Location{time.Local, time.UTC} {
						t.Run(loc.String(), func(t *testing.T) {
							got, err := db.EventsRange(ctx, bid, now.Add(3*interval.duration).In(loc), now.Add(4*interval.duration).In(loc), -1)
							if err != nil {
								t.Errorf("failed to load data: %v", err)
							}
							for i := range got {
								got[i].Bucket = bucket
							}

							want := data[0].Events[1:2]
							if !cmp.Equal(want, got, ignoreID) {
								t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
							}
						})
					}
				})

				t.Run("update_last_event_coequal", func(t *testing.T) {
					dbName := dbName + "_coequal"
					dropTestDB(t, ctx, pgUserinfo, pgHostPort, dbName)
					drop := createTestDB(t, ctx, pgUserinfo, pgHostPort, dbName)
					if !*keep {
						defer drop()
					}

					u := url.URL{
						Scheme: "postgres",
						User:   url.UserPassword(pgUser, pgPassword),
						Host:   pgHostPort,
						Path:   dbName,
					}
					db, err := Open(ctx, u.String(), "test_host")
					if err != nil && !errors.As(err, &Warning{}) {
						t.Fatalf("failed to open db: %v", err)
					}
					defer db.Close(ctx)

					buckets := []string{
						`{"id":"window","name":"window-watcher","type":"currentwindow","client":"worklog","hostname":"test_host","created":"2023-06-12T19:54:38.305691865+09:30"}`,
						`{"id":"afk","name":"afk-watcher","type":"afkstatus","client":"worklog","hostname":"test_host","created":"2023-06-12T19:54:38.310302464+09:30"}`,
					}
					for _, msg := range buckets {
						var b worklog.BucketMetadata
						err := json.Unmarshal([]byte(msg), &b)
						if err != nil {
							t.Fatalf("failed to unmarshal bucket message: %v", err)
						}
						_, err = db.CreateBucket(ctx, b.ID, b.Name, b.Type, b.Client, b.Created, b.Data)
						if err != nil {
							t.Fatalf("failed to create bucket: %v", err)
						}
					}

					events := []string{
						`{"bucket":"window","start":"2023-06-12T19:54:39.248859996+09:30","end":"2023-06-12T19:54:39.248859996+09:30","data":{"app":"Gnome-terminal","title":"Terminal"},"continue":false}`,
						`{"bucket":"afk","start":"2023-06-12T19:54:39.248859996+09:30","end":"2023-06-12T19:54:39.248859996+09:30","data":{"afk":false,"locked":false},"continue":false}`,
						`{"bucket":"window","start":"2023-06-12T19:54:40.247357339+09:30","end":"2023-06-12T19:54:40.247357339+09:30","data":{"app":"Gnome-terminal","title":"Terminal"},"continue":false}`,
						`{"bucket":"afk","start":"2023-06-12T19:54:39.248859996+09:30","end":"2023-06-12T19:54:40.247357339+09:30","data":{"afk":false,"locked":false},"continue":true}`,
					}
					for i, msg := range events {
						var note *worklog.Event
						err := json.Unmarshal([]byte(msg), &note)
						if err != nil {
							t.Fatalf("failed to unmarshal event message: %v", err)
						}
						if note.Continue != nil && *note.Continue {
							last, err := db.LastEvent(ctx, note.Bucket)
							if err != nil {
								t.Fatalf("failed to get last event: %v", err)
							}
							note.ID = last.ID
							_, err = db.UpdateEvent(ctx, note)
							if err != nil {
								t.Fatalf("failed to update event: %v", err)
							}
						} else {
							_, err = db.InsertEvent(ctx, note)
							if err != nil {
								t.Fatalf("failed to insert event: %v", err)
							}
						}

						dump, err := db.Dump(ctx)
						if err != nil {
							t.Fatalf("failed to dump db after step %d: %v", i, err)
						}
						t.Logf("note: %#v\ndump: %#v", note, dump)

						for _, b := range dump {
							for _, e := range b.Events {
								if e.Bucket == "window" {
									if _, ok := e.Data["afk"]; ok {
										t.Errorf("unexpectedly found afk data in window bucket: %v", e)
									}
								}
							}
						}
					}
				})

				t.Run("amend", func(t *testing.T) {
					// t.Skip("not yet working")

					dbName := dbName + "_amend"
					dropTestDB(t, ctx, pgUserinfo, pgHostPort, dbName)
					drop := createTestDB(t, ctx, pgUserinfo, pgHostPort, dbName)
					if !*keep {
						defer drop()
					}

					u := url.URL{
						Scheme: "postgres",
						User:   pgUserinfo,
						Host:   pgHostPort,
						Path:   dbName,
					}
					db, err := Open(ctx, u.String(), "test_host")
					if err != nil && !errors.As(err, &Warning{}) {
						t.Fatalf("failed to open db: %v", err)
					}
					defer db.Close(ctx)

					buckets := []string{
						`{"id":"window","name":"window-watcher","type":"currentwindow","client":"worklog","hostname":"test_host","created":"2023-06-12T19:54:38Z"}`,
						`{"id":"afk","name":"afk-watcher","type":"afkstatus","client":"worklog","hostname":"test_host","created":"2023-06-12T19:54:38Z"}`,
					}
					for _, msg := range buckets {
						var b worklog.BucketMetadata
						err := json.Unmarshal([]byte(msg), &b)
						if err != nil {
							t.Fatalf("failed to unmarshal bucket message: %v", err)
						}
						_, err = db.CreateBucket(ctx, b.ID, b.Name, b.Type, b.Client, b.Created, b.Data)
						if err != nil {
							t.Fatalf("failed to create bucket: %v", err)
						}
					}

					events := []string{
						`{"bucket":"window","start":"2023-06-12T19:54:40Z","end":"2023-06-12T19:54:45Z","data":{"app":"Gnome-terminal","title":"Terminal"}}`,
						`{"bucket":"afk","start":"2023-06-12T19:54:40Z","end":"2023-06-12T19:54:45Z","data":{"afk":false,"locked":false}}`,
						`{"bucket":"window","start":"2023-06-12T19:54:45Z","end":"2023-06-12T19:54:50Z","data":{"app":"Gnome-terminal","title":"Terminal"}}`,
						`{"bucket":"afk","start":"2023-06-12T19:54:45Z","end":"2023-06-12T19:54:50Z","data":{"afk":true,"locked":true}}`,
						`{"bucket":"window","start":"2023-06-12T19:54:50Z","end":"2023-06-12T19:54:55Z","data":{"app":"Gnome-terminal","title":"Terminal"}}`,
						`{"bucket":"afk","start":"2023-06-12T19:54:50Z","end":"2023-06-12T19:54:55Z","data":{"afk":false,"locked":false}}`,
						`{"bucket":"window","start":"2023-06-12T19:54:55Z","end":"2023-06-12T19:54:59Z","data":{"app":"Gnome-terminal","title":"Terminal"}}`,
						`{"bucket":"afk","start":"2023-06-12T19:54:55Z","end":"2023-06-12T19:54:59Z","data":{"afk":true,"locked":true}}`,
					}
					for _, msg := range events {
						var note *worklog.Event
						err := json.Unmarshal([]byte(msg), &note)
						if err != nil {
							t.Fatalf("failed to unmarshal event message: %v", err)
						}
						_, err = db.InsertEvent(ctx, note)
						if err != nil {
							t.Fatalf("failed to insert event: %v", err)
						}
					}
					msg := `{"bucket":"afk","msg":"testing","replace":[{"start":"2023-06-12T19:54:39Z","end":"2023-06-12T19:54:51Z","data":{"afk":true,"locked":true}}]}`
					var amendment *worklog.Amendment
					err = json.Unmarshal([]byte(msg), &amendment)
					if err != nil {
						t.Fatalf("failed to unmarshal event message: %v", err)
					}
					_, err = db.AmendEvents(ctx, time.Time{}, amendment)
					if err != nil {
						t.Errorf("unexpected error amending events: %v", err)
					}
					dump, err := db.Dump(ctx)
					if err != nil {
						t.Fatalf("failed to dump db: %v", err)
					}
					for _, bucket := range dump {
						for i, event := range bucket.Events {
							switch event.Bucket {
							case "window":
								_, ok := event.Data["amend"]
								if ok {
									t.Errorf("unexpected amendment in window event %d: %v", i, event.Data)
								}
							case "afk":
								a, ok := event.Data["amend"]
								if !ok {
									for _, r := range amendment.Replace {
										if overlaps(event.Start, event.End, r.Start, r.End) {
											t.Errorf("expected amendment for event %d of afk", i)
											break
										}
									}
									break
								}
								var n []worklog.Amendment
								err = remarshalJSON(&n, a)
								if err != nil {
									t.Errorf("unexpected error remarshalling []AmendEvents: %v", err)
								}
								if len(n) == 0 {
									t.Fatal("unexpected zero-length []AmendEvents")
								}
								for _, r := range n[len(n)-1].Replace {
									if r.Start.Before(event.Start) {
										t.Errorf("replacement start extends before start of event: %s < %s",
											r.Start.Format(time.RFC3339), event.Start.Format(time.RFC3339))
									}
									if noted, ok := findOverlap(r, amendment.Replace); ok && !r.Start.Equal(event.Start) && !r.Start.Equal(noted.Start) {
										t.Errorf("non-truncated replacement start was altered: %s != %s",
											r.Start.Format(time.RFC3339), noted.Start.Format(time.RFC3339))
									}
									if r.End.After(event.End) {
										t.Errorf("replacement end extends beyond end of event: %s > %s",
											r.End.Format(time.RFC3339), event.End.Format(time.RFC3339))
									}
									if noted, ok := findOverlap(r, amendment.Replace); ok && !r.End.Equal(event.End) && !r.End.Equal(noted.End) {
										t.Errorf("non-truncated replacement end was altered: %s != %s",
											r.End.Format(time.RFC3339), noted.End.Format(time.RFC3339))
									}
								}
							default:
								t.Errorf("unexpected event bucket name in event %d of %s: %s", i, bucket.ID, event.Bucket)
							}
						}
					}
				})

				t.Run("dynamic_query", func(t *testing.T) {
					grantReadAccess(t, ctx, pgUserinfo, pgHost, dbName, pgUser+"_ro")
					u := url.URL{
						Scheme: "postgres",
						User:   pgUserinfo,
						Host:   pgHostPort,
						Path:   dbName,
					}
					db, err := Open(ctx, u.String(), "test_host")
					if err != nil {
						t.Fatalf("failed to open db: %v", err)
					}
					defer db.Close(ctx)

					dynamicTests := []struct {
						name    string
						sql     string
						wantErr []error
					}{
						{
							name: "kitchen_or",
							sql: `select datastr ->> 'title', starttime, datastr ->> 'afk' from events
					where 
						not (datastr ->> 'afk')::boolean or datastr ->> 'title' = 'Terminal'
					limit 2`,
						},
						{
							name: "kitchen_and",
							sql: `select datastr ->> 'title', starttime, datastr ->> 'afk' from events
					where 
						not (datastr ->> 'afk')::boolean and datastr ->> 'title' = 'Terminal'
					limit 2`,
						},
						{
							name: "count",
							sql:  `select count(*) from events`,
						},
						{
							name: "all",
							sql:  `select * from events`,
						},
						{
							name: "non_null_afk",
							sql:  `select * from events where datastr ? 'app'`,
						},
						{
							name: "drop_table",
							sql:  `drop table events`,
							wantErr: []error{
								errors.New("ERROR: must be owner of relation events (SQLSTATE 42501)"),
								errors.New("ERROR: must be owner of table events (SQLSTATE 42501)"),
							},
						},
						{
							name: "sneaky_create_table",
							sql:  "select count(*) from events; create table if not exists t(i)",
							wantErr: []error{
								errors.New("ERROR: syntax error at end of input (SQLSTATE 42601)"),
							},
						},
						{
							name: "sneaky_drop_table",
							sql:  "select count(*) from events; drop table events",
							wantErr: []error{
								errors.New("ERROR: cannot insert multiple commands into a prepared statement (SQLSTATE 42601)"),
							},
						},
					}

					for _, test := range dynamicTests {
						t.Run(test.name, func(t *testing.T) {
							got, err := db.Select(ctx, test.sql)
							if !sameErrorIn(err, test.wantErr) {
								t.Errorf("unexpected error: got:%v want:%v", err, test.wantErr)
								return
							}
							if err != nil {
								return
							}

							rows, err := db.store.Query(ctx, test.sql)
							if err != nil {
								t.Fatalf("unexpected error for query: %v", err)
							}
							descs := rows.FieldDescriptions()
							var want []map[string]any
							for rows.Next() {
								args := make([]any, len(descs))
								for i := range args {
									var a any
									args[i] = &a
								}
								err = rows.Scan(args...)
								if err != nil {
									t.Fatal(err)
								}
								row := make(map[string]any)
								for i, a := range args {
									row[descs[i].Name] = *(a.(*any))
								}
								want = append(want, row)
							}
							rows.Close()

							if !cmp.Equal(want, got) {
								t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got))
							}
						})
					}
				})
			})
		})
	}
}

func createTestDB(t *testing.T, ctx context.Context, user *url.Userinfo, host, dbname string) func() {
	t.Helper()

	u := url.URL{
		Scheme: "postgres",
		User:   user,
		Host:   host,
		Path:   "template1",
	}
	db, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("failed to open admin database: %v", err)
	}
	_, err = db.Exec(ctx, "create database "+dbname)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}
	err = db.Close(ctx)
	if err != nil {
		t.Fatalf("failed to close admin connection: %v", err)
	}

	return func() {
		dropTestDB(t, ctx, user, host, dbname)
	}
}

func dropTestDB(t *testing.T, ctx context.Context, user *url.Userinfo, host, dbname string) {
	t.Helper()

	u := url.URL{
		Scheme: "postgres",
		User:   user,
		Host:   host,
		Path:   "template1",
	}
	db, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("failed to open admin database: %v", err)
	}
	_, err = db.Exec(ctx, "drop database if exists "+dbname)
	if err != nil {
		t.Fatalf("failed to drop test database: %v", err)
	}
	err = db.Close(ctx)
	if err != nil {
		t.Fatalf("failed to close admin connection: %v", err)
	}
}

func grantReadAccess(t *testing.T, ctx context.Context, user *url.Userinfo, host, dbname, target string) {
	t.Helper()

	u := url.URL{
		Scheme: "postgres",
		User:   user,
		Host:   host,
		Path:   dbname,
	}
	db, err := pgx.Connect(ctx, u.String())
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}

	statements := []string{
		fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", dbname, target),
		fmt.Sprintf("GRANT USAGE ON SCHEMA public TO %s", target),
		fmt.Sprintf("GRANT SELECT ON ALL TABLES IN SCHEMA public TO %s", target),
	}
	for _, s := range statements {
		_, err = db.Exec(ctx, s)
		if err != nil {
			t.Fatalf("failed to execute grant: %v", err)
		}
	}

	err = db.Close(ctx)
	if err != nil {
		t.Fatalf("failed to close connection: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func findOverlap(n worklog.Replacement, h []worklog.Replacement) (worklog.Replacement, bool) {
	for _, c := range h {
		if overlaps(n.Start, n.End, c.Start, c.End) {
			return c, true
		}
	}
	return worklog.Replacement{}, false
}

func overlaps(as, ae, bs, be time.Time) bool {
	return ae.After(bs) && as.Before(be)
}
func remarshalJSON(dst, src any) error {
	b, err := json.Marshal(src)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func sameErrorIn(err error, list []error) bool {
	switch {
	case err != nil && list != nil:
		return slices.ContainsFunc(list, func(e error) bool {
			return err.Error() == e.Error()
		})
	case err == nil && list == nil:
		return true
	default:
		return false
	}
}
