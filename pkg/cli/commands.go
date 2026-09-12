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
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/trickstercache/mecone/pkg/appinfo"
	"github.com/trickstercache/mecone/pkg/client"
	"github.com/trickstercache/mecone/pkg/corpus"
	"github.com/trickstercache/mecone/pkg/origin"
	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/report"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
	"github.com/trickstercache/mecone/suites"
)

const (
	formatText     = "text"
	formatMarkdown = "md"
	formatJSON     = "json"
)

const (
	unset              = ""
	listSep            = ","
	defaultConcurrency = 8
	defaultTimeout     = 30 * time.Second
	noMinMust          = 0.0
	filePerm           = 0o600
)

const (
	tabMinWidth = 0
	tabWidth    = 4
	tabPadding  = 2
	tabPadChar  = ' '
	tabFlags    = 0
)

const (
	resultsFileArg = iota
	reportArgCount
)

const (
	flagConfig      = "config"
	flagConcurrency = "concurrency"
	flagCSV         = "csv"
	flagFormat      = "format"
	flagInsecure    = "insecure"
	flagJSON        = "json"
	flagLevels      = "levels"
	flagMarkdown    = "md"
	flagProto       = "proto"
	flagSuites      = "suites"
	flagSuitesDir   = "suites-dir"
	flagTests       = "tests"
	flagTimeout     = "timeout"
	flagYAML        = "yaml"
)

const (
	listHeader  = "ID\tLEVEL\tSUITE\tTITLE\n"
	reportUsage = "usage: mecone report [flags] results.json\n"
)

type selectFlags struct {
	suites    string
	tests     string
	levels    string
	suitesDir string
}

func (s *selectFlags) register(flags *flag.FlagSet) {
	flags.StringVar(&s.suites, flagSuites, unset, "comma-separated suite IDs; empty selects every suite")
	flags.StringVar(&s.tests, flagTests, unset, "comma-separated test IDs or glob patterns, such as 'storage-*'")
	flags.StringVar(&s.levels, flagLevels, unset, "comma-separated requirement levels: must, should, may, info")
	flags.StringVar(&s.suitesDir, flagSuitesDir, unset, "load suites from this directory instead of the built-in set")
}

func (s *selectFlags) selectTests() ([]*testdef.Test, error) {
	// the result includes the tests that the selected tests require
	var fsys fs.FS = suites.FS
	if s.suitesDir != unset {
		fsys = os.DirFS(s.suitesDir)
	}
	c, err := corpus.Load(fsys)
	if err != nil {
		return nil, err
	}
	f := corpus.Filter{Suites: splitList(s.suites), IDs: splitList(s.tests)}
	for _, l := range splitList(s.levels) {
		lv := testdef.Level(l)
		if !slices.Contains(testdef.Levels, lv) {
			return nil, fmt.Errorf("unknown level %q (want must, should, may or info)", l)
		}
		f.Levels = append(f.Levels, lv)
	}
	return c.Select(f)
}

func splitList(s string) []string {
	var out []string
	for part := range strings.SplitSeq(s, listSep) {
		if p := strings.TrimSpace(part); p != unset {
			out = append(out, p)
		}
	}
	return out
}

type clientFlags struct {
	selectFlags
	config      string
	target      string
	control     string
	protos      string
	jsonPath    string
	yamlPath    string
	csvPath     string
	mdPath      string
	format      string
	notice      string
	concurrency int
	timeout     time.Duration
	insecure    bool
}

func (c *clientFlags) register(flags *flag.FlagSet) {
	c.selectFlags.register(flags)
	flags.StringVar(&c.config, flagConfig, unset, "configuration file supplying defaults and, optionally, a catalog of proxy targets")
	flags.StringVar(&c.target, "target", unset, "base URL of the proxy under test, such as http://proxy:8080")
	flags.StringVar(&c.control, "control", unset, "origin control-plane URL, when reachable directly; defaults to -target (through the proxy)")
	flags.StringVar(&c.protos, flagProto, "h1", "comma-separated client protocols: h1, h2, h2c")
	flags.StringVar(&c.jsonPath, flagJSON, unset, "write the full results to this JSON file")
	flags.StringVar(&c.yamlPath, flagYAML, unset, "write the full results to this YAML file")
	flags.StringVar(&c.csvPath, flagCSV, unset, "write one row per request, with timings, to this CSV file")
	flags.StringVar(&c.mdPath, flagMarkdown, unset, "write the Markdown report to this file")
	flags.StringVar(&c.format, flagFormat, formatText, "summary written to stdout: text, md or json")
	flags.IntVar(&c.concurrency, flagConcurrency, defaultConcurrency, "number of tests to run at once")
	flags.DurationVar(&c.timeout, flagTimeout, defaultTimeout, "timeout for each request")
	flags.BoolVar(&c.insecure, flagInsecure, false, "skip TLS certificate verification of the proxy under test")
}

func (c *clientFlags) runner(target, control string) (*client.Runner, error) {
	protos, err := proto.ParseList(c.protos)
	if err != nil {
		return nil, err
	}
	return client.New(client.Options{
		Target:        target,
		OriginControl: control,
		Protocols:     protos,
		Concurrency:   c.concurrency,
		Timeout:       c.timeout,
		Insecure:      c.insecure,
	})
}

func (c *clientFlags) output(w io.Writer, rep *results.Report) error {
	if err := writeFiles(
		outputFile{c.jsonPath, rep.Write},
		outputFile{c.yamlPath, rep.WriteYAML},
		csvOutput(c.csvPath, rep),
		markdownOutput(c.mdPath, rep, nil),
	); err != nil {
		return err
	}
	return render(w, c.format, rep, nil)
}

// outputFile is one optional file the command writes; an empty path means it was not asked for.
type outputFile struct {
	path string
	emit func(io.Writer) error
}

func csvOutput(path string, rep *results.Report) outputFile {
	return outputFile{path, func(w io.Writer) error { return report.CSV(w, rep) }}
}

// markdownOutput always writes Markdown, whatever -format the summary on stdout uses, so the
// flag name and the file extension agree.
func markdownOutput(path string, rep, baseline *results.Report) outputFile {
	return outputFile{path, func(w io.Writer) error { return report.Markdown(w, rep, baseline) }}
}

func writeFiles(files ...outputFile) error {
	for _, f := range files {
		if f.path == unset {
			continue
		}
		if err := writeToFile(f.path, f.emit); err != nil {
			return err
		}
	}
	return nil
}

func writeToFile(path string, emit func(io.Writer) error) (err error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return emit(f)
}

type originFlags struct {
	listen    string
	tlsListen string
	tlsCert   string
	tlsKey    string
}

func (o *originFlags) register(flags *flag.FlagSet, defaultListen string) {
	flags.StringVar(&o.listen, "listen", defaultListen, "cleartext listen address, serving HTTP/1.1 and h2c; empty disables")
	flags.StringVar(&o.tlsListen, "tls-listen", unset, "TLS listen address, serving HTTP/1.1 and HTTP/2")
	flags.StringVar(&o.tlsCert, "tls-cert", unset, "TLS certificate file (PEM)")
	flags.StringVar(&o.tlsKey, "tls-key", unset, "TLS private key file (PEM)")
}

func (o *originFlags) options() origin.Options {
	return origin.Options{Listen: o.listen, TLSListen: o.tlsListen, TLSCert: o.tlsCert, TLSKey: o.tlsKey}
}

type reportFlags struct {
	baseline string
	format   string
	csvPath  string
	mdPath   string
	minMust  float64
}

func (r *reportFlags) register(flags *flag.FlagSet) {
	flags.StringVar(&r.baseline, "baseline", unset, "an earlier results file to compare against")
	flags.StringVar(&r.format, flagFormat, formatMarkdown, "output written to stdout: md, json or text")
	flags.StringVar(&r.csvPath, flagCSV, unset, "also write one row per request, with timings, to this CSV file")
	flags.StringVar(&r.mdPath, flagMarkdown, unset, "also write the Markdown report to this file")
	flags.Float64Var(&r.minMust, "min-must", noMinMust, "fail when a target's MUST pass rate, in percent, is below this value")
}

func runOrigin(ctx context.Context, args []string, e *env) error {
	flags := newFlagSet("origin", e)
	var of originFlags
	of.register(flags, ":8000")
	if err := parse(flags, args); err != nil {
		return err
	}
	srv := origin.New(of.options())
	if err := srv.Listen(); err != nil {
		return err
	}
	if err := write(e.stdout, listening(srv)); err != nil {
		return errors.Join(err, srv.Close())
	}
	return srv.Serve(ctx)
}

func listening(srv *origin.Server) []byte {
	var b []byte
	if a := srv.Addr(); a != unset {
		b = fmt.Appendf(b, "mecone origin listening on http://%s (HTTP/1.1, h2c)\n", a)
	}
	if a := srv.TLSAddr(); a != unset {
		b = fmt.Appendf(b, "mecone origin listening on https://%s (HTTP/1.1, HTTP/2)\n", a)
	}
	return b
}

func runClient(ctx context.Context, args []string, e *env) error {
	flags := newFlagSet("client", e)
	var cf clientFlags
	cf.register(flags)
	if err := parse(flags, args); err != nil {
		return err
	}
	cfg, err := cf.load(flags)
	if err != nil {
		return err
	}
	if hasTargets(cfg) {
		return execCatalog(ctx, &cf, cfg, e)
	}
	if cf.target == unset {
		return errors.New("-target is required, or name proxy targets in a -config file")
	}
	return execClient(ctx, &cf, cf.target, cf.control, e)
}

// runBoth serves an in-process origin and runs the client against -target, or against the origin
// itself when no target is given. A configuration file with a catalog takes over instead, giving
// each proxy target its own origin.
func runBoth(ctx context.Context, args []string, e *env) error {
	flags := newFlagSet("run", e)
	var of originFlags
	of.register(flags, "127.0.0.1:0")
	var cf clientFlags
	cf.register(flags)
	if err := parse(flags, args); err != nil {
		return err
	}
	cfg, err := cf.load(flags)
	if err != nil {
		return err
	}
	if hasTargets(cfg) {
		return execCatalog(ctx, &cf, cfg, e)
	}
	if of.listen == unset {
		return errors.New("run needs a cleartext -listen address for its control plane")
	}
	if err := checkFormat(cf.format); err != nil {
		return err
	}
	return serveAndTest(ctx, &cf, of, e)
}

func serveAndTest(ctx context.Context, cf *clientFlags, of originFlags, e *env) error {
	srv := origin.New(of.options())
	if err := srv.Listen(); err != nil {
		return err
	}
	// control traffic always goes straight to the in-process origin, which is also the target when none is given
	control := "http://" + srv.Addr()
	target := cf.target
	if target == unset {
		target = control
	}
	octx, stop := context.WithCancel(ctx)
	var serveErr error
	var wg sync.WaitGroup
	wg.Go(func() { serveErr = srv.Serve(octx) })
	err := execClient(ctx, cf, target, control, e)
	stop()
	wg.Wait()
	return errors.Join(err, serveErr)
}

func execClient(ctx context.Context, cf *clientFlags, target, control string, e *env) error {
	if err := checkFormat(cf.format); err != nil {
		return err
	}
	tests, err := cf.selectTests()
	if err != nil {
		return err
	}
	r, err := cf.runner(target, control)
	if err != nil {
		return err
	}
	started := time.Now().UTC()
	run, err := r.Run(ctx, tests)
	if err != nil {
		return err
	}
	rep := &results.Report{
		Tool:     appinfo.Name,
		Version:  appinfo.Version,
		Started:  started,
		Finished: time.Now().UTC(),
		Notice:   cf.notice,
		Runs:     []*results.Run{run},
	}
	return cf.output(e.stdout, rep)
}

func checkFormat(format string) error {
	switch format {
	case formatText, formatMarkdown, formatJSON:
		return nil
	default:
		return fmt.Errorf("unknown format %q (want text, md or json)", format)
	}
}

func render(w io.Writer, format string, rep, baseline *results.Report) error {
	switch format {
	case formatJSON:
		return report.JSON(w, rep, baseline)
	case formatMarkdown:
		return report.Markdown(w, rep, baseline)
	default:
		return report.Text(w, rep, baseline)
	}
}

func runList(_ context.Context, args []string, e *env) error {
	flags := newFlagSet("list", e)
	var sf selectFlags
	sf.register(flags)
	if err := parse(flags, args); err != nil {
		return err
	}
	tests, err := sf.selectTests()
	if err != nil {
		return err
	}
	b := []byte(listHeader)
	for _, t := range tests {
		b = fmt.Appendf(b, "%s\t%s\t%s\t%s\n", t.ID, t.Level, t.Suite, t.Title)
	}
	tw := tabwriter.NewWriter(e.stdout, tabMinWidth, tabWidth, tabPadding, tabPadChar, tabFlags)
	if _, err := tw.Write(b); err != nil {
		return err
	}
	return tw.Flush()
}

func runReport(_ context.Context, args []string, e *env) error {
	flags := newFlagSet("report", e)
	var rf reportFlags
	rf.register(flags)
	var usageErr error
	flags.Usage = func() { usageErr = printUsage(flags, reportUsage) }
	if err := parse(flags, args); err != nil {
		return firstError(usageErr, err)
	}
	if flags.NArg() != reportArgCount {
		flags.Usage()
		return firstError(usageErr, errUsage)
	}
	return execReport(e.stdout, &rf, flags.Arg(resultsFileArg))
}

func printUsage(flags *flag.FlagSet, line string) error {
	if err := write(flags.Output(), []byte(line)); err != nil {
		return fmt.Errorf("writing usage: %w", err)
	}
	flags.PrintDefaults()
	return nil
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func execReport(w io.Writer, rf *reportFlags, path string) error {
	if err := checkFormat(rf.format); err != nil {
		return err
	}
	rep, err := results.ReadFile(path)
	if err != nil {
		return err
	}
	base, err := readBaseline(rf.baseline)
	if err != nil {
		return err
	}
	if err := writeFiles(csvOutput(rf.csvPath, rep), markdownOutput(rf.mdPath, rep, base)); err != nil {
		return err
	}
	if err := render(w, rf.format, rep, base); err != nil {
		return err
	}
	return checkMinMust(rep, rf.minMust)
}

func readBaseline(path string) (*results.Report, error) {
	if path == unset {
		return nil, nil
	}
	return results.ReadFile(path)
}

// checkMinMust holds every target to the same bar, so one passing proxy cannot hide a failing one.
func checkMinMust(rep *results.Report, minMust float64) error {
	if minMust <= noMinMust {
		return nil
	}
	for _, run := range rep.Runs {
		if run.Error != unset {
			return fmt.Errorf("%s was not tested: %s", targetName(run), run.Error)
		}
		overall, _ := report.Summarize(run)
		if p := overall.Must.Percent(); p < minMust {
			return fmt.Errorf("%s: MUST pass rate %.1f%% is below the required %.1f%%", targetName(run), p, minMust)
		}
	}
	return nil
}

func targetName(run *results.Run) string {
	if run.Name != unset {
		return run.Name
	}
	return run.Target
}

func runVersion(_ context.Context, args []string, e *env) error {
	flags := newFlagSet("version", e)
	if err := parse(flags, args); err != nil {
		return err
	}
	return write(e.stdout, []byte(appinfo.String()+"\n"))
}
