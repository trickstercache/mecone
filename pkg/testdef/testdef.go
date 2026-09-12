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

// Package testdef defines the schema of Mecone test definitions and validates them.
package testdef

import (
	"slices"

	"github.com/trickstercache/mecone/pkg/proto"
)

// Test is one named, independently runnable exchange between the client, the proxy under test and
// the origin, with the behavior the proxy must exhibit.
type Test struct {
	ID        string               `yaml:"id"`
	Title     string               `yaml:"title"`
	Level     Level                `yaml:"level"`
	Refs      []Ref                `yaml:"refs,omitempty"`
	Protocols []proto.Proto        `yaml:"protocols,omitempty"`
	Requires  []string             `yaml:"requires,omitempty"`
	Responses map[string]*Response `yaml:"responses,omitempty"`
	Steps     []*Step              `yaml:"steps"`

	// Suite and Group identify where the test was loaded from.
	Suite string `yaml:"-"`
	Group string `yaml:"-"`
}

// RunsOver reports whether the test applies to client protocol p.
func (t *Test) RunsOver(p proto.Proto) bool {
	return len(t.Protocols) == none || slices.Contains(t.Protocols, p)
}

// Response is a response the origin gives when a request reaches it. Unless told otherwise, the
// origin answers conditional requests with 304 as RFC 9110 prescribes.
type Response struct {
	Status             int       `yaml:"status,omitempty"`
	Headers            []string  `yaml:"headers,omitempty"`
	Body               string    `yaml:"body,omitempty"`
	BodySize           int       `yaml:"body_size,omitempty"`
	Interim            []Interim `yaml:"interim,omitempty"`
	Trailers           []string  `yaml:"trailers,omitempty"`
	IgnoreConditionals bool      `yaml:"ignore_conditionals,omitempty"`
	Ranges             bool      `yaml:"ranges,omitempty"`
	Delay              Duration  `yaml:"delay,omitempty"`
	Disconnect         bool      `yaml:"disconnect,omitempty"`
}

// Interim is a 1xx response the origin sends before the final response.
type Interim struct {
	Status  int      `yaml:"status"`
	Headers []string `yaml:"headers,omitempty"`
}

// Step is either a pause (Wait) or one request sent through the proxy with its expectations.
type Step struct {
	Wait *Duration `yaml:"wait,omitempty"`
	// Parallel runs this request concurrently with adjacent steps that also set parallel.
	Parallel bool `yaml:"parallel,omitempty"`

	Method  string   `yaml:"method,omitempty"`
	Path    string   `yaml:"path,omitempty"`
	Query   string   `yaml:"query,omitempty"`
	Headers []string `yaml:"headers,omitempty"`
	Body    string   `yaml:"body,omitempty"`

	// RespondWith names the Response the origin gives if this request reaches it.
	RespondWith string `yaml:"respond_with,omitempty"`
	// Arrange marks a step that only establishes state; if its expectations fail the result is inconclusive.
	Arrange bool    `yaml:"arrange,omitempty"`
	Expect  *Expect `yaml:"expect,omitempty"`
}

// IsWait reports whether the step is a pause rather than a request.
func (s *Step) IsWait() bool {
	return s.Wait != nil
}

// Expect is what must be observed after a step's request. Header entries are "Name" (present),
// "Name: value" (combined field value equals) or "Name: ~regexp" (combined value matches).
type Expect struct {
	Status        StatusSet `yaml:"status,omitempty"`
	Headers       []string  `yaml:"headers,omitempty"`
	HeadersAbsent []string  `yaml:"headers_absent,omitempty"`
	Body          *string   `yaml:"body,omitempty"`
	Interim       []int     `yaml:"interim,omitempty"`
	Trailers      []string  `yaml:"trailers,omitempty"`

	// Forwarded says whether the proxy must (true) or must not (false) contact the origin.
	Forwarded              *bool    `yaml:"forwarded,omitempty"`
	ForwardedHeaders       []string `yaml:"forwarded_headers,omitempty"`
	ForwardedHeadersAbsent []string `yaml:"forwarded_headers_absent,omitempty"`
}
