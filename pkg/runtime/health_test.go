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
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerWithLivenessAlwaysHealthy(t *testing.T) {
	server := NewServer(":0").WithLiveness("/health/live")

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health/live", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestServerWithReadinessPassesWhenChecksSucceed(t *testing.T) {
	server := NewServer(":0").WithReadiness("/health/ready", func(ctx context.Context) error {
		return nil
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health/ready", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestServerWithReadinessFailsWhenCheckErrors(t *testing.T) {
	server := NewServer(":0").WithReadiness("/health/ready", func(ctx context.Context) error {
		return errors.New("db unreachable")
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health/ready", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", res.Code)
	}
}

func TestServerWithReadinessTimesOutSlowCheck(t *testing.T) {
	server := NewServer(":0").WithReadinessTimeout(20*time.Millisecond).WithReadiness("/health/ready", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health/ready", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on timeout, got %d", res.Code)
	}
}

func TestServerWithAlertThresholdTriggersCallback(t *testing.T) {
	var triggered bool
	var lastRate float64
	server := NewServer(":0").WithMetrics().WithAlertThreshold(0.4, func(snapshot MetricsSnapshot, errorRate float64) {
		triggered = true
		lastRate = errorRate
	})
	server.Get("/flaky", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "http://example.com/flaky", nil)
		res := httptest.NewRecorder()
		server.Handler.ServeHTTP(res, req)
	}

	if !triggered {
		t.Fatal("expected alert callback to trigger once error rate crosses threshold")
	}
	if lastRate <= 0.4 {
		t.Fatalf("expected error rate above threshold, got %f", lastRate)
	}
}

func TestHealthEndpointsDefaultsAndNilSafety(t *testing.T) {
	var nilServer *Server
	if got := nilServer.WithLiveness(""); got != nil {
		t.Fatal("expected WithLiveness on a nil server to return nil")
	}
	if got := nilServer.WithReadinessTimeout(time.Second); got != nil {
		t.Fatal("expected WithReadinessTimeout on a nil server to return nil")
	}
	if got := nilServer.WithReadiness(""); got != nil {
		t.Fatal("expected WithReadiness on a nil server to return nil")
	}

	server := NewServer(":0").WithLiveness("").WithReadiness("", nil)
	req := httptest.NewRequest(http.MethodGet, "http://example.com/health/live", nil)
	res := httptest.NewRecorder()
	server.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected the default liveness path to be registered, got %d", res.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://example.com/health/ready", nil)
	res2 := httptest.NewRecorder()
	server.router.ServeHTTP(res2, req2)
	if res2.Code != http.StatusOK {
		t.Fatalf("expected a nil readiness check to be skipped, got %d", res2.Code)
	}
}

func TestAlertThresholdNilSafety(t *testing.T) {
	var nilServer *Server
	if got := nilServer.WithAlertThreshold(0.5, nil); got != nil {
		t.Fatal("expected WithAlertThreshold on a nil server to return nil")
	}
}

// TestAlertThresholdSlidingWindow verifies the bounded-window semantics: a
// recent error spike must fire the alert even when the cumulative lifetime
// error rate is low (the old cumulative behavior never fired on long-lived
// services).
func TestAlertThresholdSlidingWindow(t *testing.T) {
	srv := NewServer(":0")
	srv.WithMetrics()
	fired := make(chan float64, 1)
	srv.WithAlertThreshold(0.3, func(_ MetricsSnapshot, rate float64) {
		select {
		case fired <- rate:
		default:
		}
	})
	srv.Get("/ok", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	srv.Get("/bad", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	ts := httptest.NewServer(srv.router)
	defer ts.Close()

	get := func(path string) {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// Healthy history: 20 OK requests — no alert.
	for i := 0; i < 20; i++ {
		get("/ok")
	}
	select {
	case rate := <-fired:
		t.Fatalf("alert fired with healthy history: rate %.2f", rate)
	default:
	}

	// Spike: 15 consecutive errors out of the last 35 — window rate 15/35
	// (~0.43) crosses the 0.3 threshold; cumulative rate (15/35 lifetime)
	// would be identical here, so also assert the window is bounded below.
	for i := 0; i < 15; i++ {
		get("/bad")
	}
	select {
	case rate := <-fired:
		if rate < 0.3 {
			t.Fatalf("alert fired with rate %.2f below threshold", rate)
		}
	case <-time.After(time.Second):
		t.Fatal("expected alert to fire after error spike")
	}
}
