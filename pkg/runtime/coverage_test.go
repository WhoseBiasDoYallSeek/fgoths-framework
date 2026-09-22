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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusRecorderImplicitWriteAndNilSafety(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := newStatusRecorder(rec)
	_, _ = sr.Write([]byte("hello"))
	if got := sr.StatusCode(); got != http.StatusOK {
		t.Fatalf("expected an implicit 200 after Write, got %d", got)
	}

	var nilRecorder *statusRecorder
	if got := nilRecorder.StatusCode(); got != http.StatusOK {
		t.Fatalf("expected nil recorder StatusCode to default to 200, got %d", got)
	}
	if n, err := nilRecorder.Write([]byte("x")); n != 0 || err != nil {
		t.Fatalf("expected nil recorder Write to be a no-op, got (%d, %v)", n, err)
	}
	nilRecorder.WriteHeader(http.StatusInternalServerError) // must not panic

	if got := newStatusRecorder(nil).StatusCode(); got != http.StatusOK {
		t.Fatalf("expected a recorder wrapping nil to default to 200, got %d", got)
	}
}

func TestStatusRecorderFirstWriteHeaderWins(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := newStatusRecorder(rec)
	sr.WriteHeader(http.StatusAccepted)
	sr.WriteHeader(http.StatusTeapot)
	if got := sr.StatusCode(); got != http.StatusAccepted {
		t.Fatalf("expected the first WriteHeader code to win, got %d", got)
	}
}

func TestMetricsSnapshotMarshalJSONNilMaps(t *testing.T) {
	payload, err := json.Marshal(MetricsSnapshot{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(payload), `"by_method":{}`) {
		t.Fatalf("expected nil maps to serialize as empty objects, got %s", payload)
	}
}

func TestMetricsNilSafetyAndDefaults(t *testing.T) {
	var metrics *Metrics
	metrics.Record(http.MethodGet, "/x", http.StatusOK, time.Millisecond) // must not panic
	snapshot := metrics.Snapshot()
	if snapshot.TotalRequests != 0 || snapshot.ByMethod == nil {
		t.Fatalf("expected an empty snapshot with initialized maps, got %#v", snapshot)
	}

	metrics2 := NewMetrics()
	metrics2.Record("", "", 0)
	metrics2.Record(http.MethodGet, "/orders", http.StatusInternalServerError, -time.Second)
	s := metrics2.Snapshot()
	if s.TotalRequests != 2 {
		t.Fatalf("expected 2 recorded requests, got %d", s.TotalRequests)
	}
	if s.ByMethod["GET"] != 2 {
		t.Fatalf("expected an empty method to default to GET, got %#v", s.ByMethod)
	}
	if s.ByRoute["/"] != 1 {
		t.Fatalf("expected an empty route to normalize to /, got %#v", s.ByRoute)
	}
	if s.TotalErrors != 1 {
		t.Fatalf("expected 1 error for the 500 status, got %d", s.TotalErrors)
	}
	if s.TotalLatency < 0 {
		t.Fatalf("expected negative durations to be clamped to 0, got %v", s.TotalLatency)
	}
	if s.AvgLatency < 0 {
		t.Fatalf("expected a non-negative average latency, got %v", s.AvgLatency)
	}
}

func TestTenantRegistryRegisterBranches(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterTenant(TenantConfig{Name: "", Enabled: true}); err == nil {
		t.Fatal("expected a missing tenant name to fail")
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "disabled"}); err == nil {
		t.Fatal("expected a disabled tenant to fail")
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "acme", Enabled: true}); err != nil {
		t.Fatalf("expected a valid tenant to register, got %v", err)
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "acme", Enabled: true}); err == nil {
		t.Fatal("expected a duplicate tenant to fail")
	}
	if list := registry.ListTenants(); len(list) != 1 {
		t.Fatalf("expected 1 tenant, got %d", len(list))
	}

	var nilRegistry *RouteRegistry
	if err := nilRegistry.RegisterTenant(TenantConfig{}); err == nil {
		t.Fatal("expected RegisterTenant on a nil registry to fail")
	}
	if got := nilRegistry.ListTenants(); got != nil {
		t.Fatalf("expected ListTenants on a nil registry to return nil, got %#v", got)
	}
}

func TestRouteRegistryRegisterRolloutBranches(t *testing.T) {
	registry := NewRouteRegistry()
	if err := registry.RegisterRollout(RolloutConfig{Name: "", Route: "r", Target: "t", Enabled: true}); err == nil {
		t.Fatal("expected an invalid rollout to fail")
	}
	if err := registry.RegisterRollout(RolloutConfig{Name: "n", Route: "r", Target: "t", Enabled: true, CanaryWeight: 10}); err != nil {
		t.Fatalf("expected a valid rollout to register, got %v", err)
	}
	if err := registry.RegisterRollout(RolloutConfig{Name: "n", Route: "r", Target: "t", Enabled: true, CanaryWeight: 10}); err == nil {
		t.Fatal("expected a duplicate rollout to fail")
	}

	var nilRegistry *RouteRegistry
	if err := nilRegistry.RegisterRollout(RolloutConfig{}); err == nil {
		t.Fatal("expected RegisterRollout on a nil registry to fail")
	}
}

func TestNewReleasePlanDefaultsEnvironment(t *testing.T) {
	plan := NewReleasePlan("", "orders-api", "1.0.0")
	if plan.Environment != "prod" {
		t.Fatalf("expected default environment prod, got %q", plan.Environment)
	}
}

func TestReleasePlanValidateBranches(t *testing.T) {
	base := NewReleasePlan("prod", "orders-api", "1.0.0")
	base.Gate = NewApprovalGate("release", 1)
	_ = base.Gate.Approve("alice")
	artifact := NewArtifactProvenance("orders-api", "1.0.0")
	artifact.SetContent("payload")
	artifact.Sign("secret", "platform")
	base.Artifact = artifact
	base.SLO = NewSLOPolicy(0.05, 250, 0.02)
	base.Rollback = NewRollbackPlan("rollback", "1.0.0", []string{"drain"})
	base.Runbook = "docs/runbook.md"

	if err := base.Validate("secret"); err != nil {
		t.Fatalf("expected a fully populated plan to validate, got %v", err)
	}
	if !base.IsReady("secret") {
		t.Fatal("expected the plan to be ready")
	}

	var nilPlan *ReleasePlan
	if err := nilPlan.Validate("secret"); err == nil {
		t.Fatal("expected Validate on a nil plan to fail")
	}

	missingRunbook := NewReleasePlan("prod", "orders-api", "1.0.0")
	missingRunbook.Gate = base.Gate
	missingRunbook.Artifact = artifact
	missingRunbook.SLO = base.SLO
	missingRunbook.Rollback = base.Rollback
	if err := missingRunbook.Validate("secret"); err == nil {
		t.Fatal("expected a missing runbook to fail validation")
	}
}

func TestFileDeploymentStoreSaveCreateDirectoriesAndCorruptFile(t *testing.T) {
	corruptPath := filepath.Join(t.TempDir(), "nested", "corrupt.json")
	if err := os.MkdirAll(filepath.Dir(corruptPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(corruptPath, []byte("not-json"), 0o644); err != nil {
		t.Fatalf("seed corrupt state: %v", err)
	}
	corrupt := NewFileDeploymentStore(corruptPath)
	if _, err := corrupt.Load(); err == nil {
		t.Fatal("expected loading a corrupt state file to fail")
	}

	// Saving into a nested directory that already exists must succeed.
	ok := NewFileDeploymentStore(filepath.Join(t.TempDir(), "state.json"))
	if err := ok.Save(DeploymentLedgerState{Current: map[string]map[string]*DeploymentManifest{}}); err != nil {
		t.Fatalf("expected Save to succeed, got %v", err)
	}
}

func TestProxyNilReverseProxyServesNotFound(t *testing.T) {
	p := &Proxy{}
	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	p.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a proxy without a reverse proxy, got %d", res.Code)
	}
}
