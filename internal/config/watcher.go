// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"hash"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/fsnotify/fsnotify"

	"github.com/kortschak/dex/rpc"
)

// FileDebounce is the default duration we wait for the contents to have
// stabilised to work around some editors writing an empty file and then the
// buffer.
const FileDebounce = 10 * time.Millisecond

// Change is a set of related configuration changes identified by Watch.
type Change struct {
	Event  []fsnotify.Event
	Config *System
	Err    error
}

// Op returns an aggregated fsnotify.Op for all elements of the receivers'
// Event field.
func (c Change) Op() fsnotify.Op {
	switch len(c.Event) {
	case 0:
		return 0
	case 1:
		return c.Event[0].Op
	default:
		var op fsnotify.Op
		for _, o := range c.Event {
			op |= o.Op
		}
		return op
	}
}

// NewWatcher starts an fsnotify.Watcher for the provided directory, sending change
// events on the changes channel. If dir is deleted, it is recreated as a new
// directory and a new watcher is set. The debounce parameter specifies how
// long to wait after an fsnotify.Event before reading the file to ensure that
// writes will be reflected in the state checksum. If it is less than zero,
// FileDebounce is used.
func NewWatcher(ctx context.Context, dir string, changes chan<- Change, debounce time.Duration, log *slog.Logger) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	_, err = os.Stat(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		err = os.Mkdir(dir, 0o755)
		if err != nil {
			return nil, err
		}
	}

	err = watcher.Add(dir)
	if err != nil {
		return nil, err
	}
	return newWatcher(changes, watcher, dir, debounce, log).init(ctx)
}

// Watcher collects raw fsnotify.Events and aggregates and filters for
// semantically meaningful configuration changes.
type Watcher struct {
	dir      string
	debounce time.Duration
	watcher  *fsnotify.Watcher
	done     chan struct{}
	changes  chan<- Change
	hash     hash.Hash
	hashes   map[string]Sum
	log      *slog.Logger
}

var watcherUID = rpc.UID{Module: "kernel", Service: "config_watcher"}

// newWatcher returns a new Watcher.
func newWatcher(changes chan<- Change, w *fsnotify.Watcher, dir string, debounce time.Duration, log *slog.Logger) *Watcher {
	if debounce < 0 {
		debounce = FileDebounce
	}
	return &Watcher{
		dir:      dir,
		debounce: debounce,
		watcher:  w,
		changes:  changes,
		log:      log.With(slog.String("component", watcherUID.String())),
		hash:     sha1.New(),
		hashes:   make(map[string]Sum),
	}
}

// init performs an initial scan of the streamProcessor's directory, sending
// create events for all toml files found in the directory.
func (w *Watcher) init(ctx context.Context) (*Watcher, error) {
	de, err := os.ReadDir(w.dir)
	if err != nil {
		return nil, err
	}
	w.done = make(chan struct{})
	go func() {
		defer close(w.done)
		for _, e := range de {
			name := e.Name()
			if filepath.Ext(name) != ".toml" {
				continue
			}

			path := filepath.Join(w.dir, name)
			fi, err := os.Stat(path)
			if err != nil {
				w.changes <- Change{Err: err}
				continue
			}
			if fi.IsDir() {
				continue
			}
			b, err := os.ReadFile(path)
			if err != nil {
				w.log.LogAttrs(ctx, slog.LevelError, "read file", slog.Any("error", err))
				w.changes <- Change{Err: err}
				continue
			}
			cfg, sum, err := unmarshalConfigs(w.hash, b)
			if cfg != nil {
				w.hashes[path] = sum
			}
			w.changes <- Change{
				Event:  []fsnotify.Event{{Name: path, Op: fsnotify.Create}},
				Config: cfg,
				Err:    err,
			}
		}
	}()
	return w, nil
}

// unmarshalConfigs returns a, potentially partial, configuration and its
// semantic hash from the provided raw data.
func unmarshalConfigs(h hash.Hash, b []byte) (cfg *System, sum Sum, _ error) {
	c := &System{}
	err := toml.Unmarshal(b, c)
	if err != nil {
		return nil, sum, err
	}

	paths, deferredErr := Validate(fragmentSchema, c)
	if deferredErr != nil {
		c, err = remove(c, paths, false)
		if err != nil {
			panic(err)
		}
	}

	enc := json.NewEncoder(h)
	if c.Kernel != nil {
		err = enc.Encode(c.Kernel)
		if err != nil {
			return nil, sum, err
		}
		c.Kernel.Sum = (*Sum)(h.Sum(nil))
		h.Reset()
	}
	for name, config := range c.Modules {
		err = enc.Encode(config)
		if err != nil {
			return nil, sum, err
		}
		config.Sum = (*Sum)(h.Sum(nil))
		h.Reset()
		c.Modules[name] = config
	}
	for name, config := range c.Services {
		err = enc.Encode(config)
		if err != nil {
			return nil, sum, err
		}
		config.Sum = (*Sum)(h.Sum(nil))
		h.Reset()
		c.Services[name] = config
	}

	err = enc.Encode(c)
	if err != nil {
		return nil, sum, err
	}
	sum = ([sha1.Size]byte)(h.Sum(nil))
	h.Reset()
	return c, sum, deferredErr
}

// remove removes modules and services in cfg that correspond to invalid
// field paths identified by Vet until no invalid fields are found, and
// returning the result. If safe is true, paths referring to invalid fields
// in the kernel configuration will result in an error. Paths referring to
// the kernel otherwise have no effect. The final result may have no
// configured module or instance.
func remove(cfg *System, paths [][]string, safe bool) (*System, error) {
	if safe {
		for _, p := range paths {
			if len(p) == 0 {
				// Not all cue Errors will have a path,
				// so we may have an empty path here.
				return cfg, errors.New("cannot remove: empty path")
			}
			if p[0] == "kernel" {
				return cfg, errors.New("cannot repair kernel config")
			}
		}
	}
	for _, p := range paths {
		if len(p) < 2 {
			continue
		}
		switch p[0] {
		case kernelName:
			cfg.Kernel = nil
		case moduleName:
			delete(cfg.Modules, p[1])
		case serviceName:
			delete(cfg.Services, p[1])
		}
	}
	return cfg, nil
}
