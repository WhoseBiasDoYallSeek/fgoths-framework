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
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestServerWithOpenTelemetry(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)

	server := NewServer(":0").WithOpenTelemetry("fgoths-test")
	server.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		span := oteltrace.SpanFromContext(r.Context())
		if !span.IsRecording() {
			t.Fatal("expected active span in request context")
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestServerWithRequestID(t *testing.T) {
	server := NewServer(":0").WithRequestID().WithOpenTelemetry("fgoths-test")
	server.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromRequest(r)
		if id == "" {
			t.Fatal("expected request ID to be attached to the request context")
		}
		w.Header().Set("X-Trace-ID", id)
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health", nil)
	req.Header.Set("X-Request-ID", "trace-123")
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)
	if got := res.Header().Get("X-Request-ID"); got != "trace-123" {
		t.Fatalf("expected request ID header to be propagated, got %q", got)
	}
	if got := res.Header().Get("X-Trace-ID"); got != "trace-123" {
		t.Fatalf("expected trace ID to match request ID, got %q", got)
	}
}

func TestProxyWithOpenTelemetry(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithOpenTelemetry("fgoths-proxy")

	req := httptest.NewRequest(http.MethodGet, "http://example.com/api/test", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
}

func TestServerWithMetrics(t *testing.T) {
	server := NewServer(":0").WithMetrics()
	server.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/metrics", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	metrics := server.Metrics()
	if metrics == nil {
		t.Fatal("expected metrics collector to be registered")
	}
	snapshot := metrics.Snapshot()
	if snapshot.TotalRequests != 1 {
		t.Fatalf("expected 1 request counted, got %d", snapshot.TotalRequests)
	}
	if snapshot.ByMethod[http.MethodGet] != 1 {
		t.Fatalf("expected GET count to be 1, got %d", snapshot.ByMethod[http.MethodGet])
	}
	if snapshot.ByRoute["/metrics"] != 1 {
		t.Fatalf("expected /metrics count to be 1, got %d", snapshot.ByRoute["/metrics"])
	}
}

func TestProxyWithMetrics(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("metric-ok"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithMetrics()

	req := httptest.NewRequest(http.MethodGet, "http://example.com/metric", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.Code)
	}
	metrics := proxy.Metrics()
	if metrics == nil {
		t.Fatal("expected proxy metrics collector to be registered")
	}
	snapshot := metrics.Snapshot()
	if snapshot.TotalRequests != 1 {
		t.Fatalf("expected 1 proxy request counted, got %d", snapshot.TotalRequests)
	}
	if snapshot.ByStatus[http.StatusCreated] != 1 {
		t.Fatalf("expected status 201 count to be 1, got %d", snapshot.ByStatus[http.StatusCreated])
	}
}

func TestMetricsSnapshotTracksLatency(t *testing.T) {
	server := NewServer(":0").WithMetrics()
	server.Get("/latency", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/latency", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}

	snapshot := server.Metrics().Snapshot()
	if snapshot.TotalRequests != 1 {
		t.Fatalf("expected 1 request counted, got %d", snapshot.TotalRequests)
	}
	if snapshot.TotalLatency <= 0 {
		t.Fatalf("expected a positive total latency, got %s", snapshot.TotalLatency)
	}
	if snapshot.AvgLatency <= 0 {
		t.Fatalf("expected a positive average latency, got %s", snapshot.AvgLatency)
	}
	if snapshot.ByRouteLatency["/latency"] <= 0 {
		t.Fatalf("expected route latency for /latency to be recorded, got %s", snapshot.ByRouteLatency["/latency"])
	}
}

func TestServerWithMetricsEndpoint(t *testing.T) {
	server := NewServer(":0").WithMetrics().WithMetricsEndpoint("/metrics")
	server.Get("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/ping", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)
	if res.Code != http.StatusNoContent {
		t.Fatalf("expected 204 from ping, got %d", res.Code)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "http://example.com/metrics", nil)
	metricsRes := httptest.NewRecorder()
	server.Handler.ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from metrics endpoint, got %d", metricsRes.Code)
	}
	if !strings.Contains(metricsRes.Body.String(), "\"total_requests\":1") {
		t.Fatalf("expected metrics snapshot in JSON output, got %s", metricsRes.Body.String())
	}
}

func TestTraceMiddlewareStartsSpanForRequest(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)

	var sawSpan bool
	handler := TraceMiddleware("fgoths-test")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := SpanFromRequest(r)
		sawSpan = span.IsRecording()
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/orders", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if !sawSpan {
		t.Fatal("expected TraceMiddleware to start a recording span")
	}
}

func TestTraceMiddlewareDefaultsServiceNameAndHandlesNilRequest(t *testing.T) {
	handler := TraceMiddleware("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health", nil)
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 with default service name, got %d", res.Code)
	}
}

func TestSpanFromRequestNilRequest(t *testing.T) {
	span := SpanFromRequest(nil)
	if span.IsRecording() {
		t.Fatal("expected a non-recording span for a nil request")
	}
}

func TestSchemeFromRequestBranches(t *testing.T) {
	if got := schemeFromRequest(nil); got != "http" {
		t.Fatalf("schemeFromRequest(nil) = %q, want http", got)
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.com/x", nil)
	if got := schemeFromRequest(req); got != "http" {
		t.Fatalf("schemeFromRequest(plain) = %q, want http", got)
	}
	if got := statusCode(nil); got != 0 {
		t.Fatalf("statusCode(nil) = %d, want 0", got)
	}
	if got := statusCode(&http.Response{StatusCode: http.StatusTeapot}); got != http.StatusTeapot {
		t.Fatalf("statusCode(resp) = %d, want %d", got, http.StatusTeapot)
	}
}

func TestProxyWithOpenTelemetryWrapsObserver(t *testing.T) {
	provider := sdktrace.NewTracerProvider()
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(old)

	var sawWrapped bool
	proxy, err := NewProxy("http://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithObserver(func(req *http.Request, resp *http.Response, elapsed time.Duration) {
		sawWrapped = true
	})
	proxy.WithOpenTelemetry("fgoths-test")

	req := httptest.NewRequest(http.MethodGet, "http://example.com/orders", nil)
	resp := &http.Response{StatusCode: http.StatusOK}
	proxy.observer(req, resp, time.Millisecond)
	if !sawWrapped {
		t.Fatal("expected WithOpenTelemetry to preserve the previously registered observer")
	}

	proxy2, _ := NewProxy("http://upstream.internal")
	proxy2.WithOpenTelemetry("fgoths-test")
	proxy2.observer(nil, nil, 0) // nil request must not panic
}

func TestRequestIDGenerationAndReuse(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "incoming-id")
	if got := ensureRequestID(req); got != "incoming-id" {
		t.Fatalf("expected the inbound request ID to be reused, got %q", got)
	}

	fresh := httptest.NewRequest(http.MethodGet, "/x", nil)
	generated := ensureRequestID(fresh)
	if generated == "" {
		t.Fatal("expected a request ID to be generated")
	}
	for _, header := range []string{"X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if got := fresh.Header.Get(header); got != generated {
			t.Fatalf("expected header %s to carry the generated ID, got %q", header, got)
		}
	}
	if got := newRequestID(); got == "" {
		t.Fatal("expected newRequestID to return a non-empty ID")
	}
}
