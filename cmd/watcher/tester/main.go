// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"log"
	"os"

	"gioui.org/app"
	"gioui.org/io/system"
)

func main() {
	title := flag.String("title", "", "set window title")
	flag.Parse()

	go func() {
		w := app.NewWindow(app.Title(*title), app.Size(200, 100))
		for {
			e := <-w.Events()
			switch e := e.(type) {
			case system.DestroyEvent:
				if e.Err != nil {
					log.Fatal(e.Err)
				}
				os.Exit(0)
			}
		}
	}()
	app.Main()
}
