// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"encoding/json"
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

		date, err := dateQuery(req.RequestURI)
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

func dateQuery(uri string) (time.Time, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return time.Time{}, err
	}
	d := u.Query().Get("date")
	if d == "" {
		return time.Now(), nil
	}
	loc := time.Local // TODO: Resolve how we store time. Probably UTC.
	if tz := u.Query().Get("tz"); tz != "" {
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
		"date": start,
	}
	dayEvents := make(map[string][]worklog.Event)
	windowEvents := make(map[string][]worklog.Event)
	var atKeyboard []worklog.Event
	transitions := newGraph(rng{min: 5, max: 30}, rng{min: 1, max: 5})
	for srcBucket, ruleSet := range rules {
		for dstBucket, rule := range ruleSet {
			var nextApp worklog.Event // EventsRange is sorted descending.
			err := db.EventsRangeFunc(db.BucketID(srcBucket), start, end, -1, func(m worklog.Event) error {
				m.Continue = nil
				if m.Start.Before(start) {
					m.Start = start
				}
				if m.End.After(end) {
					m.End = end
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
				return events, err
			}
		}
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

func (d *daemon) atKeyboard(ctx context.Context, db *store.DB, rules map[string]map[string]ruleDetail, start, end time.Time) ([]worklog.Event, error) {
	var atKeyboard []worklog.Event
	for srcBucket, ruleSet := range rules {
		for dstBucket, rule := range ruleSet {
			err := db.EventsRangeFunc(db.BucketID(srcBucket), start, end, -1, func(m worklog.Event) error {
				if m.Start.Before(start) {
					m.Start = start
				}
				if m.End.After(end) {
					m.End = end
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
	return t.Round(res).Add(-(time.Duration(off) * time.Second) % res)
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

func day(date time.Time) (start, end time.Time) {
	loc := date.Location()
	year, month, day := date.Date()
	start = time.Date(year, month, day, 0, 0, 0, 0, loc)
	end = time.Date(year, month, day+1, 0, 0, 0, 0, loc).Add(-time.Nanosecond)
	return start, end
}

func week(date time.Time) (start, end time.Time) {
	loc := date.Location()
	year, month, day := date.Date()
	day -= int(date.Weekday())
	start = time.Date(year, month, day, 0, 0, 0, 0, loc)
	end = time.Date(year, month, day+7, 0, 0, 0, 0, loc).Add(-time.Nanosecond)
	return start, end
}

func year(date time.Time) (start, end time.Time) {
	loc := date.Location()
	year := date.Year()
	start = time.Date(year, time.January, 0, 0, 0, 0, 0, loc)
	end = time.Date(year+1, time.January, 0, 0, 0, 0, 0, loc).Add(-time.Nanosecond)
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
