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

package origin

import (
	"sync"
	"time"

	"github.com/trickstercache/mecone/pkg/protocol"
)

const maxLogEntries = 1000

type store struct {
	mu        sync.RWMutex
	tests     map[string]*entry
	ttl       time.Duration
	lastSweep time.Time
}

type entry struct {
	script  protocol.Script
	created time.Time

	mu  sync.Mutex
	seq int
	log []protocol.LogEntry
}

func newStore(ttl time.Duration) *store {
	return &store{tests: make(map[string]*entry), ttl: ttl}
}

func (s *store) put(id string, sc protocol.Script, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.lastSweep) > s.ttl {
		for k, e := range s.tests {
			if now.Sub(e.created) > s.ttl {
				delete(s.tests, k)
			}
		}
		s.lastSweep = now
	}
	// a stored script is never modified, so request handlers read it without locking
	s.tests[id] = &entry{script: sc, created: now}
}

func (s *store) get(id string) *entry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tests[id]
}

func (s *store) remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tests, id)
}

func (e *entry) nextSeq() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seq++
	return e.seq
}

func (e *entry) record(le protocol.LogEntry) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.log) < maxLogEntries {
		e.log = append(e.log, le)
	}
}

func (e *entry) snapshot() protocol.Log {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make(protocol.Log, len(e.log))
	copy(out, e.log)
	return out
}
