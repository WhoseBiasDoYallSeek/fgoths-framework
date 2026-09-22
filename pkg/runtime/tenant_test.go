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
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequireTenantAddsTenantFromHeader(t *testing.T) {
	mw := RequireTenant("X-Tenant-ID")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/secure", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := ContextTenant(r); got != "acme" {
			t.Fatalf("expected tenant acme, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestRequireTenantRejectsMissingTenant(t *testing.T) {
	mw := RequireTenant("X-Tenant-ID")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/secure", nil)
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.Code)
	}
}

func TestTenantQuotaMiddlewareEnforcesLimitPerTenant(t *testing.T) {
	mw := TenantQuotaMiddleware(1, time.Second)

	for i, tenant := range []string{"acme", "acme"} {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/secure", nil)
		req.Header.Set("X-Tenant-ID", tenant)
		res := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(res, req)
		if i == 0 && res.Code != http.StatusOK {
			t.Fatalf("first request expected 200, got %d", res.Code)
		}
		if i == 1 && res.Code != http.StatusTooManyRequests {
			t.Fatalf("second request expected 429, got %d", res.Code)
		}
	}
}

func TestRequireTenantMatchRejectsCrossTenantAccess(t *testing.T) {
	mw := RequireTenantMatch("acme")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/secure", nil)
	req.Header.Set("X-Tenant-ID", "beta")
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, req)

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestRequireTenantDefaultsHeaderName(t *testing.T) {
	mw := RequireTenant("")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/secure", nil)
	req.Header.Set("X-Tenant-ID", "acme")
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected the default X-Tenant-ID header to be honored, got %d", res.Code)
	}
}

func TestContextTenantNilRequest(t *testing.T) {
	if got := ContextTenant(nil); got != "" {
		t.Fatalf("expected empty tenant for a nil request, got %q", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if got := ContextTenant(req); got != "" {
		t.Fatalf("expected empty tenant without middleware, got %q", got)
	}
}

func TestRequireTenantMatchBranches(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("missing tenant", func(t *testing.T) {
		res := httptest.NewRecorder()
		RequireTenantMatch("acme")(next).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for a missing tenant, got %d", res.Code)
		}
	})
	t.Run("empty expected accepts any tenant", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("X-Tenant-ID", "any-tenant")
		res := httptest.NewRecorder()
		RequireTenantMatch("")(next).ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for an unrestricted policy, got %d", res.Code)
		}
	})
}

func TestTenantQuotaMiddlewareDisabledOrPassthrough(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	t.Run("non-positive limit disables the quota", func(t *testing.T) {
		res := httptest.NewRecorder()
		TenantQuotaMiddleware(0, time.Second)(next).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
		if res.Code != http.StatusOK {
			t.Fatalf("expected a disabled quota to pass through, got %d", res.Code)
		}
	})
	t.Run("non-positive window disables the quota", func(t *testing.T) {
		res := httptest.NewRecorder()
		TenantQuotaMiddleware(5, 0)(next).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
		if res.Code != http.StatusOK {
			t.Fatalf("expected a disabled quota to pass through, got %d", res.Code)
		}
	})
	t.Run("request without tenant is rejected", func(t *testing.T) {
		res := httptest.NewRecorder()
		TenantQuotaMiddleware(1, time.Second)(next).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
		if res.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for a request without tenant identity, got %d", res.Code)
		}
	})
}
