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

package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/trickstercache/mecone/pkg/protocol"
)

const maxErrorBody = 512

// Control is a client for an origin's control plane.
type Control struct {
	base *url.URL
	hc   *http.Client
}

// NewControl returns a Control for the origin whose control plane is reachable at base.
func NewControl(base string, hc *http.Client) (*Control, error) {
	u, err := parseBase(base)
	if err != nil {
		return nil, err
	}
	return &Control{base: u, hc: hc}, nil
}

// Info performs the handshake, returning the origin's description of itself.
func (c *Control) Info(ctx context.Context) (protocol.Info, error) {
	var info protocol.Info
	err := c.do(ctx, http.MethodGet, protocol.InfoPath, nil, &info)
	return info, err
}

// PutScript uploads the script for a test.
func (c *Control) PutScript(ctx context.Context, id string, s protocol.Script) error {
	return c.do(ctx, http.MethodPut, protocol.ScriptsPath+id, s, nil)
}

// Log returns the requests the origin received for a test.
func (c *Control) Log(ctx context.Context, id string) (protocol.Log, error) {
	var l protocol.Log
	err := c.do(ctx, http.MethodGet, protocol.LogsPath+id, nil, &l)
	return l, err
}

// DeleteScript removes a test's script and log from the origin.
func (c *Control) DeleteScript(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, protocol.ScriptsPath+id, nil, nil)
}

func (c *Control) do(ctx context.Context, method, path string, in, out any) error {
	req, err := c.newRequest(ctx, method, path, in)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Control) newRequest(ctx context.Context, method, path string, in any) (*http.Request, error) {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, joinPath(c.base, path).String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-store")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func parseBase(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%q must be an absolute http or https URL", s)
	}
	return u, nil
}

func joinPath(base *url.URL, p string) *url.URL {
	// Only scheme, user, host and path carry over, so base's query and fragment never leak into a request.
	return &url.URL{Scheme: base.Scheme, User: base.User, Host: base.Host, Path: strings.TrimSuffix(base.Path, "/") + p}
}
