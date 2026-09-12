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
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const idLen = 32

// NewID returns a random test ID of 32 lowercase hex characters.
func NewID() string {
	var b [idLen / 2]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ValidID reports whether s is a well-formed test ID.
func ValidID(s string) bool {
	if len(s) != idLen {
		return false
	}
	for i := range len(s) {
		if !isLowerHex(s[i]) {
			return false
		}
	}
	return true
}

func isLowerHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f')
}

// TestPath returns the request path for a test, with file appended when it is not empty.
func TestPath(id, file string) string {
	if file == "" {
		return TestPrefix + id
	}
	return TestPrefix + id + "/" + strings.TrimPrefix(file, "/")
}
