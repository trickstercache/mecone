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

package origin

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/appinfo"
	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/protocol"
)

const (
	anyLoopbackPort = "127.0.0.1:0"
	badLoopbackPort = "127.0.0.1:-1"
	missingFile     = "missing"
	noFile          = ""
	testFile        = "/f.txt"
	testQuery       = "?x=1"

	headerCacheControl = "Cache-Control"
	headerContentRange = "Content-Range"

	respPlain    = "plain"
	respTagged   = "tagged"
	respStubborn = "stubborn"
	respRanged   = "ranged"
	respBare     = "bare"
	respExtras   = "extras"
	respGone     = "gone"
	respSlow     = "slow"
	respSized    = "sized"
	respForbid   = "forbidden"
	respShort    = "short"

	stepUnscripted = 0
	stepPlain      = 1
	stepTagged     = 2
	stepStubborn   = 3
	stepRanged     = 4
	stepBare       = 5

	stepExtras = 1
	stepGone   = 2
	stepSlow   = 3
	stepSized  = 4
	stepForbid = 5
	stepShort  = 6

	bodyHello  = "hello"
	bodyTagged = "tagged body"
	bodyAlways = "always"
	bodyRanged = "0123456789abcdefghij"
	bodyBare   = "bare body"
	bodyExtras = "extras body"
	bodySlow   = "slow body"
	anyBody    = "*"
	sizedBytes = 40
	firstSeq   = 1
	http1Major = 1
	http2Major = 2

	etagV1 = `"v1"`
	etagA  = `"a"`
	weakA  = `W/"a"`

	slowDelay     = 30 * time.Millisecond
	cancelAfter   = 5 * time.Millisecond
	clientTimeout = 5 * time.Second
	certSerial    = 1
	privateFile   = 0o600
)

type responseCase struct {
	name         string
	method       string
	step         int
	headers      []string
	status       int
	body         string
	response     string
	contentRange string
}

type traced struct {
	resp    *http.Response
	interim []int
	body    string
}

var responseCases = []responseCase{
	{name: "plain", method: http.MethodGet, step: stepPlain, status: http.StatusNonAuthoritativeInfo, body: bodyHello, response: respPlain},
	{
		name: "etag match", method: http.MethodGet, step: stepTagged, headers: []string{`If-None-Match: W/"x", ` + etagV1},
		status: http.StatusNotModified, response: respTagged,
	},
	{name: "etag miss", method: http.MethodGet, step: stepTagged, headers: []string{`If-None-Match: "v2"`}, status: http.StatusOK, body: bodyTagged, response: respTagged},
	{
		name: "etag on post", method: http.MethodPost, step: stepTagged, headers: []string{"If-None-Match: " + etagV1},
		status: http.StatusOK, body: bodyTagged, response: respTagged,
	},
	{
		name: "ims match", method: http.MethodGet, step: stepTagged, headers: []string{"If-Modified-Since: Tue, 02 Jan 2024 00:00:00 GMT"},
		status: http.StatusNotModified, response: respTagged,
	},
	{
		name: "ims older", method: http.MethodGet, step: stepTagged, headers: []string{"If-Modified-Since: Sun, 31 Dec 2023 00:00:00 GMT"},
		status: http.StatusOK, body: bodyTagged, response: respTagged,
	},
	{
		name: "conditionals ignored", method: http.MethodGet, step: stepStubborn, headers: []string{"If-None-Match: " + etagV1},
		status: http.StatusOK, body: bodyAlways, response: respStubborn,
	},
	{name: "star without etag", method: http.MethodGet, step: stepBare, headers: []string{"If-None-Match: *"}, status: http.StatusOK, body: bodyBare, response: respBare},
	{
		name: "ims without last-modified", method: http.MethodGet, step: stepBare, headers: []string{"If-Modified-Since: Tue, 02 Jan 2024 00:00:00 GMT"},
		status: http.StatusOK, body: bodyBare, response: respBare,
	},
	{
		name: "suffix range", method: http.MethodGet, step: stepRanged, headers: []string{"Range: bytes=-5"},
		status: http.StatusPartialContent, body: "fghij", response: respRanged, contentRange: "bytes 15-19/20",
	},
	{
		name: "unsatisfiable range", method: http.MethodGet, step: stepRanged, headers: []string{"Range: bytes=100-200"},
		status: http.StatusRequestedRangeNotSatisfiable, body: anyBody, response: respRanged, contentRange: "bytes */20",
	},
	{
		name: "unknown range unit", method: http.MethodGet, step: stepRanged, headers: []string{"Range: mecone-parts=1-2"},
		status: http.StatusOK, body: bodyRanged, response: respRanged,
	},
	{
		name: "range without a unit", method: http.MethodGet, step: stepRanged, headers: []string{"Range: 0-4"},
		status: http.StatusOK, body: bodyRanged, response: respRanged,
	},
	{
		name: "range on a method other than get", method: http.MethodPost, step: stepRanged, headers: []string{"Range: bytes=-5"},
		status: http.StatusOK, body: bodyRanged, response: respRanged,
	},
	{name: "default response", method: http.MethodGet, step: stepUnscripted, status: http.StatusNonAuthoritativeInfo, body: bodyHello, response: respPlain},
}

func newTestServer(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(New(Options{}).Handler())
	t.Cleanup(ts.Close)
	return ts.URL
}

func do(t *testing.T, method, url string, body io.Reader, headers ...string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, body)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range headers {
		name, value, _ := protocol.SplitLine(line)
		req.Header.Add(name, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(resp.Body)
	if err = errors.Join(err, resp.Body.Close()); err != nil {
		t.Fatal(err)
	}
	return resp, string(b)
}

func doStep(t *testing.T, method, url string, step int, headers ...string) (*http.Response, string) {
	t.Helper()
	return do(t, method, url, nil, slices.Concat(headers, []string{protocol.HeaderStep + ": " + strconv.Itoa(step)})...)
}

func putScript(t *testing.T, base, id string, sc protocol.Script) {
	t.Helper()
	b, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := do(t, http.MethodPut, base+protocol.ScriptsPath+id, bytes.NewReader(b)); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("put script: %s", resp.Status)
	}
}

func getLog(t *testing.T, base, id string) protocol.Log {
	t.Helper()
	resp, body := do(t, http.MethodGet, base+protocol.LogsPath+id, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("log: %s", resp.Status)
	}
	var l protocol.Log
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatal(err)
	}
	return l
}

func isNoStore(resp *http.Response) bool {
	return resp.Header.Get(headerCacheControl) == "no-store"
}

func TestInfo(t *testing.T) {
	base := newTestServer(t)
	resp, body := do(t, http.MethodGet, base+protocol.InfoPath, nil)
	var info protocol.Info
	if err := json.Unmarshal([]byte(body), &info); err != nil {
		t.Fatal(err)
	}
	if !isNoStore(resp) || info.Protocol != protocol.Version || info.Name != appinfo.Name {
		t.Errorf("info = %+v, header %v", info, resp.Header)
	}
}

func TestRejectedScripts(t *testing.T) {
	base := newTestServer(t)
	id := protocol.NewID()
	for _, bad := range []struct{ id, body string }{{"not-an-id", "{}"}, {id, "{bogus"}, {id, `{"nope":1}`}} {
		if resp, _ := do(t, http.MethodPut, base+protocol.ScriptsPath+bad.id, strings.NewReader(bad.body)); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("put %q: %s", bad.body, resp.Status)
		}
	}
	if resp, _ := do(t, http.MethodGet, base+protocol.LogsPath+id, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("log of unknown test: %s", resp.Status)
	}
}

func TestScriptLifecycle(t *testing.T) {
	base := newTestServer(t)
	id := protocol.NewID()
	putScript(t, base, id, protocol.Script{})
	if resp, body := do(t, http.MethodGet, base+protocol.LogsPath+id, nil); resp.StatusCode != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("fresh log: %s %q", resp.Status, body)
	}
	if resp, _ := do(t, http.MethodDelete, base+protocol.ScriptsPath+id, nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("delete: %s", resp.Status)
	}
	resp, _ := do(t, http.MethodGet, base+protocol.TestPath(id, noFile), nil)
	if resp.StatusCode != http.StatusNotFound || !isNoStore(resp) {
		t.Errorf("deleted test: %s %v", resp.Status, resp.Header)
	}
}

func scriptedServer(t *testing.T) (base, id string) {
	t.Helper()
	base, id = newTestServer(t), protocol.NewID()
	putScript(t, base, id, protocol.Script{
		Responses: map[string]protocol.Response{
			respPlain: {Status: http.StatusNonAuthoritativeInfo, Body: bodyHello, Headers: []string{
				"Cache-Control: max-age=60", "Expires: ${now+1h}", "Content-Type: application/x-test",
			}},
			respTagged:   {Headers: []string{"ETag: " + etagV1, "Last-Modified: Mon, 01 Jan 2024 00:00:00 GMT"}, Body: bodyTagged},
			respStubborn: {Headers: []string{"ETag: " + etagV1}, Body: bodyAlways, IgnoreConditionals: true},
			respRanged:   {Headers: []string{`ETag: "r1"`}, Body: bodyRanged, Ranges: true},
			respBare:     {Body: bodyBare},
		},
		Steps:   map[int]string{stepPlain: respPlain, stepTagged: respTagged, stepStubborn: respStubborn, stepRanged: respRanged, stepBare: respBare},
		Default: respPlain,
	})
	return base, id
}

func TestScriptedHeaders(t *testing.T) {
	base, id := scriptedServer(t)
	resp, _ := doStep(t, http.MethodGet, base+protocol.TestPath(id, testFile), stepPlain)
	expires, err := http.ParseTime(resp.Header.Get("Expires"))
	if err != nil || !expires.After(time.Now()) {
		t.Errorf("Expires: %v, %v", expires, err)
	}
	if resp.Header.Get("Content-Type") != "application/x-test" || resp.Header.Get(headerCacheControl) != "max-age=60" {
		t.Errorf("scripted headers: %v", resp.Header)
	}
}

func TestScriptedResponses(t *testing.T) {
	base, id := scriptedServer(t)
	u := base + protocol.TestPath(id, testFile) + testQuery
	resps := make([]*http.Response, len(responseCases))
	for i, c := range responseCases {
		resp, body := doStep(t, c.method, u, c.step, c.headers...)
		checkResponse(t, c, resp, body)
		resps[i] = resp
	}
	l := getLog(t, base, id)
	if len(l) != len(responseCases) {
		t.Fatalf("log has %d entries, want %d", len(l), len(responseCases))
	}
	wantTarget := protocol.TestPrefix + id + testFile + testQuery
	for i, c := range responseCases {
		checkEntry(t, c, resps[i], l[i], wantTarget)
		if l[i].Seq != i+firstSeq {
			t.Errorf("%s: logged seq %d, want %d", c.name, l[i].Seq, i+firstSeq)
		}
	}
}

func checkResponse(t *testing.T, c responseCase, resp *http.Response, body string) {
	t.Helper()
	if resp.StatusCode != c.status || (c.body != anyBody && body != c.body) {
		t.Errorf("%s: %d %q, want %d %q", c.name, resp.StatusCode, body, c.status, c.body)
	}
	if got := resp.Header.Get(headerContentRange); got != c.contentRange {
		t.Errorf("%s: Content-Range %q, want %q", c.name, got, c.contentRange)
	}
	if resp.Header.Get(protocol.HeaderResponse) != c.response || resp.Header.Get(protocol.HeaderOriginTime) == noValue {
		t.Errorf("%s: origin headers %v", c.name, resp.Header)
	}
}

func checkEntry(t *testing.T, c responseCase, resp *http.Response, e protocol.LogEntry, wantTarget string) {
	t.Helper()
	if e.Step != c.step || e.Method != c.method || e.Status != c.status || e.Response != c.response {
		t.Errorf("%s: logged step %d, %s, status %d, response %q", c.name, e.Step, e.Method, e.Status, e.Response)
	}
	if e.Target != wantTarget || e.Proto != string(proto.H1) || e.Header.Get("Host") == noValue {
		t.Errorf("%s: logged request %s %s %v", c.name, e.Target, e.Proto, e.Header)
	}
	if resp.Header.Get(protocol.HeaderOriginSeq) != strconv.Itoa(e.Seq) || e.ResponseHeader.Get(headerCacheControl) != resp.Header.Get(headerCacheControl) {
		t.Errorf("%s: logged seq %d and response header %v, sent %v", c.name, e.Seq, e.ResponseHeader, resp.Header)
	}
}

func extrasURL(t *testing.T) string {
	t.Helper()
	base, id := newTestServer(t), protocol.NewID()
	putScript(t, base, id, protocol.Script{
		Responses: map[string]protocol.Response{
			respExtras: {
				Interim:  []protocol.Interim{{Status: http.StatusEarlyHints, Headers: []string{"Link: </a.css>; rel=preload"}}},
				Trailers: []string{"X-Sum: ${now}"},
				Body:     bodyExtras,
			},
			respGone:   {Disconnect: true},
			respSlow:   {DelayMS: slowDelay.Milliseconds(), Body: bodySlow},
			respSized:  {BodySize: sizedBytes},
			respForbid: {Status: http.StatusNoContent, Body: bodyExtras, Trailers: []string{"X-Sum: ${now}"}},
			respShort:  {Headers: []string{"Content-Length: 1"}, Body: bodyExtras},
		},
		Steps: map[int]string{stepExtras: respExtras, stepGone: respGone, stepSlow: respSlow, stepSized: respSized, stepForbid: respForbid, stepShort: respShort},
	})
	return base + protocol.TestPath(id, noFile)
}

func sendTraced(ctx context.Context, url string, step int) (traced, error) {
	var out traced
	trace := &httptrace.ClientTrace{Got1xxResponse: func(code int, _ textproto.MIMEHeader) error {
		out.interim = append(out.interim, code)
		return nil
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, url, nil)
	if err != nil {
		return out, err
	}
	req.Header.Set(protocol.HeaderStep, strconv.Itoa(step))
	if out.resp, err = http.DefaultClient.Do(req); err != nil {
		return out, err
	}
	b, err := io.ReadAll(out.resp.Body)
	out.body = string(b)
	return out, errors.Join(err, out.resp.Body.Close())
}

func TestInterimAndTrailers(t *testing.T) {
	got, err := sendTraced(t.Context(), extrasURL(t), stepExtras)
	if err != nil {
		t.Fatal(err)
	}
	if got.body != bodyExtras || !slices.Equal(got.interim, []int{http.StatusEarlyHints}) {
		t.Errorf("extras: %q, interim %v", got.body, got.interim)
	}
	if got.resp.Header.Get("Link") != noValue || got.resp.Trailer.Get("X-Sum") == noValue {
		t.Errorf("extras: header %v, trailer %v", got.resp.Header, got.resp.Trailer)
	}
}

func TestDisconnect(t *testing.T) {
	if _, err := sendTraced(t.Context(), extrasURL(t), stepGone); err == nil {
		t.Error("disconnect: request succeeded")
	}
}

func TestDelay(t *testing.T) {
	u := extrasURL(t)
	start := time.Now()
	if got, err := sendTraced(t.Context(), u, stepSlow); err != nil || got.body != bodySlow || time.Since(start) < slowDelay {
		t.Errorf("slow: %v %q after %v", err, got.body, time.Since(start))
	}
	ctx, cancel := context.WithTimeout(t.Context(), cancelAfter)
	defer cancel()
	if _, err := sendTraced(ctx, u, stepSlow); err == nil {
		t.Error("canceled slow request succeeded")
	}
}

func TestBodySize(t *testing.T) {
	if got, err := sendTraced(t.Context(), extrasURL(t), stepSized); err != nil || len(got.body) != sizedBytes {
		t.Errorf("sized: %v %d bytes", err, len(got.body))
	}
}

func TestBodyWriteErrors(t *testing.T) {
	u := extrasURL(t)
	if got, err := sendTraced(t.Context(), u, stepForbid); err != nil || got.resp.StatusCode != http.StatusNoContent || got.body != noValue {
		t.Errorf("body on a no-content status: %v %q", err, got.body)
	}
	if got, err := sendTraced(t.Context(), u, stepShort); err == nil {
		t.Errorf("body longer than its Content-Length was delivered: %q", got.body)
	}
}

func TestStoreSweepAndCap(t *testing.T) {
	const expired, fresh, overflow = "expired", "fresh", 5
	s := newStore(time.Minute)
	t0 := time.Now()
	s.put(expired, protocol.Script{}, t0)
	s.put(fresh, protocol.Script{}, t0.Add(time.Minute+time.Second))
	e := s.get(fresh)
	if s.get(expired) != nil || e == nil {
		t.Fatal("expired script was not swept")
	}
	for i := range maxLogEntries + overflow {
		e.record(protocol.LogEntry{Seq: i})
	}
	if n := len(e.snapshot()); n != maxLogEntries {
		t.Errorf("log grew to %d entries", n)
	}
}

func TestETagListMatches(t *testing.T) {
	cases := []struct {
		list, etag string
		want       bool
	}{
		{etagA, etagA, true},
		{weakA, etagA, true},
		{`"x", ` + weakA, weakA, true},
		{`*`, etagA, true},
		{`"a,b"`, `"a,b"`, true},
		{`"b"`, etagA, false},
		{`a`, etagA, false},
		{`"a`, etagA, false},
		{`W/`, etagA, false},
		{` , `, etagA, false},
	}
	for _, c := range cases {
		if got := etagListMatches(c.list, c.etag); got != c.want {
			t.Errorf("etagListMatches(%q, %q) = %v", c.list, c.etag, got)
		}
	}
}

func writeCert(t *testing.T) (certFile, keyFile string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(certSerial),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	writePEM(t, certFile, "CERTIFICATE", der)
	writePEM(t, keyFile, "EC PRIVATE KEY", kb)
	return certFile, keyFile
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: der}), privateFile); err != nil {
		t.Fatal(err)
	}
}

func serveInBackground(t *testing.T, srv *Server) (stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	var err error
	wg.Go(func() { err = srv.Serve(ctx) })
	return func() error {
		cancel()
		wg.Wait()
		return err
	}
}

func fetchInfo(t *testing.T, p proto.Proto, base string) (resp *http.Response, info protocol.Info) {
	t.Helper()
	hc, err := proto.NewClient(p, proto.ClientOptions{InsecureSkipVerify: true, Timeout: clientTimeout})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+protocol.InfoPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp, err = hc.Do(req); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	if err = errors.Join(json.NewDecoder(resp.Body).Decode(&info), resp.Body.Close()); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
	return resp, info
}

func TestListenAndServe(t *testing.T) {
	cert, key := writeCert(t)
	srv := New(Options{Listen: anyLoopbackPort, TLSListen: anyLoopbackPort, TLSCert: cert, TLSKey: key})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	stop := serveInBackground(t, srv)
	cases := []struct {
		p     proto.Proto
		url   string
		major int
	}{
		{proto.H1, "http://" + srv.Addr(), http1Major},
		{proto.H2C, "http://" + srv.Addr(), http2Major},
		{proto.H2, "https://" + srv.TLSAddr(), http2Major},
	}
	for _, c := range cases {
		if resp, info := fetchInfo(t, c.p, c.url); resp.ProtoMajor != c.major || !slices.Equal(info.Listeners, srv.listeners()) {
			t.Errorf("%s: proto %d, info %+v", c.p, resp.ProtoMajor, info)
		}
	}
	if err := stop(); err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestListenErrors(t *testing.T) {
	cert, key := writeCert(t)
	cases := map[string]Options{
		"no addresses":      {},
		"a missing key":     {TLSListen: anyLoopbackPort, TLSCert: missingFile, TLSKey: missingFile},
		"a bad address":     {Listen: badLoopbackPort},
		"a bad TLS address": {TLSListen: badLoopbackPort, TLSCert: cert, TLSKey: key},
	}
	for name, o := range cases {
		if err := New(o).Listen(); err == nil {
			t.Errorf("Listen with %s succeeded", name)
		}
	}
	srv := New(Options{Listen: anyLoopbackPort, TLSListen: badLoopbackPort, TLSCert: cert, TLSKey: key})
	if err := srv.Listen(); err == nil || srv.Addr() != noAddr {
		t.Error("a failed TLS listener must release the cleartext listener")
	}
}

func TestClose(t *testing.T) {
	cert, key := writeCert(t)
	srv := New(Options{Listen: anyLoopbackPort, TLSListen: anyLoopbackPort, TLSCert: cert, TLSKey: key})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	addr := srv.Addr()
	if err := srv.Close(); err != nil || srv.Addr() != noAddr || srv.TLSAddr() != noAddr {
		t.Fatalf("Close = %v, addresses %q %q", err, srv.Addr(), srv.TLSAddr())
	}
	again := New(Options{Listen: addr})
	if err := again.Listen(); err != nil {
		t.Fatalf("address was not released: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Error(err)
	}
}

func TestCloseErrors(t *testing.T) {
	if err := New(Options{}).Close(); err != nil {
		t.Errorf("Close without Listen = %v", err)
	}
	srv := New(Options{Listen: anyLoopbackPort})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	if err := srv.plain.Close(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Close(); err == nil {
		t.Error("closing an already closed listener reported no error")
	}
}

func TestServeErrors(t *testing.T) {
	if err := New(Options{}).Serve(t.Context()); err == nil {
		t.Error("Serve without Listen succeeded")
	}
	srv := New(Options{Listen: anyLoopbackPort})
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	if err := srv.plain.Close(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Serve(t.Context()); err == nil {
		t.Error("Serve on a closed listener succeeded")
	}
}
