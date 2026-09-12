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
	"bytes"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trickstercache/mecone/pkg/origin"
)

const (
	demoSuite = "id: demo\ntitle: Demo\n"
	demoGroup = `group: basics
title: Basics
refs: [RFC9110#9.3.1]
tests:
  - id: basics-get
    title: A GET reaches the origin
    level: must
    responses:
      ok: {body: hello}
    steps:
      - expect: {status: 200, body: hello, forwarded: true}
  - id: basics-reuse
    title: A fresh response is reused
    level: may
    responses:
      fresh:
        headers: ["Cache-Control: max-age=60"]
    steps:
      - arrange: true
      - expect: {forwarded: false}
`
)

const (
	dirPerm      = 0o750
	testFilePerm = 0o600

	cmdClient  = "client"
	cmdHelp    = "help"
	cmdList    = "list"
	cmdOrigin  = "origin"
	cmdReport  = "report"
	cmdRun     = "run"
	cmdVersion = "version"

	flagBaseline = "-baseline"
	flagBogus    = "-bogus"
	argFormat    = "-format"
	argLevels    = "-levels"
	flagListen   = "-listen"
	flagMinMust  = "-min-must"
	argJSON      = "-json"
	argYAML      = "-yaml"
	argSuitesDir = "-suites-dir"
	flagTarget   = "-target"
	argTests     = "-tests"

	testGet   = "basics-get"
	testReuse = "basics-reuse"
	formatXML = "xml"

	anyLoopbackPort = "127.0.0.1:0"
	noAddr          = ""
)

var errWrite = errors.New("write failed")

type failWriter struct{}

func (failWriter) Write([]byte) (n int, err error) {
	return n, errWrite
}

type cliCase struct {
	name   string
	args   []string
	code   int
	want   []string
	absent []string
}

type writeCase struct {
	name   string
	args   []string
	stdout io.Writer
	stderr io.Writer
	code   int
}

func suitesDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "demo"), dirPerm); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"suite.yaml": demoSuite, "basics.yaml": demoGroup} {
		if err := os.WriteFile(filepath.Join(dir, "demo", name), []byte(data), testFilePerm); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func invoke(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	var outBuf, errBuf bytes.Buffer
	code = Run(ctx, args, &outBuf, &errBuf)
	return code, outBuf.String(), errBuf.String()
}

func expectCode(ctx context.Context, t *testing.T, want int, args ...string) string {
	t.Helper()
	code, stdout, stderr := invoke(ctx, args...)
	if code != want {
		t.Fatalf("mecone %v exited %d, want %d\nstdout: %s\nstderr: %s", args, code, want, stdout, stderr)
	}
	return stdout + stderr
}

func checkOutput(t *testing.T, out string, want, absent []string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(out, s) {
			t.Errorf("output lacks %q:\n%s", s, out)
		}
	}
	for _, s := range absent {
		if strings.Contains(out, s) {
			t.Errorf("output has %q:\n%s", s, out)
		}
	}
}

func runCases(ctx context.Context, t *testing.T, cases []cliCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkOutput(t, expectCode(ctx, t, tc.code, tc.args...), tc.want, tc.absent)
		})
	}
}

func TestUsageAndVersion(t *testing.T) {
	runCases(t.Context(), t, []cliCase{
		{name: "no command", code: exitUsage, want: []string{"usage: mecone"}},
		{name: "help command", args: []string{cmdHelp}, code: exitOK, want: []string{"commands:"}},
		{name: "unknown command", args: []string{"bogus"}, code: exitUsage, want: []string{"unknown command"}},
		{name: "version", args: []string{cmdVersion}, code: exitOK, want: []string{"mecone"}},
		{name: "version help", args: []string{cmdVersion, "-h"}, code: exitOK},
		{name: "version bad flag", args: []string{cmdVersion, "-nope"}, code: exitUsage},
	})
}

func TestList(t *testing.T) {
	dir := suitesDir(t)
	runCases(t.Context(), t, []cliCase{
		{
			name: "must only", args: []string{cmdList, argSuitesDir, dir, argLevels, "must"}, code: exitOK,
			want: []string{testGet}, absent: []string{testReuse},
		},
		{name: "built-in", args: []string{cmdList}, code: exitOK, want: []string{"storage-no-store"}},
		{name: "unknown level", args: []string{cmdList, argLevels, "sometimes"}, code: exitFailure},
		{name: "missing suites dir", args: []string{cmdList, argSuitesDir, filepath.Join(dir, "missing")}, code: exitFailure},
	})
}

func TestRunAndReport(t *testing.T) {
	dir := suitesDir(t)
	tmp := t.TempDir()
	all, mayOnly := filepath.Join(tmp, "all.json"), filepath.Join(tmp, "may.json")
	allYAML := filepath.Join(tmp, "all.yaml")
	missing, badOut := filepath.Join(tmp, "missing.json"), filepath.Join(tmp, "no", "dir.json")
	runCases(t.Context(), t, []cliCase{
		{name: "run json", args: []string{cmdRun, argSuitesDir, dir, argJSON, all, argFormat, formatJSON}, code: exitOK, want: []string{`"overall"`}},
		{name: "run yaml", args: []string{cmdRun, argSuitesDir, dir, argYAML, allYAML}, code: exitOK},
		{
			name: "report baseline", args: []string{cmdReport, flagBaseline, all, all}, code: exitOK,
			want: []string{"# Mecone results", "No outcomes changed."},
		},
		{name: "report min-must met", args: []string{cmdReport, argFormat, formatJSON, flagMinMust, "100", all}, code: exitOK},
		{name: "run may only", args: []string{cmdRun, argSuitesDir, dir, argTests, testReuse, argJSON, mayOnly}, code: exitOK},
		{name: "report min-must missed", args: []string{cmdReport, flagMinMust, "50", mayOnly}, code: exitFailure},
		{name: "report without file", args: []string{cmdReport}, code: exitUsage},
		{name: "report bad format", args: []string{cmdReport, argFormat, formatXML, all}, code: exitFailure},
		{name: "report missing file", args: []string{cmdReport, missing}, code: exitFailure},
		{name: "report missing baseline", args: []string{cmdReport, flagBaseline, missing, all}, code: exitFailure},
		{name: "run bad format", args: []string{cmdRun, argFormat, formatXML, argSuitesDir, dir}, code: exitFailure},
		{name: "run without listen", args: []string{cmdRun, flagListen, noAddr, argSuitesDir, dir}, code: exitFailure},
		{name: "run bad listen", args: []string{cmdRun, flagListen, "127.0.0.1:-1", argSuitesDir, dir}, code: exitFailure},
		{name: "run bad flag", args: []string{cmdRun, flagBogus}, code: exitUsage},
		{name: "run bad proto", args: []string{cmdRun, argSuitesDir, dir, "-proto", "h9"}, code: exitFailure},
		{name: "run no tests", args: []string{cmdRun, argSuitesDir, dir, argTests, "nothing-*"}, code: exitFailure},
		{name: "run bad out", args: []string{cmdRun, argSuitesDir, dir, argJSON, badOut}, code: exitFailure},
	})
	body, err := os.ReadFile(filepath.Clean(allYAML))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "outcome:") {
		t.Errorf("YAML results lack outcomes:\n%s", body)
	}
}

func TestClientAgainstOrigin(t *testing.T) {
	ts := httptest.NewServer(origin.New(origin.Options{}).Handler())
	defer ts.Close()
	dir := suitesDir(t)
	runCases(t.Context(), t, []cliCase{
		{
			name: "through origin", args: []string{cmdClient, flagTarget, ts.URL, argSuitesDir, dir}, code: exitOK,
			// the terminal summary scores every suite but never lists individual findings
			want: []string{"- Client protocols: h1", "Suite", "overall"}, absent: []string{testGet, "Findings"},
		},
		{name: "no target", args: []string{cmdClient, argSuitesDir, dir}, code: exitFailure},
		{name: "bad target", args: []string{cmdClient, flagTarget, "ftp://proxy", argSuitesDir, dir}, code: exitFailure},
		{name: "bad flag", args: []string{cmdClient, flagBogus}, code: exitUsage},
	})
	ts.Close()
	runCases(t.Context(), t, []cliCase{
		{name: "origin down", args: []string{cmdClient, flagTarget, ts.URL, argSuitesDir, dir, "-timeout", "1s"}, code: exitFailure},
	})
}

func TestOriginCommand(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	runCases(ctx, t, []cliCase{
		{name: "listens", args: []string{cmdOrigin, flagListen, anyLoopbackPort}, code: exitOK, want: []string{"listening on http://127.0.0.1:"}},
		{name: "no listeners", args: []string{cmdOrigin, flagListen, noAddr}, code: exitFailure},
		{name: "no key pair", args: []string{cmdOrigin, flagListen, noAddr, "-tls-listen", anyLoopbackPort}, code: exitFailure},
		{name: "bad flag", args: []string{cmdOrigin, flagBogus}, code: exitUsage},
	})
}

func TestUnwritableOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dir := suitesDir(t)
	var sink bytes.Buffer
	cases := []writeCase{
		{name: "help output", args: []string{cmdHelp}, stdout: failWriter{}, stderr: &sink, code: exitFailure},
		{name: "no command", stdout: &sink, stderr: failWriter{}, code: exitUsage},
		{name: "version", args: []string{cmdVersion}, stdout: failWriter{}, stderr: &sink, code: exitFailure},
		{name: "list", args: []string{cmdList, argSuitesDir, dir}, stdout: failWriter{}, stderr: &sink, code: exitFailure},
		{name: "origin", args: []string{cmdOrigin, flagListen, anyLoopbackPort}, stdout: failWriter{}, stderr: &sink, code: exitFailure},
		{name: "report help", args: []string{cmdReport, "-h"}, stdout: &sink, stderr: failWriter{}, code: exitFailure},
		{name: "report without file", args: []string{cmdReport}, stdout: &sink, stderr: failWriter{}, code: exitFailure},
		{name: "error message", args: []string{cmdList, argLevels, "sometimes"}, stdout: &sink, stderr: failWriter{}, code: exitFailure},
	}
	for _, tc := range cases {
		if code := Run(ctx, tc.args, tc.stdout, tc.stderr); code != tc.code {
			t.Errorf("%s: exited %d, want %d", tc.name, code, tc.code)
		}
	}
}
