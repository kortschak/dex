// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
)

/*
#cgo LDFLAGS: -lX11 -lXss
#include <stdbool.h>
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <X11/extensions/scrnsaver.h>

struct details {
	int wid;
	char* class;
	char* name;
	char* window;
	unsigned long idle;
	int saver_state;
};

#define enodisplay     1
#define enoscreensaver 2

int handleXError(Display *dpy, XErrorEvent *event);

Status get_focused_window(Display *display, Window *window_return);
Status get_window_classname(Display *display, Window window, char **class_ret, char **name_ret);
Status get_window_name(Display *display, Window window, char **name_ret);
Status get_screen_saver_info(Display *display, XScreenSaverInfo *saver_info);
char *get_window_property_by_atom(Display *display, Window window, Atom atom, long *nitems, Atom *type, int *size);

int activeWindow(struct details *d) {
	if (d == NULL) {
		return 0;
	}

	Status ok = 0;
	Window window;
	int flags = 0;

	XErrorHandler oldHandler = XSetErrorHandler(handleXError);

	Display *display = XOpenDisplay(NULL);
	if (display == NULL) {
		XSetErrorHandler(oldHandler);
		return -enodisplay;
	}
	XScreenSaverInfo saver_info;
	ok = get_screen_saver_info(display, &saver_info);
	if (!ok) {
		XCloseDisplay(display);
		XSetErrorHandler(oldHandler);
		return -enoscreensaver;
	}
	d->idle = saver_info.idle;
	d->saver_state = saver_info.state;
	ok = get_focused_window(display, &window);
	if (!ok) {
		XCloseDisplay(display);
		XSetErrorHandler(oldHandler);
		return flags;
	}
	flags = 1;
	d->wid = window;
	ok = get_window_classname(display, window, &(d->class), &(d->name));
	if (ok) {
		flags |= 2;
	}
	ok = get_window_name(display, window, &(d->window));
	if (ok) {
		flags |= 4;
	}
	XCloseDisplay(display);
	XSetErrorHandler(oldHandler);
	return flags;
}

void freeDetails(struct details *d) {
	if (d->class) {
		XFree((void*)d->class);
	}
	if (d->name) {
		XFree((void*)d->name);
	}
	if (d->window) {
		XFree((void*)d->window);
	}
}

Status get_focused_window(Display *display, Window *window_return) {
	int unused_revert_ret;
	Status ok;
	ok = XGetInputFocus(display, window_return, &unused_revert_ret);
	if (!ok) {
		return ok;
	}

	Window window = *window_return;
	Window unused_root_return, parent, *children = NULL;
	unsigned int nchildren;
	Atom atom_wmstate = XInternAtom(display, "WM_STATE", False);

	int done = False;
	while (!done) {
		if (window == None || window == PointerRoot) {
			return 0;
		}

		long items;
		get_window_property_by_atom(display, window, atom_wmstate, &items, NULL, NULL);

		if (items == 0) {
			XQueryTree(display, window, &unused_root_return, &parent, &children, &nchildren);
			if (children != NULL) {
				XFree(children);
			}
			window = parent;
		} else {
			*window_return = window;
			done = True;
		}
	}
	return 1;
}

Status get_screen_saver_info(Display *display, XScreenSaverInfo *saver_info) {
	int event_base, error_base = 0;
	Bool ok = XScreenSaverQueryExtension(display, &event_base, &error_base);
	if (!ok) {
		return 0;
	}
	Window root = XRootWindow(display, 0);
	return XScreenSaverQueryInfo(display, root, saver_info);
}

static Atom atom_NET_WM_NAME = -1;
static Atom atom_WM_NAME = -1;
static Atom atom_STRING = -1;
static Atom atom_UTF8_STRING = -1;

Status get_window_name(Display *display, Window window, char **name_ret) {
	if (atom_NET_WM_NAME == (Atom)-1) {
		atom_NET_WM_NAME = XInternAtom(display, "_NET_WM_NAME", False);
	}
	if (atom_WM_NAME == (Atom)-1) {
		atom_WM_NAME = XInternAtom(display, "WM_NAME", False);
	}
	if (atom_STRING == (Atom)-1) {
		atom_STRING = XInternAtom(display, "STRING", False);
	}
	if (atom_UTF8_STRING == (Atom)-1) {
		atom_UTF8_STRING = XInternAtom(display, "UTF8_STRING", False);
	}

	Atom type;
	int size;
	long nitems;
	*name_ret = get_window_property_by_atom(display, window, atom_NET_WM_NAME, &nitems, &type, &size);
	if (nitems == 0) {
		*name_ret = get_window_property_by_atom(display, window, atom_WM_NAME, &nitems, &type, &size);
	}
	return 1;
}

Status get_window_classname(Display *display, Window window, char **class_ret, char **name_ret) {
	XClassHint classhint;
	Status status = XGetClassHint(display, window, &classhint);
	if (status) {
		*class_ret = (char*)classhint.res_class;
		*name_ret = (char*)classhint.res_name;
	} else {
		*class_ret = NULL;
	}
	return status;
}

char *get_window_property_by_atom(Display *display, Window window, Atom atom, long *nitems, Atom *type, int *size) {
	Atom actual_type_return;
	int actual_format_return;
	unsigned long nitems_return;
	unsigned long unused_bytes_after;
	unsigned char *prop_return;

	Status status = XGetWindowProperty(display, window, atom, 0, (~0L),
		False, AnyPropertyType, &actual_type_return,
		&actual_format_return, &nitems_return, &unused_bytes_after,
		&prop_return);
	if (status != Success) {
	  return NULL;
	}
	if (nitems != NULL) {
		*nitems = nitems_return;
	}
	if (type != NULL) {
		*type = actual_type_return;
	}
	if (size != NULL) {
		*size = actual_format_return;
	}
	return (char*)prop_return;
}
*/
import "C"

func init() {
	detailers[(&xOrgDetailer{}).strategy()] = newXOrgDetailer
}

func newXOrgDetailer() (detailer, error) {
	return &xOrgDetailer{last: time.Now()}, nil
}

type xOrgDetailer struct {
	last time.Time
}

func (*xOrgDetailer) strategy() string { return "xorg" }

func (d *xOrgDetailer) details() (watcher.Details, error) {
	var det C.struct_details
	flags := C.activeWindow(&det)
	if flags < 0 {
		return watcher.Details{}, xOrgDetailError(-flags)
	}
	if det.idle > 0 {
		d.last = time.Now().Add(time.Duration(det.idle) * -time.Millisecond).Round(time.Second / 10)
	}
	active := watcher.Details{
		WindowID:   int(det.wid),
		Name:       C.GoString(det.name),
		Class:      C.GoString(det.class),
		WindowName: C.GoString(det.window),
		LastInput:  d.last,
		Locked:     det.saver_state == C.ScreenSaverOn,
	}
	C.freeDetails(&det)
	if flags != 0b111 && !active.Locked {
		return active, warning{fmt.Errorf("failed to obtain some details: missing %s", missing(flags))}
	}
	return active, nil
}

type xOrgDetailError int

func (e xOrgDetailError) Error() string {
	if 0 < e && int(e) < len(failureReason) {
		return "failed to obtain details: " + failureReason[e]
	}
	return "failed to obtain details: error " + strconv.Itoa(int(e))
}

var failureReason = [...]string{
	C.enodisplay:     "no display",
	C.enoscreensaver: "no screensaver info",
}

// warning is a warn-only error.
type warning struct {
	error
}

func missing(flags C.int) string {
	m := make([]string, 0, 3)
	if flags&0b001 == 0 {
		m = append(m, "window id")
	}
	if flags&0b010 == 0 {
		m = append(m, "class name")
	}
	if flags&0b100 == 0 {
		m = append(m, "window name")
	}
	return strings.Join(m, ", ")
}
