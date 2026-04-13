// Copyright ©2026 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/godbus/dbus/v5"
)

// logindSession queries the session lock state from systemd-logind.
// The LockedHint property on the session reflects whether the session
// is locked, and unlike the screensaver active state, it is not
// cleared when the unlock dialog is shown.
type logindSession struct {
	mu   sync.Mutex
	conn *dbus.Conn
	path dbus.ObjectPath
}

func newLogindSession() (*logindSession, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to system bus: %w", err)
	}
	path, err := findSession(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return &logindSession{conn: conn, path: path}, nil
}

const logind = "org.freedesktop.login1"

// findSession resolves the current user's graphical session. It tries,
// in order: the $XDG_SESSION_ID environment variable, the process's own
// session, and the user's display session. The last fallback covers the
// common case where dex runs as a systemd user service that is not part
// of any logind session.
func findSession(conn *dbus.Conn) (dbus.ObjectPath, error) {
	var path dbus.ObjectPath

	man := conn.Object(logind, "/org/freedesktop/login1")
	if id := os.Getenv("XDG_SESSION_ID"); id != "" {
		err := man.Call(logind+".Manager.GetSession", 0, id).Store(&path)
		if err == nil {
			return path, nil
		}
	}
	err := man.Call(logind+".Manager.GetSessionByPID", 0, uint32(os.Getpid())).Store(&path)
	if err == nil {
		return path, nil
	}
	return userDisplaySession(conn, man)
}

// userDisplaySession returns the graphical session for the current user
// via the logind User object's Display property.
func userDisplaySession(conn *dbus.Conn, man dbus.BusObject) (dbus.ObjectPath, error) {
	var user dbus.ObjectPath
	err := man.Call(logind+".Manager.GetUser", 0, uint32(os.Getuid())).Store(&user)
	if err != nil {
		return "", fmt.Errorf("getting user: %w", err)
	}
	v, err := conn.Object(logind, user).GetProperty(logind + ".User.Display")
	if err != nil {
		return "", fmt.Errorf("getting display session: %w", err)
	}
	// Display is a two-element struct: session ID and object path.
	vals, ok := v.Value().([]any)
	if !ok || len(vals) != 2 {
		return "", fmt.Errorf("unexpected type for Display: %T", v.Value())
	}
	sess, ok := vals[1].(dbus.ObjectPath)
	if !ok {
		return "", fmt.Errorf("unexpected type for Display path: %T", vals[1])
	}
	if sess == "" || sess == "/" {
		return "", fmt.Errorf("no display session for uid %d", os.Getuid())
	}
	return sess, nil
}

func (s *logindSession) locked() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return false, errors.New("closed")
	}
	return dbusProperty[bool](s.conn,
		"org.freedesktop.login1",
		s.path,
		"org.freedesktop.login1.Session.LockedHint",
	)
}

func (s *logindSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn == nil {
		return nil
	}
	err := s.conn.Close()
	s.conn = nil
	return err
}

// sessionLocked returns the session lock state from logind if
// available, falling back to the provided value.
func sessionLocked(logind *logindSession, fallback bool) (bool, error) {
	if logind == nil {
		return fallback, nil
	}
	locked, err := logind.locked()
	if err != nil {
		return fallback, err
	}
	return locked, nil
}
