// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package api

import (
	"testing"
	"testing/quick"
	"time"

	"github.com/kortschak/dex/config"
)

func TestConfig(t *testing.T) {
	err := quick.Check(func(c Config) bool {
		if c.Options.Heartbeat != nil {
			c.Options.Heartbeat.Duration = c.Options.Heartbeat.Round(time.Millisecond)
		}
		if len(c.Options.Rules) == 0 {
			c.Options.Rules = nil
		}
		if c.Options.Web != nil && len(c.Options.Web.Rules) == 0 {
			c.Options.Web.Rules = nil
		}
		_, err := config.RoundTrip[config.Module](c)
		if err != nil {
			t.Log(err)
		}
		return err == nil
	}, nil)
	if err != nil {
		t.Error(err)
	}
}
