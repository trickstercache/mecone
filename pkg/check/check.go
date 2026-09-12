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

// Package check evaluates a test's expectations against what the client observed and what the
// origin logged.
package check

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/trickstercache/mecone/pkg/protocol"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
)

// Observation is what the client saw in response to one request step.
type Observation struct {
	Status  int
	Header  http.Header
	Trailer http.Header
	Interim []int
	Body    []byte
	Proto   string
	Err     error
	// TTFB is the time until the first response byte, Total the time until the body was fully read
	// and Reused whether the connection was already open. Timings are reported, never judged.
	TTFB   time.Duration
	Total  time.Duration
	Reused bool
}

// Failure is one unmet expectation.
type Failure struct {
	Step    int
	Arrange bool
	Message string
}

// String formats the failure for people.
func (f Failure) String() string {
	return "step " + strconv.Itoa(f.Step) + ": " + f.Message
}

const (
	none    = 0
	first   = 0
	matched = ""
)

// Evaluate checks every request step of t; obs is indexed like t.Steps.
func Evaluate(t *testdef.Test, obs []Observation, log protocol.Log) []Failure {
	var fails []Failure
	for i, o := range obs[:min(len(obs), len(t.Steps))] {
		s := t.Steps[i]
		if s.IsWait() {
			continue
		}
		for _, msg := range evaluateStep(s.Expect, o, log.Step(i+1)) {
			fails = append(fails, Failure{Step: i + 1, Arrange: s.Arrange, Message: msg})
		}
	}
	return fails
}

// Verdict turns failures into an outcome; a failed arrange step makes the result inconclusive.
func Verdict(fails []Failure) results.Outcome {
	outcome := results.Pass
	for _, f := range fails {
		if f.Arrange {
			return results.Inconclusive
		}
		outcome = results.Fail
	}
	return outcome
}

func evaluateStep(e *testdef.Expect, o Observation, forwarded []protocol.LogEntry) []string {
	if o.Err != nil {
		return []string{"request failed: " + o.Err.Error()}
	}
	if e == nil {
		return nil
	}
	return slices.Concat(
		checkStatus(e.Status, o.Status),
		matchAll("response", e.Headers, o.Header),
		absentAll("response", e.HeadersAbsent, o.Header),
		matchAll("trailer", e.Trailers, o.Trailer),
		checkBody(e.Body, o.Body),
		checkInterim(e.Interim, o.Interim),
		checkForwarded(e.Forwarded, forwarded),
		checkForwardedHeaders(e, forwarded),
	)
}

func checkStatus(want testdef.StatusSet, got int) []string {
	if len(want) == none || want.Contains(got) {
		return nil
	}
	return []string{fmt.Sprintf("status is %d, want %s", got, statusList(want))}
}

func checkBody(want *string, got []byte) []string {
	if want == nil || string(got) == *want {
		return nil
	}
	return []string{fmt.Sprintf("body is %s, want %q", preview(got), *want)}
}

func checkInterim(want, got []int) []string {
	if inOrder(got, want) {
		return nil
	}
	return []string{fmt.Sprintf("1xx responses received were %v, want %v in that order", got, want)}
}

func checkForwarded(want *bool, forwarded []protocol.LogEntry) []string {
	switch {
	case want == nil:
		return nil
	case *want && len(forwarded) == none:
		return []string{"the proxy answered without contacting the origin, but it must forward this request"}
	case !*want && len(forwarded) > none:
		return []string{fmt.Sprintf("the origin received this request %d time(s), but the proxy must answer it itself", len(forwarded))}
	default:
		return nil
	}
}

func checkForwardedHeaders(e *testdef.Expect, forwarded []protocol.LogEntry) []string {
	if len(e.ForwardedHeaders)+len(e.ForwardedHeadersAbsent) == none {
		return nil
	}
	if len(forwarded) == none {
		return []string{"the request never reached the origin, so its forwarded headers cannot be checked"}
	}
	h := forwarded[first].Header
	return append(matchAll("forwarded", e.ForwardedHeaders, h), absentAll("forwarded", e.ForwardedHeadersAbsent, h)...)
}

// MatchLine reports whether h satisfies one expectation line: "Name" (present), "Name: value"
// (combined field value equals) or "Name: ~regexp" (combined field value matches).
func MatchLine(line string, h http.Header) (bool, string) {
	name, want, hasValue := protocol.SplitLine(line)
	vals := h.Values(name)
	if len(vals) == none {
		return false, name + " is missing"
	}
	if !hasValue {
		return true, matched
	}
	got := strings.Join(vals, ", ")
	if pattern, ok := strings.CutPrefix(want, "~"); ok {
		return matchPattern(name, got, pattern)
	}
	if got == want {
		return true, matched
	}
	return false, fmt.Sprintf("%s is %q, want %q", name, got, want)
}

func matchPattern(name, got, pattern string) (bool, string) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return false, fmt.Sprintf("%s: invalid pattern: %v", name, err)
	}
	if re.MatchString(got) {
		return true, matched
	}
	return false, fmt.Sprintf("%s is %q, want a match for %s", name, got, pattern)
}

func matchAll(where string, lines []string, h http.Header) []string {
	var msgs []string
	for _, line := range lines {
		if ok, msg := MatchLine(line, h); !ok {
			msgs = append(msgs, where+" field "+msg)
		}
	}
	return msgs
}

func absentAll(where string, names []string, h http.Header) []string {
	var msgs []string
	for _, name := range names {
		if vals := h.Values(name); len(vals) > none {
			msgs = append(msgs, fmt.Sprintf("%s field %s is %q, but must be absent", where, name, strings.Join(vals, ", ")))
		}
	}
	return msgs
}

func inOrder(got, want []int) bool {
	var i int
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	return i == len(want)
}

func statusList(codes []int) string {
	parts := make([]string, len(codes))
	for i, c := range codes {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, " or ")
}

func preview(b []byte) string {
	const limit = 64
	if len(b) > limit {
		return fmt.Sprintf("%q... (%d bytes)", b[:limit], len(b))
	}
	return fmt.Sprintf("%q", b)
}
