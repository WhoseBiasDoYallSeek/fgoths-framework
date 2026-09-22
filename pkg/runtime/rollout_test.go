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

func TestRolloutRegistryLifecycle(t *testing.T) {
	registry := NewRolloutRegistry()

	cfg := RolloutConfig{
		Name:         "orders-canary",
		Route:        "orders-api",
		Strategy:     RolloutStrategyCanary,
		Target:       "http://orders.internal/v2",
		CanaryWeight: 25,
		MaxWeight:    100,
		Step:         25,
		Enabled:      true,
		Progressive:  true,
	}
	if err := registry.RegisterRollout(cfg); err != nil {
		t.Fatalf("register rollout: %v", err)
	}
	if err := registry.RegisterRollout(cfg); err == nil {
		t.Fatal("expected duplicate rollout registration to fail")
	}

	got, ok := registry.GetRollout("orders-canary")
	if !ok || got.Target != "http://orders.internal/v2" {
		t.Fatalf("expected to retrieve registered rollout, got %#v ok=%v", got, ok)
	}

	if _, ok := registry.GetRollout("missing"); ok {
		t.Fatal("expected missing rollout to be absent")
	}
}

func TestRolloutRegistryNilSafety(t *testing.T) {
	var registry *RolloutRegistry
	if err := registry.RegisterRollout(RolloutConfig{}); err == nil {
		t.Fatal("expected error registering on a nil registry")
	}
	if _, ok := registry.GetRollout("anything"); ok {
		t.Fatal("expected GetRollout on nil registry to report not found")
	}
}

func TestRouteRegistryListRollouts(t *testing.T) {
	registry := NewRouteRegistry()
	if list := registry.ListRollouts(); len(list) != 0 {
		t.Fatalf("expected empty list on empty registry, got %#v", list)
	}
	if err := registry.RegisterRollout(RolloutConfig{
		Name:         "orders-canary",
		Route:        "orders-api",
		Strategy:     RolloutStrategyCanary,
		Target:       "http://orders.internal/v2",
		CanaryWeight: 25,
		MaxWeight:    100,
		Step:         25,
		Enabled:      true,
	}); err != nil {
		t.Fatalf("register rollout: %v", err)
	}
	if list := registry.ListRollouts(); len(list) != 1 || list[0].Name != "orders-canary" {
		t.Fatalf("expected one orders-canary rollout, got %#v", list)
	}

	var nilRegistry *RouteRegistry
	if got := nilRegistry.ListRollouts(); got != nil {
		t.Fatalf("expected nil list on nil registry, got %#v", got)
	}
}

func TestRolloutConfigValidateBranches(t *testing.T) {
	t.Run("missing name", func(t *testing.T) {
		cfg := RolloutConfig{Route: "r", Target: "http://x", Enabled: true}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected missing name to fail validation")
		}
	})
	t.Run("missing route", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Target: "http://x", Enabled: true}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected missing route to fail validation")
		}
	})
	t.Run("missing target", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Enabled: true}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected missing target to fail validation")
		}
	})
	t.Run("rollback rejected", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, Rollback: true}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected rollback rollouts to be rejected")
		}
	})
	t.Run("disabled rejected", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x"}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected a disabled rollout to be rejected")
		}
	})
	t.Run("negative canary weight", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, CanaryWeight: -1}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected a negative canary weight to be rejected")
		}
	})
	t.Run("canary weight above max", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, CanaryWeight: 150, MaxWeight: 100}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected canary weight above max to be rejected")
		}
	})
	t.Run("progressive with zero weight", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, Progressive: true}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected a progressive rollout with zero canary weight to be rejected")
		}
	})
	t.Run("negative error threshold", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, CanaryWeight: 10, ErrorThreshold: -0.5}
		if err := cfg.Validate(); err == nil {
			t.Fatal("expected a negative error threshold to be rejected")
		}
	})
	t.Run("error threshold percent normalization", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, CanaryWeight: 10, ErrorThreshold: 50}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected percent threshold to be accepted, got %v", err)
		}
	})

	t.Run("defaults applied", func(t *testing.T) {
		cfg := RolloutConfig{Name: "n", Route: "r", Target: "http://x", Enabled: true, CanaryWeight: 10}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected valid minimal config, got %v", err)
		}
	})
}

func TestRolloutDecideBranches(t *testing.T) {
	base := RolloutConfig{
		Name:         "orders-canary",
		Route:        "orders-api",
		Strategy:     RolloutStrategyCanary,
		Target:       "http://orders.internal/v2",
		CanaryWeight: 50,
		MaxWeight:    100,
		Step:         10,
		Enabled:      true,
	}

	t.Run("inactive when weight is zero", func(t *testing.T) {
		cfg := base
		cfg.CanaryWeight = 0
		decision, err := cfg.Decide(10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Allow {
			t.Fatal("expected a zero-weight rollout to be inactive")
		}
		if decision.Reason != "rollout is inactive" {
			t.Fatalf("expected inactive reason, got %q", decision.Reason)
		}
	})

	t.Run("negative current clamps to zero", func(t *testing.T) {
		decision, err := base.Decide(-5)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !decision.Allow {
			t.Fatal("expected decision to allow with clamped current")
		}
	})

	t.Run("progressive caps weight at current", func(t *testing.T) {
		cfg := base
		cfg.Progressive = true
		decision, err := cfg.Decide(20)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Weight != 20 {
			t.Fatalf("expected progressive weight capped at current=20, got %d", decision.Weight)
		}
	})

	t.Run("invalid config fails", func(t *testing.T) {
		cfg := base
		cfg.CanaryWeight = -1
		if _, err := cfg.Decide(10); err == nil {
			t.Fatal("expected Decide to propagate a validation error")
		}
	})
}

func TestRolloutEvaluateBranches(t *testing.T) {
	base := RolloutConfig{
		Name:           "orders-canary",
		Route:          "orders-api",
		Strategy:       RolloutStrategyCanary,
		Target:         "http://orders.internal/v2",
		CanaryWeight:   50,
		MaxWeight:      100,
		Step:           10,
		Enabled:        true,
		ErrorThreshold: 0.05,
	}

	t.Run("error rate above threshold blocks canary", func(t *testing.T) {
		decision, err := base.Evaluate(RolloutMetrics{Requests: 100, ErrorRate: 0.10})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if decision.Allow {
			t.Fatal("expected the canary to be blocked when the error rate exceeds the threshold")
		}
		if decision.Reason == "" {
			t.Fatal("expected a blocking reason to be reported")
		}
	})

	t.Run("error rate within threshold allows canary", func(t *testing.T) {
		decision, err := base.Evaluate(RolloutMetrics{Requests: 100, ErrorRate: 0.01})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !decision.Allow {
			t.Fatalf("expected the canary to continue within the threshold, got reason %q", decision.Reason)
		}
	})

	t.Run("zero error threshold disables the guardrail", func(t *testing.T) {
		cfg := base
		cfg.ErrorThreshold = 0
		decision, err := cfg.Evaluate(RolloutMetrics{Requests: 100, ErrorRate: 1.0})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !decision.Allow {
			t.Fatal("expected a zero error threshold to disable the guardrail")
		}
	})

	t.Run("invalid config fails", func(t *testing.T) {
		cfg := base
		cfg.Rollback = true
		if _, err := cfg.Evaluate(RolloutMetrics{}); err == nil {
			t.Fatal("expected Evaluate to propagate a validation error")
		}
	})
}

func TestRouteRegistryStoresRolloutConfig(t *testing.T) {
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
	if err := registry.RegisterRollout(RolloutConfig{
		Name:         "orders-canary",
		Route:        "orders-api",
		Strategy:     RolloutStrategyCanary,
		Target:       "http://orders.internal/v2",
		CanaryWeight: 25,
		MaxWeight:    100,
		Step:         25,
		Enabled:      true,
		Progressive:  true,
	}); err != nil {
		t.Fatalf("register rollout: %v", err)
	}
	cfg, ok := registry.GetRollout("orders-canary")
	if !ok {
		t.Fatal("expected rollout to be present")
	}
	if cfg.Target != "http://orders.internal/v2" {
		t.Fatalf("expected canary target v2, got %q", cfg.Target)
	}
	if cfg.CanaryWeight != 25 {
		t.Fatalf("expected canary weight 25, got %d", cfg.CanaryWeight)
	}
}

func TestProgressiveDeliveryRejectsRollbackAndApprovesCanary(t *testing.T) {
	cfg := RolloutConfig{
		Name:         "orders-canary",
		Route:        "orders-api",
		Strategy:     RolloutStrategyCanary,
		Target:       "http://orders.internal/v2",
		CanaryWeight: 25,
		MaxWeight:    100,
		Step:         25,
		Enabled:      true,
		Progressive:  true,
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid canary config, got %v", err)
	}

	cfg.Rollback = true
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected rollback config to be rejected")
	}
}

func TestRolloutDecisionAppliesProgressiveSteps(t *testing.T) {
	cfg := RolloutConfig{
		Name:         "catalog-canary",
		Route:        "catalog-api",
		Strategy:     RolloutStrategyProgressive,
		Target:       "http://catalog.internal/v2",
		CanaryWeight: 10,
		MaxWeight:    100,
		Step:         10,
		Enabled:      true,
		Progressive:  true,
	}

	decision, err := cfg.Decide(30)
	if err != nil {
		t.Fatalf("decision error: %v", err)
	}
	if !decision.Allow {
		t.Fatal("expected progressive step to be allowed")
	}
	if decision.Weight != 10 {
		t.Fatalf("expected incremental canary weight 10, got %d", decision.Weight)
	}
	if decision.Target != "http://catalog.internal/v2" {
		t.Fatalf("expected target to be the canary target, got %q", decision.Target)
	}
}

func TestRolloutEvaluationHaltsCanaryOnErrorThreshold(t *testing.T) {
	cfg := RolloutConfig{
		Name:           "payments-canary",
		Route:          "payments-api",
		Strategy:       RolloutStrategyProgressive,
		Target:         "http://payments.internal/v2",
		CanaryWeight:   25,
		MaxWeight:      100,
		Step:           25,
		Enabled:        true,
		Progressive:    true,
		ErrorThreshold: 0.05,
	}

	decision, err := cfg.Evaluate(RolloutMetrics{Requests: 400, ErrorRate: 0.08})
	if err != nil {
		t.Fatalf("evaluate rollout: %v", err)
	}
	if decision.Allow {
		t.Fatal("expected canary to pause when error rate exceeds threshold")
	}
	if decision.Weight != 0 {
		t.Fatalf("expected emergency rollback weight 0, got %d", decision.Weight)
	}
	if decision.Target != "http://payments.internal/v2" {
		t.Fatalf("expected target to remain canary target, got %q", decision.Target)
	}
}
