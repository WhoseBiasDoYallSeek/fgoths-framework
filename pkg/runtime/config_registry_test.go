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

import "testing"

func TestRouteRegistryStoresRoutesAndReturnsDefaults(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.Register(RouteConfig{
		Name:    "orders-api",
		Method:  "GET",
		Path:    "/orders",
		Target:  "http://orders.internal/v1",
		Weight:  100,
		Enabled: true,
	}); err != nil {
		t.Fatalf("register route: %v", err)
	}

	route, ok := registry.Get("orders-api")
	if !ok {
		t.Fatal("expected route to be present")
	}
	if route.Target != "http://orders.internal/v1" {
		t.Fatalf("expected target v1, got %q", route.Target)
	}
	if route.Weight != 100 {
		t.Fatalf("expected weight 100, got %d", route.Weight)
	}
}

func TestRouteRegistryRejectsDuplicateOrDisabledRoutes(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.Register(RouteConfig{Name: "checkout", Method: "POST", Path: "/checkout", Target: "http://checkout.internal", Weight: 100, Enabled: true}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := registry.Register(RouteConfig{Name: "checkout", Method: "POST", Path: "/checkout", Target: "http://checkout.internal/v2", Weight: 100, Enabled: true}); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	if err := registry.Register(RouteConfig{Name: "legacy", Method: "GET", Path: "/legacy", Target: "http://legacy.internal", Weight: 0, Enabled: false}); err == nil {
		t.Fatal("expected disabled route with zero weight to fail")
	}
}

func TestRouteRegistryListReturnsAllRoutes(t *testing.T) {
	registry := NewRouteRegistry()
	if list := registry.List(); len(list) != 0 {
		t.Fatalf("expected empty list on empty registry, got %#v", list)
	}

	if err := registry.Register(RouteConfig{Name: "orders-api", Method: "GET", Path: "/orders", Target: "http://orders.internal", Weight: 100, Enabled: true}); err != nil {
		t.Fatalf("register orders-api: %v", err)
	}
	if err := registry.Register(RouteConfig{Name: "checkout", Method: "POST", Path: "/checkout", Target: "http://checkout.internal", Weight: 100, Enabled: true}); err != nil {
		t.Fatalf("register checkout: %v", err)
	}

	list := registry.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 routes, got %d", len(list))
	}
}

func TestRouteRegistryListPoliciesReturnsAllPolicies(t *testing.T) {
	registry := NewRouteRegistry()
	if list := registry.ListPolicies(); len(list) != 0 {
		t.Fatalf("expected empty list on empty registry, got %#v", list)
	}

	if err := registry.RegisterPolicy(RoutePolicy{Name: "checkout-policy", Environment: "staging", Service: "checkout"}); err != nil {
		t.Fatalf("register policy: %v", err)
	}

	list := registry.ListPolicies()
	if len(list) != 1 || list[0].Name != "checkout-policy" {
		t.Fatalf("expected one checkout-policy, got %#v", list)
	}
}

func TestRouteRegistryListNilSafety(t *testing.T) {
	var registry *RouteRegistry
	if got := registry.List(); got != nil {
		t.Fatalf("expected nil list on nil registry, got %#v", got)
	}
	if got := registry.ListPolicies(); got != nil {
		t.Fatalf("expected nil policy list on nil registry, got %#v", got)
	}
}

func TestRouteRegistryRegisterBranches(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.Register(RouteConfig{Name: "", Method: "GET", Path: "/x", Target: "t", Enabled: true}); err == nil {
		t.Fatal("expected a missing name to fail")
	}
	if err := registry.Register(RouteConfig{Name: "n", Method: "GET", Path: "/", Target: "t", Enabled: true}); err != nil {
		t.Fatalf("expected root path with a method to be accepted, got %v", err)
	}
	if err := registry.Register(RouteConfig{Name: "n", Method: "GET", Path: "/x", Target: "", Enabled: true}); err == nil {
		t.Fatal("expected a missing target to fail")
	}
	if err := registry.Register(RouteConfig{Name: "n", Method: "GET", Path: "/x", Target: "t"}); err == nil {
		t.Fatal("expected a disabled route to fail")
	}
	if err := registry.Register(RouteConfig{Name: "neg", Method: "GET", Path: "/neg", Target: "t", Enabled: true, Weight: -5}); err != nil {
		t.Fatalf("expected a negative weight to be clamped, got %v", err)
	}
	if route, _ := registry.Get("neg"); route.Weight != 100 {
		t.Fatalf("expected the negative weight to be clamped and defaulted to 100 for an enabled route, got %d", route.Weight)
	}
	if err := registry.Register(RouteConfig{Name: "anym", Path: "/y", Target: "t", Enabled: true}); err != nil {
		t.Fatalf("expected a missing method to default to ANY, got %v", err)
	}
	if route, _ := registry.Get("anym"); route.Method != httpMethodAny {
		t.Fatalf("expected the default method ANY, got %q", route.Method)
	}

	var nilRegistry *RouteRegistry
	if err := nilRegistry.Register(RouteConfig{}); err == nil {
		t.Fatal("expected Register on a nil registry to fail")
	}
}

func TestRouteRegistryRegisterPolicyBranches(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterPolicy(RoutePolicy{Name: "", Service: "svc"}); err == nil {
		t.Fatal("expected a missing policy name to fail")
	}
	if err := registry.RegisterPolicy(RoutePolicy{Name: "p", Service: ""}); err == nil {
		t.Fatal("expected a missing policy service to fail")
	}
	if err := registry.RegisterPolicy(RoutePolicy{Name: "p", Service: "svc"}); err != nil {
		t.Fatalf("expected a minimal policy to register, got %v", err)
	}
	if policy, _ := registry.GetPolicy("p"); policy.Environment != "prod" {
		t.Fatalf("expected the default environment prod, got %q", policy.Environment)
	}
	if policy, _ := registry.GetPolicy("p"); policy.SLO == nil {
		t.Fatal("expected a default SLO policy to be applied")
	}
	if err := registry.RegisterPolicy(RoutePolicy{Name: "p", Service: "svc"}); err == nil {
		t.Fatal("expected a duplicate policy to fail")
	}
	if _, ok := registry.GetPolicy("missing"); ok {
		t.Fatal("expected an unknown policy to be absent")
	}

	var nilRegistry *RouteRegistry
	if err := nilRegistry.RegisterPolicy(RoutePolicy{}); err == nil {
		t.Fatal("expected RegisterPolicy on a nil registry to fail")
	}
}

func TestValidatePolicyBranches(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterPolicy(RoutePolicy{
		Name:           "orders-prod",
		Service:        "orders-api",
		AllowedTenants: []string{"acme"},
		AllowedRegions: []string{"us-east"},
	}); err != nil {
		t.Fatalf("register policy: %v", err)
	}

	var nilRegistry *RouteRegistry
	if err := nilRegistry.ValidatePolicy("orders-prod", "acme", "us-east"); err == nil {
		t.Fatal("expected ValidatePolicy on a nil registry to fail")
	}
	if err := registry.ValidatePolicy("missing", "acme", "us-east"); err == nil {
		t.Fatal("expected an unknown policy to fail")
	}
	if err := registry.ValidatePolicy("orders-prod", "", "us-east"); err == nil {
		t.Fatal("expected a missing tenant to fail")
	}
	if err := registry.ValidatePolicy("orders-prod", "acme", ""); err == nil {
		t.Fatal("expected a missing region to fail")
	}
	if err := registry.ValidatePolicy("orders-prod", "beta", "us-east"); err == nil {
		t.Fatal("expected a disallowed tenant to fail")
	}
	if err := registry.ValidatePolicy("orders-prod", "acme", "eu-west"); err == nil {
		t.Fatal("expected a disallowed region to fail")
	}
	if err := registry.ValidatePolicy("orders-prod", "acme", "us-east"); err != nil {
		t.Fatalf("expected a valid tenant/region combination to pass, got %v", err)
	}

	// RegisterPolicy always installs a default SLO, so a strict policy can only
	// be built by setting the fields directly on a manually created registry.
	strict := &RouteRegistry{routes: map[string]RouteConfig{}, rollouts: map[string]RolloutConfig{}, regions: map[string]RegionConfig{}, tenants: map[string]TenantConfig{}, policies: map[string]RoutePolicy{}}
	strict.policies["strict"] = RoutePolicy{Name: "strict", Service: "svc", RequireSignedArtifacts: true}
	if err := strict.ValidatePolicy("strict", "acme", "us-east"); err == nil {
		t.Fatal("expected RequireSignedArtifacts without SLO to fail")
	}
	strict.policies["strict2"] = RoutePolicy{Name: "strict2", Service: "svc", RequireAudit: true}
	if err := strict.ValidatePolicy("strict2", "acme", "us-east"); err == nil {
		t.Fatal("expected RequireAudit without SLO to fail")
	}
}

func TestRouteRegistryEnforcesEnvironmentPolicyByTenantAndRegion(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.Register(RouteConfig{Name: "orders-api", Method: "GET", Path: "/orders", Target: "http://orders.internal", Weight: 100, Enabled: true}); err != nil {
		t.Fatalf("register route: %v", err)
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "tenant-a", AllowedRoutes: []string{"orders-api"}, AllowedRegions: []string{"us-east"}, Enabled: true}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	policy := RoutePolicy{
		Name:                   "orders-prod",
		Environment:            "prod",
		Service:                "orders-api",
		AllowedTenants:         []string{"tenant-a"},
		AllowedRegions:         []string{"us-east"},
		RequireSignedArtifacts: true,
		RequireAudit:           true,
		SLO:                    NewSLOPolicy(0.05, 250, 0.02),
	}
	if err := registry.RegisterPolicy(policy); err != nil {
		t.Fatalf("register policy: %v", err)
	}

	if err := registry.ValidatePolicy("orders-prod", "tenant-a", "us-east"); err != nil {
		t.Fatalf("expected tenant+region policy to pass: %v", err)
	}
	if err := registry.ValidatePolicy("orders-prod", "tenant-b", "us-east"); err == nil {
		t.Fatal("expected disallowed tenant to fail policy validation")
	}
	if err := registry.ValidatePolicy("orders-prod", "tenant-a", "eu-west"); err == nil {
		t.Fatal("expected disallowed region to fail policy validation")
	}
}
