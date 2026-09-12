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

// Package client implements the Mecone client role: it drives tests through the proxy under test,
// coordinates with the origin over the control plane, and judges the proxy's behavior.
package client

import (
	"cmp"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/trickstercache/mecone/pkg/check"
	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/protocol"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	none               = 0
	first              = 0
	stepIncrement      = 1
	defaultConcurrency = 8
	defaultTimeout     = 30 * time.Second
	maxBody            = 16 << 20
)

// Options configures a Runner.
type Options struct {
	// Target is the base URL of the proxy under test.
	Target string
	// OriginControl is the origin's control-plane URL when the client can reach it directly;
	// empty sends control traffic through Target.
	OriginControl string
	Protocols     []proto.Proto
	Concurrency   int
	Timeout       time.Duration
	Insecure      bool
}

// Runner executes tests against one proxy and origin pair.
type Runner struct {
	opts    Options
	target  *url.URL
	control *Control
	clients map[proto.Proto]*http.Client
}

// New validates o and builds a Runner.
func New(o Options) (*Runner, error) {
	opts := withDefaults(o)
	target, err := parseBase(opts.Target)
	if err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}
	copts := proto.ClientOptions{Timeout: opts.Timeout, InsecureSkipVerify: opts.Insecure}
	clients, err := newClients(opts.Protocols, target.Scheme, copts)
	if err != nil {
		return nil, err
	}
	hc, err := proto.NewClient(proto.H1, copts)
	if err != nil {
		return nil, err
	}
	control, err := NewControl(cmp.Or(opts.OriginControl, opts.Target), hc)
	if err != nil {
		return nil, fmt.Errorf("origin control: %w", err)
	}
	return &Runner{opts: opts, target: target, control: control, clients: clients}, nil
}

func withDefaults(o Options) Options {
	if len(o.Protocols) == none {
		o.Protocols = []proto.Proto{proto.H1}
	}
	if o.Concurrency <= none {
		o.Concurrency = defaultConcurrency
	}
	if o.Timeout <= none {
		o.Timeout = defaultTimeout
	}
	return o
}

func newClients(protos []proto.Proto, scheme string, copts proto.ClientOptions) (map[proto.Proto]*http.Client, error) {
	clients := make(map[proto.Proto]*http.Client, len(protos))
	for _, p := range protos {
		if err := p.CheckScheme(scheme); err != nil {
			return nil, err
		}
		hc, err := proto.NewClient(p, copts)
		if err != nil {
			return nil, err
		}
		clients[p] = hc
	}
	return clients, nil
}

type job struct {
	test   *testdef.Test
	proto  proto.Proto
	done   chan struct{}
	result results.Result
}

func jobKey(id string, p proto.Proto) string {
	return id + "|" + string(p)
}

// Run executes every test over every configured protocol and returns the results in a stable order.
func (r *Runner) Run(ctx context.Context, tests []*testdef.Test) (*results.Run, error) {
	if err := r.handshake(ctx); err != nil {
		return nil, err
	}
	run := &results.Run{Target: r.opts.Target, Started: time.Now().UTC()}
	for _, p := range r.opts.Protocols {
		run.Protocols = append(run.Protocols, string(p))
	}
	run.Results = r.execute(ctx, tests)
	run.Finished = time.Now().UTC()
	return run, nil
}

func (r *Runner) handshake(ctx context.Context) error {
	info, err := r.control.Info(ctx)
	if err != nil {
		return fmt.Errorf("origin handshake: %w", err)
	}
	if info.Protocol != protocol.Version {
		return fmt.Errorf("origin speaks control protocol v%d; this client speaks v%d", info.Protocol, protocol.Version)
	}
	return nil
}

func (r *Runner) plan(tests []*testdef.Test) ([]*job, map[string]*job) {
	jobs := make([]*job, none, len(tests)*len(r.opts.Protocols))
	byKey := make(map[string]*job, cap(jobs))
	for _, p := range r.opts.Protocols {
		for _, t := range tests {
			j := &job{test: t, proto: p, done: make(chan struct{})}
			jobs = append(jobs, j)
			byKey[jobKey(t.ID, p)] = j
		}
	}
	return jobs, byKey
}

func (r *Runner) execute(ctx context.Context, tests []*testdef.Test) []results.Result {
	jobs, byKey := r.plan(tests)
	sem := make(chan struct{}, r.opts.Concurrency)
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Go(func() {
			defer close(j.done)
			j.result = r.schedule(ctx, j, byKey, sem)
		})
	}
	wg.Wait()
	out := make([]results.Result, len(jobs))
	for i, j := range jobs {
		out[i] = j.result
	}
	return out
}

func (r *Runner) schedule(ctx context.Context, j *job, byKey map[string]*job, sem chan struct{}) results.Result {
	t := j.test
	res := results.Result{
		ID: t.ID, Suite: t.Suite, Group: t.Group, Level: string(t.Level), Refs: slices.Clone(t.Refs),
		ClientProto: string(j.proto),
	}
	if !t.RunsOver(j.proto) {
		return res.With(results.Skipped, "not applicable over "+string(j.proto))
	}
	// Dependencies are awaited before taking a slot, so a blocked job never holds a slot they need.
	for _, dep := range t.Requires {
		d, ok := byKey[jobKey(dep, j.proto)]
		if !ok {
			return res.With(results.Skipped, "requires "+dep+", which was not selected")
		}
		<-d.done
		if d.result.Outcome != results.Pass {
			return res.With(results.Skipped, fmt.Sprintf("requires %s, which did not pass (%s)", dep, d.result.Outcome))
		}
	}
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		return res.With(results.Error, "canceled before start")
	}
	defer func() { <-sem }()
	start := time.Now()
	res = r.exercise(ctx, res, t, j.proto)
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

func (r *Runner) exercise(ctx context.Context, res results.Result, t *testdef.Test, p proto.Proto) results.Result {
	// Each run gets a fresh test ID so concurrent runs of the same test never share origin state.
	id := protocol.NewID()
	if err := r.control.PutScript(ctx, id, t.Script()); err != nil {
		return res.With(results.Error, "uploading script: "+err.Error())
	}
	defer func() { _ = r.control.DeleteScript(context.WithoutCancel(ctx), id) }()
	obs := r.sendSteps(ctx, r.clients[p], id, t.Steps)
	if ctx.Err() != nil {
		return res.With(results.Error, "canceled")
	}
	log, err := r.control.Log(ctx, id)
	if err != nil {
		return res.With(results.Error, "fetching origin log: "+err.Error())
	}
	if len(log) > none {
		res.OriginProto = log[first].Proto
	}
	res.Steps = stepRecords(t.Steps, obs, log)
	fails := check.Evaluate(t, obs, log)
	msgs := make([]string, len(fails))
	for i, f := range fails {
		msgs[i] = f.String()
	}
	return res.With(check.Verdict(fails), msgs...)
}

func (r *Runner) sendSteps(ctx context.Context, hc *http.Client, id string, steps []*testdef.Step) []check.Observation {
	obs := make([]check.Observation, len(steps))
	for i := first; i < len(steps); {
		i = r.sendStepGroup(ctx, hc, id, steps, obs, i)
	}
	return obs
}

func (r *Runner) sendStepGroup(ctx context.Context, hc *http.Client, id string,
	steps []*testdef.Step, obs []check.Observation, i int,
) int {
	s := steps[i]
	if s.IsWait() {
		if sendWait(ctx, s) {
			return i + stepIncrement
		}
		return len(steps)
	}
	if s.Parallel {
		end := parallelEnd(steps, i)
		r.sendParallel(ctx, hc, id, steps, obs, i, end)
		return end
	}
	obs[i] = r.send(ctx, hc, id, i+1, s)
	return i + stepIncrement
}

func sendWait(ctx context.Context, step *testdef.Step) bool {
	return sleep(ctx, time.Duration(*step.Wait))
}

func parallelEnd(steps []*testdef.Step, start int) int {
	end := start
	for end < len(steps) && !steps[end].IsWait() && steps[end].Parallel {
		end++
	}
	return end
}

func (r *Runner) sendParallel(ctx context.Context, hc *http.Client, id string,
	steps []*testdef.Step, obs []check.Observation, start, end int,
) {
	var wg sync.WaitGroup
	for n := start; n < end; n++ {
		wg.Go(func() { obs[n] = r.send(ctx, hc, id, n+1, steps[n]) })
	}
	wg.Wait()
}

func (r *Runner) send(ctx context.Context, hc *http.Client, id string, n int, s *testdef.Step) check.Observation {
	var obs check.Observation
	u := joinPath(r.target, protocol.TestPath(id, s.Path))
	u.RawQuery = s.Query
	var body io.Reader
	if s.Body != "" {
		body = strings.NewReader(s.Body)
	}
	var start time.Time
	trace := &httptrace.ClientTrace{
		Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
			obs.Interim = append(obs.Interim, code)
			return nil
		},
		GotConn:              func(info httptrace.GotConnInfo) { obs.Reused = info.Reused },
		GotFirstResponseByte: func() { obs.TTFB = time.Since(start) },
	}
	method := cmp.Or(s.Method, http.MethodGet)
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, u.String(), body)
	if err != nil {
		obs.Err = err
		return obs
	}
	now := time.Now()
	for _, line := range s.Headers {
		name, value, _ := protocol.SplitLine(line)
		req.Header.Add(name, protocol.Expand(value, now))
	}
	if host := req.Header.Get("Host"); host != "" {
		req.Host = host
		req.Header.Del("Host")
	}
	req.Header.Set(protocol.HeaderStep, strconv.Itoa(n))
	start = time.Now()
	resp, err := hc.Do(req)
	if err != nil {
		obs.Err = err
		return obs
	}
	defer func() { _ = resp.Body.Close() }()
	obs.Status, obs.Header, obs.Proto = resp.StatusCode, resp.Header, resp.Proto
	obs.Body, err = io.ReadAll(io.LimitReader(resp.Body, maxBody))
	obs.Total = time.Since(start)
	if err != nil {
		obs.Err = fmt.Errorf("reading response body: %w", err)
	}
	obs.Trailer = resp.Trailer
	return obs
}

// stepRecords describes what each request step did; wait steps have nothing to report.
func stepRecords(steps []*testdef.Step, obs []check.Observation, log protocol.Log) []results.Step {
	out := make([]results.Step, none, len(steps))
	for i, o := range obs[:min(len(obs), len(steps))] {
		if steps[i].IsWait() {
			continue
		}
		out = append(out, stepRecord(i+1, steps[i], o, log))
	}
	return out
}

func stepRecord(n int, s *testdef.Step, o check.Observation, log protocol.Log) results.Step {
	rec := results.Step{
		Step:      n,
		Method:    cmp.Or(s.Method, http.MethodGet),
		Path:      s.Path,
		Status:    o.Status,
		Forwarded: len(log.Step(n)) > none,
		Reused:    o.Reused,
		TTFBMS:    millis(o.TTFB),
		TotalMS:   millis(o.Total),
		Bytes:     len(o.Body),
	}
	if o.Err != nil {
		rec.Error = o.Err.Error()
	}
	return rec
}

func millis(d time.Duration) float64 {
	return float64(d) / float64(time.Millisecond)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
