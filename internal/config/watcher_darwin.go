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

// process watches the streamProcessor's fsnotify.Watcher events performing
// aggregation and semantic filtering.
func (p *streamProcessor) process(ctx context.Context, watcher *fsnotify.Watcher) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-watcher.Events:
			if filepath.Ext(ev.Name) == ".toml" {
				if ev.Has(fsnotify.Write | fsnotify.Create) {
					fi, err := os.Stat(ev.Name)
					if err != nil {
						p.changes <- Change{Err: err}
						continue
					}
					if fi.IsDir() {
						continue
					}
				}

				switch {
				case ev.Has(fsnotify.Write | fsnotify.Create):
					p.log.LogAttrs(ctx, slog.LevelDebug, "create", slog.String("name", ev.Name))
					time.Sleep(p.debounce)

					b, err := os.ReadFile(ev.Name)
					if err != nil {
						p.log.LogAttrs(ctx, slog.LevelError, "read file", slog.Any("error", err))
						p.changes <- Change{Err: err}
						continue
					}
					cfg, sum, err := unmarshalConfigs(p.hash, b)
					if p.hashes[ev.Name] == sum {
						p.log.LogAttrs(ctx, slog.LevelDebug, "no change", slog.Any("sum", sumValue{sum}), slog.Any("existing_hashes", hashesValue{p.hashes}))
						continue
					}
					if cfg != nil {
						p.log.LogAttrs(ctx, slog.LevelDebug, "set hash", slog.Any("sum", sumValue{sum}), slog.Any("existing_hashes", hashesValue{p.hashes}))
						p.hashes[ev.Name] = sum
					}
					p.changes <- Change{
						Event:  []fsnotify.Event{ev},
						Config: cfg,
						Err:    err,
					}

				case ev.Has(fsnotify.Rename):
					p.log.LogAttrs(ctx, slog.LevelDebug, "rename", slog.String("name", ev.Name), slog.Any("sum", sumValue{p.hashes[ev.Name]}), slog.Any("existing_hashes", hashesValue{p.hashes}))
					delete(p.hashes, ev.Name)

				case ev.Has(fsnotify.Remove):
					p.log.LogAttrs(ctx, slog.LevelDebug, "remove", slog.String("name", ev.Name))
					p.changes <- Change{Event: []fsnotify.Event{ev}}
					delete(p.hashes, ev.Name)
				}
			} else if ev.Has(fsnotify.Remove) && ev.Name == p.dir {
				p.log.LogAttrs(ctx, slog.LevelDebug, "remove config directory", slog.String("name", ev.Name))
				p.changes <- Change{Event: []fsnotify.Event{ev}}
				err := os.Mkdir(p.dir, 0o755)
				if err != nil {
					p.log.LogAttrs(ctx, slog.LevelError, "replace config dir", slog.String("path", p.dir), slog.Any("error", err))
					continue
				}
				err = watcher.Add(p.dir)
				if err != nil {
					p.log.LogAttrs(ctx, slog.LevelError, "replace watch", slog.Any("error", err))
					continue
				}
			}

		case err := <-watcher.Errors:
			p.changes <- Change{Err: err}
		}
	}
}
