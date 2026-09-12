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

package corpus

import (
	"slices"
	"testing"
	"testing/fstest"

	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	suitePath  = "demo/suite.yaml"
	basicsPath = "demo/basics.yaml"
	groupPath  = "demo/g.yaml"
	testOne    = "basics-one"
	testTwo    = "basics-two"
	suiteYAML  = "id: demo\ntitle: Demo\n"
	groupYAML  = `group: basics
title: Basics
refs: [RFC9110#9.3.1]
tests:
  - id: basics-one
    title: One
    level: must
    steps: [{expect: {forwarded: true}}]
  - id: basics-two
    title: Two
    level: may
    requires: [basics-one]
    steps: [{}]
`
)

func mapFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return m
}

func loadDemo(t *testing.T) *Corpus {
	t.Helper()
	c, err := Load(mapFS(map[string]string{suitePath: suiteYAML, basicsPath: groupYAML}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func groupPaths(c *Corpus) []string {
	var out []string
	for _, s := range c.Suites() {
		for _, g := range s.Groups {
			out = append(out, s.ID+"/"+g.ID)
		}
	}
	return out
}

func testIDs(tests []*testdef.Test) []string {
	ids := make([]string, len(tests))
	for i, tt := range tests {
		ids[i] = tt.ID
	}
	return ids
}

func TestLoad(t *testing.T) {
	c, err := Load(mapFS(map[string]string{
		suitePath:   suiteYAML,
		basicsPath:  groupYAML,
		"README.md": "not a suite",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if got := groupPaths(c); !slices.Equal(got, []string{"demo/basics"}) {
		t.Fatalf("loaded groups %v", got)
	}
	if got := testIDs(c.Tests()); !slices.Equal(got, []string{testOne, testTwo}) {
		t.Fatalf("loaded tests %v", got)
	}
	if tt, ok := c.Test(testTwo); !ok || tt.Suite != "demo" || tt.Group != "basics" ||
		!slices.Equal(tt.Refs, []testdef.Ref{"RFC9110#9.3.1"}) {
		t.Errorf("Test(basics-two) = %+v, %v", tt, ok)
	}
}

func TestLoadErrors(t *testing.T) {
	const (
		groupID   = "g"
		testID    = "x"
		requiresY = "    requires: [y]\n"
	)
	group := func(name, body string) string {
		return "group: " + name + "\ntitle: G\nrefs: [RFC9110]\ntests:\n" + body
	}
	test := func(id, extra string) string {
		return "  - id: " + id + "\n    title: T\n    level: must\n    steps: [{}]\n" + extra
	}
	plain := test(testID, "")
	cases := map[string]map[string]string{
		"no suites":         {},
		"missing suite":     {basicsPath: groupYAML},
		"id mismatch":       {suitePath: "id: other\ntitle: X\n"},
		"no suite title":    {suitePath: "id: demo\n"},
		"unknown field":     {suitePath: suiteYAML + "bogus: 1\n"},
		"bad suite id":      {"Demo/suite.yaml": "id: Demo\ntitle: D\n"},
		"group mismatch":    {suitePath: suiteYAML, "demo/wrong.yaml": groupYAML},
		"bad group id":      {suitePath: suiteYAML, "demo/Bad.yaml": group("Bad", plain)},
		"no group title":    {suitePath: suiteYAML, groupPath: "group: g\ntests: []\n"},
		"bad group ref":     {suitePath: suiteYAML, groupPath: "group: g\ntitle: G\nrefs: [nope]\ntests: []\n"},
		"invalid test":      {suitePath: suiteYAML, groupPath: group(groupID, "  - id: x\n    title: T\n    steps: [{}]\n")},
		"null test":         {suitePath: suiteYAML, groupPath: group(groupID, "  - null\n")},
		"no refs":           {suitePath: suiteYAML, groupPath: "group: g\ntitle: G\ntests:\n" + plain},
		"duplicate id":      {suitePath: suiteYAML, "demo/a.yaml": group("a", plain), "demo/b.yaml": group("b", plain)},
		"unknown requires":  {suitePath: suiteYAML, groupPath: group(groupID, test(testID, requiresY))},
		"requires cycle":    {suitePath: suiteYAML, groupPath: group(groupID, test(testID, requiresY)+test("y", "    requires: [x]\n"))},
		"empty group file":  {suitePath: suiteYAML, groupPath: ""},
		"malformed yaml":    {suitePath: suiteYAML, groupPath: "group: [unclosed"},
		"bad step contents": {suitePath: suiteYAML, groupPath: group(groupID, test(testID, "    protocols: [h9]\n"))},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(mapFS(files)); err == nil {
				t.Error("Load succeeded")
			}
		})
	}
}

func TestSelect(t *testing.T) {
	c := loadDemo(t)
	both := []string{testOne, testTwo}
	cases := []struct {
		f    Filter
		want []string
	}{
		{Filter{}, both},
		{Filter{Suites: []string{"demo"}}, both},
		{Filter{IDs: []string{testOne}}, []string{testOne}},
		{Filter{IDs: []string{"*-two"}}, both},
		{Filter{Levels: []testdef.Level{testdef.LevelMay}}, both},
	}
	for _, tc := range cases {
		got, err := c.Select(tc.f)
		if err != nil {
			t.Errorf("Select(%+v): %v", tc.f, err)
			continue
		}
		if ids := testIDs(got); !slices.Equal(ids, tc.want) {
			t.Errorf("Select(%+v) = %v, want %v", tc.f, ids, tc.want)
		}
	}
}

func TestSelectErrors(t *testing.T) {
	c := loadDemo(t)
	for _, f := range []Filter{
		{Suites: []string{"nope"}},
		{IDs: []string{"["}},
		{Levels: []testdef.Level{testdef.LevelInfo}},
	} {
		if _, err := c.Select(f); err == nil || blank(err.Error()) {
			t.Errorf("Select(%+v) succeeded", f)
		}
	}
}
