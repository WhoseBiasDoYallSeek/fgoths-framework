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
)

// TestControlPlaneReleaseWorkflowFullCycle drives the release state machine
// end-to-end: create (with a scoped multi-approver gate), duplicate-create
// conflict, approve to threshold, approve-after-terminal conflict, rollback,
// and rollback-after-terminal conflict.
func TestControlPlaneReleaseWorkflowFullCycle(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	// Scoped gate: this service/env requires two approvals.
	cp.SetWorkflowGate("payments", "prod", 2)

	// Create.
	resp, body := cpRequest(t, ts, "POST", "/api/v1/releases", "test-token", map[string]any{
		"service": "payments", "environment": "prod", "version": "v3.0.0",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating release, got %d: %v", resp.StatusCode, body)
	}

	// Duplicate create must conflict.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases", "test-token", map[string]any{
		"service": "payments", "environment": "prod", "version": "v3.0.0",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate release, got %d", resp.StatusCode)
	}

	// List must include it.
	resp, body = cpRequest(t, ts, "GET", "/api/v1/releases", "test-token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing releases, got %d", resp.StatusCode)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("expected 1 release in list, got %v", body)
	}

	// First approval: below the 2-approval threshold, state stays pending.
	resp, body = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v3.0.0/approve", "test-token", map[string]any{
		"actor": "alice",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on first approval, got %d: %v", resp.StatusCode, body)
	}
	if body["state"] != "pending" || body["approved"] != false {
		t.Fatalf("expected pending/unapproved after first approval, got %v", body)
	}

	// Second approval reaches the threshold.
	resp, body = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v3.0.0/approve", "test-token", map[string]any{
		"actor": "bob",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on second approval, got %d: %v", resp.StatusCode, body)
	}
	if body["state"] != "approved" || body["approved"] != true {
		t.Fatalf("expected approved after threshold, got %v", body)
	}

	// Rollback from approved state is allowed.
	resp, body = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v3.0.0/rollback", "test-token", map[string]any{
		"actor": "carol", "reason": "regression detected",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on rollback, got %d: %v", resp.StatusCode, body)
	}
	if body["state"] != "rolled_back" {
		t.Fatalf("expected rolled_back state, got %v", body)
	}

	// Approve after terminal state must be unprocessable (422: terminal state
	// error is not an "already exists" conflict).
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v3.0.0/approve", "test-token", map[string]any{
		"actor": "dave",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 approving terminal release, got %d", resp.StatusCode)
	}

	// Rollback after terminal state must be unprocessable.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v3.0.0/rollback", "test-token", map[string]any{
		"actor": "carol", "reason": "again",
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 rolling back terminal release, got %d", resp.StatusCode)
	}

	// Approve/rollback on unknown workflow must 404.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v9.9.9/approve", "test-token", map[string]any{"actor": "x"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 approving unknown release, got %d", resp.StatusCode)
	}
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/payments/prod/v9.9.9/rollback", "test-token", map[string]any{"actor": "x", "reason": "y"})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 rolling back unknown release, got %d", resp.StatusCode)
	}
}

// TestControlPlaneCreateRouteValidation covers createRoute's error branches:
// invalid JSON and registry validation failures.
func TestControlPlaneCreateRouteValidation(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	// Invalid JSON body.
	resp, _ := rawJSONRequest(t, ts, "POST", "/api/v1/routes", "test-token", "{not json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}

	// Missing required fields (registry validation) → 422 unprocessable.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/routes", "test-token", map[string]any{
		"name": "bad-route", "method": "GET", // no path, no target
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid route config, got %d", resp.StatusCode)
	}

	// Valid route.
	resp, body := cpRequest(t, ts, "POST", "/api/v1/routes", "test-token", map[string]any{
		"name": "orders", "method": "GET", "path": "/orders", "target": "http://orders.internal", "weight": 100, "enabled": true,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating route, got %d: %v", resp.StatusCode, body)
	}

	// Duplicate route must conflict.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/routes", "test-token", map[string]any{
		"name": "orders", "method": "GET", "path": "/orders", "target": "http://orders.internal", "weight": 100, "enabled": true,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate route, got %d", resp.StatusCode)
	}
}

// TestControlPlaneCreatePolicyValidation covers createPolicy's error branches.
func TestControlPlaneCreatePolicyValidation(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	// Invalid JSON.
	resp, _ := rawJSONRequest(t, ts, "POST", "/api/v1/policies", "test-token", "{bad")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", resp.StatusCode)
	}

	// Valid policy.
	resp, body := cpRequest(t, ts, "POST", "/api/v1/policies", "test-token", map[string]any{
		"name": "orders-prod", "environment": "prod", "service": "orders",
		"actor": "alice", "reason": "initial",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating policy, got %d: %v", resp.StatusCode, body)
	}

	// Duplicate policy name must conflict.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/policies", "test-token", map[string]any{
		"name": "orders-prod", "environment": "prod", "service": "orders",
		"actor": "bob", "reason": "duplicate",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 on duplicate policy, got %d", resp.StatusCode)
	}
}

// TestControlPlaneDeploymentHistory covers the deployment history endpoint
// (hit and miss) and the deployment get endpoint.
func TestControlPlaneDeploymentHistory(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	// Miss before any deployment.
	resp, _ := cpRequest(t, ts, "GET", "/api/v1/deployments/orders/prod/history", "test-token", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for empty history, got %d", resp.StatusCode)
	}
	resp, _ = cpRequest(t, ts, "GET", "/api/v1/deployments/orders/prod", "test-token", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing deployment, got %d", resp.StatusCode)
	}

	// Record a deployment directly on the ledger (staging env with an audit
	// trail: staging still requires a non-empty audit trail).
	manifest := NewDeploymentManifest("staging", "orders", "v1.0.0")
	manifest.Audit = []string{"staged by test"}
	if err := cp.ledger.Record(manifest); err != nil {
		t.Fatalf("ledger.Record: %v", err)
	}

	resp, body := cpRequest(t, ts, "GET", "/api/v1/deployments/orders/staging", "test-token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting deployment, got %d: %v", resp.StatusCode, body)
	}
	resp, body = cpRequest(t, ts, "GET", "/api/v1/deployments/orders/staging/history", "test-token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting history, got %d: %v", resp.StatusCode, body)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("expected 1 history item, got %v", body)
	}
}

// TestControlPlanePolicyRollback covers rollbackPolicy including the
// environment-discovery fallback when History(name, "") fails.
func TestControlPlanePolicyRollback(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	// Create a policy (v1).
	resp, _ := cpRequest(t, ts, "POST", "/api/v1/policies", "test-token", map[string]any{
		"name": "orders-prod", "environment": "staging", "service": "orders",
		"actor": "alice", "reason": "v1",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating policy, got %d", resp.StatusCode)
	}

	// Rollback to the current version (no-op rollback, still valid).
	resp, body := cpRequest(t, ts, "POST", "/api/v1/policies/orders-prod/rollback", "test-token", map[string]any{
		"version": 1, "actor": "bob", "reason": "revert",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 rolling back policy, got %d: %v", resp.StatusCode, body)
	}
	if body["status"] != "rolled_back" {
		t.Fatalf("expected rolled_back status, got %v", body)
	}

	// Unknown policy must 404.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/policies/missing/rollback", "test-token", map[string]any{
		"version": 1, "actor": "bob", "reason": "x",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 rolling back unknown policy, got %d", resp.StatusCode)
	}
}

// TestControlPlaneSetWorkflowGateGuards covers SetWorkflowGate's guard branches.
func TestControlPlaneSetWorkflowGateGuards(t *testing.T) {
	cp := NewControlPlaneServer(ControlPlaneConfig{AdminToken: "t", StorePath: filepath.Join(t.TempDir(), "l.json")})
	defer cp.Close()

	var nilCP *ControlPlaneServer
	nilCP.SetWorkflowGate("svc", "prod", 2) // must not panic

	cp.SetWorkflowGate("", "prod", 2)    // empty service: ignored
	cp.SetWorkflowGate("svc", "", 2)     // empty environment: ignored
	cp.SetWorkflowGate("svc", "prod", 0) // required <= 0 clamps to 1

	// The clamped gate must be usable: create a release and approve once.
	_, ts := newTestControlPlane(t)
	defer ts.Close()
}

// rawJSONRequest sends a request with a raw (possibly invalid) body and
// returns the response with its decoded JSON (when decodable).
func rawJSONRequest(t *testing.T, ts *httptest.Server, method, path, token, rawBody string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(rawBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp, out
}

// TestControlPlaneAccessorsNilGuards covers the nil-receiver branches of the
// accessor methods.
func TestControlPlaneAccessorsNilGuards(t *testing.T) {
	var nilCP *ControlPlaneServer
	if got := nilCP.PolicyVersioner(); got != nil {
		t.Error("nil PolicyVersioner() must return nil")
	}
	if got := nilCP.Registry(); got != nil {
		t.Error("nil Registry() must return nil")
	}
	if got := nilCP.Ledger(); got != nil {
		t.Error("nil Ledger() must return nil")
	}
	if got := nilCP.StorePath(); got != "" {
		t.Error("nil StorePath() must return empty")
	}
	if err := nilCP.Reload(); err == nil {
		t.Error("nil Reload() must error")
	}
}

// TestFilePolicyVersionStoreSaveLoad covers Save/Load including the
// missing-file and empty-path branches.
func TestFilePolicyVersionStoreSaveLoad(t *testing.T) {
	dir := t.TempDir()
	store := NewFilePolicyVersionStore(filepath.Join(dir, "p.json"))

	// Load on a missing file yields an empty state.
	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load on missing file: %v", err)
	}
	if state.Versions == nil {
		t.Error("expected initialized Versions map on empty state")
	}

	// Save then Load round-trip.
	state.Versions["orders/prod"] = []PolicyVersionRecord{{Version: 1}}
	if err := store.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(loaded.Versions["orders/prod"]) != 1 {
		t.Errorf("expected 1 version after round-trip, got %v", loaded.Versions)
	}

	// Empty path errors on both operations.
	empty := NewFilePolicyVersionStore("")
	if _, err := empty.Load(); err == nil {
		t.Error("expected Load error on empty path")
	}
	if err := empty.Save(state); err == nil {
		t.Error("expected Save error on empty path")
	}
	var nilStore *FilePolicyVersionStore
	if _, err := nilStore.Load(); err == nil {
		t.Error("expected Load error on nil store")
	}
	if err := nilStore.Save(state); err == nil {
		t.Error("expected Save error on nil store")
	}
}

// TestFileDeploymentStoreSaveLoadErrors covers the FileDeploymentStore error
// branches (empty path, nil receiver).
func TestFileDeploymentStoreSaveLoadErrors(t *testing.T) {
	empty := NewFileDeploymentStore("")
	if err := empty.Save(DeploymentLedgerState{}); err == nil {
		t.Error("expected Save error on empty path")
	}
	if _, err := empty.Load(); err == nil {
		t.Error("expected Load error on empty path")
	}
	var nilStore *FileDeploymentStore
	if err := nilStore.Save(DeploymentLedgerState{}); err == nil {
		t.Error("expected Save error on nil store")
	}
	if _, err := nilStore.Load(); err == nil {
		t.Error("expected Load error on nil store")
	}
}

// TestPolicyVersionerReplaceAndPersist covers replacePolicy's not-found
// branch, persistLocked with a store, and Close idempotence.
func TestPolicyVersionerReplaceAndPersist(t *testing.T) {
	dir := t.TempDir()
	store := NewFilePolicyVersionStore(filepath.Join(dir, "p.json"))
	registry := NewRouteRegistry()
	pv := NewPolicyVersioner(registry, store)

	// replacePolicy on a missing policy must fail.
	err := pv.replacePolicy(RoutePolicy{Name: "missing", Service: "svc"})
	if err == nil {
		t.Fatal("expected replacePolicy error for missing policy")
	}

	// Register a policy through the versioner, then replace it.
	if err := pv.Create(RoutePolicy{Name: "orders", Service: "orders", Environment: "staging"}, "alice", "v1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := pv.replacePolicy(RoutePolicy{Name: "orders", Service: "orders", Environment: "staging"}); err != nil {
		t.Fatalf("replacePolicy existing: %v", err)
	}

	// persistLocked with a store writes to disk.
	pv.mu.Lock()
	perr := pv.persistLocked()
	pv.mu.Unlock()
	if perr != nil {
		t.Fatalf("persistLocked: %v", perr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "p.json")); statErr != nil {
		t.Errorf("expected persisted file after persistLocked: %v", statErr)
	}

	// Close is idempotent.
	if err := pv.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := pv.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	var nilPV *PolicyVersioner
	if err := nilPV.Close(); err != nil {
		t.Errorf("nil Close: %v", err)
	}
}

// TestFileDeploymentStoreSaveBadDir covers the MkdirAll/WriteFile error
// branches of FileDeploymentStore.Save (unwritable path).
func TestFileDeploymentStoreSaveBadDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root; permission-based failure not reproducible")
	}
	// A path under a regular file cannot become a directory.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewFileDeploymentStore(filepath.Join(blocker, "sub", "ledger.json"))
	if err := store.Save(DeploymentLedgerState{}); err == nil {
		t.Error("expected Save error when parent path is a file")
	}
}

// TestDeploymentLedgerSnapshotAndPersist covers Snapshot's nil branch and the
// Persist error branches (nil ledger, nil store).
func TestDeploymentLedgerSnapshotAndPersist(t *testing.T) {
	var nilLedger *DeploymentLedger
	if got := nilLedger.Snapshot(); got.Current != nil {
		t.Error("nil Snapshot() must return empty state")
	}

	ledger := NewDeploymentLedger()
	manifest := NewDeploymentManifest("staging", "orders", "v1.0.0")
	manifest.Audit = []string{"staged"}
	if err := ledger.Record(manifest); err != nil {
		t.Fatalf("Record: %v", err)
	}
	snap := ledger.Snapshot()
	if len(snap.Current["orders"]["staging"].Audit) != 1 {
		t.Errorf("expected snapshot to carry the manifest, got %v", snap)
	}

	if err := nilLedger.Persist(NewFileDeploymentStore(filepath.Join(t.TempDir(), "l.json"))); err == nil {
		t.Error("nil ledger Persist must error")
	}
	if err := ledger.Persist(nil); err == nil {
		t.Error("Persist with nil store must error")
	}
	if err := ledger.Persist(NewFileDeploymentStore(filepath.Join(t.TempDir(), "l.json"))); err != nil {
		t.Errorf("Persist to file store: %v", err)
	}
}

// TestPolicyVersionerPersistWithoutStore covers persistLocked's nil-store
// branch (no-op).
func TestPolicyVersionerPersistWithoutStore(t *testing.T) {
	pv := NewPolicyVersioner(NewRouteRegistry(), nil)
	pv.mu.Lock()
	err := pv.persistLocked()
	pv.mu.Unlock()
	if err != nil {
		t.Errorf("persistLocked without store must be a no-op, got %v", err)
	}
}
