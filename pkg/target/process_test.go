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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
)

const (
	proxyConf  = "proxy.conf"
	proxyCmd   = "proxy"
	shortGrace = 10 * time.Millisecond
)

func processTarget(p *config.Process, baseDir string) *config.Target {
	return &config.Target{Name: testName, Process: p, BaseDir: baseDir}
}

func TestStartProcessForeground(t *testing.T) {
	x := &fakeExec{running: newFakeRunning()}
	tg := processTarget(&config.Process{
		Start: "proxy --port ${LISTEN_PORT} --config ${CONFIG_FILE}",
		Files: []config.File{{Content: "listen ${LISTEN_PORT}", Target: proxyConf}},
	}, t.TempDir())
	inst, err := Start(t.Context(), tg, testVars(), x)
	if err != nil {
		t.Fatal(err)
	}
	if inst.BaseURL != testBaseURL {
		t.Errorf("base URL = %q", inst.BaseURL)
	}
	dir := assertStartCommand(t, x)
	if err := inst.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sent := x.running.sent(); len(sent) != once || sent[first] != syscall.SIGTERM {
		t.Errorf("signals = %v", sent)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("work directory survived the stop: %v", err)
	}
}

func assertStartCommand(t *testing.T, x *fakeExec) string {
	t.Helper()
	start := x.seen()[first]
	if start.Path != shellBin || !strings.Contains(start.Args[second], "--port 18080") {
		t.Fatalf("start command = %+v", start)
	}
	if !strings.HasSuffix(start.Args[second], filepath.Join(start.Dir, proxyConf)) {
		t.Errorf("config path not expanded: %q", start.Args[second])
	}
	assertFile(t, filepath.Join(start.Dir, proxyConf), "listen 18080")
	return start.Dir
}

func TestStartProcessStopCommand(t *testing.T) {
	x := &fakeExec{running: newFakeRunning()}
	tg := processTarget(&config.Process{Start: proxyCmd, Stop: "kill ${PID}"}, t.TempDir())
	inst, err := Start(t.Context(), tg, testVars(), x)
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	stop := x.seen()[len(x.seen())-fromEnd]
	if stop.Args[second] != "kill 4242" {
		t.Errorf("stop command = %q", stop.Args[second])
	}
	if sent := x.running.sent(); len(sent) != none {
		t.Errorf("a stop command must replace signals, got %v", sent)
	}
}

func TestStopKillsAProcessThatIgnoresTheStopCommand(t *testing.T) {
	restore := stopGrace
	stopGrace = shortGrace
	t.Cleanup(func() { stopGrace = restore })
	x := &fakeExec{running: newFakeRunning(), keepRunning: true}
	tg := processTarget(&config.Process{Start: proxyCmd, Stop: "true"}, t.TempDir())
	inst, err := Start(t.Context(), tg, testVars(), x)
	if err != nil {
		t.Fatal(err)
	}
	if err := inst.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sent := x.running.sent(); len(sent) != once || sent[first] != syscall.SIGKILL {
		t.Errorf("signals = %v, want a kill after the grace period", sent)
	}
}

func TestStartProcessBackground(t *testing.T) {
	x := &fakeExec{out: []byte("started")}
	tg := processTarget(&config.Process{Start: "proxy --daemon", Stop: "proxy --quit", Background: true}, t.TempDir())
	inst, err := Start(t.Context(), tg, testVars(), x)
	if err != nil {
		t.Fatal(err)
	}
	if logs := inst.Logs(t.Context()); logs != "started" {
		t.Errorf("logs = %q", logs)
	}
	if err := inst.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	stop := x.seen()[len(x.seen())-fromEnd]
	if stop.Args[second] != "proxy --quit" {
		t.Errorf("stop command = %q", stop.Args[second])
	}
}

func TestStartProcessFailures(t *testing.T) {
	failing := &fakeExec{err: errors.New("exit status 127")}
	tg := processTarget(&config.Process{Start: "nope"}, t.TempDir())
	if _, err := Start(t.Context(), tg, testVars(), failing); err == nil {
		t.Error("a failed start reported success")
	}
	daemon := processTarget(&config.Process{Start: "nope", Stop: "x", Background: true}, t.TempDir())
	if _, err := Start(t.Context(), daemon, testVars(), failing); err == nil {
		t.Error("a failed background start reported success")
	}
	bad := processTarget(&config.Process{Start: "p", Files: []config.File{{Source: "missing", Target: "p.conf"}}}, t.TempDir())
	if _, err := Start(t.Context(), bad, testVars(), &fakeExec{}); err == nil {
		t.Error("a missing rendered file reported success")
	}
}

func TestCommandDir(t *testing.T) {
	base := t.TempDir()
	work := filepath.Join(base, "work")
	cases := map[string]string{
		unset:           work,
		"/opt/proxy":    "/opt/proxy",
		"relative/path": filepath.Join(base, "relative/path"),
	}
	for dir, want := range cases {
		tg := processTarget(&config.Process{Start: "p", WorkDir: dir}, base)
		if got := commandDir(tg, testVars(), work); got != want {
			t.Errorf("commandDir(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestRing(t *testing.T) {
	r := new(ring)
	if _, err := r.Write([]byte(strings.Repeat("a", logLimit+10))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("tail")); err != nil {
		t.Fatal(err)
	}
	got := r.String()
	if len(got) != logLimit || !strings.HasSuffix(got, "tail") {
		t.Errorf("ring holds %d bytes ending %q", len(got), got[max(0, len(got)-8):])
	}
}
