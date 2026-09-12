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

package orchestrate

import (
	"net"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/target"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	pinnedName     = "pinned"
	unitPort       = 8000
	specificHost   = "127.0.0.2"
	pinnedProbe    = 2 * time.Second
	pinnedInterval = 10 * time.Millisecond
)

func pinnedTarget(t *testing.T, proxyURL string, port int) *config.Target {
	t.Helper()
	return &config.Target{
		Name:  pinnedName,
		Proxy: &config.Proxy{URL: proxyURL, OriginPort: port},
		Readiness: &config.Readiness{
			Timeout:  testdef.Duration(pinnedProbe),
			Interval: testdef.Duration(pinnedInterval),
		},
	}
}

// A proxy Mecone does not configure can only be aimed at an upstream that is known in advance, so
// pinning the port is what makes an unmanaged target usable at all.
func TestPinnedOriginPortIsReachable(t *testing.T) {
	port, err := target.FreePort(config.LoopbackHost)
	if err != nil {
		t.Fatal(err)
	}
	upstream, err := url.Parse("http://" + net.JoinHostPort(config.LoopbackHost, strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(httputil.NewSingleHostReverseProxy(upstream))
	t.Cleanup(ts.Close)
	cfg := &config.Config{Targets: []*config.Target{pinnedTarget(t, ts.URL, port)}}
	run := runCatalog(t.Context(), t, cfg, &fakeExec{}).Runs[first]
	if run.Error != unset || len(run.Results) != oneResult {
		t.Fatalf("run = %+v", run)
	}
	if run.Results[first].Outcome != results.Pass {
		t.Errorf("outcome = %s: the proxy never reached the pinned origin: %v", run.Results[first].Outcome, run.Results[first].Messages)
	}
}

func TestPinnedOriginPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", net.JoinHostPort(config.LoopbackHost, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := ln.Close(); err != nil {
			t.Error(err)
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	cfg := &config.Config{Targets: []*config.Target{pinnedTarget(t, deadTarget, port)}}
	run := runCatalog(t.Context(), t, cfg, &fakeExec{}).Runs[first]
	if run.Error == unset {
		t.Error("an origin that could not bind its pinned port was reported as a success")
	}
}

func TestOriginPort(t *testing.T) {
	if got := originPort(processTarget(targetAlpha, startCommand)); got != anyPort {
		t.Errorf("managed target origin port = %q, want an ephemeral one", got)
	}
	if got := originPort(&config.Target{Proxy: &config.Proxy{}}); got != anyPort {
		t.Errorf("unpinned origin port = %q, want an ephemeral one", got)
	}
	if got := originPort(&config.Target{Proxy: &config.Proxy{OriginPort: unitPort}}); got != strconv.Itoa(unitPort) {
		t.Errorf("pinned origin port = %q", got)
	}
}

func TestControlHost(t *testing.T) {
	cases := map[string]string{
		unset:                config.LoopbackHost,
		config.AllInterfaces: config.LoopbackHost,
		specificHost:         specificHost,
	}
	for listen, want := range cases {
		o := Options{Config: &config.Config{Origin: config.Origin{Listen: listen}}}
		if got := o.controlHost(); got != want {
			t.Errorf("controlHost(%q) = %q, want %q", listen, got, want)
		}
	}
}
