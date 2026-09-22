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

func TestRouteRegistryListRegions(t *testing.T) {
	registry := NewRouteRegistry()
	if list := registry.ListRegions(); len(list) != 0 {
		t.Fatalf("expected empty list on empty registry, got %#v", list)
	}

	if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Enabled: true, Primary: true, Weight: 100}); err != nil {
		t.Fatalf("register region: %v", err)
	}

	list := registry.ListRegions()
	if len(list) != 1 || list[0].Name != "us-east" {
		t.Fatalf("expected one us-east region, got %#v", list)
	}
}

func TestRegionRegistryStoresPrimaryAndFailoverRegions(t *testing.T) {
	registry := NewRouteRegistry()

	if err := registry.RegisterRegion(RegionConfig{
		Name:    "us-east",
		Region:  "us-east-1",
		Target:  "https://api-us-east.internal",
		Enabled: true,
		Primary: true,
		Weight:  100,
	}); err != nil {
		t.Fatalf("register primary region: %v", err)
	}
	if err := registry.RegisterRegion(RegionConfig{
		Name:    "us-west",
		Region:  "us-west-2",
		Target:  "https://api-us-west.internal",
		Enabled: true,
		Weight:  50,
	}); err != nil {
		t.Fatalf("register failover region: %v", err)
	}

	primary, ok := registry.GetRegion("us-east")
	if !ok {
		t.Fatal("expected primary region to be present")
	}
	if !primary.Primary {
		t.Fatal("expected primary region to be marked as primary")
	}
	if primary.Target != "https://api-us-east.internal" {
		t.Fatalf("expected region target to be preserved, got %q", primary.Target)
	}
}

func TestRegionRegistrySelectsAvailableFallback(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Enabled: true, Primary: true, Weight: 100, Healthy: false}); err != nil {
		t.Fatalf("primary: %v", err)
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "us-west", Region: "us-west-2", Target: "https://api-us-west.internal", Enabled: true, Weight: 50, Healthy: true}); err != nil {
		t.Fatalf("secondary: %v", err)
	}

	selected, ok := registry.SelectRegion("us-east")
	if !ok {
		t.Fatal("expected a healthy fallback region")
	}
	if selected.Name != "us-west" {
		t.Fatalf("expected us-west fallback, got %q", selected.Name)
	}
}

func TestRegionRegistryFailoverSkipsDegradedPrimary(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterRegion(RegionConfig{Name: "eu-west", Region: "eu-west-1", Target: "https://api-eu-west.internal", Enabled: true, Primary: true, Weight: 100, Healthy: false}); err != nil {
		t.Fatalf("primary: %v", err)
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Enabled: true, Weight: 50, Healthy: true}); err != nil {
		t.Fatalf("secondary: %v", err)
	}

	decision, ok := registry.EvaluateRegion("eu-west", RegionMetrics{LatencyMS: 260, ErrorRate: 0.12})
	if !ok {
		t.Fatal("expected a region decision for the degraded primary")
	}
	if !decision.Allow {
		t.Fatal("expected the failover region to be allowed")
	}
	if decision.Name != "us-east" {
		t.Fatalf("expected us-east fallback, got %q", decision.Name)
	}
	if decision.Target != "https://api-us-east.internal" {
		t.Fatalf("expected us-east target, got %q", decision.Target)
	}
}

func TestSelectRegionNilRegistryAndUnknownRegion(t *testing.T) {
	var registry *RouteRegistry
	if _, ok := registry.SelectRegion("us-east"); ok {
		t.Fatal("expected SelectRegion on a nil registry to report not found")
	}
	if _, ok := registry.EvaluateRegion("us-east", RegionMetrics{}); ok {
		t.Fatal("expected EvaluateRegion on a nil registry to report not found")
	}

	empty := NewRouteRegistry()
	if _, ok := empty.SelectRegion("unknown"); ok {
		t.Fatal("expected SelectRegion for an unknown region to report not found")
	}
}

func TestSelectRegionFallsBackToUnhealthyPrimaryWhenNoAlternatives(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Enabled: true, Primary: true, Weight: 100, Healthy: false}); err != nil {
		t.Fatalf("register: %v", err)
	}
	selected, ok := registry.SelectRegion("us-east")
	if !ok || selected.Name != "us-east" {
		t.Fatalf("expected the degraded primary to be returned when no alternative exists, got %#v ok=%v", selected, ok)
	}
}

func TestEvaluateRegionBranches(t *testing.T) {
	newRegistry := func() *RouteRegistry {
		registry := NewRouteRegistry()
		if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Enabled: true, Primary: true, Weight: 100, Healthy: true}); err != nil {
			t.Fatalf("register primary: %v", err)
		}
		if err := registry.RegisterRegion(RegionConfig{Name: "us-west", Region: "us-west-2", Target: "https://api-us-west.internal", Enabled: true, Weight: 50, Healthy: true}); err != nil {
			t.Fatalf("register fallback: %v", err)
		}
		return registry
	}

	t.Run("unknown primary", func(t *testing.T) {
		if _, ok := newRegistry().EvaluateRegion("unknown", RegionMetrics{}); ok {
			t.Fatal("expected an unknown primary to report not found")
		}
	})
	t.Run("disabled primary", func(t *testing.T) {
		registry := NewRouteRegistry()
		if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Weight: 100, Primary: true}); err == nil {
			t.Fatal("expected a disabled region registration to be rejected")
		}
		empty := NewRouteRegistry()
		if _, ok := empty.EvaluateRegion("us-east", RegionMetrics{}); ok {
			t.Fatal("expected EvaluateRegion for an unregistered primary to report not found")
		}
	})
	t.Run("healthy primary with good metrics", func(t *testing.T) {
		decision, ok := newRegistry().EvaluateRegion("us-east", RegionMetrics{LatencyMS: 100, ErrorRate: 0.01})
		if !ok || !decision.Allow || decision.Name != "us-east" {
			t.Fatalf("expected the healthy primary to be kept, got %#v ok=%v", decision, ok)
		}
		if decision.Reason != "primary region is healthy" {
			t.Fatalf("expected healthy-primary reason, got %q", decision.Reason)
		}
	})
	t.Run("degraded primary fails over", func(t *testing.T) {
		decision, ok := newRegistry().EvaluateRegion("us-east", RegionMetrics{LatencyMS: 500, ErrorRate: 0.20})
		if !ok || !decision.Allow || decision.Name != "us-west" {
			t.Fatalf("expected failover to us-west, got %#v ok=%v", decision, ok)
		}
	})
	t.Run("degraded metrics fail over even when primary is healthy", func(t *testing.T) {
		registry := NewRouteRegistry()
		if err := registry.RegisterRegion(RegionConfig{Name: "us-east", Region: "us-east-1", Target: "https://api-us-east.internal", Enabled: true, Primary: true, Weight: 100, Healthy: false}); err != nil {
			t.Fatalf("register primary: %v", err)
		}
		if err := registry.RegisterRegion(RegionConfig{Name: "us-west", Region: "us-west-2", Target: "https://api-us-west.internal", Enabled: true, Weight: 50, Healthy: true}); err != nil {
			t.Fatalf("register fallback: %v", err)
		}

		decision, ok := registry.EvaluateRegion("us-east", RegionMetrics{LatencyMS: 500, ErrorRate: 0.20})
		if !ok || !decision.Allow || decision.Name != "us-west" {
			t.Fatalf("expected failover to us-west, got %#v ok=%v", decision, ok)
		}
	})
	t.Run("unhealthy primary without fallback is blocked", func(t *testing.T) {
		registry := NewRouteRegistry()
		if err := registry.RegisterRegion(RegionConfig{Name: "only", Region: "r1", Target: "https://only.internal", Enabled: true, Primary: true, Weight: 100, Healthy: false}); err != nil {
			t.Fatalf("register primary: %v", err)
		}

		decision, ok := registry.EvaluateRegion("only", RegionMetrics{LatencyMS: 500, ErrorRate: 0.20})
		if !ok || decision.Allow {
			t.Fatalf("expected traffic to be blocked, got %#v ok=%v", decision, ok)
		}
		if decision.Reason != "primary region is degraded and no healthy fallback exists" {
			t.Fatalf("expected blocked reason, got %q", decision.Reason)
		}
	})
}

func TestRegisterRegionBranches(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterRegion(RegionConfig{Name: "", Region: "r", Target: "t", Enabled: true}); err == nil {
		t.Fatal("expected a missing name to fail")
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "n", Region: "", Target: "t", Enabled: true}); err == nil {
		t.Fatal("expected a missing region identifier to fail")
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "n", Region: "r", Target: "", Enabled: true}); err == nil {
		t.Fatal("expected a missing target to fail")
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "n", Region: "r", Target: "t"}); err == nil {
		t.Fatal("expected a disabled region to fail")
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "n", Region: "r", Target: "t", Enabled: true, Weight: 0}); err != nil {
		t.Fatalf("expected zero weight to default to 100, got %v", err)
	}
	if region, _ := registry.GetRegion("n"); region.Weight != 100 {
		t.Fatalf("expected default weight 100, got %d", region.Weight)
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "n", Region: "r", Target: "t", Enabled: true}); err == nil {
		t.Fatal("expected a duplicate region to fail")
	}

	var nilRegistry *RouteRegistry
	if err := nilRegistry.RegisterRegion(RegionConfig{}); err == nil {
		t.Fatal("expected RegisterRegion on a nil registry to fail")
	}
}
