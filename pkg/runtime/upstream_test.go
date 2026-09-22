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
	"strings"
	"testing"
)

func TestUpstreamRegistryRegisterAndResolve(t *testing.T) {
	registry := NewRouteRegistry()

	err := registry.RegisterUpstream(UpstreamConfig{
		Name:     "orders",
		Target:   "http://orders.internal",
		Weight:   100,
		Enabled:  true,
		Healthy:  true,
		Services: []string{"orders-api"},
	})
	if err != nil {
		t.Fatalf("expected upstream registration to succeed, got %v", err)
	}

	up, ok := registry.GetUpstream("orders")
	if !ok {
		t.Fatal("expected upstream orders to exist")
	}
	if up.Target != "http://orders.internal" {
		t.Fatalf("expected target http://orders.internal, got %q", up.Target)
	}

	// Route references an upstream by name; the target is derived at resolve time.
	if err := registry.Register(RouteConfig{
		Name:     "get-order",
		Method:   "GET",
		Path:     "/orders/:id",
		Upstream: "orders",
		Weight:   100,
		Enabled:  true,
	}); err != nil {
		t.Fatalf("expected route with upstream reference to register, got %v", err)
	}

	resolved, ok := registry.ResolveRouteTarget("get-order")
	if !ok {
		t.Fatal("expected route target to resolve via upstream registry")
	}
	if resolved != "http://orders.internal" {
		t.Fatalf("expected resolved target http://orders.internal, got %q", resolved)
	}
}

func TestUpstreamRegistryValidation(t *testing.T) {
	registry := NewRouteRegistry()

	cases := []struct {
		name string
		cfg  UpstreamConfig
		want string
	}{
		{"missing name", UpstreamConfig{Target: "http://x.internal"}, "upstream name is required"},
		{"missing target", UpstreamConfig{Name: "a"}, "upstream target is required"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := registry.RegisterUpstream(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}

	t.Run("disabled upstream is stored for draining", func(t *testing.T) {
		draining := UpstreamConfig{Name: "draining", Target: "http://drain.internal", Enabled: false, Healthy: true}
		if err := registry.RegisterUpstream(draining); err != nil {
			t.Fatalf("disabled upstream must be registrable (drain semantics), got %v", err)
		}
		if _, ok := registry.SelectUpstream("draining"); ok {
			t.Fatal("disabled upstream must never be selected")
		}
	})

	t.Run("duplicate name", func(t *testing.T) {
		if err := registry.RegisterUpstream(UpstreamConfig{Name: "dup", Target: "http://dup.internal", Enabled: true, Healthy: true}); err != nil {
			t.Fatalf("expected first registration to succeed, got %v", err)
		}
		if err := registry.RegisterUpstream(UpstreamConfig{Name: "dup", Target: "http://dup2.internal", Enabled: true, Healthy: true}); err == nil {
			t.Fatal("expected duplicate upstream registration to fail")
		}
	})
}

func TestUpstreamRegistryResolveMissing(t *testing.T) {
	registry := NewRouteRegistry()

	t.Run("unknown upstream", func(t *testing.T) {
		if err := registry.Register(RouteConfig{Name: "r", Method: "GET", Path: "/r", Upstream: "ghost", Weight: 100, Enabled: true}); err == nil {
			t.Fatal("expected registering a route with unknown upstream to fail")
		}
	})

	t.Run("unknown route", func(t *testing.T) {
		if _, ok := registry.ResolveRouteTarget("nope"); ok {
			t.Fatal("expected unknown route not to resolve")
		}
	})
}

func TestUpstreamRegistryLegacyTargetStillWorks(t *testing.T) {
	registry := NewRouteRegistry()

	// Backward compatibility: routes may still declare an inline Target.
	if err := registry.Register(RouteConfig{
		Name:    "legacy",
		Method:  "GET",
		Path:    "/legacy",
		Target:  "http://legacy.internal",
		Weight:  100,
		Enabled: true,
	}); err != nil {
		t.Fatalf("expected legacy route registration to succeed, got %v", err)
	}

	resolved, ok := registry.ResolveRouteTarget("legacy")
	if !ok || resolved != "http://legacy.internal" {
		t.Fatalf("expected legacy inline target to resolve, got %q (ok=%v)", resolved, ok)
	}
}

func TestUpstreamRegistryFailoverToHealthyFallback(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.RegisterUpstream(UpstreamConfig{Name: "primary", Target: "http://primary.internal", Enabled: true, Healthy: false, Fallback: "secondary"}); err != nil {
		t.Fatalf("expected primary registration to succeed, got %v", err)
	}
	if err := registry.RegisterUpstream(UpstreamConfig{Name: "secondary", Target: "http://secondary.internal", Enabled: true, Healthy: true}); err != nil {
		t.Fatalf("expected secondary registration to succeed, got %v", err)
	}

	got, ok := registry.SelectUpstream("primary")
	if !ok {
		t.Fatal("expected failover selection to succeed")
	}
	if got.Target != "http://secondary.internal" {
		t.Fatalf("expected failover to secondary, got %q", got.Target)
	}
}

func TestUpstreamRegistryUnhealthyWithoutFallback(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.RegisterUpstream(UpstreamConfig{Name: "solo", Target: "http://solo.internal", Enabled: true, Healthy: false}); err != nil {
		t.Fatalf("expected solo registration to succeed, got %v", err)
	}
	if _, ok := registry.SelectUpstream("solo"); ok {
		t.Fatal("expected unhealthy upstream without fallback not to be selected")
	}
}

func TestUpstreamRegistryWeightedSelection(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.RegisterUpstream(UpstreamConfig{Name: "a", Target: "http://a.internal", Enabled: true, Healthy: true, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterUpstream(UpstreamConfig{Name: "b", Target: "http://b.internal", Enabled: true, Healthy: true, Weight: 3}); err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{}
	for i := 0; i < 4000; i++ {
		up, ok := registry.PickUpstream("svc")
		if !ok {
			t.Fatal("expected weighted pick to succeed")
		}
		counts[up.Name]++
	}
	if counts["a"] == 0 || counts["b"] == 0 {
		t.Fatalf("expected both upstreams to receive traffic, got %v", counts)
	}
	if counts["b"] <= counts["a"] {
		t.Fatalf("expected b (weight 3) to receive more traffic than a (weight 1), got %v", counts)
	}
}

// TestServesServiceEdgeCases covers the M3 gap: servesService with len(Services)==0
// must return true (no filter means serve all), and explicit filter must match
// services in the list (and trim whitespace).
func TestServesServiceEdgeCases(t *testing.T) {
	// Empty services = no filter, should accept any service name.
	up := UpstreamConfig{Name: "svc", Services: []string{}}
	if !servesService(up, "svc") {
		t.Error("empty Services should accept any service")
	}
	if !servesService(up, "anything") {
		t.Error("empty Services should accept any service name")
	}

	// Explicit filter: returns true only when the service is in the list.
	filtered := UpstreamConfig{Name: "svc", Services: []string{"orders", "billing"}}
	if !servesService(filtered, "billing") {
		t.Error("servesService should return true for billing (in list)")
	}
	if servesService(filtered, "unknown") {
		t.Error("servesService should return false for unknown (not in list)")
	}

	// Whitespace is trimmed before comparison.
	filtered2 := UpstreamConfig{Name: "svc", Services: []string{" orders "}}
	if !servesService(filtered2, "orders") {
		t.Error("servesService should trim whitespace before comparing")
	}
}

// TestWeightedPoolRouting verifies that ResolveRouteTarget picks among a
// service's healthy upstreams by Weight (the pool path), and that a drained
// (disabled) member is excluded from the pool.
func TestWeightedPoolRouting(t *testing.T) {
	r := NewRouteRegistry()
	mustRegister := func(cfg UpstreamConfig) {
		t.Helper()
		if err := r.RegisterUpstream(cfg); err != nil {
			t.Fatal(err)
		}
	}
	mustRegister(UpstreamConfig{Name: "a", Target: "http://a.internal", Enabled: true, Healthy: true, Weight: 3, Services: []string{"payments"}})
	mustRegister(UpstreamConfig{Name: "b", Target: "http://b.internal", Enabled: true, Healthy: true, Weight: 1, Services: []string{"payments"}})
	mustRegister(UpstreamConfig{Name: "drained", Target: "http://drained.internal", Enabled: false, Healthy: true, Weight: 100, Services: []string{"payments"}})
	if err := r.Register(RouteConfig{Name: "pay", Path: "/payments", Upstream: "a", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	// 200 picks: only healthy+enabled members (a, b) may appear; the drained
	// member (weight 100, would dominate if included) must never be chosen.
	counts := map[string]int{}
	for i := 0; i < 200; i++ {
		target, ok := r.ResolveRouteTarget("pay")
		if !ok {
			t.Fatal("expected route to resolve")
		}
		counts[targetHost(target)]++
	}
	if counts["http://drained.internal"] > 0 {
		t.Error("drained upstream must not receive traffic")
	}
	if counts["http://a.internal"] == 0 || counts["http://b.internal"] == 0 {
		t.Errorf("both healthy members must receive traffic: %v", counts)
	}
}

func targetHost(target string) string { return target }

// TestRouteHeadersExposed verifies that declarative route headers are
// retrievable (and copied) via RouteHeaders — previously declared but inert.
func TestRouteHeadersExposed(t *testing.T) {
	r := NewRouteRegistry()
	if err := r.Register(RouteConfig{
		Name:    "h",
		Path:    "/h",
		Target:  "http://h.internal",
		Enabled: true,
		Header:  map[string]string{"X-Tenant": "acme"},
	}); err != nil {
		t.Fatal(err)
	}
	headers := r.RouteHeaders("h")
	if headers["X-Tenant"] != "acme" {
		t.Fatalf("headers %v, want X-Tenant=acme", headers)
	}
	// Mutating the returned map must not affect the registry.
	headers["X-Tenant"] = "mutated"
	if again := r.RouteHeaders("h"); again["X-Tenant"] != "acme" {
		t.Error("RouteHeaders must return a copy")
	}
	// Unknown route: nil.
	if r.RouteHeaders("nope") != nil {
		t.Error("unknown route must return nil headers")
	}
}

func TestNewRouteProxyAppliesConfiguredHeaders(t *testing.T) {
	var gotHeader string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("X-Tenant")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	registry := NewRouteRegistry()
	if err := registry.Register(RouteConfig{
		Name: "orders", Path: "/orders", Target: upstream.URL, Enabled: true,
		Header: map[string]string{"X-Tenant": "acme"},
	}); err != nil {
		t.Fatal(err)
	}
	proxy, err := registry.NewProxy("orders")
	if err != nil {
		t.Fatalf("NewProxy() error: %v", err)
	}
	response := httptest.NewRecorder()
	proxy.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/orders", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("proxy response = %d, want %d", response.Code, http.StatusNoContent)
	}
	if gotHeader != "acme" {
		t.Fatalf("upstream X-Tenant = %q, want acme", gotHeader)
	}
}
