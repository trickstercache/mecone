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

package config

import "testing"

const (
	wantOriginPort = 8000

	pinnedOrigin = `
targets:
  - name: staging
    proxy:
      url: http://proxy.example.com
      origin_port: 8000
`
	unpinnedOrigin = `
targets:
  - name: staging
    proxy:
      url: http://proxy.example.com
`
	backgroundQuit = `
targets:
  - name: local
    process:
      start: "proxy --daemon"
      stop: "proxy --quit"
      background: true
`
)

func TestProxyOriginPort(t *testing.T) {
	c := mustLoad(t, pinnedOrigin)
	if got := c.Targets[proxyTarget].Proxy.OriginPort; got != wantOriginPort {
		t.Errorf("origin port = %d, want %d", got, wantOriginPort)
	}
	if got := mustLoad(t, unpinnedOrigin).Targets[proxyTarget].Proxy.OriginPort; got != none {
		t.Errorf("unpinned origin port = %d, want an ephemeral one", got)
	}
	if _, err := loadBody(t, "targets:\n  - name: x\n    proxy: {url: 'http://p', origin_port: 70000}\n"); err == nil {
		t.Error("an out-of-range origin port was accepted")
	}
}

// A backgrounding start command leaves no process to signal, so ${PID} would expand to 0 and a
// shell would signal every process in Mecone's own group.
func TestBackgroundStopRejectsPID(t *testing.T) {
	if _, err := loadBody(t, "targets:\n  - name: x\n    process: {start: p, stop: 'kill ${PID}', background: true}\n"); err == nil {
		t.Error("a backgrounded target was allowed to stop itself with ${PID}")
	}
	if _, err := loadBody(t, backgroundQuit); err != nil {
		t.Errorf("a backgrounded target with its own stop command was rejected: %v", err)
	}
	if _, err := loadBody(t, "targets:\n  - name: x\n    process: {start: p, stop: 'kill ${PID}'}\n"); err != nil {
		t.Errorf("a foreground target may use ${PID}: %v", err)
	}
}
