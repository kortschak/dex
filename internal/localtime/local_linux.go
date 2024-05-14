// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package localtime

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// Dynamic is a handle to obtain the current local timezone via the system
// DBus. It is safe for concurrent use.
type Dynamic struct {
	mu   sync.Mutex
	conn *dbus.Conn
}

// NewDynamic returns a Location for the current system local timezone.
func NewDynamic() (*Dynamic, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, err
	}
	return &Dynamic{conn: conn}, nil
}

// Location returns the current system local timezone. If Location fails it
// returns time.UTC and a non-nil error.
func (d *Dynamic) Location() (*time.Location, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return time.UTC, errors.New("closed")
	}

	tz, err := dbusProperty[string](d.conn, "org.freedesktop.timedate1", "/org/freedesktop/timedate1", "org.freedesktop.timedate1.Timezone")
	if err != nil {
		return time.UTC, fmt.Errorf("could not get time zone: %w", err)
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC, err
	}
	return loc, nil
}

// Close releases the connection to the system DBus.
func (d *Dynamic) Close() error {
	d.mu.Lock()
	var err error
	if d.conn != nil {
		err = d.conn.Close()
		d.conn = nil
	}
	d.mu.Unlock()
	return err
}

func dbusProperty[T any](conn *dbus.Conn, dest, path, property string) (T, error) {
	var v T
	p, err := conn.Object(dest, dbus.ObjectPath(path)).GetProperty(property)
	if err != nil {
		return v, err
	}
	v, ok := p.Value().(T)
	if !ok {
		return v, fmt.Errorf("invalid type for %T", p.Value())
	}
	return v, nil
}
