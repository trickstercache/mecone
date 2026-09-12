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

// Package results defines the machine-readable record of a Mecone run.
package results

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/trickstercache/mecone/pkg/testdef"

	"go.yaml.in/yaml/v3"
)

const filePerm = 0o600

// Outcome is the result of running one test over one client protocol.
type Outcome string

const (
	// Pass means every expectation was met.
	Pass Outcome = "pass"
	// Fail means the proxy did not behave as the test requires.
	Fail Outcome = "fail"
	// Inconclusive means an arrange step did not establish the state the test needs.
	Inconclusive Outcome = "inconclusive"
	// Skipped means the test did not apply, or a test it requires did not pass.
	Skipped Outcome = "skipped"
	// Error means the harness itself failed, so nothing was learned about the proxy.
	Error Outcome = "error"
)

// Step records what one request of a test did, including client-measured timings. Timings are
// reported only; they never affect an outcome or a score.
type Step struct {
	Step      int     `json:"step" yaml:"step"`
	Method    string  `json:"method" yaml:"method"`
	Path      string  `json:"path,omitempty" yaml:"path,omitempty"`
	Status    int     `json:"status,omitempty" yaml:"status,omitempty"`
	Forwarded bool    `json:"forwarded" yaml:"forwarded"`
	Reused    bool    `json:"conn_reused" yaml:"conn_reused"`
	TTFBMS    float64 `json:"ttfb_ms,omitempty" yaml:"ttfb_ms,omitempty"`
	TotalMS   float64 `json:"total_ms,omitempty" yaml:"total_ms,omitempty"`
	Bytes     int     `json:"bytes" yaml:"bytes"`
	Error     string  `json:"error,omitempty" yaml:"error,omitempty"`
}

// Result is the outcome of one test over one client protocol.
type Result struct {
	ID          string        `json:"id" yaml:"id"`
	Suite       string        `json:"suite" yaml:"suite"`
	Group       string        `json:"group" yaml:"group"`
	Level       string        `json:"level" yaml:"level"`
	Refs        []testdef.Ref `json:"refs" yaml:"refs"`
	ClientProto string        `json:"client_proto" yaml:"client_proto"`
	OriginProto string        `json:"origin_proto,omitempty" yaml:"origin_proto,omitempty"`
	Outcome     Outcome       `json:"outcome" yaml:"outcome"`
	Messages    []string      `json:"messages,omitempty" yaml:"messages,omitempty"`
	DurationMS  int64         `json:"duration_ms" yaml:"duration_ms"`
	Steps       []Step        `json:"steps,omitempty" yaml:"steps,omitempty"`
}

// Key identifies a result across runs.
func (r Result) Key() string {
	return r.ID + " [" + r.ClientProto + "]"
}

// With returns a copy of r with the given outcome and messages.
func (r Result) With(o Outcome, messages ...string) Result {
	r.Outcome = o
	r.Messages = messages
	return r
}

// Run is everything learned about one target.
type Run struct {
	// Name is the catalog name of the target, empty when the target came from a flag.
	Name      string    `json:"name,omitempty" yaml:"name,omitempty"`
	Target    string    `json:"target" yaml:"target"`
	Protocols []string  `json:"protocols" yaml:"protocols"`
	Started   time.Time `json:"started" yaml:"started"`
	Finished  time.Time `json:"finished" yaml:"finished"`
	// Error is set when the target could not be tested, such as a proxy that never started.
	Error   string   `json:"error,omitempty" yaml:"error,omitempty"`
	Results []Result `json:"results,omitempty" yaml:"results,omitempty"`
}

// Report is the record of one invocation of Mecone, holding a run per target tested.
type Report struct {
	Tool     string    `json:"tool" yaml:"tool"`
	Version  string    `json:"version" yaml:"version"`
	Started  time.Time `json:"started" yaml:"started"`
	Finished time.Time `json:"finished" yaml:"finished"`
	// Notice is a line about the run that reports print under their scores.
	Notice string `json:"notice,omitempty" yaml:"notice,omitempty"`
	Runs   []*Run `json:"runs" yaml:"runs"`
}

const yamlIndent = 2

// Write encodes the report as indented JSON.
func (r *Report) Write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// WriteYAML encodes the report as YAML.
func (r *Report) WriteYAML(w io.Writer) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(yamlIndent)
	if err := enc.Encode(r); err != nil {
		return err
	}
	return enc.Close()
}

// WriteFile writes the report as indented JSON to path.
func (r *Report) WriteFile(path string) error {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), filePerm)
}

// WriteYAMLFile writes the report as YAML to path.
func (r *Report) WriteYAMLFile(path string) (err error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, filePerm)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return r.WriteYAML(f)
}

// Read decodes a report from JSON.
func Read(rd io.Reader) (*Report, error) {
	r := new(Report)
	if err := json.NewDecoder(rd).Decode(r); err != nil {
		return nil, err
	}
	return r, nil
}

// ReadFile decodes a report from the JSON file at path.
func ReadFile(path string) (report *Report, err error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return Read(f)
}
