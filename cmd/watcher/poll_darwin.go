// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
	"unsafe"

	// For get_active_app.applescript
	_ "embed"

	"golang.org/x/sys/execabs"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
)

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework CoreFoundation -framework CoreGraphics -framework Cocoa
#include <CoreFoundation/CoreFoundation.h>
#include <CoreGraphics/CGWindow.h>
#include <Cocoa/Cocoa.h>

struct details {
	int wid;
	int pid;
	const char* name;
	const char* window;
	double secondsAgo;
	bool isAsleep;
	bool isLocked;
	bool onConsole;
};

void activeWindow(struct details *d)
{
	d->secondsAgo = CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, kCGAnyInputEventType);
	d->isAsleep = CGDisplayIsAsleep(CGMainDisplayID());
	CFDictionaryRef session = CGSessionCopyCurrentDictionary();
	if (session != NULL) {
		d->isLocked = [[(id)session objectForKey:(__bridge NSString *)CFSTR("CGSSessionScreenIsLocked")] boolValue];
		d->onConsole = [[(id)session objectForKey:(NSString *)kCGSessionOnConsoleKey] boolValue];
		CFRelease(session);
	}
	NSArray *windows = (NSArray *)CGWindowListCopyWindowInfo(kCGWindowListExcludeDesktopElements|kCGWindowListOptionOnScreenOnly,kCGNullWindowID);
	for(NSDictionary *window in windows){
		int pid = [[window objectForKey:(NSString *)kCGWindowOwnerPID] intValue];
		if (pid == d->pid) {
			@autoreleasepool {
				d->wid = [[window objectForKey:(NSString *)kCGWindowNumber] intValue];
				const char *ownerName = [[window objectForKey:(NSString *)kCGWindowOwnerName] UTF8String];
				if (ownerName != NULL) {
					d->name = strdup(ownerName);
				}
				const char *windowName = [[window objectForKey:(NSString *)kCGWindowName] UTF8String];
				if (windowName != NULL) {
					d->window = strdup(windowName);
				}
			}
			if (windows != NULL) {
				// An overabundance of caution; we must have been non-null to get here.
				CFRelease(windows);
			}
			return;
		}
	}
	d->pid = -1;
	return;
}
*/
import "C"

func activeWindow() (watcher.Details, error) {
	// Do the fallback first because MacOS is garbage.
	pid, name, windowName, err := activeWindowDetails()
	t := C.struct_details{pid: C.int(pid)}
	C.activeWindow(&t)
	active := watcher.Details{
		WindowID:   int(t.wid), // Note that this is not reliable. MacOS is garbage.
		Name:       name,
		Class:      C.GoString(t.name),
		WindowName: C.GoString(t.window),
		LastInput:  time.Now().Add(time.Duration(float64(t.secondsAgo) * float64(-time.Second))),
		Locked:     bool(t.isLocked || t.isAsleep || !t.onConsole),
	}
	if t.name != nil {
		C.free(unsafe.Pointer(t.name))
	}
	if t.window != nil {
		C.free(unsafe.Pointer(t.window))
	}
	if active.WindowName == "" {
		active.WindowName = windowName
	}
	if err != nil {
		// Could not get window exact details name, but return what we have.
		err = warning{fmt.Errorf("failed to obtain some details: missing application or window name: %w", err)}
	}
	return active, err
}

// warning is a warn-only error.
type warning struct {
	error
}

//go:embed get_active_app.applescript
var getActiveApp string

func activeWindowDetails() (pid int, name, window string, err error) {
	cmd := execabs.Command("osascript", "-e", getActiveApp)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if err != nil {
		return 0, "", "", fmt.Errorf("%w: %s", err, &stderr)
	}
	var active struct {
		PID    int    `json:"pid"`
		Name   string `json:"name"`
		Window string `json:"window"`
	}
	err = json.Unmarshal(stdout.Bytes(), &active)
	if err != nil {
		err = fmt.Errorf("%w: %s", err, stdout.Bytes())
	}
	return active.PID, active.Name, active.Window, err
}
