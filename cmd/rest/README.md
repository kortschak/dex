# `rest`

`rest` is a module that transforms and forwards REST API calls to either the kernel or other modules.

Example configuration fragment (requires a kernel configuration fragment):
```
# REST module component: forward REST notifications.
[module.rest]
path = "rest"
log_mode = "log"
log_level = "error"
log_add_source = false

# Get the current system state with a request to localhost:7474.
[module.rest.options.server.state]
addr = ":7474"
request = """
{"method": "state"}
"""
response = """
response.body
"""
```

If the response program is present, a JSON RPC-2.0 Call is invoked, otherwise a Notify is used. The fragment above without the response program will log the state in the kernel log, but will not return it to the REST client.

The response program may return the following CEL types:

- bytes/string
- int/uint
- double
- bool
- timestamp (will be formatted as time.RFC3339Nano)
- duration (will be formatted as nanoseconds)
- JSON object or array

## CEL optional types

The CEL environment enables the CEL [optional types library](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypes), [version 1](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypesVersion).

## CEL extensions

The CEL environment provide extensions from the [celext package](https://pkg.go.dev/github.com/kortschak/dex/internal/celext#Lib), a JSON decoder to convert `bytes` JSON messages to objects, `<bytes>.decode_json() -> <dyn>`/`decode_json(<bytes>) -> <dyn>` and a base64 encoder, `<bytes>.base64() -> <string>`.

## Security

`rest` is a sharp tool; it may be used to open the dex system to clients outside the local host. This has security implications since requests within the dex JSON RPC-2.0 message passing system are trusted and so any external client may be able to send messages that the system will act on. In some cases this may include execution of arbitrary code, for example if `rest` is configured to pass unvetted messages to the `runner` module. It is recommended to not expose `rest` servers to outside hosts. If external connection is necessary, make use of TLS and mTLS configurations; `rest` enforces the use of mTLS on non-loopback devices unless the server is configured with the "insecure" option.