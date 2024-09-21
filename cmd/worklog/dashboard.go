// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"time"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
)

func (d *daemon) dashboardData(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		local, err := d.timezone.Load().Location()
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "polling current location", slog.Any("error", err))
		}
		date, err := dateQuery(req.URL, local)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		raw, err := rawQuery(req.URL)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rules := d.dashboardRules.Load()
		if rules == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no dashboard rules"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		events, err := d.eventData(ctx, db, rules, date, raw)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		b, err := json.Marshal(events)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, req, "events.json", time.Now(), bytes.NewReader(b))
	}
}

func dateQuery(u *url.URL, loc *time.Location) (time.Time, error) {
	d := u.Query().Get("date")
	if d == "" {
		return time.Now(), nil
	}
	if tz := u.Query().Get("tz"); tz != "" {
		var err error
		loc, err = locationFor(tz)
		if err != nil {
			return time.Time{}, err
		}
	}
	return time.ParseInLocation(time.DateOnly, d, loc)
}

func (d *daemon) eventData(ctx context.Context, db storage, rules map[string]map[string]ruleDetail, date time.Time, raw bool) (map[string]any, error) {
	if raw {
		return d.rawEventData(ctx, db, rules, date)
	}
	start, end := day(date)
	events := map[string]any{
		"date": zoneTranslatedTime(start, date.Location()),
	}
	atKeyboard, dayEvents, windowEvents, transitions, err := d.dayData(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	dayMinutes := make(map[string]float64)
	var dayTotalHours float64
	for _, a := range mergeIntervals(atKeyboard, 0) {
		dayTotalHours += a.End.Sub(a.Start).Hours()
		const binSize = time.Hour
		for h := tzRound(a.Start, time.Hour); h.Before(a.End); h = h.Add(binSize) {
			dayMinutes[h.Format(time.TimeOnly)] += part(h, h.Add(binSize), a.Start, a.End).Minutes()
		}
	}
	windowFlow := make(map[string]map[string]float64)
	for app, events := range windowEvents {
		appHours := make(map[string]float64)
		const binSize = time.Hour
		for _, a := range mergeIntervals(events, 0) {
			for h := tzRound(a.Start, time.Hour); h.Before(a.End); h = h.Add(binSize) {
				appHours[h.Format(time.DateTime)] += part(h, h.Add(binSize), a.Start, a.End).Hours()
			}
		}

		// Make sure there are zeros in sections with no use,
		// otherwise we'll see interpolation between the flanking
		// activity.
		var first, last time.Time
		for h := tzRound(start, time.Hour); h.Before(end); h = h.Add(binSize) {
			_, ok := appHours[h.Format(time.DateTime)]
			if ok {
				if first.IsZero() {
					first = h
				}
				last = h
			}
		}
		for h := first; h.Before(last); h = h.Add(binSize) {
			appHours[h.Format(time.DateTime)] += 0
		}

		windowFlow[app] = appHours
	}
	events["day"] = map[string]any{
		"events":      dayEvents,
		"flow":        windowFlow,
		"transitions": transitions,
		"minutes":     dayMinutes,
		"total_hours": dayTotalHours,
	}

	start, end = week(date)
	weekAtKeyboard, err := d.atKeyboard(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	weekHours := make(map[string]float64)
	var weekTotalHours float64
	for _, a := range mergeIntervals(weekAtKeyboard, 0) {
		weekTotalHours += a.End.Sub(a.Start).Hours()
		const binSize = time.Hour
		for h := tzRound(a.Start, time.Hour); h.Before(a.End); h = h.Add(binSize) {
			weekHours[h.Format(time.DateTime)] += part(h, h.Add(binSize), a.Start, a.End).Hours()
		}
	}
	events["week"] = map[string]any{
		"hours":       weekHours,
		"total_hours": weekTotalHours,
	}

	start, end = year(date)
	yearAtKeyboard, err := d.atKeyboard(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	yearHours := make(map[string]float64)
	var yearTotalHours float64
	for _, a := range mergeIntervals(yearAtKeyboard, 0) {
		yearTotalHours += a.End.Sub(a.Start).Hours()
		for h := tzRound(a.Start, time.Hour); h.Before(a.End); h = h.AddDate(0, 0, 1) {
			yearHours[h.Format(time.DateOnly)] += part(h, h.AddDate(0, 0, 1), a.Start, a.End).Hours()
		}
	}
	events["year"] = map[string]any{
		"hours":       yearHours,
		"total_hours": yearTotalHours,
	}

	return events, nil
}

func (d *daemon) rawEventData(ctx context.Context, db storage, rules map[string]map[string]ruleDetail, date time.Time) (map[string]any, error) {
	start, end := day(date)
	events := map[string]any{
		"date": zoneTranslatedTime(start, date.Location()),
	}
	_, dayEvents, windowEvents, transitions, err := d.dayData(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	for app, events := range windowEvents {
		windowEvents[app] = mergeIntervals(events, 0)
	}
	events["day"] = map[string]any{
		"events":      dayEvents,
		"flow":        windowEvents,
		"transitions": transitions,
	}

	weekStart, weekEnd := week(date)
	yearStart, yearEnd := year(date)
	start = minTime(weekStart, yearStart)
	end = maxTime(weekEnd, yearEnd)
	yearAtKeyboard, err := d.atKeyboard(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	events["at_keyboard"] = mergeIntervals(yearAtKeyboard, 0)

	return events, nil
}

func (d *daemon) dayData(ctx context.Context, db storage, rules map[string]map[string]ruleDetail, start, end time.Time) (atKeyboard []worklog.Event, dayEvents, windowEvents map[string][]worklog.Event, transitions graph, err error) {
	dayEvents = make(map[string][]worklog.Event)
	windowEvents = make(map[string][]worklog.Event)
	transitions = newGraph(rng{min: 5, max: 30}, rng{min: 1, max: 5})
	for srcBucket, ruleSet := range rules {
		for dstBucket, rule := range ruleSet {
			var nextApp worklog.Event // EventsRange is sorted descending.
			err := db.EventsRangeFunc(db.BucketID(srcBucket), start, end, -1, func(m worklog.Event) error {
				// canonicalise to the time zone that the event was
				// recorded in for the purposes of the dashboard.
				// See comment in atKeyboard.
				m.Start = maxTime(m.Start, zoneTranslatedTime(start, m.Start.Location()))
				m.End = minTime(m.End, zoneTranslatedTime(end, m.End.Location()))
				if !m.End.After(m.Start) {
					// This also excludes events that have zero
					// length. These are most likely uninteresting
					// artifacts of the watcher.
					return nil
				}

				for _, m := range d.applyAmendments(ctx, m) {
					act := map[string]any{
						"bucket": dstBucket,
						"data":   m.Data,
					}
					note, err := eval[worklog.Activity](rule.prg, act)
					if err != nil {
						d.log.LogAttrs(ctx, slog.LevelError, "activity evaluation", slog.Any("bucket", srcBucket), slog.Any("error", err), slog.Any("act", act))
						return nil
					}
					if note.Bucket == "" || note.Data == nil {
						return nil
					}
					m.Bucket = srcBucket // Label the source of the activity event.
					m.Data = note.Data

					wasExtended := false
					if bucketEvents := dayEvents[note.Bucket]; len(bucketEvents) != 0 {
						next := bucketEvents[len(bucketEvents)-1]
						// Compare start with end of previous events. Note that
						// db.EventsRangeFunc returns events in descending time
						// order.
						if next.Start.Equal(m.End) && reflect.DeepEqual(next.Data, m.Data) {
							bucketEvents[len(bucketEvents)-1].Start = m.Start
							wasExtended = true
						}
					}
					if !wasExtended {
						dayEvents[note.Bucket] = append(dayEvents[note.Bucket], m)
					}

					if afk, ok := m.Data["afk"]; ok && afk == false {
						atKeyboard = append(atKeyboard, m)
					}
					if app, ok := m.Data["app"].(string); ok {
						if !nextApp.Start.IsZero() {
							transitions.connect(m, nextApp)
						}
						nextApp = m
						windowEvents[app] = append(windowEvents[app], m)
					}
				}
				return nil
			})
			if err != nil {
				return nil, nil, nil, graph{}, err
			}
		}
	}
	return atKeyboard, dayEvents, windowEvents, transitions, nil
}

func (d *daemon) summaryData(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		local, err := d.timezone.Load().Location()
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "polling current location", slog.Any("error", err))
		}
		start, end, err := dateRangeQuery(req.RequestURI, local)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, err)
			return
		}
		raw, err := rawQuery(req.URL)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		db := d.db.Load()
		if db == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rules := d.dashboardRules.Load()
		if rules == nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no dashboard rules"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		events, err := d.rangeSummary(ctx, db, rules, start, end, raw, req.URL)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			if len(events.Warnings) == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		b, err := json.Marshal(events)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, req, "events.json", time.Now(), bytes.NewReader(b))
	}
}

func dateRangeQuery(uri string, loc *time.Location) (start, end time.Time, err error) {
	u, err := url.Parse(uri)
	if err != nil {
		return start, end, err
	}
	s := u.Query().Get("start")
	if s == "" {
		return start, end, errors.New("missing start parameter")
	}
	e := u.Query().Get("end")
	if tz := u.Query().Get("tz"); tz != "" {
		loc, err = locationFor(tz)
		if err != nil {
			return start, end, err
		}
	}
	start, err = time.ParseInLocation(time.DateOnly, s, loc)
	if err != nil {
		return start, end, err
	}
	if e == "" {
		// If no end specified, make range from start to now.
		return start, time.Now().In(loc), nil
	}
	end, err = time.ParseInLocation(time.DateOnly, e, loc)
	end = end.AddDate(0, 0, 1).Add(-time.Nanosecond)
	return start, end, err
}

func locationFor(tz string) (*time.Location, error) {
	var errs [2]error
	loc, err := time.LoadLocation(tz)
	if err == nil {
		return loc, nil
	}
	errs[0] = err
	t, err := time.Parse("-07:00", tz)
	if err == nil {
		_, offset := t.Zone()
		return time.FixedZone(tz, offset), nil
	}
	errs[1] = err
	return nil, errors.Join(errs[:]...)
}

func rawQuery(u *url.URL) (bool, error) {
	r := u.Query().Get("raw")
	if r == "" {
		return false, nil
	}
	return strconv.ParseBool(r)
}

type summary struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Period struct {
		AtKeyboard []worklog.Event    `json:"at_keyboard,omitempty"`
		Hours      map[string]float64 `json:"hours,omitempty"`
		TotalHours float64            `json:"total_hours,omitempty"`
	} `json:"period"`
	Cooldown string   `json:"cooldown,omitempty"`
	Warnings []string `json:"warn,omitempty"`
}

func (d *daemon) rangeSummary(ctx context.Context, db storage, rules map[string]map[string]ruleDetail, start, end time.Time, raw bool, req *url.URL) (summary, error) {
	events := summary{
		Start: start,
		End:   end,
	}
	atKeyboard, err := d.atKeyboard(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	if raw {
		events.Period.AtKeyboard = mergeIntervals(atKeyboard, 0)
		return events, nil
	}
	var cooldown time.Duration
	if req != nil && req.Query().Has("cooldown") {
		cooldown, err = time.ParseDuration(req.Query().Get("cooldown"))
		if err != nil {
			events.Warnings = append(events.Warnings, fmt.Sprintf("%s: %v", req, err))
			return events, fmt.Errorf("invalid cooldown: %w", err)
		}
		if cooldown != 0 {
			events.Cooldown = fmt.Sprint(cooldown)
		}
	}
	if req != nil && req.Query().Has("other") {
		otherAtKeyboard, err := d.remoteRanges(ctx, req, start, end)
		if err != nil {
			events.Warnings = append(events.Warnings, fmt.Sprintf("%s: %v", req, err))
			return events, err
		}
		events.Period.AtKeyboard = atKeyboard // Let the full merge handle this.
		return mergeSummaries(append(otherAtKeyboard, events), cooldown)
	}
	periodHours := make(map[string]float64)
	var periodTotalHours float64
	for _, a := range mergeIntervals(atKeyboard, cooldown) {
		periodTotalHours += a.End.Sub(a.Start).Hours()
		for h := tzRound(a.Start, time.Hour); h.Before(a.End); h = h.AddDate(0, 0, 1) {
			periodHours[h.Format(time.DateOnly)] += part(h, h.AddDate(0, 0, 1), a.Start, a.End).Hours()
		}
	}
	events.Period.Hours = periodHours
	events.Period.TotalHours = periodTotalHours
	return events, nil
}

func (d *daemon) remoteRanges(ctx context.Context, req *url.URL, start, end time.Time) ([]summary, error) {
	query := url.Values{
		"start": []string{start.Format(time.DateOnly)},
		"end":   []string{end.Format(time.DateOnly)},
		"raw":   []string{"true"},
	}
	reqQuery := req.Query()
	if reqQuery.Has("tz") {
		query.Set("tz", reqQuery.Get("tz"))
	}
	var strict bool
	if reqQuery.Has("strict") {
		var err error
		strict, err = strconv.ParseBool(reqQuery.Get("strict"))
		if err != nil {
			err = fmt.Errorf("parse strict: %w", err)
			return []summary{{Warnings: []string{err.Error()}}}, err
		}
	}
	rawQuery := query.Encode()
	other := reqQuery["other"]
	summaries := make([]summary, 0, len(other))
	var buf bytes.Buffer
	seen := make(map[string]bool)
	for _, h := range other {
		u, err := url.Parse(h)
		if err != nil {
			err = fmt.Errorf("parse host: %s %w", h, err)
			if strict {
				return nil, err
			}
			summaries = append(summaries, summary{Warnings: []string{fmt.Sprintf("%s: %v", u, err)}})
			continue
		}
		if u.Host == req.Host {
			d.log.LogAttrs(ctx, slog.LevelInfo, "skipping self remote ranges", slog.String("host", req.Host))
			continue
		}
		if seen[u.Host] {
			d.log.LogAttrs(ctx, slog.LevelInfo, "skipping repeated remote ranges host", slog.String("host", u.Host))
			continue
		}
		seen[u.Host] = true
		*u = url.URL{
			Scheme:   u.Scheme,
			Host:     u.Host,
			Path:     req.Path,
			RawQuery: rawQuery,
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "get remote summary", slog.Any("url", u))
		resp, err := http.Get(u.String())
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "remote summary failure", slog.Any("error", err))
			if strict {
				return nil, err
			}
			summaries = append(summaries, summary{Warnings: []string{fmt.Sprintf("%s: %v", u, err)}})
			continue
		}
		buf.Reset()
		d.log.LogAttrs(ctx, slog.LevelDebug, "get remote summary response", slog.Any("status_code", resp.StatusCode), slog.Any("status", resp.Status))
		_, err = io.Copy(&buf, resp.Body)
		resp.Body.Close()
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "remote summary failure", slog.Any("error", err))
			if strict {
				return nil, err
			}
			summaries = append(summaries, summary{Warnings: []string{fmt.Sprintf("%s: %v", u, err)}})
			continue
		}
		var s summary
		err = json.Unmarshal(buf.Bytes(), &s)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelError, "remote summary failure", slog.Any("error", err))
			if strict {
				return nil, err
			}
			summaries = append(summaries, summary{Warnings: []string{fmt.Sprintf("%s: %v", u, err)}})
			continue
		}
		d.log.LogAttrs(ctx, slog.LevelDebug, "add remote summary", slog.Any("url", u))
		summaries = append(summaries, s)
	}
	return summaries, nil
}

func mergeSummaries(summaries []summary, cooldown time.Duration) (summary, error) {
	// TODO: Use a merge strategy that doesn't rely on
	// copying and sorting the entire set of summaries
	// before merging the intervals.

	if len(summaries) == 0 {
		return summary{}, nil
	}
	warnings := summaries[0].Warnings
	sum := summary{
		Start:    summaries[0].Start,
		End:      summaries[0].End,
		Warnings: warnings[:len(warnings):len(warnings)],
	}
	if cooldown != 0 {
		sum.Cooldown = fmt.Sprint(cooldown)
	}
	n := len(summaries[0].Period.AtKeyboard)
	for _, s := range summaries[1:] {
		n += len(s.Period.AtKeyboard)
		if s.Start.Before(sum.Start) {
			sum.Start = s.Start
		}
		if s.End.After(sum.End) {
			sum.End = s.End
		}
		sum.Warnings = append(sum.Warnings, s.Warnings...)
	}
	sum.Period.AtKeyboard = make([]worklog.Event, 0, n)
	for _, s := range summaries {
		sum.Period.AtKeyboard = append(sum.Period.AtKeyboard, s.Period.AtKeyboard...)
	}
	periodHours := make(map[string]float64)
	var periodTotalHours float64
	for _, a := range mergeIntervals(sum.Period.AtKeyboard, cooldown) {
		periodTotalHours += a.End.Sub(a.Start).Hours()
		for h := tzRound(a.Start, time.Hour); h.Before(a.End); h = h.AddDate(0, 0, 1) {
			periodHours[h.Format(time.DateOnly)] += part(h, h.AddDate(0, 0, 1), a.Start, a.End).Hours()
		}
	}
	sum.Period.AtKeyboard = nil
	sum.Period.Hours = periodHours
	sum.Period.TotalHours = periodTotalHours
	return sum, nil
}

func (d *daemon) atKeyboard(ctx context.Context, db storage, rules map[string]map[string]ruleDetail, start, end time.Time) ([]worklog.Event, error) {
	var atKeyboard []worklog.Event
	for srcBucket, ruleSet := range rules {
		for dstBucket, rule := range ruleSet {
			err := db.EventsRangeFunc(db.BucketID(srcBucket), start, end, -1, func(m worklog.Event) error {
				// atKeyboard is used for week and year intervals which
				// may involve work spanning multiple time zones. We
				// canonicalise to the time zone that the event was
				// recorded in for the purposes of the dashboard. This
				// is not the correct solution — there is no correct
				// solution — but it provides a reasonable representation
				// of the data with the low risk of having hours or days
				// showing more than their interval's worth of time worked
				// if a set work intervals span multiple time zones.
				m.Start = maxTime(m.Start, zoneTranslatedTime(start, m.Start.Location()))
				m.End = minTime(m.End, zoneTranslatedTime(end, m.End.Location()))
				if !m.End.After(m.Start) {
					// This also excludes events that have zero
					// length. These are most likely uninteresting
					// artifacts of the watcher.
					return nil
				}

				for _, m := range d.applyAmendments(ctx, m) {
					act := map[string]any{
						"bucket": dstBucket,
						"data":   m.Data,
					}
					note, err := eval[worklog.Activity](rule.prg, act)
					if err != nil {
						d.log.LogAttrs(ctx, slog.LevelError, "activity evaluation", slog.Any("src_bucket", srcBucket), slog.Any("dst_bucket", dstBucket), slog.Any("error", err), slog.Any("act", act))
						return nil
					}
					if note.Bucket == "" || note.Data == nil {
						return nil
					}
					m.Data = note.Data

					if afk, ok := m.Data["afk"]; ok && afk == false {
						atKeyboard = append(atKeyboard, m)
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
		}
	}
	return atKeyboard, nil
}

// zoneTranslatedTime returns the civil time of t in loc. This is not the
// same as t.In(loc) which refers to the same instance in a different
// location.
func zoneTranslatedTime(t time.Time, loc *time.Location) time.Time {
	year, month, day := t.Date()
	return time.Date(year, month, day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// applyAmendments applies any amend replacements in e.Data to e, expanding
// the set of events as necessary. The returned events are sorted in descending
// order by end to match the behaviour of db.EventsRangeFunc.
func (d *daemon) applyAmendments(ctx context.Context, e worklog.Event) []worklog.Event {
	amend := d.getAmendments(ctx, e)
	if amend == nil {
		return []worklog.Event{e}
	}
	orig := e
	delete(e.Data, "amend")
	d.log.LogAttrs(ctx, slog.LevelDebug, "apply amendments", slog.Any("event", e), slog.Any("amend", amend))
	replace := d.mergeAmendments(ctx, amend)
	d.log.LogAttrs(ctx, slog.LevelDebug, "apply replacements", slog.Any("event", e), slog.Any("replace", replace))
	var events []worklog.Event
	for _, r := range replace {
		if !e.End.After(e.Start) {
			break
		}
		if !r.End.After(r.Start) {
			continue
		}
		r := replacement(r)
		w := worklogEvent(e)
		switch {
		case r.before(w):
			// Look in next replacement.
		case r.after(w):
			// Done; no more replacements will match.
		case r.overlapAll(w):
			// Complete overlap; replace data and done.
			// r:   ~~~~~~~~~~~~~~~
			// w:     ~~~~~~~~~~~
			//
			// a:     ~~~~~~~~~~~

			e.Data = mergeData(e.Data, r.Data)
			return append(events, e)
		case r.overlapLeftOf(w):
			// Replace left data and truncate rightward.
			// r:   ~~~~~~~~~~~~~~~
			// w:           ~~~~~~~~~~~~~~~
			//
			// a:           ~~~~~~~
			// e:                  ~~~~~~~~

			// I don't think this can be hit, but I haven't proven it.
			// So leaving in.

			left := e
			left.End = r.Start
			left.Data = mergeData(e.Data, r.Data)
			events = append(events, left)
			e.Start = r.End
		case r.overlapRightOf(w):
			// Replace right data and truncate leftward.
			// r:           ~~~~~~~~~~~~~~~
			// w:   ~~~~~~~~~~~~~~~
			//
			// e:   ~~~~~~~~
			// a:           ~~~~~~~

			// I don't think this can be hit, but I haven't proven it.
			// So leaving in.

			right := e
			right.Start = r.End
			right.Data = mergeData(e.Data, r.Data)
			e.End = r.Start
			if e.End.Before(e.Start) {
				events = append(events, e)
			}
			events = append(events, right)
			return events
		case r.overlapIn(w):
			// Overlap in the centre, complete overlap already covered;
			// replace central data and truncate left and right.
			// r:     ~~~~~~~~~~~
			// w:   ~~~~~~~~~~~~~~~
			//
			// l:   ~~
			// m:     ~~~~~~~~~~~
			// e:                ~~

			left := e
			left.End = r.Start
			if left.End.After(left.Start) {
				events = append(events, left)
			}

			middle := e
			middle.Start = r.Start
			middle.End = r.End
			middle.Data = mergeData(e.Data, r.Data)
			events = append(events, middle)

			e.Start = r.End
		default:
			d.log.LogAttrs(ctx, slog.LevelError, "apply amendments", slog.Any("event", e), slog.Any("replace", r))
			if d.log.Enabled(ctx, slog.LevelDebug-1) {
				// Only panic when running at DEBUG-1 or lower.
				panic("unreachable")
			}
			return []worklog.Event{orig}
		}
	}
	if e.End.After(e.Start) {
		events = append(events, e)
	}
	sort.Slice(events, func(i int, j int) bool {
		return events[i].End.After(events[j].End)
	})
	return events
}

// mergeData adds elements of src into a clone of dst recursively, removing any
// elements that are nil in src and replacing elements in dst with elements in
// src if they have the same key.
func mergeData(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any)
	} else {
		dst = maps.Clone(dst)
	}
	for k, v := range src {
		switch {
		case v == nil:
			delete(dst, k)
		case is[map[string]any](dst[k]) && is[map[string]any](v):
			dst[k] = mergeData(dst[k].(map[string]any), v.(map[string]any))
		default:
			dst[k] = v
		}
	}
	return dst
}

type worklogEvent worklog.Event

func (e worklogEvent) start() time.Time { return e.Start }
func (e worklogEvent) end() time.Time   { return e.End }

// getAmendments returns the amendments in e.Data.
func (d *daemon) getAmendments(ctx context.Context, e worklog.Event) []worklog.Amendment {
	a, ok := e.Data["amend"]
	if !ok {
		return nil
	}
	b, err := json.Marshal(a)
	if err != nil {
		d.log.LogAttrs(ctx, slog.LevelWarn, "get amendments", slog.Any("error", err))
		return nil
	}
	var amendments []worklog.Amendment
	err = json.Unmarshal(b, &amendments)
	if err != nil {
		d.log.LogAttrs(ctx, slog.LevelWarn, "get amendments", slog.Any("error", err))
		return nil
	}
	return amendments
}

// mergeAmendmets merges all replacements in a into a set of linear replacements
// with later amendments' replacements taking priority.
func (d *daemon) mergeAmendments(ctx context.Context, a []worklog.Amendment) []worklog.Replacement {
	var m []worklog.Replacement
	for i := len(a) - 1; i >= 0; i-- {
		replacements := a[i].Replace
		sort.Slice(replacements, func(i int, j int) bool {
			return replacements[i].Start.Before(replacements[j].Start)
		})
		var ok bool
		for _, r := range replacements {
			m, ok = d.mergeReplacements(ctx, m, r)
			if !ok {
				return nil
			}
		}
	}
	return m
}

// mergeReplacements merges r into merged, extending merged if necessary.
// If an internal inconsistency is found, ok is returned false, unless the
// daemon's log level is lower than DEBUG in which case it will panic.
func (d *daemon) mergeReplacements(ctx context.Context, merged []worklog.Replacement, r worklog.Replacement) (_ []worklog.Replacement, ok bool) {
	if len(merged) == 0 {
		return []worklog.Replacement{r}, true
	}
	w := replacement(r)
	for i := 0; i < len(merged); i++ {
		if !w.End.After(w.Start) {
			return merged, true
		}
		e := replacement(merged[i])
		switch {
		case w.before(e):
			// This depends on the invariant that w is has no overlap
			// with the previous element of merged. Either i is 0 or
			// we have already trimmed w.
			return slices.Insert(merged, i, worklog.Replacement(w)), true
		case w.after(e):
			if i == len(merged)-1 {
				return append(merged, worklog.Replacement(w)), true
			}
			i++
			left := worklog.Replacement(w)
			if left.End.After(merged[i].Start) {
				left.End = merged[i].Start
				// Jump past the overlap. This may make the duration of w
				// negative; it is caught next iteration.
				w.Start = merged[i].End
			}
			merged = slices.Insert(merged, i, left)
		case w.overlapIn(e):
			// w is completely masked by existing element in merged.
			return merged, true
		case w.overlapLeftOf(e):
			// I don't think this can be hit, but I haven't proven it.
			// So leaving in.

			left := worklog.Replacement(w)
			left.End = e.Start
			// Jump past the overlap. This may make the duration of w
			// negative; it is caught next iteration.
			w.Start = e.End
			merged = slices.Insert(merged, i, left)
			i++
		case w.overlapRightOf(e):
			// Trim and re-check against current e.
			w.Start = e.End
			i--
		default:
			d.log.LogAttrs(ctx, slog.LevelError, "merge replacements", slog.Any("next", w), slog.Any("current", e), slog.Any("merged", merged))
			if d.log.Enabled(ctx, slog.LevelDebug-1) {
				// Only panic when running at DEBUG-1 or lower.
				panic("unreachable")
			}
			return nil, false
		}
	}
	return merged, true
}

type replacement worklog.Replacement

type interval interface {
	start() time.Time
	end() time.Time
}

func (r replacement) start() time.Time { return r.Start }
func (r replacement) end() time.Time   { return r.End }

func (r replacement) before(o interval) bool {
	return r.Start.Before(o.start()) && !r.End.After(o.start())
}
func (r replacement) after(o interval) bool {
	return !r.Start.Before(o.end()) && r.End.After(o.end())
}
func (r replacement) overlapIn(o interval) bool {
	return !r.Start.Before(o.start()) && !r.End.After(o.end())
}
func (r replacement) overlapLeftOf(o interval) bool {
	return r.Start.Before(o.start()) && r.End.After(o.start())
}
func (r replacement) overlapRightOf(o interval) bool {
	return r.Start.Before(o.end()) && r.End.After(o.end())
}
func (r replacement) overlapAll(o interval) bool {
	return !r.Start.After(o.start()) && !r.End.Before(o.end())
}

// mergeIntervals returns the intervals in events sorted and merged so that
// no pair of events overlaps within the cool-down duration.
func mergeIntervals(events []worklog.Event, cooldown time.Duration) []worklog.Event {
	if len(events) == 0 {
		return nil
	}
	sort.Slice(events, func(i, j int) bool {
		switch {
		case events[i].Start.Before(events[j].Start):
			return true
		case events[i].Start.After(events[j].Start):
			return false
		default:
			return events[i].End.Before(events[j].End)
		}
	})
	var (
		merged []worklog.Event
		last   worklog.Event
	)
	for i, a := range events {
		switch {
		case i == 0:
			last = worklog.Event{
				Start: a.Start,
				End:   a.End,
			}
		case a.Start.After(last.End.Add(cooldown)):
			merged = append(merged, last)
			last = worklog.Event{
				Start: a.Start,
				End:   a.End,
			}
		case a.End.After(last.End):
			last.End = a.End
		}
	}
	return append(merged, last)
}

// tzRound rounds t to the provided resolution and offsets the time's
// timezone so that timezone-independent date and time values can be used.
func tzRound(t time.Time, res time.Duration) time.Time {
	_, off := t.Zone()
	t = t.Round(res)
	if off < 0 {
		return t.Add(-(time.Duration(off)*time.Second)%res - res)
	}
	return t.Add(-(time.Duration(off) * time.Second) % res)
}

func part(binstart, binend, start, end time.Time) time.Duration {
	d := minTime(end, binend).Sub(maxTime(start, binstart))
	if d < 0 {
		return 0
	}
	return d
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// day provides the start and end time of the provided date in locations
// spanning all time zones. The returned start is at the east dateline and
// the end is at the west dateline.
func day(date time.Time) (start, end time.Time) {
	year, month, day := date.Date()
	const dateLineOffset = int(12 * time.Hour / time.Second)
	start = time.Date(year, month, day, 0, 0, 0, 0, time.FixedZone("east", dateLineOffset))
	end = time.Date(year, month, day+1, 0, 0, 0, 0, time.FixedZone("west", -dateLineOffset)).Add(-time.Nanosecond)
	return start, end
}

// week provides the start and end time of the week of the provided date in
// locations spanning all time zones. The returned start is at the east dateline
// and the end is at the west dateline.
func week(date time.Time) (start, end time.Time) {
	year, month, day := date.Date()
	day -= int(date.Weekday())
	const dateLineOffset = int(12 * time.Hour / time.Second)
	start = time.Date(year, month, day, 0, 0, 0, 0, time.FixedZone("east", dateLineOffset))
	end = time.Date(year, month, day+7, 0, 0, 0, 0, time.FixedZone("west", -dateLineOffset)).Add(-time.Nanosecond)
	return start, end
}

// year provides the start and end time of the year of the provided date in
// locations spanning all time zones. The returned start is at the east dateline
// and the end is at the west dateline.
func year(date time.Time) (start, end time.Time) {
	year := date.Year()
	const dateLineOffset = int(12 * time.Hour / time.Second)
	start = time.Date(year, time.January, 0, 0, 0, 0, 0, time.FixedZone("east", dateLineOffset))
	end = time.Date(year+1, time.January, 0, 0, 0, 0, 0, time.FixedZone("west", -dateLineOffset)).Add(-time.Nanosecond)
	return start, end
}

// graph is an application transition graph.
type graph struct {
	nodes map[string]*node
	edges map[edge]float64
	times map[appPeriod]struct{}

	size  rng
	width rng
}

type rng struct {
	min, max float64
}

type appPeriod struct {
	app        string
	start, end time.Time
}

// newGraph returns a new graph with the provided maximum node size and edge
// width.
func newGraph(size, width rng) graph {
	return graph{
		nodes: make(map[string]*node),
		edges: make(map[edge]float64),
		times: make(map[appPeriod]struct{}),
		size:  size,
		width: width,
	}
}

// node is a node in the transition graph.
type node struct {
	id       int
	duration time.Duration
}

// edge is an edge in the transition graph.
type edge struct {
	from int
	to   int
}

// connect makes an edge between the from and to worklog events.
func (g graph) connect(from, to worklog.Event) {
	f := g.node(from)
	t := g.node(to)
	if f == nil || t == nil || f.id == t.id {
		return
	}
	g.edges[edge{from: f.id, to: t.id}]++
}

// node returns a node corresponding to a worklog event, creating it if
// necessary.
func (g graph) node(e worklog.Event) *node {
	app := e.Data["app"].(string)
	if app == "" {
		return nil
	}
	u, ok := g.nodes[app]
	if !ok {
		u = &node{id: len(g.nodes)}
		g.nodes[app] = u
	}
	g.times[appPeriod{app: app, start: e.Start, end: e.End}] = struct{}{}
	return u
}

// MarshalJSON formats the graph for the echart graph type.
func (g graph) MarshalJSON() ([]byte, error) {
	type jsonNode struct {
		ID       int     `json:"id"`
		Name     string  `json:"name"`
		Value    float64 `json:"value"`
		Size     float64 `json:"symbolSize"`
		Category int     `json:"category"`
	}
	nodes := make([]jsonNode, 0, len(g.nodes))
	type jsonCategory struct {
		Name string `json:"name"`
	}
	categories := make([]jsonCategory, len(g.nodes))
	for e := range g.times {
		g.nodes[e.app].duration += e.end.Sub(e.start)
	}
	var maxDuration time.Duration
	for name, n := range g.nodes {
		if n.duration > maxDuration {
			maxDuration = n.duration
		}
		nodes = append(nodes, jsonNode{
			ID:       n.id,
			Name:     name,
			Value:    n.duration.Hours(),
			Category: n.id,
		})
		categories[n.id] = jsonCategory{Name: name}
	}
	s := (g.size.max - g.size.min) / maxDuration.Hours()
	for i, n := range nodes {
		nodes[i].Size = n.Value*s + g.size.min
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].ID < nodes[j].ID
	})

	type jsonEdge struct {
		From      int     `json:"source"`
		To        int     `json:"target"`
		Value     float64 `json:"value"`
		LineStyle struct {
			Width float64 `json:"width"`
		} `json:"lineStyle"`
	}
	edges := make([]jsonEdge, 0, len(g.edges))
	var max float64
	for e, weight := range g.edges {
		if weight > max {
			max = weight
		}
		edges = append(edges, jsonEdge{
			From:  e.from,
			To:    e.to,
			Value: weight,
		})
	}
	s = (g.width.max - g.width.min) / max
	for i, e := range edges {
		edges[i].LineStyle.Width = e.Value*s + g.width.min
	}
	sort.Slice(edges, func(i, j int) bool {
		switch {
		case edges[i].From < edges[j].From:
			return true
		case edges[i].From > edges[j].From:
			return false
		default:
			return edges[i].To < edges[j].To
		}
	})
	type jsonGraph struct {
		Nodes      []jsonNode     `json:"nodes"`
		Edges      []jsonEdge     `json:"links"`
		Categories []jsonCategory `json:"categories"`
	}
	return json.Marshal(jsonGraph{
		Nodes:      nodes,
		Edges:      edges,
		Categories: categories,
	})
}
