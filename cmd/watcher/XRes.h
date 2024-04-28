/*
   Copyright (c) 2002  XFree86 Inc
Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is fur-
nished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FIT-
NESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.  IN NO EVENT SHALL THE
XFREE86 PROJECT BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER
IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CON-
NECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

Except as contained in this notice, the name of the XFree86 Project shall not
be used in advertising or otherwise to promote the sale, use or other deal-
ings in this Software without prior written authorization from the XFree86
Project.
*/

#ifndef _XRES_H
#define _XRES_H

#include <X11/Xfuncproto.h>

/* v1.0 */

typedef struct {
  XID resource_base;
  XID resource_mask;
} XResClient;

typedef struct {
  Atom resource_type;
  unsigned int count;
} XResType;

/* v1.2 */

typedef enum {
  XRES_CLIENT_ID_XID,
  XRES_CLIENT_ID_PID,
  XRES_CLIENT_ID_NR
} XResClientIdType;

typedef enum {
  XRES_CLIENT_ID_XID_MASK = 1 << XRES_CLIENT_ID_XID,
  XRES_CLIENT_ID_PID_MASK = 1 << XRES_CLIENT_ID_PID
} XResClientIdMask;

typedef struct {
  XID           client;
  unsigned int  mask;
} XResClientIdSpec;

typedef struct {
  XResClientIdSpec spec;
  long          length;
  void         *value;
} XResClientIdValue;

typedef struct {
  XID           resource;
  Atom          type;
} XResResourceIdSpec;

typedef struct {
  XResResourceIdSpec spec;
  long          bytes;
  long          ref_count;
  long          use_count;
} XResResourceSizeSpec;

typedef struct {
  XResResourceSizeSpec  size;
  long                  num_cross_references;
  XResResourceSizeSpec *cross_references;
} XResResourceSizeValue;

_XFUNCPROTOBEGIN

/* v1.0 */

Bool XResQueryExtension (
   Display *dpy,
   int *event_base_return,
   int *error_base_return
);

Status XResQueryVersion (
   Display *dpy,
   int *major_version_return,
   int *minor_version_return
);

Status XResQueryClients (
   Display *dpy,
   int *num_clients,
   XResClient **clients
);

Status XResQueryClientResources (
   Display *dpy,
   XID xid,
   int *num_types,
   XResType **types
);

Status XResQueryClientPixmapBytes (
   Display *dpy,
   XID xid,
   unsigned long *bytes
);

/* v1.2 */

Status XResQueryClientIds (
   Display            *dpy,
   long                num_specs,
   XResClientIdSpec   *client_specs,   /* in */
   long               *num_ids,        /* out */
   XResClientIdValue **client_ids      /* out */
);

XResClientIdType XResGetClientIdType(XResClientIdValue* value);

/* return -1 if no pid associated to the value */
pid_t XResGetClientPid(XResClientIdValue* value);

void XResClientIdsDestroy (
   long                num_ids,
   XResClientIdValue  *client_ids
);

Status XResQueryResourceBytes (
   Display            *dpy,
   XID                 client,
   long                num_specs,
   XResResourceIdSpec *resource_specs, /* in */
   long               *num_sizes,      /* out */
   XResResourceSizeValue **sizes       /* out */
);

void XResResourceSizeValuesDestroy (
   long                num_sizes,
   XResResourceSizeValue *sizes
);

_XFUNCPROTOEND

#endif /* _XRES_H */
