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

package config

import (
	"net/http"
	"time"

	"github.com/trickstercache/mecone/pkg/protocol"
	"github.com/trickstercache/mecone/pkg/testdef"
)

// Kind is how a target runs.
type Kind string

const (
	// KindProxy is an endpoint Mecone does not manage, such as a proxy that is already running.
	KindProxy Kind = "proxy"
	// KindDocker is a container Mecone starts and stops around the target's tests.
	KindDocker Kind = "docker"
	// KindProcess is a local command Mecone starts and stops around the target's tests.
	KindProcess Kind = "process"
)

const (
	defaultReadyTimeout  = 30 * time.Second
	defaultReadyInterval = 250 * time.Millisecond
)

// Target is one proxy to test. Exactly one of Proxy, Docker or Process must be set, and which one
// decides how Mecone reaches, and whether it manages, the proxy.
type Target struct {
	Name      string     `yaml:"name"`
	Proxy     *Proxy     `yaml:"proxy,omitempty"`
	Docker    *Docker    `yaml:"docker,omitempty"`
	Process   *Process   `yaml:"process,omitempty"`
	Readiness *Readiness `yaml:"readiness,omitempty"`

	// Settle is an optional pause after the target reports ready and before its tests start.
	Settle testdef.Duration `yaml:"settle,omitempty"`

	// BaseDir is the directory the target's relative paths resolve against, set when the file loads.
	BaseDir string `yaml:"-"`
}

// Proxy is a target Mecone only sends traffic to.
type Proxy struct {
	URL      string `yaml:"url"`
	Insecure bool   `yaml:"insecure,omitempty"`
	// OriginPort pins the port this target's origin binds, so a proxy Mecone does not configure can
	// be pointed at a known upstream address. Zero takes any free port, which only suits a proxy
	// that is told its upstream some other way.
	OriginPort int `yaml:"origin_port,omitempty"`
}

// Readiness is how Mecone decides a managed target is serving. The default fetches the origin's
// info endpoint through the proxy, which proves the whole client, proxy and origin path works.
type Readiness struct {
	Path     string           `yaml:"path,omitempty"`
	Status   []int            `yaml:"status,omitempty"`
	Timeout  testdef.Duration `yaml:"timeout,omitempty"`
	Interval testdef.Duration `yaml:"interval,omitempty"`
}

// Kind reports how the target runs.
func (t *Target) Kind() Kind {
	switch {
	case t.Docker != nil:
		return KindDocker
	case t.Process != nil:
		return KindProcess
	default:
		return KindProxy
	}
}

// Managed reports whether Mecone starts and stops the target.
func (t *Target) Managed() bool {
	return t.Kind() != KindProxy
}

// Probe returns the readiness settings with defaults applied.
func (t *Target) Probe() Readiness {
	r := Readiness{
		Path:     protocol.InfoPath,
		Status:   []int{http.StatusOK},
		Timeout:  testdef.Duration(defaultReadyTimeout),
		Interval: testdef.Duration(defaultReadyInterval),
	}
	if t.Readiness == nil {
		return r
	}
	if t.Readiness.Path != unset {
		r.Path = t.Readiness.Path
	}
	if len(t.Readiness.Status) > none {
		r.Status = t.Readiness.Status
	}
	if t.Readiness.Timeout > none {
		r.Timeout = t.Readiness.Timeout
	}
	if t.Readiness.Interval > none {
		r.Interval = t.Readiness.Interval
	}
	return r
}
