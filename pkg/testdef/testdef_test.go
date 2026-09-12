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
	"maps"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/protocol"

	"go.yaml.in/yaml/v3"
)

const sample = `
id: sample-full
title: Everything at once
level: should
refs: [RFC9111#4.2.1]
protocols: [h1, h2]
requires: [other-test]
responses:
  first:
    status: 203
    headers: ["Cache-Control: max-age=60", "Expires: ${now+1m}"]
    body: hello
    interim: [{status: 103, headers: ["Link: </a>"]}]
    trailers: ["X-Sum: 1"]
    delay: 50ms
  second:
    body_size: 64
    ranges: true
    ignore_conditionals: true
steps:
  - method: POST
    path: /thing
    query: a=1
    headers: ["X-Req: 1"]
    body: payload
    respond_with: first
    arrange: true
    expect:
      status: [200, 203]
      headers: [Cache-Control, "Age: ~^[0-9]+$"]
      headers_absent: [Set-Cookie]
      body: hello
      interim: [103]
      trailers: ["X-Sum: 1"]
      forwarded: true
      forwarded_headers: ["X-Req: 1"]
      forwarded_headers_absent: [Connection]
  - wait: 1500ms
  - respond_with: second
    expect: {status: 206}
`

const (
	postStep = iota
	waitStep
	rangeStep
)

const (
	firstResponse  = "first"
	secondResponse = "second"
	onlyResponse   = "only"
	testID         = "a-test"
	unknownLine    = nameLine + 1

	sampleWait     = 1500 * time.Millisecond
	sampleDelay    = 50 * time.Millisecond
	sampleBodySize = 64
)

var invalidTests = map[string]func(*Test){
	"bad id":               func(x *Test) { x.ID = "Bad_ID" },
	"no title":             func(x *Test) { x.Title = " " },
	"bad level":            func(x *Test) { x.Level = "sometimes" },
	"bad ref":              func(x *Test) { x.Refs = []Ref{"rfc1"} },
	"bad proto":            func(x *Test) { x.Protocols = []proto.Proto{"h9"} },
	"self require":         func(x *Test) { x.Requires = []string{testID} },
	"no request steps":     func(x *Test) { x.Steps = []*Step{{Wait: new(Duration(time.Second))}} },
	"nil step":             func(x *Test) { x.Steps = append(x.Steps, nil) },
	"zero wait":            func(x *Test) { x.Steps = append(x.Steps, &Step{Wait: new(noDelay)}) },
	"wait with request":    func(x *Test) { x.Steps = append(x.Steps, &Step{Wait: new(Duration(time.Second)), Path: "/x"}) },
	"bad method":           withStep(func(s *Step) { s.Method = "GE T" }),
	"bad path":             withStep(func(s *Step) { s.Path = "x?y" }),
	"bad query":            withStep(func(s *Step) { s.Query = "a#b" }),
	"header without value": withStep(func(s *Step) { s.Headers = []string{"X-Foo"} }),
	"bad header name":      withStep(func(s *Step) { s.Headers = []string{"X Foo: 1"} }),
	"bad template":         withStep(func(s *Step) { s.Headers = []string{"Date: ${then}"} }),
	"unknown response":     withStep(func(s *Step) { s.RespondWith = "nope" }),
	"ambiguous response":   func(x *Test) { x.Responses = map[string]*Response{firstResponse: {}, secondResponse: {}} },
	"bad response name":    func(x *Test) { x.Responses = map[string]*Response{"A": {}} },
	"nil response":         withResponse(nil),
	"1xx response":         withResponse(&Response{Status: http.StatusEarlyHints}),
	"body and size":        withResponse(&Response{Body: "x", BodySize: 3}),
	"negative size":        withResponse(&Response{BodySize: -1}),
	"oversized body":       withResponse(&Response{BodySize: maxBodySize + sampleBodySize}),
	"ranges on 404":        withResponse(&Response{Status: http.StatusNotFound, Ranges: true}),
	"negative delay":       withResponse(&Response{Delay: -1}),
	"bad interim":          withResponse(&Response{Interim: []Interim{{Status: http.StatusSwitchingProtocols}}}),
	"bad interim header":   withResponse(&Response{Interim: []Interim{{Status: http.StatusEarlyHints, Headers: []string{"Link"}}}}),
	"bad trailer":          withResponse(&Response{Trailers: []string{"X"}}),
	"low expect status":    withExpect(&Expect{Status: StatusSet{42}}),
	"high expect status":   withExpect(&Expect{Status: StatusSet{maxStatus + 1}}),
	"bad expect interim":   withExpect(&Expect{Interim: []int{http.StatusOK}}),
	"contradiction":        withExpect(&Expect{Forwarded: new(false), ForwardedHeaders: []string{"Via"}}),
	"bad regexp":           withExpect(&Expect{Headers: []string{"Age: ~["}}),
	"absent with value":    withExpect(&Expect{HeadersAbsent: []string{"Via: 1"}}),
	"bad expect name":      withExpect(&Expect{ForwardedHeaders: []string{": x"}}),
}

func decodeSample(t *testing.T) *Test {
	t.Helper()
	var tt Test
	dec := yaml.NewDecoder(strings.NewReader(sample))
	dec.KnownFields(true)
	if err := dec.Decode(&tt); err != nil {
		t.Fatal(err)
	}
	return &tt
}

func validTest() *Test {
	return &Test{ID: testID, Title: "A test", Level: LevelMust, Steps: []*Step{{}}}
}

func withStep(mutate func(*Step)) func(*Test) {
	return func(x *Test) {
		for _, s := range x.Steps {
			mutate(s)
		}
	}
}

func withResponse(r *Response) func(*Test) {
	return func(x *Test) { x.Responses = map[string]*Response{onlyResponse: r} }
}

func withExpect(e *Expect) func(*Test) {
	return withStep(func(s *Step) { s.Expect = e })
}

func assertRejected(t *testing.T, target any, docs ...string) {
	t.Helper()
	for _, doc := range docs {
		if err := yaml.Unmarshal([]byte(doc), target); err == nil {
			t.Errorf("%q decoded into %T", doc, target)
		}
	}
}

func TestDecodeAndValidate(t *testing.T) {
	tt := decodeSample(t)
	if err := tt.Validate(); err != nil {
		t.Fatal(err)
	}
	if !tt.RunsOver(proto.H2) || tt.RunsOver(proto.H2C) {
		t.Error("protocols not honored")
	}
	if !(&Test{}).RunsOver(proto.H3) {
		t.Error("a test without protocols runs over every protocol")
	}
}

func TestDecodeSteps(t *testing.T) {
	tt := decodeSample(t)
	post, wait := tt.Steps[postStep], tt.Steps[waitStep]
	if !wait.IsWait() || post.IsWait() || *wait.Wait != Duration(sampleWait) {
		t.Error("wait step not decoded")
	}
	if !post.Expect.Status.Contains(http.StatusNonAuthoritativeInfo) || post.Expect.Status.Contains(http.StatusNotFound) {
		t.Error("status set not decoded")
	}
}

func TestScript(t *testing.T) {
	s := decodeSample(t).Script()
	steps := map[int]string{stepNumber(postStep): firstResponse, stepNumber(rangeStep): secondResponse}
	if !maps.Equal(s.Steps, steps) || s.Default != secondResponse {
		t.Errorf("steps = %v, default %q", s.Steps, s.Default)
	}
	responses := map[string]protocol.Response{
		firstResponse: {
			Status:   http.StatusNonAuthoritativeInfo,
			Headers:  []string{"Cache-Control: max-age=60", "Expires: ${now+1m}"},
			Body:     "hello",
			Interim:  []protocol.Interim{{Status: http.StatusEarlyHints, Headers: []string{"Link: </a>"}}},
			Trailers: []string{"X-Sum: 1"},
			DelayMS:  sampleDelay.Milliseconds(),
		},
		secondResponse: {BodySize: sampleBodySize, IgnoreConditionals: true, Ranges: true},
	}
	if !reflect.DeepEqual(s.Responses, responses) {
		t.Errorf("responses = %+v", s.Responses)
	}
}

func TestScriptDefaults(t *testing.T) {
	single := &Test{Responses: map[string]*Response{onlyResponse: {}}, Steps: []*Step{{}, {}}}
	s := single.Script()
	for i := range single.Steps {
		if got := s.Steps[stepNumber(i)]; got != onlyResponse {
			t.Errorf("step %d answered by %q", stepNumber(i), got)
		}
	}
	if s.Default != onlyResponse {
		t.Errorf("default = %q", s.Default)
	}
	if s := (&Test{Steps: []*Step{{}}}).Script(); len(s.Steps) != none || s.Responses != nil || s.Default != unset {
		t.Errorf("none = %+v", s)
	}
}

func TestYAMLTypes(t *testing.T) {
	var d Duration
	assertRejected(t, &d, "[1s]", "soon")
	var s StatusSet
	assertRejected(t, &s, "abc", "{a: 1}")
	for doc, want := range map[string]int{"204": http.StatusNoContent, "[200, 416]": http.StatusRequestedRangeNotSatisfiable} {
		if err := yaml.Unmarshal([]byte(doc), &s); err != nil || !s.Contains(want) {
			t.Errorf("%s decoded as %v: %v", doc, s, err)
		}
	}
}

func TestRef(t *testing.T) {
	valid := map[Ref]bool{
		"RFC9111": true, "RFC9111#5.2.2.5": true, "RFC9110#appendix-A": true,
		"rfc9111": false, "RFC9111#": false, "RFC0#1": false, "RFC9111#5.": false, "9111": false,
	}
	for ref, want := range valid {
		if ref.Valid() != want {
			t.Errorf("%q valid = %v", ref, !want)
		}
	}
	urls := map[Ref]string{
		"RFC9111#5.2.2.5": "https://www.rfc-editor.org/rfc/rfc9111#section-5.2.2.5",
		"RFC8297":         "https://www.rfc-editor.org/rfc/rfc8297",
	}
	for ref, want := range urls {
		if got := ref.URL(); got != want {
			t.Errorf("%q URL = %q", ref, got)
		}
	}
}

func TestValidateErrors(t *testing.T) {
	if err := validTest().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range invalidTests {
		t.Run(name, func(t *testing.T) {
			x := validTest()
			mutate(x)
			if x.Validate() == nil {
				t.Error("expected a validation error")
			}
		})
	}
}

func TestCheckLineUnknownKind(t *testing.T) {
	if err := checkLine("X-Foo: 1", unknownLine); err == nil {
		t.Error("an unknown line kind was accepted")
	}
}
