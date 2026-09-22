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
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRouterDispatch(t *testing.T) {
	r := NewRouter()
	r.Get("/health", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	res := httptest.NewRecorder()

	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	if got := res.Body.String(); got != "ok" {
		t.Fatalf("expected ok body, got %q", got)
	}
}

func TestRouterHTTPMethodHelpers(t *testing.T) {
	r := NewRouter()
	echo := func(label string) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(label))
		}
	}

	r.Post("/items", echo("post"))
	r.Put("/items", echo("put"))
	r.Delete("/items", echo("delete"))
	r.Patch("/items", echo("patch"))
	r.Any("/anything", echo("any"))

	cases := []struct {
		method string
		path   string
		want   string
	}{
		{http.MethodPost, "/items", "post"},
		{http.MethodPut, "/items", "put"},
		{http.MethodDelete, "/items", "delete"},
		{http.MethodPatch, "/items", "patch"},
		{http.MethodGet, "/anything", "any"},
		{http.MethodPost, "/anything", "any"},
		{http.MethodHead, "/anything", "any"},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s %s: expected 200, got %d", tc.method, tc.path, res.Code)
		}
		if got := res.Body.String(); got != tc.want {
			t.Fatalf("%s %s: expected body %q, got %q", tc.method, tc.path, tc.want, got)
		}
	}
}

func TestServerPostAndAnyDelegateToRouter(t *testing.T) {
	s := NewServer(":0")
	s.Post("/orders", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	s.Any("/ping", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	postReq := httptest.NewRequest(http.MethodPost, "/orders", nil)
	postRes := httptest.NewRecorder()
	s.router.ServeHTTP(postRes, postReq)
	if postRes.Code != http.StatusCreated {
		t.Fatalf("expected 201 from Post handler, got %d", postRes.Code)
	}

	anyReq := httptest.NewRequest(http.MethodDelete, "/ping", nil)
	anyRes := httptest.NewRecorder()
	s.router.ServeHTTP(anyRes, anyReq)
	if anyRes.Code != http.StatusOK {
		t.Fatalf("expected 200 from Any handler, got %d", anyRes.Code)
	}
}

func TestProxyCircuitBreakerRecordSuccessResetsFailures(t *testing.T) {
	cb := newProxyCircuitBreaker(2, time.Minute, 15*time.Second)
	cb.recordFailure()
	cb.recordFailure()
	if cb.allow() {
		t.Fatal("expected circuit breaker to be open after reaching the failure threshold")
	}

	cb.openUntil = time.Time{}
	cb.recordSuccess()
	if cb.failures != 0 {
		t.Fatalf("expected recordSuccess to reset failures, got %d", cb.failures)
	}
	if !cb.allow() {
		t.Fatal("expected circuit breaker to allow requests after recordSuccess")
	}
}

func TestProxyCircuitBreakerNilSafety(t *testing.T) {
	var cb *proxyCircuitBreaker
	if !cb.allow() {
		t.Fatal("expected nil circuit breaker to always allow")
	}
	cb.recordSuccess() // must not panic
	cb.recordFailure() // must not panic
}

func TestNewProxyCircuitBreakerDefaults(t *testing.T) {
	if cb := newProxyCircuitBreaker(0, time.Second, time.Second); cb != nil {
		t.Fatal("expected a non-positive threshold to disable the circuit breaker")
	}
	cb := newProxyCircuitBreaker(1, 0, 0)
	if cb.window != time.Minute {
		t.Fatalf("expected default window of 1 minute, got %v", cb.window)
	}
	if cb.openFor != 15*time.Second {
		t.Fatalf("expected default openFor of 15s, got %v", cb.openFor)
	}
}

func TestProxyRateLimiterNilSafetyAndDefaults(t *testing.T) {
	var rl *proxyRateLimiter
	if !rl.allow() {
		t.Fatal("expected nil rate limiter to always allow")
	}
	if rl := newProxyRateLimiter(0, 5); rl != nil {
		t.Fatal("expected non-positive requestsPerSecond to disable the rate limiter")
	}
	rl2 := newProxyRateLimiter(5, 0)
	if rl2.burst != 5 {
		t.Fatalf("expected burst to default to the requests-per-second value, got %v", rl2.burst)
	}
}

func TestRouterNilSafety(t *testing.T) {
	var r *Router
	r.Use(func(next http.Handler) http.Handler { return next }) // must not panic
	r.Handle(http.MethodGet, "/x", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {}))

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected nil router to 404, got %d", res.Code)
	}

	if _, _, ok := r.matchRoute(http.MethodGet, "/x"); ok {
		t.Fatal("expected matchRoute on a nil router to report no match")
	}
}

func TestRouterHandleIgnoresNilHandler(t *testing.T) {
	r := NewRouter()
	r.Handle(http.MethodGet, "/x", nil)
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a nil handler registration, got %d", res.Code)
	}
}

func TestRouterReleasesLockBeforeCallingHandler(t *testing.T) {
	r := NewRouter()
	done := make(chan struct{})
	r.Get("/register", func(w http.ResponseWriter, req *http.Request) {
		r.Get("/new", func(http.ResponseWriter, *http.Request) {})
		close(done)
		w.WriteHeader(http.StatusNoContent)
	})

	go func() {
		r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/register", nil))
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler could not register a route while serving a request")
	}
}

func TestRouterMatchRouteFallsBackToLongestWildcard(t *testing.T) {
	r := NewRouter()
	r.Get("/api/*", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("short"))
	})
	r.Get("/api/v1/*", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("long"))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/orders", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if got := res.Body.String(); got != "long" {
		t.Fatalf("expected the longest matching wildcard route to win, got %q", got)
	}
}

func TestServerNilSafety(t *testing.T) {
	var s *Server
	if got := s.WithMetrics(); got != nil {
		t.Fatal("expected WithMetrics on a nil server to return nil")
	}
	if got := s.WithLogger(nil); got != nil {
		t.Fatal("expected WithLogger on a nil server to return nil")
	}
	if got := s.Metrics(); got != nil {
		t.Fatal("expected Metrics on a nil server to return nil")
	}
	if got := s.WithMetricsEndpoint(""); got != nil {
		t.Fatal("expected WithMetricsEndpoint on a nil server to return nil")
	}
	s.Handle(http.MethodGet, "/x", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})) // must not panic
	s.Use()                                                                                           // must not panic
	s.Mount("/x", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))                  // must not panic
	s.Any("/x", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))                    // must not panic
	if err := (&Server{}).Proxy("/x", "http://example.com"); err == nil {
		t.Fatal("expected Proxy on a server with an uninitialized router to fail")
	}
}

func TestServerMountIgnoresNilHandler(t *testing.T) {
	s := NewServer(":0")
	s.Mount("/internal", nil) // must not panic and register nothing
	req := httptest.NewRequest(http.MethodGet, "/internal", nil)
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected no route to be registered for a nil mount handler, got %d", res.Code)
	}
}

func TestServerWithMetricsEndpointDefaultPath(t *testing.T) {
	s := NewServer(":0").WithMetricsEndpoint("")
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected default /metrics path to be registered, got %d", res.Code)
	}
}

func TestServerProxyRejectsInvalidTarget(t *testing.T) {
	s := NewServer(":0")
	if err := s.Proxy("/upstream", ""); err == nil {
		t.Fatal("expected Proxy to reject an empty target")
	}
}

func TestProxyNilSafety(t *testing.T) {
	var p *Proxy
	if got := p.WithPathPrefix("/x"); got != nil {
		t.Fatal("expected WithPathPrefix on a nil proxy to return nil")
	}
	if got := p.WithStripPrefix("/x"); got != nil {
		t.Fatal("expected WithStripPrefix on a nil proxy to return nil")
	}
	if got := p.WithHeader("k", "v"); got != nil {
		t.Fatal("expected WithHeader on a nil proxy to return nil")
	}
	if got := p.WithHealthCheck("/health", time.Second); got != nil {
		t.Fatal("expected WithHealthCheck on a nil proxy to return nil")
	}
	if got := p.WithFailover("http://example.com"); got != nil {
		t.Fatal("expected WithFailover on a nil proxy to return nil")
	}
	if got := p.WithObserver(nil); got != nil {
		t.Fatal("expected WithObserver on a nil proxy to return nil")
	}
	if got := p.WithLogger(nil); got != nil {
		t.Fatal("expected WithLogger on a nil proxy to return nil")
	}
	if got := p.WithMetrics(); got != nil {
		t.Fatal("expected WithMetrics on a nil proxy to return nil")
	}
	if got := p.Metrics(); got != nil {
		t.Fatal("expected Metrics on a nil proxy to return nil")
	}
	if got := p.WithRetry(1, time.Second); got != nil {
		t.Fatal("expected WithRetry on a nil proxy to return nil")
	}
	if got := p.WithRateLimit(1, 1); got != nil {
		t.Fatal("expected WithRateLimit on a nil proxy to return nil")
	}
	if got := p.WithCircuitBreaker(1, time.Second, time.Second); got != nil {
		t.Fatal("expected WithCircuitBreaker on a nil proxy to return nil")
	}
	if got := p.WithRewrite("/a", "/b"); got != nil {
		t.Fatal("expected WithRewrite on a nil proxy to return nil")
	}
	if got := p.WithTimeout(time.Second); got != nil {
		t.Fatal("expected WithTimeout on a nil proxy to return nil")
	}
	if got := p.TargetURL(); got != "" {
		t.Fatalf("expected TargetURL on a nil proxy to be empty, got %q", got)
	}
	if !p.refreshHealthStatus() {
		t.Fatal("expected refreshHealthStatus on a nil proxy or empty health path to report healthy")
	}

	res := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	p.ServeHTTP(res, req)
	if res.Code != http.StatusNotFound {
		t.Fatalf("expected nil proxy to 404, got %d", res.Code)
	}
}

func TestProxyWithFailoverRejectsInvalidTarget(t *testing.T) {
	proxy, err := NewProxy("http://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	if got := proxy.WithFailover(""); got != proxy {
		t.Fatal("expected WithFailover with an empty target to be a no-op returning the same proxy")
	}
	if got := proxy.WithFailover("://not a url"); got != proxy {
		t.Fatal("expected WithFailover with an invalid target to be a no-op returning the same proxy")
	}
}

func TestProxyWithTimeoutAndRetryCombination(t *testing.T) {
	proxy, err := NewProxy("http://upstream.internal")
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithRetry(2, 10*time.Millisecond)
	proxy.WithTimeout(50 * time.Millisecond)
	if _, ok := proxy.reverseProxy.Transport.(*proxyRetryTransport); !ok {
		t.Fatalf("expected retry transport to wrap the timeout transport, got %T", proxy.reverseProxy.Transport)
	}
}

func TestNormalizePathVariants(t *testing.T) {
	cases := map[string]string{
		"":         "/",
		"foo":      "/foo",
		"/foo/":    "/foo",
		"/":        "/",
		"/foo/bar": "/foo/bar",
	}
	for input, want := range cases {
		if got := normalizePath(input); got != want {
			t.Errorf("normalizePath(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestWithWildcardRouteRoot(t *testing.T) {
	if got := withWildcardRoute("/"); got != "/*" {
		t.Fatalf("withWildcardRoute(\"/\") = %q, want \"/*\"", got)
	}
	if got := withWildcardRoute("/api"); got != "/api/*" {
		t.Fatalf("withWildcardRoute(\"/api\") = %q, want \"/api/*\"", got)
	}
}

func TestJoinProxyPathVariants(t *testing.T) {
	cases := []struct {
		name                                                        string
		base, incoming, strip, targetPrefix, rewriteFrom, rewriteTo string
		want                                                        string
	}{
		{name: "no options", base: "", incoming: "/orders", want: "/orders"},
		{name: "strip prefix", base: "", incoming: "/api/orders", strip: "/api", want: "/orders"},
		{name: "strip to empty becomes root", base: "", incoming: "/api", strip: "/api", want: "/"},
		{name: "rewrite exact match", base: "", incoming: "/old", rewriteFrom: "/old", rewriteTo: "/new", want: "/new"},
		{name: "rewrite prefix match", base: "", incoming: "/old/123", rewriteFrom: "/old", rewriteTo: "/new", want: "/new/123"},
		{name: "target prefix", base: "", incoming: "/orders", targetPrefix: "/svc", want: "/svc/orders"},
		{name: "base path with trailing slash", base: "/base/", incoming: "/orders", want: "/base/orders"},
		{name: "base path without trailing slash", base: "/base", incoming: "/orders", want: "/base/orders"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := joinProxyPath(tc.base, tc.incoming, tc.strip, tc.targetPrefix, tc.rewriteFrom, tc.rewriteTo)
			if got != tc.want {
				t.Fatalf("joinProxyPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewServerDefaultsAddrWhenEmpty(t *testing.T) {
	s := NewServer("")
	if s.Addr != ":8080" {
		t.Fatalf("expected default addr :8080, got %q", s.Addr)
	}
}

func TestProxyRetryTransportNilBaseUsesDefaultTransport(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	rt := &proxyRetryTransport{retries: 2}
	req := httptest.NewRequest(http.MethodGet, upstream.URL, nil)
	req.RequestURI = ""
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("expected RoundTrip to fall back to the default transport, got error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestApplyDirectorNilSafety(t *testing.T) {
	var p *Proxy
	p.applyDirector() // must not panic

	p2 := &Proxy{}
	p2.applyDirector() // must not panic without a reverseProxy/targetURL
}

func TestRefreshHealthStatusNilTargetURL(t *testing.T) {
	p := &Proxy{healthCheckPath: "/health"}
	if p.refreshHealthStatus() {
		t.Fatal("expected refreshHealthStatus to report unhealthy when targetURL is nil")
	}
}

func TestRefreshHealthStatusCachesWithinInterval(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithHealthCheck("/health", time.Minute)
	initialHits := hits
	if !proxy.refreshHealthStatus() {
		t.Fatal("expected upstream to be healthy")
	}
	if hits != initialHits {
		t.Fatalf("expected cached result within interval to avoid another probe, got %d additional hits", hits-initialHits)
	}
	if !proxy.refreshHealthStatus() {
		t.Fatal("expected cached healthy status to be returned")
	}
	if hits != initialHits {
		t.Fatalf("expected cached result within interval to avoid a second probe, got %d hits", hits)
	}
}

func TestServerRoundTripAllOptionsCombined(t *testing.T) {
	s := NewServer(":0").WithMetrics().WithMetricsEndpoint("/metrics")
	s.Get("/ok", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "http://example.com/ok", nil)
	res := httptest.NewRecorder()
	s.router.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "http://example.com/metrics", nil)
	metricsRes := httptest.NewRecorder()
	s.router.ServeHTTP(metricsRes, metricsReq)
	if metricsRes.Code != http.StatusOK {
		t.Fatalf("expected metrics endpoint 200, got %d", metricsRes.Code)
	}
}

func TestProxyForwardsRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("proxied"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/hello", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
	if got := res.Body.String(); got != "proxied" {
		t.Fatalf("expected proxied response, got %q", got)
	}
}

func TestRouterMatchesPrefixPath(t *testing.T) {
	r := NewRouter()
	r.Get("/api", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/users/42", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusTeapot {
		t.Fatalf("expected 418, got %d", res.Code)
	}
}

func TestRouterMatchesWildcardPrefix(t *testing.T) {
	r := NewRouter()
	r.Get("/api/*", func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("wildcard"))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.Code)
	}
	if got := res.Body.String(); got != "wildcard" {
		t.Fatalf("expected wildcard body, got %q", got)
	}
}

func TestServerProxyRoute(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/alpha" {
			t.Fatalf("expected upstream path /alpha, got %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("forwarded"))
	}))
	defer upstream.Close()

	server := NewServer(":0")
	if err := server.Proxy("/services", upstream.URL); err != nil {
		t.Fatalf("server.Proxy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/services/alpha", nil)
	res := httptest.NewRecorder()
	server.router.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.Code)
	}
	if got := res.Body.String(); got != "forwarded" {
		t.Fatalf("expected forwarded body, got %q", got)
	}
}

func TestProxyWithPathPrefixAndStripPrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/gateway/alpha" {
			t.Fatalf("expected upstream path /gateway/alpha, got %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("rewritten"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithPathPrefix("/gateway").WithStripPrefix("/services")

	req := httptest.NewRequest(http.MethodGet, "/services/alpha", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
	if got := res.Body.String(); got != "rewritten" {
		t.Fatalf("expected rewritten response, got %q", got)
	}
}

func TestProxyWithRewritePrefix(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/v2/customers/42" {
			t.Fatalf("expected upstream path /v2/customers/42, got %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("rewritten-v2"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithStripPrefix("/services").WithRewrite("/customers", "/v2/customers")

	req := httptest.NewRequest(http.MethodGet, "/services/customers/42", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.Code)
	}
	if got := res.Body.String(); got != "rewritten-v2" {
		t.Fatalf("expected rewritten-v2 body, got %q", got)
	}
}

func TestProxyAddsForwardedHeadersAndTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-App-ID"); got != "fgoths" {
			t.Fatalf("expected X-App-ID header, got %q", got)
		}
		if got := r.Header.Get("X-Forwarded-Proto"); got != "http" {
			t.Fatalf("expected X-Forwarded-Proto=http, got %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithHeader("X-App-ID", "fgoths").WithTimeout(2 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/internal", nil)
	req.RemoteAddr = "10.0.0.5:4567"
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.Code)
	}
}

func TestProxyObserverTracksRequestTiming(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("observed"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}

	var (
		gotMethod string
		gotPath   string
		gotStatus int
		gotTime   time.Duration
	)
	proxy.WithObserver(func(req *http.Request, resp *http.Response, took time.Duration) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		gotStatus = resp.StatusCode
		gotTime = took
	})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
	if gotMethod != http.MethodGet {
		t.Fatalf("expected observer method GET, got %q", gotMethod)
	}
	if gotPath != "/metrics" {
		t.Fatalf("expected observer path /metrics, got %q", gotPath)
	}
	if gotStatus != http.StatusAccepted {
		t.Fatalf("expected observer status 202, got %d", gotStatus)
	}
	if gotTime <= 0 {
		t.Fatalf("expected positive elapsed duration, got %s", gotTime)
	}
}

func TestServerWithStructuredLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	server := NewServer(":0").WithRequestID().WithLogger(logger)
	server.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	req := httptest.NewRequest(http.MethodGet, "http://example.com/health", nil)
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
	logLine := buf.String()
	if !strings.Contains(logLine, "GET") || !strings.Contains(logLine, "/health") || !strings.Contains(logLine, "status=200") {
		t.Fatalf("expected structured request log, got %q", logLine)
	}
}

func TestProxyWithStructuredLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("logged"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithLogger(logger)

	req := httptest.NewRequest(http.MethodPost, "http://example.com/ingest", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", res.Code)
	}
	logLine := buf.String()
	if !strings.Contains(logLine, "POST") || !strings.Contains(logLine, "/ingest") || !strings.Contains(logLine, "status=201") {
		t.Fatalf("expected structured proxy log, got %q", logLine)
	}
}

func TestProxyRetryOnTransientFailure(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("retry me"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("recovered"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithRetry(2, 10*time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/retry", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", res.Code)
	}
	if got := res.Body.String(); got != "recovered" {
		t.Fatalf("expected recovered body, got %q", got)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", attempts.Load())
	}
}

func TestProxyRetryPreservesRequestBody(t *testing.T) {
	var attempts atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if string(body) != "payload" {
			t.Errorf("attempt %d received body %q, want payload", attempts.Load()+1, body)
		}
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithRetry(1, 0)

	req := httptest.NewRequest(http.MethodPost, "/retry", strings.NewReader("payload"))
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 after retry, got %d", res.Code)
	}
	if attempts.Load() != 2 {
		t.Fatalf("expected exactly 2 attempts, got %d", attempts.Load())
	}
}

func TestProxyRateLimitRejectsBurst(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithRateLimit(1, 1)

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodGet, "/burst", nil)
		res := httptest.NewRecorder()
		proxy.ServeHTTP(res, req)
		if res.Code != want {
			t.Fatalf("request %d: expected %d, got %d", i+1, want, res.Code)
		}
	}
}

func TestProxyCircuitBreakerTemporarilyBlocksUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("down"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithCircuitBreaker(2, time.Second, 50*time.Millisecond)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/breaker", nil)
		res := httptest.NewRecorder()
		proxy.ServeHTTP(res, req)
		if res.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected upstream failure on attempt %d, got %d", i+1, res.Code)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/breaker", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected circuit breaker to reject request with 503, got %d", res.Code)
	}
	if got := res.Body.String(); got == "down" {
		t.Fatalf("expected circuit breaker to short-circuit before upstream call")
	}
}

func TestProxyHealthCheckRejectsUnhealthyUpstream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("unhealthy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("healthy"))
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithHealthCheck("/health", time.Second)

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for unhealthy upstream, got %d", res.Code)
	}
	if got := res.Body.String(); got == "healthy" {
		t.Fatalf("expected health short-circuit before upstream call")
	}
}

func TestProxyFailoverRoutesToSecondaryTarget(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("primary-down"))
	}))
	defer primary.Close()

	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("secondary-ok"))
	}))
	defer secondary.Close()

	proxy, err := NewProxy(primary.URL)
	if err != nil {
		t.Fatalf("NewProxy failed: %v", err)
	}
	proxy.WithFailover(secondary.URL)

	req := httptest.NewRequest(http.MethodGet, "/fallback", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202 from fallback target, got %d", res.Code)
	}
	if got := res.Body.String(); got != "secondary-ok" {
		t.Fatalf("expected fallback body, got %q", got)
	}
}

func TestServerHandleFuncAndMount(t *testing.T) {
	server := NewServer(":0")
	server.HandleFunc(http.MethodGet, "/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})
	server.Mount("/internal", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("mounted"))
	}))

	for _, tc := range []struct {
		name string
		path string
		want string
		code int
	}{
		{name: "handle-func", path: "/ping", want: "pong", code: http.StatusOK},
		{name: "mount", path: "/internal", want: "mounted", code: http.StatusAccepted},
		{name: "mount-subpath", path: "/internal/metrics", want: "mounted", code: http.StatusAccepted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			res := httptest.NewRecorder()
			server.router.ServeHTTP(res, req)

			if res.Code != tc.code {
				t.Fatalf("expected %d, got %d", tc.code, res.Code)
			}
			if got := res.Body.String(); got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestServerMiddleware(t *testing.T) {
	server := NewServer(":0")
	server.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Trace", "runtime")
			next.ServeHTTP(w, r)
		})
	})
	server.Get("/trace", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/trace", nil)
	res := httptest.NewRecorder()
	server.router.ServeHTTP(res, req)

	if got := res.Header().Get("X-Trace"); got != "runtime" {
		t.Fatalf("expected middleware to set header, got %q", got)
	}
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestServerWithReusePortAllowsConcurrentBind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SO_REUSEPORT is POSIX-only")
	}

	serverA := NewServer("127.0.0.1:0").WithReusePort()
	listenerA, err := serverA.listen()
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer listenerA.Close()
	port := listenerA.Addr().(*net.TCPAddr).Port

	go func() { _ = serverA.Server.Serve(listenerA) }()

	serverB := NewServer(fmt.Sprintf("127.0.0.1:%d", port)).WithReusePort()
	listenerB, err := serverB.listen()
	if err != nil {
		t.Fatalf("second listen on shared port %d: %v", port, err)
	}
	defer listenerB.Close()
	go func() { _ = serverB.Server.Serve(listenerB) }()

	client := &http.Client{Timeout: time.Second}
	for i := 0; i < 4; i++ {
		resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	_ = serverB.Shutdown(context.Background())
	_ = serverA.Shutdown(context.Background())
}

func TestServerListenWithoutReusePortStaysExclusive(t *testing.T) {
	serverA := NewServer("127.0.0.1:0")
	listenerA, err := serverA.listen()
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer listenerA.Close()
	port := listenerA.Addr().(*net.TCPAddr).Port

	serverB := NewServer(fmt.Sprintf("127.0.0.1:%d", port))
	listenerB, err := serverB.listen()
	if err == nil {
		listenerB.Close()
		t.Fatal("expected second listen without reuse port to fail")
	}
}

// TestProxyFailoverStreamsSuccessResponse verifies the streaming-safe
// failover path: a successful primary response flows directly to the client
// (no buffering), including chunked writes with Flush.
func TestProxyFailoverStreamsSuccessResponse(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("chunk-1"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		_, _ = w.Write([]byte("chunk-2"))
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("secondary must not be called when the primary succeeds")
	}))
	defer secondary.Close()

	proxy, err := NewProxy(primary.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.WithFailover(secondary.URL)

	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/stream", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", res.Code)
	}
	if got, want := res.Body.String(), "chunk-1chunk-2"; got != want {
		t.Errorf("body %q, want %q (streamed, not buffered)", got, want)
	}
}

// TestProxyFailoverSuppressesFailedPrimary verifies that a 5xx primary
// response is suppressed and the secondary produces the real response.
func TestProxyFailoverSuppressesFailedPrimary(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("primary-failure"))
	}))
	defer primary.Close()
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secondary-ok"))
	}))
	defer secondary.Close()

	proxy, err := NewProxy(primary.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.WithFailover(secondary.URL)

	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/x", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 from secondary", res.Code)
	}
	if got := res.Body.String(); got != "secondary-ok" {
		t.Errorf("body %q, want secondary-ok (primary suppressed)", got)
	}
}

// TestMiddlewareAppliesToRoutesRegisteredBeforeUse is the regression guard
// for dispatch-time middleware: a route registered BEFORE Use() must still
// receive the middleware registered later.
func TestMiddlewareAppliesToRoutesRegisteredBeforeUse(t *testing.T) {
	r := NewRouter()
	r.Get("/early", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var ran bool
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ran = true
			w.Header().Set("X-Middleware-Ran", "yes")
			next.ServeHTTP(w, req)
		})
	})

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/early", nil))
	if !ran {
		t.Fatal("middleware did not run for a route registered before Use()")
	}
	if got := res.Header().Get("X-Middleware-Ran"); got != "yes" {
		t.Errorf("X-Middleware-Ran %q, want yes", got)
	}
}

// TestProxyXForwardedForIPv6AndChain verifies XFF handling: IPv6 client
// addresses are parsed correctly and existing chains are appended, not
// overwritten.
func TestProxyXForwardedForIPv6AndChain(t *testing.T) {
	var gotXFF string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotXFF = r.Header.Get("X-Forwarded-For")
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxy, err := NewProxy(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}

	// IPv6 remote address.
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.RemoteAddr = "[::1]:52341"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	proxy.ServeHTTP(httptest.NewRecorder(), req)
	if got, want := gotXFF, "203.0.113.9, ::1"; got != want {
		t.Errorf("XFF %q, want %q (chain appended, IPv6 host parsed)", got, want)
	}

	// No prior chain: just the client IP.
	gotXFF = ""
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	req2.RemoteAddr = "192.0.2.5:1000"
	proxy.ServeHTTP(httptest.NewRecorder(), req2)
	if got, want := gotXFF, "192.0.2.5"; got != want {
		t.Errorf("XFF %q, want %q", got, want)
	}
}

// TestWithRetryZeroPreservesPooledTransport verifies that disabling retry
// (attempts <= 0) restores the pooled transport instead of dropping it —
// WithRetry(0) after NewProxy must not regress connection pooling.
func TestWithRetryZeroPreservesPooledTransport(t *testing.T) {
	proxy, err := NewProxy("http://localhost:9999")
	if err != nil {
		t.Fatal(err)
	}
	pooled := proxy.reverseProxy.Transport

	proxy.WithRetry(2, time.Millisecond)
	if _, ok := proxy.reverseProxy.Transport.(*proxyRetryTransport); !ok {
		t.Fatal("WithRetry(2) must install the retry transport")
	}

	proxy.WithRetry(0, 0)
	if proxy.reverseProxy.Transport != pooled {
		t.Error("WithRetry(0) must restore the pooled transport, not nil/DefaultTransport")
	}

	// Repeated WithRetry calls must not nest retry transports.
	proxy.WithRetry(3, time.Millisecond)
	proxy.WithRetry(1, time.Millisecond)
	rt, ok := proxy.reverseProxy.Transport.(*proxyRetryTransport)
	if !ok {
		t.Fatal("WithRetry must install a retry transport")
	}
	if _, nested := rt.base.(*proxyRetryTransport); nested {
		t.Error("retry transports must not nest")
	}
}
