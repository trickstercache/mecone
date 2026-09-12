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

// Package target starts and stops the proxies under test: containers, local processes, or nothing
// at all for an endpoint Mecone does not manage.
package target

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/trickstercache/mecone/pkg/config"
)

const (
	unset = ""
	none  = 0

	varName       = "NAME"
	varListenHost = "LISTEN_HOST"
	varListenPort = "LISTEN_PORT"
	varListenAddr = "LISTEN_ADDR"
	varOriginHost = "ORIGIN_HOST"
	varOriginPort = "ORIGIN_PORT"
	varOriginURL  = "ORIGIN_URL"
	varWorkDir    = "WORKDIR"
	varConfigFile = "CONFIG_FILE"
	varPID        = "PID"
)

// Vars are the substitutions available in a target's commands, file contents, environment and
// readiness path.
type Vars struct {
	Name       string
	ListenHost string
	ListenPort int
	OriginHost string
	OriginPort int
	WorkDir    string
	ConfigFile string
}

// OriginURL is the base URL a target uses to reach its origin.
func (v Vars) OriginURL() string {
	return "http://" + v.OriginAddr()
}

// OriginAddr is the address a target uses to reach its origin.
func (v Vars) OriginAddr() string {
	return net.JoinHostPort(v.OriginHost, strconv.Itoa(v.OriginPort))
}

// ListenAddr is the address a managed target binds.
func (v Vars) ListenAddr() string {
	return net.JoinHostPort(v.ListenHost, strconv.Itoa(v.ListenPort))
}

// Expand replaces ${NAME}, ${LISTEN_HOST}, ${LISTEN_PORT}, ${LISTEN_ADDR}, ${ORIGIN_HOST},
// ${ORIGIN_PORT}, ${ORIGIN_URL}, ${WORKDIR} and ${CONFIG_FILE}; anything else is left as written.
func (v Vars) Expand(s string) string {
	return v.expandWith(s, nil)
}

// ExpandAll expands every string in ss.
func (v Vars) ExpandAll(ss []string) []string {
	if len(ss) == none {
		return nil
	}
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = v.Expand(s)
	}
	return out
}

func (v Vars) expandWith(s string, extra map[string]string) string {
	return os.Expand(s, func(key string) string {
		if val, ok := extra[key]; ok {
			return val
		}
		if val, ok := v.lookup(key); ok {
			return val
		}
		return "${" + key + "}"
	})
}

func (v Vars) lookup(key string) (string, bool) {
	switch key {
	case varName:
		return v.Name, true
	case varWorkDir:
		return v.WorkDir, true
	case varConfigFile:
		return v.ConfigFile, true
	}
	if val, ok := v.listenVar(key); ok {
		return val, true
	}
	return v.originVar(key)
}

func (v Vars) listenVar(key string) (string, bool) {
	switch key {
	case varListenHost:
		return v.ListenHost, true
	case varListenPort:
		return strconv.Itoa(v.ListenPort), true
	case varListenAddr:
		return v.ListenAddr(), true
	default:
		return unset, false
	}
}

func (v Vars) originVar(key string) (string, bool) {
	switch key {
	case varOriginHost:
		return v.OriginHost, true
	case varOriginPort:
		return strconv.Itoa(v.OriginPort), true
	case varOriginURL:
		return v.OriginURL(), true
	default:
		return unset, false
	}
}

// Instance is a target that has been started and can receive test traffic once it is ready.
type Instance struct {
	// BaseURL is where the client sends test traffic.
	BaseURL string

	logs func(context.Context) string
	stop func(context.Context) error
}

// Stop shuts a managed target down; it does nothing for an endpoint Mecone does not manage.
func (i *Instance) Stop(ctx context.Context) error {
	if i.stop == nil {
		return nil
	}
	return i.stop(ctx)
}

// Logs returns recent output from a managed target, for diagnosing a failure.
func (i *Instance) Logs(ctx context.Context) string {
	if i.logs == nil {
		return unset
	}
	return i.logs(ctx)
}

// Start brings the target up and returns once its container or process is running. Use WaitReady
// to find out when it is actually serving.
func Start(ctx context.Context, t *config.Target, v Vars, x Exec) (*Instance, error) {
	switch t.Kind() {
	case config.KindDocker:
		return startDocker(ctx, t, v, x)
	case config.KindProcess:
		return startProcess(ctx, t, v, x)
	default:
		return &Instance{BaseURL: strings.TrimSuffix(v.Expand(t.Proxy.URL), "/")}, nil
	}
}
