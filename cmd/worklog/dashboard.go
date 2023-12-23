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
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"time"

	worklog "github.com/kortschak/dex/cmd/worklog/api"
	"github.com/kortschak/dex/cmd/worklog/store"
)

func (d *daemon) dashboardData(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		date, err := dateQuery(req.URL)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		db, ok := d.db.Load().(*store.DB)
		if !ok {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rules, ok := d.dashboardRules.Load().(map[string]map[string]ruleDetail)
		if !ok {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no dashboard rules"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		events, err := d.eventData(ctx, db, rules, date)
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

func dateQuery(u *url.URL) (time.Time, error) {
	d := u.Query().Get("date")
	if d == "" {
		return time.Now(), nil
	}
	loc := time.Local // TODO: Resolve how we store time. Probably UTC.
	if tz := u.Query().Get("tz"); tz != "" {
		var err error
		loc, err = time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, err
		}
	}
	return time.ParseInLocation(time.DateOnly, d, loc)
}

func (d *daemon) eventData(ctx context.Context, db *store.DB, rules map[string]map[string]ruleDetail, date time.Time) (map[string]any, error) {
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
	for _, a := range mergeIntervals(atKeyboard) {
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
		for _, a := range mergeIntervals(events) {
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
	for _, a := range mergeIntervals(weekAtKeyboard) {
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
	for _, a := range mergeIntervals(yearAtKeyboard) {
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

func (d *daemon) dayData(ctx context.Context, db *store.DB, rules map[string]map[string]ruleDetail, start, end time.Time) (atKeyboard []worklog.Event, dayEvents, windowEvents map[string][]worklog.Event, transitions graph, err error) {
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

		start, end, err := dateRangeQuery(req.RequestURI)
		if err != nil {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.Any("error", err), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, `{"error":%q}`, err)
			return
		}
		db, ok := d.db.Load().(*store.DB)
		if !ok {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no database"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rules, ok := d.dashboardRules.Load().(map[string]map[string]ruleDetail)
		if !ok {
			d.log.LogAttrs(ctx, slog.LevelWarn, "web server", slog.String("error", "no dashboard rules"), slog.String("url", req.RequestURI))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		events, err := d.rangeSummary(ctx, db, rules, start, end)
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

func dateRangeQuery(uri string) (start, end time.Time, err error) {
	u, err := url.Parse(uri)
	if err != nil {
		return start, end, err
	}
	s := u.Query().Get("start")
	if s == "" {
		return start, end, errors.New("missing start parameter")
	}
	e := u.Query().Get("end")
	loc := time.Local // TODO: Resolve how we store time. Probably UTC.
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

func (d *daemon) rangeSummary(ctx context.Context, db *store.DB, rules map[string]map[string]ruleDetail, start, end time.Time) (map[string]any, error) {
	events := map[string]any{
		"start": start,
		"end":   end,
	}
	yearAtKeyboard, err := d.atKeyboard(ctx, db, rules, start, end)
	if err != nil {
		return events, err
	}
	yearHours := make(map[string]float64)
	var yearTotalHours float64
	for _, a := range mergeIntervals(yearAtKeyboard) {
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

func (d *daemon) atKeyboard(ctx context.Context, db *store.DB, rules map[string]map[string]ruleDetail, start, end time.Time) ([]worklog.Event, error) {
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

// mergeIntervals returns the intervals in events sorted and merged so that
// no pair of events overlap.
func mergeIntervals(events []worklog.Event) []worklog.Event {
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
		case a.Start.After(last.End):
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
