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

package proto

import (
	"crypto/tls"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

var fromRequestCases = []struct {
	r    *http.Request
	want Proto
}{
	{&http.Request{ProtoMajor: 1}, H1},
	{&http.Request{ProtoMajor: 2}, H2C},
	{&http.Request{ProtoMajor: 2, TLS: &tls.ConnectionState{}}, H2},
	{&http.Request{ProtoMajor: 3, TLS: &tls.ConnectionState{}}, H3},
}

func TestParse(t *testing.T) {
	if p, err := Parse(" H2C "); err != nil || p != H2C {
		t.Errorf("Parse = %q, %v", p, err)
	}
	if _, err := Parse("h9"); err == nil {
		t.Error("Parse(h9) succeeded")
	}
}

func TestParseList(t *testing.T) {
	got, err := ParseList("h1, h2c,h1,")
	if err != nil || !slices.Equal(got, []Proto{H1, H2C}) {
		t.Errorf("ParseList = %v, %v", got, err)
	}
	for _, bad := range []string{" , ", "h1,bogus"} {
		if _, err := ParseList(bad); err == nil {
			t.Errorf("ParseList(%q) succeeded", bad)
		}
	}
}

func TestCheckScheme(t *testing.T) {
	ok := map[Proto][]string{H1: {schemeHTTP, schemeHTTPS}, H2: {schemeHTTPS}, H2C: {schemeHTTP}, H3: {schemeHTTPS}}
	for p, schemes := range ok {
		for _, s := range []string{schemeHTTP, schemeHTTPS, "ftp"} {
			err := p.CheckScheme(s)
			if want := slices.Contains(schemes, s); (err == nil) != want {
				t.Errorf("%s.CheckScheme(%s) = %v", p, s, err)
			}
		}
	}
}

func TestFromRequest(t *testing.T) {
	for _, c := range fromRequestCases {
		if got := FromRequest(c.r); got != c.want {
			t.Errorf("FromRequest(%d) = %s, want %s", c.r.ProtoMajor, got, c.want)
		}
	}
}

func TestNewClient(t *testing.T) {
	for _, p := range []Proto{H1, H2, H2C} {
		for _, insecure := range []bool{false, true} {
			tr := newTestTransport(t, p, ClientOptions{Timeout: time.Second, InsecureSkipVerify: insecure})
			if tr.TLSClientConfig.InsecureSkipVerify != insecure {
				t.Errorf("%s: InsecureSkipVerify = %v, want %v", p, tr.TLSClientConfig.InsecureSkipVerify, insecure)
			}
		}
	}
}

func TestNewClientUnsupported(t *testing.T) {
	if _, err := NewClient(H3, ClientOptions{}); !errors.Is(err, ErrUnsupported) {
		t.Errorf("h3: %v", err)
	}
	if _, err := NewClient("h9", ClientOptions{}); err == nil {
		t.Error("h9 succeeded")
	}
}

func newTestTransport(t *testing.T, p Proto, o ClientOptions) *http.Transport {
	t.Helper()
	hc, err := NewClient(p, o)
	if err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	if hc.Timeout != o.Timeout {
		t.Errorf("%s: timeout %v", p, hc.Timeout)
	}
	if !errors.Is(hc.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Errorf("%s: redirects are followed", p)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("%s: transport is %T", p, hc.Transport)
	}
	if !tr.DisableCompression || tr.Proxy != nil {
		t.Errorf("%s: transport alters traffic", p)
	}
	return tr
}
