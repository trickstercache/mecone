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

const (
	// NetworkHost runs a container in the host's network namespace, which is the default.
	NetworkHost = "host"
	// DefaultContainerPort is the port a container's proxy listens on when it joins a docker network.
	DefaultContainerPort = 8080

	// PullMissing pulls the image only when it is not present locally, which is the default.
	PullMissing = "missing"
	// PullAlways pulls the image before every run.
	PullAlways = "always"
	// PullNever fails rather than pulling an image that is not present locally.
	PullNever = "never"
)

// Docker runs the proxy in a container for the duration of its tests.
type Docker struct {
	Image string `yaml:"image"`
	Tag   string `yaml:"tag,omitempty"`
	// Network is "host" (the default, for a Mecone running on this host) or the name of a docker
	// network, which Mecone must also be running on.
	Network string `yaml:"network,omitempty"`
	// ContainerPort is the port the proxy listens on inside the container; it applies when the
	// container joins a docker network rather than the host's.
	ContainerPort int      `yaml:"container_port,omitempty"`
	Command       []string `yaml:"command,omitempty"`
	Args          []string `yaml:"args,omitempty"`
	Env           []string `yaml:"env,omitempty"`
	Volumes       []string `yaml:"volumes,omitempty"`
	Pull          string   `yaml:"pull,omitempty"`
	Files         []File   `yaml:"files,omitempty"`
}

// Process runs the proxy as a local command for the duration of its tests.
type Process struct {
	Start string `yaml:"start"`
	// Stop runs to shut the proxy down; ${PID} is the started process. Without it, Mecone signals
	// the process it started, which a backgrounding start command does not leave behind.
	Stop string `yaml:"stop,omitempty"`
	// Background marks a start command that returns once the proxy is running, rather than one
	// that runs in the foreground for the whole session.
	Background bool     `yaml:"background,omitempty"`
	WorkDir    string   `yaml:"workdir,omitempty"`
	Env        []string `yaml:"env,omitempty"`
	Files      []File   `yaml:"files,omitempty"`
}

// File is a file rendered for a target, with its templates expanded, before the target starts.
// Exactly one of Source and Content supplies the text.
type File struct {
	// Source is a path relative to the configuration file.
	Source string `yaml:"source,omitempty"`
	// Content is the text of the file, written inline.
	Content string `yaml:"content,omitempty"`
	// Target is where the file lands: an absolute path in the container, or a path relative to a
	// process target's working directory.
	Target string `yaml:"target"`
}

// Ref is the image reference the container runs, including its tag.
func (d *Docker) Ref() string {
	if d.Tag == unset {
		return d.Image
	}
	return d.Image + ":" + d.Tag
}

// JoinsNetwork reports whether the container joins a docker network rather than the host's.
func (d *Docker) JoinsNetwork() bool {
	return d.Network != unset && d.Network != NetworkHost
}

// Port is the port the proxy listens on inside the container when it joins a docker network.
func (d *Docker) Port() int {
	if d.ContainerPort <= none {
		return DefaultContainerPort
	}
	return d.ContainerPort
}

// PullPolicy is the image pull policy, defaulting to pulling only what is missing.
func (d *Docker) PullPolicy() string {
	if d.Pull == unset {
		return PullMissing
	}
	return d.Pull
}
