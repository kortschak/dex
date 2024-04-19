# `watcher`

`watcher` is a module that observes window, keyboard and mouse activity. Canonically it is intended for use in contextually switching device page to match the current application, but can also be used to log activity using the [`worklog`](../worklog) module.

`watcher` periodically polls the OS for active window and last activity time. The results of this are passed to a CEL evaluation as the following object:
```
{
	"time":   <timestamp>,
	"period": <duration>,

	"window_id":  <int>,
	"name":       <string>,
	"class":      <string>,
	"window":     <string>,
	"last_input": <timestamp>,
	"locked":     <bool>,
	"last": {
		"window_id":  <int>,
		"name":       <string>,
		"class":      <string>,
		"window":     <string>,
		"last_input": <timestamp>,
		"locked":     <bool>,
	},
}
```
The resulting evaluation is then send as an RPC message if it has a string field "method". If the evaluation has a field "uid" corresponding to an `rpc.UID` value the message is forwarded as a "method" notification to the daemon identified by that UID. Otherwise it is passed to the kernel to handle.

Example configuration fragment that pages to the "dev" page when "sublime_text" or "sublime_merge" is the active window (requires a kernel configuration fragment with an additional service — below):
```
# Watcher module component: get active window and activity.
[module.watcher]
path = "watcher"
log_mode = "log"
log_level = "info"
log_add_source = false

[module.watcher.options]
polling = "1s"

rules.paging = """
{
	"sublime_text":  "dev",
	"sublime_merge": "dev",
}.as(page_for, !(last.name in page_for || name in page_for) || page_for[?name] == page_for[?last.name] ? {} : {
	"method": "page",
	"params": {
		"page":    name in page_for ? page_for[name] : "default",
		"service": {"service":"kernel_default_service"}
	},
})
"""
```

A rule may also be used to blank the device when the screen is locked.
```
rules.blanking = """
locked == last.locked ? {} : {
	"method": "sleep",
	"params": {
		"action":  "set",
		"state":   locked ? "blanked" : "awake",
		"service": {"service":"kernel_default_service"}
	},
}
"""
```

These depend on a kernel service to assign a device. This can be set up in the kernel configuration by adding the following lines.
```
# Define a default kernel service.
[service.kernel_default_service]
serial = ""
```
Where `serial` is either default or the target device's serial.

See the example for the [`worklog`](../worklog) to see how a non-kernel call is handled.

## Detailers

`watcher` obtains details from the GUI environment. On Linux, by default this is from an X server, but this is configurable to allow alternatives. This is necessary when running under Wayland as Wayland does not make the required information available by design.

The configuration is made in the `options.strategy` configuration field.
```
[module.watcher.options]
strategy = "xorg"
polling = "1s"
```

Currently, optional Linux detailers are:
- `xorg`: use the X server (same as default)
- `gnome/mutter`: use the [User Activiy GNOME Shell extension](./extensions/user-activity@kortschak.io) in the extensions directory. Requires GNOME v45+.

On MacOS, details are obtained from the OS and no configuration is required.

## CEL optional types

The CEL environment enables the CEL [optional types library](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypes), [version 1](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypesVersion), and a function to obtain the current sleep state of a device and its last button event time, `sleep_state(<string>) -> <map<string,dyn>>` using the [`sleep` RPC method](https://pkg.go.dev/github.com/kortschak/dex/internal/device#SleepMessage).
