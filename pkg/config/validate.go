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
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	minStatus = 100
	maxStatus = 999
	maxPort   = 65535
	oneKind   = 1
	pidVar    = "${PID}"
)

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)

// Validate reports every problem with the configuration as one joined error.
func (c *Config) Validate() error {
	errs := c.Run.validate()
	seen := make(map[string]bool, len(c.Targets))
	for i, t := range c.Targets {
		if t == nil {
			errs = append(errs, fmt.Errorf("target %d is empty", i+1))
			continue
		}
		if seen[t.Name] {
			errs = append(errs, fmt.Errorf("target %q is defined more than once", t.Name))
		}
		seen[t.Name] = true
		for _, err := range t.validate() {
			errs = append(errs, fmt.Errorf("target %q: %w", t.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (r *Run) validate() []error {
	var errs []error
	for _, p := range r.Protocols {
		if _, err := proto.Parse(string(p)); err != nil {
			errs = append(errs, fmt.Errorf("run.protocols: %w", err))
		}
	}
	for _, l := range r.Levels {
		if !slices.Contains(testdef.Levels, l) {
			errs = append(errs, fmt.Errorf("run.levels: %q is not must, should, may or info", l))
		}
	}
	if r.Timeout < none {
		errs = append(errs, errors.New("run.timeout must not be negative"))
	}
	return errs
}

func (t *Target) validate() []error {
	var errs []error
	if !namePattern.MatchString(t.Name) {
		errs = append(errs, fmt.Errorf("name %q must start with a letter or digit and hold only letters, digits, '.', '_' or '-'", t.Name))
	}
	defined := []string{}
	for name, set := range map[string]bool{"proxy": t.Proxy != nil, "docker": t.Docker != nil, "process": t.Process != nil} {
		if set {
			defined = append(defined, name)
		}
	}
	slices.Sort(defined)
	if len(defined) != oneKind {
		errs = append(errs, fmt.Errorf("define exactly one of proxy, docker or process, not %d (%v)", len(defined), defined))
	}
	errs = append(errs, t.validateKind()...)
	if t.Readiness != nil {
		errs = append(errs, t.Readiness.validate()...)
	}
	if t.Settle < none {
		errs = append(errs, errors.New("settle must not be negative"))
	}
	return errs
}

func (t *Target) validateKind() []error {
	switch {
	case t.Proxy != nil:
		return t.Proxy.validate()
	case t.Docker != nil:
		return t.Docker.validate()
	case t.Process != nil:
		return t.Process.validate()
	default:
		return nil
	}
}

func (p *Proxy) validate() []error {
	u, err := url.Parse(p.URL)
	if err != nil {
		return []error{fmt.Errorf("proxy.url: %w", err)}
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == unset {
		return []error{fmt.Errorf("proxy.url %q must be an absolute http or https URL", p.URL)}
	}
	if p.OriginPort < none || p.OriginPort > maxPort {
		return []error{fmt.Errorf("proxy.origin_port %d is out of range", p.OriginPort)}
	}
	return nil
}

func (d *Docker) validate() []error {
	var errs []error
	if d.Image == unset {
		errs = append(errs, errors.New("docker.image is required"))
	}
	if !slices.Contains([]string{PullMissing, PullAlways, PullNever}, d.PullPolicy()) {
		errs = append(errs, fmt.Errorf("docker.pull %q is not always, missing or never", d.Pull))
	}
	if d.ContainerPort < none || d.ContainerPort > maxPort {
		errs = append(errs, fmt.Errorf("docker.container_port %d is out of range", d.ContainerPort))
	}
	return append(errs, validateFiles("docker", d.Files, true)...)
}

func (p *Process) validate() []error {
	var errs []error
	if p.Start == unset {
		errs = append(errs, errors.New("process.start is required"))
	}
	if p.Background && p.Stop == unset {
		errs = append(errs, errors.New("process.stop is required when start backgrounds the proxy, since there is no process left to signal"))
	}
	if p.Background && strings.Contains(p.Stop, pidVar) {
		errs = append(errs, errors.New("process.stop cannot use ${PID} when start backgrounds the proxy: "+
			"no process is left to name, so it expands to 0 and signals the caller's whole process group"))
	}
	return append(errs, validateFiles("process", p.Files, false)...)
}

func validateFiles(where string, files []File, absolute bool) []error {
	var errs []error
	for i, f := range files {
		at := fmt.Sprintf("%s.files[%d]", where, i)
		if (f.Source == unset) == (f.Content == unset) {
			errs = append(errs, fmt.Errorf("%s: set exactly one of source or content", at))
		}
		if err := validateFileTarget(at, f.Target, absolute); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

func validateFileTarget(at, target string, absolute bool) error {
	switch {
	case target == unset:
		return fmt.Errorf("%s: target is required", at)
	case absolute && !filepath.IsAbs(target):
		return fmt.Errorf("%s: target %q must be an absolute path inside the container", at, target)
	case !absolute && filepath.IsAbs(target):
		return fmt.Errorf("%s: target %q must be relative to the working directory", at, target)
	default:
		return nil
	}
}

func (r *Readiness) validate() []error {
	var errs []error
	for _, s := range r.Status {
		if s < minStatus || s > maxStatus {
			errs = append(errs, fmt.Errorf("readiness.status %d is out of range", s))
		}
	}
	if r.Timeout < none || r.Interval < none {
		errs = append(errs, errors.New("readiness timeout and interval must not be negative"))
	}
	return errs
}
