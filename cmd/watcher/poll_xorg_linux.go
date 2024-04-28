// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
	"github.com/kortschak/dex/cmd/watcher/dl"
)

/*
#include <stdbool.h>
#include <X11/Xlib.h>
#include <X11/Xutil.h>
#include <scrnsaver.h>
#include <XRes.h>

struct details {
	int wid;
	pid_t pid;
	char* class;
	char* name;
	char* window;
	unsigned long idle;
	int saver_state;
};

typedef Display* (*xOpenDisplay) (_Xconst char *display_name);
typedef int (*xCloseDisplay) (Display *display);
typedef XErrorHandler (*xSetErrorHandler) (XErrorHandler handler);
typedef int (*xGetInputFocus) (Display *display, Window *focus_return, int *revert_to_return );
typedef Window (*xRootWindow) (Display* display, int screen_number);
typedef Atom (*xInternAtom) (Display *display, _Xconst char *atom_name, Bool only_if_exists);
typedef Status (*xQueryTree) (Display *display, Window w, Window *root_return, Window *parent_return, Window **children_return, unsigned int *nchildren_return);
typedef Status (*xGetClassHint) (Display* display, Window w, XClassHint* class_hints_return);
typedef int (*xGetWindowProperty) (Display *display, Window w, Atom property, long long_offset, long long_length, Bool delete, Atom req_type, Atom *actual_type_return, int *actual_format_return, unsigned long *nitems_return, unsigned long *bytes_after_return, unsigned char **prop_return);
typedef void (*xFree) (void *data);
struct X11Lib {
	xOpenDisplay XOpenDisplay;
	xCloseDisplay XCloseDisplay;
	xSetErrorHandler XSetErrorHandler;
	xGetInputFocus XGetInputFocus;
	xRootWindow XRootWindow;
	xInternAtom XInternAtom;
	xQueryTree XQueryTree;
	xGetClassHint XGetClassHint;
	xGetWindowProperty XGetWindowProperty;
	xFree XFree;
};

typedef Bool (*xScreenSaverQueryExtension) (Display *display, int *event_base, int *error_base);
typedef Status (*xScreenSaverQueryInfo) (Display *display, Drawable drawable, XScreenSaverInfo *info);
struct XssLib {
	xScreenSaverQueryExtension XScreenSaverQueryExtension;
	xScreenSaverQueryInfo XScreenSaverQueryInfo;
};

typedef Status (*xResQueryClientIds) (Display *dpy, long num_specs, XResClientIdSpec *client_specs, long *num_ids, XResClientIdValue **client_ids);
typedef pid_t (*xResGetClientPid) (XResClientIdValue* value);
typedef void (*xResClientIdsDestroy) (long num_ids, XResClientIdValue *client_ids);
struct XResLib {
	xResQueryClientIds XResQueryClientIds;
	xResGetClientPid XResGetClientPid;
	xResClientIdsDestroy XResClientIdsDestroy;
};

#define enodisplay     1

#define fdisplay       1
#define fclassname     2
#define fwindowname    4
#define fscreensaver   8

int handleXError(Display *dpy, XErrorEvent *event);

pid_t get_window_pid(struct XResLib *lib, Display *display, Window window);
Status get_focused_window(struct X11Lib *lib, Display *display, Window *window_return);
Status get_window_classname(struct X11Lib *lib, Display *display, Window window, char **class_ret, char **name_ret);
Status get_window_name(struct X11Lib *lib, Display *display, Window window, char **name_ret);
Status get_screen_saver_info(struct X11Lib *x11Lib, struct XssLib *xssLib, Display *display, XScreenSaverInfo *saver_info);
char *get_window_property_by_atom(struct X11Lib *lib, Display *display, Window window, Atom atom, long *nitems, Atom *type, int *size);

int activeWindow(struct details *d, struct X11Lib *lib, struct XResLib *xResLib, struct XssLib *xssLib) {
	if (d == NULL) {
		return 0;
	}

	Status ok = 0;
	Window window;
	int flags = 0;

	XErrorHandler oldHandler = lib->XSetErrorHandler(handleXError);

	Display *display = lib->XOpenDisplay(NULL);
	if (display == NULL) {
		lib->XSetErrorHandler(oldHandler);
		return -enodisplay;
	}
	XScreenSaverInfo saver_info;
	ok = get_screen_saver_info(lib, xssLib, display, &saver_info);
	if (ok) {
		d->idle = saver_info.idle;
		d->saver_state = saver_info.state;
		flags |= fscreensaver;
	}
	ok = get_focused_window(lib, display, &window);
	if (!ok) {
		lib->XCloseDisplay(display);
		lib->XSetErrorHandler(oldHandler);
		return flags;
	}
	flags |= fdisplay;
	d->wid = window;
	d->pid = get_window_pid(xResLib, display, window);
	ok = get_window_classname(lib, display, window, &(d->class), &(d->name));
	if (ok) {
		flags |= fclassname;
	}
	ok = get_window_name(lib, display, window, &(d->window));
	if (ok) {
		flags |= fwindowname;
	}
	lib->XCloseDisplay(display);
	lib->XSetErrorHandler(oldHandler);
	return flags;
}

void freeDetails(struct X11Lib *lib, struct details *d) {
	if (d->class) {
		lib->XFree((void*)d->class);
	}
	if (d->name) {
		lib->XFree((void*)d->name);
	}
	if (d->window) {
		lib->XFree((void*)d->window);
	}
}

pid_t get_window_pid(struct XResLib *lib, Display *display, Window window) {
	pid_t pid = -1;

	if (lib == NULL) {
		return pid;
	}

	XResClientIdSpec spec = {
		.client = window,
		.mask   = XRES_CLIENT_ID_PID_MASK,
	};
	long num_ids;
	XResClientIdValue *client_ids;

	lib->XResQueryClientIds(display, 1, &spec, &num_ids, &client_ids);
	for (int i = 0; i < num_ids; i++) {
		if (client_ids[i].spec.mask == XRES_CLIENT_ID_PID_MASK) {
			pid = lib->XResGetClientPid(&client_ids[i]);
			break;
		}
	}
	lib->XResClientIdsDestroy(num_ids, client_ids);

	return pid;
}

Status get_focused_window(struct X11Lib *lib, Display *display, Window *window_return) {
	int unused_revert_ret;
	Status ok;
	ok = lib->XGetInputFocus(display, window_return, &unused_revert_ret);
	if (!ok) {
		return ok;
	}

	Window window = *window_return;
	Window unused_root_return, parent, *children = NULL;
	unsigned int nchildren;
	Atom atom_wmstate = lib->XInternAtom(display, "WM_STATE", False);

	int done = False;
	while (!done) {
		if (window == None || window == PointerRoot) {
			return 0;
		}

		long items;
		get_window_property_by_atom(lib, display, window, atom_wmstate, &items, NULL, NULL);

		if (items == 0) {
			lib->XQueryTree(display, window, &unused_root_return, &parent, &children, &nchildren);
			if (children != NULL) {
				lib->XFree(children);
			}
			window = parent;
		} else {
			*window_return = window;
			done = True;
		}
	}
	return 1;
}

Status get_screen_saver_info(struct X11Lib *x11Lib, struct XssLib *xssLib, Display *display, XScreenSaverInfo *saver_info) {
	if (xssLib == NULL) {
		return 0;
	}
	int event_base, error_base = 0;
	Bool ok = xssLib->XScreenSaverQueryExtension(display, &event_base, &error_base);
	if (!ok) {
		return 0;
	}
	Window root = x11Lib->XRootWindow(display, 0);
	return xssLib->XScreenSaverQueryInfo(display, root, saver_info);
}

static Atom atom_NET_WM_NAME = -1;
static Atom atom_WM_NAME = -1;
static Atom atom_STRING = -1;
static Atom atom_UTF8_STRING = -1;

Status get_window_name(struct X11Lib *lib, Display *display, Window window, char **name_ret) {
	if (atom_NET_WM_NAME == (Atom)-1) {
		atom_NET_WM_NAME = lib->XInternAtom(display, "_NET_WM_NAME", False);
	}
	if (atom_WM_NAME == (Atom)-1) {
		atom_WM_NAME = lib->XInternAtom(display, "WM_NAME", False);
	}
	if (atom_STRING == (Atom)-1) {
		atom_STRING = lib->XInternAtom(display, "STRING", False);
	}
	if (atom_UTF8_STRING == (Atom)-1) {
		atom_UTF8_STRING = lib->XInternAtom(display, "UTF8_STRING", False);
	}

	Atom type;
	int size;
	long nitems;
	*name_ret = get_window_property_by_atom(lib, display, window, atom_NET_WM_NAME, &nitems, &type, &size);
	if (nitems == 0) {
		*name_ret = get_window_property_by_atom(lib, display, window, atom_WM_NAME, &nitems, &type, &size);
	}
	return 1;
}

Status get_window_classname(struct X11Lib *lib, Display *display, Window window, char **class_ret, char **name_ret) {
	XClassHint classhint;
	Status status = lib->XGetClassHint(display, window, &classhint);
	if (status) {
		*class_ret = (char*)classhint.res_class;
		*name_ret = (char*)classhint.res_name;
	} else {
		*class_ret = NULL;
	}
	return status;
}

char *get_window_property_by_atom(struct X11Lib *lib, Display *display, Window window, Atom atom, long *nitems, Atom *type, int *size) {
	Atom actual_type_return;
	int actual_format_return;
	unsigned long nitems_return;
	unsigned long unused_bytes_after;
	unsigned char *prop_return;

	Status status = lib->XGetWindowProperty(display, window, atom, 0, (~0L),
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
	var (
		d   xOrgDetailer
		err error
	)
	d.x11, d.x11Lib, err = openX11Lib()
	if err != nil {
		return noDetails{}, err
	}
	d.xRes, d.xResLib = openXResLib()
	d.xss, d.xssLib = openXssLib()
	if d.xss != nil {
		d.last = time.Now()
	}
	return &d, nil
}

func openX11Lib() (*dl.Lib, *C.struct_X11Lib, error) {
	x11, err := dl.Open(dl.RTLD_LAZY, "libX11.so", "libX11.so.6")
	if err != nil {
		return nil, nil, err
	}
	var x11Lib C.struct_X11Lib

	sym, err := x11.Symbol("XOpenDisplay")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XOpenDisplay = C.xOpenDisplay(sym)

	sym, err = x11.Symbol("XCloseDisplay")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XCloseDisplay = C.xCloseDisplay(sym)

	sym, err = x11.Symbol("XSetErrorHandler")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XSetErrorHandler = C.xSetErrorHandler(sym)

	sym, err = x11.Symbol("XGetInputFocus")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XGetInputFocus = C.xGetInputFocus(sym)

	sym, err = x11.Symbol("XRootWindow")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XRootWindow = C.xRootWindow(sym)

	sym, err = x11.Symbol("XInternAtom")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XInternAtom = C.xInternAtom(sym)

	sym, err = x11.Symbol("XQueryTree")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XQueryTree = C.xQueryTree(sym)

	sym, err = x11.Symbol("XGetClassHint")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XGetClassHint = C.xGetClassHint(sym)

	sym, err = x11.Symbol("XGetWindowProperty")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XGetWindowProperty = C.xGetWindowProperty(sym)

	sym, err = x11.Symbol("XFree")
	if err != nil {
		x11.Close()
		return nil, nil, err
	}
	x11Lib.XFree = C.xFree(sym)

	return x11, &x11Lib, nil
}

func openXssLib() (*dl.Lib, *C.struct_XssLib) {
	xss, err := dl.Open(dl.RTLD_LAZY, "libXss.so", "libXss.so.1")
	if err != nil {
		return nil, nil
	}
	var xssLib C.struct_XssLib

	sym, err := xss.Symbol("XScreenSaverQueryExtension")
	if err != nil {
		xss.Close()
		return nil, nil
	}
	xssLib.XScreenSaverQueryExtension = C.xScreenSaverQueryExtension(sym)

	sym, err = xss.Symbol("XScreenSaverQueryInfo")
	if err != nil {
		xss.Close()
		return nil, nil
	}
	xssLib.XScreenSaverQueryInfo = C.xScreenSaverQueryInfo(sym)

	return xss, &xssLib
}

func openXResLib() (*dl.Lib, *C.struct_XResLib) {
	xRes, err := dl.Open(dl.RTLD_LAZY, "libXRes.so", "libXRes.so.1")
	if err != nil {
		return nil, nil
	}
	var xResLib C.struct_XResLib

	sym, err := xRes.Symbol("XResQueryClientIds")
	if err != nil {
		xRes.Close()
		return nil, nil
	}
	xResLib.XResQueryClientIds = C.xResQueryClientIds(sym)

	sym, err = xRes.Symbol("XResGetClientPid")
	if err != nil {
		xRes.Close()
		return nil, nil
	}
	xResLib.XResGetClientPid = C.xResGetClientPid(sym)

	sym, err = xRes.Symbol("XResClientIdsDestroy")
	if err != nil {
		xRes.Close()
		return nil, nil
	}
	xResLib.XResClientIdsDestroy = C.xResClientIdsDestroy(sym)

	return xRes, &xResLib
}

type xOrgDetailer struct {
	last time.Time

	x11    *dl.Lib
	x11Lib *C.struct_X11Lib

	xRes    *dl.Lib
	xResLib *C.struct_XResLib

	xss    *dl.Lib
	xssLib *C.struct_XssLib
}

func (*xOrgDetailer) strategy() string { return "xorg" }

func (d *xOrgDetailer) Close() error {
	d.x11Lib = nil
	d.xResLib = nil
	d.xssLib = nil
	return errors.Join(d.x11.Close(), d.xRes.Close(), d.xss.Close())
}

func (d *xOrgDetailer) details() (watcher.Details, error) {
	var det C.struct_details
	flags := C.activeWindow(&det, d.x11Lib, d.xResLib, d.xssLib)
	if flags < 0 {
		return watcher.Details{}, xOrgDetailError(-flags)
	}
	if det.idle > 0 {
		d.last = time.Now().Add(time.Duration(det.idle) * -time.Millisecond).Round(time.Second / 10)
	}
	active := watcher.Details{
		WindowID:   int(det.wid),
		ProcessID:  int(det.pid),
		Name:       C.GoString(det.name),
		Class:      C.GoString(det.class),
		WindowName: C.GoString(det.window),
		LastInput:  d.last,
		Locked:     det.saver_state == C.ScreenSaverOn,
	}
	C.freeDetails(d.x11Lib, &det)
	const allWindowDetails = C.fdisplay | C.fclassname | C.fwindowname
	if flags&C.fscreensaver == 0 || (!active.Locked && flags&allWindowDetails != allWindowDetails) {
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
	C.enodisplay: "no display",
}

// warning is a warn-only error.
type warning struct {
	error
}

func missing(flags C.int) string {
	m := make([]string, 0, 3)
	if flags&C.fdisplay == 0 {
		m = append(m, "window id")
	}
	if flags&C.fclassname == 0 {
		m = append(m, "class name")
	}
	if flags&C.fwindowname == 0 {
		m = append(m, "window name")
	}
	if flags&C.fscreensaver == 0 {
		m = append(m, "screensaver info")
	}
	return strings.Join(m, ", ")
}
