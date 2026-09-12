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

package protocol

import (
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	empty         = ""
	plainText     = "plain"
	explicitBody  = "abc"
	generatedBody = "0123456789abcdefghijklmnopqrstuvwxyz01"
	pathID        = "x"
)

var testNow = time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)

var testScript = Script{
	Responses: map[string]Response{"a": {Status: http.StatusCreated}, "b": {Status: http.StatusAccepted}},
	Steps:     map[int]string{1: "a", 3: "b"},
	Default:   "b",
}

var responseForCases = map[int]string{0: "b", 1: "a", 2: "b", 3: "b"}

func TestSplitLine(t *testing.T) {
	cases := []struct {
		in, name, value string
		has             bool
	}{
		{"Cache-Control: max-age=60", "Cache-Control", "max-age=60", true},
		{"Via", "Via", empty, false},
		{"X-Empty:", "X-Empty", empty, true},
		{" Name :  spaced value \t", "Name", "spaced value", true},
		{"Link: <a:b>; rel=x", "Link", "<a:b>; rel=x", true},
	}
	for _, c := range cases {
		name, value, has := SplitLine(c.in)
		if name != c.name || value != c.value || has != c.has {
			t.Errorf("SplitLine(%q) = %q, %q, %v", c.in, name, value, has)
		}
	}
}

func TestExpand(t *testing.T) {
	cases := map[string]string{
		plainText:        plainText,
		"${now}":         "Fri, 11 Sep 2026 12:00:00 GMT",
		"${now+1h}":      "Fri, 11 Sep 2026 13:00:00 GMT",
		"a ${now-90s} b": "a Fri, 11 Sep 2026 11:58:30 GMT b",
		"${nope}":        "${nope}",
	}
	for in, want := range cases {
		if got := Expand(in, testNow); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCheckTemplates(t *testing.T) {
	for _, ok := range []string{empty, plainText, "${now}", "x ${now+1h} y ${now-2m30s}"} {
		if err := CheckTemplates(ok); err != nil {
			t.Errorf("CheckTemplates(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"${now", "${later}", "${now*5}", "${now+5x}"} {
		if CheckTemplates(bad) == nil {
			t.Errorf("CheckTemplates(%q) succeeded", bad)
		}
	}
}

func TestResponseBody(t *testing.T) {
	cases := []struct {
		r    Response
		want string
	}{
		{Response{Body: explicitBody, BodySize: len(generatedBody)}, explicitBody},
		{Response{BodySize: len(generatedBody)}, generatedBody},
		{Response{}, empty},
	}
	for _, c := range cases {
		if got := string(ResponseBody(c.r)); got != c.want {
			t.Errorf("ResponseBody(%+v) = %q, want %q", c.r, got, c.want)
		}
	}
}

func TestScriptResponseFor(t *testing.T) {
	for step, want := range responseForCases {
		name, r := testScript.ResponseFor(step)
		if name != want || !reflect.DeepEqual(r, testScript.Responses[want]) {
			t.Errorf("ResponseFor(%d) = %q, %+v, want %q", step, name, r, want)
		}
	}
}

func TestEmptyScriptResponseFor(t *testing.T) {
	var none Script
	for step := range responseForCases {
		if name, r := none.ResponseFor(step); name != empty || !reflect.DeepEqual(r, Response{}) {
			t.Errorf("ResponseFor(%d) = %q, %+v", step, name, r)
		}
	}
}

func TestNewID(t *testing.T) {
	id := NewID()
	if !ValidID(id) {
		t.Fatalf("NewID() = %q is not valid", id)
	}
	if NewID() == id {
		t.Error("NewID returned the same ID twice")
	}
}

func TestValidID(t *testing.T) {
	for _, bad := range []string{empty, "abc123", strings.Repeat("g", idLen), strings.Repeat("A", idLen)} {
		if ValidID(bad) {
			t.Errorf("ValidID(%q) = true", bad)
		}
	}
}

func TestTestPath(t *testing.T) {
	cases := []struct{ file, want string }{
		{empty, "/t/x"},
		{"/a/b", "/t/x/a/b"},
		{"a", "/t/x/a"},
	}
	for _, c := range cases {
		if got := TestPath(pathID, c.file); got != c.want {
			t.Errorf("TestPath(%q, %q) = %q, want %q", pathID, c.file, got, c.want)
		}
	}
}
