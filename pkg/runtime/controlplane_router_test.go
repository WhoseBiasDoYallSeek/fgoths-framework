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
)

func TestMatchRoute(t *testing.T) {
	pattern := []string{"releases", "{service}", "{environment}", "{version}", "approve"}

	t.Run("exact match captures params", func(t *testing.T) {
		params, ok := matchRoute(pattern, []string{"releases", "orders", "staging", "v1.0.0", "approve"})
		if !ok {
			t.Fatal("expected match")
		}
		if len(params) != 3 || params[0] != "orders" || params[1] != "staging" || params[2] != "v1.0.0" {
			t.Fatalf("unexpected params: %v", params)
		}
	})

	t.Run("literal mismatch", func(t *testing.T) {
		if _, ok := matchRoute(pattern, []string{"deployments", "orders", "staging", "v1.0.0", "approve"}); ok {
			t.Fatal("expected no match for wrong literal segment")
		}
	})

	t.Run("length mismatch", func(t *testing.T) {
		if _, ok := matchRoute(pattern, []string{"releases", "orders", "staging"}); ok {
			t.Fatal("expected no match for different length")
		}
	})

	t.Run("empty pattern matches empty path", func(t *testing.T) {
		if _, ok := matchRoute([]string{}, []string{}); !ok {
			t.Fatal("expected empty pattern to match empty path")
		}
	})
}

func TestRouteAPIDispatch(t *testing.T) {
	cp := NewControlPlaneServer(ControlPlaneConfig{
		StorePath:  t.TempDir() + "/ledger.json",
		AdminToken: "test-token",
	})
	ts := httptest.NewServer(cp.Handler())
	defer ts.Close()

	// Seed one upstream so list has content.
	resp, _ := cpRequest(t, ts, "POST", "/api/v1/upstreams", "test-token", map[string]any{
		"name": "orders", "target": "http://orders.internal", "enabled": true, "healthy": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("setup failed: %d", resp.StatusCode)
	}

	t.Run("routing table covers all documented endpoints", func(t *testing.T) {
		endpoints := []struct {
			method, path string
			want         int
		}{
			{"GET", "/api/v1/upstreams", http.StatusOK},
			{"GET", "/api/v1/routes", http.StatusOK},
			{"GET", "/api/v1/deployments", http.StatusOK},
			{"GET", "/api/v1/releases", http.StatusOK},
			{"GET", "/api/v1/policies", http.StatusOK},
			{"GET", "/api/v1/routes/orders/target", http.StatusNotFound}, // route not registered
			{"GET", "/api/v1/deployments/orders/staging", http.StatusNotFound},
			{"GET", "/api/v1/releases/orders/staging/v1.0.0", http.StatusNotFound},
			{"GET", "/api/v1/policies/checkout/history", http.StatusNotFound},
			{"GET", "/api/v1/ghost", http.StatusNotFound},
			{"DELETE", "/api/v1/upstreams", http.StatusMethodNotAllowed},
			{"PUT", "/api/v1/upstreams", http.StatusMethodNotAllowed},
		}
		for _, ep := range endpoints {
			resp, _ := cpRequest(t, ts, ep.method, ep.path, "test-token", nil)
			if resp.StatusCode != ep.want {
				t.Errorf("%s %s: expected %d, got %d", ep.method, ep.path, ep.want, resp.StatusCode)
			}
		}
	})

	t.Run("param capture works end to end", func(t *testing.T) {
		// Register a route referencing the upstream, then resolve it via the
		// parameterized endpoint.
		resp, _ := cpRequest(t, ts, "POST", "/api/v1/routes", "test-token", map[string]any{
			"name": "get-order", "method": "GET", "path": "/orders/:id", "upstream": "orders", "enabled": true,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("route registration failed: %d", resp.StatusCode)
		}
		resp, body := cpRequest(t, ts, "GET", "/api/v1/routes/get-order/target", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if body["target"] != "http://orders.internal" {
			t.Fatalf("unexpected target: %v", body)
		}
	})
}
