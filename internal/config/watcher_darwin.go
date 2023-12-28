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
				case ev.Has(fsnotify.Write | fsnotify.Create):
					w.log.LogAttrs(ctx, slog.LevelDebug, "create", slog.String("name", ev.Name))
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

					// Darwin appears to sometimes fuse chmod into a write. This
					// has no impact on our logic, but can cause tests to fail,
					// so unset that bit.
					ev.Op &^= fsnotify.Chmod

					w.changes <- Change{
						Event:  []fsnotify.Event{ev},
						Config: cfg,
						Err:    err,
					}

				case ev.Has(fsnotify.Rename):
					w.log.LogAttrs(ctx, slog.LevelDebug, "rename", slog.String("name", ev.Name), slog.Any("sum", sumValue{w.hashes[ev.Name]}), slog.Any("existing_hashes", hashesValue{w.hashes}))
					delete(w.hashes, ev.Name)

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
