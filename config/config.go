// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package config provides dex system configuration types and schemas.
package config

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/ardilla"
	"github.com/kortschak/jsonrpc2"

	"github.com/kortschak/dex/rpc"
)

// System is a complete configuration.
type System struct {
	Kernel   *Kernel             `json:"kernel,omitempty" toml:"kernel"`
	Modules  map[string]*Module  `json:"module,omitempty" toml:"module"`
	Services map[string]*Service `json:"service,omitempty" toml:"service"`
}

// Kernel is the kernel configuration.
type Kernel struct {
	// Device is the set of physical devices the kernel is handling.
	// If Device is [{0, ""}], it is handling the first available device.
	Device []Device `json:"device,omitempty" toml:"device"`
	// Missing is the list of devices that are not required and are
	// not present.
	Missing []string `json:"missing,omitempty"`
	// Network is the network the kernel is communicating on.
	Network   string      `json:"network,omitempty" toml:"network"`
	LogLevel  *slog.Level `json:"log_level,omitempty" toml:"log_level"`
	AddSource *bool       `json:"log_add_source,omitempty" toml:"log_add_source"`
	// Options is a bag of arbitrary configuration values.
	// See Schema for valid entries.
	Options map[string]any `json:"options,omitempty" toml:"options"`

	Sum *Sum  `json:"sum,omitempty"`
	Err error `json:"err,omitempty"`
}

type Device struct {
	// PID is the product ID of the device.
	PID ardilla.PID `json:"pid,omitempty" toml:"pid"`
	// Serial is the device serial numbers.
	Serial *string `json:"serial,omitempty" toml:"serial"`
	// Default is the default page name for the device.
	// If Default is nil, the device default name is used.
	Default *string `json:"default,omitempty" toml:"default"`
	// Required indicates the configuration may not
	// continue if the device is not available.
	Required bool `json:"required,omitempty" toml:"required"`
}

// Module is a module configuration.
type Module struct {
	// Path is the path to the module's executable.
	Path string `json:"path,omitempty" toml:"path"`
	// Args is any additional arguments pass to the module's
	// executable at start up.
	Args      []string    `json:"args,omitempty" toml:"args"`
	LogLevel  *slog.Level `json:"log_level,omitempty" toml:"log_level"`
	AddSource *bool       `json:"log_add_source,omitempty" toml:"log_add_source"`
	// LogMode specifies how module logging is handled
	// by the system; options are "log", "passthrough"
	// and "none". The default behaviour is "passthrough".
	// Modules must support the -log_stdout flag to use
	// the "log" option. This flag will be added if not
	// already included in Args.
	//
	//  log:         stdout → stderr
	//               stderr → capture and log via system logger
	//
	//  passthrough: stdout → stdout
	//               stderr → stderr
	//
	//  none:        stdout → /dev/null
	//               stderr → /dev/null
	//
	LogMode string `json:"log_mode,omitempty" toml:"log_mode"`
	// Options is a bag of arbitrary configuration values.
	// Valid values are module-specific.
	Options map[string]any `json:"options,omitempty" toml:"options"`
	// Schema is the CUE schema for vetting module and services.
	// If Schema is empty, DeviceDependency is used.
	Schema string `json:"schema,omitempty"`

	Sum *Sum  `json:"sum,omitempty"`
	Err error `json:"err,omitempty"`
}

// Service is a module service configuration.
type Service struct {
	// Name is used to indicate to a module the service
	// to configure in a configure method call.
	Name string `json:"name,omitempty"`
	// Active indicates the state of the service.
	// If a configure call with Active false is made
	// the service is deconfigured.
	Active *bool `json:"active,omitempty"`

	// Module is the module the service depends on.
	Module *string `json:"module,omitempty" toml:"module"`
	// Serial is the device's serial number the service is using.
	// This must correspond to a serial number held by the kernel.
	Serial *string `json:"serial,omitempty" toml:"serial"`
	// Listen is the set of buttons the service expects to be
	// notified of changes in.
	Listen []Button `json:"listen,omitempty" toml:"listen"`
	// Options is a bag of arbitrary configuration values.
	// Valid values are service-specific.
	Options map[string]any `json:"options,omitempty" toml:"options"`

	Sum *Sum  `json:"sum,omitempty"`
	Err error `json:"err,omitempty"`
}

// IsService returns whether the request is a service configuration.
// If err is nil and ok is false, the request is a module configuration.
func IsService(req *jsonrpc2.Request) (ok bool, err error) {
	if req.Method != rpc.Configure {
		return false, rpc.NewError(rpc.ErrCodeInvalidMessage,
			fmt.Sprintf("not a configuration request: %s", req.Method),
			map[string]any{
				"type":   rpc.ErrCodeMethod,
				"method": req.Method,
			},
		)
	}
	var probe rpc.Message[struct {
		// Name is the service name if the
		// configuration is for a service.
		Name *string `json:"name"`
	}]
	err = json.Unmarshal(req.Params, &probe)
	if err != nil {
		return false, err
	}
	return probe.Body.Name != nil, nil
}

// Button indicates actions associated with a button location and
// state change.
type Button struct {
	Row    int     `json:"row" toml:"row"`
	Col    int     `json:"col" toml:"col"`
	Page   string  `json:"page,omitempty" toml:"page"`
	Change *string `json:"change,omitempty" toml:"change"`
	Do     *string `json:"do,omitempty" toml:"do"`
	Args   any     `json:"args,omitempty" toml:"args"`
	Image  string  `json:"image,omitempty" toml:"image"`
}

// Schema is the schema for a valid configuration.
const Schema = `
{
	kernel:   _#kernel
	module?:  {[string]: _#module}
	service?: {[string]: _#service}
}

_#kernel: {
	device:          *[{pid: 0, serial: ""}] | [... _#device]
	network:         "tcp" | "unix"
	log_level?:      _#log_level 
	log_add_source?: bool 
	options?:        {
		repair:   bool
		[string]: _
	}
}

_#device: {
	pid:       *0 | uint16
	serial:    *"" | string
	default?:  string
	required?: bool
}

_#module: {
	path:       !=""
	args?:      [... string]
	log_level?: _#log_level 
	log_mode?:  _#log_mode 
	options?:   {[string]: _}
	schema?:    string
}

_#service: {
	module?:  !=""
	serial?:  string
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
_#text: =~"^data:text/plain(?:;[^;]+=[^;]*)*,.*$"
_#image: =~"^data:image/\\*(?:;[^;]+=[^;]*)*;base64,.*$"
_#image_file: =~"^data:text/filename(?:;[^;]+=[^;]*)*,.*$"
_#named_color: =~"^data:image/color(?:;[^;]+=[^;]*)*;name,(?:hi)?(?:black|red|green|yellow|blue|magenta|cyan|white)$"
_#web_color: =~"^data:image/color(?:;[^;]+=[^;]*)*;web,#[0-9a-fA-F]{6}$"

_#log_level: =~"(?i)^(?:debug|info|warn|error)$"
_#log_mode: "log" | "passthrough" | "none"
`

// ModuleDependency is the module dependency schema for service and module
// validation.
const ModuleDependency = `
{
	module: [_]: {
		...
	}

	#Module: or([for k, v in module{k}])
	service: [_]: {
		module?: #Module
		...
	}
}
`

// DeviceDependency is the device serial dependency schema for service
// validation.
const DeviceDependency = `
{
	kernel: device: *[{pid: 0, serial: ""}] | [... {serial: string}]

	module: [_]: {
		...
	}

	service: [_]: S={
		if S.listen != _|_ {
			serial!: or([for v in kernel.device{v.serial}]) | _|_
		}
		...
	}
}
`

// Sum is a comparable optional SHA-1 sum.
type Sum [sha1.Size]byte

// Equal returns whether s is equal to other.
func (s *Sum) Equal(other *Sum) bool {
	switch {
	case s == other:
		return true
	case s != nil && other != nil:
		return *s == *other
	default:
		return false
	}
}

func (s *Sum) String() string {
	if s == nil {
		return ""
	}
	return hex.EncodeToString(s[:])
}

func (s *Sum) UnmarshalText(text []byte) error {
	if len(text) != hex.EncodedLen(len(s)) {
		return fmt.Errorf("invalid length: %d != %d", len(text), hex.EncodedLen(len(s)))
	}
	_, err := hex.Decode(s[:], text)
	if err != nil {
		return err
	}
	return nil
}

func (s *Sum) MarshalText() (text []byte, err error) {
	if s == nil {
		return nil, nil
	}
	text = make([]byte, hex.EncodedLen(len(s)))
	hex.Encode(text, s[:])
	return text, nil
}

// RoundTrip sends v on an RPC round-trip via the provided config type T and
// compares the result to the input. It returns an error if they do not match.
func RoundTrip[T interface{ Kernel | Module | Service }, C any](v C) (C, error) {
	var zero C
	b, err := json.Marshal(rpc.Message[C]{Body: v})
	if err != nil {
		return zero, err
	}
	var m rpc.Message[T]
	err = rpc.UnmarshalMessage(b, &m)
	if err != nil {
		return zero, err
	}
	b, err = json.Marshal(m)
	if err != nil {
		return zero, err
	}
	var r rpc.Message[C]
	err = rpc.UnmarshalMessage(b, &r)
	if err != nil {
		return zero, err
	}
	if !cmp.Equal(v, r.Body) {
		err = fmt.Errorf("mismatch after round-trip:\n%s", cmp.Diff(v, r.Body))
	}
	return r.Body, err
}
