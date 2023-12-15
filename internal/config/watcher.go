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

// FileDebounce is the default duration we wait for the the contents to have
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

// Watch starts an fsnotify.Watcher for the provided directory, sending change
// events on the changes channel. If dir is deleted, it is recreated as a new
// directory and a new watcher is set. The debounce parameter specifies how
// long to wait after an fsnotify.Event before reading the file to ensure that
// writes will be reflected in the state checksum. If it is less than zero,
// FileDebounce is used.
func Watch(ctx context.Context, dir string, changes chan<- Change, debounce time.Duration, log *slog.Logger) error {
	defer close(changes)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	_, err = os.Stat(dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		err = os.Mkdir(dir, 0o755)
		if err != nil {
			return err
		}
	}

	err = watcher.Add(dir)
	if err != nil {
		return err
	}

	p, err := newStreamProcessor(changes, dir, debounce, log).init(ctx)
	if err != nil {
		return err
	}

	return p.process(ctx, watcher)
}

// streamProcessor collects raw fsnotify.Events and aggregates and filters
// for semantically meaningful configuration changes.
type streamProcessor struct {
	dir      string
	debounce time.Duration
	changes  chan<- Change
	hash     hash.Hash
	hashes   map[string]Sum
	log      *slog.Logger
}

var watcherUID = rpc.UID{Module: "kernel", Service: "config_watcher"}

// newStreamProcessor returns a new streamProcessor.
func newStreamProcessor(changes chan<- Change, dir string, debounce time.Duration, log *slog.Logger) *streamProcessor {
	if debounce < 0 {
		debounce = FileDebounce
	}
	return &streamProcessor{
		dir:      dir,
		debounce: debounce,
		changes:  changes,
		log:      log.With(slog.String("component", watcherUID.String())),
		hash:     sha1.New(),
		hashes:   make(map[string]Sum),
	}
}

// init performs an initial scan of the streamProcessor's directory, sending
// create events for all toml files found in the directory.
func (p *streamProcessor) init(ctx context.Context) (*streamProcessor, error) {
	de, err := os.ReadDir(p.dir)
	if err != nil {
		return nil, err
	}
	for _, e := range de {
		name := e.Name()
		if filepath.Ext(name) != ".toml" {
			continue
		}

		path := filepath.Join(p.dir, name)
		fi, err := os.Stat(path)
		if err != nil {
			p.changes <- Change{Err: err}
			continue
		}
		if fi.IsDir() {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			p.log.LogAttrs(ctx, slog.LevelError, "read file", slog.Any("error", err))
			p.changes <- Change{Err: err}
			continue
		}
		cfg, sum, err := unmarshalConfigs(p.hash, b)
		if cfg != nil {
			p.hashes[path] = sum
		}
		p.changes <- Change{
			Event:  []fsnotify.Event{{Name: path, Op: fsnotify.Create}},
			Config: cfg,
			Err:    err,
		}
	}

	return p, nil
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
