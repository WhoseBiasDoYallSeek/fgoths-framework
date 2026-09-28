// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// Package runtime provides the embedded execution layer for FGOTHS apps.
//
// The initial implementation focuses on a native Go HTTP router and reverse
// proxy primitives that are compatible with the runtime design described in the
// ADRs without requiring CGO or external Envoy/native dependencies.
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"time"
)

type requestIDContextKey string

const requestIDContextKeyValue requestIDContextKey = "fgoths.request.id"

// WithRequestID injects a request correlation ID into the request context and
// response headers. If an incoming trace or correlation header already exists,
// it is reused to keep diagnostics consistent across the call chain.
func (s *Server) WithRequestID() *Server {
	if s == nil || s.router == nil {
		return s
	}
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			requestID := ensureRequestID(r)
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("X-Correlation-ID", requestID)
			w.Header().Set("X-Trace-ID", requestID)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKeyValue, requestID)))
		})
	})
	return s
}

func ensureRequestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if requestID := RequestIDFromRequest(r); requestID != "" {
		return requestID
	}
	requestID := newRequestID()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	for _, header := range []string{"X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if r.Header.Get(header) == "" {
			r.Header.Set(header, requestID)
		}
	}
	return requestID
}

// RequestIDFromRequest extracts a request correlation ID from the current request
// context or the canonical inbound headers.
func RequestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id, ok := r.Context().Value(requestIDContextKeyValue).(string); ok && id != "" {
		return id
	}
	for _, header := range []string{"X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if id := strings.TrimSpace(r.Header.Get(header)); id != "" {
			return id
		}
	}
	return ""
}

func newRequestID() string {
	var b [8]byte
	if _, err := randRead(b[:]); err == nil {
		return "fgoths-" + hex.EncodeToString(b[:])
	}
	return "fgoths-" + time.Now().UTC().Format("20060102150405.000000000")
}

// randRead is an indirection over crypto/rand.Read so tests can force the
// fallback path (same pattern as osExit/osGetenv in internal/cli).
var randRead = rand.Read
