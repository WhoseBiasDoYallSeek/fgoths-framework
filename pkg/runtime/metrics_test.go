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
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMetricsRecordAndSnapshot(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/foo", 200, 10*time.Millisecond)
	m.Record("GET", "/foo", 200, 20*time.Millisecond)
	m.Record("POST", "/foo", 500, 5*time.Millisecond)

	snap := m.Snapshot()
	if snap.TotalRequests != 3 {
		t.Errorf("TotalRequests = %d, want 3", snap.TotalRequests)
	}
	if snap.TotalErrors != 1 {
		t.Errorf("TotalErrors = %d, want 1", snap.TotalErrors)
	}
	if snap.ByMethod["GET"] != 2 {
		t.Errorf("ByMethod[GET] = %d, want 2", snap.ByMethod["GET"])
	}
	if snap.ByStatus[500] != 1 {
		t.Errorf("ByStatus[500] = %d, want 1", snap.ByStatus[500])
	}
}

func TestMetricsRecordNilSafe(t *testing.T) {
	var m *Metrics
	m.Record("GET", "/", 200) // must not panic
}

func TestMetricsRecordNormalizes(t *testing.T) {
	m := NewMetrics()
	m.Record("  get ", "/bar/", 200)
	snap := m.Snapshot()
	if snap.ByMethod["GET"] != 1 {
		t.Errorf("expected method to be uppercased and trimmed, got %v", snap.ByMethod)
	}
	if snap.ByRoute["/bar"] != 1 {
		t.Errorf("expected route to be normalized, got %v", snap.ByRoute)
	}
}

func TestMetricsRecordEmptyMethodDefaultsToGET(t *testing.T) {
	m := NewMetrics()
	m.Record("", "/", 200)
	snap := m.Snapshot()
	if snap.ByMethod[http.MethodGet] != 1 {
		t.Errorf("expected GET fallback, got %v", snap.ByMethod)
	}
}

func TestMetricsRecordEmptyRouteDefaultsToSlash(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "", 200)
	snap := m.Snapshot()
	if snap.ByRoute["/"] != 1 {
		t.Errorf("expected / fallback, got %v", snap.ByRoute)
	}
}

func TestMetricsRecordClampsNegativeDuration(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/", 200, -5*time.Second)
	snap := m.Snapshot()
	if snap.TotalLatency != 0 {
		t.Errorf("expected 0 latency after negative clamp, got %v", snap.TotalLatency)
	}
}

func TestMetricsSnapshotNilSafe(t *testing.T) {
	var m *Metrics
	snap := m.Snapshot()
	if snap.ByMethod == nil || snap.ByRoute == nil || snap.ByStatus == nil || snap.ByRouteLatency == nil {
		t.Errorf("nil receiver must return initialized maps: %+v", snap)
	}
}

func TestMetricsSnapshotMarshalJSON(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/", 200, time.Millisecond)
	data, err := json.Marshal(m.Snapshot())
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON")
	}
}

func TestStatusRecorderDefaultStatus(t *testing.T) {
	sr := newStatusRecorder(nil)
	if got := sr.StatusCode(); got != http.StatusOK {
		t.Errorf("default status = %d, want %d", got, http.StatusOK)
	}
}

func TestStatusRecorderWriteSetsStatus(t *testing.T) {
	rec := &recordingResponseWriter{header: http.Header{}}
	sr := newStatusRecorder(rec)
	if _, err := sr.Write([]byte("body")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := sr.StatusCode(); got != http.StatusOK {
		t.Errorf("status after Write = %d, want %d", got, http.StatusOK)
	}
}

func TestStatusRecorderWriteHeader(t *testing.T) {
	rec := &recordingResponseWriter{header: http.Header{}}
	sr := newStatusRecorder(rec)
	sr.WriteHeader(http.StatusTeapot)
	if got := sr.StatusCode(); got != http.StatusTeapot {
		t.Errorf("status = %d, want %d", got, http.StatusTeapot)
	}
}

func TestStatusRecorderNilSafe(t *testing.T) {
	var sr *statusRecorder
	sr.WriteHeader(200)
	_, _ = sr.Write([]byte("x"))
}

type recordingResponseWriter struct {
	header http.Header
	body   []byte
	status int
}

func (r *recordingResponseWriter) Header() http.Header { return r.header }
func (r *recordingResponseWriter) Write(b []byte) (int, error) {
	r.body = append(r.body, b...)
	return len(b), nil
}
func (r *recordingResponseWriter) WriteHeader(statusCode int) { r.status = statusCode }

// ─── Histogram and percentiles ────────────────────────────────────────────────

func TestRouteHistogramRecordsAndComputesPercentiles(t *testing.T) {
	h := newRouteHistogram(100)

	// Record known latencies: 10 samples from 1ms to 10ms
	for i := 1; i <= 10; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}

	p := h.Snapshot()
	// p50 should be around 5-6ms for 10 sorted samples
	if p.P50 == 0 {
		t.Error("expected non-zero P50 after recording samples")
	}
	if p.P95 == 0 {
		t.Error("expected non-zero P95 after recording samples")
	}
	if p.P99 == 0 {
		t.Error("expected non-zero P99 after recording samples")
	}
	// P50 should be <= P95 <= P99
	if p.P50 > p.P95 {
		t.Errorf("P50 (%v) should be <= P95 (%v)", p.P50, p.P95)
	}
	if p.P95 > p.P99 {
		t.Errorf("P95 (%v) should be <= P99 (%v)", p.P95, p.P99)
	}
}

func TestRouteHistogramEvictsOldSamples(t *testing.T) {
	h := newRouteHistogram(20)

	// Record 30 samples — oldest 10% should be evicted
	for i := 1; i <= 30; i++ {
		h.Record(time.Duration(i) * time.Millisecond)
	}

	// After 30 samples with capacity 20, we should have 20 samples.
	// The first 3 (oldest 10% of 30) should have been evicted.
	// So we should have samples from ~4ms to 30ms.
	p := h.Snapshot()
	if p.P50 == 0 {
		t.Error("expected non-zero P50 after eviction")
	}
}

func TestRouteHistogramNilReceiverSnapshot(t *testing.T) {
	var h *RouteHistogram
	p := h.Snapshot()
	if p.P50 != 0 || p.P95 != 0 || p.P99 != 0 {
		t.Errorf("nil histogram should return zero percentiles, got %+v", p)
	}
}

func TestRouteHistogramClampsNegativeDuration(t *testing.T) {
	h := newRouteHistogram(100)
	h.Record(-5 * time.Second)
	h.Record(0)
	p := h.Snapshot()
	// Should not panic and should have reasonable values
	if p.P50 < 0 {
		t.Errorf("P50 should not be negative, got %v", p.P50)
	}
}

func TestMetricsGlobalPercentiles(t *testing.T) {
	m := NewMetrics()
	for i := 1; i <= 20; i++ {
		m.Record("GET", "/test", 200, time.Duration(i)*time.Millisecond)
	}

	gp := m.GlobalPercentiles()
	if gp.P50 == 0 {
		t.Error("expected non-zero global P50")
	}
	if gp.P95 == 0 {
		t.Error("expected non-zero global P95")
	}
	if gp.P99 == 0 {
		t.Error("expected non-zero global P99")
	}
}

func TestMetricsGlobalPercentilesNilMetrics(t *testing.T) {
	var m *Metrics
	gp := m.GlobalPercentiles()
	if gp.P50 != 0 || gp.P95 != 0 || gp.P99 != 0 {
		t.Errorf("nil metrics should return zero percentiles, got %+v", gp)
	}
}

func TestMetricsRoutePercentiles(t *testing.T) {
	m := NewMetrics()
	for i := 1; i <= 15; i++ {
		m.Record("GET", "/api/users", 200, time.Duration(i)*time.Millisecond)
	}

	rp := m.RoutePercentiles("/api/users")
	if rp.P50 == 0 {
		t.Error("expected non-zero P50 for /api/users")
	}
	if rp.P95 == 0 {
		t.Error("expected non-zero P95 for /api/users")
	}
	if rp.P99 == 0 {
		t.Error("expected non-zero P99 for /api/users")
	}
}

func TestMetricsRoutePercentilesNormalizesPath(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/api/users/", 200, 5*time.Millisecond)

	// RoutePercentiles should normalize the path
	rp := m.RoutePercentiles("/api/users")
	if rp.P50 == 0 {
		t.Error("expected non-zero P50 after path normalization")
	}
}

func TestMetricsRoutePercentilesUnknownRoute(t *testing.T) {
	m := NewMetrics()
	m.Record("GET", "/api/users", 200, 5*time.Millisecond)

	rp := m.RoutePercentiles("/unknown")
	// Unknown route should return zero percentiles
	if rp.P50 != 0 || rp.P95 != 0 || rp.P99 != 0 {
		t.Errorf("expected zero percentiles for unknown route, got %+v", rp)
	}
}

func TestMetricsRoutePercentilesNilMetrics(t *testing.T) {
	var m *Metrics
	rp := m.RoutePercentiles("/any")
	if rp.P50 != 0 || rp.P95 != 0 || rp.P99 != 0 {
		t.Errorf("nil metrics should return zero percentiles, got %+v", rp)
	}
}

func TestMetricsSnapshotIncludesPercentiles(t *testing.T) {
	m := NewMetrics()
	for i := 1; i <= 10; i++ {
		m.Record("GET", "/test", 200, time.Duration(i)*time.Millisecond)
	}

	snap := m.Snapshot()
	if snap.ByRoutePercentiles == nil {
		t.Error("expected ByRoutePercentiles to be non-nil")
	}
	if len(snap.ByRoutePercentiles) == 0 {
		t.Error("expected ByRoutePercentiles to contain /test entry")
	}
	p, ok := snap.ByRoutePercentiles["/test"]
	if !ok {
		t.Error("expected /test in ByRoutePercentiles")
	}
	if p.P50 == 0 || p.P95 == 0 || p.P99 == 0 {
		t.Errorf("expected non-zero percentiles in snapshot, got %+v", p)
	}
}

// TestStatusRecorderSupportsFlushAndUnwrap verifies that the metrics wrapper
// preserves streaming (Flusher) and capability discovery (Unwrap) — SSE/HMR
// must keep working when metrics middleware is enabled.
func TestStatusRecorderSupportsFlushAndUnwrap(t *testing.T) {
	flushed := false
	base := &flushRecorder{ResponseWriter: httptest.NewRecorder(), flushed: &flushed}
	sr := newStatusRecorder(base)

	if _, ok := any(sr).(http.Flusher); !ok {
		t.Fatal("statusRecorder must implement http.Flusher")
	}
	sr.Flush()
	if !flushed {
		t.Error("Flush must forward to the underlying writer")
	}
	if got := sr.Unwrap(); got != http.ResponseWriter(base) {
		t.Error("Unwrap must return the underlying writer")
	}
}

type flushRecorder struct {
	http.ResponseWriter
	flushed *bool
}

func (f *flushRecorder) Flush() { *f.flushed = true }

// TestServerSSEWithMetricsEnabled is the end-to-end guard: an SSE handler
// must stream (Flush visible to the client) even with metrics middleware on.
func TestServerSSEWithMetricsEnabled(t *testing.T) {
	srv := NewServer(":0")
	srv.WithMetrics()
	srv.Get("/events", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: hello\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "data: hello") {
		t.Errorf("SSE body %q missing event", body)
	}
}

// TestMetricsBoundedCardinality is the guard against unbounded metric
// cardinality: requests to a param route must aggregate under the route
// pattern ("/users/{id}"), not under each raw path.
func TestMetricsBoundedCardinality(t *testing.T) {
	srv := NewServer(":0")
	srv.WithMetrics()
	srv.Get("/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	for i := 0; i < 50; i++ {
		resp, err := ts.Client().Get(fmt.Sprintf("%s/users/%d", ts.URL, i))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	snap := srv.metrics.Snapshot()
	if got := len(snap.ByRoute); got != 1 {
		t.Errorf("ByRoute has %d entries, want 1 (aggregated under /users/{id}): %v", got, snap.ByRoute)
	}
	if _, ok := snap.ByRoute["/users/{id}"]; !ok {
		t.Errorf("ByRoute missing /users/{id}: %v", snap.ByRoute)
	}
}
