# `runner`

`runner` is a module that runs arbitrary executables.

Example configuration fragment (requires a kernel configuration fragment with a Stream Deck device configured):
```
# Runner module component: run executables.
[module.runner]
path = "runner"
log_mode = "log"
log_level = "error"
log_add_source = true

[service.subl]
module = "runner"
serial = ""
listen = [
	{"row" = 1, "col" = 0, "image" = "data:text/plain,subl"},
	{"row" = 1, "col" = 0, "change" = "press", "do" = "run", "args" = {"path" = "subl"}}
]

[service.smerge]
module = "runner"
serial = ""
listen = [
	{"row" = 1, "col" = 1, "image" = "data:text/plain,smerge"},
	{"row" = 1, "col" = 1, "change" = "press", "do" = "run", "args" = {"path" = "smerge"}}
]
```
