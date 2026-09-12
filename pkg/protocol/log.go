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
	"time"
)

// LogEntry records one test request received by the origin and the response it generated.
type LogEntry struct {
	Seq            int         `json:"seq"`
	Step           int         `json:"step"`
	Response       string      `json:"response,omitempty"`
	Received       time.Time   `json:"received"`
	Method         string      `json:"method"`
	Target         string      `json:"target"`
	Proto          string      `json:"proto"`
	Header         http.Header `json:"header"`
	Status         int         `json:"status"`
	ResponseHeader http.Header `json:"response_header,omitempty"`
}

// Log is the ordered record of test requests the origin received for one test.
type Log []LogEntry

// Step returns the entries for the given 1-based step number, in arrival order.
func (l Log) Step(n int) []LogEntry {
	var out []LogEntry
	for _, e := range l {
		if e.Step == n {
			out = append(out, e)
		}
	}
	return out
}
