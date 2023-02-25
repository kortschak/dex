# ![dex logo](dex_logo_24.png) `dex`

`dex` is a work in progress El Gato Stream Deck controller system.

## Architecture

`dex` is built on a [JSON-RPC 2.0](https://www.jsonrpc.org/specification) message passing system with a message passing kernel managing a set of modules that can communicate with it and each other through it via the API described in the [rpc package](./rpc/rpc.go). Modules may provide services that perform actions in response to Stream Deck button presses or change button images in response to external state.

In addition to handling message passing, the kernel also provides a set of low level operations such as retaining persistent state in a key store and interacting with the Stream Deck device.

Modules running in the system are started by the kernel which passes a set of well-known command line flags to establish communication. These string flags MUST be handled by the module's executable at start.

- `-uid` — opaque session-unique ID of the module
- `-network` — network for communication ("unix" or "tcp")
- `-addr` — address for communication

In addition to these required flags, if the `log` `module.*.log_mode` configuration is used, the module must understand the boolean `log_stdout` flag which indicates that it should log to stdout instead of stderr.

## Configuration

The complete specification of the required by the system is defined in the [configuration schema](./config/config.go) using the [CUE language](https://cuelang.org/) and in the [Go types](./config/config.go) used to hold the configuration values.

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

All RPC methods expect call and notify parameters to be a JSON object that can be deserialised into an `rpc.Message` envelope which contains the time of the call, the module/service UID of the caller, and the call parameters in the `rpc.Message.Body` field.

Core methods provided by the kernel:

- `call` — JSON-RPC2.0 **call** with a body corresponding to `rcp.Message[rpc.Forward[any]]` forwarding the `rpc.Forward.Method` call to the module/service identified by `rpc.Forward.UID` with the parameters in `rpc.Forward.Params`.
- `notify` — JSON-RPC2.0 **notify** with a body corresponding to `rcp.Message[rpc.Forward[any]]` forwarding the `rpc.Forward.Method` notification to the module/service identified by `rpc.Forward.UID` with the parameters in `rpc.Forward.Params`.
- `unregister` — `rpc.Message[rpc.None]` to unregister the sending module and its services from the kernel's registry.
- `heartbeat` — `rpc.Message[rpc.Deadline]` record a heartbeat from the sending module with a deadline for the next expected heartbeat.
- `state` — `rpc.Message[rpc.None]` returns or logs the current [`rpc.SysState`](./rpc/rpc.go).

Extended methods provide by the kernel:

- [`system`](./internal/sys/funcs.go) — `rpc.Message[rpc.None]` returns or logs the current [`config.System`](./config/config.go).
- [`draw`](./internal/device/funcs.go) — `rpc.Message[device.DrawMessage]` draw an image to a device 
button.
- [`brightness`](./internal/device/funcs.go) — `rpc.Message[device.BrightnessMessage]` set a device's brightness.
- [`sleep`](./internal/device/funcs.go) — `rpc.Message[device.SleepMessage]` change a device's sleep state.
- [`get`](./internal/state/funcs.go) — `rpc.Message[state.GetMessage]` get a value from the state store.
- [`set`](./internal/state/funcs.go) — `rpc.Message[state.SetMessage]` set a value in the state store.
- [`put`](./internal/state/funcs.go) — `rpc.Message[state.PutMessage]` replace a value in the state store.
- [`delete`](./internal/state/funcs.go) — `rpc.Message[state.DeleteMessage]` delete a value from the state store.
- [`drop`](./internal/state/funcs.go) — `rpc.Message[rpc.None]` delete all state data associated with a service (identity in the `rpc.Message`).
- [`drop_module`](./internal/state/funcs.go) — `rpc.Message[rpc.None]` delete all state data associated with a module (identity in the `rpc.Message`).

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
    {"row" = 0, "col" = 1, "image" = "data:text/plain,state"},
    {"row" = 0, "col" = 1, "change" = "press", "do" = "state"}
]
```

#### Log the current system configuration

Pressing the button at row 0/column 2 will log the system configuration at INFO level.
```
[service.kernel_system]
serial = ""
listen = [
    {"row" = 0, "col" = 2, "image" = "data:text/plain,system"},
    {"row" = 0, "col" = 2, "change" = "press", "do" = "system"}
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

Modules have an additional setting, [`log_mode`](./config/config.go), that specifies how module logging is handled by the system; options are "log", "passthrough" and "none". The default behaviour is "passthrough". Modules must support the boolean `-log_stdout` flag to use the "log" option. When a module is passed a true `-log_stdout` it must log to stdout, but can emit arbitrary text to stderr.

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