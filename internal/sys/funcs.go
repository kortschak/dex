// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package sys

import (
	"context"
	"encoding/json"
	"log/slog"
	"sort"

	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/internal/private"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/version"
	"github.com/kortschak/dex/rpc"
)

// Funcs returns an [rpc.Funcs] with a function table for accessing the manager.
//
// The RPC methods in the table are:
//
//   - "system": returns the current [config.System]
//
// If redact is true, fields marked with private will be redacted using
// [private.Redact].
func Funcs[K Kernel, D Device[B], B Button](redact bool) func(manager *Manager[K, D, B], log *slog.Logger) rpc.Funcs {
	return func(manager *Manager[K, D, B], log *slog.Logger) rpc.Funcs {
		return rpc.Funcs{
			"system": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
				var m rpc.Message[rpc.None]
				err := rpc.UnmarshalMessage(params, &m)
				if err != nil {
					log.LogAttrs(ctx, slog.LevelError, "system", slog.Any("error", err))
					return nil, err
				}
				current := *manager.current
				kernel := *current.Kernel
				for m := range manager.missingSerial {
					kernel.Missing = append(kernel.Missing, m)
				}
				sort.Strings(kernel.Missing)
				current.Kernel = &kernel
				current.Version, err = version.String()
				if err != nil {
					current.Version = err.Error()
				}
				if id.IsValid() {
					if redact {
						current, err = private.Redact(current, "json")
						if err != nil {
							return nil, rpc.NewError(rpc.ErrCodeInternal,
								err.Error(),
								map[string]any{
									"uid": m.UID,
								},
							)
						}
					}
					return rpc.NewMessage[any](kernelUID, current), nil
				}
				log.LogAttrs(ctx, slog.LevelInfo, "system config request", slog.Any("config", slogext.PrivateRedact{Val: current, Tag: "json"}))
				return nil, nil
			},
		}
	}
}
