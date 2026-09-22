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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type failingDeploymentStore struct{}

func (failingDeploymentStore) Save(DeploymentLedgerState) error {
	return errors.New("store unavailable")
}
func (failingDeploymentStore) Load() (DeploymentLedgerState, error) {
	return DeploymentLedgerState{}, nil
}
func (failingDeploymentStore) Close() error { return nil }

func newTestControlPlane(t *testing.T) (*ControlPlaneServer, *httptest.Server) {
	t.Helper()
	cp := NewControlPlaneServer(ControlPlaneConfig{
		StorePath:  t.TempDir() + "/ledger.json",
		AdminToken: "test-token",
	})
	ts := httptest.NewServer(cp.Handler())
	t.Cleanup(ts.Close)
	return cp, ts
}

func cpRequest(t *testing.T, ts *httptest.Server, method, path, token string, body any) (*http.Response, map[string]any) {
	t.Helper()
	var buf *bytes.Buffer
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		buf = bytes.NewBuffer(raw)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	req, err := http.NewRequest(method, ts.URL+path, buf)
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
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp, out
}

func TestControlPlaneAuthFailsClosed(t *testing.T) {
	cp, ts := newTestControlPlane(t)

	t.Run("missing token", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "GET", "/api/v1/routes", "", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
		}
	})
	t.Run("wrong token", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "GET", "/api/v1/routes", "wrong", nil)
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 with wrong token, got %d", resp.StatusCode)
		}
	})
	t.Run("valid token reaches api", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "GET", "/api/v1/routes", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 with valid token, got %d", resp.StatusCode)
		}
	})
	t.Run("health endpoint is public", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "GET", "/healthz", "", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on public healthz, got %d", resp.StatusCode)
		}
	})
	_ = cp
}

func TestControlPlaneUpstreamsAndRoutes(t *testing.T) {
	_, ts := newTestControlPlane(t)

	t.Run("register upstream", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "POST", "/api/v1/upstreams", "test-token", map[string]any{
			"name": "orders", "target": "http://orders.internal", "enabled": true, "healthy": true,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %v", resp.StatusCode, body)
		}
	})
	t.Run("duplicate upstream rejected", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "POST", "/api/v1/upstreams", "test-token", map[string]any{
			"name": "orders", "target": "http://orders2.internal", "enabled": true,
		})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 for duplicate upstream, got %d", resp.StatusCode)
		}
	})
	t.Run("list upstreams", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "GET", "/api/v1/upstreams", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		items, ok := body["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("expected 1 upstream, got %v", body)
		}
	})
	t.Run("register route referencing upstream", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "POST", "/api/v1/routes", "test-token", map[string]any{
			"name": "get-order", "method": "GET", "path": "/orders/:id", "upstream": "orders", "enabled": true,
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %v", resp.StatusCode, body)
		}
	})
	t.Run("resolve route target", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "GET", "/api/v1/routes/get-order/target", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d: %v", resp.StatusCode, body)
		}
		if body["target"] != "http://orders.internal" {
			t.Fatalf("expected resolved target, got %v", body)
		}
	})
	t.Run("invalid upstream rejected with 422", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "POST", "/api/v1/upstreams", "test-token", map[string]any{
			"name": "", "target": "", "enabled": true,
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for invalid upstream, got %d", resp.StatusCode)
		}
	})
}

func TestControlPlaneDeployLifecycle(t *testing.T) {
	cp, ts := newTestControlPlane(t)

	// Stage a non-prod manifest (prod requires signed artifacts + allowlists).
	t.Run("stage manifest", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "POST", "/api/v1/deployments", "test-token", map[string]any{
			"service": "orders", "environment": "staging", "version": "v1.0.0",
			"audit": []string{"staged via control plane API"},
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %v", resp.StatusCode, body)
		}
	})
	t.Run("current deployment", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "GET", "/api/v1/deployments/orders/staging", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		if body["version"] != "v1.0.0" {
			t.Fatalf("expected version v1.0.0, got %v", body)
		}
	})
	t.Run("record same version rejected", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "POST", "/api/v1/deployments", "test-token", map[string]any{
			"service": "orders", "environment": "staging", "version": "v1.0.0",
			"audit": []string{"duplicate"},
		})
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf("expected 409 for duplicate version, got %d", resp.StatusCode)
		}
	})
	t.Run("history", func(t *testing.T) {
		resp, body := cpRequest(t, ts, "GET", "/api/v1/deployments/orders/staging/history", "test-token", nil)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		items, ok := body["items"].([]any)
		if !ok || len(items) != 1 {
			t.Fatalf("expected 1 history entry, got %v", body)
		}
	})

	t.Run("release workflow approve then rollback", func(t *testing.T) {
		cp.SetWorkflowGate("orders", "staging", 2)

		resp, body := cpRequest(t, ts, "POST", "/api/v1/releases", "test-token", map[string]any{
			"service": "orders", "environment": "staging", "version": "v1.1.0",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %v", resp.StatusCode, body)
		}

		// First approval does not reach the threshold.
		resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/orders/staging/v1.1.0/approve", "test-token", map[string]any{"actor": "alice"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on first approval, got %d", resp.StatusCode)
		}
		_, body = cpRequest(t, ts, "GET", "/api/v1/releases/orders/staging/v1.1.0", "test-token", nil)
		if body["state"] != "pending" {
			t.Fatalf("expected pending after first approval, got %v", body)
		}

		// Second approval crosses the gate.
		resp, _ = cpRequest(t, ts, "POST", "/api/v1/releases/orders/staging/v1.1.0/approve", "test-token", map[string]any{"actor": "bob"})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on second approval, got %d", resp.StatusCode)
		}
		_, body = cpRequest(t, ts, "GET", "/api/v1/releases/orders/staging/v1.1.0", "test-token", nil)
		if body["state"] != "approved" {
			t.Fatalf("expected approved after gate, got %v", body)
		}

		// Rollback is terminal.
		resp, body = cpRequest(t, ts, "POST", "/api/v1/releases/orders/staging/v1.1.0/rollback", "test-token", map[string]any{
			"actor": "alice", "reason": "error rate spike",
		})
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200 on rollback, got %d: %v", resp.StatusCode, body)
		}
		resp, body = cpRequest(t, ts, "GET", "/api/v1/releases/orders/staging/v1.1.0", "test-token", nil)
		if body["state"] != "rolled_back" {
			t.Fatalf("expected rolled_back, got %v", body)
		}
	})

	// Persistence: state must survive a fresh server on the same store path.
	t.Run("state survives restart", func(t *testing.T) {
		cp2 := NewControlPlaneServer(ControlPlaneConfig{
			StorePath:  cp.StorePath(),
			AdminToken: "test-token",
		})
		if _, ok := cp2.Ledger().Current("orders", "staging"); !ok {
			t.Fatal("expected persisted deployment to survive restart")
		}
	})
}

func TestControlPlaneWorkflowGatesAreScoped(t *testing.T) {
	cp, ts := newTestControlPlane(t)
	cp.SetWorkflowGate("orders", "staging", 2)

	for _, release := range []struct {
		service     string
		environment string
		wantGate    int
	}{
		{service: "orders", environment: "staging", wantGate: 2},
		{service: "orders", environment: "prod", wantGate: 1},
		{service: "payments", environment: "staging", wantGate: 1},
	} {
		resp, body := cpRequest(t, ts, http.MethodPost, "/api/v1/releases", "test-token", map[string]any{
			"service": release.service, "environment": release.environment, "version": "v1",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s/%s release: got %d: %v", release.service, release.environment, resp.StatusCode, body)
		}
		workflow, ok := cp.workflow(release.service, release.environment, "v1")
		if !ok || workflow.Gate.Required != release.wantGate {
			t.Fatalf("gate for %s/%s = %#v, want %d approvals", release.service, release.environment, workflow, release.wantGate)
		}
	}
}

func TestControlPlaneRejectsDeploymentWhenPersistenceFails(t *testing.T) {
	cp := NewControlPlaneServerWithStores(ControlPlaneConfig{AdminToken: "test-token"}, failingDeploymentStore{}, nil)
	ts := httptest.NewServer(cp.Handler())
	defer ts.Close()

	resp, body := cpRequest(t, ts, http.MethodPost, "/api/v1/deployments", "test-token", map[string]any{
		"service": "orders", "environment": "staging", "version": "v1",
		"audit": []string{"staged by persistence regression test"},
	})
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected persistence failure to return 500, got %d: %v", resp.StatusCode, body)
	}
}

func TestControlPlaneValidationErrors(t *testing.T) {
	_, ts := newTestControlPlane(t)

	t.Run("malformed json", func(t *testing.T) {
		req, _ := http.NewRequest("POST", ts.URL+"/api/v1/upstreams", strings.NewReader("{not json"))
		req.Header.Set("Authorization", "Bearer test-token")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("expected 400 for malformed json, got %d", resp.StatusCode)
		}
	})
	t.Run("unknown route 404", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "GET", "/api/v1/ghost", "test-token", nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", resp.StatusCode)
		}
	})
	t.Run("method not allowed 405", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "DELETE", "/api/v1/routes", "test-token", nil)
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", resp.StatusCode)
		}
	})
	t.Run("deployment without audit rejected", func(t *testing.T) {
		resp, _ := cpRequest(t, ts, "POST", "/api/v1/deployments", "test-token", map[string]any{
			"service": "ghost-svc", "environment": "staging", "version": "v0.1.0",
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 without audit trail, got %d", resp.StatusCode)
		}
	})
	_, _ = time.Now(), fmt.Sprintf
}
