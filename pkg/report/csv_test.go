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
	"encoding/csv"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	protoH2C  = "h2c"
	methodGet = "GET"
	pathObj   = "/mecone/obj/one"
	groupName = "reuse"
	emptyCell = ""

	wantRows     = 6
	bulkResults  = 100
	overflowSize = 8192

	stepOne   = 1
	stepTwo   = 2
	statusOK  = 200
	bodyBytes = 512
	ttfbMS    = 0.125
	totalMS   = 1.5
)

type cellCase struct {
	row, col int
	want     string
}

var csvCells = []cellCase{
	{1, colTarget, targetAlpha},
	{1, colTargetURL, urlAlpha},
	{1, colClientProto, protoH1},
	{1, colOriginProto, protoH2C},
	{1, colSuite, suiteCaching},
	{1, colGroup, groupName},
	{1, colTest, "a"},
	{1, colRefs, "RFC9110#9.3.1; RFC9111#4"},
	{1, colLevel, string(testdef.LevelMust)},
	{1, colOutcome, string(results.Pass)},
	{1, colStep, "1"},
	{1, colMethod, methodGet},
	{1, colPath, pathObj},
	{1, colStatus, "200"},
	{1, colForwarded, "true"},
	{1, colReused, "false"},
	{1, colTTFB, "0.125"},
	{1, colTotal, "1.500"},
	{1, colBytes, "512"},
	{1, colError, emptyCell},
	{2, colStep, "2"},
	{2, colStatus, "0"},
	{2, colReused, "true"},
	{2, colTTFB, "0.000"},
	{2, colError, "timeout"},
	{3, colTest, "g"},
	{3, colOutcome, string(results.Skipped)},
	{3, colStep, emptyCell},
	{3, colMethod, emptyCell},
	{3, colBytes, emptyCell},
	{3, colError, "not applicable"},
	{4, colTarget, targetBravo},
	{4, colTargetURL, urlBravo},
	{4, colSuite, emptyCell},
	{4, colOutcome, string(results.Error)},
	{4, colError, bravoErr},
	{5, colTarget, urlCharlie},
	{5, colTest, "z"},
	{5, colError, emptyCell},
}

func stepRun() *results.Run {
	return &results.Run{
		Name: targetAlpha, Target: urlAlpha, Protocols: []string{protoH1}, Started: started,
		Results: []results.Result{
			{
				ID: "a", Suite: suiteCaching, Group: groupName, Level: string(testdef.LevelMust),
				Refs:        []testdef.Ref{"RFC9110#9.3.1", "RFC9111#4"},
				ClientProto: protoH1, OriginProto: protoH2C, Outcome: results.Pass,
				Steps: []results.Step{
					{
						Step: stepOne, Method: methodGet, Path: pathObj, Status: statusOK,
						Forwarded: true, TTFBMS: ttfbMS, TotalMS: totalMS, Bytes: bodyBytes,
					},
					{Step: stepTwo, Method: methodGet, Path: pathObj, Reused: true, Error: "timeout"},
				},
			},
			{
				ID: "g", Suite: suiteRanges, Level: string(testdef.LevelMust), ClientProto: protoH1,
				Outcome: results.Skipped, Messages: []string{"not applicable"},
			},
		},
	}
}

func unnamedRun() *results.Run {
	return &results.Run{
		Target: urlCharlie, Started: started,
		Results: []results.Result{{ID: "z", Suite: suiteRanges, ClientProto: protoH1, Outcome: results.Pass}},
	}
}

func bulkReport() *results.Report {
	run := &results.Run{Name: targetAlpha, Target: urlAlpha, Started: started}
	for i := range bulkResults {
		run.Results = append(run.Results, results.Result{
			ID: strconv.Itoa(i), Suite: suiteCaching, ClientProto: protoH1, Outcome: results.Pass,
			Steps: []results.Step{{Step: stepOne, Method: methodGet, Path: pathObj, Status: statusOK}},
		})
	}
	return report(run)
}

func TestCSV(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, report(stepRun(), erroredRun(), unnamedRun())); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != wantRows || !slices.Equal(rows[passedMust], csvHeader) {
		t.Fatalf("rows = %q", rows)
	}
	for _, c := range csvCells {
		if got := rows[c.row][c.col]; got != c.want {
			t.Errorf("row %d column %s = %q, want %q", c.row, csvHeader[c.col], got, c.want)
		}
	}
}

func TestCSVRunStarted(t *testing.T) {
	var buf bytes.Buffer
	if err := CSV(&buf, report(unnamedRun())); err != nil {
		t.Fatal(err)
	}
	want := started.Format(time.RFC3339)
	if !strings.Contains(buf.String(), want) || len(csvHeader) != colCount {
		t.Errorf("csv = %q, want start %q over %d columns", buf.String(), want, colCount)
	}
}

func TestCSVWriteError(t *testing.T) {
	tooLong := &results.Run{
		Name: targetBravo, Target: urlBravo, Started: started,
		Error: strings.Repeat("x", overflowSize),
	}
	for _, rep := range []*results.Report{report(tooLong), bulkReport()} {
		if err := CSV(failWriter{}, rep); err == nil {
			t.Errorf("write error not returned for %d runs", len(rep.Runs))
		}
	}
}
