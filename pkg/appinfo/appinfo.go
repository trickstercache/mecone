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

// Package appinfo holds the application name and build-time version information.
package appinfo

import "cmp"

// Name is the application name.
const Name = "mecone"

var (
	// Version is the application version.
	Version = "dev"
	// BuildTime is the time the binary was built.
	BuildTime string
	// GitCommitID is the git commit the binary was built from.
	GitCommitID string
)

// Set records build-time values; empty arguments leave the current values in place.
func Set(version, buildTime, commitID string) {
	Version = cmp.Or(version, Version)
	BuildTime = cmp.Or(buildTime, BuildTime)
	GitCommitID = cmp.Or(commitID, GitCommitID)
}

// String returns a one-line summary of the application version.
func String() string {
	return Name + " " + Version + labeled(" commit ", GitCommitID) + labeled(" built ", BuildTime)
}

func labeled(label, value string) string {
	if value == "" {
		return ""
	}
	return label + value
}
