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
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/trickstercache/mecone/pkg/config"
)

const (
	shellBin  = "/bin/sh"
	shellFlag = "-c"
	logLimit  = 8 << 10
	// exitBuffer keeps the goroutine watching for an exit from blocking once the stop gives up.
	exitBuffer = 1
)

// stopGrace is how long a target has to exit after being asked, before it is killed outright.
var stopGrace = 5 * time.Second

func startProcess(ctx context.Context, t *config.Target, v Vars, x Exec) (*Instance, error) {
	dir, cleanup, err := workDir(t.Name)
	if err != nil {
		return nil, err
	}
	// files are rendered where the command runs, so a relative target resolves the way it reads
	v.WorkDir = commandDir(t, v, dir)
	if _, v, err = renderFiles(t.Process.Files, v, t.BaseDir, inPlace); err != nil {
		cleanup()
		return nil, err
	}
	logs := new(ring)
	cmd := Command{
		Path: shellBin,
		Args: []string{shellFlag, v.Expand(t.Process.Start)},
		Dir:  v.WorkDir,
		Env:  v.ExpandAll(t.Process.Env),
		Log:  logs,
	}
	inst := &Instance{
		BaseURL: "http://" + v.ListenAddr(),
		logs:    func(context.Context) string { return logs.String() },
	}
	if err := launch(ctx, t, v, x, cmd, inst, logs, cleanup); err != nil {
		cleanup()
		return nil, err
	}
	return inst, nil
}

func launch(ctx context.Context, t *config.Target, v Vars, x Exec, cmd Command, inst *Instance, logs *ring, cleanup func()) error {
	if t.Process.Background {
		out, err := x.Output(ctx, cmd)
		_, _ = logs.Write(out)
		if err != nil {
			return fmt.Errorf("process start: %w: %s", err, logs.String())
		}
		inst.stop = func(ctx context.Context) error { return runStop(ctx, t, v, x, cmd, none, cleanup) }
		return nil
	}
	run, err := x.Start(ctx, cmd)
	if err != nil {
		return fmt.Errorf("process start: %w", err)
	}
	inst.stop = func(ctx context.Context) error { return stopRunning(ctx, t, v, x, cmd, run, cleanup) }
	return nil
}

func commandDir(t *config.Target, v Vars, dir string) string {
	if t.Process.WorkDir == unset {
		return dir
	}
	// inside workdir itself, ${WORKDIR} can only mean the temporary directory
	v.WorkDir = dir
	if d := v.Expand(t.Process.WorkDir); filepath.IsAbs(d) {
		return d
	}
	return filepath.Join(t.BaseDir, v.Expand(t.Process.WorkDir))
}

func stopRunning(ctx context.Context, t *config.Target, v Vars, x Exec, cmd Command, run Running, cleanup func()) error {
	defer cleanup()
	exited := make(chan error, exitBuffer)
	go func() { exited <- run.Wait() }()
	if err := signalStop(ctx, t, v, x, cmd, run); err != nil {
		return err
	}
	select {
	case <-exited:
		return nil
	case <-time.After(stopGrace):
		return run.Signal(syscall.SIGKILL)
	}
}

func signalStop(ctx context.Context, t *config.Target, v Vars, x Exec, cmd Command, run Running) error {
	if t.Process.Stop == unset {
		return run.Signal(syscall.SIGTERM)
	}
	return runStop(ctx, t, v, x, cmd, run.PID(), nil)
}

func runStop(ctx context.Context, t *config.Target, v Vars, x Exec, cmd Command, pid int, cleanup func()) error {
	if cleanup != nil {
		defer cleanup()
	}
	script := v.expandWith(t.Process.Stop, map[string]string{varPID: strconv.Itoa(pid)})
	stop := Command{Path: shellBin, Args: []string{shellFlag, script}, Dir: cmd.Dir, Env: cmd.Env}
	out, err := x.Output(ctx, stop)
	if err != nil {
		return fmt.Errorf("process stop: %w: %s", err, out)
	}
	return nil
}

// ring keeps the most recent output of a managed process, for diagnosing a failure to start.
type ring struct {
	mu  sync.Mutex
	buf []byte
}

func (r *ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > logLimit {
		r.buf = r.buf[len(r.buf)-logLimit:]
	}
	return len(p), nil
}

func (r *ring) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
