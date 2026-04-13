// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
)

func init() {
	detailers[(&mutterDetailer{}).strategy()] = newMutterDetailer
}

func newMutterDetailer() (detailer, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return noDetails{}, err
	}
	logind, err := newLogindSession()
	if err != nil {
		err = warning{err}
	}
	return &mutterDetailer{conn: conn, logind: logind, last: time.Now()}, err
}

type mutterDetailer struct {
	mu     sync.Mutex
	conn   *dbus.Conn
	logind *logindSession
	last   time.Time
}

func (*mutterDetailer) strategy() string { return "gnome/mutter" }

func (d *mutterDetailer) Close() error {
	d.mu.Lock()
	var err error
	if d.conn != nil {
		err = d.conn.Close()
		d.conn = nil
	}
	if d.logind != nil {
		err = errors.Join(err, d.logind.Close())
		d.logind = nil
	}
	d.mu.Unlock()
	return err
}

func (d *mutterDetailer) details() (watcher.Details, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.conn == nil {
		return watcher.Details{}, errClosedDetailer
	}

	active, errActive := dbusCall[bool](d.conn,
		"org.gnome.ScreenSaver",
		"/org/gnome/ScreenSaver",
		"org.gnome.ScreenSaver.GetActive", 0,
	)

	// Use logind LockedHint as the authoritative session lock
	// state when available. The ScreenSaver active state does
	// not distinguish between the password dialog being shown
	// and the session being unlocked.
	locked, errLocked := sessionLocked(d.logind, active)

	if active || locked {
		// Extensions are not available when the screen
		// shield is active or the session is locked, so
		// don't try; just get idle time from Mutter.
		last, errTime := d.mutterIdleMonitor()
		if !last.IsZero() {
			d.last = last
		}
		return watcher.Details{
			LastInput: d.last,
			Locked:    locked,
		}, errors.Join(errActive, errLocked, errTime)
	}

	detMsg, errWindow := dbusCall[string](d.conn,
		"org.gnome.Shell",
		"/org/gnome/Shell/Extensions/UserActivity",
		"org.gnome.Shell.Extensions.UserActivity.Details", 0,
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
		}, errors.Join(errActive, errLocked, errWindow, errTime)
	}
	var det watcher.Details
	errWindow = json.Unmarshal([]byte(detMsg), &det)
	if !det.LastInput.IsZero() {
		d.last = det.LastInput
	}
	det.Locked = false
	return det, errors.Join(errActive, errLocked, errWindow)
}

func (d *mutterDetailer) mutterIdleMonitor() (time.Time, error) {
	idle, err := dbusCall[int64](d.conn,
		"org.gnome.Mutter.IdleMonitor",
		"/org/gnome/Mutter/IdleMonitor/Core",
		"org.gnome.Mutter.IdleMonitor.GetIdletime", 0,
	)
	if idle < 0 {
		return time.Time{}, err
	}
	return time.Now().Add(time.Duration(idle) * -time.Millisecond).Round(time.Second / 10), err
}
