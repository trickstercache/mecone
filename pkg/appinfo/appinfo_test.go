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

package appinfo

import "testing"

const (
	unset       = ""
	testVersion = "1.2.3"
	testBuilt   = "2026-09-11"
	testCommit  = "abc"
)

func TestSetAndString(t *testing.T) {
	v, b, c := Version, BuildTime, GitCommitID
	t.Cleanup(func() { Version, BuildTime, GitCommitID = v, b, c })
	Set(unset, unset, unset)
	if Version != v || BuildTime != b || GitCommitID != c {
		t.Fatal("empty values must not overwrite")
	}
	Version, BuildTime, GitCommitID = testVersion, unset, unset
	if got, want := String(), Name+" "+testVersion; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	Set(testVersion, testBuilt, testCommit)
	if got, want := String(), "mecone 1.2.3 commit abc built 2026-09-11"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
