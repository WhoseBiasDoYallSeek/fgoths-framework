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
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkRouterPercentiles measures the full latency distribution of the
// in-memory router dispatch and reports p50/p90/p99/p999/max as metrics.
func BenchmarkRouterPercentiles(b *testing.B) {
	r := NewRouter()
	r.Get("/orders/:id", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":42}`))
	})

	latencies := make([]time.Duration, 0, b.N)
	var mu sync.Mutex

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		local := make([]time.Duration, 0, 1024)
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/orders/42", nil)
			res := httptest.NewRecorder()
			start := time.Now()
			r.ServeHTTP(res, req)
			local = append(local, time.Since(start))
		}
		mu.Lock()
		latencies = append(latencies, local...)
		mu.Unlock()
	})
	b.StopTimer()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	b.ReportMetric(float64(percentile(latencies, 50).Microseconds()), "p50-µs")
	b.ReportMetric(float64(percentile(latencies, 90).Microseconds()), "p90-µs")
	b.ReportMetric(float64(percentile(latencies, 99).Microseconds()), "p99-µs")
	b.ReportMetric(float64(percentile(latencies, 99.9).Microseconds()), "p999-µs")
	b.ReportMetric(float64(latencies[len(latencies)-1].Microseconds()), "max-µs")
}

// BenchmarkProxyPercentiles measures the proxy round-trip latency distribution
// (director + header propagation + observer) against an httptest upstream.
func BenchmarkProxyPercentiles(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		b.Fatal(err)
	}
	proxy.WithMetrics()

	latencies := make([]time.Duration, 0, b.N)
	var mu sync.Mutex

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		local := make([]time.Duration, 0, 1024)
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/anything", nil)
			res := httptest.NewRecorder()
			start := time.Now()
			proxy.ServeHTTP(res, req)
			local = append(local, time.Since(start))
		}
		mu.Lock()
		latencies = append(latencies, local...)
		mu.Unlock()
	})
	b.StopTimer()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	b.ReportMetric(float64(percentile(latencies, 50).Microseconds()), "p50-µs")
	b.ReportMetric(float64(percentile(latencies, 99).Microseconds()), "p99-µs")
	b.ReportMetric(float64(percentile(latencies, 99.9).Microseconds()), "p999-µs")
}

// TestNetworkLatencyPercentilesGuard measures tail latency over a real TCP
// socket (not in-memory), the closest local approximation of production
// behavior. Ceilings are generous: this catches gross regressions, not
// micro-fluctuations.
func TestNetworkLatencyPercentilesGuard(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping network latency guard in short mode")
	}
	r := NewRouter()
	r.Get("/ping", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})
	server := httptest.NewServer(r)
	defer server.Close()

	client := &http.Client{Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}}

	var latencies []time.Duration
	var mu sync.Mutex
	var wg sync.WaitGroup
	stop := make(chan struct{})
	var errors atomic.Int64

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []time.Duration
			for {
				select {
				case <-stop:
					mu.Lock()
					latencies = append(latencies, local...)
					mu.Unlock()
					return
				default:
				}
				start := time.Now()
				resp, err := client.Get(server.URL + "/ping")
				elapsed := time.Since(start)
				if err != nil {
					errors.Add(1)
					continue
				}
				resp.Body.Close()
				local = append(local, elapsed)
			}
		}()
	}

	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	n := len(latencies)
	if n < 1000 {
		t.Fatalf("insufficient samples: %d (errors: %d)", n, errors.Load())
	}
	p50 := percentile(latencies, 50)
	p99 := percentile(latencies, 99)
	p999 := percentile(latencies, 99.9)
	t.Logf("samples=%d errors=%d p50=%v p99=%v p99.9=%v max=%v", n, errors.Load(), p50, p99, p999, latencies[n-1])

	if p99 > 50*time.Millisecond {
		t.Errorf("network p99 latency %v exceeds 50ms ceiling", p99)
	}
	if p999 > 250*time.Millisecond {
		t.Errorf("network p99.9 latency %v exceeds 250ms ceiling", p999)
	}
}
