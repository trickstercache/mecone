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
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/trickstercache/mecone/pkg/results"
)

const (
	argConfig = "-config"
	argCSV    = "-csv"
	argProto  = "-proto"
	argMD     = "-md"

	catalogName  = "catalog.yaml"
	staticTarget = "static"
	reportJSON   = "report.json"
	reportMD     = "report.md"

	markdownHeading   = "# Mecone results"
	suiteTableHeading = "| Suite | Score |"

	firstRun    = 0
	secondRun   = 1
	firstResult = 0
	oneRun      = 1
	twoRuns     = 2
	oneResult   = 1

	headerRow    = 0
	firstDataRow = 1
	startedCol   = 0
	targetCol    = 1
	minCSVRows   = 2

	// the endpoint answers everything, so readiness passes and the tests run and fail honestly
	catalogTemplate = `
run:
  levels: [must]
  concurrency: 2
targets:
  - name: static
    proxy:
      url: %s
    readiness:
      status: [200]
      timeout: 5s
      interval: 10ms
`
	brokenTemplate = `
targets:
  - name: static
    proxy:
      url: %s
    readiness: {status: [200], timeout: 5s, interval: 10ms}
  - name: broken
    process:
      start: "echo nope; exit 7"
    readiness: {timeout: 100ms, interval: 10ms}
`
)

func staticEndpoint(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts.URL
}

func writeCatalog(t *testing.T, template, url string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, catalogName)
	if err := os.WriteFile(path, fmt.Appendf(nil, template, url), testFilePerm); err != nil {
		t.Fatal(err)
	}
	return path
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func mustReadReport(t *testing.T, path string) *results.Report {
	t.Helper()
	rep, err := results.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestRunCatalog(t *testing.T) {
	catalog := writeCatalog(t, catalogTemplate, staticEndpoint(t))
	tmp := t.TempDir()
	jsonPath, csvPath := filepath.Join(tmp, reportJSON), filepath.Join(tmp, "report.csv")
	mdPath := filepath.Join(tmp, reportMD)
	out := expectCode(t.Context(), t, exitOK, cmdRun, argConfig, catalog, argSuitesDir, suitesDir(t),
		argJSON, jsonPath, argCSV, csvPath, argMD, mdPath)
	if !strings.Contains(out, staticTarget) {
		t.Errorf("report lacks the target name:\n%s", out)
	}
	checkCatalogReport(t, jsonPath)
	checkCatalogCSV(t, csvPath)
	checkWrittenReport(t, mdPath)
}

func checkWrittenReport(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	checkOutput(t, string(b), []string{markdownHeading, suiteTableHeading}, nil)
}

func checkCatalogReport(t *testing.T, path string) {
	t.Helper()
	rep := mustReadReport(t, path)
	if len(rep.Runs) != oneRun || rep.Runs[firstRun].Name != staticTarget || rep.Runs[firstRun].Error != unset {
		t.Fatalf("runs = %+v", rep.Runs)
	}
	// the config selects MUST tests only, so the MAY test in the suite must not have run
	got := rep.Runs[firstRun].Results
	if len(got) != oneResult || got[firstResult].ID != testGet {
		t.Errorf("results = %+v", got)
	}
}

func checkCatalogCSV(t *testing.T, path string) {
	t.Helper()
	rows := readCSV(t, path)
	if len(rows) < minCSVRows || rows[headerRow][startedCol] != "run_started" || rows[headerRow][targetCol] != "target" {
		t.Fatalf("csv header = %v", rows[headerRow])
	}
	if rows[firstDataRow][targetCol] != staticTarget {
		t.Errorf("csv target column = %q", rows[firstDataRow][targetCol])
	}
}

func TestRunCatalogFlagsOverrideConfig(t *testing.T) {
	catalog := writeCatalog(t, catalogTemplate, staticEndpoint(t))
	jsonPath := filepath.Join(t.TempDir(), reportJSON)
	expectCode(t.Context(), t, exitOK, cmdRun, argConfig, catalog, argSuitesDir, suitesDir(t),
		argLevels, "may", argJSON, jsonPath)
	rep := mustReadReport(t, jsonPath)
	got := rep.Runs[firstRun].Results
	if len(got) != oneResult || got[firstResult].ID != testReuse {
		t.Errorf("a -levels flag must override the file: %+v", got)
	}
}

func TestRunCatalogTargetFailure(t *testing.T) {
	catalog := writeCatalog(t, brokenTemplate, staticEndpoint(t))
	jsonPath := filepath.Join(t.TempDir(), reportJSON)
	out := expectCode(t.Context(), t, exitFailure, cmdRun, argConfig, catalog, argSuitesDir, suitesDir(t), argJSON, jsonPath)
	if !strings.Contains(out, "broken") {
		t.Errorf("output does not name the failed target:\n%s", out)
	}
	// a catalog of more than one target leads with every target's overall score
	checkOutput(t, out, []string{"Target", "not tested"}, nil)
	rep := mustReadReport(t, jsonPath)
	if len(rep.Runs) != twoRuns {
		t.Fatalf("runs = %+v", rep.Runs)
	}
	// a target that never starts must not hide the one that did
	if rep.Runs[firstRun].Error != unset || rep.Runs[secondRun].Error == unset {
		t.Errorf("errors = %q, %q", rep.Runs[firstRun].Error, rep.Runs[secondRun].Error)
	}
}

func TestCatalogViaClientCommand(t *testing.T) {
	catalog := writeCatalog(t, catalogTemplate, staticEndpoint(t))
	out := expectCode(t.Context(), t, exitOK, cmdClient, argConfig, catalog, argSuitesDir, suitesDir(t))
	if !strings.Contains(out, staticTarget) {
		t.Errorf("client did not test the catalog:\n%s", out)
	}
}

func TestCatalogErrors(t *testing.T) {
	dir := suitesDir(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	catalog := writeCatalog(t, catalogTemplate, staticEndpoint(t))
	cases := []cliCase{
		{name: "missing config", args: []string{cmdRun, argConfig, missing}, code: exitFailure},
		{name: "client missing config", args: []string{cmdClient, argConfig, missing}, code: exitFailure},
		{name: "bad protocol", args: []string{cmdRun, argConfig, catalog, argSuitesDir, dir, argProto, "h9"}, code: exitFailure},
		{name: "no tests match", args: []string{cmdRun, argConfig, catalog, argSuitesDir, dir, argTests, "nothing-*"}, code: exitFailure},
		{name: "bad format", args: []string{cmdRun, argConfig, catalog, argSuitesDir, dir, argFormat, formatXML}, code: exitFailure},
	}
	runCases(t.Context(), t, cases)
}

func TestReportCSVOutput(t *testing.T) {
	catalog := writeCatalog(t, catalogTemplate, staticEndpoint(t))
	tmp := t.TempDir()
	jsonPath, csvPath := filepath.Join(tmp, reportJSON), filepath.Join(tmp, "from-report.csv")
	expectCode(t.Context(), t, exitOK, cmdRun, argConfig, catalog, argSuitesDir, suitesDir(t), argJSON, jsonPath)
	mdPath := filepath.Join(tmp, reportMD)
	expectCode(t.Context(), t, exitOK, cmdReport, argCSV, csvPath, argMD, mdPath, jsonPath)
	if rows := readCSV(t, csvPath); len(rows) < minCSVRows {
		t.Errorf("csv has %d rows", len(rows))
	}
	checkWrittenReport(t, mdPath)
	// every target is held to the bar separately, and this one fails its MUST test
	expectCode(t.Context(), t, exitFailure, cmdReport, flagMinMust, "100", jsonPath)
}

func TestWriteFileFailures(t *testing.T) {
	rep := &results.Report{Runs: []*results.Run{{Name: staticTarget}}}
	missingDir := filepath.Join(t.TempDir(), "no")
	if err := writeFiles(csvOutput(filepath.Join(missingDir, "dir.csv"), rep)); err == nil {
		t.Error("writing a CSV into a missing directory reported success")
	}
	if err := writeFiles(markdownOutput(filepath.Join(missingDir, "dir.md"), rep, nil)); err == nil {
		t.Error("writing a report into a missing directory reported success")
	}
	if err := writeFiles(csvOutput(unset, rep)); err != nil {
		t.Errorf("an unnamed file was written anyway: %v", err)
	}
}
