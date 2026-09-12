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
	"fmt"
	"net/http"
	"strings"
	"time"
)

const (
	templateOpen  = "${"
	templateClose = "}"
	templateNow   = "now"
)

// SplitLine splits a "Name: value" header line; hasValue is false when the line has no colon.
func SplitLine(line string) (name, value string, hasValue bool) {
	name, value, hasValue = strings.Cut(line, ":")
	return strings.TrimSpace(name), strings.Trim(value, " \t"), hasValue
}

// Expand replaces each ${now}, ${now+DURATION} or ${now-DURATION} in v with the HTTP-date that far
// from now. Malformed templates are left untouched; CheckTemplates reports them.
func Expand(v string, now time.Time) string {
	out, err := expandTemplates(v, now)
	if err != nil {
		return v
	}
	return out
}

// CheckTemplates returns an error when v contains a malformed template.
func CheckTemplates(v string) error {
	_, err := expandTemplates(v, time.Time{})
	return err
}

func expandTemplates(v string, now time.Time) (string, error) {
	before, rest, found := strings.Cut(v, templateOpen)
	if !found {
		return v, nil
	}
	out := []byte(before)
	for found {
		name, after, closed := strings.Cut(rest, templateClose)
		if !closed {
			return "", fmt.Errorf("unterminated template in %q", v)
		}
		d, err := parseTemplate(name)
		if err != nil {
			return "", err
		}
		out = now.Add(d).UTC().AppendFormat(out, http.TimeFormat)
		before, rest, found = strings.Cut(after, templateOpen)
		out = append(out, before...)
	}
	return string(out), nil
}

func parseTemplate(t string) (offset time.Duration, err error) {
	if t == templateNow {
		return offset, nil
	}
	rest, ok := strings.CutPrefix(t, templateNow)
	switch {
	case !ok:
		return offset, fmt.Errorf("unknown template ${%s}", t)
	case !strings.HasPrefix(rest, "+") && !strings.HasPrefix(rest, "-"):
		return offset, fmt.Errorf("template ${%s}: offset must start with + or -", t)
	}
	offset, err = time.ParseDuration(rest)
	if err != nil {
		return offset, fmt.Errorf("template ${%s}: %w", t, err)
	}
	return offset, nil
}
