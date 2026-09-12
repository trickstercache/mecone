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

// Package protocol is the wire contract shared by the Mecone client and origin:
// URL paths, header names and the JSON documents exchanged over the control plane.
package protocol

// Version is the control-plane protocol version; a client refuses an origin reporting another.
const Version = 1

const (
	// ControlPrefix is the path prefix of every control-plane endpoint on the origin.
	ControlPrefix = "/_mecone/v1/"
	// InfoPath serves the origin's Info document.
	InfoPath = ControlPrefix + "info"
	// ScriptsPath prefixes script uploads (PUT) and removals (DELETE): ScriptsPath + test ID.
	ScriptsPath = ControlPrefix + "scripts/"
	// LogsPath prefixes request log retrieval (GET): LogsPath + test ID.
	LogsPath = ControlPrefix + "logs/"
	// TestPrefix prefixes test traffic: TestPrefix + test ID, optionally followed by a path.
	TestPrefix = "/t/"
)

const (
	// HeaderStep carries the 1-based step number of a client request, so the origin can pick its response.
	HeaderStep = "Mecone-Step"
	// HeaderResponse names the scripted response the origin served.
	HeaderResponse = "Mecone-Response"
	// HeaderOriginSeq is the arrival order of the request at the origin, among requests for the same test.
	HeaderOriginSeq = "Mecone-Origin-Seq"
	// HeaderOriginTime is the origin clock when the response was generated, in Unix milliseconds.
	HeaderOriginTime = "Mecone-Origin-Time"
)

// Info describes an origin to a client during the control-plane handshake.
type Info struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Protocol  int      `json:"protocol"`
	Listeners []string `json:"listeners,omitempty"`
}

// Script tells the origin how to answer one test's requests: a set of named responses and which
// one answers each step. Default answers requests that carry no recognized step number.
type Script struct {
	Responses map[string]Response `json:"responses,omitempty"`
	Steps     map[int]string      `json:"steps,omitempty"`
	Default   string              `json:"default,omitempty"`
}

// ResponseFor returns the name and definition of the response for a step. An unknown name yields
// the zero Response, which the origin serves as an empty 200.
func (s *Script) ResponseFor(step int) (string, Response) {
	name, ok := s.Steps[step]
	if !ok {
		name = s.Default
	}
	return name, s.Responses[name]
}

// Response is one scripted origin response. Header and trailer entries are "Name: value" lines
// whose values may contain the date templates understood by Expand.
type Response struct {
	Status             int       `json:"status,omitempty"`
	Headers            []string  `json:"headers,omitempty"`
	Body               string    `json:"body,omitempty"`
	BodySize           int       `json:"body_size,omitempty"`
	Interim            []Interim `json:"interim,omitempty"`
	Trailers           []string  `json:"trailers,omitempty"`
	IgnoreConditionals bool      `json:"ignore_conditionals,omitempty"`
	Ranges             bool      `json:"ranges,omitempty"`
	DelayMS            int64     `json:"delay_ms,omitempty"`
	Disconnect         bool      `json:"disconnect,omitempty"`
}

// Interim is a 1xx informational response sent before the final response.
type Interim struct {
	Status  int      `json:"status"`
	Headers []string `json:"headers,omitempty"`
}

const (
	bodyAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"
	noBody       = 0
)

// ResponseBody returns the body the origin sends for r: Body when set, otherwise BodySize bytes
// cycling through 0-9a-z, so any byte range of it is predictable.
func ResponseBody(r Response) []byte {
	if r.Body != "" || r.BodySize <= noBody {
		return []byte(r.Body)
	}
	b := make([]byte, r.BodySize)
	for i := range b {
		b[i] = bodyAlphabet[i%len(bodyAlphabet)]
	}
	return b
}
