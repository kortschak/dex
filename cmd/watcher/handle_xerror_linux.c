//go:build !no_xorg

/*

Copyright 1985, 1986, 1987, 1998  The Open Group

Permission to use, copy, modify, distribute, and sell this software and its
documentation for any purpose is hereby granted without fee, provided that
the above copyright notice appear in all copies and that both that
copyright notice and this permission notice appear in supporting
documentation.

The above copyright notice and this permission notice shall be included
in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS
OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE OPEN GROUP BE LIABLE FOR ANY CLAIM, DAMAGES OR
OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE,
ARISING FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR
OTHER DEALINGS IN THE SOFTWARE.

Except as contained in this notice, the name of The Open Group shall
not be used in advertising or otherwise to promote the sale, use or
other dealings in this Software without prior written authorization
from The Open Group.

*/

#include <X11/Xlibint.h>
#include <stdio.h>
#include <string.h>
#include <dlfcn.h>

typedef int (*xGetErrorText) (
    Display *display,
    int     code,
    char    *buffer_return,
    int     length
);
typedef int (*xGetErrorDatabaseText) (
    Display       *display,
    _Xconst char  *name,
    _Xconst char  *message,
    _Xconst char  *default_string,
    char          *buffer_return,
    int           length
);
struct X11Lib {
	xGetErrorText XGetErrorText;
	xGetErrorDatabaseText XGetErrorDatabaseText;
};

static int _XPrintDefaultError(
	struct X11Lib *lib,
    Display *dpy,
    XErrorEvent *event,
    FILE *fp)
{
    char buffer[BUFSIZ];
    char mesg[BUFSIZ];
    char number[32];
    const char *mtype = "XlibMessage";
    register _XExtension *ext = (_XExtension *)NULL;
    _XExtension *bext = (_XExtension *)NULL;
    lib->XGetErrorText(dpy, event->error_code, buffer, BUFSIZ);
    lib->XGetErrorDatabaseText(dpy, mtype, "XError", "X Error", mesg, BUFSIZ);
    (void) fprintf(fp, "%s:  %s\n  ", mesg, buffer);
    lib->XGetErrorDatabaseText(dpy, mtype, "MajorCode", "Request Major code %d",
	mesg, BUFSIZ);
    (void) fprintf(fp, mesg, event->request_code);
    if (event->request_code < 128) {
	snprintf(number, sizeof(number), "%d", event->request_code);
	lib->XGetErrorDatabaseText(dpy, "XRequest", number, "", buffer, BUFSIZ);
    } else {
	for (ext = dpy->ext_procs;
	     ext && (ext->codes.major_opcode != event->request_code);
	     ext = ext->next)
	  ;
	if (ext) {
	    strncpy(buffer, ext->name, BUFSIZ);
	    buffer[BUFSIZ - 1] = '\0';
        } else
	    buffer[0] = '\0';
    }
    (void) fprintf(fp, " (%s)\n", buffer);
    if (event->request_code >= 128) {
	lib->XGetErrorDatabaseText(dpy, mtype, "MinorCode", "Request Minor code %d",
			      mesg, BUFSIZ);
	fputs("  ", fp);
	(void) fprintf(fp, mesg, event->minor_code);
	if (ext) {
	    snprintf(mesg, sizeof(mesg), "%s.%d", ext->name, event->minor_code);
	    lib->XGetErrorDatabaseText(dpy, "XRequest", mesg, "", buffer, BUFSIZ);
	    (void) fprintf(fp, " (%s)", buffer);
	}
	fputs("\n", fp);
    }
    if (event->error_code >= 128) {
	/* kludge, try to find the extension that caused it */
	buffer[0] = '\0';
	for (ext = dpy->ext_procs; ext; ext = ext->next) {
	    if (ext->error_string)
		(*ext->error_string)(dpy, event->error_code, &ext->codes,
				     buffer, BUFSIZ);
	    if (buffer[0]) {
		bext = ext;
		break;
	    }
	    if (ext->codes.first_error &&
		ext->codes.first_error < (int)event->error_code &&
		(!bext || ext->codes.first_error > bext->codes.first_error))
		bext = ext;
	}
	if (bext)
	    snprintf(buffer, sizeof(buffer), "%s.%d", bext->name,
                     event->error_code - bext->codes.first_error);
	else
	    strcpy(buffer, "Value");
	lib->XGetErrorDatabaseText(dpy, mtype, buffer, "", mesg, BUFSIZ);
	if (mesg[0]) {
	    fputs("  ", fp);
	    (void) fprintf(fp, mesg, event->resourceid);
	    fputs("\n", fp);
	}
	/* let extensions try to print the values */
	for (ext = dpy->ext_procs; ext; ext = ext->next) {
	    if (ext->error_values)
		(*ext->error_values)(dpy, event, fp);
	}
    } else if ((event->error_code == BadWindow) ||
	       (event->error_code == BadPixmap) ||
	       (event->error_code == BadCursor) ||
	       (event->error_code == BadFont) ||
	       (event->error_code == BadDrawable) ||
	       (event->error_code == BadColor) ||
	       (event->error_code == BadGC) ||
	       (event->error_code == BadIDChoice) ||
	       (event->error_code == BadValue) ||
	       (event->error_code == BadAtom)) {
	if (event->error_code == BadValue)
	    lib->XGetErrorDatabaseText(dpy, mtype, "Value", "Value 0x%x",
				  mesg, BUFSIZ);
	else if (event->error_code == BadAtom)
	    lib->XGetErrorDatabaseText(dpy, mtype, "AtomID", "AtomID 0x%x",
				  mesg, BUFSIZ);
	else
	    lib->XGetErrorDatabaseText(dpy, mtype, "ResourceID", "ResourceID 0x%x",
				  mesg, BUFSIZ);
	fputs("  ", fp);
	(void) fprintf(fp, mesg, event->resourceid);
	fputs("\n", fp);
    }
    lib->XGetErrorDatabaseText(dpy, mtype, "ErrorSerial", "Error Serial #%d",
			  mesg, BUFSIZ);
    fputs("  ", fp);
    (void) fprintf(fp, mesg, event->serial);
    lib->XGetErrorDatabaseText(dpy, mtype, "CurrentSerial", "Current Serial #%lld",
			  mesg, BUFSIZ);
    fputs("\n  ", fp);
    (void) fprintf(fp, mesg, (unsigned long long)(X_DPY_GET_REQUEST(dpy)));
    fputs("\n", fp);
    if (event->error_code == BadImplementation) return 0;
    return 1;
}

/*
handleXError is _XDefaultError from libx11/src/XlibInt.c stripped of its
exit(1) call and with dynamic XGetErrorText and XGetErrorDatabaseText added.
*/
int handleXError(Display *dpy, XErrorEvent *event) {
	dlerror();
	void *h = dlopen("libX11.so", RTLD_LAZY);
	if (h == NULL) {
		(void) fprintf(stderr, "failed to open libX11: %s\n", dlerror());
		(void) fprintf(stderr, "X error: resourceid=%ld serial=%ld error_code=%d request_code=%d minor_code=%d\n",
			event->resourceid, event->serial, event->error_code, event->request_code, event->minor_code);
		return 0;
	}
	struct X11Lib lib = {
		.XGetErrorText = dlsym(h, "XGetErrorText"),
		.XGetErrorDatabaseText = dlsym(h, "xGetErrorDatabaseText")
	};
	if (lib.XGetErrorText != NULL && lib.XGetErrorDatabaseText != NULL) {
		_XPrintDefaultError(&lib, dpy, event, stderr);	
	} else {
		if (lib.XGetErrorText == NULL) {
			(void) fprintf(stderr, "failed to get XGetErrorText: %s\n", dlerror());
		}
		if (lib.XGetErrorDatabaseText == NULL) {
			(void) fprintf(stderr, "failed to get XGetErrorDatabaseText: %s\n", dlerror());
		}
		(void) fprintf(stderr, "X error: resourceid=%ld serial=%ld error_code=%d request_code=%d minor_code=%d\n",
			event->resourceid, event->serial, event->error_code, event->request_code, event->minor_code);
	}
	dlclose(h);
	char *e = dlerror();
	if (e != NULL) {
		(void) fprintf(stderr, "failed to close libX11: %s\n", e);
	}
	return 0;
}
