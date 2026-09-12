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
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"

	"github.com/trickstercache/mecone/pkg/config"
	"github.com/trickstercache/mecone/pkg/protocol"
)

const (
	dockerBin = "docker"
	// NetworkBridge publishes the proxy's port to this machine, which is how a container reaches a
	// Mecone running outside docker on hosts where a container cannot share the host's network.
	NetworkBridge = "bridge"
	// DockerHostGateway is the name a container uses for the machine running docker.
	DockerHostGateway = "host.docker.internal"

	stopTimeout  = "5"
	logTailLines = "40"
	nameIDLength = 8
	linuxGOOS    = "linux"
	colon        = ":"
)

// NetworkMode is the network a target's container joins, resolving the default for this machine.
// Containers share the host's network only where that works; elsewhere the proxy's port is
// published instead, so one catalog runs on Linux, macOS and Windows alike.
func NetworkMode(d *config.Docker) string {
	switch {
	case d.Network != unset:
		return d.Network
	case runtime.GOOS == linuxGOOS:
		return config.NetworkHost
	default:
		return NetworkBridge
	}
}

// OriginHost is the host a target uses to reach its origin when the configuration names none.
func OriginHost(t *config.Target) string {
	if t.Docker == nil {
		return config.LoopbackHost
	}
	mode := NetworkMode(t.Docker)
	switch {
	case mode == config.NetworkHost && runtime.GOOS == linuxGOOS:
		return config.LoopbackHost
	case mode == config.NetworkHost, mode == NetworkBridge:
		return DockerHostGateway
	default:
		return hostname()
	}
}

func startDocker(ctx context.Context, t *config.Target, v Vars, x Exec) (*Instance, error) {
	dir, cleanup, err := workDir(t.Name)
	if err != nil {
		return nil, err
	}
	v.WorkDir = dir
	files, v, err := renderFiles(t.Docker.Files, v, t.BaseDir, staged)
	if err != nil {
		cleanup()
		return nil, err
	}
	name := containerName(t.Name)
	out, err := x.Output(ctx, Command{Path: dockerBin, Args: dockerArgs(t.Docker, v, name, files)})
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("docker run %s: %w: %s", t.Docker.Ref(), err, bytes.TrimSpace(out))
	}
	return &Instance{
		BaseURL: dockerURL(t.Docker, v, name),
		logs:    func(ctx context.Context) string { return dockerLogs(ctx, x, name) },
		stop:    func(ctx context.Context) error { return stopContainer(ctx, x, name, cleanup) },
	}, nil
}

func stopContainer(ctx context.Context, x Exec, name string, cleanup func()) error {
	defer cleanup()
	out, err := x.Output(ctx, Command{Path: dockerBin, Args: []string{"stop", "-t", stopTimeout, name}})
	if err != nil {
		return fmt.Errorf("docker stop %s: %w: %s", name, err, bytes.TrimSpace(out))
	}
	return nil
}

func dockerArgs(d *config.Docker, v Vars, name string, files []rendered) []string {
	args := []string{"run", "--detach", "--rm", "--name", name, "--pull", d.PullPolicy()}
	args = append(args, networkArgs(d, v)...)
	for _, e := range d.Env {
		args = append(args, "--env", v.Expand(e))
	}
	for _, f := range files {
		args = append(args, "--volume", f.host+colon+f.target+":ro")
	}
	for _, vol := range d.Volumes {
		args = append(args, "--volume", v.Expand(vol))
	}
	args = append(args, d.Ref())
	args = append(args, v.ExpandAll(d.Command)...)
	return append(args, v.ExpandAll(d.Args)...)
}

func networkArgs(d *config.Docker, v Vars) []string {
	port := strconv.Itoa(v.ListenPort)
	switch mode := NetworkMode(d); mode {
	case config.NetworkHost:
		return []string{"--network", config.NetworkHost}
	case NetworkBridge:
		return []string{
			"--publish", v.ListenHost + colon + port + colon + port,
			"--add-host", DockerHostGateway + ":host-gateway",
		}
	default:
		return []string{"--network", mode}
	}
}

// dockerURL is where the client reaches the proxy: a host or published port on this machine, or
// the container itself when Mecone shares a docker network with it.
func dockerURL(d *config.Docker, v Vars, name string) string {
	if mode := NetworkMode(d); mode == config.NetworkHost || mode == NetworkBridge {
		return "http://" + v.ListenAddr()
	}
	return "http://" + name + colon + strconv.Itoa(v.ListenPort)
}

func dockerLogs(ctx context.Context, x Exec, name string) string {
	out, err := x.Output(ctx, Command{Path: dockerBin, Args: []string{"logs", "--tail", logTailLines, name}})
	if err != nil {
		return unset
	}
	return string(bytes.TrimSpace(out))
}

func containerName(name string) string {
	return "mecone-" + name + "-" + protocol.NewID()[:nameIDLength]
}

func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return config.LoopbackHost
	}
	return name
}
