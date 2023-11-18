# ![dex logo](dex_logo_24.png) `dex`

`dex` is a work in progress El Gato Stream Deck controller system.

## Architecture

`dex` is built on a [JSON-RPC 2.0](https://www.jsonrpc.org/specification) message passing system with a message passing kernel managing a set of modules that can communicate with it and each other through it via the API described in the [rpc package](https://pkg.go.dev/github.com/kortschak/dex/rpc). Modules may provide services that perform actions in response to Stream Deck button presses or change button images in response to external state.

In addition to handling message passing, the kernel also provides a set of low level operations such as retaining persistent state in a key store and interacting with the Stream Deck device.

Modules running in the system are started by the kernel which passes a set of well-known command line flags to establish communication. These string flags MUST be handled by the module's executable at start.

- `-uid` — opaque session-unique ID of the module
- `-network` — network for communication ("unix" or "tcp")
- `-addr` — address for communication

In addition to these required flags, if the `log` `module.*.log_mode` configuration is used, the module must understand the boolean `log_stdout` flag which indicates that it should log to stdout instead of stderr.

## Configuration

The complete specification of the required by the system is defined in the [configuration schema](https://pkg.go.dev/github.com/kortschak/dex/config#pkg-constants) using the [CUE language](https://cuelang.org/) and in the [Go types](https://pkg.go.dev/github.com/kortschak/dex/config#System) used to hold the configuration values.

Configurations are represented on disk as TOML files and may be split across multiple files.

Configuration files MUST be in `${XDG_CONFIG_HOME}/dex/` (`~/.config/dex/` on Linux and `~/Library/Application Support/dex/` on Mac) and this directory will be created at start up if it does not exist.

The minimal system configuration is
```
[kernel]
device = []
network = "unix"
```
which is a no-op system that brings up a kernel using Unix sockets, with no modules running, and does not attempt to find a Stream Deck device.

Each module and service it provides MUST use configurations consistent with the global schema, but MAY also have specific configuration options under `module.*.options`. Modules SHOULD use an api package so that configuration and RPC types are visible publicly. If `module.*.schema` exists it is interpreted as a CUE schema and the complete configuration will be validated against this schema in addition to the global schema.

## Kernel RPC methods

All RPC methods expect call and notify parameters to be a JSON object that can be deserialised into an [`rpc.Message`](https://pkg.go.dev/github.com/kortschak/dex/rpc#Message) envelope which contains the time of the call, the module/service UID of the caller, and the call parameters in the `rpc.Message.Body` field.

Core methods provided by the kernel:

- `call` — JSON-RPC2.0 **call** with a body corresponding to [`rcp.Message[rpc.Forward[any]]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#Forward) forwarding the `rpc.Forward.Method` call to the module/service identified by `rpc.Forward.UID` with the parameters in `rpc.Forward.Params`.
- `notify` — JSON-RPC2.0 **notify** with a body corresponding to [`rcp.Message[rpc.Forward[any]]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#Forward) forwarding the `rpc.Forward.Method` notification to the module/service identified by `rpc.Forward.UID` with the parameters in `rpc.Forward.Params`.
- `unregister` — [`rpc.Message[rpc.None]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#None) to unregister the sending module and its services from the kernel's registry.
- `heartbeat` — [`rpc.Message[rpc.Deadline]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#Deadline) record a heartbeat from the sending module with a deadline for the next expected heartbeat.
- `state` — [`rpc.Message[rpc.None]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#None) returns or logs the current [`rpc.SysState`](https://pkg.go.dev/github.com/kortschak/dex/rpc#SysState).

Extended methods provide by the kernel:

- [`system`](https://pkg.go.dev/github.com/kortschak/dex/internal/sys#Funcs) — [`rpc.Message[rpc.None]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#None) returns or logs the current [`config.System`](https://pkg.go.dev/github.com/kortschak/dex/config#System).
- [`draw`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#Funcs) — [`rpc.Message[device.DrawMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#DrawMessage) draw an image to a device 
button.
- [`page`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#Funcs) — [`rpc.Message[device.PageMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#PageMessage) change the displayed page.
- [`page_names`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#Funcs) — [`rpc.Message[device.PageStateMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#PageStateMessage) returns or logs the list of the device's page names for a service.
- [`page_details`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#Funcs) — [`rpc.Message[device.PageStateMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#PageStateMessage) returns of logs the device's page state for a service.
- [`brightness`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#Funcs) — [`rpc.Message[device.BrightnessMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#BrightnessMessage) set a device's brightness.
- [`sleep`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#Funcs) — [`rpc.Message[device.SleepMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/device#SleepMessage) change a device's sleep state.
- [`get`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#Funcs) — [`rpc.Message[state.GetMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#GetMessage) get a value from the state store.
- [`set`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#Funcs) — [`rpc.Message[state.SetMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#SetMessage) set a value in the state store.
- [`put`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#Funcs) — [`rpc.Message[state.PutMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#PutMessage) replace a value in the state store.
- [`delete`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#Funcs) — [`rpc.Message[state.DeleteMessage]`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#DeleteMessage) delete a value from the state store.
- [`drop`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#Funcs) — [`rpc.Message[rpc.None]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#None) delete all state data associated with a service (identity in the `rpc.Message`).
- [`drop_module`](https://pkg.go.dev/github.com/kortschak/dex/internal/state#Funcs) — [`rpc.Message[rpc.None]`](https://pkg.go.dev/github.com/kortschak/dex/rpc#None) delete all state data associated with a module (identity in the `rpc.Message`).

## Provided modules

### `rest`

[`rest`](./cmd/rest) is a module that can be used to run forward REST API calls to other modules and the kernel.

### `runner`

[`runner`](./cmd/runner) is a module that can be used to run arbitrary executables. `runner` services allow buttons to start programs to perform actions.

### `watcher`

[`watcher`](./cmd/watcher) is a module that polls the host's user activity, and active application and window. This information can then be sent to other modules to use.

### `worklog`

[`worklog`](./cmd/worklog) is a module that can receive polling data from other modules such as `watcher` to keep a log of active application, AFK status and so on.

### The kernel module

The kernel is a special module that has direct access to kernel RPC methods. It is specified in the configuration by omitting the module key for the service. For example, the following two services that are useful in debugging kernel configuration.

#### Log the current kernel state

Pressing the button at row 0/column 1 will log the kernel state at INFO level.
```
[service.kernel_state]
serial = ""
listen = [
    {row = 0, col = 1, image = "data:text/plain,state"},
    {row = 0, col = 1, change = "press", do = "state"}
]
```

#### Log the current system configuration

Pressing the button at row 0/column 2 will log the system configuration at INFO level.
```
[service.kernel_system]
serial = ""
listen = [
    {row = 0, col = 2, image = "data:text/plain,system"},
    {row = 0, col = 2, change = "press", do = "system"}
]
```

## Pages

`dex` has a notion of pages. A page is a set of buttons and actions that are presented as a unit. The current page can be changed using the `page` RPC call.

For example, the state logging actions above could be put into a "debug" page which is togglable with the button at row 1/column 0.

```
[service.kernel_debug]
serial = ""
listen = [
    {row = 1, col = 0, image = "data:text/plain,debug"},
    {row = 1, col = 0, change = "press", do = "page", args = {page = "debug"}},
    {page = "debug", row = 1, col = 0, image = "data:text/plain,home"},
    {page = "debug", row = 1, col = 0, change = "press", do = "page", args = {page = "default"}}
]

[service.kernel_state]
serial = ""
listen = [
    {page = "debug", row = 0, col = 1, image = "data:text/plain,state"},
    {page = "debug", row = 0, col = 1, change = "press", do = "state"}
]

[service.kernel_system]
serial = ""
listen = [
    {page = "debug", row = 0, col = 2, image = "data:text/plain,system"},
    {page = "debug", row = 0, col = 2, change = "press", do = "system"}
]

[service.kernel_page_details]
serial = ""
listen = [
    {page = "debug", row = 0, col = 3, image = "data:text/plain,page details"},
    {page = "debug", row = 0, col = 3, change = "press", do = "page_details"}
]
```

The set of available pages can be obtained using the `page_names` RPC call. The following button action in the "debug" page logs the list of pages at INFO level.
```
[service.kernel_page_names]
serial = ""
listen = [
    {page = "debug", row = 0, col = 3, image = "data:text/plain,pages"},
    {page = "debug", row = 0, col = 3, change = "press", do = "page_names"}
]
```

## `minidex`

[`minidex`](./minidex) is a greatly simplified Stream Deck controller that does not use RPC and is intended primarily for debugging the device management components of the kernel.

## Logging

The `dex` system and provided modules all provide configurable structured logging using [log/slog](https://pkg.go.dev/log/slog).

Log level for the kernel and modules are specified in their config stanzas using the `log_level` option. Source location of the log calls can be added by setting `log_add_source` to `true`. Both of these options are dynamically reloaded during run time.

```
[kernel]
device = ...
network = ...
log_level = "info"
log_add_source = false
```

```
[module. ...]
path = ...
log_mode = "log"
log_level = "error"
log_add_source = true
```

Modules have an additional setting, [`log_mode`](https://pkg.go.dev/github.com/kortschak/dex/config#Module), that specifies how module logging is handled by the system; options are "log", "passthrough" and "none". The default behaviour is "passthrough". Modules must support the boolean `-log_stdout` flag to use the "log" option. When a module is passed a true `-log_stdout` it must log to stdout, but can emit arbitrary text to stderr.

- log:
    stdout → stderr
    stderr → capture and log via system logger

- passthrough:
    stdout → stdout
    stderr → stderr

- none:
    stdout → /dev/null
    stderr → /dev/null

The `log_mode` option is static and set when the module is spawned.