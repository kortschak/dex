// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"os"

	"gioui.org/app"
)

func main() {
	title := flag.String("title", "", "set window title")
	flag.Parse()

	go func() {
		var w app.Window
		w.Option(app.Title(*title), app.Size(200, 100))
		for {
			switch e := w.Event().(type) {
			case app.DestroyEvent:
				if e.Err != nil {
					log.Fatal(e.Err)
				}
				os.Exit(0)
			}
		}
	}()
	app.Main()
}
