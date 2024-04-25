// Copyright ©2024 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

import Gio from 'gi://Gio';
import Meta from 'gi://Meta';

const interfaceXML = `
<node>
   <interface name="org.gnome.Shell.Extensions.UserActivity">
      <method name="Details">
         <arg type="s" direction="out" name="detail"/>
      </method>
   </interface>
</node>`;

export default class Extension {
  enable() {
    this._dbus = Gio.DBusExportedObject.wrapJSObject(interfaceXML, this);
    this._dbus.export(Gio.DBus.session, '/org/gnome/Shell/Extensions/UserActivity');
  }

  disable() {
    this._dbus.flush();
    this._dbus.unexport();
    delete this._dbus;
  }

  // Details returns the window details for the currently focused window
  // and the last input time.
  Details() {
    const idle = global.backend.get_core_idle_monitor().get_idletime();
    const last = idle < 0 ? '' : new Date(new Date().getTime()-idle).toISOString();

    const det = global.get_window_actors()
    .filter(w => w.meta_window[`has_focus`]?.())
    .map(w => {
      // https://developer-old.gnome.org/meta/stable/MetaWindow.html
      function get(name) {
        return w.meta_window[`get_${name}`]?.();
      }
      return {
        'wid':        get('id'),
        'pid':        get('pid'),
        'name':       get('wm_class_instance'),
        'class':      get('wm_class'),
        'window':     get('title'),
        'last_input': last,

        // Unused by watcher, but kept for debugging.
        'type':       get('type'), // https://developer-old.gnome.org/meta/stable/MetaWindow.html#MetaWindowType
        'layer':      get('layer') // https://developer-old.gnome.org/meta/stable/meta-Common.html#MetaStackLayer
      };
    });
    if (det.length == 0) {
      // No window was filtered, so just return the last input time.
      return JSON.stringify({'last_input': last});
    }
    // det should have length one by now since has_focus for MetaWindow is
    // defined as true iff? the window == window->display->focus_window.
    // https://github.com/GNOME/metacity/blob/65d7e5b3eeb2810d3a27fb5405706eb39b8281ae/src/core/window-private.h#L282-L283
    return JSON.stringify(det[0]);
  }
}
