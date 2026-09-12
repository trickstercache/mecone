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

// Package report scores the results of a run and renders them for people and machines.
package report

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	none    = 0
	percent = 100
	// tenths rounds a published pass rate to one decimal place, matching what reports display.
	tenths = 10

	overallName = "overall"
	unset       = ""
)

// runNote explains a run that has nothing to score; an empty string means the run has results.
func runNote(run *results.Run) string {
	switch {
	case run.Error != unset:
		return "Could not be tested: " + run.Error
	case len(run.Results) == none:
		return "No tests ran."
	}
	return unset
}

// Requirement levels are weighted against each other: MAY is the baseline, SHOULD counts four
// times as much, and MUST ten times. A proxy is therefore scored mostly on the requirements it
// has no discretion about, while optional capabilities still move the number a little.
const (
	mayWeight    = 1
	shouldWeight = 4
	mustWeight   = 10
)

// Tally counts conclusive results at one requirement level.
type Tally struct {
	Passed int `json:"passed"`
	Total  int `json:"total"`
}

// Percent returns the pass rate, or 0 when there are no results.
func (t Tally) Percent() float64 {
	if t.Total == none {
		return none
	}
	return percent * float64(t.Passed) / float64(t.Total)
}

// String formats the tally as "passed/total (percent)".
func (t Tally) String() string {
	if t.Total == none {
		return "-"
	}
	return fmt.Sprintf("%d/%d (%.1f%%)", t.Passed, t.Total, t.Percent())
}

// MarshalJSON publishes the pass rate alongside the counts, so a reader of the JSON sees the
// same three numbers the Markdown report shows.
func (t Tally) MarshalJSON() ([]byte, error) {
	type tally Tally // a distinct type, so marshaling it does not call this method again
	return json.Marshal(struct {
		tally
		Percent float64 `json:"percent"`
	}{tally(t), math.Round(t.Percent()*tenths) / tenths})
}

// Summary counts the results of one suite, or of a whole run. Only passes and failures are
// conclusive; inconclusive, skipped and errored results are counted but never scored.
type Summary struct {
	Name         string `json:"name"`
	Must         Tally  `json:"must"`
	Should       Tally  `json:"should"`
	May          Tally  `json:"may"`
	Info         int    `json:"info"`
	Inconclusive int    `json:"inconclusive"`
	Skipped      int    `json:"skipped"`
	Errors       int    `json:"errors"`
}

func (s *Summary) add(r results.Result) {
	switch r.Outcome {
	case results.Skipped:
		s.Skipped++
		return
	case results.Error:
		s.Errors++
		return
	case results.Inconclusive:
		s.Inconclusive++
		return
	}
	var t *Tally
	switch testdef.Level(r.Level) {
	case testdef.LevelMust:
		t = &s.Must
	case testdef.LevelShould:
		t = &s.Should
	case testdef.LevelMay:
		t = &s.May
	default:
		s.Info++
		return
	}
	t.Total++
	if r.Outcome == results.Pass {
		t.Passed++
	}
}

// WeightedScore is the share of weighted requirements met, from 0 to 100. Each level's passes
// and its total are multiplied by the level's weight before being summed, so the three levels
// become one number without any of them being scored in isolation.
func WeightedScore(must, should, may Tally) int {
	passed := mustWeight*must.Passed + shouldWeight*should.Passed + mayWeight*may.Passed
	tested := mustWeight*must.Total + shouldWeight*should.Total + mayWeight*may.Total
	if tested == none {
		return none
	}
	return percent * passed / tested
}

// Score is the summary's weighted score, 0 when nothing conclusive was tested.
func (s Summary) Score() int {
	return WeightedScore(s.Must, s.Should, s.May)
}

// Scored reports whether anything conclusive was tested, which is what separates a real score of
// 0 from no score at all.
func (s Summary) Scored() bool {
	return s.Must.Total+s.Should.Total+s.May.Total > none
}

// String formats the score for a report column, or "-" when nothing was scored.
func (s Summary) String() string {
	if !s.Scored() {
		return "-"
	}
	return strconv.Itoa(s.Score())
}

// MarshalJSON publishes the derived score, which is null when nothing conclusive was tested.
func (s Summary) MarshalJSON() ([]byte, error) {
	type summary Summary // a distinct type, so marshaling it does not call this method again
	var score *int
	if s.Scored() {
		score = new(int)
		*score = s.Score()
	}
	return json.Marshal(struct {
		summary
		Score *int `json:"score"`
	}{summary(s), score})
}

// Summarize scores a run overall and per suite, with suites in order of first appearance.
func Summarize(run *results.Run) (Summary, []Summary) {
	overall := Summary{Name: overallName}
	var suites []Summary
	index := make(map[string]int)
	for _, r := range run.Results {
		overall.add(r)
		i, ok := index[r.Suite]
		if !ok {
			i = len(suites)
			index[r.Suite] = i
			suites = append(suites, Summary{Name: r.Suite})
		}
		suites[i].add(r)
	}
	return overall, suites
}

// Change is a result whose outcome differs between two runs.
type Change struct {
	Key  string          `json:"key"`
	From results.Outcome `json:"from"`
	To   results.Outcome `json:"to"`
}

// Compare lists the results whose outcome changed from base to cur, sorted by key.
func Compare(base, cur *results.Run) []Change {
	before := make(map[string]results.Outcome, len(base.Results))
	for _, r := range base.Results {
		before[r.Key()] = r.Outcome
	}
	var out []Change
	for _, r := range cur.Results {
		if prev, ok := before[r.Key()]; ok && prev != r.Outcome {
			out = append(out, Change{Key: r.Key(), From: prev, To: r.Outcome})
		}
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Key, b.Key) })
	return out
}
