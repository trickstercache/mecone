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

package suites_test

import (
	"maps"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/client"
	"github.com/trickstercache/mecone/pkg/corpus"
	"github.com/trickstercache/mecone/pkg/origin"
	"github.com/trickstercache/mecone/pkg/results"
	"github.com/trickstercache/mecone/pkg/testdef"
	"github.com/trickstercache/mecone/suites"
)

const (
	none        = 0
	concurrency = 32
	timeout     = 5 * time.Second
)

func startOrigin(t *testing.T) *url.URL {
	t.Helper()
	ts := httptest.NewServer(origin.New(origin.Options{}).Handler())
	t.Cleanup(ts.Close)
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func startProxy(t *testing.T, originURL *url.URL) string {
	t.Helper()
	ts := httptest.NewServer(httputil.NewSingleHostReverseProxy(originURL))
	t.Cleanup(ts.Close)
	return ts.URL
}

func runAll(t *testing.T, target, control string, tests []*testdef.Test) *results.Run {
	t.Helper()
	r, err := client.New(client.Options{Target: target, OriginControl: control, Concurrency: concurrency, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	run, err := r.Run(t.Context(), tests)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func checkOutcomes(t *testing.T, run *results.Run, want map[string]results.Outcome) {
	t.Helper()
	for _, res := range run.Results {
		if res.Outcome == results.Error || res.Outcome == results.Inconclusive {
			t.Errorf("%s: %s: %v", res.ID, res.Outcome, res.Messages)
		}
		if w, ok := want[res.ID]; ok && res.Outcome != w {
			t.Errorf("%s: %s, want %s: %v", res.ID, res.Outcome, w, res.Messages)
		}
	}
}

func TestBuiltinSuitesLoad(t *testing.T) {
	c, err := corpus.Load(suites.FS)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range c.Suites() {
		if len(s.Groups) == none {
			t.Errorf("suite %s has no groups", s.ID)
		}
	}
}

// The outcomes the built-in suites produce against a bare origin, with no proxy in between.
var directOutcomes = map[string]results.Outcome{
	"cache-status-identifies-the-cache":                     results.Skipped,
	"cache-status-is-present":                               results.Fail,
	"cache-status-reports-a-forward-reason":                 results.Skipped,
	"cache-status-reports-forwarded-status":                 results.Skipped,
	"cache-status-reports-a-hit":                            results.Skipped,
	"cache-status-reports-storage-on-miss":                  results.Skipped,
	"cache-status-reports-ttl-on-hit":                       results.Skipped,
	"directives-relayed-on-requests":                        results.Pass,
	"directives-relayed-on-responses":                       results.Pass,
	"directives-request-no-cache-forces-validation":         results.Pass,
	"directives-request-only-if-cached":                     results.Fail,
	"forwarding-adds-via":                                   results.Fail,
	"forwarding-adds-via-to-responses":                      results.Fail,
	"forwarding-drops-connection-options":                   results.Fail,
	"forwarding-drops-fields-named-in-connection":           results.Fail,
	"forwarding-drops-proxy-connection":                     results.Fail,
	"forwarding-keeps-repeated-field-order":                 results.Pass,
	"forwarding-max-forwards-is-decremented":                results.Fail,
	"forwarding-max-forwards-zero-stops-here":               results.Fail,
	"forwarding-relays-early-hints":                         results.Pass,
	"forwarding-relays-trailers":                            results.Pass,
	"forwarding-via-names-the-received-protocol":            results.Skipped,
	"freshness-age-on-reuse":                                results.Skipped,
	"freshness-future-expires-is-reused":                    results.Fail,
	"freshness-immutable-expires-with-freshness":            results.Pass,
	"freshness-invalid-expires-is-already-stale":            results.Pass,
	"freshness-max-age-overrides-expires":                   results.Pass,
	"freshness-must-revalidate-when-stale":                  results.Pass,
	"freshness-no-cache-forces-validation":                  results.Pass,
	"freshness-no-heuristic-with-explicit-expiry":           results.Pass,
	"freshness-proxy-revalidate-when-stale":                 results.Pass,
	"freshness-request-max-age-zero-forces-reuse-check":     results.Pass,
	"freshness-request-min-fresh-is-honored":                results.Pass,
	"freshness-s-maxage-overrides-max-age":                  results.Pass,
	"freshness-stale-response-is-revalidated":               results.Pass,
	"freshness-unrecognized-directive-ignored":              results.Pass,
	"freshness-upstream-age-counts":                         results.Pass,
	"invalidation-delete-invalidates-target-uri":            results.Pass,
	"invalidation-put-invalidates-target-uri":               results.Pass,
	"invalidation-unknown-method-invalidates-target-uri":    results.Pass,
	"invalidation-unsafe-method-invalidates-target-uri":     results.Pass,
	"messages-204-has-no-content":                           results.Pass,
	"messages-h1-content-is-complete":                       results.Pass,
	"messages-h2-content-is-complete":                       results.Skipped,
	"messages-h2-trailers-are-received":                     results.Skipped,
	"messages-head-has-no-content":                          results.Pass,
	"messages-unknown-status-is-forwarded":                  results.Pass,
	"ranges-stored-open-ended":                              results.Skipped,
	"ranges-stored-suffix":                                  results.Skipped,
	"ranges-stored-unsatisfiable":                           results.Pass,
	"request-conditional-outranks-range":                    results.Pass,
	"request-matching-if-range-gets-partial-content":        results.Pass,
	"request-partial-content-carries-representation-fields": results.Pass,
	"request-range-is-ignored-on-head":                      results.Pass,
	"request-stale-if-range-gets-the-whole-response":        results.Pass,
	"request-unknown-range-unit-is-ignored":                 results.Pass,
	"stale-if-error-serves-stale":                           results.Fail,
	"stale-if-error-does-not-cover-not-found":               results.Skipped,
	"stale-if-error-window-ends":                            results.Skipped,
	"stale-while-revalidate-serves-stale":                   results.Fail,
	"stale-while-revalidate-window-ends":                    results.Skipped,
	"storage-authorized-in-shared-cache":                    results.Pass,
	"storage-authorized-with-s-maxage":                      results.Fail,
	"storage-authorized-with-public":                        results.Fail,
	"storage-explicitly-fresh-301":                          results.Fail,
	"storage-explicitly-fresh-404":                          results.Fail,
	"storage-keeps-unrecognized-fields":                     results.Skipped,
	"storage-method-in-cache-key":                           results.Pass,
	"storage-no-store":                                      results.Pass,
	"storage-no-store-on-second-field-line":                 results.Pass,
	"storage-private-in-shared-cache":                       results.Pass,
	"storage-request-no-store":                              results.Pass,
	"storage-reuse-fresh":                                   results.Fail,
	"storage-target-uri-in-cache-key":                       results.Pass,
	"storage-uncacheable-status":                            results.Pass,
	"storage-writes-through-unsafe-methods":                 results.Pass,
	"targeted-cdn-cache-control-is-used":                    results.Fail,
	"targeted-cdn-cache-control-wins":                       results.Skipped,
	"targeted-empty-field-falls-back":                       results.Skipped,
	"targeted-malformed-field-falls-back":                   results.Skipped,
	"targeted-no-cache-overrides-max-age":                   results.Skipped,
	"targeted-unrecognized-directive-is-ignored":            results.Skipped,
	"targeted-unknown-field-changes-nothing":                results.Skipped,
	"targeted-unknown-field-is-relayed":                     results.Pass,
	"validation-304-carries-the-entity-tag":                 results.Skipped,
	"validation-304-freshens-the-stored-response":           results.Skipped,
	"validation-answers-matching-if-modified-since":         results.Skipped,
	"validation-answers-matching-if-none-match":             results.Skipped,
	"validation-if-none-match-checks-every-tag":             results.Pass,
	"validation-if-none-match-uses-weak-comparison":         results.Pass,
	"validation-if-none-match-beats-if-modified-since":      results.Pass,
	"validation-revalidates-stale-with-last-modified":       results.Fail,
	"validation-revalidates-stale-with-etag":                results.Fail,
	"validation-uses-a-full-validation-response":            results.Pass,
	"vary-absent-field-does-not-match":                      results.Pass,
	"vary-asterisk-never-matches":                           results.Pass,
	"vary-matching-fields-are-reused":                       results.Skipped,
	"vary-mismatched-field-is-not-reused":                   results.Pass,
	"vary-multiple-matching-fields-are-reused":              results.Skipped,
	"vary-repeated-lines-nominate-every-field":              results.Pass,
}

func proxiedOutcomes() map[string]results.Outcome {
	out := maps.Clone(directOutcomes)
	// The reference proxy strips the hop-by-hop fields it knows about; it adds no Via of its own.
	out["forwarding-drops-connection-options"] = results.Pass
	out["forwarding-drops-fields-named-in-connection"] = results.Pass
	out["forwarding-drops-proxy-connection"] = results.Pass
	return out
}

func TestBuiltinSuitesAgainstReferences(t *testing.T) {
	// Pins the outcomes the built-in suites must produce with no proxy at all, and through a
	// non-caching httputil.ReverseProxy.
	c, err := corpus.Load(suites.FS)
	if err != nil {
		t.Fatal(err)
	}
	originURL := startOrigin(t)

	cases := map[string]struct {
		target string
		want   map[string]results.Outcome
	}{
		"direct":       {originURL.String(), directOutcomes},
		"reverseproxy": {startProxy(t, originURL), proxiedOutcomes()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			checkOutcomes(t, runAll(t, tc.target, originURL.String(), c.Tests()), tc.want)
		})
	}
}
