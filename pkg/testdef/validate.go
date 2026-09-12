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

package testdef

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/protocol"
)

const (
	none  = 0
	unset = ""

	oneResponse = 1
	maxBodySize = 16 << 20
	minBodySize = 0

	noDelay Duration = 0

	noStatus         = 0
	minStatus        = http.StatusContinue
	maxInterimStatus = 199
	minFinalStatus   = http.StatusOK
	maxStatus        = 999

	tcharPunctuation = "!#$%&'*+-.^_`|~"
)

var idPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// ValidID reports whether id is a well-formed suite, group, test or response ID (lowercase kebab-case).
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Validate reports every problem with the test definition as one joined error.
func (t *Test) Validate() error {
	errs := t.validateIdentity()
	errs = append(errs, t.validatePrerequisites()...)
	errs = append(errs, t.validateResponses()...)
	errs = append(errs, t.validateSteps()...)
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("test %q: %w", t.ID, err)
	}
	return nil
}

func (t *Test) validateIdentity() []error {
	var errs []error
	if !ValidID(t.ID) {
		errs = append(errs, fmt.Errorf("id %q must be lowercase kebab-case", t.ID))
	}
	if strings.TrimSpace(t.Title) == unset {
		errs = append(errs, errors.New("title is required"))
	}
	if !slices.Contains(Levels, t.Level) {
		errs = append(errs, fmt.Errorf("level %q must be one of must, should, may, info", t.Level))
	}
	for _, r := range t.Refs {
		if !r.Valid() {
			errs = append(errs, fmt.Errorf("ref %q must look like RFC9111#5.2.2.5", r))
		}
	}
	return errs
}

func (t *Test) validatePrerequisites() []error {
	var errs []error
	for _, p := range t.Protocols {
		if _, err := proto.Parse(string(p)); err != nil {
			errs = append(errs, fmt.Errorf("protocols: %w", err))
		}
	}
	for _, dep := range t.Requires {
		if dep == t.ID || !ValidID(dep) {
			errs = append(errs, fmt.Errorf("requires: %q is not a valid dependency", dep))
		}
	}
	return errs
}

func (t *Test) validateResponses() []error {
	var errs []error
	for name, r := range t.Responses {
		if !ValidID(name) {
			errs = append(errs, fmt.Errorf("response name %q must be lowercase kebab-case", name))
		}
		for _, err := range r.validate() {
			errs = append(errs, fmt.Errorf("response %s: %w", name, err))
		}
	}
	return errs
}

func (t *Test) validateSteps() []error {
	var errs []error
	for i, s := range t.Steps {
		for _, err := range s.validate(t) {
			errs = append(errs, fmt.Errorf("step %d: %w", stepNumber(i), err))
		}
	}
	if !slices.ContainsFunc(t.Steps, (*Step).isRequest) {
		errs = append(errs, errors.New("at least one request step is required"))
	}
	return errs
}

func (r *Response) validate() []error {
	if r == nil {
		return []error{errors.New("response is empty")}
	}
	var errs []error
	if r.Status != noStatus && !inRange(r.Status, minFinalStatus, maxStatus) {
		errs = append(errs, fmt.Errorf("status %d must be %d-%d; send 1xx responses with interim", r.Status, minFinalStatus, maxStatus))
	}
	errs = append(errs, r.validateBody()...)
	if r.Ranges && r.Status != noStatus && r.Status != http.StatusOK {
		errs = append(errs, errors.New("ranges applies only to 200 responses"))
	}
	if r.Delay < noDelay {
		errs = append(errs, errors.New("delay must not be negative"))
	}
	errs = append(errs, checkLines("headers", r.Headers, sendLine)...)
	errs = append(errs, checkLines("trailers", r.Trailers, sendLine)...)
	return append(errs, r.validateInterim()...)
}

func (r *Response) validateBody() []error {
	var errs []error
	if r.Body != unset && r.BodySize != none {
		errs = append(errs, errors.New("body and body_size are mutually exclusive"))
	}
	if !inRange(r.BodySize, minBodySize, maxBodySize) {
		errs = append(errs, fmt.Errorf("body_size %d must be %d-%d", r.BodySize, minBodySize, maxBodySize))
	}
	return errs
}

func (r *Response) validateInterim() []error {
	var errs []error
	for _, in := range r.Interim {
		if !inRange(in.Status, minStatus, maxInterimStatus) || in.Status == http.StatusSwitchingProtocols {
			errs = append(errs, fmt.Errorf("interim status %d must be 1xx other than 101", in.Status))
		}
		errs = append(errs, checkLines("interim headers", in.Headers, sendLine)...)
	}
	return errs
}

func (s *Step) isRequest() bool {
	return s != nil && !s.IsWait()
}

func (s *Step) validate(t *Test) []error {
	switch {
	case s == nil:
		return []error{errors.New("step is empty")}
	case s.IsWait():
		return s.validateWait()
	default:
		return s.validateRequest(t)
	}
}

func (s *Step) validateWait() []error {
	if *s.Wait <= noDelay {
		return []error{errors.New("wait must be positive")}
	}
	if s.sendsRequest() {
		return []error{errors.New("a wait step must not also send a request")}
	}
	return nil
}

func (s *Step) sendsRequest() bool {
	return s.Parallel || s.Method != unset || s.Path != unset || s.Query != unset || s.Body != unset || len(s.Headers) > none ||
		s.RespondWith != unset || s.Arrange || s.Expect != nil
}

func (s *Step) validateRequest(t *Test) []error {
	var errs []error
	if s.Method != unset && !validToken(s.Method) {
		errs = append(errs, fmt.Errorf("method %q is not a valid token", s.Method))
	}
	if s.Path != unset && !validPath(s.Path) {
		errs = append(errs, fmt.Errorf("path %q must start with / and contain no '?', '#' or spaces", s.Path))
	}
	if strings.ContainsAny(s.Query, "# ") {
		errs = append(errs, fmt.Errorf("query %q must not contain '#' or spaces", s.Query))
	}
	errs = append(errs, checkLines("headers", s.Headers, sendLine)...)
	errs = append(errs, s.validateRespondWith(t)...)
	if s.Expect != nil {
		errs = append(errs, s.Expect.validate()...)
	}
	return errs
}

func (s *Step) validateRespondWith(t *Test) []error {
	_, known := t.Responses[s.RespondWith]
	switch {
	case s.RespondWith != unset && !known:
		return []error{fmt.Errorf("respond_with names unknown response %q", s.RespondWith)}
	case s.RespondWith == unset && len(t.Responses) > oneResponse:
		return []error{errors.New("respond_with is required when a test defines more than one response")}
	default:
		return nil
	}
}

func (e *Expect) validate() []error {
	errs := e.validateStatus()
	if e.Forwarded != nil && !*e.Forwarded && e.hasForwardedHeaders() {
		errs = append(errs, errors.New("forwarded header expectations contradict forwarded: false"))
	}
	errs = append(errs, checkLines("expect headers", e.Headers, expectLine)...)
	errs = append(errs, checkLines("expect trailers", e.Trailers, expectLine)...)
	errs = append(errs, checkLines("expect forwarded_headers", e.ForwardedHeaders, expectLine)...)
	errs = append(errs, checkLines("expect headers_absent", e.HeadersAbsent, nameLine)...)
	errs = append(errs, checkLines("expect forwarded_headers_absent", e.ForwardedHeadersAbsent, nameLine)...)
	return errs
}

func (e *Expect) validateStatus() []error {
	var errs []error
	for _, c := range e.Status {
		if !inRange(c, minStatus, maxStatus) {
			errs = append(errs, fmt.Errorf("expected status %d is out of range", c))
		}
	}
	for _, c := range e.Interim {
		if !inRange(c, minStatus, maxInterimStatus) {
			errs = append(errs, fmt.Errorf("expected interim status %d must be 1xx", c))
		}
	}
	return errs
}

func (e *Expect) hasForwardedHeaders() bool {
	return len(e.ForwardedHeaders) > none || len(e.ForwardedHeadersAbsent) > none
}

type lineKind int

const (
	sendLine lineKind = iota
	expectLine
	nameLine
)

func checkLines(field string, lines []string, kind lineKind) []error {
	var errs []error
	for _, line := range lines {
		if err := checkLine(line, kind); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", field, err))
		}
	}
	return errs
}

func checkLine(line string, kind lineKind) error {
	name, value, hasValue := protocol.SplitLine(line)
	if !validToken(name) {
		return fmt.Errorf("%q does not start with a valid field name", line)
	}
	switch kind {
	case sendLine:
		if !hasValue {
			return fmt.Errorf("%q needs a value (Name: value)", line)
		}
		return protocol.CheckTemplates(value)
	case expectLine:
		return checkPattern(line, value)
	case nameLine:
		if hasValue {
			return fmt.Errorf("%q must be a field name only", line)
		}
		return nil
	default:
		return fmt.Errorf("%q has unknown line kind %d", line, kind)
	}
}

func checkPattern(line, value string) error {
	pattern, isPattern := strings.CutPrefix(value, "~")
	if !isPattern {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("%q: %w", line, err)
	}
	return nil
}

func validPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "?# ")
}

func inRange(v, lo, hi int) bool {
	return lo <= v && v <= hi
}

func validToken(s string) bool {
	if s == unset {
		return false
	}
	for i := range len(s) {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9':
		case strings.ContainsRune(tcharPunctuation, rune(c)):
		default:
			return false
		}
	}
	return true
}
