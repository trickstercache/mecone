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
	"encoding/json"
	"net/http"
	"time"

	"github.com/trickstercache/mecone/pkg/appinfo"
	"github.com/trickstercache/mecone/pkg/protocol"
)

const (
	maxScriptBytes = 4 << 20
	idParam        = "id"
)

func (s *Server) routes() {
	s.mux.HandleFunc("GET "+protocol.InfoPath, s.handleInfo)
	s.mux.HandleFunc("PUT "+protocol.ScriptsPath+"{id}", s.handlePutScript)
	s.mux.HandleFunc("DELETE "+protocol.ScriptsPath+"{id}", s.handleDeleteScript)
	s.mux.HandleFunc("GET "+protocol.LogsPath+"{id}", s.handleLog)
	s.mux.HandleFunc(protocol.TestPrefix+"{id}", s.serveTest)
	s.mux.HandleFunc(protocol.TestPrefix+"{id}/{path...}", s.serveTest)
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, protocol.Info{
		Name:      appinfo.Name,
		Version:   appinfo.Version,
		Protocol:  protocol.Version,
		Listeners: s.listeners(),
	})
}

func (s *Server) handlePutScript(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue(idParam)
	if !protocol.ValidID(id) {
		controlError(w, http.StatusBadRequest, "invalid test id")
		return
	}
	var sc protocol.Script
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxScriptBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sc); err != nil {
		controlError(w, http.StatusBadRequest, "invalid script: "+err.Error())
		return
	}
	s.store.put(id, sc, time.Now())
	noStore(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteScript(w http.ResponseWriter, r *http.Request) {
	s.store.remove(r.PathValue(idParam))
	noStore(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	e := s.store.get(r.PathValue(idParam))
	if e == nil {
		controlError(w, http.StatusNotFound, "unknown test id")
		return
	}
	writeJSON(w, http.StatusOK, e.snapshot())
}

func noStore(w http.ResponseWriter) {
	// control-plane and error responses must stay out of the cache under test
	w.Header().Set("Cache-Control", "no-store")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	noStore(w)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func controlError(w http.ResponseWriter, status int, msg string) {
	noStore(w)
	http.Error(w, msg, status)
}
