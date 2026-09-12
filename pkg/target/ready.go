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

package target

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
)

const (
	probeTimeout = 5 * time.Second
	firstAttempt = 1
)

// FreePort returns a port that is free on host at this moment. A target binds it shortly after,
// so a port that something else claims first is a startup failure, not a silent misconfiguration.
func FreePort(host string) (int, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		return none, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return port, ln.Close()
}

// WaitReady polls the target until it answers the readiness probe, or the probe's timeout passes.
func WaitReady(ctx context.Context, baseURL string, r config.Readiness, v Vars) error {
	hc := &http.Client{Timeout: probeTimeout, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	url := strings.TrimSuffix(baseURL, "/") + v.Expand(r.Path)
	deadline := time.Now().Add(time.Duration(r.Timeout))
	var last error
	for attempt := firstAttempt; ; attempt++ {
		last = probe(ctx, hc, url, r.Status)
		if last == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("not ready after %d attempts over %s: %w", attempt, time.Duration(r.Timeout), last)
		}
		if !sleep(ctx, time.Duration(r.Interval)) {
			return fmt.Errorf("canceled while waiting for readiness: %w", last)
		}
	}
}

func probe(ctx context.Context, hc *http.Client, url string, want []int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Cache-Control", "no-store")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if len(want) > none && !slices.Contains(want, resp.StatusCode) {
		return fmt.Errorf("%s returned %s, want %v", url, resp.Status, want)
	}
	return nil
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
