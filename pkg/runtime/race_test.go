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
	"sync"
	"testing"
	"time"
)

// TestConcurrentRouteRegistryAccessIsRaceFree hammers RouteRegistry from many
// goroutines at once. Run with `go test -race` to catch data races; the
// assertions here also verify the registry stays internally consistent under
// concurrent load.
func TestConcurrentRouteRegistryAccessIsRaceFree(t *testing.T) {
	registry := NewRouteRegistry()
	const workers = 50

	var wg sync.WaitGroup
	wg.Add(workers * 4)
	for i := 0; i < workers; i++ {
		name := "route-" + string(rune('A'+i%26)) + string(rune('0'+i/26))
		go func(name string) {
			defer wg.Done()
			_ = registry.Register(RouteConfig{Name: name, Method: "GET", Path: "/" + name, Target: "http://upstream.internal", Weight: 100, Enabled: true})
		}(name)
		go func() {
			defer wg.Done()
			registry.List()
		}()
		go func(name string) {
			defer wg.Done()
			registry.Get(name)
		}(name)
		go func() {
			defer wg.Done()
			_ = registry.RegisterPolicy(RoutePolicy{Name: "policy", Environment: "staging", Service: "svc"})
		}()
	}
	wg.Wait()

	if len(registry.List()) == 0 {
		t.Fatal("expected at least one route to have been registered concurrently")
	}
}

// TestConcurrentDeploymentLedgerAccessIsRaceFree exercises the deployment
// ledger's Record/Current/History paths concurrently across many services.
func TestConcurrentDeploymentLedgerAccessIsRaceFree(t *testing.T) {
	ledger := NewDeploymentLedger()
	const workers = 30

	var wg sync.WaitGroup
	wg.Add(workers * 3)
	for i := 0; i < workers; i++ {
		version := string(rune('a' + i%26))
		go func(version string) {
			defer wg.Done()
			manifest := NewDeploymentManifest("staging", "orders-api", "1.0."+version)
			manifest.Audit = []string{"reviewed"}
			_ = ledger.Record(manifest)
		}(version)
		go func() {
			defer wg.Done()
			ledger.Current("orders-api", "staging")
		}()
		go func() {
			defer wg.Done()
			ledger.History("orders-api", "staging")
		}()
	}
	wg.Wait()

	if _, ok := ledger.Current("orders-api", "staging"); !ok {
		t.Fatal("expected at least one manifest to have been recorded concurrently")
	}
}

// TestConcurrentDeploymentEventLogAccessIsRaceFree exercises event recording
// and reads concurrently to ensure the operator timeline stays race-free.
func TestConcurrentDeploymentEventLogAccessIsRaceFree(t *testing.T) {
	log := NewDeploymentEventLog()
	const workers = 40

	var wg sync.WaitGroup
	wg.Add(workers * 2)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			_ = log.Record("orders-api", "prod", "1.0.0", "promote", "actor", "reason")
		}(i)
		go func() {
			defer wg.Done()
			log.History("orders-api", "prod")
			log.Latest("orders-api", "prod")
		}()
	}
	wg.Wait()

	events, ok := log.History("orders-api", "prod")
	if !ok || len(events) != workers {
		t.Fatalf("expected %d recorded events, got %d (ok=%v)", workers, len(events), ok)
	}
}

// TestConcurrentApprovalGateAccessIsRaceFree ensures approvals from many
// actors racing to approve the same gate only ever count unique approvers.
func TestConcurrentApprovalGateAccessIsRaceFree(t *testing.T) {
	gate := NewApprovalGate("release", 10)
	const workers = 40

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		actor := string(rune('a' + i%10))
		go func(actor string) {
			defer wg.Done()
			_ = gate.Approve(actor)
		}(actor)
	}
	wg.Wait()

	if len(gate.Approvals) != 10 {
		t.Fatalf("expected exactly 10 unique approvers, got %d: %#v", len(gate.Approvals), gate.Approvals)
	}
	if !gate.IsApproved() {
		t.Fatal("expected the gate to be approved once the threshold is reached")
	}
}

// TestConcurrentMetricsRecordIsRaceFree records metrics from many goroutines
// simultaneously and verifies the aggregate counters remain consistent.
func TestConcurrentMetricsRecordIsRaceFree(t *testing.T) {
	metrics := NewMetrics()
	const workers = 100

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func(i int) {
			defer wg.Done()
			status := http.StatusOK
			if i%10 == 0 {
				status = http.StatusInternalServerError
			}
			metrics.Record(http.MethodGet, "/orders", status, time.Millisecond)
		}(i)
	}
	wg.Wait()

	snapshot := metrics.Snapshot()
	if snapshot.TotalRequests != workers {
		t.Fatalf("expected %d total requests, got %d", workers, snapshot.TotalRequests)
	}
	if snapshot.TotalErrors != workers/10 {
		t.Fatalf("expected %d total errors, got %d", workers/10, snapshot.TotalErrors)
	}
}

// TestConcurrentCircuitBreakerAccessIsRaceFree hammers the proxy circuit
// breaker's allow/recordSuccess/recordFailure paths concurrently.
func TestConcurrentCircuitBreakerAccessIsRaceFree(t *testing.T) {
	cb := newProxyCircuitBreaker(5, time.Second, 10*time.Millisecond)
	const workers = 50

	var wg sync.WaitGroup
	wg.Add(workers * 3)
	for i := 0; i < workers; i++ {
		go func() { defer wg.Done(); cb.allow() }()
		go func() { defer wg.Done(); cb.recordSuccess() }()
		go func() { defer wg.Done(); cb.recordFailure() }()
	}
	wg.Wait()
}

// TestConcurrentRateLimiterAccessIsRaceFree hammers the proxy rate limiter's
// allow path concurrently from many goroutines.
func TestConcurrentRateLimiterAccessIsRaceFree(t *testing.T) {
	rl := newProxyRateLimiter(1000, 1000)
	const workers = 100

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() { defer wg.Done(); rl.allow() }()
	}
	wg.Wait()
}

// TestConcurrentRouterDispatchIsRaceFree serves many concurrent requests
// through a shared Router with middleware and a metrics-backed server to
// verify the whole request path is safe under concurrency.
func TestConcurrentRouterDispatchIsRaceFree(t *testing.T) {
	server := NewServer(":0").WithMetrics()
	server.Get("/orders", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	const workers = 100
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://example.com/orders", nil)
			res := httptest.NewRecorder()
			server.router.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Errorf("expected 200, got %d", res.Code)
			}
		}()
	}
	wg.Wait()

	if got := server.Metrics().Snapshot().TotalRequests; got != workers {
		t.Fatalf("expected %d recorded requests, got %d", workers, got)
	}
}
