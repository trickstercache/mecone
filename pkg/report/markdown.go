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
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	reportTitle = "# Mecone results\n\n"
	tableRule   = "|---|---|---|---|---|---|---|---|\n"
	suiteHeader = "| Suite | Score | MUST | SHOULD | MAY | Inconclusive | Skipped | Errors |\n" + tableRule
	targetTable = "| Target | Score | MUST | SHOULD | MAY | Inconclusive | Skipped | Errors |\n" + tableRule
	tableRow    = "| %s | %s | %s | %s | %s | %d | %d | %d |\n"

	notTested  = "not tested"
	section    = "##"
	subsection = "###"
	blankLine  = '\n'
)

// Markdown renders the report, and each target's changes from baseline when one is given.
func Markdown(w io.Writer, rep, baseline *results.Report) error {
	base := newBaselines(rep, baseline)
	b := appendReportInfo(nil, rep)
	if len(rep.Runs) == singleRun {
		b = appendRunBody(b, rep.Runs[first], base, section)
	} else {
		b = appendTargetTable(b, rep.Runs)
		for _, run := range rep.Runs {
			b = appendTargetSection(b, run, base)
		}
	}
	b = appendMarkdownNotice(b, rep.Notice)
	_, err := w.Write(b)
	return err
}

func appendMarkdownNotice(b []byte, notice string) []byte {
	if notice == unset {
		return b
	}
	return fmt.Appendf(b, "> **Note:** %s\n", notice)
}

func appendReportInfo(b []byte, rep *results.Report) []byte {
	b = append(b, reportTitle...)
	if len(rep.Runs) == singleRun {
		b = appendTargetInfo(b, rep.Runs[first])
	} else {
		b = fmt.Appendf(b, "- Targets: %d\n", len(rep.Runs))
	}
	return fmt.Appendf(b, "- Tool: %s %s\n- Finished: %s\n\n",
		rep.Tool, rep.Version, rep.Finished.Format(time.RFC3339))
}

func appendTargetInfo(b []byte, run *results.Run) []byte {
	return fmt.Appendf(b, "- Target: %s\n- Client protocols: %s\n",
		run.Target, strings.Join(run.Protocols, ", "))
}

func appendTargetSection(b []byte, run *results.Run, base baselines) []byte {
	b = fmt.Appendf(b, "%s %s\n\n", section, runLabel(run))
	b = appendTargetInfo(b, run)
	b = append(b, blankLine)
	return appendRunBody(b, run, base, subsection)
}

func appendRunBody(b []byte, run *results.Run, base baselines, heading string) []byte {
	b = appendNote(b, run)
	if len(run.Results) == none {
		return b
	}
	overall, suites := Summarize(run)
	b = appendScores(b, append([]Summary{overall}, suites...))
	b = appendFindings(b, run.Results, heading)
	if prev := base.find(run); prev != nil {
		b = appendChanges(b, Compare(prev, run), heading)
	}
	return b
}

func appendNote(b []byte, run *results.Run) []byte {
	if note := runNote(run); note != unset {
		return fmt.Appendf(b, "%s\n\n", note)
	}
	return b
}

func appendTargetTable(b []byte, runs []*results.Run) []byte {
	b = append(b, targetTable...)
	for _, run := range byScoreDesc(runs) {
		overall, _ := Summarize(run)
		b = fmt.Appendf(b, tableRow, runLabel(run), targetScore(run, overall), overall.Must,
			overall.Should, overall.May, overall.Inconclusive, overall.Skipped, overall.Errors)
	}
	return append(b, blankLine)
}

func targetScore(run *results.Run, overall Summary) string {
	if run.Error != "" {
		return notTested
	}
	return overall.String()
}

func appendScores(b []byte, scores []Summary) []byte {
	b = append(b, suiteHeader...)
	for _, s := range scores {
		b = fmt.Appendf(b, tableRow, s.Name, s.String(), s.Must, s.Should, s.May,
			s.Inconclusive, s.Skipped, s.Errors)
	}
	return append(b, blankLine)
}

func appendFindings(b []byte, rs []results.Result, heading string) []byte {
	findings := slices.DeleteFunc(slices.Clone(rs), func(r results.Result) bool {
		return !isFinding(r.Outcome)
	})
	if len(findings) == none {
		return b
	}
	b = fmt.Appendf(b, "%s Findings\n\n", heading)
	for _, r := range findings {
		refs := markdownRefs(r.Refs)
		if refs != "" {
			refs = " — " + refs
		}
		b = fmt.Appendf(b, "- `%s` [%s] %s, %s%s: %s\n", r.ID, r.ClientProto,
			strings.ToUpper(r.Level), r.Outcome, refs, strings.Join(r.Messages, "; "))
	}
	return append(b, blankLine)
}

func markdownRefs(refs []testdef.Ref) string {
	links := make([]string, len(refs))
	for i, ref := range refs {
		links[i] = fmt.Sprintf("[%s](%s)", ref, ref.URL())
	}
	return strings.Join(links, ", ")
}

func isFinding(o results.Outcome) bool {
	return o == results.Fail || o == results.Error || o == results.Inconclusive
}

func appendChanges(b []byte, changes []Change, heading string) []byte {
	b = fmt.Appendf(b, "%s Changes from baseline\n\n", heading)
	if len(changes) == none {
		return append(b, "No outcomes changed.\n\n"...)
	}
	for _, c := range changes {
		b = fmt.Appendf(b, "- `%s`: %s -> %s\n", c.Key, c.From, c.To)
	}
	return append(b, blankLine)
}
