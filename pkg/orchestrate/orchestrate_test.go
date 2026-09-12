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
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/target"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	listenFlag   = "--listen "
	originFlag   = "--origin "
	startCommand = "proxy --listen ${LISTEN_PORT} --origin ${ORIGIN_URL} "
	badCommand   = "proxy --broken"
	testTimeout  = 10 * time.Second
	fakePID      = 4242
	doneBuffer   = 1
	oneSlot      = 1

	catalogNotice = "these targets share an origin"

	targetAlpha = "alpha"
	targetBeta  = "beta"
	targetGamma = "gamma"

	testOriginAddr = "127.0.0.1:5555"
	testOriginPort = 5555
	containerPort  = 9100
	deadTarget     = "http://127.0.0.1:1"
	deadOrigin     = "127.0.0.1:1"
	unusableHost   = "no.such.host.invalid"

	readyTimeout  = 50 * time.Millisecond
	readyInterval = 5 * time.Millisecond
	settleDelay   = 50 * time.Millisecond
	settleWindow  = 300 * time.Millisecond

	first       = 0
	second      = 1
	oneResult   = 1
	wantProxies = 2
	minProgress = 2
)

// fakeExec stands in for a process manager: it starts a real reverse proxy inside this process on
// the port and origin the start command names, so a catalog run exercises the whole path.
type fakeExec struct {
	mu      sync.Mutex
	started int
	// stopErr models a proxy whose stop command fails, leaving it to be reported as a lifecycle error.
	stopErr error
}

func (f *fakeExec) Output(context.Context, target.Command) ([]byte, error) {
	return nil, f.stopErr
}

func (f *fakeExec) Start(_ context.Context, c target.Command) (target.Running, error) {
	addr, origin, err := parseScript(c.Args[len(c.Args)-1])
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(origin)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: httputil.NewSingleHostReverseProxy(u), ReadHeaderTimeout: testTimeout}
	go func() { _ = srv.Serve(ln) }()
	f.mu.Lock()
	f.started++
	f.mu.Unlock()
	return &fakeRunning{srv: srv, done: make(chan error, doneBuffer)}, nil
}

func (f *fakeExec) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

type fakeRunning struct {
	srv  *http.Server
	done chan error
}

func (*fakeRunning) PID() int {
	return fakePID
}

func (f *fakeRunning) Signal(os.Signal) error {
	err := f.srv.Close()
	select {
	case f.done <- err:
	default:
	}
	return nil
}

func (f *fakeRunning) Wait() error {
	return <-f.done
}

type progress struct {
	mu   sync.Mutex
	msgs []string
}

func (p *progress) add(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.msgs = append(p.msgs, msg)
}

func (p *progress) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.msgs)
}

func (p *progress) contains(want string) bool {
	for _, msg := range p.seen() {
		if strings.Contains(msg, want) {
			return true
		}
	}
	return false
}

func parseScript(script string) (addr, origin string, err error) {
	port, okPort := field(script, listenFlag)
	origin, okOrigin := field(script, originFlag)
	if !okPort || !okOrigin {
		return unset, unset, errors.New("start command names no listener or origin")
	}
	return net.JoinHostPort(config.LoopbackHost, port), origin, nil
}

func field(script, flag string) (string, bool) {
	_, rest, ok := strings.Cut(script, flag)
	if !ok {
		return unset, false
	}
	value, _, _ := strings.Cut(rest, " ")
	return value, true
}

func forwardedTest() []*testdef.Test {
	yes := true
	return []*testdef.Test{{
		ID:    "reaches-origin",
		Title: "the request reaches the origin",
		Level: testdef.LevelMust,
		Steps: []*testdef.Step{{Expect: &testdef.Expect{
			Status:    testdef.StatusSet{http.StatusOK},
			Forwarded: &yes,
		}}},
	}}
}

func processTarget(name, start string) *config.Target {
	return &config.Target{Name: name, Process: &config.Process{Start: start}}
}

func okServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func runWithProgress(ctx context.Context, t *testing.T, cfg *config.Config, x target.Exec) (*results.Report, *progress) {
	t.Helper()
	p := &progress{}
	rep, err := Run(ctx, Options{Config: cfg, Tests: forwardedTest(), Timeout: testTimeout, Exec: x, Progress: p.add})
	if err != nil {
		t.Fatal(err)
	}
	return rep, p
}

func runCatalog(ctx context.Context, t *testing.T, cfg *config.Config, x target.Exec) *results.Report {
	t.Helper()
	rep, _ := runWithProgress(ctx, t, cfg, x)
	return rep
}

func TestRunCatalog(t *testing.T) {
	cfg := &config.Config{Targets: []*config.Target{
		processTarget(targetAlpha, startCommand),
		processTarget(targetBeta, startCommand),
		processTarget(targetGamma, badCommand),
	}}
	x := &fakeExec{}
	rep := runCatalog(t.Context(), t, cfg, x)
	if len(rep.Runs) != len(cfg.Targets) || rep.Tool == unset || rep.Finished.Before(rep.Started) {
		t.Fatalf("report = %+v", rep)
	}
	byName := runsByName(t, rep, cfg)
	assertPassed(t, byName, targetAlpha, targetBeta)
	if byName[targetAlpha].Target == byName[targetBeta].Target {
		t.Errorf("targets share the URL %q, so they are not isolated", byName[targetAlpha].Target)
	}
	if gamma := byName[targetGamma]; gamma.Error == unset || len(gamma.Results) != none {
		t.Errorf("gamma = %+v", gamma)
	}
	if x.count() != wantProxies {
		t.Errorf("started %d proxies", x.count())
	}
}

func runsByName(t *testing.T, rep *results.Report, cfg *config.Config) map[string]*results.Run {
	t.Helper()
	byName := map[string]*results.Run{}
	for i, run := range rep.Runs {
		if run.Name != cfg.Targets[i].Name {
			t.Errorf("run %d is %q, want %q: catalog order must be kept", i, run.Name, cfg.Targets[i].Name)
		}
		byName[run.Name] = run
	}
	return byName
}

func assertPassed(t *testing.T, byName map[string]*results.Run, names ...string) {
	t.Helper()
	for _, name := range names {
		run := byName[name]
		if run.Error != unset || len(run.Results) != oneResult || run.Results[first].Outcome != results.Pass {
			t.Errorf("%s: error %q, results %+v", name, run.Error, run.Results)
		}
	}
}

func TestRunUnmanagedTarget(t *testing.T) {
	ts := okServer(t)
	cfg := &config.Config{Targets: []*config.Target{{Name: "static", Proxy: &config.Proxy{URL: ts.URL}}}}
	run := runCatalog(t.Context(), t, cfg, &fakeExec{}).Runs[first]
	if run.Error != unset || run.Target != ts.URL || len(run.Results) != oneResult {
		t.Fatalf("run = %+v", run)
	}
	// the endpoint answers without forwarding, so the test fails: the point is that it produced a result
	if run.Results[first].Outcome != results.Fail {
		t.Errorf("outcome = %s", run.Results[first].Outcome)
	}
}

func TestRunProgressAndSettle(t *testing.T) {
	tg := processTarget(targetAlpha, startCommand)
	tg.Settle = testdef.Duration(time.Millisecond)
	cfg := &config.Config{Targets: []*config.Target{tg}}
	_, p := runWithProgress(t.Context(), t, cfg, &fakeExec{})
	msgs := p.seen()
	if len(msgs) < minProgress || !strings.Contains(msgs[first], targetAlpha) {
		t.Errorf("progress = %v", msgs)
	}
}

func TestRunReportsAFailedStop(t *testing.T) {
	tg := processTarget(targetAlpha, startCommand)
	tg.Process.Stop = "stop-proxy"
	cfg := &config.Config{Targets: []*config.Target{tg}}
	x := &fakeExec{stopErr: errors.New("exit status 1")}
	rep, p := runWithProgress(t.Context(), t, cfg, x)
	if run := rep.Runs[first]; run.Error != unset {
		t.Errorf("a failed stop must not fail the run: %+v", run)
	}
	if !p.contains("stopping") {
		t.Errorf("progress = %v, want the stop failure reported", p.seen())
	}
}

func TestRunOriginCannotBind(t *testing.T) {
	cfg := &config.Config{
		Origin:  config.Origin{Listen: unusableHost},
		Targets: []*config.Target{processTarget(targetAlpha, startCommand)},
	}
	run := runCatalog(t.Context(), t, cfg, &fakeExec{}).Runs[first]
	if run.Error == unset || !strings.Contains(run.Error, "origin") {
		t.Errorf("error = %q, want the origin's bind failure", run.Error)
	}
}

func TestRunTargetThatNeverAnswers(t *testing.T) {
	tg := &config.Target{
		Name:      "dead",
		Proxy:     &config.Proxy{URL: deadTarget},
		Readiness: &config.Readiness{Timeout: testdef.Duration(readyTimeout), Interval: testdef.Duration(readyInterval)},
	}
	cfg := &config.Config{Targets: []*config.Target{tg}}
	run := runCatalog(t.Context(), t, cfg, &fakeExec{}).Runs[first]
	if run.Error == unset || !strings.Contains(run.Error, "not ready") {
		t.Errorf("error = %q, want a readiness failure", run.Error)
	}
	if strings.Contains(run.Error, "last output") {
		t.Errorf("an unmanaged target has no output to report: %q", run.Error)
	}
}

func TestRunCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cfg := &config.Config{Targets: []*config.Target{processTarget(targetAlpha, startCommand)}}
	for _, run := range runCatalog(ctx, t, cfg, &fakeExec{}).Runs {
		if run.Error == unset {
			t.Errorf("%s ran to completion under a canceled context: %+v", run.Name, run)
		}
	}
}

func TestRunCanceledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o := Options{Config: &config.Config{}, Tests: forwardedTest(), Exec: &fakeExec{}}
	// the only slot is taken, so the target has to wait for one and the canceled context wins
	sem := make(chan struct{}, oneSlot)
	sem <- struct{}{}
	run := o.one(ctx, processTarget(targetAlpha, startCommand), sem)
	if run.Error == unset || !strings.Contains(run.Error, "canceled") {
		t.Errorf("run = %+v", run)
	}
	if run.Finished.Before(run.Started) {
		t.Errorf("run finished %s before it started %s", run.Finished, run.Started)
	}
}

func settleTarget(rawURL string, settle time.Duration) *config.Target {
	return &config.Target{Name: targetAlpha, Proxy: &config.Proxy{URL: rawURL}, Settle: testdef.Duration(settle)}
}

func TestAwaitSettleDelaysTheTests(t *testing.T) {
	ts := okServer(t)
	o := Options{}
	start := time.Now()
	if err := o.await(t.Context(), settleTarget(ts.URL, settleDelay), &target.Instance{BaseURL: ts.URL}, target.Vars{}); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(start); waited < settleDelay {
		t.Errorf("await waited %s, want at least the %s settle", waited, settleDelay)
	}
}

func TestAwaitCanceledWhileSettling(t *testing.T) {
	ts := okServer(t)
	ctx, cancel := context.WithTimeout(t.Context(), settleWindow)
	defer cancel()
	o := Options{}
	err := o.await(ctx, settleTarget(ts.URL, time.Minute), &target.Instance{BaseURL: ts.URL}, target.Vars{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("await = %v, want the expired context", err)
	}
}

func TestExerciseFailures(t *testing.T) {
	o := Options{Config: &config.Config{}, Tests: forwardedTest(), Timeout: testTimeout}
	tg := processTarget(targetAlpha, startCommand)
	cases := map[string]struct{ base, origin string }{
		"malformed origin address": {base: deadTarget, origin: "not-an-address"},
		"unusable target URL":      {base: "not-a-url", origin: testOriginAddr},
		"unreachable origin":       {base: deadTarget, origin: deadOrigin},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			assertExerciseFails(t, o, tg, c.base, c.origin)
		})
	}
}

func assertExerciseFails(t *testing.T, o Options, tg *config.Target, base, origin string) {
	t.Helper()
	run := &results.Run{Name: targetAlpha}
	if err := o.exercise(t.Context(), tg, &target.Instance{BaseURL: base}, origin, run); err == nil {
		t.Error("exercise succeeded")
	}
	if len(run.Results) != none {
		t.Errorf("a target that was never exercised produced results: %+v", run.Results)
	}
}

func TestVarsNetworkedDocker(t *testing.T) {
	o := Options{Config: &config.Config{Origin: config.Origin{Advertise: "gateway.test"}}}
	networked := &config.Target{
		Name:   "lab",
		Docker: &config.Docker{Image: "nginx", Network: "lab", ContainerPort: containerPort},
	}
	v, err := o.vars(networked, testOriginAddr)
	if err != nil || v.OriginHost != "gateway.test" || v.OriginPort != testOriginPort || v.ListenPort != containerPort {
		t.Fatalf("networked vars = %+v, %v", v, err)
	}
}

func TestVarsManagedAndUnmanaged(t *testing.T) {
	local := Options{Config: &config.Config{}}
	managed, err := local.vars(processTarget(targetAlpha, startCommand), testOriginAddr)
	if err != nil || managed.ListenPort <= none || managed.OriginHost != config.LoopbackHost {
		t.Fatalf("managed vars = %+v, %v", managed, err)
	}
	unmanaged, err := local.vars(&config.Target{Name: "x", Proxy: &config.Proxy{URL: "http://p"}}, testOriginAddr)
	if err != nil || unmanaged.ListenPort != none {
		t.Fatalf("unmanaged vars = %+v, %v", unmanaged, err)
	}
}

func TestVarsRejectsAMalformedOriginAddress(t *testing.T) {
	local := Options{Config: &config.Config{}}
	if _, err := local.vars(processTarget(targetAlpha, startCommand), "not-an-address"); err == nil {
		t.Error("a malformed origin address was accepted")
	}
}

func TestExecDefaultsToRealCommands(t *testing.T) {
	if (Options{}).exec() == nil {
		t.Error("exec must fall back to real commands")
	}
}

func TestRunRealProcessFailure(t *testing.T) {
	// the real process manager runs here, so a target that dies immediately is reported with the
	// output it produced rather than as a pass
	tg := processTarget("broken", "echo boom; exit 3")
	tg.Readiness = &config.Readiness{Timeout: testdef.Duration(readyTimeout), Interval: testdef.Duration(readyInterval)}
	cfg := &config.Config{Targets: []*config.Target{tg}}
	run := runCatalog(t.Context(), t, cfg, nil).Runs[first]
	if run.Error == unset || !strings.Contains(run.Error, "boom") {
		t.Errorf("error = %q, want the target's output", run.Error)
	}
	if len(run.Results) != none {
		t.Errorf("a target that never started produced results: %+v", run.Results)
	}
}

func TestRunValidation(t *testing.T) {
	cases := map[string]Options{
		"no config":  {Tests: forwardedTest()},
		"no targets": {Config: &config.Config{}, Tests: forwardedTest()},
		"no tests":   {Config: &config.Config{Targets: []*config.Target{processTarget(targetAlpha, startCommand)}}},
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Run(t.Context(), o); err == nil {
				t.Error("Run succeeded")
			}
		})
	}
}

func TestRunKeepsCatalogOrder(t *testing.T) {
	ts := okServer(t)
	// alpha settles long enough that beta, which starts alongside it, is certain to finish first
	slow := &config.Target{Name: targetAlpha, Proxy: &config.Proxy{URL: ts.URL}, Settle: testdef.Duration(settleWindow)}
	fast := &config.Target{Name: targetBeta, Proxy: &config.Proxy{URL: ts.URL}}
	rep := runCatalog(t.Context(), t, &config.Config{Targets: []*config.Target{slow, fast}}, &fakeExec{})
	if len(rep.Runs) != wantProxies {
		t.Fatalf("runs = %+v", rep.Runs)
	}
	alpha, beta := rep.Runs[first], rep.Runs[second]
	if !beta.Finished.Before(alpha.Finished) {
		t.Fatalf("beta finished at %s, not before alpha at %s; the targets did not overlap",
			beta.Finished, alpha.Finished)
	}
	// the report is ordered by the catalog, not by which target finished first
	if alpha.Name != targetAlpha || beta.Name != targetBeta {
		t.Errorf("runs = %s, %s, want %s, %s", alpha.Name, beta.Name, targetAlpha, targetBeta)
	}
}

func TestRunCarriesTheCatalogNotice(t *testing.T) {
	ts := okServer(t)
	cfg := &config.Config{
		Notice:  catalogNotice,
		Targets: []*config.Target{{Name: targetAlpha, Proxy: &config.Proxy{URL: ts.URL}}},
	}
	if got := runCatalog(t.Context(), t, cfg, &fakeExec{}).Notice; got != catalogNotice {
		t.Errorf("notice = %q, want %q", got, catalogNotice)
	}
}
