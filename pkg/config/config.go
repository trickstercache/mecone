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

// Package config defines Mecone's optional configuration file: client defaults, origin settings
// and the catalog of proxy targets to test.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/trickstercache/mecone/pkg/proto"
	"github.com/trickstercache/mecone/pkg/testdef"

	"go.yaml.in/yaml/v3"
)

const (
	unset = ""
	none  = 0

	// DefaultTargetConcurrency is how many targets are tested at once when the file sets no value.
	DefaultTargetConcurrency = 10
	// LoopbackHost is the interface per-target origins bind unless a target joins a docker network.
	LoopbackHost = "127.0.0.1"
	// AllInterfaces is the bind address used when a target reaches the origin over a docker network.
	AllInterfaces = "0.0.0.0"
)

// Config is a Mecone configuration file. Every section is optional; a file with no targets only
// supplies client defaults.
type Config struct {
	Run     Run       `yaml:"run,omitempty"`
	Origin  Origin    `yaml:"origin,omitempty"`
	Targets []*Target `yaml:"targets,omitempty"`
	// Notice is a line about this catalog that reports print under their scores, for context a
	// reader of the numbers needs and would otherwise have to find in the documentation.
	Notice string `yaml:"notice,omitempty"`

	// Dir is the directory holding the file, which a target's relative paths resolve against.
	Dir string `yaml:"-"`
}

// Run holds client settings the file can set. A flag given on the command line overrides them.
type Run struct {
	Protocols   []proto.Proto    `yaml:"protocols,omitempty"`
	Suites      []string         `yaml:"suites,omitempty"`
	Tests       []string         `yaml:"tests,omitempty"`
	Levels      []testdef.Level  `yaml:"levels,omitempty"`
	SuitesDir   string           `yaml:"suites_dir,omitempty"`
	Concurrency int              `yaml:"concurrency,omitempty"`
	Targets     int              `yaml:"target_concurrency,omitempty"`
	Timeout     testdef.Duration `yaml:"timeout,omitempty"`
	Insecure    bool             `yaml:"insecure,omitempty"`
}

// Origin configures the origins Mecone starts for the targets it manages.
type Origin struct {
	// Listen is the interface per-target origins bind; empty means loopback, or all interfaces
	// when a target reaches the origin over a docker network.
	Listen string `yaml:"listen,omitempty"`
	// Advertise is the host a target uses to reach its origin; empty means one is chosen per target.
	Advertise string `yaml:"advertise,omitempty"`
}

// Load reads, decodes and validates the configuration file at path.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	c := new(Config)
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.Dir = filepath.Dir(path)
	for _, t := range c.Targets {
		if t != nil {
			t.BaseDir = c.Dir
		}
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// TargetConcurrency is how many targets may be tested at once.
func (c *Config) TargetConcurrency() int {
	if c.Run.Targets <= none {
		return DefaultTargetConcurrency
	}
	return c.Run.Targets
}

// ListenHost is the interface per-target origins bind. Targets reached over a docker network need
// the origin on every interface, since Mecone itself is a peer on that network.
func (c *Config) ListenHost() string {
	if c.Origin.Listen != unset {
		return c.Origin.Listen
	}
	for _, t := range c.Targets {
		if t.Docker != nil && t.Docker.JoinsNetwork() {
			return AllInterfaces
		}
	}
	return LoopbackHost
}

// Path resolves a target's relative path against the directory holding the configuration file.
func (c *Config) Path(p string) string {
	if p == unset || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}
