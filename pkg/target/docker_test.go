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
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/trickstercache/mecone/pkg/config"
)

func dockerTarget(d *config.Docker) *config.Target {
	return &config.Target{Name: testName, Docker: d}
}

func TestNetworkMode(t *testing.T) {
	if got := NetworkMode(&config.Docker{Network: networkLab}); got != networkLab {
		t.Errorf("named network = %q", got)
	}
	want := NetworkBridge
	if runtime.GOOS == linuxGOOS {
		want = config.NetworkHost
	}
	if got := NetworkMode(&config.Docker{}); got != want {
		t.Errorf("default network = %q, want %q", got, want)
	}
}

func TestOriginHost(t *testing.T) {
	if got := OriginHost(&config.Target{}); got != config.LoopbackHost {
		t.Errorf("unmanaged origin host = %q", got)
	}
	if got := OriginHost(dockerTarget(&config.Docker{Network: networkLab})); got != hostname() {
		t.Errorf("networked origin host = %q", got)
	}
	want := DockerHostGateway
	if runtime.GOOS == linuxGOOS {
		want = config.LoopbackHost
	}
	if got := OriginHost(dockerTarget(&config.Docker{Network: config.NetworkHost})); got != want {
		t.Errorf("host network origin host = %q, want %q", got, want)
	}
	if got := OriginHost(dockerTarget(&config.Docker{Network: NetworkBridge})); got != DockerHostGateway {
		t.Errorf("bridge origin host = %q", got)
	}
}

func TestNetworkArgs(t *testing.T) {
	v := testVars()
	cases := map[string][]string{
		config.NetworkHost: {"--network", "host"},
		NetworkBridge:      {"--publish", "127.0.0.1:18080:18080", "--add-host", DockerHostGateway + ":host-gateway"},
		networkLab:         {"--network", networkLab},
	}
	for network, want := range cases {
		if got := networkArgs(&config.Docker{Network: network}, v); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", network, got, want)
		}
	}
}

func TestDockerArgs(t *testing.T) {
	d := &config.Docker{
		Image:   imageNginx,
		Tag:     "1.27",
		Network: config.NetworkHost,
		Env:     []string{"ORIGIN=${ORIGIN_URL}"},
		Volumes: []string{"cache:/var/cache"},
		Command: []string{imageNginx},
		Args:    []string{"-p", "${LISTEN_PORT}"},
		Pull:    config.PullAlways,
	}
	files := []rendered{{host: "/tmp/0-nginx.conf", target: "/etc/nginx/nginx.conf"}}
	got := dockerArgs(d, testVars(), "mecone-proxy-one-abcd1234", files)
	for _, want := range [][]string{
		{"run", "--detach", "--rm", "--name", "mecone-proxy-one-abcd1234", "--pull", config.PullAlways},
		{"--env", "ORIGIN=http://origin.test:9000"},
		{"--volume", "/tmp/0-nginx.conf:/etc/nginx/nginx.conf:ro"},
		{"--volume", "cache:/var/cache"},
		{"nginx:1.27", imageNginx, "-p", "18080"},
	} {
		if !containsRun(got, want) {
			t.Errorf("args %v lack %v", got, want)
		}
	}
}

func containsRun(args, want []string) bool {
	for i := range args {
		if i+len(want) <= len(args) && slices.Equal(args[i:i+len(want)], want) {
			return true
		}
	}
	return false
}

func TestDockerURL(t *testing.T) {
	v := testVars()
	if got := dockerURL(&config.Docker{Network: config.NetworkHost}, v, "c1"); got != testBaseURL {
		t.Errorf("host URL = %q", got)
	}
	if got := dockerURL(&config.Docker{Network: networkLab}, v, "c1"); got != "http://c1:18080" {
		t.Errorf("network URL = %q", got)
	}
}

func TestStartDocker(t *testing.T) {
	x := &fakeExec{out: []byte("container-id")}
	tg := dockerTarget(&config.Docker{
		Image:   imageNginx,
		Tag:     "1.27",
		Network: config.NetworkHost,
		Files:   []config.File{{Content: "listen ${LISTEN_PORT};", Target: "/etc/nginx/nginx.conf"}},
	})
	inst, err := Start(t.Context(), tg, testVars(), x)
	if err != nil {
		t.Fatal(err)
	}
	if inst.BaseURL != testBaseURL {
		t.Errorf("base URL = %q", inst.BaseURL)
	}
	name := startedContainer(t, x)
	if logs := inst.Logs(t.Context()); logs != "container-id" {
		t.Errorf("logs = %q", logs)
	}
	if err := inst.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
	stop := x.seen()[len(x.seen())-fromEnd]
	if !slices.Equal(stop.Args, []string{"stop", "-t", stopTimeout, name}) {
		t.Errorf("stop command = %v", stop.Args)
	}
}

func startedContainer(t *testing.T, x *fakeExec) string {
	t.Helper()
	run := x.seen()[first]
	if run.Path != dockerBin || run.Args[first] != "run" {
		t.Fatalf("first command = %+v", run)
	}
	name := run.Args[slices.Index(run.Args, "--name")+second]
	if !strings.HasPrefix(name, "mecone-"+testName+"-") {
		t.Errorf("container name = %q", name)
	}
	return name
}

func TestStartDockerFailure(t *testing.T) {
	x := &fakeExec{out: []byte("no such image"), err: errors.New("exit status 125")}
	tg := dockerTarget(&config.Docker{Image: imageNginx})
	_, err := Start(t.Context(), tg, testVars(), x)
	if err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Errorf("Start = %v", err)
	}
}

func TestStopDockerFailure(t *testing.T) {
	x := &fakeExec{}
	inst, err := Start(t.Context(), dockerTarget(&config.Docker{Image: imageNginx}), testVars(), x)
	if err != nil {
		t.Fatal(err)
	}
	x.err = errors.New("exit status 1")
	if err := inst.Stop(t.Context()); err == nil {
		t.Error("a failed docker stop reported success")
	}
	if logs := inst.Logs(t.Context()); logs != unset {
		t.Errorf("logs after a failure = %q", logs)
	}
}
