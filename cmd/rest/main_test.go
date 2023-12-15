// Copyright ©2023 Dan Kortschak. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/kortschak/jsonrpc2"
	"golang.org/x/tools/godoc/vfs"
	"golang.org/x/tools/godoc/vfs/mapfs"
	"golang.org/x/tools/txtar"

	rest "github.com/kortschak/dex/cmd/rest/api"
	"github.com/kortschak/dex/internal/locked"
	"github.com/kortschak/dex/internal/slogext"
	"github.com/kortschak/dex/internal/state"
	"github.com/kortschak/dex/rpc"
)

const workDir = "testdata"

var (
	verbose = flag.Bool("verbose_log", false, "print full logging")
	lines   = flag.Bool("show_lines", false, "log source code position")
	keep    = flag.Bool("keep", false, "keep workdir after tests")
)

func Test(t *testing.T) {
	err := os.Mkdir(workDir, 0o755)
	if err != nil && !errors.Is(err, fs.ErrExist) {
		t.Fatalf("failed to make dir: %v", err)
	}
	if !*keep {
		t.Cleanup(func() {
			os.RemoveAll(workDir)
		})
	}

	goCmd, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("failed to find go command: %v", err)
	}

	// Make certificates and CA.
	certs, err := testCertificates("state_server", "state_client")
	if err != nil {
		t.Fatalf("unexpected error creating certificates: %v", err)
	}
	t.Logf("certificates:\n%s", txtar.Format(certs))
	certFS := txtarFS(certs)

	// Make common CA.
	caCert, err := fs.ReadFile(certFS, "ca_crt.pem")
	if err != nil {
		t.Fatalf("unexpected error reading CA cert: %v", err)
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)

	// Get PEM formatted data for configs.
	srvCert, err := fs.ReadFile(certFS, "state_server_crt.pem")
	if err != nil {
		t.Fatalf("unexpected error reading state_server cert: %v", err)
	}
	srvKey, err := fs.ReadFile(certFS, "state_server_key.pem")
	if err != nil {
		t.Fatalf("unexpected error reading state_server key: %v", err)
	}
	cliCert, err := fs.ReadFile(certFS, "state_client_crt.pem")
	if err != nil {
		t.Fatalf("unexpected error reading state_client cert: %v", err)
	}
	cliKey, err := fs.ReadFile(certFS, "state_client_key.pem")
	if err != nil {
		t.Fatalf("unexpected error reading state_client key: %v", err)
	}

	for _, network := range []string{"unix", "tcp"} {
		t.Run(network, func(t *testing.T) {
			var (
				level      slog.LevelVar
				kernLogBuf locked.BytesBuffer
				restLogBuf locked.BytesBuffer
			)
			log := slog.New(slogext.NewJSONHandler(&kernLogBuf, &slogext.HandlerOptions{
				Level:     slog.LevelDebug,
				AddSource: slogext.NewAtomicBool(*lines),
			}))

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			kernel, err := rpc.NewKernel(ctx, network, jsonrpc2.NetListenOptions{}, log)
			if err != nil {
				t.Fatalf("failed to start kernel: %v", err)
			}
			// Catch failures to terminate.
			closed := make(chan struct{})
			go func() {
				select {
				case <-ctx.Done():
					t.Error("failed to close server")
					*verbose = true
					t.Logf("kernel log:\n%s\n", &kernLogBuf)
					t.Logf("rest log:\n%s", &restLogBuf)
				case <-closed:
				}
			}()
			defer func() {
				err = kernel.Close()
				if err != nil {
					t.Errorf("failed to close kernel: %v", err)
				}
				close(closed)

				if *verbose {
					t.Logf("kernel log:\n%s\n", &kernLogBuf)
					t.Logf("rest log:\n%s", &restLogBuf)
				}
			}()

			level.Set(slog.LevelDebug)
			uid := rpc.UID{Module: "rest"}
			err = kernel.Spawn(ctx, os.Stdout, &restLogBuf, uid.Module,
				"go", "run", "-race", ".", "-log", level.Level().String(), fmt.Sprintf("-lines=%t", *lines),
			)
			if err != nil {
				t.Fatalf("failed to spawn rest: %v", err)
			}

			conn, _, ok := kernel.Conn(ctx, uid.Module)
			if !ok {
				t.Fatal("failed to get daemon conn")
			}

			storeUID := rpc.UID{Module: "kernel", Service: "store"}
			store, err := state.Open(filepath.Join(workDir, network+"-test.db"), log)
			if err != nil {
				t.Fatalf("failed to open data store: %v\n", err)
			}
			defer store.Close()

			var gotChanges int
			kernel.Funcs(rpc.Funcs{
				"change": func(ctx context.Context, id jsonrpc2.ID, m json.RawMessage) (*rpc.Message[any], error) {
					var v rpc.Message[map[string]any]
					err := rpc.UnmarshalMessage(m, &v)
					if err != nil {
						return nil, err
					}
					gotChanges++
					return nil, nil
				},

				// State store methods from internal/state/funcs.go.
				"set": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
					var m rpc.Message[state.SetMessage]
					err := rpc.UnmarshalMessage(params, &m)
					if err != nil {
						log.LogAttrs(ctx, slog.LevelError, "set", slog.Any("error", err))
						return nil, err
					}
					err = store.Set(m.UID, m.Body.Item, m.Body.Value)
					return nil, err
				},
				"get": func(ctx context.Context, id jsonrpc2.ID, params json.RawMessage) (*rpc.Message[any], error) {
					var m rpc.Message[state.GetMessage]
					err := rpc.UnmarshalMessage(params, &m)
					if err != nil {
						log.LogAttrs(ctx, slog.LevelError, "get", slog.Any("error", err))
						return nil, err
					}
					val, err := store.Get(m.UID, m.Body.Item)
					if err != nil {
						return nil, err
					}
					return rpc.NewMessage[any](storeUID, state.GetResult{Value: val}), nil
				},
			})

			t.Run("configure", func(t *testing.T) {
				beat := &rpc.Duration{Duration: 1 * time.Second}

				var resp rpc.Message[string]

				type moduleOptions struct {
					Heartbeat *rpc.Duration          `json:"heartbeat,omitempty"`
					Servers   map[string]rest.Server `json:"server,omitempty"`
				}
				err := conn.Call(ctx, "configure", rpc.NewMessage(uid, rest.Config{Options: moduleOptions{
					Heartbeat: beat,
					Servers: map[string]rest.Server{
						"state": {
							Addr:         ":7474",
							Request:      `{"method": "state"}`,
							Response:     `response.body`,
							CertPEMBlock: ptr(string(srvCert)),
							KeyPEMBlock:  ptr(string(srvKey)),
							RootCA:       ptr(string(caCert)), // Require mTLS.
						},
						"change": {
							Addr:    ":7575",
							Request: `{"method": "change"}`,
						},
						"store": {
							Addr: ":7676",
							Request: `{
								"method": "set",
								"params": {
									"item": "key",
									"value": b"module-level value"
								},
								"from": {
									"module":"rest","service":"store"
								}
							}`,
						},
					},
				}})).Await(ctx, &resp)
				if err != nil {
					t.Errorf("failed configure call: %v", err)
				}
				if resp.Body != "done" {
					t.Errorf("unexpected response body: got:%s want:done", resp.Body)
				}

				type serviceOptions struct {
					Server rest.Server `json:"server,omitempty"`
				}
				err = conn.Call(ctx, "configure", rpc.NewMessage(uid, rest.Service{
					Name:   "store",
					Active: ptr(true),
					Options: serviceOptions{
						Server: rest.Server{
							Addr: ":7777",
							Request: `has(request.URL.Path) && request.URL.Path == "/set" ?
								debug("set", {
									"method": "set",
									"params": {"item": "key", "value": b"service-level value"}
								})
							: has(request.URL.Path) && request.URL.Path == "/get" ?
								debug("get", {
									"method": "get",
									"params": {"item": "key"}
								})
							: {"debug": debug("request", request)}
							`,
							Response: `response.body`,
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

			time.Sleep(5 * time.Second) // Let some updates and heartbeats past.

			// Perform REST queries on the two end points.

			t.Run("state", func(t *testing.T) {
				cert, err := tls.X509KeyPair(cliCert, cliKey)
				if err != nil {
					t.Fatalf("unexpected error making client certificate: %v", err)
				}
				cli := &http.Client{
					Transport: &http.Transport{
						TLSClientConfig: &tls.Config{
							RootCAs:      caCertPool,
							Certificates: []tls.Certificate{cert},
						},
					},
				}
				resp, err := cli.Get("https://localhost:7474/")
				if err != nil {
					t.Fatalf("unexpected error from GET https://localhost:7474/: %v", err)
				}
				var buf bytes.Buffer
				io.Copy(&buf, resp.Body)
				resp.Body.Close()
				var got rpc.SysState
				err = json.Unmarshal(buf.Bytes(), &got)
				if err != nil {
					t.Fatalf("unexpected error from unmarshal state: %v\n%s", err, buf.Bytes())
				}
				addr := kernel.Addr().String()
				want := rpc.SysState{
					Network: network,
					Addr:    addr,
					Daemons: map[string]rpc.DaemonState{
						"rest": {
							UID:           "rest",
							Command:       ptr(goCmd + " run -race . -log " + level.Level().String() + " -lines=false -uid rest -network " + network + " -addr " + addr),
							LastHeartbeat: &time.Time{},
							Deadline:      &time.Time{},
						},
					},
					Funcs: []string{
						"change",
						"get",
						"set",
					},
				}
				if network == "unix" {
					want.Sock = ptr(filepath.Dir(addr))
				}
				timePresent := cmp.Comparer(func(a, b *time.Time) bool {
					return (a == nil) == (b == nil)
				})
				if !cmp.Equal(want, got, timePresent) {
					t.Errorf("unexpected state result:\n--- want:\n+++ got:\n%s", cmp.Diff(want, got, timePresent))
				}
			})

			t.Run("changes", func(t *testing.T) {
				const changes = 5
				for i := 0; i < changes; i++ {
					resp, err := http.Get("http://localhost:7575/")
					if err != nil {
						t.Fatalf("unexpected error from GET http://localhost:7575/: %v", err)
					}
					io.Copy(io.Discard, resp.Body)
					resp.Body.Close()
				}
				if gotChanges != changes {
					t.Errorf("unexpected number of changes: got:%d want:%d", gotChanges, changes)
				}
			})

			t.Run("store", func(t *testing.T) {
				tests := []struct {
					url  string
					want string
				}{
					{url: "http://localhost:7676/"},
					{url: "http://localhost:7777/"},
					{
						url: "http://localhost:7777/get",
						want: fmt.Sprintf(`{"value":%q}`,
							base64.StdEncoding.EncodeToString([]byte(
								"module-level value",
							))),
					},
					{url: "http://localhost:7777/set", want: "ok"},
					{
						url: "http://localhost:7777/get",
						want: fmt.Sprintf(`{"value":%q}`,
							base64.StdEncoding.EncodeToString([]byte(
								"service-level value",
							))),
					},
				}
				for _, test := range tests {
					resp, err := http.Get(test.url)
					if err != nil {
						t.Fatalf("unexpected error from GET %s: %v", test.url, err)
					}
					var got bytes.Buffer
					io.Copy(&got, resp.Body)
					resp.Body.Close()
					if got.String() != test.want {
						t.Errorf("unexpected result for %s: got:%s want:%s", test.url, &got, test.want)
					}
				}
			})

			t.Run("stop", func(t *testing.T) {
				err := conn.Notify(ctx, "stop", rpc.NewMessage(uid, rpc.None{}))
				if err != nil {
					t.Errorf("failed stop call: %v", err)
				}
			})

			time.Sleep(time.Second) // Let kernel complete final logging.
		})
	}
}

func ptr[T any](v T) *T {
	return &v
}

func testCertificates(names ...string) (*txtar.Archive, error) {
	const keyBits = 1024

	now := time.Now()

	var ar txtar.Archive

	caCert := &x509.Certificate{
		SerialNumber: big.NewInt(9001),
		Subject: pkix.Name{
			CommonName: "authority",
		},
		NotBefore:             now,
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caKey, err := rsa.GenerateKey(rand.Reader, keyBits)
	if err != nil {
		return nil, err
	}
	caBytes, err := x509.CreateCertificate(rand.Reader, caCert, caCert, &caKey.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	ar.Files = append(ar.Files,
		txtar.File{Name: "ca_crt.pem", Data: pemBytes("CERTIFICATE", caBytes)},
		txtar.File{Name: "ca_key.pem", Data: pemBytes("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(caKey))},
	)

	for i, name := range names {
		cert := &x509.Certificate{
			SerialNumber: big.NewInt(int64(i + 1)),
			Subject: pkix.Name{
				CommonName: name,
			},
			DNSNames:    []string{"localhost"},
			NotBefore:   now,
			NotAfter:    now.Add(time.Hour),
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
			KeyUsage:    x509.KeyUsageDigitalSignature,
		}
		certKey, err := rsa.GenerateKey(rand.Reader, keyBits)
		if err != nil {
			return nil, err
		}
		certBytes, err := x509.CreateCertificate(rand.Reader, cert, caCert, &certKey.PublicKey, caKey)
		if err != nil {
			return nil, err
		}
		ar.Files = append(ar.Files,
			txtar.File{Name: name + "_crt.pem", Data: pemBytes("CERTIFICATE", certBytes)},
			txtar.File{Name: name + "_key.pem", Data: pemBytes("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(certKey))},
		)
	}

	return &ar, nil
}

func pemBytes(typ string, data []byte) []byte {
	var buf bytes.Buffer
	pem.Encode(&buf, &pem.Block{Type: typ, Bytes: data})
	return buf.Bytes()
}

// Placeholder until https://go.dev/issue/44158 is implemented.
func txtarFS(ar *txtar.Archive) fs.FS {
	m := make(map[string]string, len(ar.Files))
	for _, f := range ar.Files {
		m[f.Name] = string(f.Data)
	}
	return fsShim{mapfs.New(m)}
}

type fsShim struct {
	vfs.FileSystem
}

func (fs fsShim) Open(name string) (fs.File, error) {
	f, err := fs.FileSystem.Open(name)
	if err != nil {
		return nil, err
	}
	fi, err := fs.Stat(name)
	if err != nil {
		return nil, err
	}
	return fileShim{ReadCloser: f, fi: fi}, nil
}

type fileShim struct {
	io.ReadCloser
	fi fs.FileInfo
}

func (f fileShim) Stat() (fs.FileInfo, error) {
	return f.fi, nil
}
