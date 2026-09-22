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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditLoggerCapturesRequestMetadata(t *testing.T) {
	events := make(chan AuditEvent, 1)
	mw := AuditLogger(func(event AuditEvent) {
		events <- event
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/orders/42", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	req = req.WithContext(context.WithValue(req.Context(), claimsContextKey, map[string]any{
		"sub":   "user-42",
		"roles": []string{"admin"},
	}))
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}

	select {
	case event := <-events:
		if event.Tenant != "acme" {
			t.Fatalf("expected tenant acme, got %q", event.Tenant)
		}
		if event.Subject != "user-42" {
			t.Fatalf("expected subject user-42, got %q", event.Subject)
		}
		if event.Method != http.MethodGet {
			t.Fatalf("expected method GET, got %q", event.Method)
		}
		if event.Path != "/orders/42" {
			t.Fatalf("expected path /orders/42, got %q", event.Path)
		}
		if event.StatusCode != http.StatusAccepted {
			t.Fatalf("expected status 202, got %d", event.StatusCode)
		}
		if event.Duration <= 0 {
			t.Fatalf("expected a positive audit duration, got %s", event.Duration)
		}
	default:
		t.Fatal("expected audit event to be emitted")
	}
}

func TestAuditLoggerNilCallbackAndNilRequest(t *testing.T) {
	mw := AuditLogger(nil)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	res := httptest.NewRecorder()
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})).ServeHTTP(res, req)
	if res.Code != http.StatusTeapot {
		t.Fatalf("expected a nil callback to be a pass-through, got %d", res.Code)
	}

	events := make(chan AuditEvent, 1)
	mw2 := AuditLogger(func(event AuditEvent) { events <- event })
	mw2(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(httptest.NewRecorder(), nil)
}

func TestAuditLoggerFallsBackToUserIDAndHeaderTenant(t *testing.T) {
	var got AuditEvent
	mw := AuditLogger(func(event AuditEvent) { got = event })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	req = req.WithContext(context.WithValue(req.Context(), claimsContextKey, map[string]any{"user_id": "user-7"}))
	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})).ServeHTTP(httptest.NewRecorder(), req)

	if got.Subject != "user-7" {
		t.Fatalf("expected subject user-7, got %q", got.Subject)
	}
	if got.Tenant != "acme" {
		t.Fatalf("expected tenant acme from the header, got %q", got.Tenant)
	}
}
