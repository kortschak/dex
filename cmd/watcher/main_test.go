// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/kortschak/jsonrpc2"

	watcher "github.com/kortschak/dex/cmd/watcher/api"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/rpc"
)

var (
	verbose  = flag.Bool("verbose_log", false, "print full logging")
	lines    = flag.Bool("show_lines", false, "log source code position")
	strategy = flag.String("strategy", "", "details strategy")
	timezone tern
)

func init() {
	flag.Var(&timezone, "dynamic_timezone", "use dynamic timezone strategy")
}

type tern struct {
	val *bool
}

func (t *tern) String() string {
	if t.val == nil {
		return "unset"
	}
	return strconv.FormatBool(*t.val)
}

func (t *tern) Set(f string) error {
	b, err := strconv.ParseBool(f)
	if err != nil {
		return err
	}
	t.val = &b
	return nil
}

func (t *tern) IsBoolFlag() bool { return true }

func TestDaemon(t *testing.T) {
	const changes = 5

	tmp := t.TempDir()
	exePath := filepath.Join(tmp, "watcher")
	out, err := exec.Command("go", "build", "-o", exePath, "-race").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build daemon: %v\n%s", err, out)
	}

	cold := true
	for _, useDBus := range []bool{false, true} {
		if useDBus && runtime.GOOS == "darwin" {
			continue
		}
		if !useDBus && os.Getenv("XDG_SESSION_TYPE") == "wayland" {
			continue
		}

		t.Run(fmt.Sprintf("dbus=%t", useDBus), func(t *testing.T) {

			var (
				testerPath, busAddr string
				resetDetails        func()
			)
			switch useDBus {
			case false:
				testerPath = filepath.Join(tmp, "tester")
				build := exec.Command("go", "build", "-o", testerPath)
				build.Dir = "./tester"
				out, err = build.CombinedOutput()
				if err != nil {
					t.Fatalf("failed to build tester: %v\n%s", err, out)
				}

			case true:
				now := time.Now().Truncate(time.Second)
				responses := make([]string, changes)
				for i := range responses {
					b, err := json.Marshal(watcher.Details{
						WindowID:   i + 1,
						ProcessID:  100 + i,
						Name:       "tester",
						Class:      "Tester",
						WindowName: fmt.Sprintf("tester:%d", i),
						LastInput:  now.Add(time.Duration(i) * time.Second),
					})
					if err != nil {
						t.Fatalf("failed to marshal response %d: %v", i, err)
					}
					responses[i] = string(b)
				}

				busAddr, resetDetails = startFakeDBus(t, responses)
				t.Setenv("DBUS_SESSION_BUS_ADDRESS", busAddr)
			}

			for _, network := range []string{"unix", "tcp"} {
				t.Run(network, func(t *testing.T) {
					if useDBus {
						resetDetails()
					}

					verbose := slogext.NewAtomicBool(*verbose)
					var (
						level slog.LevelVar
						buf   bytes.Buffer
					)
					h := slogext.NewJSONHandler(&buf, &slogext.HandlerOptions{
						Level:     &level,
						AddSource: slogext.NewAtomicBool(*lines),
					})
					g := slogext.NewPrefixHandlerGroup(&buf, h)
					level.Set(slog.LevelDebug)
					log := slog.New(g.NewHandler("🔷 "))

					ctx, cancel := context.WithTimeoutCause(context.Background(), 20*time.Second, errors.New("test waited too long"))
					defer cancel()

					kernel, err := rpc.NewKernel(ctx, network, jsonrpc2.NetListenOptions{}, log)
					if err != nil {
						t.Fatalf("failed to start kernel: %v", err)
					}
					// Catch failures to terminate.
					closed := make(chan struct{})
					var wg sync.WaitGroup
					wg.Add(1)
					go func() {
						defer wg.Done()
						select {
						case <-ctx.Done():
							t.Error("failed to close server")
							verbose.Store(true)
						case <-closed:
						}
					}()
					defer func() {
						err = kernel.Close()
						if err != nil {
							t.Errorf("failed to close kernel: %v", err)
						}
						close(closed)
						wg.Wait()
						if verbose.Load() {
							t.Logf("log:\n%s\n", &buf)
						}
					}()

					uid := rpc.UID{Module: "watcher"}
					err = kernel.Spawn(ctx, os.Stdout, g.NewHandler("🔶 "), nil, uid.Module,
						exePath, "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
					)
					if err != nil {
						t.Fatalf("failed to spawn watcher: %v", err)
					}

					conn, _, ok := kernel.Conn(ctx, uid.Module)
					if !ok {
						t.Fatalf("failed to get daemon conn: %v: %v", ctx.Err(), context.Cause(ctx))
					}

					const wantChanges = changes
					var (
						gotChanges  int
						doneChanges = make(chan struct{})
					)
					kernel.Funcs(rpc.Funcs{
						"change": func(ctx context.Context, id jsonrpc2.ID, m jsontext.Value) (*rpc.Message[any], error) {
							var v rpc.Message[map[string]any]
							err := rpc.UnmarshalMessage(m, &v)
							if err != nil {
								return nil, err
							}
							log.LogAttrs(ctx, slog.LevelInfo, "change", slog.Any("details", v.Body))
							gotChanges++
							if gotChanges == wantChanges {
								close(doneChanges)
							}
							return nil, nil
						},
						// store is a simulation. In practice this would use an [rpc.Forward]
						// call to an activity store.
						"store": func(ctx context.Context, id jsonrpc2.ID, m jsontext.Value) (*rpc.Message[any], error) {
							var v rpc.Message[map[string]any]
							err := rpc.UnmarshalMessage(m, &v)
							if err != nil {
								return nil, err
							}
							log.LogAttrs(ctx, slog.LevelInfo, "store", slog.Any("details", v.Body))
							return nil, nil
						},
					})

					t.Run("configure", func(t *testing.T) {
						period := &rpc.Duration{Duration: time.Second / 2}
						beat := &rpc.Duration{Duration: 5 * time.Second}

						var resp rpc.Message[string]

						type options struct {
							DynamicLocation *bool             `json:"dynamic_location,omitempty"`
							Strategy        string            `json:"strategy,omitempty"`
							Polling         *rpc.Duration     `json:"polling,omitempty"`
							Heartbeat       *rpc.Duration     `json:"heartbeat,omitempty"`
							Rules           map[string]string `json:"rules,omitempty"`
						}
						err := conn.Call(ctx, "configure", rpc.NewMessage(uid, watcher.Config{
							Options: options{
								DynamicLocation: timezone.val,
								Strategy:        effectiveStrategy(*strategy, useDBus),
								Polling:         period,
								Heartbeat:       beat,
								Rules: map[string]string{
									"change": `
								name.contains('tester') && (name != last.name || wid != last.wid || window != last.window) ?
									[{"method":"change","params":{"page":"tester","details":{"name":name,"window":window}}}]
								:
									[{}]`,
									"activity": `
								!locked && last_input != last.last_input ?
									{"method":"store","params":{"name":name,"window":window,"last_input":last_input}}
								:
									{}`,
								},
							},
						})).Await(ctx, &resp)
						if err != nil {
							t.Errorf("failed configure call: %v", err)
						}
						if resp.Body != "done" {
							t.Errorf("unexpected response body: got:%s want:done", resp.Body)
						}
					})

					if !useDBus {
						// MacOS, most (but not all) of the time fails to notice the first
						// run of the tester. So run it once to get started.
						if cold && runtime.GOOS == "darwin" {
							cmd := exec.Command(testerPath, "-title", "tester:cold")
							err := cmd.Start()
							if err != nil {
								t.Error("failed to start tester cold")
							}
							time.Sleep(time.Second)
							cmd.Process.Kill()
						}
						cold = false
						for i := range changes {
							if runtime.GOOS == "darwin" {
								// On MacOS, the applescript used to get the PID returns
								// the same PID for all the testers resulting in the
								// application and window names being identical, so wait
								// for at least a polling period to allow a different
								// window to be observed. This is obviously horrible.
								time.Sleep(time.Second)
							}
							cmd := exec.Command(testerPath, "-title", fmt.Sprintf("tester:%d", i))
							err := cmd.Start()
							if err != nil {
								t.Errorf("failed to start tester %d", i)
							}
							time.Sleep(time.Second)
							cmd.Process.Kill()
						}
					}

					time.Sleep(5 * time.Second) // Let some updates and heartbeats past.

					t.Run("stop", func(t *testing.T) {
						err := conn.Notify(ctx, "stop", rpc.NewMessage(uid, rpc.None{}))
						if err != nil {
							t.Errorf("failed stop call: %v", err)
						}
					})

					select {
					case <-time.After(time.Second):
					case <-doneChanges:
					}
					if gotChanges < wantChanges {
						t.Errorf("failed to get %d changes: got:%d", wantChanges, gotChanges)
					}

					time.Sleep(time.Second) // Let kernel complete final logging.
				})
			}
		})
	}
}

func effectiveStrategy(strategy string, dbus bool) string {
	if runtime.GOOS == "darwin" {
		return ""
	}
	if dbus {
		return "gnome/mutter"
	}
	return "xorg"
}

func startFakeDBus(t *testing.T, responses []string) (addr string, resetDetails func()) {
	t.Helper()

	_, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("dbus-daemon not available")
	}

	cmd := exec.Command("dbus-daemon", "--session", "--print-address", "--nofork")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout pipe: %v", err)
	}
	err = cmd.Start()
	if err != nil {
		t.Fatalf("failed to start dbus-daemon: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})

	sc := bufio.NewScanner(pipe)
	if !sc.Scan() {
		t.Fatalf("dbus-daemon did not print address: %v", sc.Err())
	}
	busAddr := sc.Text()

	conn, err := dbus.Connect(busAddr)
	if err != nil {
		t.Fatalf("failed to connect to private bus: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	err = dbusExport(conn,
		"org.gnome.ScreenSaver", dbus.NameFlagDoNotQueue,
		"/org/gnome/ScreenSaver", "org.gnome.ScreenSaver",
		fakeScreenSaver{},
	)
	if err != nil {
		t.Fatal(err)
	}

	ua := &fakeUserActivity{responses: responses}
	err = dbusExport(conn,
		"org.gnome.Shell", dbus.NameFlagDoNotQueue,
		"/org/gnome/Shell/Extensions/UserActivity", "org.gnome.Shell.Extensions.UserActivity",
		ua,
	)
	if err != nil {
		t.Fatal(err)
	}

	err = dbusExport(conn,
		"org.gnome.Mutter.IdleMonitor", dbus.NameFlagDoNotQueue,
		"/org/gnome/Mutter/IdleMonitor/Core", "org.gnome.Mutter.IdleMonitor",
		fakeIdleMonitor{},
	)
	if err != nil {
		t.Fatal(err)
	}

	return busAddr, func() { ua.n.Store(0) }
}

func dbusExport(conn *dbus.Conn, name string, flags dbus.RequestNameFlags, path dbus.ObjectPath, iface string, val any) error {
	reply, err := conn.RequestName(name, flags)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("failed to own %s: %w (reply=%d)", name, err, reply)
	}
	err = conn.Export(val, path, iface)
	if err != nil {
		return fmt.Errorf("failed to export %s: %w", iface[strings.LastIndex(iface, ".")+1:], err)
	}
	return nil
}

type fakeScreenSaver struct{}

func (fakeScreenSaver) GetActive() (bool, *dbus.Error) {
	return false, nil
}

type fakeUserActivity struct {
	n         atomic.Int64
	responses []string
}

func (f *fakeUserActivity) Details() (string, *dbus.Error) {
	i := int(f.n.Add(1)) - 1
	if i >= len(f.responses) {
		i = len(f.responses) - 1
	}
	return f.responses[i], nil
}

type fakeIdleMonitor struct{}

func (fakeIdleMonitor) GetIdletime() (int64, *dbus.Error) {
	return 0, nil
}
