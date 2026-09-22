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
package runtime

import (
	"net/http"
	"strings"
	"time"
)

// AuditEvent captures the minimum metadata required for auditable platform operations.
type AuditEvent struct {
	Timestamp  time.Time
	Duration   time.Duration
	Method     string
	Path       string
	StatusCode int
	Tenant     string
	Subject    string
	RequestID  string
	RemoteAddr string
	UserAgent  string
}

// AuditLogger emits a structured audit event for each request after the handler completes.
func AuditLogger(on func(AuditEvent)) func(http.Handler) http.Handler {
	if on == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			recorder := newStatusRecorder(w)
			next.ServeHTTP(recorder, r)
			claims := ContextClaims(r)
			subject := ""
			if claims != nil {
				if v, ok := claims["sub"].(string); ok {
					subject = v
				}
				if subject == "" {
					if v, ok := claims["user_id"].(string); ok {
						subject = v
					}
				}
			}
			tenant := ContextTenant(r)
			if tenant == "" {
				tenant = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			}
			logEvent := AuditEvent{
				Timestamp:  time.Now(),
				Duration:   time.Since(start),
				Method:     r.Method,
				Path:       normalizePath(r.URL.Path),
				StatusCode: recorder.StatusCode(),
				Tenant:     tenant,
				Subject:    subject,
				RequestID:  RequestIDFromRequest(r),
				RemoteAddr: r.RemoteAddr,
				UserAgent:  r.UserAgent(),
			}
			on(logEvent)
		})
	}
}
