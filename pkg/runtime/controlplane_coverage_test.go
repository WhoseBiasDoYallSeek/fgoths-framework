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
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestControlPlaneServerWithStoresAndLifecycle covers the store-injection
// constructor plus the programmatic accessors (Reload, Registry, Ledger,
// StorePath, Close).
func TestControlPlaneServerWithStoresAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	depStore := NewFileDeploymentStore(filepath.Join(dir, "ledger.json"))
	policyStore := NewFilePolicyVersionStore(filepath.Join(dir, "policies.json"))

	cp := NewControlPlaneServerWithStores(ControlPlaneConfig{AdminToken: "t"}, depStore, policyStore)
	if cp == nil {
		t.Fatal("expected non-nil control plane server")
	}

	if cp.Registry() == nil {
		t.Error("Registry() must return the route registry")
	}
	if cp.Ledger() == nil {
		t.Error("Ledger() must return the deployment ledger")
	}
	if cp.PolicyVersioner() == nil {
		t.Error("PolicyVersioner() must return the versioner")
	}
	if got := cp.StorePath(); got != filepath.Join(dir, "ledger.json") {
		t.Errorf("StorePath() = %q, want the injected file store path", got)
	}

	// Reload with a working store must succeed.
	if err := cp.Reload(); err != nil {
		t.Errorf("Reload() error = %v", err)
	}

	// A server built without a store must fail Reload and return empty
	// StorePath.
	cpNoStore := NewControlPlaneServerWithStores(ControlPlaneConfig{AdminToken: "t"}, nil, nil)
	if err := cpNoStore.Reload(); err == nil {
		t.Error("expected Reload error without a store")
	}
	if got := cpNoStore.StorePath(); got != "" {
		t.Errorf("StorePath() without store = %q, want empty", got)
	}

	// Close must be idempotent and nil-safe.
	if err := cp.Close(); err != nil {
		t.Errorf("first Close() error = %v", err)
	}
	if err := cp.Close(); err != nil {
		t.Errorf("second Close() error = %v", err)
	}
	var nilCP *ControlPlaneServer
	if err := nilCP.Close(); err != nil {
		t.Errorf("nil Close() error = %v", err)
	}
}

// TestControlPlanePolicyLifecycle exercises the /policies endpoints: create,
// list, get, and get-miss.
func TestControlPlanePolicyLifecycle(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	resp, body := cpRequest(t, ts, "POST", "/api/v1/policies", "test-token", map[string]any{
		"name": "orders-prod", "environment": "prod", "service": "orders",
		"require_audit": true,
		"actor":         "alice", "reason": "initial policy",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 creating policy, got %d: %v", resp.StatusCode, body)
	}

	resp, body = cpRequest(t, ts, "GET", "/api/v1/policies", "test-token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 listing policies, got %d", resp.StatusCode)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 1 {
		t.Fatalf("expected 1 policy, got %v", body)
	}

	resp, body = cpRequest(t, ts, "GET", "/api/v1/policies/orders-prod", "test-token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 getting policy, got %d: %v", resp.StatusCode, body)
	}

	resp, _ = cpRequest(t, ts, "GET", "/api/v1/policies/missing", "test-token", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing policy, got %d", resp.StatusCode)
	}

	// History endpoint reflects the create revision.
	resp, _ = cpRequest(t, ts, "GET", "/api/v1/policies/orders-prod/history", "test-token", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for policy history, got %d", resp.StatusCode)
	}
}

// TestControlPlaneRejectRelease covers the reject path of the release
// workflow (terminate before approval).
func TestControlPlaneRejectRelease(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	defer cp.Close()

	resp, _ := cpRequest(t, ts, "POST", "/api/v1/releases", "test-token", map[string]any{
		"service": "payments", "environment": "staging", "version": "v2.0.0",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201 staging release, got %d", resp.StatusCode)
	}

	resp, body := cpRequest(t, ts, "POST", "/api/v1/releases/payments/staging/v2.0.0/reject", "test-token", map[string]any{
		"actor": "alice", "reason": "failing smoke tests",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 rejecting release, got %d: %v", resp.StatusCode, body)
	}

	_, body = cpRequest(t, ts, "GET", "/api/v1/releases/payments/staging/v2.0.0", "test-token", nil)
	if body["state"] != "rejected" {
		t.Fatalf("expected rejected state, got %v", body)
	}

	// Rejecting an unknown workflow must 404.
	resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/payments/staging/v9.9.9/reject", "test-token", map[string]any{
		"actor": "alice", "reason": "nope",
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 rejecting unknown release, got %d", resp.StatusCode)
	}
}

// TestServerShutdownLifecycle covers WithShutdownTimeout, OnShutdown hooks and
// graceful Shutdown.
func TestServerShutdownLifecycle(t *testing.T) {
	srv := NewServer(":0").WithShutdownTimeout(2 * time.Second)

	var hookOrder []string
	srv.OnShutdown(func(context.Context) error {
		hookOrder = append(hookOrder, "first")
		return nil
	})
	srv.OnShutdown(func(context.Context) error {
		hookOrder = append(hookOrder, "second")
		return nil
	})

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	go func() { _ = srv.ListenAndServe() }()
	time.Sleep(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(hookOrder) != 2 || hookOrder[0] != "first" || hookOrder[1] != "second" {
		t.Errorf("shutdown hooks ran out of order or incompletely: %v", hookOrder)
	}
}

// TestServerShutdownHookErrorPropagates verifies the first failing hook
// short-circuits the remaining ones and surfaces the error.
func TestServerShutdownHookErrorPropagates(t *testing.T) {
	srv := NewServer(":0")
	secondCalled := false
	srv.OnShutdown(func(context.Context) error { return os.ErrDeadlineExceeded })
	srv.OnShutdown(func(context.Context) error { secondCalled = true; return nil })

	if err := srv.Shutdown(context.Background()); err == nil {
		t.Fatal("expected Shutdown to surface the hook error")
	}
	if secondCalled {
		t.Error("second hook must not run after the first fails")
	}
}

// TestFileDeploymentStoreCloseIsNoOp documents that file stores close without
// error (no held descriptors).
func TestFileDeploymentStoreCloseIsNoOp(t *testing.T) {
	store := NewFileDeploymentStore(filepath.Join(t.TempDir(), "ledger.json"))
	if err := store.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
	if err := NewFilePolicyVersionStore(filepath.Join(t.TempDir(), "p.json")).Close(); err != nil {
		t.Errorf("policy store Close() error = %v", err)
	}
}
