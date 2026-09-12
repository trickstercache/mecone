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

package report

import (
	"encoding/json"
	"io"
	"time"

	"github.com/trickstercache/mecone/pkg/results"
)

const jsonIndent = "  "

type targetSummary struct {
	Name      string    `json:"name"`
	Target    string    `json:"target"`
	Protocols []string  `json:"protocols,omitempty"`
	Error     string    `json:"error,omitempty"`
	Overall   Summary   `json:"overall"`
	Suites    []Summary `json:"suites,omitempty"`
	Changes   []Change  `json:"changes,omitempty"`
}

type reportSummary struct {
	Tool     string          `json:"tool"`
	Version  string          `json:"version"`
	Started  time.Time       `json:"started"`
	Finished time.Time       `json:"finished"`
	Notice   string          `json:"notice,omitempty"`
	Targets  []targetSummary `json:"targets"`
}

// JSON renders the report's scores, and each target's changes from baseline when one is given.
func JSON(w io.Writer, rep, baseline *results.Report) error {
	s := reportSummary{
		Tool:     rep.Tool,
		Version:  rep.Version,
		Started:  rep.Started,
		Finished: rep.Finished,
		Notice:   rep.Notice,
		Targets:  make([]targetSummary, none, len(rep.Runs)),
	}
	base := newBaselines(rep, baseline)
	for _, run := range rep.Runs {
		s.Targets = append(s.Targets, summarizeTarget(run, base))
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", jsonIndent)
	return enc.Encode(s)
}

func summarizeTarget(run *results.Run, base baselines) targetSummary {
	overall, suites := Summarize(run)
	t := targetSummary{
		Name:      runLabel(run),
		Target:    run.Target,
		Protocols: run.Protocols,
		Error:     run.Error,
		Overall:   overall,
		Suites:    suites,
	}
	if prev := base.find(run); prev != nil {
		t.Changes = Compare(prev, run)
	}
	return t
}
