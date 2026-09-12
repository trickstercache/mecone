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
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/protocol"
)

const (
	noName             = ""
	noValue            = ""
	unsetStatus        = 0
	noDelay            = 0
	decimal            = 10
	headerContentType  = "Content-Type"
	headerLastModified = "Last-Modified"
	headerRange        = "Range"
	bytesUnit          = "bytes"
	defaultContentType = "text/plain; charset=utf-8"
	anyETag            = "*"
	weakPrefix         = "W/"
	quote              = `"`
	etagSeparators     = " \t,"
)

type recorder struct {
	header http.Header
	status int
	body   *bytes.Buffer
}

func (s *Server) serveTest(w http.ResponseWriter, r *http.Request) {
	e := s.store.get(r.PathValue(idParam))
	if e == nil {
		controlError(w, http.StatusNotFound, "no script for this test id")
		return
	}
	now := time.Now()
	step, _ := strconv.Atoi(r.Header.Get(protocol.HeaderStep))
	name, resp := e.script.ResponseFor(step)
	le := newLogEntry(r, e.nextSeq(), step, name, now)
	if resp.Disconnect {
		e.record(le)
		panic(http.ErrAbortHandler)
	}
	out := build(r, resp, now)
	out.stamp(name, le.Seq, now)
	le.Status, le.ResponseHeader = out.status, out.header.Clone()
	// the entry is logged before any bytes are written, so it is visible once the client has the response
	e.record(le)

	if resp.DelayMS > noDelay && !sleep(r.Context(), time.Duration(resp.DelayMS)*time.Millisecond) {
		return
	}
	writeInterim(w, resp.Interim, now)
	writeFinal(w, out, resp.Trailers, now)
}

func newLogEntry(r *http.Request, seq, step int, name string, now time.Time) protocol.LogEntry {
	le := protocol.LogEntry{
		Seq:      seq,
		Step:     step,
		Response: name,
		Received: now.UTC(),
		Method:   r.Method,
		Target:   r.RequestURI,
		Proto:    string(proto.FromRequest(r)),
		Header:   r.Header.Clone(),
	}
	le.Header.Set("Host", r.Host)
	return le
}

func (rec *recorder) Header() http.Header {
	return rec.header
}

func (rec *recorder) WriteHeader(code int) {
	if rec.status == unsetStatus {
		rec.status = code
	}
}

func (rec *recorder) Write(b []byte) (int, error) {
	if rec.status == unsetStatus {
		rec.status = http.StatusOK
	}
	return rec.body.Write(b)
}

func (rec *recorder) stamp(name string, seq int, now time.Time) {
	if name != noName {
		rec.header.Set(protocol.HeaderResponse, name)
	}
	rec.header.Set(protocol.HeaderOriginSeq, strconv.Itoa(seq))
	rec.header.Set(protocol.HeaderOriginTime, strconv.FormatInt(now.UnixMilli(), decimal))
}

func build(r *http.Request, resp protocol.Response, now time.Time) *recorder {
	rec := &recorder{header: make(http.Header), body: new(bytes.Buffer)}
	for _, line := range resp.Headers {
		name, value, _ := protocol.SplitLine(line)
		rec.header.Add(name, protocol.Expand(value, now))
	}
	if rec.header.Get(headerContentType) == noValue {
		rec.header.Set(headerContentType, defaultContentType)
	}
	body := protocol.ResponseBody(resp)
	status := resp.Status
	if status == unsetStatus {
		status = http.StatusOK
	}
	switch {
	case resp.Ranges && status == http.StatusOK:
		modtime, _ := http.ParseTime(rec.header.Get(headerLastModified))
		http.ServeContent(rec, withoutIgnoredRange(r), noName, modtime, bytes.NewReader(body))
	case !resp.IgnoreConditionals && status == http.StatusOK && notModified(r, rec.header):
		rec.status = http.StatusNotModified
		rec.header.Del(headerContentType)
		rec.header.Del("Content-Length")
	default:
		rec.status = status
		rec.body = bytes.NewBuffer(body)
	}
	return rec
}

// withoutIgnoredRange drops a Range field the server must ignore: range handling is defined only
// for GET, and only for range units the server understands.
func withoutIgnoredRange(r *http.Request) *http.Request {
	v := r.Header.Get(headerRange)
	if v == noValue || (r.Method == http.MethodGet && isBytesRange(v)) {
		return r
	}
	out := r.Clone(r.Context())
	out.Header.Del(headerRange)
	return out
}

func isBytesRange(v string) bool {
	unit, _, ok := strings.Cut(v, "=")
	return ok && strings.EqualFold(strings.TrimSpace(unit), bytesUnit)
}

func notModified(r *http.Request, h http.Header) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	// If-None-Match takes precedence over If-Modified-Since; an absent date fails to parse
	if inm := strings.Join(r.Header.Values("If-None-Match"), ","); inm != noValue {
		etag := h.Get("Etag")
		return etag != noValue && etagListMatches(inm, etag)
	}
	since, err1 := http.ParseTime(r.Header.Get("If-Modified-Since"))
	modified, err2 := http.ParseTime(h.Get(headerLastModified))
	return err1 == nil && err2 == nil && !modified.After(since)
}

func etagListMatches(list, etag string) bool {
	if strings.TrimSpace(list) == anyETag {
		return true
	}
	// weak comparison: W/ prefixes are ignored on both sides
	want := strings.TrimPrefix(etag, weakPrefix)
	for tag, rest, ok := nextETag(list); ok; tag, rest, ok = nextETag(rest) {
		if tag == want {
			return true
		}
	}
	return false
}

func nextETag(list string) (tag, rest string, ok bool) {
	s := strings.TrimPrefix(strings.TrimLeft(list, etagSeparators), weakPrefix)
	opaque, found := strings.CutPrefix(s, quote)
	if !found {
		return tag, rest, false
	}
	_, rest, ok = strings.Cut(opaque, quote)
	return s[:len(s)-len(rest)], rest, ok
}

func writeInterim(w http.ResponseWriter, interim []protocol.Interim, now time.Time) {
	h := w.Header()
	for _, in := range interim {
		for _, line := range in.Headers {
			name, value, _ := protocol.SplitLine(line)
			h.Add(name, protocol.Expand(value, now))
		}
		w.WriteHeader(in.Status)
		clear(h)
	}
}

func writeFinal(w http.ResponseWriter, out *recorder, trailers []string, now time.Time) {
	h := w.Header()
	maps.Copy(h, out.header)
	for _, line := range trailers {
		name, _, _ := protocol.SplitLine(line)
		h.Add("Trailer", name)
	}
	w.WriteHeader(out.status)
	// WriteTo skips empty bodies; a body the status forbids is dropped, but its trailers still go out
	if _, err := out.body.WriteTo(w); err != nil && !errors.Is(err, http.ErrBodyNotAllowed) {
		return
	}
	for _, line := range trailers {
		name, value, _ := protocol.SplitLine(line)
		h.Add(name, protocol.Expand(value, now))
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
