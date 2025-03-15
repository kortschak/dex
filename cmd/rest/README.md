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

### ActivityWatch Web Watcher integration

It is possible to make use of the [ActivityWatch Web Watcher](https://github.com/ActivityWatch/aw-watcher-web) browser plugin to send AW event to the [`worklog` module](../worklog).

Assuming there is already a `rest` module configured, adding the following server definition will receive AW Web events and send them to `worklog`.
```
[module.rest.options.server.web_watcher]
addr = "localhost:5600"
request = """
request.Method == "OPTIONS" ?
	{
		"header": {
			"Access-Control-Allow-Origin": request.Header["Origin"], // or ["*"] or known origin.
			"Access-Control-Allow-Method": ["POST"],
			"Access-Control-Allow-Headers": ["content-type"],
		},
	}
: (request.Method == "POST" && request.URL.Path == "/api/0/buckets/aw-watcher-web-firefox_localhost/heartbeat") ?
	{
		"uid":    {"module": "worklog"},
		"from":   {"module": "web_watcher"},
		"method": "record",
		"params": {
			"details": decode_json(request.Body),
		},
	}
:
	{
		"status": 405,
	}
"""
```
Note that the OPTIONS method handler is only required for Firefox and is part of [Mozilla CORS handling](https://developer.mozilla.org/en-US/docs/Glossary/CORS). It is not currently required for Chromium.

This message can then be recorded by `worklog` by adding the following configuration snippet.
```
[module.worklog.options.rules.browser]
name = "web-watcher"
type = "browserdetail"
src = """
data_src != {"module":"web_watcher"} ? {} :
{
	"bucket":   curr.?data.icognito.orValue(false) ? "" : bucket,
	"start":    curr.time,
	"end":      curr.time,
	"data":     curr.data,
	"continue": false,
}
"""
```

## CEL optional types

The CEL environment enables the CEL [optional types library](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypes), [version 1](https://pkg.go.dev/github.com/google/cel-go/cel#OptionalTypesVersion).

## CEL extensions

The CEL environment provides the [`Lib`](https://pkg.go.dev/github.com/kortschak/dex/internal/celext#Lib) and [`StateLib`](https://pkg.go.dev/github.com/kortschak/dex/internal/celext#StateLib) extensions from the celext package, a JSON decoder to convert `bytes` JSON messages to objects, `<bytes>.decode_json() -> <dyn>`/`decode_json(<bytes>) -> <dyn>` and a base64 encoder, `<bytes>.base64() -> <string>`.

## Security

`rest` is a sharp tool; it may be used to open the dex system to clients outside the local host. This has security implications since requests within the dex JSON RPC-2.0 message passing system are trusted and so any external client may be able to send messages that the system will act on. In some cases this may include execution of arbitrary code, for example if `rest` is configured to pass unvetted messages to the `runner` module. It is recommended to not expose `rest` servers to outside hosts. If external connection is necessary, make use of TLS and mTLS configurations; `rest` enforces the use of mTLS on non-loopback devices unless the server is configured with the "insecure" option.
