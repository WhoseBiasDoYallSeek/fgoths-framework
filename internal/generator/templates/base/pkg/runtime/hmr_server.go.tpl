// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package runtime

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"{{.ProjectName}}/pkg/runtime/hmr"
)

// hmrHubs stores the HMR hub per server. A map keyed by the server
// pointer avoids adding HMR-specific fields to the core Server struct.
var hmrHubs = struct {
	sync.Mutex
	hubs map[*Server]*hmr.Hub
}{hubs: make(map[*Server]*hmr.Hub)}

// HMRHub exposes the server's hot module replacement hub so the dev
// watcher can broadcast change events to connected browsers.
func (s *Server) HMRHub() *hmr.Hub {
	if s == nil {
		return nil
	}
	hmrHubs.Lock()
	defer hmrHubs.Unlock()
	hub, ok := hmrHubs.hubs[s]
	if !ok {
		// Build identity for this dev boot: every HMR event is stamped
		// with it, so the browser console shows which build produced the
		// change. Uses monotonic boot time unless FGOTHS_BUILD_ID is set.
		buildID := os.Getenv("FGOTHS_BUILD_ID")
		if buildID == "" {
			buildID = "dev-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
		}
		hub = hmr.NewHub(buildID)
		hmrHubs.hubs[s] = hub
	}
	return hub
}

// WithHMR enables hot module replacement endpoints on this server:
//
//	GET  /hmr/events     SSE stream of change events
//	GET  /hmr/hmr.js     browser client script
//	POST /hmr/broadcast  publish a change event (dev watcher only)
//
// HTML responses pass through a wrapper that injects the client script
// before </body>. Development-only: guard with FGOTHS_DEV in main().
func (s *Server) WithHMR() *Server {
	if s == nil || s.Server == nil {
		return s
	}
	hub := s.HMRHub()

	mux := http.NewServeMux()
	mux.Handle("GET /hmr/meta", metaHandler(hub))
	mux.Handle("GET /hmr/events", hub.Handler())
	mux.Handle("GET /hmr/hmr.js", hmr.ScriptHandler())
	mux.Handle("POST /hmr/broadcast", broadcastHandler(hub))

	inner := s.Server.Handler
	s.Server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/hmr/") {
			mux.ServeHTTP(w, r)
			return
		}
		wrapped := &hmrInjectWriter{ResponseWriter: w}
		inner.ServeHTTP(wrapped, r)
		wrapped.finalize()
	})
	return s
}

func metaHandler(hub *hmr.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_ = json.NewEncoder(w).Encode(struct {
			BuildID     string `json:"build_id"`
			SchemaMajor uint16 `json:"schema_major"`
			SchemaMinor uint16 `json:"schema_minor"`
		}{
			BuildID:     hub.BuildID(),
			SchemaMajor: hmr.SchemaMajor,
			SchemaMinor: hmr.SchemaMinor,
		})
	}
}

// broadcastMessage is the JSON body accepted by POST /hmr/broadcast.
type broadcastMessage struct {
	Kind    string `json:"kind"`
	Target  string `json:"target"`
	Payload string `json:"payload"`
	Source  string `json:"source"`
	Route   string `json:"route"`
}

// broadcastHandler accepts change events from the dev watcher and fans
// them out to connected browsers. Only registered when WithHMR is
// enabled (development builds).
func broadcastHandler(hub *hmr.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var msg broadcastMessage
		if err := json.NewDecoder(r.Body).Decode(&msg); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		if msg.Kind == "" {
			msg.Kind = hmr.EventReload
		}
		hub.Broadcast(hmr.Event{
			Kind:    msg.Kind,
			Target:  msg.Target,
			Payload: msg.Payload,
			Source:  msg.Source,
			Route:   msg.Route,
		})
		w.WriteHeader(http.StatusAccepted)
	}
}

// hmrInjectWriter buffers HTML responses and injects the HMR client
// script before </body>. Non-HTML responses pass through untouched.
type hmrInjectWriter struct {
	http.ResponseWriter
	buf    strings.Builder
	status int
}

func (w *hmrInjectWriter) WriteHeader(code int) {
	w.status = code
}

func (w *hmrInjectWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	return len(p), nil
}

// Flush implements http.Flusher pass-through for buffered writers.
func (w *hmrInjectWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finalize writes the buffered body, injecting the client script into
// HTML responses. Content-Type may be unset at this point because the
// buffered writer delayed the response header: sniff it from the body
// the same way net/http would.
func (w *hmrInjectWriter) finalize() {
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	body := w.buf.String()
	contentType := w.Header().Get("Content-Type")
	if contentType == "" && http.DetectContentType([]byte(body)) == "text/html; charset=utf-8" {
		contentType = "text/html; charset=utf-8"
		w.Header().Set("Content-Type", contentType)
	}
	if status >= 200 && status < 300 &&
		strings.Contains(contentType, "text/html") {
		body = hmr.InjectClientScript(body)
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write([]byte(body))
}
