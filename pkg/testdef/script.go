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
	"time"

	"github.com/trickstercache/mecone/pkg/protocol"
)

const firstStepNumber = 1

// Script returns the origin's instructions for the test. Step numbers are 1-based positions in
// Steps; requests that arrive without a known step get the last request step's response.
func (t *Test) Script() protocol.Script {
	s := protocol.Script{Steps: make(map[int]string), Responses: t.wireResponses()}
	for i, st := range t.Steps {
		if st.IsWait() {
			continue
		}
		if name := t.ResponseFor(st); name != unset {
			s.Steps[stepNumber(i)] = name
			s.Default = name
		}
	}
	return s
}

// ResponseFor returns the name of the response that answers step s, or "" for the origin default.
func (t *Test) ResponseFor(s *Step) string {
	if s.RespondWith != unset || len(t.Responses) != oneResponse {
		return s.RespondWith
	}
	for name := range t.Responses {
		return name
	}
	return unset
}

func stepNumber(index int) int {
	return index + firstStepNumber
}

func (t *Test) wireResponses() map[string]protocol.Response {
	if len(t.Responses) == none {
		return nil
	}
	out := make(map[string]protocol.Response, len(t.Responses))
	for name, r := range t.Responses {
		out[name] = r.wire()
	}
	return out
}

func (r *Response) wire() protocol.Response {
	out := protocol.Response{
		Status:             r.Status,
		Headers:            r.Headers,
		Body:               r.Body,
		BodySize:           r.BodySize,
		Trailers:           r.Trailers,
		IgnoreConditionals: r.IgnoreConditionals,
		Ranges:             r.Ranges,
		DelayMS:            time.Duration(r.Delay).Milliseconds(),
		Disconnect:         r.Disconnect,
	}
	for _, in := range r.Interim {
		out.Interim = append(out.Interim, protocol.Interim{Status: in.Status, Headers: in.Headers})
	}
	return out
}
