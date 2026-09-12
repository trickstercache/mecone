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

package check

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/protocol"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	age  = "Age"
	via  = "Via"
	xHop = "X-Hop"

	arrangeStep   = 1
	fullStep      = 3
	unwantedStep  = 4
	longBodyBytes = 100

	wantMismatches          = 10
	wantForwardedMismatches = 2
)

func TestEvaluate(t *testing.T) {
	yes, no := true, false
	body := "hello"
	wait := testdef.Duration(time.Millisecond)
	tt := &testdef.Test{Steps: []*testdef.Step{
		{Arrange: true, Expect: &testdef.Expect{Status: testdef.StatusSet{http.StatusOK}}},
		{Wait: &wait},
		{Expect: &testdef.Expect{
			Status:                 testdef.StatusSet{http.StatusOK, http.StatusPartialContent},
			Headers:                []string{age, "Cache-Control: max-age=60", `Etag: ~^"v`},
			HeadersAbsent:          []string{"Set-Cookie"},
			Body:                   &body,
			Interim:                []int{http.StatusEarlyHints},
			Trailers:               []string{"X-Sum: 1"},
			Forwarded:              &yes,
			ForwardedHeaders:       []string{via},
			ForwardedHeadersAbsent: []string{xHop},
		}},
		{Expect: &testdef.Expect{Forwarded: &no}},
		{},
		{Expect: &testdef.Expect{ForwardedHeaders: []string{via}}},
		{},
	}}
	obs := []Observation{
		{Status: http.StatusOK},
		{},
		{
			Status:  http.StatusPartialContent,
			Header:  http.Header{age: {"3"}, "Cache-Control": {"max-age=60"}, "Etag": {`"v1"`}},
			Trailer: http.Header{"X-Sum": {"1"}},
			Interim: []int{http.StatusContinue, http.StatusEarlyHints},
			Body:    []byte("hello"),
		},
		{Status: http.StatusOK},
		{Err: errors.New("connection reset")},
		{Status: http.StatusOK},
	}
	log := protocol.Log{
		{Step: arrangeStep},
		{Step: fullStep, Header: http.Header{via: {"1.1 proxy"}}},
		{Step: unwantedStep},
	}
	fails := Evaluate(tt, obs, log)
	var steps []int
	for _, f := range fails {
		steps = append(steps, f.Step)
	}
	if !slices.Equal(steps, []int{4, 5, 6}) {
		t.Fatalf("failed steps = %v: %v", steps, fails)
	}
	if !strings.HasPrefix(fails[1].String(), "step 5: request failed") {
		t.Errorf("String() = %q", fails[1].String())
	}
	if Verdict(fails) != results.Fail {
		t.Errorf("verdict = %s", Verdict(fails))
	}
}

func TestEvaluateMismatches(t *testing.T) {
	yes := true
	body := "want"
	tt := &testdef.Test{Steps: []*testdef.Step{{Expect: &testdef.Expect{
		Status:           testdef.StatusSet{http.StatusOK},
		Headers:          []string{age, "Cache-Control: a", "Etag: ~^x"},
		HeadersAbsent:    []string{via},
		Body:             &body,
		Interim:          []int{http.StatusEarlyHints},
		Trailers:         []string{"X: 1"},
		Forwarded:        &yes,
		ForwardedHeaders: []string{via},
	}}}}
	obs := []Observation{{
		Status: http.StatusInternalServerError,
		Header: http.Header{"Cache-Control": {"b"}, "Etag": {"y"}, via: {"1.1 p"}},
		Body:   []byte(strings.Repeat("z", longBodyBytes)),
	}}
	fails := Evaluate(tt, obs, protocol.Log{})
	if len(fails) != wantMismatches {
		t.Errorf("got %d failures, want %d: %v", len(fails), wantMismatches, fails)
	}
}

func TestForwardedHeaderMismatch(t *testing.T) {
	tt := &testdef.Test{Steps: []*testdef.Step{{Expect: &testdef.Expect{
		ForwardedHeaders:       []string{via},
		ForwardedHeadersAbsent: []string{xHop},
	}}}}
	log := protocol.Log{{Step: arrangeStep, Header: http.Header{xHop: {"1"}}}}
	if fails := Evaluate(tt, []Observation{{Status: http.StatusOK}}, log); len(fails) != wantForwardedMismatches {
		t.Errorf("failures = %v", fails)
	}
	if fails := Evaluate(tt, nil, log); fails != nil {
		t.Errorf("missing observations must be skipped: %v", fails)
	}
}

func TestTimingsNeverAffectOutcome(t *testing.T) {
	timed := func(o Observation) Observation {
		o.TTFB, o.Total, o.Reused = time.Millisecond, time.Second, true
		return o
	}
	expectOK := &testdef.Expect{Status: testdef.StatusSet{http.StatusOK}}
	tt := &testdef.Test{Steps: []*testdef.Step{{Expect: expectOK}, {Expect: expectOK}}}
	obs := []Observation{{Status: http.StatusOK}, {Status: http.StatusNotFound}}
	want := Evaluate(tt, obs, nil)
	got := Evaluate(tt, []Observation{timed(obs[0]), timed(obs[1])}, nil)
	if !slices.Equal(want, got) {
		t.Fatalf("timings changed the failures: %v, want %v", got, want)
	}
	if Verdict(got) != Verdict(want) || Verdict(got) != results.Fail {
		t.Errorf("timings changed the verdict: %s, want %s", Verdict(got), Verdict(want))
	}
}

func TestVerdict(t *testing.T) {
	if Verdict(nil) != results.Pass {
		t.Error("no failures must pass")
	}
	if Verdict([]Failure{{Step: 2}, {Step: 1, Arrange: true}}) != results.Inconclusive {
		t.Error("a failed arrange step must be inconclusive")
	}
}

func TestMatchLineBadPattern(t *testing.T) {
	if ok, msg := MatchLine("Age: ~[", http.Header{age: {"1"}}); ok || !strings.Contains(msg, "invalid pattern") {
		t.Errorf("MatchLine = %v, %q", ok, msg)
	}
}
