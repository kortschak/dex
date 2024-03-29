// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/godbus/dbus/v5"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
)

func init() {
	for _, s := range (&mutterDetailer{}).strategy() {
		detailers[s] = newMutterDetailer
	}
}

func newMutterDetailer() (detailer, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return noDetails{}, err
	}
	return &mutterDetailer{conn: conn, last: time.Now()}, nil
}

type mutterDetailer struct {
	conn *dbus.Conn
	last time.Time
}

func (*mutterDetailer) strategy() []string { return []string{"gnome/mutter"} }

func (d *mutterDetailer) Close() error {
	err := d.conn.Close()
	d.conn = nil
	return err
}

func (d *mutterDetailer) details() (watcher.Details, error) {
	locked, errLocked := dbusCall[bool](d.conn,
		"org.gnome.ScreenSaver",
		"/org/gnome/ScreenSaver",
		"org.gnome.ScreenSaver.GetActive",
	)

	if locked {
		// Extensions are not available when locked, so
		// don't try; just get idle time from Mutter.
		last, errTime := d.mutterIdleMonitor()
		if !last.IsZero() {
			d.last = last
		}
		return watcher.Details{
			LastInput: d.last,
			Locked:    true,
		}, errors.Join(errLocked, errTime)
	}

	detMsg, errWindow := dbusCall[string](d.conn,
		"org.gnome.Shell",
		"/org/gnome/Shell/Extensions/UserActivity",
		"org.gnome.Shell.Extensions.UserActivity.Details",
	)
	if errWindow != nil {
		// If the user activity call failed, get last
		// activity from Mutter. This will either be
		// because of a race between locking and the
		// user activity call, or not having the user
		// activity extension installed. Assume the
		// extension is installed, and so that the
		// screen has been locked.
		last, errTime := d.mutterIdleMonitor()
		if !last.IsZero() {
			d.last = last
		}
		return watcher.Details{
			LastInput: d.last,
			Locked:    true,
		}, errors.Join(errLocked, errWindow, errTime)
	}
	var det struct {
		WindowID   int       `json:"wid"`
		Name       string    `json:"name"`
		Class      string    `json:"class"`
		WindowName string    `json:"window"`
		LastInput  time.Time `json:"last_input,omitempty"`
		Locked     bool      `json:"locked"`
	}
	errWindow = json.Unmarshal([]byte(detMsg), &det)
	if !det.LastInput.IsZero() {
		d.last = det.LastInput
	}
	det.Locked = false
	return watcher.Details(det), errors.Join(errLocked, errWindow)
}

func (d *mutterDetailer) mutterIdleMonitor() (time.Time, error) {
	idle, err := dbusCall[int64](d.conn,
		"org.gnome.Mutter.IdleMonitor",
		"/org/gnome/Mutter/IdleMonitor/Core",
		"org.gnome.Mutter.IdleMonitor.GetIdletime",
	)
	if idle < 0 {
		return time.Time{}, err
	}
	return time.Now().Add(time.Duration(idle) * -time.Millisecond).Round(time.Second / 10), err
}

func dbusCall[T any](conn *dbus.Conn, dest, path, method string) (T, error) {
	var v T
	c := conn.Object(dest, dbus.ObjectPath(path)).Call(method, 0)
	err := c.Store(&v)
	if err != nil {
		return v, err
	}
	return v, nil
}
