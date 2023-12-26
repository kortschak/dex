// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:generate go run generate_tests.go

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
)

func main() {
	const dataDate = "2023-12-31" // Centre point of data.

	cases := []struct {
		tzName string
		tz     string
	}{
		{tz: "UTC", tzName: "utc"},
		{tz: "+10:30", tzName: "east"},
		{tz: "-10:30", tzName: "west"},
	}

	type pattern []struct {
		Duration time.Duration  `json:"duration,omitempty"`
		AFK      map[string]any `json:"afk,omitempty"`
		Window   map[string]any `json:"window,omitempty"`
	}
	template := struct {
		Buckets map[string]worklog.BucketMetadata `json:"buckets"`
		Offsets []time.Duration                   `json:"offsets"`
		Pattern pattern                           `json:"pattern"`
	}{
		Buckets: map[string]worklog.BucketMetadata{
			"afk": worklog.BucketMetadata{
				ID:       "afk_localhost",
				Name:     "afk",
				Type:     "afk",
				Client:   "worklog",
				Hostname: "localhost",
			},
			"window": worklog.BucketMetadata{
				ID:       "window_localhost",
				Name:     "window",
				Type:     "window",
				Client:   "worklog",
				Hostname: "localhost",
			},
		},
		Offsets: []time.Duration{
			0,
			time.Hour,
			23 * time.Hour,
		},
		Pattern: pattern{
			{
				Duration: 6 * time.Minute,
				AFK:      map[string]any{"afk": true, "locked": true},
				Window:   nil,
			},
			{
				Duration: 6 * time.Minute,
				AFK:      map[string]any{"afk": false, "locked": false},
				Window:   map[string]any{"app": "terminal", "title": "Terminal"},
			},
			{
				Duration: 0,
				AFK:      map[string]any{"afk": false, "locked": false},
				Window:   map[string]any{"app": "terminal", "title": "Terminal"},
			},
			{
				Duration: 6 * time.Minute,
				AFK:      map[string]any{"afk": false, "locked": false},
				Window:   map[string]any{"app": "editor", "title": "Text Editor"},
			},
			{
				Duration: 6 * time.Minute,
				AFK:      map[string]any{"afk": true, "locked": false},
				Window:   map[string]any{"app": "terminal", "title": "Terminal"},
			},
			{
				Duration: 6 * time.Minute,
				AFK:      map[string]any{"afk": true, "locked": true},
				Window:   nil,
			},
		},
	}
	b, err := json.MarshalIndent(template, "", "\t")
	if err != nil {
		log.Fatal(err)
	}

	for _, data := range cases {
		t, err := time.ParseInLocation(time.DateOnly, dataDate, mustLocationFor(data.tz))
		if err != nil {
			log.Fatal(err)
		}
		for _, query := range cases {
			for _, queryDate := range []time.Time{
				t.AddDate(0, 0, -4),
				t.AddDate(0, 0, 4),
			} {
				for _, raw := range []bool{false, true} {
					var name string
					if raw {
						name = fmt.Sprintf("generated_%s_%s_%s_raw.txt", data.tzName, query.tzName, queryDate.Format(time.DateOnly))
					} else {
						name = fmt.Sprintf("generated_%s_%s_%s.txt", data.tzName, query.tzName, queryDate.Format(time.DateOnly))
					}
					f, err := os.Create(name)
					if err != nil {
						log.Fatal(err)
					}
					defer f.Close()

					_, err = fmt.Fprintf(f, `gen_testdata -cen %s -tz %s -radius 7 -tmplt template.json data.json

dashboard_data -rules rules.toml -raw=%t -data data.json -tz %s %s
cmp stdout want.json

-- rules.toml --
[afk.afk]
src = """
{
	"bucket": bucket,
	"data":   data,
}
"""
[afk.locked]
src = """
{
	"bucket": bucket,
	"data":   {
		"activity": data.locked ? "locked" : "not-locked",
		"afk":      data.locked,
	},
}
"""
[window.window]
src = """
{
	"bucket": data.app == "" ? "" : bucket,
	"data":   data,
}
"""
[window.meeting]
src = """
{
	"bucket": data.app == "zoom.us" || data.app == "zoom" ? bucket : "",
	"data":   {
		"activity": "meeting",
		"afk":      false,
	},
}
"""
-- template.json --
%s
-- want.json --
`, dataDate, data.tz, raw, query.tz, queryDate.Format(time.DateOnly), b)
					if err != nil {
						log.Fatal(f)
					}
				}
			}
		}
	}
}

func mustLocationFor(tz string) *time.Location {
	var errs [2]error
	loc, err := time.LoadLocation(tz)
	if err == nil {
		return loc
	}
	errs[0] = err
	t, err := time.Parse("-07:00", tz)
	if err == nil {
		_, offset := t.Zone()
		return time.FixedZone(tz, offset)
	}
	errs[1] = err
	panic(errors.Join(errs[:]...))
}
