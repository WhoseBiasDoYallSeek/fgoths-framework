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
	"strings"
	"sync"
	"time"
)

var tenantContextKey = struct{}{}

// ContextTenant returns the tenant identifier attached to the current request.
func ContextTenant(r *http.Request) string {
	if r == nil {
		return ""
	}
	if tenant, ok := r.Context().Value(tenantContextKey).(string); ok {
		return tenant
	}
	return ""
}

// RequireTenant ensures that a request includes a tenant identifier in the given header.
func RequireTenant(headerName string) func(http.Handler) http.Handler {
	if headerName == "" {
		headerName = "X-Tenant-ID"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := strings.TrimSpace(r.Header.Get(headerName))
			if tenant == "" {
				http.Error(w, "tenant required", http.StatusBadRequest)
				return
			}
			ctx := context.WithValue(r.Context(), tenantContextKey, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireTenantMatch ensures a request is scoped to a single tenant and rejects
// cross-tenant access for route-level multi-tenancy isolation.
func RequireTenantMatch(expected string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := strings.TrimSpace(ContextTenant(r))
			if tenant == "" {
				tenant = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			}
			if tenant == "" {
				http.Error(w, "tenant required", http.StatusBadRequest)
				return
			}
			if expected != "" && tenant != expected {
				http.Error(w, "tenant access denied", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

type tenantQuotaStore struct {
	mu       sync.Mutex
	byUser   map[string][]time.Time
	requests uint64
}

// TenantQuotaMiddleware enforces a per-tenant request limit within a rolling time window.
func TenantQuotaMiddleware(limit int, window time.Duration) func(http.Handler) http.Handler {
	if limit <= 0 || window <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	store := &tenantQuotaStore{byUser: make(map[string][]time.Time)}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenant := strings.TrimSpace(ContextTenant(r))
			if tenant == "" {
				tenant = strings.TrimSpace(r.Header.Get("X-Tenant-ID"))
			}
			if tenant == "" {
				http.Error(w, "tenant required", http.StatusBadRequest)
				return
			}

			now := time.Now()
			cutoff := now.Add(-window)
			store.mu.Lock()
			store.requests++
			if store.requests%256 == 0 {
				for key, timestamps := range store.byUser {
					if len(timestamps) == 0 || !timestamps[len(timestamps)-1].After(cutoff) {
						delete(store.byUser, key)
					}
				}
			}
			entries := store.byUser[tenant]
			filtered := entries[:0]
			for _, ts := range entries {
				if ts.After(cutoff) {
					filtered = append(filtered, ts)
				}
			}
			if len(filtered) >= limit {
				store.byUser[tenant] = filtered
				store.mu.Unlock()
				http.Error(w, "tenant quota exceeded", http.StatusTooManyRequests)
				return
			}
			filtered = append(filtered, now)
			store.byUser[tenant] = filtered
			store.mu.Unlock()

			ctx := context.WithValue(r.Context(), tenantContextKey, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
