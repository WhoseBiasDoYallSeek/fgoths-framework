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
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DurationPercentiles holds observed latency percentiles.
type DurationPercentiles struct {
	P50 time.Duration
	P95 time.Duration
	P99 time.Duration
}

// RouteHistogram holds per-route latency samples for percentile calculation.
type RouteHistogram struct {
	mu      sync.Mutex
	samples []time.Duration // sliding window, oldest are evicted
	maxSize int             // capacity cap
	// percentilesDirty marks that samples changed since the last
	// Snapshot, deferring the O(n log n) recompute out of Record.
	percentilesDirty bool
	p50              time.Duration
	p95              time.Duration
	P99              time.Duration
}

func newRouteHistogram(capacity int) *RouteHistogram {
	return &RouteHistogram{samples: make([]time.Duration, 0, capacity), maxSize: capacity}
}

// Record adds a latency sample. Percentile computation is deferred to
// Snapshot, keeping Record O(1) amortized — no per-request copy or sort.
func (h *RouteHistogram) Record(d time.Duration) {
	if d < 0 {
		d = 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.samples = append(h.samples, d)
	if len(h.samples) > h.maxSize {
		// Evict oldest 10%
		cut := h.maxSize / 10
		if cut < 1 {
			cut = 1
		}
		h.samples = h.samples[cut:]
	}
	h.percentilesDirty = true
}

// Snapshot returns the current percentiles, recomputing them from the
// sample window when new samples were recorded since the last snapshot.
func (h *RouteHistogram) Snapshot() DurationPercentiles {
	if h == nil {
		return DurationPercentiles{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.percentilesDirty {
		h.recomputePercentilesLocked()
		h.percentilesDirty = false
	}
	return DurationPercentiles{P50: h.p50, P95: h.p95, P99: h.P99}
}

// recomputePercentilesLocked sorts the sample window and derives p50/p95/p99.
// Callers must hold h.mu.
func (h *RouteHistogram) recomputePercentilesLocked() {
	if len(h.samples) == 0 {
		h.p50, h.p95, h.P99 = 0, 0, 0
		return
	}
	sorted := make([]time.Duration, len(h.samples))
	copy(sorted, h.samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	n := len(sorted)
	h.p50 = sorted[n*50/100]
	h.p95 = sorted[n*95/100]
	h.P99 = sorted[n*99/100]
}

// MetricsSnapshot contains a point-in-time view of request counters, route
// activity, and duration measurements captured by the FGOTHS runtime.
type MetricsSnapshot struct {
	TotalRequests  int64
	TotalErrors    int64
	TotalLatency   time.Duration
	AvgLatency     time.Duration
	ByMethod       map[string]int64
	ByRoute        map[string]int64
	ByStatus       map[int]int64
	ByRouteLatency map[string]time.Duration
	// Histogram percentiles per route (p50, p95, p99)
	ByRoutePercentiles map[string]DurationPercentiles
}

// Metrics collects lightweight runtime telemetry for server and proxy traffic.
// Hot counters use atomics so Record's read-modify-writes never contend with
// Snapshot; the mutex guards only the maps and the error ring, keeping its
// critical section minimal under high request concurrency.
type Metrics struct {
	mu               sync.RWMutex
	totalRequests    atomic.Int64
	totalErrors      atomic.Int64
	totalLatency     atomic.Int64 // nanoseconds
	byMethod         map[string]int64
	byRoute          map[string]int64
	byStatus         map[int]int64
	byRouteLatency   map[string]time.Duration
	byRouteHistogram map[string]*RouteHistogram
	latencyHistogram *RouteHistogram // global latency samples for overall percentiles

	// errorWindow is a fixed-size ring of the last errorWindowCap request
	// outcomes (true = error), backing the bounded-window error rate used
	// by WithAlertThreshold. Cumulative rates dilute spikes; this does not.
	errorWindow   []bool
	errorWinHead  int
	errorWinCount int64
	errorWinErrs  int64
}

const errorWindowCap = 1000

func NewMetrics() *Metrics {
	return &Metrics{
		byMethod:         make(map[string]int64),
		byRoute:          make(map[string]int64),
		byStatus:         make(map[int]int64),
		byRouteLatency:   make(map[string]time.Duration),
		byRouteHistogram: make(map[string]*RouteHistogram),
		latencyHistogram: newRouteHistogram(10000),
	}
}

func (m *Metrics) Record(method, route string, status int, elapsed ...time.Duration) {
	if m == nil {
		return
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}
	route = normalizePath(route)
	if route == "" {
		route = "/"
	}
	var duration time.Duration
	if len(elapsed) > 0 {
		duration = elapsed[0]
		if duration < 0 {
			duration = 0
		}
	}
	m.mu.Lock()
	m.totalRequests.Add(1)
	m.byMethod[method]++
	m.byRoute[route]++
	m.byStatus[status]++
	m.totalLatency.Add(int64(duration))
	m.byRouteLatency[route] += duration
	if status >= http.StatusBadRequest {
		m.totalErrors.Add(1)
	}
	// Sliding-window counters for alerting: cumulative error rates dilute
	// spikes over a long-lived process, so alerts watch a bounded window.
	if m.errorWindow == nil {
		m.errorWindow = make([]bool, errorWindowCap)
	}
	if m.errorWinCount < errorWindowCap {
		m.errorWindow[m.errorWinHead] = status >= http.StatusBadRequest
		m.errorWinHead = (m.errorWinHead + 1) % errorWindowCap
		m.errorWinCount++
		if status >= http.StatusBadRequest {
			m.errorWinErrs++
		}
	} else {
		evicted := m.errorWindow[m.errorWinHead]
		if evicted {
			m.errorWinErrs--
		}
		m.errorWindow[m.errorWinHead] = status >= http.StatusBadRequest
		m.errorWinHead = (m.errorWinHead + 1) % errorWindowCap
		if status >= http.StatusBadRequest {
			m.errorWinErrs++
		}
	}
	routeHist, ok := m.byRouteHistogram[route]
	if !ok {
		routeHist = newRouteHistogram(1000)
		m.byRouteHistogram[route] = routeHist
	}
	globalHist := m.latencyHistogram
	m.mu.Unlock()

	// Histograms carry their own lock, so samples are recorded outside the
	// global metrics lock — shrinking its critical section under contention.
	if globalHist != nil {
		globalHist.Record(duration)
	}
	routeHist.Record(duration)
}

// ErrorWindow reports the error rate over the last `size` requests (a
// bounded sliding window, not the cumulative process lifetime). Returns
// (requests, errorRate).
func (m *Metrics) ErrorWindow(size int) (int64, float64) {
	if m == nil || size <= 0 {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	reqs := m.errorWinCount
	if reqs > int64(size) {
		reqs = int64(size)
	}
	if reqs == 0 {
		return 0, 0
	}
	return reqs, float64(m.errorWinErrs) / float64(reqs)
}

func (m *Metrics) Snapshot() MetricsSnapshot {
	if m == nil {
		return MetricsSnapshot{ByMethod: map[string]int64{}, ByRoute: map[string]int64{}, ByStatus: map[int]int64{}, ByRouteLatency: map[string]time.Duration{}}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cloneMethod := make(map[string]int64, len(m.byMethod))
	for key, value := range m.byMethod {
		cloneMethod[key] = value
	}
	cloneRoute := make(map[string]int64, len(m.byRoute))
	for key, value := range m.byRoute {
		cloneRoute[key] = value
	}
	cloneStatus := make(map[int]int64, len(m.byStatus))
	for key, value := range m.byStatus {
		cloneStatus[key] = value
	}
	cloneRouteLatency := make(map[string]time.Duration, len(m.byRouteLatency))
	for key, value := range m.byRouteLatency {
		cloneRouteLatency[key] = value
	}
	clonePercentiles := make(map[string]DurationPercentiles, len(m.byRouteHistogram))
	for key, h := range m.byRouteHistogram {
		clonePercentiles[key] = h.Snapshot()
	}
	totalRequests := m.totalRequests.Load()
	totalLatency := time.Duration(m.totalLatency.Load())
	avgLatency := time.Duration(0)
	if totalRequests > 0 {
		avgLatency = totalLatency / time.Duration(totalRequests)
	}
	return MetricsSnapshot{
		TotalRequests:      totalRequests,
		TotalErrors:        m.totalErrors.Load(),
		TotalLatency:       totalLatency,
		AvgLatency:         avgLatency,
		ByMethod:           cloneMethod,
		ByRoute:            cloneRoute,
		ByStatus:           cloneStatus,
		ByRouteLatency:     cloneRouteLatency,
		ByRoutePercentiles: clonePercentiles,
	}
}

// GlobalPercentiles returns the overall request latency percentiles (p50/p95/p99)
// across all routes without requiring a full snapshot.
func (m *Metrics) GlobalPercentiles() DurationPercentiles {
	if m == nil || m.latencyHistogram == nil {
		return DurationPercentiles{}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latencyHistogram.Snapshot()
}

// RoutePercentiles returns the latency percentiles for a specific route.
func (m *Metrics) RoutePercentiles(route string) DurationPercentiles {
	if m == nil {
		return DurationPercentiles{}
	}
	route = normalizePath(route)
	m.mu.RLock()
	defer m.mu.RUnlock()
	if h, ok := m.byRouteHistogram[route]; ok {
		return h.Snapshot()
	}
	return DurationPercentiles{}
}

func (s MetricsSnapshot) MarshalJSON() ([]byte, error) {
	type jsonSnapshot struct {
		TotalRequests  int64                    `json:"total_requests"`
		TotalErrors    int64                    `json:"total_errors"`
		TotalLatency   time.Duration            `json:"total_latency"`
		AvgLatency     time.Duration            `json:"avg_latency"`
		ByMethod       map[string]int64         `json:"by_method"`
		ByRoute        map[string]int64         `json:"by_route"`
		ByStatus       map[int]int64            `json:"by_status"`
		ByRouteLatency map[string]time.Duration `json:"by_route_latency"`
	}
	payload := jsonSnapshot{
		TotalRequests:  s.TotalRequests,
		TotalErrors:    s.TotalErrors,
		TotalLatency:   s.TotalLatency,
		AvgLatency:     s.AvgLatency,
		ByMethod:       s.ByMethod,
		ByRoute:        s.ByRoute,
		ByStatus:       s.ByStatus,
		ByRouteLatency: s.ByRouteLatency,
	}
	if payload.ByMethod == nil {
		payload.ByMethod = map[string]int64{}
	}
	if payload.ByRoute == nil {
		payload.ByRoute = map[string]int64{}
	}
	if payload.ByStatus == nil {
		payload.ByStatus = map[int]int64{}
	}
	if payload.ByRouteLatency == nil {
		payload.ByRouteLatency = map[string]time.Duration{}
	}
	return json.Marshal(payload)
}

type statusRecorder struct {
	http.ResponseWriter
	status    int
	wroteCode bool
}

func (sr *statusRecorder) WriteHeader(code int) {
	if sr == nil {
		return
	}
	if !sr.wroteCode {
		sr.status = code
		sr.wroteCode = true
	}
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(data []byte) (int, error) {
	if sr == nil {
		return 0, nil
	}
	if !sr.wroteCode {
		sr.status = http.StatusOK
		sr.wroteCode = true
	}
	return sr.ResponseWriter.Write(data)
}

// Flush forwards to the underlying writer when it supports flushing, so
// streaming responses (SSE — including the HMR hub — and chunked encoding)
// keep working when metrics/logging middleware wraps the handler.
func (sr *statusRecorder) Flush() {
	if f, ok := sr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController and middleware chains reach the
// original writer for capabilities the wrapper does not implement
// (Hijacker for WebSocket upgrades, ReadFrom optimizations).
func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

// statusRecorderPool recycles per-request response wrappers used by the
// metrics/logging/health middleware, avoiding one allocation per
// request on instrumented servers.
var statusRecorderPool = sync.Pool{
	New: func() any { return new(statusRecorder) },
}

func newStatusRecorder(w http.ResponseWriter) *statusRecorder {
	sr, _ := statusRecorderPool.Get().(*statusRecorder)
	if w == nil {
		*sr = statusRecorder{status: http.StatusOK}
		return sr
	}
	*sr = statusRecorder{ResponseWriter: w, status: http.StatusOK}
	return sr
}

// releaseStatusRecorder returns the wrapper to the pool after the request
// completes. Callers must not use the recorder afterwards.
func releaseStatusRecorder(sr *statusRecorder) {
	if sr == nil {
		return
	}
	sr.ResponseWriter = nil
	statusRecorderPool.Put(sr)
}

func (sr *statusRecorder) StatusCode() int {
	if sr == nil || sr.status == 0 {
		return http.StatusOK
	}
	return sr.status
}
