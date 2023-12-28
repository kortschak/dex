// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch processes the receiver's fsnotify.Watcher events, performing
// aggregation and semantic filtering.
func (w *Watcher) Watch(ctx context.Context) error {
	defer func() {
		w.watcher.Close()
		close(w.changes)
	}()

	renames := make(map[Sum]fsnotify.Event)
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-w.watcher.Events:
			if filepath.Ext(ev.Name) == ".toml" {
				if ev.Has(fsnotify.Write | fsnotify.Create) {
					fi, err := os.Stat(ev.Name)
					if err != nil {
						w.changes <- Change{Err: err}
						continue
					}
					if fi.IsDir() {
						continue
					}
				}

				switch {
				case ev.Has(fsnotify.Write):
					w.log.LogAttrs(ctx, slog.LevelDebug, "write", slog.String("name", ev.Name))
					time.Sleep(w.debounce)

					b, err := os.ReadFile(ev.Name)
					if err != nil {
						w.log.LogAttrs(ctx, slog.LevelError, "read file", slog.Any("error", err))
						w.changes <- Change{Err: err}
						continue
					}
					cfg, sum, err := unmarshalConfigs(w.hash, b)
					if w.hashes[ev.Name] == sum {
						w.log.LogAttrs(ctx, slog.LevelDebug, "no change", slog.Any("sum", sumValue{sum}), slog.Any("existing_hashes", hashesValue{w.hashes}))
						continue
					}
					if cfg != nil {
						w.log.LogAttrs(ctx, slog.LevelDebug, "set hash", slog.Any("sum", sumValue{sum}), slog.Any("existing_hashes", hashesValue{w.hashes}))
						w.hashes[ev.Name] = sum
					}
					w.changes <- Change{
						Event:  []fsnotify.Event{ev},
						Config: cfg,
						Err:    err,
					}

				// Renames are seen as a rename/create pair. Create events
				// independent of a write are not informative, so we handle
				// those in the write case.
				case ev.Has(fsnotify.Rename):
					w.log.LogAttrs(ctx, slog.LevelDebug, "rename", slog.String("name", ev.Name))
					sum := w.hashes[ev.Name]
					w.log.LogAttrs(ctx, slog.LevelDebug, "set renames", slog.Any("sum", sumValue{sum}), slog.Any("existing_hashes", hashesValue{w.hashes}), slog.Any("renames", renamesValue{renames}))
					renames[sum] = ev
					delete(w.hashes, ev.Name)
				case ev.Has(fsnotify.Create):
					w.log.LogAttrs(ctx, slog.LevelDebug, "create", slog.String("name", ev.Name))
					b, err := os.ReadFile(ev.Name)
					if err != nil {
						w.log.LogAttrs(ctx, slog.LevelError, "read file", slog.Any("error", err))
						w.changes <- Change{Err: err}
						continue
					}
					cfg, sum, err := unmarshalConfigs(w.hash, b)
					prev, ok := renames[sum]
					if !ok {
						w.log.LogAttrs(ctx, slog.LevelDebug, "no renames", slog.Any("sum", sumValue{sum}), slog.Any("renames", renamesValue{renames}))
						continue
					}
					delete(renames, sum)
					w.log.LogAttrs(ctx, slog.LevelDebug, "set hash", slog.Any("sum", sumValue{sum}), slog.Any("existing_hashes", hashesValue{w.hashes}))
					w.hashes[ev.Name] = sum
					w.changes <- Change{
						Event:  []fsnotify.Event{prev, ev},
						Config: cfg,
						Err:    err,
					}

				case ev.Has(fsnotify.Remove):
					w.log.LogAttrs(ctx, slog.LevelDebug, "remove", slog.String("name", ev.Name))
					w.changes <- Change{Event: []fsnotify.Event{ev}}
					delete(w.hashes, ev.Name)
				}
			} else if ev.Has(fsnotify.Remove) && ev.Name == w.dir {
				w.log.LogAttrs(ctx, slog.LevelDebug, "remove config directory", slog.String("name", ev.Name))
				w.changes <- Change{Event: []fsnotify.Event{ev}}
				err := os.Mkdir(w.dir, 0o755)
				if err != nil {
					w.log.LogAttrs(ctx, slog.LevelError, "replace config dir", slog.String("path", w.dir), slog.Any("error", err))
					continue
				}
				err = w.watcher.Add(w.dir)
				if err != nil {
					w.log.LogAttrs(ctx, slog.LevelError, "replace watch", slog.Any("error", err))
					continue
				}
			}

		case err := <-w.watcher.Errors:
			w.changes <- Change{Err: err}
		}
	}
}
