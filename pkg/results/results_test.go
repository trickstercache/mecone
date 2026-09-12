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

package results

import (
	"bytes"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	toolName   = "mecone"
	targetName = "nginx"
	targetURL  = "http://proxy:8080"
	message    = "step 2: nope"
	ttfb       = 1.25
	total      = 4.5
	bodyBytes  = 512
	firstStep  = 1
	statusOK   = 200
	testRef    = testdef.Ref("RFC9111#4")

	first       = 0
	wantRuns    = 1
	wantResults = 1
	wantSteps   = 1
)

func newTestReport() *Report {
	return &Report{
		Tool:    toolName,
		Version: "test",
		Runs: []*Run{{
			Name:      targetName,
			Target:    targetURL,
			Protocols: []string{"h1"},
			Results: []Result{{
				ID:      "a",
				Suite:   "caching",
				Level:   "must",
				Refs:    []testdef.Ref{testRef},
				Outcome: Fail,

				Messages: []string{message},
				Steps: []Step{{
					Step:      firstStep,
					Method:    "GET",
					Status:    statusOK,
					Forwarded: true,
					TTFBMS:    ttfb,
					TotalMS:   total,
					Bytes:     bodyBytes,
				}},
			}},
		}},
	}
}

func TestResultHelpers(t *testing.T) {
	r := Result{ID: "a", ClientProto: "h2", Outcome: Pass}
	if r.Key() != "a [h2]" {
		t.Errorf("Key = %q", r.Key())
	}
	s := r.With(Skipped, "why")
	if s.Outcome != Skipped || s.Messages[first] != "why" || r.Outcome != Pass {
		t.Errorf("With = %+v, original %+v", s, r)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	want := newTestReport()
	if err := want.WriteFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tool != want.Tool || len(got.Runs) != wantRuns {
		t.Fatalf("report = %+v", got)
	}
	checkRun(t, got.Runs[first])
}

func checkRun(t *testing.T, run *Run) {
	t.Helper()
	if run.Name != targetName || run.Target != targetURL || len(run.Results) != wantResults {
		t.Fatalf("run = %+v", run)
	}
	checkResult(t, run.Results[first])
}

func checkResult(t *testing.T, res Result) {
	t.Helper()
	if res.Messages[first] != message || len(res.Steps) != wantSteps ||
		!slices.Equal(res.Refs, []testdef.Ref{testRef}) {
		t.Fatalf("result = %+v", res)
	}
	if step := res.Steps[first]; step.TTFBMS != ttfb || step.TotalMS != total || step.Bytes != bodyBytes || !step.Forwarded {
		t.Errorf("step = %+v", step)
	}
}

func TestWrite(t *testing.T) {
	var buf bytes.Buffer
	if err := newTestReport().Write(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"outcome": "fail"`, `"name": "nginx"`, `"refs": [`,
		`"RFC9111#4"`, `"ttfb_ms": 1.25`,
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("JSON lacks %q:\n%s", want, buf.String())
		}
	}
}

func TestWriteYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := newTestReport().WriteYAML(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"outcome: fail", "name: nginx", "RFC9111#4", "ttfb_ms: 1.25",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("YAML lacks %q:\n%s", want, buf.String())
		}
	}
	path := filepath.Join(t.TempDir(), "results.yaml")
	if err := newTestReport().WriteYAMLFile(path); err != nil {
		t.Fatal(err)
	}
	if err := newTestReport().WriteYAMLFile(filepath.Join(t.TempDir(), "no", "such", "dir.yaml")); err == nil {
		t.Error("WriteYAMLFile wrote into a missing directory")
	}
}

func TestReadWriteErrors(t *testing.T) {
	if _, err := Read(strings.NewReader("{bad")); err == nil {
		t.Error("Read accepted bad JSON")
	}
	if _, err := ReadFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("ReadFile accepted a missing file")
	}
	if err := newTestReport().WriteFile(filepath.Join(t.TempDir(), "no", "such", "dir.json")); err == nil {
		t.Error("WriteFile wrote into a missing directory")
	}
}
