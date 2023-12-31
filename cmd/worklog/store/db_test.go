// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package store

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/exp/slices"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
)

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
	keep    = flag.Bool("keep", false, "keep workdir after tests")
)

const workDir = "testdata"

func TestDB(t *testing.T) {
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

		db, err := Open(context.Background(), path, "")
		if err != nil {
			t.Fatalf("failed to create db: %v", err)
		}
		err = db.Close()
		if err != nil {
			t.Fatalf("failed to close db: %v", err)
		}

		now := time.Now().Round(0)
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
						Start:  now.Add(1 * time.Second),
						End:    now.Add(2 * time.Second),
						Data:   map[string]any{"key1": "value1"},
					},
					{
						Bucket: bucket,
						Start:  now.Add(3 * time.Second),
						End:    now.Add(4 * time.Second),
						Data:   map[string]any{"key2": "value2"},
					},
					{
						Bucket: bucket,
						Start:  now.Add(5 * time.Second),
						End:    now.Add(6 * time.Second),
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

		t.Run("load_dump", func(t *testing.T) {
			db, err := Open(context.Background(), path, "")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}

			err = db.Load(data)
			if err != nil {
				t.Errorf("failed to load data: %v", err)
			}

			got, err := db.Dump()
			if err != nil {
				t.Errorf("failed to dump data: %v", err)
			}

			want := data
			if !cmp.Equal(want, got, ignoreID) {
				t.Errorf("unexpected dump result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
			}

			gotRange, err := db.DumpRange(now.Add(3*time.Second), now.Add(4*time.Second))
			if err != nil {
				t.Errorf("failed to dump data: %v", err)
			}

			wantRange := slices.Clone(data)
			wantRange[0].Events = wantRange[0].Events[1:2]
			if !cmp.Equal(wantRange, gotRange, ignoreID) {
				t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRange, gotRange, ignoreID))
			}

			gotRangeFrom, err := db.DumpRange(now.Add(3*time.Second), time.Time{})
			if err != nil {
				t.Errorf("failed to dump data: %v", err)
			}

			wantRangeFrom := slices.Clone(data)
			wantRangeFrom[0].Events = wantRangeFrom[0].Events[1:]
			if !cmp.Equal(wantRangeFrom, gotRangeFrom, ignoreID) {
				t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRangeFrom, gotRangeFrom, ignoreID))
			}

			gotRangeUntil, err := db.DumpRange(time.Time{}, now.Add(4*time.Second))
			if err != nil {
				t.Errorf("failed to dump data: %v", err)
			}

			wantRangeUntil := slices.Clone(data)
			wantRangeUntil[0].Events = wantRangeUntil[0].Events[:2]
			if !cmp.Equal(wantRangeUntil, gotRangeUntil, ignoreID) {
				t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRangeUntil, gotRangeUntil, ignoreID))
			}

			gotRangeAll, err := db.DumpRange(time.Time{}, time.Time{})
			if err != nil {
				t.Errorf("failed to dump data: %v", err)
			}

			wantRangeAll := slices.Clone(data)
			if !cmp.Equal(wantRangeAll, gotRangeAll, ignoreID) {
				t.Errorf("unexpected dump range result:\n--- want:\n+++ got:\n%s", cmp.Diff(wantRangeAll, gotRangeAll, ignoreID))
			}

			err = db.Close()
			if err != nil {
				t.Errorf("failed to close db: %v", err)
			}
		})

		t.Run("last_event", func(t *testing.T) {
			db, err := Open(context.Background(), path, "")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			db.host = "test_host"

			got, err := db.LastEvent(bucket)
			if err != nil {
				t.Errorf("failed to get last event: %v", err)
			}
			got.Bucket = bucket

			want := &data[0].Events[len(data[0].Events)-1]
			if !cmp.Equal(want, got, ignoreID) {
				t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
			}

			err = db.Close()
			if err != nil {
				t.Errorf("failed to close db: %v", err)
			}
		})

		t.Run("update_last_event", func(t *testing.T) {
			db, err := Open(context.Background(), path, "")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			db.host = "test_host"

			e := data[0].Events[len(data[0].Events)-1]
			e.End = e.End.Add(time.Second)
			last, err := db.LastEvent(e.Bucket)
			if err != nil {
				t.Fatalf("failed to get last event: %v", err)
			}
			e.ID = last.ID
			_, err = db.UpdateEvent(&e)
			if err != nil {
				t.Fatalf("failed to update event: %v", err)
			}
			if err != nil {
				t.Errorf("failed to update event: %v", err)
			}
			got, err := db.LastEvent(bucket)
			if err != nil {
				t.Errorf("failed to get last event: %v", err)
			}
			got.Bucket = bucket

			want := &e
			if !cmp.Equal(want, got, ignoreID) {
				t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, ignoreID))
			}

			err = db.Close()
			if err != nil {
				t.Errorf("failed to close db: %v", err)
			}
		})

		t.Run("events_range", func(t *testing.T) {
			db, err := Open(context.Background(), path, "")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			db.host = "test_host"

			bid := db.BucketID(bucket)
			got, err := db.EventsRange(bid, now.Add(3*time.Second), now.Add(4*time.Second), -1)
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

			err = db.Close()
			if err != nil {
				t.Errorf("failed to close db: %v", err)
			}
		})

		t.Run("update_last_event_coequal", func(t *testing.T) {
			db, err := Open(context.Background(), filepath.Join(workDir, "coequal.db"), "")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			db.host = "test_host"

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
				_, err = db.CreateBucket(b.ID, b.Name, b.Type, b.Client, b.Created, b.Data)
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
					last, err := db.LastEvent(note.Bucket)
					if err != nil {
						t.Fatalf("failed to get last event: %v", err)
					}
					note.ID = last.ID
					_, err = db.UpdateEvent(note)
					if err != nil {
						t.Fatalf("failed to update event: %v", err)
					}
				} else {
					_, err = db.InsertEvent(note)
					if err != nil {
						t.Fatalf("failed to insert event: %v", err)
					}
				}

				dump, err := db.Dump()
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

			t.Run("dynamic_query", func(t *testing.T) {
				dynamicTests := []struct {
					name  string
					sql   string
					query Query
				}{
					{
						name: "kitchen_or",
						sql: `select json_extract(datastr, "$.title"), starttime, json_extract(datastr, "$.afk") from events
					where 
						json_extract(datastr, "$.afk") = false or json_extract(datastr, "$.title") = "Terminal"
					limit 2`,
						query: Query{
							Fields: []string{"starttime", "$.title", "$.afk"},
							From:   "events",
							Where: map[string]any{
								"or": []any{
									map[string]any{
										"=": map[string]any{
											"$.afk": false,
										},
									},
									map[string]any{
										"=": map[string]any{
											"$.title": "Terminal",
										},
									},
								},
							},
							Limit: ptr(2),
						},
					},
					{
						name: "kitchen_and",
						sql: `select json_extract(datastr, "$.title"), starttime, json_extract(datastr, "$.afk") from events
					where 
						json_extract(datastr, "$.afk") = false and json_extract(datastr, "$.title") = "Terminal"
					limit 2`,
						query: Query{
							Fields: []string{"starttime", "$.title", "$.afk"},
							From:   "events",
							Where: map[string]any{
								"and": []any{
									map[string]any{
										"=": map[string]any{
											"$.afk": false,
										},
									},
									map[string]any{
										"=": map[string]any{
											"$.title": "Terminal",
										},
									},
								},
							},
							Limit: ptr(2),
						},
					},
					{
						name: "count",
						sql:  `select count(*) from events`,
						query: Query{
							Fields: "count ( *)", // Intentionally ugly whitespace.
							From:   "events",
						},
					},
					{
						name: "all_implicit",
						sql:  `select * from events`,
						query: Query{
							From: "events",
						},
					},
					{
						name: "all_explicit",
						sql:  `select * from events`,
						query: Query{
							Fields: "*",
							From:   "events",
						},
					},
					{
						name: "non_null_afk",
						sql:  `select * from events where json_extract(datastr, "$.app") notnull`,
						query: Query{
							Fields: "*",
							From:   "events",
							Where: map[string]any{
								"notnull": "$.app",
							},
						},
					},
				}

				for _, test := range dynamicTests {
					t.Run(test.name, func(t *testing.T) {
						rows, err := db.query(test.sql)
						if err != nil {
							t.Fatalf("unexpected error for query: %v", err)
						}
						names, err := rows.Columns()
						if err != nil {
							t.Fatalf("failed to get names: %v", err)
						}
						for i, n := range names {
							n = strings.TrimPrefix(names[i], `json_extract(datastr, "`)
							names[i] = strings.TrimSuffix(n, `")`)
						}
						var want []map[string]any
						for rows.Next() {
							args := make([]any, len(names))
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
								row[names[i]] = *(a.(*any))
							}
							want = append(want, row)
						}
						rows.Close()

						got, err := db.Dynamic(test.query)
						if err != nil {
							t.Fatal(err)
						}

						if !cmp.Equal(want, got) {
							t.Errorf("unexpected result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got))
						}
					})
				}
			})

			db.Close()
		})

		t.Run("amend", func(t *testing.T) {
			db, err := Open(context.Background(), filepath.Join(workDir, "amend.db"), "")
			if err != nil {
				t.Fatalf("failed to create db: %v", err)
			}
			db.host = "test_host"

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
				_, err = db.CreateBucket(b.ID, b.Name, b.Type, b.Client, b.Created, b.Data)
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
				_, err = db.InsertEvent(note)
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
			_, err = db.AmendEvents(time.Time{}, amendment)
			if err != nil {
				t.Errorf("unexpected error amending events: %v", err)
			}
			dump, err := db.Dump()
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
							t.Error("unexpected zero-length []AmendEvents")
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

			db.Close()
		})
	})
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
