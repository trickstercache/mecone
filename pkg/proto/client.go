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
	"fmt"
	"net/http"
	"time"
)

const (
	maxIdleConnsPerHost = 64
	idleConnTimeout     = 90 * time.Second
)

// ClientOptions configures clients built by NewClient.
type ClientOptions struct {
	Timeout time.Duration
	// InsecureSkipVerify disables TLS certificate verification, for proxies under test that
	// present self-signed certificates.
	InsecureSkipVerify bool
}

// NewClient returns an HTTP client that speaks only p. It never follows redirects, negotiates
// compression or honors proxy environment variables, so each response is exactly what the peer sent.
func NewClient(p Proto, o ClientOptions) (*http.Client, error) {
	protos := new(http.Protocols)
	switch p {
	case H1:
		protos.SetHTTP1(true)
	case H2:
		protos.SetHTTP2(true)
	case H2C:
		protos.SetUnencryptedHTTP2(true)
	case H3:
		return nil, fmt.Errorf("%s: %w", p, ErrUnsupported)
	default:
		return nil, fmt.Errorf("unknown protocol %q", p)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if o.InsecureSkipVerify {
		tlsConfig.InsecureSkipVerify = true
	}
	tr := &http.Transport{
		Protocols:           protos,
		ForceAttemptHTTP2:   p == H2,
		DisableCompression:  true,
		TLSClientConfig:     tlsConfig,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		IdleConnTimeout:     idleConnTimeout,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   o.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}
