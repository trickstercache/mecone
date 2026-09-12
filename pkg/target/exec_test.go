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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const markerFile = "marker"

func TestSystemOutput(t *testing.T) {
	x := System()
	out, err := x.Output(t.Context(), Command{Path: shellBin, Args: []string{shellFlag, "echo hello"}})
	if err != nil || strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("Output = %q, %v", out, err)
	}
	if _, err := x.Output(t.Context(), Command{Path: shellBin, Args: []string{shellFlag, "exit 3"}}); err == nil {
		t.Error("a failing command reported success")
	}
}

func TestSystemOutputDirAndEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, markerFile), []byte("here"), testFilePerm); err != nil {
		t.Fatal(err)
	}
	out, err := System().Output(t.Context(), Command{
		Path: shellBin,
		Args: []string{shellFlag, "cat " + markerFile + "; echo $MECONE_TEST"},
		Dir:  dir,
		Env:  []string{"MECONE_TEST=env-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"here", "env-value"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output %q lacks %q", out, want)
		}
	}
}

func TestSystemStart(t *testing.T) {
	logs := new(ring)
	run, err := System().Start(t.Context(), Command{
		Path: shellBin,
		Args: []string{shellFlag, "echo started; sleep 30"},
		Log:  logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.PID() <= none {
		t.Errorf("PID = %d", run.PID())
	}
	if err := run.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// a signalled shell exits non-zero, so Wait reporting an error is the expected outcome here
	if err := run.Wait(); err == nil {
		t.Error("Wait on a terminated process reported success")
	}
}

func TestSystemStartFailure(t *testing.T) {
	if _, err := System().Start(t.Context(), Command{Path: filepath.Join(t.TempDir(), "no-such-binary")}); err == nil {
		t.Error("starting a missing binary reported success")
	}
}

func TestSignalGroupRejectsNonUnixSignals(t *testing.T) {
	if err := signalGroup(none, syscall.SIGTERM); err == nil {
		t.Error("signalling an unstarted process reported success")
	}
}
