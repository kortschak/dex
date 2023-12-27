# `worklog`

`worklog` is a module that records screen activity, screen-saver lock state and AFK status. It takes messages from the `watcher` module and records them in an SQLite database and serves a small dashboard page that shows work activity.

Example configuration fragment (requires a kernel configuration fragment):
```
# Watcher module component: get active window and activity.
[module.watcher]
path = "watcher"
log_mode = "log"
log_level = "info"
log_add_source = false

[module.watcher.options]
polling = "1s"
rules.activity = """
{
	"uid":    {"module": "worklog"},
	"method": "record",
	"params": {
		"details": {
			"name":       locked ? "" : name,
			"class":      locked ? "" : class,
			"window":     locked ? "" : window,
			"last_input": last_input,
			"locked":     locked,
		},
		"period": period,
	},
}
"""

# Worklog module component: record details.
[module.worklog]
path = "worklog"
log_mode = "log"
log_level = "info"

[module.worklog.options]
database_dir = "worklog"

[module.worklog.options.rules.afk]
name = "afk-watcher"
type = "afkstatus"
src = """
data_src != {"module":"watcher"} ? {} :
{
	"afk_timeout":   duration("3m"),
	// On Mac use a higher value for the gap_tolerance
	// like 0.5s due to lax scheduling and poor performace
	// on that platform. See watcher/poll_darwin.go for
	// possible reasons.
	"gap_tolerance": duration("0.1s"), 
}.as(params, {
	"can_continue": last_event.end.is_zero() ?
	                	false
	                :
                    	(curr.locked && last.?locked.value()) ||
	                	(curr.time-last_event.end)-period < params.gap_tolerance,
	"is_afk":       curr.last_input.is_zero() ?
	                	true
	                :
	                	curr.locked || curr.time-curr.last_input > params.afk_timeout,
	"was_afk":      has(last_event.data.afk) ?
	                	last_event.data.afk
	                :
	                	true,
}).as(cond, cond.with({
	"continue": cond.can_continue &&
	            has(last.locked) && curr.locked == last.locked &&
	            cond.is_afk == cond.was_afk
})).as(cond, {
	"bucket":     bucket, // bucket or empty if no store is required.
	"start":      cond.continue ? last_event.start : last.time,
	"end":        curr.time,
	"data": {
		"afk":    cond.is_afk,
		"locked": curr.locked,
	},
	"continue":   cond.continue,
})
"""

[module.worklog.options.rules.window]
name = "window-watcher"
type = "currentwindow"
src = """
data_src != {"module":"watcher"} ? {} :
{
	"gap_tolerance": duration("0.1s"), // Use 0.5s on Mac.
}.as(params, {
	"can_continue": last_event.end.is_zero() ?
                    	false
                    :
                    	(curr.time-last_event.end)-period < params.gap_tolerance,
}).as(cond, cond.with({
	"continue": cond.can_continue &&
	            has(last.locked) && curr.locked == last.locked &&
	            curr.class == last.class &&
	            curr.window == last.window,
	"private":  curr.window.matches("(?i:private browsing)"), // Don't record private browsing.
})).as(cond, {
	"bucket":    cond.private || (has(last.locked) && curr.locked && last.locked) ? "" : bucket,
	"start":     cond.continue ? last_event.start : last.time,
	"end":       curr.time,
	"data": {
		"app":   curr.class,
		"title": curr.window,
	},
	"continue":  cond.continue,
})
"""
```

`worklog` provides a dashboard that allows simple visual presentation of active windows, AFK status, screen time and work flow. The dashboard is at the root of the configured web addr, for example http://localhost:6363/ below.

```
# Worklog module component: server.
[module.worklog.options.web]
addr = ":6363"

# Worklog module component: dashboard rules.
[module.worklog.options.web.rules.afk.afk]
src = """
{
	"bucket": bucket,
	"data":   data,
}
"""
[module.worklog.options.web.rules.afk.locked]
src = """
{
	"bucket": bucket,
	"data": {
		"activity": data.locked ? "locked" : "not-locked",
	},
}
"""
[module.worklog.options.web.rules.window.window]
src = """
{
	"bucket": data.app == "" ? "" : bucket,
	"data":   data,
}
"""
[module.worklog.options.web.rules.window.meeting]
src = """
{
	"bucket": data.app == "zoom.us" || data.app == "zoom" ? bucket : "",
	"data": {
		"activity": "meeting",
		"afk":      false,
	},
}
"""
```

In addition to the dashboard endpoint provided by the `worklog` server, there are end points for getting a day's details, dumping the data store in its entirety or between dates, and arbitrarily querying the database.

- `GET` `/data/`: accepts `date` and `tz` query parameters.
- `GET` `/dump/`: accepts `start` and `end` query parameters.
- `GET`/`POST` `/query`: takes a JSON-encoded (content-type:application/json) [query](https://pkg.go.dev/github.com/kortschak/dex/cmd/worklog/store#Query) or a CEL program (content-type:application/cel) that may use a built-in `query(<from table>, <JSON-encoded query without "from">)` function.

A potentially useful configuration for debugging rules is
```
[module.worklog.options.rules.raw]
name = "poller"
type = "raw-poll"
src = """
{
	"bucket":   bucket,
	"start":    curr.time,
	"end":      curr.time,
	"data":     curr.with({"period":period}),
	"continue": false,
}
"""
```
This will log all raw events sent from `watcher` so they can be correlated with digest rules and their resulting events. This will obviously store a lot of events in the database, and so is not recommended for long term operation.

## CEL optional types

The CEL environment enables the CEL [optional types library](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypes), [version 1](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypesVersion).

## CEL extensions

The CEL environment provides the [`Lib`](https://pkg.go.dev/github.com/kortschak/dex/internal/celext#Lib) and [`StateLib`](https://pkg.go.dev/github.com/kortschak/dex/internal/celext#StateLib) extensions from the celext package. `StateLib` is only available in `module.*.options.rules.*.src`.
