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
	"io"
	"os"
	"os/exec"
)

// Command is one command to run.
type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
	// Log receives the command's output while it runs.
	Log io.Writer
}

// Running is a command that was started and left running.
type Running interface {
	PID() int
	Signal(sig os.Signal) error
	Wait() error
}

// Exec runs the commands that manage a target; tests substitute their own.
type Exec interface {
	Output(ctx context.Context, c Command) ([]byte, error)
	Start(ctx context.Context, c Command) (Running, error)
}

// System returns an Exec that runs real commands. Each starts in its own process group, so a
// proxy that forks workers can be stopped along with them.
func System() Exec {
	return system{}
}

type system struct{}

func (system) Output(ctx context.Context, c Command) ([]byte, error) {
	return newCmd(ctx, c).CombinedOutput()
}

func (system) Start(ctx context.Context, c Command) (Running, error) {
	cmd := newCmd(ctx, c)
	if c.Log != nil {
		cmd.Stdout, cmd.Stderr = c.Log, c.Log
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &process{cmd: cmd}, nil
}

func newCmd(ctx context.Context, c Command) *exec.Cmd {
	cmd := execCommand(ctx, c.Path)
	cmd.Args = append(cmd.Args, c.Args...)
	cmd.Dir = c.Dir
	if len(c.Env) > none {
		cmd.Env = append(os.Environ(), c.Env...)
	}
	setProcessGroup(cmd)
	return cmd
}

func execCommand(ctx context.Context, path string) *exec.Cmd {
	// the binary runs directly rather than through a shell, and its arguments are added as separate
	// words, so nothing a catalog names is interpreted as a command
	return exec.CommandContext(ctx, path)
}

type process struct {
	cmd *exec.Cmd
}

func (p *process) PID() int {
	if p.cmd.Process == nil {
		return none
	}
	return p.cmd.Process.Pid
}

func (p *process) Signal(sig os.Signal) error {
	if err := signalGroup(p.PID(), sig); err == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}

func (p *process) Wait() error {
	return p.cmd.Wait()
}
