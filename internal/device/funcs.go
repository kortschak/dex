// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package device

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"
)

// DrawMessage is is the RPC message for drawing an image to a device.
type DrawMessage struct {
	Page  string `json:"page"`
	Row   int    `json:"row"`
	Col   int    `json:"col"`
	Image string `json:"image"`
}

// PageMessage is is the RPC message for changing page.
type PageMessage struct {
	Page string `json:"page"`
	// The service owning the device to request
	// the page change on. If nil, query the
	// calling manager's service.
	Service *rpc.UID `json:"service"`
}

// PageStateMessage is is the RPC message for listing pages.
type PageStateMessage struct {
	// The service owning the device to request
	// the page list from. If nil, query the
	// calling manager's service.
	Service *rpc.UID `json:"service"`
}

// BrightnessMessage is is the RPC message for getting or setting the brightness
// of a device.
type BrightnessMessage struct {
	// Valid actions are "add", "get" and "set"
	Action string `json:"action"`
	// Absolute brightness for "get" and "set".
	// Relative for "add", use a negative value
	// to reduce brightness.
	Brightness int `json:"brightness"`
}

// SleepMessage is is the RPC message for setting a device's sleep mode.
type SleepMessage struct {
	// Valid actions are "wake", "sleep" and "clear"
	State string `json:"sleep"`
}

// Funcs returns an [rpc.Funcs] with a function table for accessing a store
// held by the manager.
//
// The RPC methods in the table are:
//
//   - "draw": see [sys.Page.Button]/[DecodeImage]/[sys.Button.Draw] and [DrawMessage]
//   - "page": see [Controller.SetDisplayTo] and [PageMessage]
//   - "page_names": see [Controller.PageNames] and [PageStateMessage], returns []string
//   - "page_details": see [Controller.PageNames] and [PageStateMessage], returns map[string][]config.Button
//   - "brightness": see [sys.Device.SetBrightness] and [BrightnessMessage]
//   - "sleep": see [sys.Device.Wake]/[sys.Device.Sleep]/[sys.Device.Clear] and [SleepMessage]
func Funcs[K sys.Kernel, D sys.Device[B], B sys.Button](manager *sys.Manager[K, D, B], log *slog.Logger) rpc.Funcs {
	store := manager.Store()
	return rpc.Funcs{
		"draw": func(ctx context.Context, id jsonrpc2.ID, msg json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[DrawMessage]
			err := rpc.UnmarshalMessage(msg, &m)
			if err != nil {
				return nil, err
			}

			dev, err := manager.DeviceFor(m.UID)
			if err != nil {
				return nil, err
			}
			p, ok := dev.Page(m.Body.Page)
			if !ok {
				return nil, fmt.Errorf("no page %s for %s", m.Body.Page, m.UID)
			}
			rows, cols := dev.Layout()
			if m.Body.Row < 0 || rows <= m.Body.Row {
				return nil, fmt.Errorf("row out of bound: %d", m.Body.Row)
			}
			if m.Body.Col < 0 || cols <= m.Body.Col {
				return nil, fmt.Errorf("column out of bound: %d", m.Body.Col)
			}
			bounds, err := dev.Bounds()
			if err != nil {
				return nil, fmt.Errorf("no bounds for %s: %v", m.UID, err)
			}
			img, err := DecodeImage(bounds, m.Body.Image, filepath.Join(manager.Datadir(), m.UID.Module))
			if !ok {
				return nil, fmt.Errorf("%w for %s", err, m.UID)
			}
			p.Button(m.Body.Row, m.Body.Col).Draw(ctx, img)
			var resp *rpc.Message[any]
			if id.IsValid() {
				resp = rpc.NewMessage[any](kernelUID, "ok")
			}
			return resp, nil
		},

		"page": func(ctx context.Context, id jsonrpc2.ID, msg json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[PageMessage]
			err := rpc.UnmarshalMessage(msg, &m)
			if err != nil {
				return nil, err
			}

			uid := m.UID
			if m.Body.Service != nil {
				uid = *m.Body.Service
			}
			dev, err := manager.DeviceFor(uid)
			if err != nil {
				return nil, err
			}
			err = dev.SetDisplayTo(ctx, m.Body.Page)
			var resp *rpc.Message[any]
			if err == nil && id.IsValid() {
				resp = rpc.NewMessage[any](kernelUID, "ok")
			}
			return resp, err
		},

		"page_names": func(ctx context.Context, id jsonrpc2.ID, msg json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[PageStateMessage]
			err := rpc.UnmarshalMessage(msg, &m)
			if err != nil {
				return nil, err
			}

			uid := m.UID
			if m.Body.Service != nil {
				uid = *m.Body.Service
			}
			dev, err := manager.DeviceFor(uid)
			if err != nil {
				return nil, err
			}
			if id.IsValid() {
				return rpc.NewMessage[any](kernelUID, dev.PageNames()), nil
			}
			log.LogAttrs(ctx, slog.LevelInfo, "page list request", slog.Any("service", uid), slog.Any("pages", dev.PageNames()))
			return nil, nil
		},

		"page_details": func(ctx context.Context, id jsonrpc2.ID, msg json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[PageStateMessage]
			err := rpc.UnmarshalMessage(msg, &m)
			if err != nil {
				return nil, err
			}

			uid := m.UID
			if m.Body.Service != nil {
				uid = *m.Body.Service
			}
			dev, err := manager.DeviceFor(uid)
			if err != nil {
				return nil, err
			}
			if id.IsValid() {
				return rpc.NewMessage[any](kernelUID, dev.PageDetails()), nil
			}
			log.LogAttrs(ctx, slog.LevelInfo, "page details request", slog.Any("service", uid), slog.Any("pages", dev.PageDetails()))
			return nil, nil
		},

		"brightness": func(ctx context.Context, id jsonrpc2.ID, msg json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[BrightnessMessage]
			err := rpc.UnmarshalMessage(msg, &m)
			if err != nil {
				return nil, err
			}
			switch m.Body.Action {
			case "get", "add":
				if store == nil {
					return nil, errors.New("no store")
				}
			}

			dev, err := manager.DeviceFor(m.UID)
			if err != nil {
				return nil, err
			}
			devUID := rpc.UID{Module: "kernel", Service: dev.Serial()}
			switch m.Body.Action {
			case "get", "add":
				var val []byte
				val, err = store.Get(devUID, "brightness")
				if err != nil {
					break
				}
				if len(val) != 1 {
					err = fmt.Errorf("unexpected brightness value length: %d != 1", len(val))
					break
				}
				if m.Body.Action == "get" {
					return rpc.NewMessage[any](kernelUID, int(val[0])), nil
				}
				m.Body.Brightness += int(val[0])
				switch {
				case m.Body.Brightness < 0:
					m.Body.Brightness = 0
				case m.Body.Brightness > 100:
					m.Body.Brightness = 100
				}
				fallthrough
			case "set":
				if m.Body.Brightness < 0 || 100 < m.Body.Brightness {
					return nil, fmt.Errorf("brightness out of bounds: %d", m.Body.Brightness)
				}
				err = dev.SetBrightness(m.Body.Brightness)
				if err == nil {
					if store != nil {
						store.Set(devUID, "brightness", []byte{byte(m.Body.Brightness)})
					}
				}
			default:
				err = jsonrpc2.ErrInvalidParams
			}
			var resp *rpc.Message[any]
			if err == nil && id.IsValid() {
				resp = rpc.NewMessage[any](kernelUID, "ok")
			}
			return resp, err
		},

		"sleep": func(ctx context.Context, id jsonrpc2.ID, msg json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[SleepMessage]
			err := rpc.UnmarshalMessage(msg, &m)
			if err != nil {
				return nil, err
			}

			dev, err := manager.DeviceFor(m.UID)
			if err != nil {
				return nil, err
			}
			switch m.Body.State {
			case "wake":
				dev.Wake(ctx)
			case "sleep":
				err = dev.Sleep()
			case "clear":
				err = dev.Clear()
			default:
				return nil, fmt.Errorf("invalid state request: %q", m.Body.State)
			}
			var resp *rpc.Message[any]
			if err == nil && id.IsValid() {
				resp = rpc.NewMessage[any](kernelUID, "ok")
			}
			return resp, err
		},
	}
}
