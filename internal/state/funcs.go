// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package state

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/sys"
	"github.com/kortschak/dex/rpc"
)

// SetMessage is the RPC message for setting a value into the store.
type SetMessage struct {
	Item  string `json:"item"`
	Value []byte `json:"value"`
}

// GetMessage is the RPC message for getting a value from the store.
type GetMessage struct {
	Item string `json:"item"`
}

// GetResult is the result of a GetMessage RPC call.
type GetResult struct {
	Value []byte `json:"value"`
}

// PutMessage is the RPC message for putting a value into the store.
type PutMessage struct {
	Item  string `json:"item"`
	Value []byte `json:"value"`
}

// PutResult is the result of a PutMessage call.
type PutResult struct {
	Value   []byte `json:"value"`
	Written bool   `json:"written"`
}

// Delete message is the RPC message for deleting an item in the store.
type DeleteMessage struct {
	Item string `json:"item"`
}

// Funcs returns an [rpc.Funcs] with a function table for accessing a store
// held by the manager.
//
// The RPC methods in the table are:
//
//   - "get": see [DB.Get] and [GetMessage]/[GetResult]
//   - "set": see [DB.Set] and [SetMessage]
//   - "put": see [DB.Put] and [PutMessage]/[PutResult]
//   - "delete": [DB.Delete] and [DeleteMessage]
//   - "drop": [DB.Drop]
//   - "drop_module": [DB.DropModule]
//
// "drop" and "drop_module" expect [rpc.None] as the call message body.
func Funcs[K sys.Kernel, D sys.Device[B], B sys.Button](manager *sys.Manager[K, D, B], log *slog.Logger) rpc.Funcs {
	store := manager.Store()
	storeUID := rpc.UID{Module: "kernel", Service: "store"}
	return rpc.Funcs{
		// Set(owner rpc.UID, item string, val []byte) error
		"set": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[SetMessage]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "set", slog.Any("error", err))
				return nil, err
			}
			err = store.Set(m.UID, m.Body.Item, m.Body.Value)
			if err != nil {
				return nil, rpc.NewError(rpc.ErrCodeInternal,
					err.Error(),
					map[string]any{
						"type": rpc.ErrCodeStoreErr,
						"op":   "set",
						"key":  m.Body.Item,
						"uid":  m.UID,
					},
				)
			}
			return nil, nil
		},

		// Get(owner rpc.UID, item string) (val []byte, err error)
		"get": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[GetMessage]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "get", slog.Any("error", err))
				return nil, err
			}
			val, err := store.Get(m.UID, m.Body.Item)
			if err != nil {
				code := int64(rpc.ErrCodeNotFound)
				data := map[string]any{
					"op":  "get",
					"key": m.Body.Item,
					"uid": m.UID,
				}
				if err != sys.ErrNotFound {
					code = rpc.ErrCodeInternal
					data["type"] = rpc.ErrCodeStoreErr
				}
				return nil, rpc.NewError(code,
					err.Error(),
					data,
				)
			}
			return rpc.NewMessage[any](storeUID, GetResult{val}), nil
		},

		// Put(owner rpc.UID, item string, new []byte) (old []byte, written bool, err error)
		"put": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[PutMessage]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "put", slog.Any("error", err))
				return nil, err
			}
			val, written, err := store.Put(m.UID, m.Body.Item, m.Body.Value)
			if err != nil {
				return nil, rpc.NewError(rpc.ErrCodeInternal,
					err.Error(),
					map[string]any{
						"type": rpc.ErrCodeStoreErr,
						"op":   "put",
						"key":  m.Body.Item,
						"uid":  m.UID,
					},
				)
			}
			return rpc.NewMessage[any](storeUID, PutResult{val, written}), nil
		},

		// Delete(owner rpc.UID, item string) error
		"delete": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[DeleteMessage]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "delete", slog.Any("error", err))
				return nil, err
			}
			err = store.Delete(m.UID, m.Body.Item)
			if err != nil {
				return nil, rpc.NewError(rpc.ErrCodeInternal,
					err.Error(),
					map[string]any{
						"type": rpc.ErrCodeStoreErr,
						"op":   "delete",
						"key":  m.Body.Item,
						"uid":  m.UID,
					},
				)
			}
			return nil, nil
		},

		// Drop(owner rpc.UID) error
		"drop": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[rpc.None]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "drop", slog.Any("error", err))
				return nil, err
			}
			err = store.Drop(m.UID)
			if err != nil {
				return nil, rpc.NewError(rpc.ErrCodeInternal,
					err.Error(),
					map[string]any{
						"type": rpc.ErrCodeStoreErr,
						"op":   "drop",
						"uid":  m.UID,
					},
				)
			}
			return nil, nil
		},

		// DropModule(module string) error
		"drop_module": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[rpc.None]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "drop_module", slog.Any("error", err))
				return nil, err
			}
			err = store.DropModule(m.UID.Module)
			if err != nil {
				return nil, rpc.NewError(rpc.ErrCodeInternal,
					err.Error(),
					map[string]any{
						"type": rpc.ErrCodeStoreErr,
						"op":   "drop_module",
						"uid":  m.UID.Module,
					},
				)
			}
			return nil, err
		},
	}
}
