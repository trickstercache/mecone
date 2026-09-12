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
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	suiteCaching = "caching"
	suiteRanges  = "ranges"

	protoH1 = "h1"
	toolID  = "mecone"

	targetAlpha    = "alpha"
	targetBravo    = "bravo"
	targetPerfect  = "perfect"
	urlAlpha       = "http://alpha"
	urlBravo       = "http://bravo"
	urlCharlie     = "http://charlie"
	urlPerfect     = "http://perfect"
	bravoErr       = "dial tcp: connection refused"
	changedResult  = "`a [h1]`: fail -> pass"
	errNotReturned = "write error not returned"
	sampleNotice   = "no proxy stood between the client and the origin"

	wrapRepeats  = 6
	minWrapped   = 2
	noticeLead   = "Note:"
	noticeIndent = "      "

	passedMust = 0
	failedMust = 1

	wantTargets    = 2
	nothingWritten = 0

	// the sample run passes 1 of 2 MUSTs, 1 of 1 SHOULD and 0 of 1 MAY: (10+4+0)/(20+4+1)
	wantAlphaScore = 56
	// noScore stands in for a null score, which no real score can be mistaken for
	noScore = -1
)

var (
	started      = time.Date(2026, time.September, 11, 0, 0, 0, 0, time.UTC)
	finished     = started.Add(time.Minute)
	sampleSuites = []string{suiteCaching, suiteRanges}
	wantOverall  = Summary{
		Name: overallName, Must: Tally{1, 2}, Should: Tally{1, 1}, May: Tally{0, 1},
		Info: 1, Inconclusive: 1, Skipped: 1, Errors: 1,
	}
)

type scoreCase struct {
	must, should, may Tally
	want              int
	display           string
}

var scoreCases = []scoreCase{
	// nothing conclusive is not a score of zero, and must not be shown as one
	{Tally{}, Tally{}, Tally{}, 0, "-"},
	// the worked example: 5 of 8 MUSTs, 2 of 4 SHOULDs, 1 of 5 MAYs is (50+8+1)/(80+16+5)
	{Tally{5, 8}, Tally{2, 4}, Tally{1, 5}, 58, "58"},
	{Tally{10, 10}, Tally{10, 10}, Tally{10, 10}, 100, "100"},
	{Tally{0, 10}, Tally{0, 10}, Tally{0, 10}, 0, "0"},
	// the same tally at a different level moves the score by the same amount only in isolation
	{Tally{9, 10}, Tally{}, Tally{}, 90, "90"},
	{Tally{}, Tally{}, Tally{9, 10}, 90, "90"},
	// one passed MUST outweighs a failed SHOULD and a failed MAY together
	{Tally{1, 1}, Tally{0, 1}, Tally{0, 1}, 66, "66"},
}

type jsonOverall struct {
	Score *int  `json:"score"`
	Must  Tally `json:"must"`
}

type jsonTarget struct {
	Name    string            `json:"name"`
	Target  string            `json:"target"`
	Error   string            `json:"error"`
	Overall jsonOverall       `json:"overall"`
	Suites  []json.RawMessage `json:"suites"`
	Changes []Change          `json:"changes"`
}

type jsonReport struct {
	Tool    string       `json:"tool"`
	Version string       `json:"version"`
	Notice  string       `json:"notice"`
	Targets []jsonTarget `json:"targets"`
}

func sampleRun() *results.Run {
	r := func(id, suite string, level testdef.Level, o results.Outcome, msgs ...string) results.Result {
		res := results.Result{
			ID: id, Suite: suite, Level: string(level), ClientProto: protoH1, Outcome: o, Messages: msgs,
		}
		if id == "b" {
			res.Refs = []testdef.Ref{"RFC9111#4", "RFC9110#13.1.1"}
		}
		return res
	}
	return &results.Run{
		Name: targetAlpha, Target: urlAlpha, Protocols: []string{protoH1},
		Started: started, Finished: finished,
		Results: []results.Result{
			r("a", suiteCaching, testdef.LevelMust, results.Pass),
			r("b", suiteCaching, testdef.LevelMust, results.Fail, "step 2: nope"),
			r("c", suiteCaching, testdef.LevelShould, results.Pass),
			r("d", suiteRanges, testdef.LevelMay, results.Fail),
			r("e", suiteRanges, testdef.LevelInfo, results.Pass),
			r("f", suiteRanges, testdef.LevelMust, results.Inconclusive),
			r("g", suiteRanges, testdef.LevelMust, results.Skipped),
			r("h", suiteRanges, testdef.LevelMust, results.Error),
		},
	}
}

func baselineRun() *results.Run {
	run := sampleRun()
	run.Results[passedMust].Outcome = results.Fail
	return run
}

func erroredRun() *results.Run {
	return &results.Run{
		Name: targetBravo, Target: urlBravo, Protocols: []string{protoH1},
		Started: started, Finished: finished, Error: bravoErr,
	}
}

func report(runs ...*results.Run) *results.Report {
	return &results.Report{
		Tool: toolID, Version: "test", Started: started, Finished: finished, Runs: runs,
	}
}

func suiteNames(scores []Summary) []string {
	var names []string
	for _, s := range scores {
		names = append(names, s.Name)
	}
	return names
}

func mustContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func mustNotContain(t *testing.T, out string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if strings.Contains(out, want) {
			t.Errorf("output has unwanted %q:\n%s", want, out)
		}
	}
}

type fieldCase struct {
	name, got, want string
}

func mustMatch(t *testing.T, cases ...fieldCase) {
	t.Helper()
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// scoreOf flattens a target's overall score, so a null one can be compared like any other value.
func scoreOf(target jsonTarget) int {
	if target.Overall.Score == nil {
		return noScore
	}
	return *target.Overall.Score
}

func mustScores(t *testing.T, alpha, bravo jsonTarget) {
	t.Helper()
	if got := scoreOf(alpha); got != wantAlphaScore {
		t.Errorf("alpha score = %d, want %d", got, wantAlphaScore)
	}
	// a target that was never tested has no score, which is not the same as scoring zero
	if got := scoreOf(bravo); got != noScore {
		t.Errorf("bravo score = %d, want none", got)
	}
}

func markdownOf(t *testing.T, rep, baseline *results.Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Markdown(&buf, rep, baseline); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestSummarize(t *testing.T) {
	overall, suites := Summarize(sampleRun())
	if overall != wantOverall {
		t.Errorf("overall = %+v, want %+v", overall, wantOverall)
	}
	if !slices.Equal(suiteNames(suites), sampleSuites) {
		t.Errorf("suites = %+v", suites)
	}
}

func TestWeightedScore(t *testing.T) {
	for _, c := range scoreCases {
		s := Summary{Must: c.must, Should: c.should, May: c.may}
		if got := s.Score(); got != c.want {
			t.Errorf("Score(%v, %v, %v) = %d, want %d", c.must, c.should, c.may, got, c.want)
		}
		if got := s.String(); got != c.display {
			t.Errorf("String(%v, %v, %v) = %q, want %q", c.must, c.should, c.may, got, c.display)
		}
	}
	if (Tally{}).String() != "-" || (Tally{1, 2}).String() != "1/2 (50.0%)" {
		t.Error("Tally.String")
	}
}

func TestCompare(t *testing.T) {
	base, cur := sampleRun(), sampleRun()
	base.Results[failedMust].Outcome = results.Pass
	changes := Compare(base, cur)
	if want := []Change{{Key: "b [h1]", From: results.Pass, To: results.Fail}}; !slices.Equal(changes, want) {
		t.Errorf("changes = %+v", changes)
	}
}

func TestMarkdownSingleTarget(t *testing.T) {
	rep := report(sampleRun())
	out := markdownOf(t, rep, rep)
	mustContain(t, out, "# Mecone results", "- Target: "+urlAlpha, "- Client protocols: h1",
		"- Tool: mecone test", "| overall | 56 | 1/2 (50.0%) |", "## Findings",
		"`b` [h1] MUST, fail — [RFC9111#4](https://www.rfc-editor.org/rfc/rfc9111#section-4), "+
			"[RFC9110#13.1.1](https://www.rfc-editor.org/rfc/rfc9110#section-13.1.1): step 2: nope",
		"## Changes from baseline", "No outcomes changed.")
	mustNotContain(t, out, "| Target |", "- Targets:", "## "+targetAlpha)
}

func TestMarkdownBaselineFallback(t *testing.T) {
	base := report(baselineRun())
	base.Runs[passedMust].Name = targetBravo
	out := markdownOf(t, report(sampleRun()), base)
	mustContain(t, out, changedResult)
}

func TestMarkdownMultiTarget(t *testing.T) {
	rep := report(sampleRun(), erroredRun())
	out := markdownOf(t, rep, report(baselineRun(), erroredRun()))
	mustContain(t, out, "- Targets: 2", "| Target | Score |", "| alpha | 56 | 1/2 (50.0%) |",
		"| bravo | not tested | - | - | - | 0 | 0 | 0 |", "## alpha", "## bravo",
		"- Target: "+urlBravo, "Could not be tested: "+bravoErr, "### Findings",
		"### Changes from baseline", changedResult)
}

func TestTargetSummarySortedByScore(t *testing.T) {
	// catalog order is alpha then perfect; the summary tables must lead with the higher score
	perfect := &results.Run{
		Name: targetPerfect, Target: urlPerfect, Protocols: []string{protoH1},
		Started: started, Finished: finished,
		Results: []results.Result{{
			ID: "a", Suite: suiteCaching, Level: string(testdef.LevelMust),
			ClientProto: protoH1, Outcome: results.Pass,
		}},
	}
	rep := report(sampleRun(), perfect)
	md, text := markdownOf(t, rep, nil), textOf(t, rep, nil)
	mdSummary := before(t, md, "## "+targetAlpha)
	textSummary := before(t, text, divider+targetAlpha)
	mustPrecede(t, mdSummary, "| "+targetPerfect+" | 100 |", "| "+targetAlpha+" | 56 |")
	mustPrecede(t, textSummary, targetPerfect, targetAlpha)
	// per-target sections keep catalog order
	mustPrecede(t, md, "## "+targetAlpha, "## "+targetPerfect)
	mustPrecede(t, text, divider+targetAlpha, divider+targetPerfect)
}

func before(t *testing.T, out, marker string) string {
	t.Helper()
	head, _, found := strings.Cut(out, marker)
	if !found {
		mustContain(t, out, marker)
		t.FailNow()
	}
	return head
}

func mustPrecede(t *testing.T, out, first, second string) {
	t.Helper()
	_, rest, found := strings.Cut(out, first)
	if !found {
		mustContain(t, out, first)
		return
	}
	if !strings.Contains(rest, second) {
		t.Errorf("%q must precede %q:\n%s", first, second, out)
	}
}

func TestMarkdownNoResults(t *testing.T) {
	mustContain(t, markdownOf(t, report(&results.Run{Target: urlCharlie}), nil), "No tests ran.")
	mustContain(t, markdownOf(t, report(), nil), "- Targets: 0")
}

func TestMarkdownWriteError(t *testing.T) {
	if err := Markdown(failWriter{}, report(sampleRun()), nil); err == nil {
		t.Error(errNotReturned)
	}
}

func jsonOf(t *testing.T, rep, baseline *results.Report) jsonReport {
	t.Helper()
	var buf bytes.Buffer
	if err := JSON(&buf, rep, baseline); err != nil {
		t.Fatal(err)
	}
	var got jsonReport
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestJSON(t *testing.T) {
	got := jsonOf(t, report(sampleRun(), erroredRun()), report(baselineRun(), erroredRun()))
	if len(got.Targets) != wantTargets {
		t.Fatalf("report = %+v", got)
	}
	alpha, bravo := got.Targets[passedMust], got.Targets[failedMust]
	mustMatch(t,
		fieldCase{"tool", got.Tool, toolID},
		fieldCase{"alpha name", alpha.Name, targetAlpha},
		fieldCase{"alpha target", alpha.Target, urlAlpha},
		fieldCase{"bravo name", bravo.Name, targetBravo},
		fieldCase{"bravo error", bravo.Error, bravoErr},
	)
	mustScores(t, alpha, bravo)
	wantChanges := []Change{{Key: "a [h1]", From: results.Fail, To: results.Pass}}
	if alpha.Overall.Must != wantOverall.Must || !slices.Equal(alpha.Changes, wantChanges) {
		t.Errorf("alpha scores = %+v", alpha)
	}
	if len(alpha.Suites) != len(sampleSuites) || len(bravo.Suites) != nothingWritten {
		t.Errorf("suites = %+v and %+v", alpha.Suites, bravo.Suites)
	}
}

// The JSON carries the same three numbers per level the Markdown table shows, plus the score.
func TestJSONPublishesScoreAndRates(t *testing.T) {
	var buf bytes.Buffer
	if err := JSON(&buf, report(sampleRun()), nil); err != nil {
		t.Fatal(err)
	}
	mustContain(t, buf.String(), `"score": 56`, `"passed": 1`, `"total": 2`, `"percent": 50`)
}

func TestJSONUnnamedTarget(t *testing.T) {
	got := jsonOf(t, report(&results.Run{Target: urlCharlie}), nil)
	if got.Targets[passedMust].Name != urlCharlie {
		t.Errorf("targets = %+v", got.Targets)
	}
}

func TestJSONWriteError(t *testing.T) {
	if err := JSON(failWriter{}, report(sampleRun()), nil); err == nil {
		t.Error(errNotReturned)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) {
	return nothingWritten, errors.New("boom")
}

func textOf(t *testing.T, rep, baseline *results.Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := Text(&buf, rep, baseline); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestText(t *testing.T) {
	rep := report(sampleRun(), erroredRun())
	out := textOf(t, rep, report(baselineRun(), erroredRun()))
	mustContain(t, out, targetAlpha, targetBravo, "- Client protocols: h1",
		"Suite", "Inconclusive", "overall", "Could not be tested: "+bravoErr,
		"Changes from baseline:", "a [h1]: fail -> pass",
		// every target's overall score leads the per-target breakdowns, each set off by a divider
		"Target", "Score", "not tested", divider+targetAlpha, divider+targetBravo)
	// findings and the Markdown scaffolding belong in the report files, not on a terminal
	mustNotContain(t, out, "# Mecone results", "|", "step 2: nope")
	if !strings.Contains(out, "overall  56") {
		t.Errorf("the score table is not column-aligned:\n%s", out)
	}
}

func TestTextWithoutBaseline(t *testing.T) {
	out := textOf(t, report(sampleRun()), nil)
	// one target needs neither a table of targets nor a divider to set it off from its neighbors
	mustNotContain(t, out, "Changes from baseline:", "Target\t", "Target ", divider)
	mustContain(t, textOf(t, report(&results.Run{Target: urlCharlie}), nil), "No tests ran.")
}

func TestTextUnchangedBaseline(t *testing.T) {
	rep := report(sampleRun())
	mustContain(t, textOf(t, rep, rep), "No outcomes changed.")
}

func TestTextWriteError(t *testing.T) {
	if err := Text(failWriter{}, report(sampleRun()), nil); err == nil {
		t.Error(errNotReturned)
	}
}

func noticed(rep *results.Report) *results.Report {
	rep.Notice = sampleNotice
	return rep
}

func isNoticeLine(line string) bool {
	return strings.HasPrefix(line, noticeLead) || strings.HasPrefix(line, noticeIndent)
}

// splitNotice separates the wrapped notice from the tables it was wrapped to fit.
func splitNotice(out string) (tables, notice []string) {
	for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
		if isNoticeLine(line) {
			notice = append(notice, line)
			continue
		}
		tables = append(tables, strings.TrimRight(line, " "))
	}
	return tables, notice
}

func widest(lines []string) int {
	var w int
	for _, line := range lines {
		w = max(w, len(line))
	}
	return w
}

func TestNoticeWrapsToTheTables(t *testing.T) {
	rep := report(sampleRun(), erroredRun())
	rep.Notice = strings.Repeat("a notice long enough to need wrapping. ", wrapRepeats)
	tables, notice := splitNotice(textOf(t, rep, nil))
	limit := widest(tables)
	if len(notice) < minWrapped {
		t.Fatalf("the notice was not wrapped: %d line(s)", len(notice))
	}
	if got := widest(notice); got > limit {
		t.Errorf("a notice line is %d wide, past the %d of the tables", got, limit)
	}
}

func TestNoticeIsRendered(t *testing.T) {
	mustContain(t, textOf(t, noticed(report(sampleRun())), nil), noticeLead+" "+sampleNotice)
	mustContain(t, markdownOf(t, noticed(report(sampleRun())), nil), "> **Note:** "+sampleNotice)
	if got := jsonOf(t, noticed(report(sampleRun())), nil).Notice; got != sampleNotice {
		t.Errorf("json notice = %q", got)
	}
	// a report without one says nothing at all
	mustNotContain(t, textOf(t, report(sampleRun()), nil), noticeLead)
	mustNotContain(t, markdownOf(t, report(sampleRun()), nil), noticeLead)
}
