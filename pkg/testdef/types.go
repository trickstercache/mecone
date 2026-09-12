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
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Level is the RFC 2119 requirement level a test exercises, which decides how it is scored.
type Level string

const (
	// LevelMust tests an absolute requirement (MUST, MUST NOT, REQUIRED, SHALL).
	LevelMust Level = "must"
	// LevelShould tests a recommendation (SHOULD, SHOULD NOT, RECOMMENDED).
	LevelShould Level = "should"
	// LevelMay tests an optional capability (MAY, OPTIONAL) that an implementation may offer.
	LevelMay Level = "may"
	// LevelInfo observes behavior without judging it; it is reported but never scored.
	LevelInfo Level = "info"
)

// Levels lists every level from most to least significant.
var Levels = []Level{LevelMust, LevelShould, LevelMay, LevelInfo}

// Duration is a time.Duration written in Go duration syntax, such as "1500ms" or "3s".
type Duration time.Duration

// UnmarshalYAML decodes a duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: a duration must be a string such as \"3s\"", n.Line)
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: %w", n.Line, err)
	}
	*d = Duration(v)
	return nil
}

// StatusSet is one or more acceptable status codes, written as a number or a list of numbers.
type StatusSet []int

// UnmarshalYAML decodes a status code or a list of them.
func (s *StatusSet) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		var code int
		if err := n.Decode(&code); err != nil {
			return err
		}
		*s = StatusSet{code}
		return nil
	}
	var codes []int
	if err := n.Decode(&codes); err != nil {
		return err
	}
	*s = codes
	return nil
}

// Contains reports whether code is in the set.
func (s StatusSet) Contains(code int) bool {
	return slices.Contains(s, code)
}

// Ref cites a specification section as RFC<number>#<section>, such as RFC9111#5.2.2.5.
type Ref string

var refPattern = regexp.MustCompile(`^RFC[1-9][0-9]*(#[0-9A-Za-z]([0-9A-Za-z.-]*[0-9A-Za-z])?)?$`)

// Valid reports whether the reference is well formed.
func (r Ref) Valid() bool {
	return refPattern.MatchString(string(r))
}

// URL returns the RFC Editor URL of the cited document and section.
func (r Ref) URL() string {
	num, section, _ := strings.Cut(strings.TrimPrefix(string(r), "RFC"), "#")
	u := "https://www.rfc-editor.org/rfc/rfc" + num
	if section != "" {
		u += "#section-" + section
	}
	return u
}
