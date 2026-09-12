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
	"encoding/csv"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trickstercache/mecone/pkg/results"
)

// Column positions of a CSV row, in the order csvHeader names them.
const (
	colRunStarted = iota
	colTarget
	colTargetURL
	colClientProto
	colOriginProto
	colSuite
	colGroup
	colTest
	colRefs
	colLevel
	colOutcome
	colStep
	colMethod
	colPath
	colStatus
	colForwarded
	colReused
	colTTFB
	colTotal
	colBytes
	colError
	colCount
)

const (
	first     = 0
	msDigits  = 3
	floatBits = 64
	fixedFmt  = 'f'
)

// csvHeader is the first row of every CSV report.
var csvHeader = []string{
	"run_started", "target", "target_url", "client_proto", "origin_proto", "suite", "group",
	"test", "refs", "level", "outcome", "step", "method", "path", "status", "forwarded", "conn_reused",
	"ttfb_ms", "total_ms", "bytes", "error",
}

// CSV writes one row per request step, so raw timings can be analyzed in a spreadsheet. Results
// that made no requests, and targets that were never tested, each still get a row.
func CSV(w io.Writer, rep *results.Report) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(csvHeader); err != nil {
		return err
	}
	for _, run := range rep.Runs {
		if err := writeRunRows(cw, run); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func writeRunRows(cw *csv.Writer, run *results.Run) error {
	base := runRow(run)
	if run.Error != "" {
		if err := cw.Write(runErrorRow(base, run)); err != nil {
			return err
		}
	}
	for _, r := range run.Results {
		if err := writeResultRows(cw, base, r); err != nil {
			return err
		}
	}
	return nil
}

func writeResultRows(cw *csv.Writer, base []string, r results.Result) error {
	if len(r.Steps) == none {
		return cw.Write(steplessRow(base, r))
	}
	for _, s := range r.Steps {
		if err := cw.Write(stepRow(base, r, s)); err != nil {
			return err
		}
	}
	return nil
}

func runRow(run *results.Run) []string {
	row := make([]string, colCount)
	row[colRunStarted] = run.Started.Format(time.RFC3339)
	row[colTarget] = runLabel(run)
	row[colTargetURL] = run.Target
	return row
}

func runErrorRow(base []string, run *results.Run) []string {
	row := slices.Clone(base)
	row[colOutcome] = string(results.Error)
	row[colError] = run.Error
	return row
}

func resultRow(base []string, r results.Result) []string {
	row := slices.Clone(base)
	row[colClientProto] = r.ClientProto
	row[colOriginProto] = r.OriginProto
	row[colSuite] = r.Suite
	row[colGroup] = r.Group
	row[colTest] = r.ID
	refs := make([]string, len(r.Refs))
	for i, ref := range r.Refs {
		refs[i] = string(ref)
	}
	row[colRefs] = strings.Join(refs, "; ")
	row[colLevel] = r.Level
	row[colOutcome] = string(r.Outcome)
	return row
}

func steplessRow(base []string, r results.Result) []string {
	row := resultRow(base, r)
	if len(r.Messages) > none {
		row[colError] = r.Messages[first]
	}
	return row
}

func stepRow(base []string, r results.Result, s results.Step) []string {
	row := resultRow(base, r)
	row[colStep] = strconv.Itoa(s.Step)
	row[colMethod] = s.Method
	row[colPath] = s.Path
	row[colStatus] = strconv.Itoa(s.Status)
	row[colForwarded] = strconv.FormatBool(s.Forwarded)
	row[colReused] = strconv.FormatBool(s.Reused)
	row[colTTFB] = formatMS(s.TTFBMS)
	row[colTotal] = formatMS(s.TotalMS)
	row[colBytes] = strconv.Itoa(s.Bytes)
	row[colError] = s.Error
	return row
}

func formatMS(ms float64) string {
	return strconv.FormatFloat(ms, fixedFmt, msDigits, floatBits)
}
