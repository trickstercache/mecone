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

// Package orchestrate tests every target in a catalog, starting and stopping the proxies Mecone
// manages and keeping each target's origin, traffic and results separate from the others'.
package orchestrate

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/trickstercache/mecone/pkg/appinfo"
	"github.com/trickstercache/mecone/pkg/client"
	"github.com/trickstercache/mecone/pkg/config"
	"github.com/trickstercache/mecone/pkg/origin"
	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/target"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	unset         = ""
	none          = 0
	anyPort       = "0"
	shutdownGrace = 10 * time.Second
)

// Options configures a catalog run.
type Options struct {
	Config      *config.Config
	Tests       []*testdef.Test
	Protocols   []proto.Proto
	Concurrency int
	Timeout     time.Duration
	Insecure    bool
	// Exec runs the commands that manage targets; nil runs real ones.
	Exec target.Exec
	// Progress reports lifecycle milestones while targets start and stop.
	Progress func(string)
}

// Run tests every target in the catalog and returns one run per target, in catalog order. A
// target that cannot be started or reached is recorded as a failed run rather than ending the
// report, so one broken proxy never hides the results of the others.
func Run(ctx context.Context, o Options) (*results.Report, error) {
	if o.Config == nil || len(o.Config.Targets) == none {
		return nil, errors.New("the configuration defines no targets")
	}
	if len(o.Tests) == none {
		return nil, errors.New("no tests were selected")
	}
	rep := &results.Report{Tool: appinfo.Name, Version: appinfo.Version, Started: time.Now().UTC(), Notice: o.Config.Notice}
	runs := make([]*results.Run, len(o.Config.Targets))
	sem := make(chan struct{}, o.Config.TargetConcurrency())
	var wg sync.WaitGroup
	for i, t := range o.Config.Targets {
		wg.Go(func() { runs[i] = o.one(ctx, t, sem) })
	}
	wg.Wait()
	rep.Runs, rep.Finished = runs, time.Now().UTC()
	return rep, nil
}

func (o Options) one(ctx context.Context, t *config.Target, sem chan struct{}) *results.Run {
	run := &results.Run{Name: t.Name, Started: time.Now().UTC()}
	select {
	case sem <- struct{}{}:
	case <-ctx.Done():
		run.Error, run.Finished = "canceled before start", time.Now().UTC()
		return run
	}
	defer func() { <-sem }()
	o.report(t.Name + ": starting tests")
	if err := o.test(ctx, t, run); err != nil {
		run.Error = err.Error()
		o.report(t.Name + ": " + err.Error())
	}
	run.Finished = time.Now().UTC()
	o.report(t.Name + ": done testing")
	return run
}

// test gives the target its own origin, brings the proxy up, waits for the whole path to work,
// and runs the suites through it.
func (o Options) test(ctx context.Context, t *config.Target, run *results.Run) error {
	srv := origin.New(origin.Options{Listen: net.JoinHostPort(o.Config.ListenHost(), originPort(t))})
	if err := srv.Listen(); err != nil {
		return fmt.Errorf("origin: %w", err)
	}
	octx, stopOrigin := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { _ = srv.Serve(octx) })
	defer func() {
		stopOrigin()
		wg.Wait()
	}()

	v, err := o.vars(t, srv.Addr())
	if err != nil {
		return err
	}
	inst, err := target.Start(ctx, t, v, o.exec())
	if err != nil {
		return err
	}
	defer o.stop(ctx, t, inst)
	if err := o.await(ctx, t, inst, v); err != nil {
		return err
	}
	return o.exercise(ctx, t, inst, srv.Addr(), run)
}

func (o Options) vars(t *config.Target, originAddr string) (target.Vars, error) {
	port, err := portOf(originAddr)
	if err != nil {
		return target.Vars{}, err
	}
	v := target.Vars{
		Name:       t.Name,
		ListenHost: config.LoopbackHost,
		OriginHost: cmp.Or(o.Config.Origin.Advertise, target.OriginHost(t)),
		OriginPort: port,
	}
	switch {
	case !t.Managed():
		return v, nil
	case t.Docker != nil && t.Docker.JoinsNetwork():
		v.ListenPort = t.Docker.Port()
		return v, nil
	}
	if v.ListenPort, err = target.FreePort(config.LoopbackHost); err != nil {
		return v, fmt.Errorf("allocating a port for %s: %w", t.Name, err)
	}
	return v, nil
}

// originPort pins the origin's port when a target names one, which is how a proxy Mecone does not
// configure can be aimed at a known upstream address.
func originPort(t *config.Target) string {
	if t.Proxy != nil && t.Proxy.OriginPort > none {
		return strconv.Itoa(t.Proxy.OriginPort)
	}
	return anyPort
}

// controlHost is where the client reaches the origin it just started: the interface the origin
// bound, unless that is every interface, which is not an address anything can dial.
func (o Options) controlHost() string {
	if h := o.Config.ListenHost(); h != config.AllInterfaces {
		return h
	}
	return config.LoopbackHost
}

func (Options) await(ctx context.Context, t *config.Target, inst *target.Instance, v target.Vars) error {
	if err := target.WaitReady(ctx, inst.BaseURL, t.Probe(), v); err != nil {
		if logs := inst.Logs(ctx); logs != unset {
			return fmt.Errorf("%w; last output from the target:\n%s", err, logs)
		}
		return err
	}
	if t.Settle > none && !sleep(ctx, time.Duration(t.Settle)) {
		return ctx.Err()
	}
	return nil
}

func (o Options) exercise(ctx context.Context, t *config.Target, inst *target.Instance, originAddr string, run *results.Run) error {
	port, err := portOf(originAddr)
	if err != nil {
		return err
	}
	r, err := client.New(client.Options{
		Target:        inst.BaseURL,
		OriginControl: "http://" + net.JoinHostPort(o.controlHost(), strconv.Itoa(port)),
		Protocols:     o.Protocols,
		Concurrency:   o.Concurrency,
		Timeout:       o.Timeout,
		Insecure:      o.Insecure || (t.Proxy != nil && t.Proxy.Insecure),
	})
	if err != nil {
		return err
	}
	got, err := r.Run(ctx, o.Tests)
	if err != nil {
		return err
	}
	run.Target, run.Protocols, run.Results = got.Target, got.Protocols, got.Results
	return nil
}

func (o Options) stop(ctx context.Context, t *config.Target, inst *target.Instance) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
	defer cancel()
	if err := inst.Stop(sctx); err != nil {
		o.report(t.Name + ": stopping: " + err.Error())
	}
}

func (o Options) exec() target.Exec {
	if o.Exec != nil {
		return o.Exec
	}
	return target.System()
}

func (o Options) report(msg string) {
	if o.Progress != nil {
		o.Progress(msg)
	}
}

func portOf(addr string) (int, error) {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return none, err
	}
	return strconv.Atoi(p)
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
