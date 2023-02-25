// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package config provides live configuration reloading and unification
// functions.
package config

import "github.com/kortschak/dex/config"

// Alias the publicly visible types.
type (
	System  = config.System
	Kernel  = config.Kernel
	Module  = config.Module
	Service = config.Service
	Device  = config.Device
	Button  = config.Button
	Sum     = config.Sum
)

const (
	kernelName  = "kernel"
	moduleName  = "module"
	serviceName = "service"
)

// fragmentSchema is the schema for a valid configuration fragment. This
// does not guarantee a valid schema, but is relaxed to allow incomplete
// configurations to be vetted. See [config.Schema] for the complete schema.
const fragmentSchema = `
{
	kernel?:  _#kernel
	module?:  {[string]: _#module}
	service?: {[string]: _#service}
}

_#kernel: {
	device?:         [... _#device] // default applied at unification.
	network?:        "tcp" | "unix"
	log_level?:      _#log_level
	log_add_source?: bool
	options?:        {[string]: _}
}

_#device: {
	pid:      *0 | uint16
	serial:   *"" | string
	default?: string
}

_#module: {
	path?:      string // !="" test done at unification.
	args?:      [... string]
	log_level?: _#log_level 
	log_mode?:  _#log_mode 
	options?:   {[string]: _}
	schema?:    string
}

_#service: {
	module?:  string // !="" test done at unification.
	serial?:  string // validated at unification.
	listen?:  [... _#button]
	options?: {[string]: _}
}

_#button: B={
	row:     uint
	col:     uint
	page?:   string
	change?: "press" | "release"
	do?:     string
	args?:   _
	if B.change == _|_ {
		do?:  ""
		args: null   
	}
	image?:  _#data_uri
}

_#data_uri: _#text | _#image | _#image_file | _#named_color | _#web_color
_#text: =~"^data:text/plain,.*$"
_#image: =~"^data:image/\\*;base64,.*$"
_#image_file: =~"^data:text/filename,.*$"
_#named_color: =~"^data:image/color;name,(?:hi)?(?:black|red|green|yellow|blue|magenta|cyan|white)$"
_#web_color: =~"^data:image/color;web,#[0-9a-fA-F]{6}$"

_#log_level: =~"(?i)^(?:debug|info|warn|error)$"
_#log_mode: "log" | "passthrough" | "none"
`
