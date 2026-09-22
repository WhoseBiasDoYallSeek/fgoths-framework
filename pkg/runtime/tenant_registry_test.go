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

func TestRouteRegistryGetTenantAndCanAccessRegion(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterTenant(TenantConfig{
		Name:           "acme",
		AllowedRoutes:  []string{"orders-api"},
		AllowedRegions: []string{"us-east"},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("register acme tenant: %v", err)
	}

	tenant, ok := registry.GetTenant("acme")
	if !ok || tenant.Name != "acme" {
		t.Fatalf("expected to retrieve acme tenant, got %#v ok=%v", tenant, ok)
	}
	if _, ok := registry.GetTenant("missing"); ok {
		t.Fatal("expected missing tenant to be absent")
	}

	if !registry.CanAccessRegion("acme", "us-east") {
		t.Fatal("expected acme to access its allowed region")
	}
	if registry.CanAccessRegion("acme", "us-west") {
		t.Fatal("expected acme to be denied an unlisted region")
	}
	if registry.CanAccessRegion("missing-tenant", "us-east") {
		t.Fatal("expected an unknown tenant to be denied region access")
	}
}

func TestRouteRegistryTenantNilSafety(t *testing.T) {
	var registry *RouteRegistry
	if _, ok := registry.GetTenant("acme"); ok {
		t.Fatal("expected GetTenant on nil registry to report not found")
	}
	if registry.CanAccessRegion("acme", "us-east") {
		t.Fatal("expected CanAccessRegion on nil registry to deny access")
	}
}

func TestTenantRegistryEnforcesRouteIsolation(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.RegisterTenant(TenantConfig{
		Name:           "acme",
		AllowedRoutes:  []string{"orders-api"},
		AllowedRegions: []string{"us-east"},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("register acme tenant: %v", err)
	}
	if err := registry.RegisterTenant(TenantConfig{
		Name:           "beta",
		AllowedRoutes:  []string{"billing-api"},
		AllowedRegions: []string{"us-west"},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("register beta tenant: %v", err)
	}

	if err := registry.Register(RouteConfig{
		Name:    "orders-api",
		Method:  "GET",
		Path:    "/orders",
		Target:  "http://orders.internal/v1",
		Weight:  100,
		Tenant:  "acme",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register orders route: %v", err)
	}
	if err := registry.Register(RouteConfig{
		Name:    "billing-api",
		Method:  "GET",
		Path:    "/billing",
		Target:  "http://billing.internal/v1",
		Weight:  100,
		Tenant:  "beta",
		Enabled: true,
	}); err != nil {
		t.Fatalf("register billing route: %v", err)
	}

	if !registry.CanAccessRoute("acme", "orders-api") {
		t.Fatal("expected acme to access its own route")
	}
	if registry.CanAccessRoute("beta", "orders-api") {
		t.Fatal("expected beta to be denied across tenant route")
	}
	if !registry.CanAccessRoute("beta", "billing-api") {
		t.Fatal("expected beta to access its own billing route")
	}
}

func TestTenantRegistryListsConfiguredTenants(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.RegisterTenant(TenantConfig{Name: "acme", AllowedRoutes: []string{"orders-api"}, AllowedRegions: []string{"us-east"}, Enabled: true}); err != nil {
		t.Fatalf("register acme tenant: %v", err)
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "beta", AllowedRoutes: []string{"billing-api"}, AllowedRegions: []string{"us-west"}, Enabled: true}); err != nil {
		t.Fatalf("register beta tenant: %v", err)
	}

	tenants := registry.ListTenants()
	if len(tenants) != 2 {
		t.Fatalf("expected 2 tenants, got %d", len(tenants))
	}
	if tenants[0].Name == "" || tenants[1].Name == "" {
		t.Fatal("expected tenant entries to include names")
	}
}
