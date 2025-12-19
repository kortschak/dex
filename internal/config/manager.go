// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package config

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"hash"
	"io/fs"
	"log/slog"
	"sort"

	"cuelang.org/go/cue"
	"cuelang.org/go/cue/cuecontext"
	cerrors "cuelang.org/go/cue/errors"
	"cuelang.org/go/encoding/gocode/gocodec"
	"github.com/fsnotify/fsnotify"

	"github.com/kortschak/dex/config"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

// Manager is a configurations stream manager. It holds a progressive
// configuration state constructed from applying a sequence of configuration
// changes.
type Manager struct {
	fragments map[string]*System
	hash      hash.Hash
	log       *slog.Logger
}

var managerUID = rpc.UID{Module: "kernel", Service: "config_manager"}

// NewManager returns a new Manager.
func NewManager(log *slog.Logger) *Manager {
	return &Manager{
		fragments: make(map[string]*System),
		hash:      sha1.New(),
		log:       log.With(slog.String("component", managerUID.String())),
	}
}

// Apply applies the provided change to the current configuration state. Any
// error returned will be fs.PathError.
func (m *Manager) Apply(c Change) error {
	ctx := context.Background()
	m.log.LogAttrs(ctx, slog.LevelDebug, "apply", slog.Any("op", slogext.Stringer{Stringer: c.Op()}))
	for _, ev := range c.Event {
		switch {
		case ev.Has(fsnotify.Write):
			m.log.LogAttrs(ctx, slog.LevelDebug, "apply write", slog.Any("change", changeValue{c}))
			_, ok := m.fragments[ev.Name]
			m.log.LogAttrs(ctx, slog.LevelInfo, "apply write", slog.Bool("exists", ok))
			m.fragments[ev.Name] = c.Config

		case ev.Has(fsnotify.Rename):
			m.log.LogAttrs(ctx, slog.LevelDebug, "apply rename", slog.Any("change", changeValue{c}))
			if _, ok := m.fragments[ev.Name]; !ok {
				return &fs.PathError{Op: "rename", Path: ev.Name, Err: fs.ErrNotExist}
			}
			delete(m.fragments, ev.Name)

		case ev.Has(fsnotify.Create):
			m.log.LogAttrs(ctx, slog.LevelDebug, "apply create", slog.Any("change", changeValue{c}))
			if _, ok := m.fragments[ev.Name]; ok {
				return &fs.PathError{Op: "create", Path: ev.Name, Err: fs.ErrExist}
			}
			m.fragments[ev.Name] = c.Config

		case ev.Has(fsnotify.Remove):
			m.log.LogAttrs(ctx, slog.LevelDebug, "apply remove", slog.Any("change", changeValue{c}))
			if _, ok := m.fragments[ev.Name]; !ok {
				return &fs.PathError{Op: "remove", Path: ev.Name, Err: fs.ErrNotExist}
			}
			delete(m.fragments, ev.Name)
		}
	}
	return nil
}

// Unify returns a complete unified configuration validated against the provided
// CUE schema. The configuration is returned as both a *Config and a cue.Value
// to allow inspection of incomplete unification. The names of files that are
// included and those that remain to be included is also returned.
func (m *Manager) Unify(schema string) (cfg *System, val cue.Value, included, remain []string, err error) {
	ctx := cuecontext.New()

	u := ctx.CompileString(schema)
	codec := gocodec.New(ctx, nil)

	paths := make([]string, 0, len(m.fragments))
	for p := range m.fragments {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for i, p := range paths {
		if m.fragments[p] == nil {
			continue
		}
		w, err := codec.Decode(desum(m.fragments[p]))
		if err != nil {
			return nil, u, paths[:i], paths[i:], err
		}
		u = u.Unify(w)
		err = u.Validate()
		if err != nil {
			return nil, u, paths[:i], paths[i:], err
		}
	}
	var c System
	err = codec.Encode(u, &c)
	if err != nil {
		return nil, u, paths, nil, err
	}
	sum, err := resum(m.hash, &c)
	if err != nil {
		return nil, u, paths, nil, err
	}
	u, err = codec.Decode(&c)
	if err != nil {
		panic(fmt.Errorf("internal inconsistency: %v", err))
	}
	m.log.LogAttrs(context.Background(), slog.LevelDebug, "unified config", slog.Any("sum", slogext.Stringer{Stringer: &sum}))
	return &c, u, paths, nil, nil
}

// TODO: Instead of zeroing, can we just ignore the inconsistencies?
// See https://github.com/cue-lang/cue/issues/2300.
func desum(c *System) *System {
	var dst System
	if c.Kernel != nil {
		k := *c.Kernel
		k.Sum = nil
		dst.Kernel = &k
	}
	if c.Modules != nil {
		dst.Modules = make(map[string]*Module)
	}
	for name, config := range c.Modules {
		if config == nil {
			dst.Modules[name] = nil
			continue
		}
		m := *config
		m.Sum = nil
		dst.Modules[name] = &m
	}
	if c.Services != nil {
		dst.Services = make(map[string]*Service)
	}
	for name, config := range c.Services {
		if config == nil {
			dst.Services[name] = nil
			continue
		}
		m := *config
		m.Sum = nil
		dst.Services[name] = &m
	}
	return &dst
}

func resum(h hash.Hash, c *System) (sum Sum, err error) {
	enc := json.NewEncoder(h)
	if c.Kernel != nil {
		err = enc.Encode(c.Kernel)
		if err != nil {
			return sum, err
		}
		c.Kernel.Sum = (*Sum)(h.Sum(nil))
		h.Reset()
	}
	for name, config := range c.Modules {
		err = enc.Encode(config)
		if err != nil {
			return sum, err
		}
		config.Sum = (*Sum)(h.Sum(nil))
		h.Reset()
		c.Modules[name] = config
	}
	for name, config := range c.Services {
		err = enc.Encode(config)
		if err != nil {
			return sum, err
		}
		config.Sum = (*Sum)(h.Sum(nil))
		h.Reset()
		c.Services[name] = config
	}

	err = enc.Encode(c)
	if err != nil {
		return sum, err
	}
	sum = ([sha1.Size]byte)(h.Sum(nil))
	h.Reset()
	return sum, nil
}

// Fragments returns the currently held configuration fragments. It is intended
// only for debugging.
func (m *Manager) Fragments() map[string]*System { return m.fragments }

// Vet performs a validation of the provided configuration, returning a list
// of invalid paths and a CUE errors.Error explaining the issues found if
// the configuration is invalid. Vet uses Validate with the config.ModuleDependency
// schema to globally validate module dependencies and then for each service
// using either the modules custom scheme held in the Module.Schema field, or
// if that is empty, the config.DeviceDependency schema.
func Vet(cfg *System) (paths [][]string, err error) {
	var deferredError error

	// Globally validation.
	p, err := Validate(config.Schema, cfg)
	if err != nil {
		return p, err
	}

	// Globally validate module dependencies.
	p, err = Validate(config.ModuleDependency, System{
		Modules:  cfg.Modules,
		Services: cfg.Services,
	})
	if err != nil {
		paths = append(paths, p...)
		deferredError = appendErr(deferredError, err)
	}

	// Check each service for device dependency and
	// module-specific constraints.
	for name, c := range cfg.Services {
		schema := config.DeviceDependency
		if c.Module != nil {
			m, ok := cfg.Modules[*c.Module]
			if ok && m.Schema != "" {
				schema = m.Schema
			}
		}
		p, err = Validate(schema, System{
			Kernel:   &Kernel{Device: cfg.Kernel.Device},
			Modules:  cfg.Modules,
			Services: map[string]*Service{name: c},
		})
		if err != nil {
			paths = append(paths, p...)
			deferredError = appendErr(deferredError, err)
			continue
		}
	}
	return unique(paths), deferredError
}

// Repair removes modules and services in cfg that correspond to invalid
// field paths identified by Vet until no invalid fields are found, and
// returning the result. Paths referring to invalid fields in the kernel
// configuration will result in an error. The final result may have no
// configured module or service.
func Repair(cfg *System, paths [][]string) (*System, error) {
	for {
		// This loop is expected to only iterate at most twice.
		// If a module is removed that has dependent services
		// the second iteration will find the orphan service and
		// remove it, leaving a working config. At very most a
		// pathological set of schemas could possibly be able to
		// cause iteration for each module and service.
		cfg, err := remove(cfg, paths, true)
		if err != nil {
			return cfg, err
		}
		paths, err = Vet(cfg)
		if err == nil {
			return cfg, nil
		}
		if len(paths) == 0 {
			panic("nothing to remove")
		}
	}
}

func appendErr(dst, next error) error {
	if dst == nil {
		return next
	}
	return cerrors.Append(
		cerrors.Promote(dst, ""),
		cerrors.Promote(next, ""),
	)
}
