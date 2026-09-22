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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func basePolicy(name, env string) RoutePolicy {
	return RoutePolicy{
		Name:           name,
		Environment:    env,
		Service:        "orders",
		AllowedTenants: []string{"acme"},
		AllowedRegions: []string{"us-east"},
		SLO:            NewSLOPolicy(0.05, 250, 0.02),
	}
}

func TestPolicyVersioningCreateAndHistory(t *testing.T) {
	registry := NewRouteRegistry()
	store := NewFilePolicyVersionStore(t.TempDir() + "/policies.json")
	pv := NewPolicyVersioner(registry, store)

	// Create v1.
	if err := pv.Create(basePolicy("checkout", "prod"), "alice", "initial policy"); err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// Update to v2 with a stricter SLO.
	updated := basePolicy("checkout", "prod")
	updated.SLO = NewSLOPolicy(0.01, 150, 0.01)
	if err := pv.Update(updated, "bob", "tighten SLO"); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Update to v3.
	updated2 := basePolicy("checkout", "prod")
	updated2.SLO = NewSLOPolicy(0.01, 150, 0.01)
	updated2.AllowedTenants = []string{"acme", "globex"}
	if err := pv.Update(updated2, "carol", "add tenant globex"); err != nil {
		t.Fatalf("second update failed: %v", err)
	}

	history, err := pv.History("checkout", "prod")
	if err != nil {
		t.Fatalf("history failed: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 versions, got %d", len(history))
	}

	// Versions are sequential and each records actor/reason.
	if history[0].Version != 1 || history[1].Version != 2 || history[2].Version != 3 {
		t.Fatalf("expected versions 1,2,3, got %d,%d,%d", history[0].Version, history[1].Version, history[2].Version)
	}
	if history[0].Actor != "alice" || history[1].Actor != "bob" || history[2].Actor != "carol" {
		t.Fatalf("expected actors alice,bob,carol, got %s,%s,%s", history[0].Actor, history[1].Actor, history[2].Actor)
	}
	if history[1].Reason != "tighten SLO" {
		t.Fatalf("expected reason 'tighten SLO', got %q", history[1].Reason)
	}

	// The registry holds the latest version.
	current, ok := registry.GetPolicy("checkout")
	if !ok || current.SLO == nil || current.SLO.LatencyThresholdMS != 150 {
		t.Fatalf("expected registry to hold latest policy, got %+v", current)
	}
}

func TestPolicyVersioningDiff(t *testing.T) {
	registry := NewRouteRegistry()
	pv := NewPolicyVersioner(registry, NewFilePolicyVersionStore(t.TempDir()+"/policies.json"))

	if err := pv.Create(basePolicy("checkout", "prod"), "alice", "initial"); err != nil {
		t.Fatal(err)
	}

	updated := basePolicy("checkout", "prod")
	updated.SLO = NewSLOPolicy(0.01, 150, 0.02)
	updated.AllowedTenants = []string{"acme", "globex"}
	if err := pv.Update(updated, "bob", "tighten SLO"); err != nil {
		t.Fatal(err)
	}

	history, _ := pv.History("checkout", "prod")
	diff := history[1].Diff
	if diff == "" {
		t.Fatal("expected non-empty diff on update")
	}
	if !strings.Contains(diff, "slo") && !strings.Contains(diff, "allowed_tenants") {
		t.Fatalf("expected diff to mention changed fields, got %q", diff)
	}

	// First version has no diff.
	if history[0].Diff != "" {
		t.Fatalf("expected empty diff on first version, got %q", history[0].Diff)
	}
}

func TestPolicyVersioningUpdateUnknownPolicyFails(t *testing.T) {
	registry := NewRouteRegistry()
	pv := NewPolicyVersioner(registry, NewFilePolicyVersionStore(t.TempDir()+"/policies.json"))

	err := pv.Update(basePolicy("ghost", "prod"), "alice", "should fail")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected update of unknown policy to fail, got %v", err)
	}
}

func TestPolicyVersioningRollback(t *testing.T) {
	registry := NewRouteRegistry()
	store := NewFilePolicyVersionStore(t.TempDir() + "/policies.json")
	pv := NewPolicyVersioner(registry, store)

	if err := pv.Create(basePolicy("checkout", "prod"), "alice", "initial"); err != nil {
		t.Fatal(err)
	}
	updated := basePolicy("checkout", "prod")
	updated.SLO = NewSLOPolicy(0.01, 150, 0.02)
	if err := pv.Update(updated, "bob", "tighten SLO"); err != nil {
		t.Fatal(err)
	}

	// Rollback to version 1.
	if err := pv.Rollback("checkout", "prod", 1, "dave", "SLO too strict"); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	current, ok := registry.GetPolicy("checkout")
	if !ok || current.SLO == nil || current.SLO.LatencyThresholdMS != 250 {
		t.Fatalf("expected rollback to restore v1 SLO, got %+v", current)
	}

	// Rollback itself is recorded as a new version (v3) with a diff.
	history, _ := pv.History("checkout", "prod")
	if len(history) != 3 {
		t.Fatalf("expected 3 versions after rollback, got %d", len(history))
	}
	last := history[2]
	if last.Version != 3 || last.Actor != "dave" || !strings.Contains(strings.ToLower(last.Reason), "rollback") {
		t.Fatalf("expected rollback recorded as v3 by dave, got v%d by %s (%s)", last.Version, last.Actor, last.Reason)
	}
}

func TestPolicyVersioningPersistence(t *testing.T) {
	storePath := t.TempDir() + "/policies.json"
	registry := NewRouteRegistry()
	pv := NewPolicyVersioner(registry, NewFilePolicyVersionStore(storePath))

	if err := pv.Create(basePolicy("checkout", "prod"), "alice", "initial"); err != nil {
		t.Fatal(err)
	}
	updated := basePolicy("checkout", "prod")
	updated.SLO = NewSLOPolicy(0.01, 150, 0.02)
	if err := pv.Update(updated, "bob", "tighten SLO"); err != nil {
		t.Fatal(err)
	}

	// A fresh versioner on the same store sees the full history.
	pv2 := NewPolicyVersioner(NewRouteRegistry(), NewFilePolicyVersionStore(storePath))
	history, err := pv2.History("checkout", "prod")
	if err != nil {
		t.Fatalf("history after restart failed: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 persisted versions, got %d", len(history))
	}
	if history[1].Version != 2 {
		t.Fatalf("expected latest persisted version 2, got %d", history[1].Version)
	}

	// Regression: the latest policy must be restored into the fresh registry,
	// so updates/rollbacks work after a restart without re-creating the policy.
	if _, ok := pv2.registry.GetPolicy("checkout"); !ok {
		t.Fatal("expected latest policy to be restored into the registry after restart")
	}
	restored, _ := pv2.registry.GetPolicy("checkout")
	if restored.SLO == nil || restored.SLO.LatencyThresholdMS != 150 {
		t.Fatalf("expected restored policy to hold v2 SLO, got %+v", restored)
	}
	// And an update on the restored registry must succeed.
	updated2 := basePolicy("checkout", "prod")
	updated2.SLO = NewSLOPolicy(0.02, 200, 0.02)
	if err := pv2.Update(updated2, "carol", "post-restart update"); err != nil {
		t.Fatalf("update after restart failed: %v", err)
	}
}

func TestControlPlanePolicyEndpoints(t *testing.T) {
	cp := NewControlPlaneServer(ControlPlaneConfig{
		StorePath:  t.TempDir() + "/ledger.json",
		AdminToken: "test-token",
	})
	ts := httptest.NewServer(cp.Handler())
	defer ts.Close()

	t.Run("create policy", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "POST", "/api/v1/policies", "test-token", map[string]any{
			"name": "checkout", "environment": "prod", "service": "orders",
			"allowed_tenants": []string{"acme"}, "allowed_regions": []string{"us-east"},
			"actor": "alice", "reason": "initial policy",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %v", resp.StatusCode, body)
		}
	})

	t.Run("update policy creates version", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "PUT", "/api/v1/policies/checkout", "test-token", map[string]any{
			"environment": "prod", "service": "orders",
			"allowed_tenants": []string{"acme", "globex"}, "allowed_regions": []string{"us-east"},
			"actor": "bob", "reason": "add tenant",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", resp.StatusCode, body)
		}
		if v, ok := body["version"].(float64); !ok || v != 2 {
			t.Fatalf("expected version 2, got %v", body)
		}
	})

	t.Run("policy history", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "GET", "/api/v1/policies/checkout/history", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		items, ok := body["items"].([]any)
		if !ok || len(items) != 2 {
			t.Fatalf("expected 2 history entries, got %v", body)
		}
	})

	t.Run("rollback via api", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "POST", "/api/v1/policies/checkout/rollback", "test-token", map[string]any{
			"version": 1, "actor": "dave", "reason": "revert tenant change",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", resp.StatusCode, body)
		}
	})

	t.Run("update unknown policy 404", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "PUT", "/api/v1/policies/ghost", "test-token", map[string]any{
			"environment": "prod", "service": "orders", "actor": "alice", "reason": "x",
		})
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})

	t.Run("persistence across restart", func(t *testing.T) {
		cp2 := NewControlPlaneServer(ControlPlaneConfig{
			StorePath:  cp.StorePath(),
			AdminToken: "test-token",
		})
		history, err := cp2.PolicyVersioner().History("checkout", "prod")
		if err != nil {
			t.Fatalf("history after restart: %v", err)
		}
		if len(history) != 3 {
			t.Fatalf("expected 3 persisted versions after restart, got %d", len(history))
		}
	})
	_ = json.Marshal
}
