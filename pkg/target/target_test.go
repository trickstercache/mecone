/*
 * Copyright 2026 The Trickster Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package target

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	testName     = "proxy-one"
	testHost     = "127.0.0.1"
	testPort     = 18080
	originHost   = "origin.test"
	originPort   = 9000
	testWorkDir  = "/tmp/work"
	testFilePerm = 0o600
	testBaseURL  = "http://127.0.0.1:18080"
	testPID      = 4242
	imageNginx   = "nginx"
	networkLab   = "lab"
	readyAfter   = 3

	first   = 0
	second  = 1
	fromEnd = 1
	once    = 1
)

func testVars() Vars {
	return Vars{
		Name:       testName,
		ListenHost: testHost,
		ListenPort: testPort,
		OriginHost: originHost,
		OriginPort: originPort,
		WorkDir:    testWorkDir,
		ConfigFile: "/etc/proxy.conf",
	}
}

type fakeRunning struct {
	pid     int
	mu      sync.Mutex
	signals []os.Signal
	exited  chan error
}

func newFakeRunning() *fakeRunning {
	return &fakeRunning{pid: testPID, exited: make(chan error, exitBuffer)}
}

func (f *fakeRunning) PID() int {
	return f.pid
}

func (f *fakeRunning) Signal(sig os.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signals = append(f.signals, sig)
	select {
	case f.exited <- nil:
	default:
	}
	return nil
}

func (f *fakeRunning) Wait() error {
	return <-f.exited
}

func (f *fakeRunning) sent() []os.Signal {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.signals)
}

type fakeExec struct {
	mu      sync.Mutex
	calls   []Command
	out     []byte
	err     error
	running *fakeRunning
	// keepRunning models a stop command that does not actually stop the proxy.
	keepRunning bool
}

// Output models a stop command working: the process it manages exits, as a real one would.
func (f *fakeExec) Output(_ context.Context, c Command) ([]byte, error) {
	f.record(c)
	if f.running != nil && !f.keepRunning {
		select {
		case f.running.exited <- nil:
		default:
		}
	}
	return f.out, f.err
}

func (f *fakeExec) Start(_ context.Context, c Command) (Running, error) {
	f.record(c)
	if f.err != nil {
		return nil, f.err
	}
	if f.running == nil {
		f.running = newFakeRunning()
	}
	return f.running, nil
}

func (f *fakeExec) record(c Command) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
}

func (f *fakeExec) seen() []Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func TestExpand(t *testing.T) {
	v := testVars()
	cases := map[string]string{
		"${NAME}":                testName,
		"${LISTEN_HOST}":         testHost,
		"${LISTEN_PORT}":         "18080",
		"${LISTEN_ADDR}":         "127.0.0.1:18080",
		"${ORIGIN_HOST}":         originHost,
		"${ORIGIN_PORT}":         "9000",
		"${ORIGIN_URL}":          "http://origin.test:9000",
		"${WORKDIR}":             testWorkDir,
		"${CONFIG_FILE}":         "/etc/proxy.conf",
		"${UNKNOWN}":             "${UNKNOWN}",
		"listen ${LISTEN_PORT};": "listen 18080;",
	}
	for in, want := range cases {
		if got := v.Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
	if got := v.ExpandAll([]string{"${NAME}", "x"}); !slices.Equal(got, []string{testName, "x"}) {
		t.Errorf("ExpandAll = %v", got)
	}
	if v.ExpandAll(nil) != nil {
		t.Error("ExpandAll(nil) must stay nil")
	}
}

func TestFreePort(t *testing.T) {
	port, err := FreePort(testHost)
	if err != nil || port <= none {
		t.Fatalf("FreePort = %d, %v", port, err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(testHost, "0"))
	if err != nil {
		t.Fatal(err)
	}
	if err := ln.Close(); err != nil {
		t.Error(err)
	}
	if _, err := FreePort("no.such.host.invalid"); err == nil {
		t.Error("FreePort on an unusable host succeeded")
	}
}

func TestRenderFilesStaged(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "src.conf")
	if err := os.WriteFile(source, []byte("from ${NAME} to ${ORIGIN_URL}"), testFilePerm); err != nil {
		t.Fatal(err)
	}
	v := testVars()
	v.WorkDir = t.TempDir()
	files := []config.File{
		{Source: "src.conf", Target: "/etc/proxy/${NAME}.conf"},
		{Content: "config at ${CONFIG_FILE}", Target: "/etc/proxy/extra.conf"},
	}
	out, got, err := renderFiles(files, v, dir, staged)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigFile != "/etc/proxy/proxy-one.conf" || out[first].target != got.ConfigFile {
		t.Fatalf("config file = %q, targets %+v", got.ConfigFile, out)
	}
	if !strings.HasPrefix(out[first].host, v.WorkDir) {
		t.Errorf("staged file %q is not in the work directory", out[first].host)
	}
	assertFile(t, out[first].host, "from proxy-one to http://origin.test:9000")
	assertFile(t, out[second].host, "config at /etc/proxy/proxy-one.conf")
}

func TestRenderFilesInPlace(t *testing.T) {
	v := testVars()
	v.WorkDir = t.TempDir()
	out, _, err := renderFiles([]config.File{{Content: "port ${LISTEN_PORT}", Target: "etc/proxy.conf"}}, v, t.TempDir(), inPlace)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(v.WorkDir, "etc/proxy.conf")
	if out[first].host != want || out[first].target != want {
		t.Fatalf("rendered %+v, want %q", out[first], want)
	}
	assertFile(t, want, "port 18080")
}

func TestRenderFilesErrors(t *testing.T) {
	v := testVars()
	v.WorkDir = t.TempDir()
	if out, _, err := renderFiles(nil, v, t.TempDir(), staged); out != nil || err != nil {
		t.Errorf("no files = %v, %v", out, err)
	}
	files := []config.File{{Source: "missing.conf", Target: "/etc/x.conf"}}
	if _, _, err := renderFiles(files, v, t.TempDir(), staged); err == nil {
		t.Error("a missing source file was accepted")
	}
	// a directory opens but cannot be read, so the failure surfaces rather than writing empty content
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "conf.d"), dirPerm); err != nil {
		t.Fatal(err)
	}
	unreadable := []config.File{{Source: "conf.d", Target: "/etc/y.conf"}}
	if _, _, err := renderFiles(unreadable, v, dir, staged); err == nil {
		t.Error("a source that cannot be read was accepted")
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("%s holds %q, want %q", path, b, want)
	}
}

func TestStartProxyTarget(t *testing.T) {
	tg := &config.Target{Name: testName, Proxy: &config.Proxy{URL: "https://edge.test/${NAME}/"}}
	inst, err := Start(t.Context(), tg, testVars(), &fakeExec{})
	if err != nil {
		t.Fatal(err)
	}
	if inst.BaseURL != "https://edge.test/proxy-one" {
		t.Errorf("base URL = %q", inst.BaseURL)
	}
	if inst.Logs(t.Context()) != unset {
		t.Error("an unmanaged target has no logs")
	}
	if err := inst.Stop(t.Context()); err != nil {
		t.Errorf("stopping an unmanaged target: %v", err)
	}
}

func TestWaitReady(t *testing.T) {
	var calls int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls < readyAfter {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	probe := (&config.Target{Readiness: &config.Readiness{
		Timeout:  testdef.Duration(5 * time.Second),
		Interval: testdef.Duration(time.Millisecond),
	}}).Probe()
	if err := WaitReady(t.Context(), ts.URL, probe, testVars()); err != nil {
		t.Fatalf("WaitReady: %v", err)
	}
	if calls < readyAfter {
		t.Errorf("probed %d times", calls)
	}
}

func TestWaitReadyFailures(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer ts.Close()
	probe := config.Readiness{
		Path:     "/",
		Status:   []int{http.StatusOK},
		Timeout:  testdef.Duration(20 * time.Millisecond),
		Interval: testdef.Duration(time.Millisecond),
	}
	err := WaitReady(t.Context(), ts.URL, probe, testVars())
	if err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Errorf("WaitReady = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WaitReady(ctx, ts.URL, probe, testVars()); err == nil {
		t.Error("WaitReady ignored a canceled context")
	}
	if err := WaitReady(t.Context(), "http://127.0.0.1:1", probe, testVars()); err == nil {
		t.Error("WaitReady succeeded against a closed port")
	}
}
