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

package protocol

import (
	"slices"
	"testing"
)

var stepLog = Log{{Step: 1, Seq: 1}, {Step: 2, Seq: 2}, {Step: 1, Seq: 3}}

var stepSeqs = map[int][]int{1: {1, 3}, 2: {2}, 4: nil}

func TestLogStep(t *testing.T) {
	for step, want := range stepSeqs {
		if got := seqs(stepLog.Step(step)); !slices.Equal(got, want) {
			t.Errorf("Step(%d) seqs = %v, want %v", step, got, want)
		}
	}
}

func seqs(entries []LogEntry) []int {
	var out []int
	for _, e := range entries {
		out = append(out, e.Seq)
	}
	return out
}
