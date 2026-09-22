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
	"sort"
	"sync"
	"testing"
	"time"
)

// percentile returns the p-th percentile (0-100) of a slice of durations using
// nearest-rank interpolation. It is a test-only helper; the runtime does not
// need to track percentiles internally to validate latency behavior here.
func percentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	rank := int(p/100*float64(len(sorted))) - 1
	if rank < 0 {
		rank = 0
	}
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

func TestPercentileHelperComputesKnownDistribution(t *testing.T) {
	samples := make([]time.Duration, 0, 100)
	for i := 1; i <= 100; i++ {
		samples = append(samples, time.Duration(i)*time.Millisecond)
	}
	if got := percentile(samples, 50); got != 50*time.Millisecond {
		t.Fatalf("p50 = %v, want 50ms", got)
	}
	if got := percentile(samples, 99); got != 99*time.Millisecond {
		t.Fatalf("p99 = %v, want 99ms", got)
	}
	if got := percentile(samples, 100); got != 100*time.Millisecond {
		t.Fatalf("p100 = %v, want 100ms", got)
	}
	if got := percentile(nil, 99); got != 0 {
		t.Fatalf("percentile of an empty sample set = %v, want 0", got)
	}
}

// TestRouterServeHTTPP99LatencyUnderConcurrentLoad exercises the in-memory
// router with concurrent requests and asserts the p99 request latency stays
// within a generous bound. This is a regression guard against accidental
// lock contention or O(n) route-matching regressions, not a strict SLA.
func TestRouterServeHTTPP99LatencyUnderConcurrentLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency benchmark in short mode")
	}
	r := NewRouter()
	r.Get("/orders", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const (
		workers         = 50
		requestsPerWork = 40
	)
	latencies := make([]time.Duration, workers*requestsPerWork)
	var wg sync.WaitGroup
	var mu sync.Mutex
	idx := 0
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < requestsPerWork; i++ {
				req := httptest.NewRequest(http.MethodGet, "/orders", nil)
				res := httptest.NewRecorder()
				start := time.Now()
				r.ServeHTTP(res, req)
				elapsed := time.Since(start)
				mu.Lock()
				latencies[idx] = elapsed
				idx++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	p99 := percentile(latencies, 99)
	const maxAcceptableP99 = 50 * time.Millisecond
	if p99 > maxAcceptableP99 {
		t.Fatalf("p99 router dispatch latency = %v, want <= %v (in-memory in-process dispatch should be fast)", p99, maxAcceptableP99)
	}
	t.Logf("router dispatch p99=%v p50=%v over %d requests", p99, percentile(latencies, 50), len(latencies))
}

// TestProxyServeHTTPP99LatencyUnderConcurrentLoad exercises the reverse proxy
// path (including the director, header propagation and observer hook)
// against a local httptest upstream and checks p99 latency stays bounded.
func TestProxyServeHTTPP99LatencyUnderConcurrentLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping latency benchmark in short mode")
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithMetrics()

	const (
		workers         = 20
		requestsPerWork = 20
	)
	latencies := make([]time.Duration, workers*requestsPerWork)
	var wg sync.WaitGroup
	var mu sync.Mutex
	idx := 0
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for i := 0; i < requestsPerWork; i++ {
				req := httptest.NewRequest(http.MethodGet, "/orders", nil)
				res := httptest.NewRecorder()
				start := time.Now()
				proxy.ServeHTTP(res, req)
				elapsed := time.Since(start)
				mu.Lock()
				latencies[idx] = elapsed
				idx++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	p99 := percentile(latencies, 99)
	const maxAcceptableP99 = 250 * time.Millisecond
	if p99 > maxAcceptableP99 {
		t.Fatalf("p99 proxy round-trip latency = %v, want <= %v (local httptest upstream should be fast)", p99, maxAcceptableP99)
	}
	t.Logf("proxy round-trip p99=%v p50=%v over %d requests", p99, percentile(latencies, 50), len(latencies))

	if got := proxy.Metrics().Snapshot().TotalRequests; got != int64(len(latencies)) {
		t.Fatalf("expected %d recorded proxy requests, got %d", len(latencies), got)
	}
}

// BenchmarkRouterServeHTTP measures router dispatch throughput/latency; run
// with `go test -bench=RouterServeHTTP -benchtime=2s ./pkg/runtime` and inspect
// ns/op as an additional latency signal alongside the p99 tests above.
func BenchmarkRouterServeHTTP(b *testing.B) {
	r := NewRouter()
	r.Get("/orders", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/orders", nil)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
		}
	})
}
