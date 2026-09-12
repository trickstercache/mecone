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

package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/trickstercache/mecone/pkg/corpus"
	"github.com/trickstercache/mecone/pkg/origin"
	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/protocol"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	proxyURL          = "http://proxy"
	wrongVersion      = protocol.Version + 1
	parallelStepCount = 2
	e2eGroup          = `group: basics
title: Basics
refs: [RFC9110#9.3.1]
tests:
  - id: basics-forwarded
    title: A no-store response is fetched every time
    level: must
    responses:
      nostore:
        headers: ["Cache-Control: no-store"]
        body: fresh every time
    steps:
      - arrange: true
        expect: {forwarded: true}
      - path: /file.txt
        query: q=1
        headers: ["X-Mecone-Probe: ${now}"]
        expect:
          status: 200
          body: fresh every time
          forwarded: true
          forwarded_headers: [X-Mecone-Probe]
  - id: basics-reuse
    title: A fresh response is reused
    level: may
    responses:
      fresh:
        headers: ["Cache-Control: max-age=60"]
    steps:
      - arrange: true
      - wait: 10ms
      - expect: {forwarded: false}
  - id: basics-needs-reuse
    title: Runs only when reuse works
    level: must
    requires: [basics-reuse]
    steps: [{}]
  - id: basics-h1-only
    title: Only over HTTP/1.1
    level: must
    protocols: [h1]
    steps: [{expect: {forwarded: true}}]
  - id: basics-broken-arrange
    title: The arrange step cannot succeed
    level: must
    steps:
      - arrange: true
        expect: {status: 418}
      - {}
  - id: basics-hints-and-trailers
    title: Early hints and trailers pass through
    level: must
    responses:
      extras:
        interim: [{status: 103, headers: ["Link: </a.css>; rel=preload"]}]
        trailers: ["Mecone-Sum: 42"]
        body: extras
    steps:
      - headers: ["TE: trailers"]
        expect:
          interim: [103]
          trailers: ["Mecone-Sum: 42"]
  - id: basics-disconnect
    title: The origin hangs up
    level: must
    responses:
      gone: {disconnect: true}
    steps: [{expect: {status: 200}}]
  - id: basics-host-override
    title: A Host header line sets the request host
    level: info
    steps:
      - headers: ["Host: example.test"]
        expect:
          forwarded: true
          forwarded_headers: ["Host: example.test"]
`
	cachePath       = "/a.txt"
	cacheBody       = "cached payload"
	cacheSteps      = 2
	firstStepNumber = 1
	noError         = ""
	noMS            = 0
	cacheGroup      = `group: hit
title: Cache hits
refs: [RFC9111#4]
tests:
  - id: cache-hit
    title: A stored response is served without contacting the origin
    level: must
    responses:
      cacheable:
        headers: ["Cache-Control: max-age=60"]
        body: cached payload
    steps:
      - path: /a.txt
        expect: {status: 200, body: cached payload, forwarded: true}
      - path: /a.txt
        expect: {status: 200, body: cached payload, forwarded: false}
`
)

func loadGroup(t *testing.T, suite, group, src string) []*testdef.Test {
	t.Helper()
	c, err := corpus.Load(fstest.MapFS{
		suite + "/suite.yaml":         {Data: []byte("id: " + suite + "\ntitle: " + suite + "\n")},
		suite + "/" + group + ".yaml": {Data: []byte(src)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c.Tests()
}

func loadTests(t *testing.T) []*testdef.Test {
	t.Helper()
	return loadGroup(t, "e2e", "basics", e2eGroup)
}

func oneTest() []*testdef.Test {
	return []*testdef.Test{{ID: "x", Level: testdef.LevelMust, Steps: []*testdef.Step{{}}}}
}

func startOrigin(t *testing.T) *url.URL {
	t.Helper()
	ts := httptest.NewServer(origin.New(origin.Options{}).Handler())
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func startProxy(t *testing.T, originURL *url.URL, p proto.Proto) string {
	t.Helper()
	ts := httptest.NewUnstartedServer(httputil.NewSingleHostReverseProxy(originURL))
	switch p {
	case proto.H2:
		ts.EnableHTTP2 = true
		ts.StartTLS()
	case proto.H2C:
		ps := new(http.Protocols)
		ps.SetHTTP1(true)
		ps.SetUnencryptedHTTP2(true)
		ts.Config.Protocols = ps
		ts.Start()
	default:
		ts.Start()
	}
	t.Cleanup(ts.Close)
	return ts.URL
}

// startCachingProxy fronts the origin with a proxy that stores the first response for a path and
// answers every later request for it without contacting the origin.
func startCachingProxy(t *testing.T, originURL *url.URL) string {
	t.Helper()
	rp := httputil.NewSingleHostReverseProxy(originURL)
	var mu sync.Mutex
	cache := make(map[string][]byte)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if body, stored := cache[r.URL.Path]; stored {
			_, _ = w.Write(body)
			return
		}
		rec := httptest.NewRecorder()
		rp.ServeHTTP(rec, r)
		resp := rec.Result()
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cache[r.URL.Path] = body
		maps.Copy(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func mustNew(t *testing.T, o Options) *Runner {
	t.Helper()
	r, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mustRun(t *testing.T, o Options, tests []*testdef.Test) *results.Run {
	t.Helper()
	run, err := mustNew(t, o).Run(t.Context(), tests)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func proxyOutcomes(p proto.Proto) map[string]results.Outcome {
	want := map[string]results.Outcome{
		"basics-forwarded":          results.Pass,
		"basics-reuse":              results.Fail,
		"basics-needs-reuse":        results.Skipped,
		"basics-h1-only":            results.Pass,
		"basics-broken-arrange":     results.Inconclusive,
		"basics-hints-and-trailers": results.Pass,
		"basics-disconnect":         results.Fail,
		"basics-host-override":      results.Pass,
	}
	if p != proto.H1 {
		want["basics-h1-only"] = results.Skipped
	}
	return want
}

func checkOutcomes(t *testing.T, run *results.Run, want map[string]results.Outcome) {
	t.Helper()
	if len(run.Results) != len(want) {
		t.Fatalf("got %d results, want %d", len(run.Results), len(want))
	}
	for _, res := range run.Results {
		checkOutcome(t, res, want[res.ID])
	}
}

func checkOutcome(t *testing.T, res results.Result, want results.Outcome) {
	t.Helper()
	if res.Outcome != want {
		t.Errorf("%s: %s, want %s: %v", res.ID, res.Outcome, want, res.Messages)
	}
	if !slices.Equal(res.Refs, []testdef.Ref{"RFC9110#9.3.1"}) {
		t.Errorf("%s: refs = %v", res.ID, res.Refs)
	}
	if res.ID == "basics-forwarded" && res.OriginProto != string(proto.H1) {
		t.Errorf("origin protocol = %q", res.OriginProto)
	}
}

func TestRunThroughProxy(t *testing.T) {
	tests := loadTests(t)
	originURL := startOrigin(t)
	for _, p := range []proto.Proto{proto.H1, proto.H2, proto.H2C} {
		t.Run(string(p), func(t *testing.T) {
			run := mustRun(t, Options{
				Target:        startProxy(t, originURL, p),
				OriginControl: originURL.String(),
				Protocols:     []proto.Proto{p},
				Insecure:      true,
				Timeout:       5 * time.Second,
			}, tests)
			checkOutcomes(t, run, proxyOutcomes(p))
		})
	}
}

func TestRunInBandControl(t *testing.T) {
	target := startProxy(t, startOrigin(t), proto.H1)
	run := mustRun(t, Options{Target: target + "/"}, loadTests(t)[:1])
	if res := run.Results[first]; res.Outcome != results.Pass || run.Protocols[first] != string(proto.H1) {
		t.Errorf("in-band run: %+v", res)
	}
}

func TestRunBadRequest(t *testing.T) {
	bad := &testdef.Test{ID: "bad", Level: testdef.LevelMust, Steps: []*testdef.Step{{Method: "BAD METHOD"}}}
	run := mustRun(t, Options{Target: startOrigin(t).String()}, []*testdef.Test{bad})
	res := run.Results[first]
	if res.Outcome != results.Fail || !strings.Contains(res.Messages[first], "request failed") {
		t.Fatalf("result = %+v", res)
	}
	if step := res.Steps[first]; step.Error == noError || step.Forwarded {
		t.Errorf("step record = %+v", step)
	}
}

func checkStepRecord(t *testing.T, i int, s results.Step) {
	t.Helper()
	if s.Step != i+firstStepNumber || s.Method != http.MethodGet || s.Path != cachePath {
		t.Errorf("step record %d = %+v", i, s)
	}
	if s.Status != http.StatusOK || s.Bytes != len(cacheBody) || s.Error != noError {
		t.Errorf("step %d: status %d, %d bytes, error %q", s.Step, s.Status, s.Bytes, s.Error)
	}
	if s.TTFBMS <= noMS || s.TotalMS < s.TTFBMS {
		t.Errorf("step %d: ttfb %v ms, total %v ms", s.Step, s.TTFBMS, s.TotalMS)
	}
}

func TestStepRecords(t *testing.T) {
	originURL := startOrigin(t)
	run := mustRun(t, Options{
		Target:        startCachingProxy(t, originURL),
		OriginControl: originURL.String(),
		Timeout:       5 * time.Second,
	}, loadGroup(t, "cache", "hit", cacheGroup))
	res := run.Results[first]
	if res.Outcome != results.Pass {
		t.Fatalf("outcome = %s: %v", res.Outcome, res.Messages)
	}
	if len(res.Steps) != cacheSteps {
		t.Fatalf("got %d step records, want %d", len(res.Steps), cacheSteps)
	}
	for i, s := range res.Steps {
		checkStepRecord(t, i, s)
	}
	miss, hit := res.Steps[first], res.Steps[cacheSteps-firstStepNumber]
	if !miss.Forwarded || hit.Forwarded {
		t.Errorf("forwarded: miss %v, hit %v", miss.Forwarded, hit.Forwarded)
	}
	if miss.Reused || !hit.Reused {
		t.Errorf("connection reused: miss %v, hit %v", miss.Reused, hit.Reused)
	}
}

func TestParallelStepsStartTogether(t *testing.T) {
	var mu sync.Mutex
	arrived := none
	ready := make(chan struct{})
	ts := httptest.NewServer(overlapHandler(&mu, &arrived, ready))
	defer ts.Close()
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{target: u}
	steps := []*testdef.Step{{Parallel: true}, {Parallel: true}}
	obs := r.sendSteps(t.Context(), ts.Client(), "parallel", steps)
	for i, o := range obs {
		if o.Err != nil || o.Status != http.StatusOK {
			t.Errorf("step %d: status %d, error %v", i+1, o.Status, o.Err)
		}
	}
}

func overlapHandler(mu *sync.Mutex, arrived *int, ready chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		*arrived++
		if *arrived == parallelStepCount {
			close(ready)
		}
		mu.Unlock()
		select {
		case <-ready:
		case <-time.After(time.Second):
			http.Error(w, "requests did not overlap", http.StatusGatewayTimeout)
		}
	})
}

func TestRunCanceled(t *testing.T) {
	r := mustNew(t, Options{Target: startOrigin(t).String(), Concurrency: 1})
	long := testdef.Duration(5 * time.Second)
	slow := &testdef.Test{ID: "slow", Level: testdef.LevelMust, Steps: []*testdef.Step{{}, {Wait: &long}, {}}}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	run, err := r.Run(ctx, []*testdef.Test{slow, {ID: "quick", Level: testdef.LevelMust, Steps: []*testdef.Step{{}}}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Results[first].Outcome != results.Error {
		t.Errorf("slow test: %+v", run.Results[first])
	}
}

func fakeOrigin(t *testing.T, protocolVersion, putStatus, logStatus int) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+protocol.InfoPath, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.Info{Protocol: protocolVersion})
	})
	mux.HandleFunc("PUT "+protocol.ScriptsPath+"{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(putStatus)
	})
	mux.HandleFunc("DELETE "+protocol.ScriptsPath+"{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET "+protocol.LogsPath+"{id}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", logStatus)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestRunControlFailures(t *testing.T) {
	cases := map[string]struct {
		target    string
		wantInMsg string
	}{
		"upload fails": {fakeOrigin(t, protocol.Version, http.StatusInternalServerError, http.StatusOK), "uploading script"},
		"log fails":    {fakeOrigin(t, protocol.Version, http.StatusNoContent, http.StatusInternalServerError), "fetching origin log"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			run := mustRun(t, Options{Target: tc.target}, oneTest())
			if res := run.Results[first]; res.Outcome != results.Error || !strings.Contains(res.Messages[first], tc.wantInMsg) {
				t.Errorf("result = %+v", res)
			}
		})
	}
}

func TestRunHandshakeFailures(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	cases := map[string]string{
		"version mismatch": fakeOrigin(t, wrongVersion, http.StatusNoContent, http.StatusOK),
		"closed origin":    closed.URL,
	}
	for name, target := range cases {
		t.Run(name, func(t *testing.T) {
			r := mustNew(t, Options{Target: target, Timeout: time.Second})
			if _, err := r.Run(t.Context(), oneTest()); err == nil {
				t.Error("Run succeeded")
			}
		})
	}
}

func TestNewErrors(t *testing.T) {
	cases := map[string]Options{
		"empty target":  {},
		"ftp target":    {Target: "ftp://proxy"},
		"no host":       {Target: "http://"},
		"bad url":       {Target: "http://[::1"},
		"h2 over http":  {Target: proxyURL, Protocols: []proto.Proto{proto.H2}},
		"h3":            {Target: "https://proxy", Protocols: []proto.Proto{proto.H3}},
		"bad control":   {Target: proxyURL, OriginControl: "origin:8000"},
		"unknown proto": {Target: proxyURL, Protocols: []proto.Proto{"h9"}},
	}
	for name, o := range cases {
		if _, err := New(o); err == nil {
			t.Errorf("%s: New succeeded", name)
		}
	}
	if _, err := New(Options{Target: "https://proxy", Protocols: []proto.Proto{proto.H3}}); !errors.Is(err, proto.ErrUnsupported) {
		t.Errorf("h3: %v", err)
	}
}
