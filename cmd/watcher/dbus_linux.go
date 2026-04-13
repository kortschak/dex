// Copyright ©2026 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"github.com/godbus/dbus/v5"
)

func dbusProperty[T any](conn *dbus.Conn, dest string, path dbus.ObjectPath, prop string) (T, error) {
	var v T
	p, err := conn.Object(dest, path).GetProperty(prop)
	if err != nil {
		return v, err
	}
	err = p.Store(&v)
	return v, err
}

func dbusCall[T any](conn *dbus.Conn, dest string, path dbus.ObjectPath, method string, flags dbus.Flags, args ...any) (T, error) {
	var v T
	err := conn.Object(dest, path).Call(method, flags, args...).Store(&v)
	return v, err
}
