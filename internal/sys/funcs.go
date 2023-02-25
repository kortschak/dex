// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sys

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/rpc"
)

// Funcs returns an [rpc.Funcs] with a function table for accessing the manager.
//
// The RPC methods in the table are:
//
//   - "system": returns the current [config.System]
func Funcs[K Kernel, D Device[B], B Button](manager *Manager[K, D, B], log *slog.Logger) rpc.Funcs {
	return rpc.Funcs{
		"system": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
			var m rpc.Message[rpc.None]
			err := rpc.UnmarshalMessage(params, &m)
			if err != nil {
				log.LogAttrs(ctx, slog.LevelError, "system", slog.Any("error", err))
				return nil, err
			}
			if id.IsValid() {
				return rpc.NewMessage[any](kernelUID, manager.current), nil
			}
			log.LogAttrs(ctx, slog.LevelInfo, "system config request", slog.Any("config", manager.current))
			return nil, nil
		},
	}
}
