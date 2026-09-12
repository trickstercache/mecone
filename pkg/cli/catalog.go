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

package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
	"github.com/trickstercache/mecone/pkg/orchestrate"
	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/results"
)

const none = 0

// load reads the -config file, if any, and fills in every setting the command line left alone.
func (c *clientFlags) load(flags *flag.FlagSet) (*config.Config, error) {
	if c.config == unset {
		return nil, nil
	}
	cfg, err := config.Load(c.config)
	if err != nil {
		return nil, err
	}
	c.apply(cfg, setFlags(flags))
	return cfg, nil
}

func setFlags(flags *flag.FlagSet) map[string]bool {
	set := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

// apply copies the file's run settings into the flags the user did not give, so a flag always wins.
func (c *clientFlags) apply(cfg *config.Config, set map[string]bool) {
	c.notice = cfg.Notice
	r := cfg.Run
	c.applyStrings(r, set)
	c.applyScalars(r, set)
	if !set[flagSuitesDir] && r.SuitesDir != unset {
		c.suitesDir = cfg.Path(r.SuitesDir)
	}
}

func (c *clientFlags) applyScalars(r config.Run, set map[string]bool) {
	if !set[flagConcurrency] && r.Concurrency > none {
		c.concurrency = r.Concurrency
	}
	if !set[flagTimeout] && r.Timeout > none {
		c.timeout = time.Duration(r.Timeout)
	}
	if !set[flagInsecure] && r.Insecure {
		c.insecure = true
	}
}

func (c *clientFlags) applyStrings(r config.Run, set map[string]bool) {
	if !set[flagProto] && len(r.Protocols) > none {
		c.protos = joinProtocols(r.Protocols)
	}
	if !set[flagSuites] && len(r.Suites) > none {
		c.suites = strings.Join(r.Suites, listSep)
	}
	c.applySelectors(r, set)
}

func (c *clientFlags) applySelectors(r config.Run, set map[string]bool) {
	if !set[flagTests] && len(r.Tests) > none {
		c.tests = strings.Join(r.Tests, listSep)
	}
	if !set[flagLevels] && len(r.Levels) > none {
		c.levels = joinLevels(r.Levels)
	}
}

func joinProtocols(protos []proto.Proto) string {
	out := make([]string, len(protos))
	for i, p := range protos {
		out[i] = string(p)
	}
	return strings.Join(out, listSep)
}

func joinLevels[T ~string](levels []T) string {
	out := make([]string, len(levels))
	for i, l := range levels {
		out[i] = string(l)
	}
	return strings.Join(out, listSep)
}

func hasTargets(cfg *config.Config) bool {
	return cfg != nil && len(cfg.Targets) > none
}

type progressWriter struct {
	w  io.Writer
	mu sync.Mutex
}

func (p *progressWriter) report(msg string) {
	// the orchestrator reports from one goroutine per target, so these writes have to be serialized
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = write(p.w, []byte("mecone: "+msg+"\n"))
}

// execCatalog tests every proxy target in the catalog, each with its own origin, and writes one
// report covering them all.
func execCatalog(ctx context.Context, cf *clientFlags, cfg *config.Config, e *env) error {
	if err := checkFormat(cf.format); err != nil {
		return err
	}
	tests, err := cf.selectTests()
	if err != nil {
		return err
	}
	protos, err := proto.ParseList(cf.protos)
	if err != nil {
		return err
	}
	progress := &progressWriter{w: e.stderr}
	rep, err := orchestrate.Run(ctx, orchestrate.Options{
		Config:      cfg,
		Tests:       tests,
		Protocols:   protos,
		Concurrency: cf.concurrency,
		Timeout:     cf.timeout,
		Insecure:    cf.insecure,
		Progress:    progress.report,
	})
	if err != nil {
		return err
	}
	// a blank line separates the progress lines on stderr from the results, on a shared terminal
	if err := write(e.stdout, []byte{'\n'}); err != nil {
		return err
	}
	if err := cf.output(e.stdout, rep); err != nil {
		return err
	}
	return catalogError(rep.Runs)
}

// catalogError reports targets that never ran, so a lifecycle failure is not mistaken for a pass.
func catalogError(runs []*results.Run) error {
	var failed []string
	for _, run := range runs {
		if run.Error != unset {
			failed = append(failed, run.Name+" ("+run.Error+")")
		}
	}
	if len(failed) == none {
		return nil
	}
	return fmt.Errorf("%d of %d targets could not be tested: %s", len(failed), len(runs), strings.Join(failed, "; "))
}
