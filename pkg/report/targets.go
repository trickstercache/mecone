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
	"slices"

	"github.com/trickstercache/mecone/pkg/results"
)

const singleRun = 1

// belowScore ranks a run with nothing to score under every real score, including zero.
const belowScore = -1

// runLabel names a target in a report, falling back to its URL when it has no catalog name.
func runLabel(run *results.Run) string {
	if run.Name != "" {
		return run.Name
	}
	return run.Target
}

// byScoreDesc orders runs by overall score, highest first; unscored and failed runs trail.
func byScoreDesc(runs []*results.Run) []*results.Run {
	out := slices.Clone(runs)
	slices.SortStableFunc(out, func(a, b *results.Run) int {
		return sortScore(b) - sortScore(a)
	})
	return out
}

func sortScore(run *results.Run) int {
	if run.Error != unset {
		return belowScore
	}
	overall, _ := Summarize(run)
	if !overall.Scored() {
		return belowScore
	}
	return overall.Score()
}

// baselines finds the baseline run for a target. Targets match by label; a one-target report is
// also compared against a one-target baseline whose label differs.
type baselines struct {
	byLabel map[string]*results.Run
	only    *results.Run
}

func newBaselines(rep, baseline *results.Report) baselines {
	b := baselines{byLabel: make(map[string]*results.Run)}
	if baseline == nil {
		return b
	}
	for _, run := range baseline.Runs {
		b.byLabel[runLabel(run)] = run
	}
	if len(rep.Runs) == singleRun && len(baseline.Runs) == singleRun {
		b.only = baseline.Runs[first]
	}
	return b
}

func (b baselines) find(run *results.Run) *results.Run {
	if prev, ok := b.byLabel[runLabel(run)]; ok {
		return prev
	}
	return b.only
}
