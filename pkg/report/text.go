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
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf8"

	"github.com/trickstercache/mecone/pkg/results"
)

const (
	textHeader  = "Suite\tScore\tMUST\tSHOULD\tMAY\tInconclusive\tSkipped\tErrors\n"
	textTargets = "Target\tScore\tMUST\tSHOULD\tMAY\tInconclusive\tSkipped\tErrors\n"
	textRow     = "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\n"

	changesHeading = "Changes from baseline:\n"
	noChanges      = "No outcomes changed.\n"
	noticePrefix   = "Note: "
	wordSep        = " "

	// divider separates the per-target summaries when a table of targets leads them
	divider = "--------------------------\n\n"
)

const (
	empty        = 0
	textMinWidth = 0
	textTabWidth = 4
	textPadding  = 2
	textPadChar  = ' '
	textFlags    = 0

	// minNoticeWidth keeps the notice readable when the tables above it are narrower than it is
	minNoticeWidth = 40
)

// Text renders one score table per target for a terminal, leaving individual findings to the
// Markdown, JSON and CSV reports.
func Text(w io.Writer, rep, baseline *results.Report) error {
	base := newBaselines(rep, baseline)
	var b []byte
	many := len(rep.Runs) > singleRun
	if many {
		b = append(b, divider...)
		b = append(b, "All Targets Summary\n\n"...)
		b = appendTextTargets(b, rep.Runs)
	}
	for _, run := range rep.Runs {
		if len(b) > empty {
			b = append(b, blankLine)
		}
		if many {
			b = append(b, divider...)
		}
		b = appendTextRun(b, run, base)
	}
	body, err := expandTabs(b)
	if err != nil {
		return err
	}
	// the notice wraps to the tables above it, whose width only tabwriter knows
	body = appendTextNotice(body, rep.Notice, widestLine(body))
	_, err = w.Write(body)
	return err
}

// expandTabs lays the tab-separated tables out in columns, fixing their final width.
func expandTabs(b []byte) ([]byte, error) {
	var out bytes.Buffer
	tw := tabwriter.NewWriter(&out, textMinWidth, textTabWidth, textPadding, textPadChar, textFlags)
	if _, err := tw.Write(b); err != nil {
		return nil, err
	}
	if err := tw.Flush(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func widestLine(b []byte) int {
	var widest int
	for line := range strings.SplitSeq(string(b), "\n") {
		widest = max(widest, utf8.RuneCountInString(strings.TrimRight(line, wordSep)))
	}
	return widest
}

// appendTextTargets leads a multi-target report with every target's overall score in one table.
func appendTextTargets(b []byte, runs []*results.Run) []byte {
	b = append(b, textTargets...)
	for _, run := range byScoreDesc(runs) {
		overall, _ := Summarize(run)
		b = fmt.Appendf(b, textRow, runLabel(run), targetScore(run, overall), overall.Must,
			overall.Should, overall.May, overall.Inconclusive, overall.Skipped, overall.Errors)
	}
	return b
}

func appendTextRun(b []byte, run *results.Run, base baselines) []byte {
	b = fmt.Appendf(b, "%s\n", runLabel(run))
	// a target that was never tested reached no protocol, so it has none to name
	if len(run.Protocols) > empty {
		b = fmt.Appendf(b, "- Client protocols: %s\n", strings.Join(run.Protocols, ", "))
	}
	b = append(b, blankLine)
	if note := runNote(run); note != unset {
		return fmt.Appendf(b, "%s\n", note)
	}
	overall, suites := Summarize(run)
	b = append(b, textHeader...)
	for _, s := range append([]Summary{overall}, suites...) {
		b = fmt.Appendf(b, textRow, s.Name, s.String(), s.Must, s.Should, s.May,
			s.Inconclusive, s.Skipped, s.Errors)
	}
	return appendTextChanges(b, run, base)
}

func appendTextChanges(b []byte, run *results.Run, base baselines) []byte {
	prev := base.find(run)
	if prev == nil {
		return b
	}
	changes := Compare(prev, run)
	b = append(b, blankLine)
	b = append(b, changesHeading...)
	if len(changes) == none {
		return append(b, noChanges...)
	}
	for _, c := range changes {
		b = fmt.Appendf(b, "  %s: %s -> %s\n", c.Key, c.From, c.To)
	}
	return b
}

// appendTextNotice puts the run's notice under the scores, where a reader of the numbers sees it,
// wrapped so that no line of it runs past the tables it explains.
func appendTextNotice(b []byte, notice string, width int) []byte {
	if notice == unset {
		return b
	}
	if len(b) > empty {
		b = append(b, blankLine)
	}
	indent := strings.Repeat(wordSep, len(noticePrefix))
	for i, line := range wrapWords(notice, max(width-len(noticePrefix), minNoticeWidth)) {
		lead := indent
		if i == first {
			lead = noticePrefix
		}
		b = fmt.Appendf(b, "%s%s\n", lead, line)
	}
	return b
}

// wrapWords breaks s into lines of at most width runes, never splitting a word.
func wrapWords(s string, width int) []string {
	var lines []string
	var line string
	for word := range strings.FieldsSeq(s) {
		switch {
		case line == unset:
			line = word
		case utf8.RuneCountInString(line)+len(wordSep)+utf8.RuneCountInString(word) <= width:
			line += wordSep + word
		default:
			lines = append(lines, line)
			line = word
		}
	}
	if line != unset {
		lines = append(lines, line)
	}
	return lines
}
