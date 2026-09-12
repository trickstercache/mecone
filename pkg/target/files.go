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
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/trickstercache/mecone/pkg/config"
)

const (
	filePerm = 0o644
	dirPerm  = 0o755
)

type placement int

const (
	inPlace placement = iota
	staged
)

// rendered is one configuration file written for a target: where it lives on this machine, and
// where the target reads it from.
type rendered struct {
	host   string
	target string
}

// renderFiles writes each file with its templates expanded. Target paths are resolved first, so a
// file's content can refer to ${CONFIG_FILE}, which names the first file the target reads.
func renderFiles(files []config.File, v Vars, baseDir string, where placement) ([]rendered, Vars, error) {
	if len(files) == none {
		return nil, v, nil
	}
	out := targetPaths(files, v)
	v.ConfigFile = out[none].target
	for i, f := range files {
		out[i].host = hostPath(out[i].target, v.WorkDir, i, where)
		if err := writeFile(f, v, baseDir, out[i].host); err != nil {
			return nil, v, err
		}
	}
	return out, v, nil
}

func targetPaths(files []config.File, v Vars) []rendered {
	out := make([]rendered, len(files))
	for i, f := range files {
		out[i].target = v.Expand(f.Target)
		if !filepath.IsAbs(out[i].target) {
			out[i].target = filepath.Join(v.WorkDir, out[i].target)
		}
	}
	return out
}

func hostPath(target, workDir string, i int, where placement) string {
	if where == inPlace {
		return target
	}
	// a staged file is written beside the others, so one mount carries them all into a container
	return filepath.Join(workDir, fmt.Sprintf("%d-%s", i, filepath.Base(target)))
}

func writeFile(f config.File, v Vars, baseDir, host string) error {
	content, err := fileContent(f, baseDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(host), dirPerm); err != nil {
		return err
	}
	return os.WriteFile(host, []byte(v.Expand(content)), filePerm)
}

func fileContent(f config.File, baseDir string) (string, error) {
	if f.Source == unset {
		return f.Content, nil
	}
	b, err := readSource(filepath.Join(baseDir, f.Source))
	if err != nil {
		return unset, fmt.Errorf("reading %s: %w", f.Source, err)
	}
	return string(b), nil
}

func readSource(path string) ([]byte, error) {
	src, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	return io.ReadAll(src)
}

// workDir creates the directory holding a target's rendered files and its removal function.
func workDir(name string) (string, func(), error) {
	dir, err := os.MkdirTemp(unset, "mecone-"+name+"-")
	if err != nil {
		return unset, nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
