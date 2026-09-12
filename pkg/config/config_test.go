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
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/trickstercache/mecone/pkg/protocol"
	"github.com/trickstercache/mecone/pkg/testdef"
)

const (
	filePerm   = 0o600
	configName = "mecone.yaml"
	imageNginx = "nginx"

	proxyTarget  = 0
	dockerTarget = 1
	targetCount  = 3

	firstLevel  = 0
	secondProto = 1

	wantProtocols   = 2
	wantConcurrency = 16
	wantTargetConc  = 2
	wantTimeout     = 45 * time.Second
	wantNotice      = "a note about this catalog"

	wantProbeStatus  = 2
	wantProbeTimeout = 10 * time.Second
	customPort       = 9000

	full = `
notice: a note about this catalog
run:
  protocols: [h1, h2c]
  suites: [caching]
  tests: ["storage-*"]
  levels: [must, should]
  concurrency: 16
  target_concurrency: 2
  timeout: 45s
origin:
  advertise: gateway.test
targets:
  - name: staging
    proxy:
      url: https://proxy.example.com/edge
      insecure: true
    readiness:
      path: /healthz
      status: [200, 204]
      timeout: 10s
      interval: 100ms
  - name: nginx
    docker:
      image: nginx
      tag: 1.27-alpine
      env: ["K=V"]
      files:
        - source: conf/nginx.conf
          target: /etc/nginx/nginx.conf
    settle: 250ms
  - name: local
    process:
      start: "proxy -p ${LISTEN_PORT}"
      stop: "proxy --quit"
      background: true
      files:
        - content: "listen ${LISTEN_PORT}"
          target: proxy.conf
`
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), filePerm); err != nil {
		t.Fatal(err)
	}
	return path
}

func loadBody(t *testing.T, body string) (*Config, error) {
	t.Helper()
	return Load(write(t, t.TempDir(), configName, body))
}

func mustLoad(t *testing.T, body string) *Config {
	t.Helper()
	c, err := loadBody(t, body)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLoadRun(t *testing.T) {
	c := mustLoad(t, full)
	r := c.Run
	if len(r.Protocols) != wantProtocols || r.Protocols[secondProto] != "h2c" || r.Concurrency != wantConcurrency {
		t.Errorf("run = %+v", r)
	}
	if c.Notice != wantNotice {
		t.Errorf("notice = %q, want %q", c.Notice, wantNotice)
	}
	if time.Duration(r.Timeout) != wantTimeout || r.Levels[firstLevel] != testdef.LevelMust {
		t.Errorf("run = %+v", r)
	}
	if c.TargetConcurrency() != wantTargetConc {
		t.Errorf("target concurrency = %d", c.TargetConcurrency())
	}
	if got := (&Config{}).TargetConcurrency(); got != DefaultTargetConcurrency {
		t.Errorf("default target concurrency = %d", got)
	}
}

func TestLoadTargets(t *testing.T) {
	c := mustLoad(t, full)
	if len(c.Targets) != targetCount {
		t.Fatalf("loaded %d targets", len(c.Targets))
	}
	kinds := map[string]Kind{"staging": KindProxy, imageNginx: KindDocker, "local": KindProcess}
	for _, tg := range c.Targets {
		if tg.Kind() != kinds[tg.Name] {
			t.Errorf("%s: kind %s", tg.Name, tg.Kind())
		}
		if tg.Managed() != (tg.Kind() != KindProxy) || tg.BaseDir != c.Dir {
			t.Errorf("%s: managed %v, base dir %q", tg.Name, tg.Managed(), tg.BaseDir)
		}
	}
}

func TestProbeDefaults(t *testing.T) {
	c := mustLoad(t, full)
	custom := c.Targets[proxyTarget].Probe()
	if custom.Path != "/healthz" || len(custom.Status) != wantProbeStatus || time.Duration(custom.Timeout) != wantProbeTimeout {
		t.Errorf("custom probe = %+v", custom)
	}
	fallback := c.Targets[dockerTarget].Probe()
	if fallback.Path != protocol.InfoPath || !slices.Equal(fallback.Status, []int{http.StatusOK}) ||
		fallback.Timeout <= none || fallback.Interval <= none {
		t.Errorf("default probe = %+v", fallback)
	}
}

func TestDockerDefaults(t *testing.T) {
	c := mustLoad(t, full)
	d := c.Targets[dockerTarget].Docker
	if d.Ref() != "nginx:1.27-alpine" || d.JoinsNetwork() || d.Port() != DefaultContainerPort || d.PullPolicy() != PullMissing {
		t.Errorf("docker = %+v", d)
	}
	untagged := &Docker{Image: imageNginx, Network: "lab", ContainerPort: customPort, Pull: PullAlways}
	if untagged.Ref() != imageNginx || !untagged.JoinsNetwork() || untagged.Port() != customPort || untagged.PullPolicy() != PullAlways {
		t.Errorf("docker = %+v", untagged)
	}
}

func TestListenHost(t *testing.T) {
	if got := mustLoad(t, full).ListenHost(); got != LoopbackHost {
		t.Errorf("listen host = %q", got)
	}
	networked := mustLoad(t, "targets:\n  - name: lab\n    docker:\n      image: nginx\n      network: lab\n")
	if got := networked.ListenHost(); got != AllInterfaces {
		t.Errorf("networked listen host = %q", got)
	}
	explicit := mustLoad(t, "origin:\n  listen: 10.0.0.1\ntargets:\n  - name: lab\n    docker: {image: nginx, network: lab}\n")
	if got := explicit.ListenHost(); got != "10.0.0.1" {
		t.Errorf("explicit listen host = %q", got)
	}
}

func TestPath(t *testing.T) {
	c := mustLoad(t, full)
	if got := c.Path("conf/x.conf"); got != filepath.Join(c.Dir, "conf/x.conf") {
		t.Errorf("relative path = %q", got)
	}
	for _, p := range []string{unset, "/etc/x.conf"} {
		if got := c.Path(p); got != p {
			t.Errorf("Path(%q) = %q", p, got)
		}
	}
}

func TestLoadEmptyAndMissing(t *testing.T) {
	c, err := loadBody(t, "")
	if err != nil || len(c.Targets) != none {
		t.Errorf("empty config = %+v, %v", c, err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("loading a missing file succeeded")
	}
}

var invalidConfigs = map[string]string{
	"unknown field":         "bogus: 1\n",
	"malformed yaml":        "targets: [unclosed\n",
	"empty target":          "targets:\n  - null\n",
	"no type":               "targets:\n  - name: x\n",
	"two types":             "targets:\n  - name: x\n    proxy: {url: 'http://a'}\n    docker: {image: nginx}\n",
	"bad name":              "targets:\n  - name: Not Valid\n    docker: {image: nginx}\n",
	"duplicate name":        "targets:\n  - name: x\n    docker: {image: nginx}\n  - name: x\n    docker: {image: nginx}\n",
	"relative proxy url":    "targets:\n  - name: x\n    proxy: {url: '/edge'}\n",
	"bad proxy scheme":      "targets:\n  - name: x\n    proxy: {url: 'ftp://a'}\n",
	"no image":              "targets:\n  - name: x\n    docker: {image: ''}\n",
	"bad pull":              "targets:\n  - name: x\n    docker: {image: nginx, pull: sometimes}\n",
	"bad container port":    "targets:\n  - name: x\n    docker: {image: nginx, container_port: 70000}\n",
	"no start":              "targets:\n  - name: x\n    process: {start: ''}\n",
	"background no stop":    "targets:\n  - name: x\n    process: {start: 'p', background: true}\n",
	"file both sources":     "targets:\n  - name: x\n    docker:\n      image: nginx\n      files: [{source: a, content: b, target: /c}]\n",
	"file no source":        "targets:\n  - name: x\n    docker:\n      image: nginx\n      files: [{target: /c}]\n",
	"file no target":        "targets:\n  - name: x\n    docker:\n      image: nginx\n      files: [{content: a, target: ''}]\n",
	"docker file relative":  "targets:\n  - name: x\n    docker:\n      image: nginx\n      files: [{content: a, target: rel.conf}]\n",
	"process file absolute": "targets:\n  - name: x\n    process:\n      start: p\n      files: [{content: a, target: /abs.conf}]\n",
	"bad readiness status":  "targets:\n  - name: x\n    docker: {image: nginx}\n    readiness: {status: [99]}\n",
	"negative settle":       "targets:\n  - name: x\n    docker: {image: nginx}\n    settle: -1s\n",
	"bad level":             "run:\n  levels: [sometimes]\n",
	"bad protocol":          "run:\n  protocols: [h9]\n",
	"negative timeout":      "run:\n  timeout: -5s\n",
}

func TestLoadInvalid(t *testing.T) {
	for name, body := range invalidConfigs {
		t.Run(name, func(t *testing.T) {
			if c, err := loadBody(t, body); err == nil {
				t.Errorf("loaded without error: %+v", c)
			}
		})
	}
}
