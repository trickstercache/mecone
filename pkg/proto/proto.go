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

// Package proto names the HTTP protocol versions Mecone speaks and builds client transports for them.
package proto

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Proto names an HTTP protocol version and its transport.
type Proto string

const (
	// H1 is HTTP/1.1 over cleartext or TLS.
	H1 Proto = "h1"
	// H2 is HTTP/2 over TLS.
	H2 Proto = "h2"
	// H2C is HTTP/2 over cleartext with prior knowledge.
	H2C Proto = "h2c"
	// H3 is HTTP/3 over QUIC.
	H3 Proto = "h3"
)

const (
	http2Major = 2
	http3Major = 3
)

// ErrUnsupported is returned for a protocol Mecone recognizes but cannot speak yet.
var ErrUnsupported = errors.New("protocol not yet supported")

// All lists every recognized protocol.
var All = []Proto{H1, H2, H2C, H3}

// Parse returns the Proto named by s.
func Parse(s string) (Proto, error) {
	p := Proto(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(All, p) {
		return p, nil
	}
	return "", fmt.Errorf("unknown protocol %q (want one of h1, h2, h2c, h3)", s)
}

// ParseList parses a comma-separated list of protocol names, dropping duplicates.
func ParseList(s string) ([]Proto, error) {
	var out []Proto
	for part := range strings.SplitSeq(s, ",") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		p, err := Parse(part)
		if err != nil {
			return nil, err
		}
		out = appendUnique(out, p)
	}
	if out == nil {
		return nil, errors.New("no protocols given")
	}
	return out, nil
}

func appendUnique(list []Proto, p Proto) []Proto {
	if slices.Contains(list, p) {
		return list
	}
	return append(list, p)
}

// CheckScheme returns an error when p cannot be spoken to a URL with the given scheme.
func (p Proto) CheckScheme(scheme string) error {
	switch {
	case p == H1 && (scheme == "http" || scheme == "https"),
		(p == H2 || p == H3) && scheme == "https",
		p == H2C && scheme == "http":
		return nil
	}
	return fmt.Errorf("protocol %s cannot be used with a %q URL", p, scheme)
}

// FromRequest names the protocol an inbound server request arrived on.
func FromRequest(r *http.Request) Proto {
	switch {
	case r.ProtoMajor == http3Major:
		return H3
	case r.ProtoMajor == http2Major && r.TLS == nil:
		return H2C
	case r.ProtoMajor == http2Major:
		return H2
	}
	return H1
}
