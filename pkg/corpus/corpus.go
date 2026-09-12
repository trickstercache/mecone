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

// Package corpus loads Mecone test suites from a filesystem, validates them, and selects tests to run.
package corpus

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/trickstercache/mecone/pkg/testdef"

	"go.yaml.in/yaml/v3"
)

// SuiteFile is the file in each suite directory that describes the suite.
const SuiteFile = "suite.yaml"

const (
	none    = 0
	yamlExt = ".yaml"
)

type visitState int

const (
	visiting visitState = iota + 1
	visited
)

// Suite is a named collection of test groups, loaded from one directory.
type Suite struct {
	ID          string   `yaml:"id"`
	Title       string   `yaml:"title"`
	Description string   `yaml:"description,omitempty"`
	Groups      []*Group `yaml:"-"`
}

// Group is one file of related tests.
type Group struct {
	ID          string          `yaml:"group"`
	Title       string          `yaml:"title"`
	Description string          `yaml:"description,omitempty"`
	Refs        []testdef.Ref   `yaml:"refs,omitempty"`
	Tests       []*testdef.Test `yaml:"tests"`
}

// Corpus is a validated, indexed set of suites.
type Corpus struct {
	suites []*Suite
	tests  []*testdef.Test
	byID   map[string]*testdef.Test
}

// Load reads every suite directory at the root of fsys and validates all of its tests.
func Load(fsys fs.FS) (*Corpus, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	c := &Corpus{byID: make(map[string]*testdef.Test)}
	if err := c.loadAll(fsys, entries); err != nil {
		return nil, err
	}
	if len(c.suites) == none {
		return nil, errors.New("no suites found")
	}
	return c, nil
}

// Suites returns the loaded suites in directory order.
func (c *Corpus) Suites() []*Suite {
	return c.suites
}

// Tests returns every test in load order.
func (c *Corpus) Tests() []*testdef.Test {
	return c.tests
}

// Test returns the test with the given ID.
func (c *Corpus) Test(id string) (*testdef.Test, bool) {
	t, ok := c.byID[id]
	return t, ok
}

func (c *Corpus) loadAll(fsys fs.FS, entries []fs.DirEntry) error {
	var errs []error
	for _, de := range entries {
		if de.IsDir() {
			errs = append(errs, c.loadDir(fsys, de.Name())...)
		}
	}
	if len(errs) == none {
		errs = c.checkRequires()
	}
	return errors.Join(errs...)
}

func (c *Corpus) loadDir(fsys fs.FS, dir string) []error {
	s, err := loadSuite(fsys, dir)
	if err != nil {
		return []error{err}
	}
	return c.add(s)
}

func loadSuite(fsys fs.FS, dir string) (*Suite, error) {
	name := path.Join(dir, SuiteFile)
	s := new(Suite)
	if err := decode(fsys, name, s); err != nil {
		return nil, err
	}
	if s.ID != dir {
		return nil, fmt.Errorf("%s: id %q must match its directory name", name, s.ID)
	}
	if blank(s.Title) {
		return nil, fmt.Errorf("%s: title is required", name)
	}
	groups, err := loadGroups(fsys, dir)
	if err != nil {
		return nil, err
	}
	s.Groups = groups
	return s, nil
}

func loadGroups(fsys fs.FS, dir string) ([]*Group, error) {
	files, err := fs.Glob(fsys, path.Join(dir, "*"+yamlExt))
	if err != nil {
		return nil, err
	}
	var groups []*Group
	for _, f := range files {
		if path.Base(f) == SuiteFile {
			continue
		}
		g, err := loadGroup(fsys, f)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func loadGroup(fsys fs.FS, name string) (*Group, error) {
	g := new(Group)
	if err := decode(fsys, name, g); err != nil {
		return nil, err
	}
	if want := strings.TrimSuffix(path.Base(name), yamlExt); g.ID != want {
		return nil, fmt.Errorf("%s: group %q must match its file name %q", name, g.ID, want)
	}
	return g, nil
}

func decode(fsys fs.FS, name string, v any) error {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	// Strict decoding makes a misspelled field an error rather than silently ignored.
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func blank(s string) bool {
	return strings.TrimSpace(s) == ""
}

func (c *Corpus) add(s *Suite) []error {
	var errs []error
	if !testdef.ValidID(s.ID) {
		errs = append(errs, fmt.Errorf("suite %q: id must be lowercase kebab-case", s.ID))
	}
	for _, g := range s.Groups {
		errs = append(errs, c.addGroup(s, g)...)
	}
	c.suites = append(c.suites, s)
	return errs
}

func (c *Corpus) addGroup(s *Suite, g *Group) []error {
	where := s.ID + "/" + g.ID
	errs := checkGroup(where, g)
	for _, t := range g.Tests {
		if err := c.addTest(where, s, g, t); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func checkGroup(where string, g *Group) []error {
	var errs []error
	if !testdef.ValidID(g.ID) {
		errs = append(errs, fmt.Errorf("%s: group id must be lowercase kebab-case", where))
	}
	if blank(g.Title) {
		errs = append(errs, fmt.Errorf("%s: title is required", where))
	}
	for _, r := range g.Refs {
		if !r.Valid() {
			errs = append(errs, fmt.Errorf("%s: ref %q must look like RFC9111#5.2.2.5", where, r))
		}
	}
	return errs
}

func (c *Corpus) addTest(where string, s *Suite, g *Group, t *testdef.Test) error {
	if t == nil {
		return fmt.Errorf("%s: empty test entry", where)
	}
	t.Suite, t.Group = s.ID, g.ID
	if err := t.Validate(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	if len(t.Refs) == none {
		if len(g.Refs) == none {
			return fmt.Errorf("%s: test %q cites no refs; add refs to the test or its group", where, t.ID)
		}
		t.Refs = slices.Clone(g.Refs)
	}
	if prev, dup := c.byID[t.ID]; dup {
		return fmt.Errorf("%s: test id %q is already used in %s/%s", where, t.ID, prev.Suite, prev.Group)
	}
	c.byID[t.ID] = t
	c.tests = append(c.tests, t)
	return nil
}

func (c *Corpus) checkRequires() []error {
	if errs := c.unknownRequires(); len(errs) > none {
		return errs
	}
	state := make(map[string]visitState, len(c.tests))
	for _, t := range c.tests {
		if err := c.visit(t, state); err != nil {
			return []error{err}
		}
	}
	return nil
}

func (c *Corpus) unknownRequires() []error {
	var errs []error
	for _, t := range c.tests {
		for _, dep := range t.Requires {
			if _, ok := c.byID[dep]; !ok {
				errs = append(errs, fmt.Errorf("test %q requires unknown test %q", t.ID, dep))
			}
		}
	}
	return errs
}

func (c *Corpus) visit(t *testdef.Test, state map[string]visitState) error {
	switch state[t.ID] {
	case visiting:
		return fmt.Errorf("test %q is part of a requires cycle", t.ID)
	case visited:
		return nil
	default:
		state[t.ID] = visiting
	}
	for _, dep := range t.Requires {
		if err := c.visit(c.byID[dep], state); err != nil {
			return err
		}
	}
	state[t.ID] = visited
	return nil
}

// Filter selects tests. Empty fields match everything; IDs may be path.Match patterns.
type Filter struct {
	Suites []string
	IDs    []string
	Levels []testdef.Level
}

func (f Filter) matches(t *testdef.Test) bool {
	if len(f.Suites) > none && !slices.Contains(f.Suites, t.Suite) {
		return false
	}
	if len(f.Levels) > none && !slices.Contains(f.Levels, t.Level) {
		return false
	}
	if len(f.IDs) == none {
		return true
	}
	return slices.ContainsFunc(f.IDs, func(p string) bool {
		ok, _ := path.Match(p, t.ID)
		return ok
	})
}

// Select returns the tests matching f, plus every test they require, in load order.
func (c *Corpus) Select(f Filter) ([]*testdef.Test, error) {
	if err := c.checkFilter(f); err != nil {
		return nil, err
	}
	chosen := c.choose(f)
	var out []*testdef.Test
	for _, t := range c.tests {
		if chosen[t.ID] {
			out = append(out, t)
		}
	}
	if len(out) == none {
		return nil, errors.New("no tests match the selection")
	}
	return out, nil
}

func (c *Corpus) checkFilter(f Filter) error {
	for _, id := range f.Suites {
		if !c.hasSuite(id) {
			return fmt.Errorf("unknown suite %q", id)
		}
	}
	for _, p := range f.IDs {
		if _, err := path.Match(p, ""); err != nil {
			return fmt.Errorf("bad test pattern %q: %w", p, err)
		}
	}
	return nil
}

func (c *Corpus) hasSuite(id string) bool {
	return slices.ContainsFunc(c.suites, func(s *Suite) bool { return s.ID == id })
}

func (c *Corpus) choose(f Filter) map[string]bool {
	chosen := make(map[string]bool)
	for _, t := range c.tests {
		if f.matches(t) {
			c.include(t, chosen)
		}
	}
	return chosen
}

func (c *Corpus) include(t *testdef.Test, chosen map[string]bool) {
	if chosen[t.ID] {
		return
	}
	chosen[t.ID] = true
	for _, dep := range t.Requires {
		c.include(c.byID[dep], chosen)
	}
}
