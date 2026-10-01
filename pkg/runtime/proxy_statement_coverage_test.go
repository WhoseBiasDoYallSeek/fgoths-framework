// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type proxyCoverageRoundTripper func(*http.Request) (*http.Response, error)

func (f proxyCoverageRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type proxyCoverageReadCloser struct{ err error }

func (r proxyCoverageReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (proxyCoverageReadCloser) Close() error               { return nil }

type proxyCoverageResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *proxyCoverageResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *proxyCoverageResponseWriter) WriteHeader(status int) { w.status = status }
func (w *proxyCoverageResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func TestProxyCoverageRetryTransportFailureAndFinalAttempt(t *testing.T) {
	readFailure := errors.New("request body read failed")
	req := httptest.NewRequest(http.MethodPost, "http://proxy.test/", nil)
	req.Body = proxyCoverageReadCloser{err: readFailure}
	rt := &proxyRetryTransport{base: proxyCoverageRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("round trip must not start after a body read error")
		return nil, nil
	}), retries: 1}
	if resp, err := rt.RoundTrip(req); resp != nil || !errors.Is(err, readFailure) {
		t.Fatalf("RoundTrip after body read failure = (%v, %v), want nil/read error", resp, err)
	}

	getBodyFailure := errors.New("body recreation failed")
	req = httptest.NewRequest(http.MethodPost, "http://proxy.test/", strings.NewReader("body"))
	req.GetBody = func() (io.ReadCloser, error) { return nil, getBodyFailure }
	if resp, err := (&proxyRetryTransport{base: proxyCoverageRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Fatal("round trip must not start after GetBody fails")
		return nil, nil
	}), retries: 1}).RoundTrip(req); resp != nil || !errors.Is(err, getBodyFailure) {
		t.Fatalf("RoundTrip after GetBody failure = (%v, %v), want nil/GetBody error", resp, err)
	}

	var attempts atomic.Int32
	req = httptest.NewRequest(http.MethodGet, "http://proxy.test/", nil)
	resp, err := (&proxyRetryTransport{
		base: proxyCoverageRoundTripper(func(r *http.Request) (*http.Response, error) {
			attempts.Add(1)
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("retry later")),
				Request:    r,
			}, nil
		}),
		retries: 0,
	}).RoundTrip(req)
	if err != nil || resp == nil || resp.StatusCode != http.StatusServiceUnavailable || attempts.Load() != 1 {
		t.Fatalf("final transient response = (%v, %v), attempts=%d", resp, err, attempts.Load())
	}
	_ = resp.Body.Close()
}

func TestProxyCoverageCircuitResetAndRateLimiterInitialTimestamp(t *testing.T) {
	cb := newProxyCircuitBreaker(1, time.Minute, time.Second)
	cb.openUntil = time.Now().Add(-time.Second)
	if !cb.allow() || !cb.openUntil.IsZero() {
		t.Fatal("expired circuit breaker did not clear its open interval")
	}

	limiter := &proxyRateLimiter{limit: 1, burst: 1, tokens: 1}
	if !limiter.allow() || limiter.last.IsZero() {
		t.Fatal("rate limiter did not initialize its first-use timestamp")
	}
}

func TestProxyCoverageValidationDirectorAndResponseHooks(t *testing.T) {
	for _, target := range []string{"", "://bad", "relative/path"} {
		if _, err := NewProxy(target); err == nil {
			t.Errorf("NewProxy(%q) succeeded, want validation error", target)
		}
	}

	var nilProxy *Proxy
	if nilProxy.WithPathPrefix("/") != nil || nilProxy.WithStripPrefix("/") != nil || nilProxy.WithHeader("X-Test", "x") != nil ||
		nilProxy.WithHealthCheck("", 0) != nil || nilProxy.WithFailover("") != nil || nilProxy.WithObserver(nil) != nil ||
		nilProxy.WithLogger(nil) != nil || nilProxy.WithMetrics() != nil || nilProxy.WithRetry(1, 0) != nil ||
		nilProxy.WithRateLimit(1, 1) != nil || nilProxy.WithCircuitBreaker(1, time.Second, time.Second) != nil ||
		nilProxy.WithRewrite("/", "/") != nil || nilProxy.WithTimeout(time.Second) != nil {
		t.Fatal("a fluent proxy option did not preserve nil receiver safety")
	}
	if nilProxy.Metrics() != nil || nilProxy.TargetURL() != "" || !nilProxy.refreshHealthStatus() {
		t.Fatal("nil proxy accessors or health status were not safe")
	}

	proxy, err := NewProxy("https://upstream.example/base")
	if err != nil {
		t.Fatal(err)
	}
	proxy.WithPathPrefix("/").WithStripPrefix("/").WithRewrite("/", "/")
	proxy.WithHeader("X-Configured", "yes")
	request := &http.Request{
		Method:     http.MethodGet,
		URL:        &url.URL{Path: "/items"},
		RemoteAddr: "not-a-host-port",
	}
	proxy.reverseProxy.Director(request)
	if request.URL.Path != "/base/items" || request.Header.Get("X-Configured") != "yes" ||
		request.Header.Get("X-Forwarded-Proto") != "https" || request.Header.Get("X-Forwarded-For") != "not-a-host-port" {
		t.Fatalf("director did not initialize/forward request headers: path=%q headers=%v", request.URL.Path, request.Header)
	}

	request = httptest.NewRequest(http.MethodGet, "http://incoming.test/items", nil)
	request.Header.Set("X-Correlation-ID", "correlation")
	request.RemoteAddr = "192.0.2.1:1234"
	proxy.reverseProxy.Director(request)
	for _, header := range []string{"X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if got := request.Header.Get(header); got != "correlation" {
			t.Errorf("%s = %q, want forwarded correlation ID", header, got)
		}
	}

	uninitializedHeaders := &Proxy{
		reverseProxy: &httputil.ReverseProxy{},
		targetURL:    &url.URL{Scheme: "http", Host: "upstream.example"},
	}
	uninitializedHeaders.WithHeader("X-Configured", "initialized")
	if got := uninitializedHeaders.headers.Get("X-Configured"); got != "initialized" {
		t.Fatalf("WithHeader did not initialize a nil header map: %q", got)
	}

	var observed int
	proxy.WithCircuitBreaker(2, time.Minute, time.Minute).WithObserver(func(_ *http.Request, _ *http.Response, elapsed time.Duration) {
		observed++
		if elapsed <= 0 {
			t.Errorf("observer elapsed time = %v, want positive", elapsed)
		}
	})
	proxy.reverseProxy.ErrorHandler(httptest.NewRecorder(), request, errors.New("upstream error"))
	if got := proxy.circuitBreaker.failures; got != 1 {
		t.Errorf("ErrorHandler failure count = %d, want 1", got)
	}

	futureRequest := request.WithContext(context.WithValue(request.Context(), proxyStartKey, time.Now().Add(time.Hour)))
	if err := proxy.reverseProxy.ModifyResponse(&http.Response{StatusCode: http.StatusInternalServerError, Request: futureRequest}); err != nil {
		t.Fatal(err)
	}
	if err := proxy.reverseProxy.ModifyResponse(&http.Response{StatusCode: http.StatusOK, Request: request}); err != nil {
		t.Fatal(err)
	}
	if observed != 2 || proxy.circuitBreaker.failures != 0 {
		t.Fatalf("response hooks observed=%d failures=%d, want 2 observations and reset breaker", observed, proxy.circuitBreaker.failures)
	}
	if err := proxy.reverseProxy.ModifyResponse(nil); err != nil {
		t.Errorf("ModifyResponse(nil) = %v, want nil", err)
	}
	if err := proxy.reverseProxy.ModifyResponse(&http.Response{}); err != nil {
		t.Errorf("ModifyResponse without a request = %v, want nil", err)
	}

	var incomplete *Proxy
	incomplete.applyDirector()
	(&Proxy{}).applyDirector()
}

func TestProxyCoverageObserverWrappersAndHealthChecks(t *testing.T) {
	var previousCalls atomic.Int32
	proxy, err := NewProxy("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	proxy.observer = func(*http.Request, *http.Response, time.Duration) { previousCalls.Add(1) }
	proxy.WithLogger(nil)
	proxy.observer(nil, nil, 0)
	proxy.metrics = nil
	proxy.WithMetrics()
	proxy.observer(nil, nil, 0)
	proxy.observer(httptest.NewRequest(http.MethodGet, "http://incoming.test/x", nil), nil, time.Millisecond)
	if previousCalls.Load() != 3 {
		t.Fatalf("previous observer called %d times, want 3", previousCalls.Load())
	}
	if got := proxy.Metrics().Snapshot().ByStatus[http.StatusOK]; got != 1 {
		t.Fatalf("nil proxy response should be recorded as status 200, got %d", got)
	}

	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("health probe path = %q, want /health", r.URL.Path)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer probe.Close()
	healthy, err := NewProxy(probe.URL)
	if err != nil {
		t.Fatal(err)
	}
	healthy.WithHealthCheck("", 0)
	if healthy.healthCheckPath != "/health" || healthy.healthCheckInterval != time.Second || healthy.healthCheckTimeout != 2*time.Second || !healthy.healthy {
		t.Fatalf("health-check defaults/state = path %q interval %v timeout %v healthy %v",
			healthy.healthCheckPath, healthy.healthCheckInterval, healthy.healthCheckTimeout, healthy.healthy)
	}
	healthy.healthy = false
	if healthy.refreshHealthStatus() {
		t.Fatal("cached unhealthy status was ignored within the health-check interval")
	}

	nilTarget := &Proxy{reverseProxy: &httputil.ReverseProxy{}, healthCheckPath: "/health"}
	if nilTarget.refreshHealthStatus() {
		t.Fatal("health check without a target URL succeeded")
	}
	invalidTarget := &Proxy{
		reverseProxy:    &httputil.ReverseProxy{},
		targetURL:       &url.URL{Scheme: "http", Host: "bad host"},
		healthCheckPath: "/health",
	}
	if invalidTarget.refreshHealthStatus() {
		t.Fatal("health check with an invalid request URL succeeded")
	}

	unhealthyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer unhealthyServer.Close()
	unhealthyURL, _ := url.Parse(unhealthyServer.URL)
	unhealthy := &Proxy{
		reverseProxy:    &httputil.ReverseProxy{},
		targetURL:       unhealthyURL,
		healthCheckPath: "/health",
	}
	if unhealthy.refreshHealthStatus() {
		t.Fatal("health check treated a 500 response as healthy")
	}

	unreachableServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	unreachableURL, _ := url.Parse(unreachableServer.URL)
	unreachableServer.Close()
	unreachable := &Proxy{
		reverseProxy:       &httputil.ReverseProxy{},
		targetURL:          unreachableURL,
		healthCheckPath:    "/health",
		healthCheckTimeout: time.Second,
	}
	if unreachable.refreshHealthStatus() {
		t.Fatal("health check treated an unreachable upstream as healthy")
	}
}

func TestProxyCoverageFailoverWriterImplicitAndCommittedResponses(t *testing.T) {
	recorder := &proxyCoverageResponseWriter{}
	writer := &failoverResponseWriter{ResponseWriter: recorder}
	if _, err := writer.Write([]byte("streamed")); err != nil {
		t.Fatal(err)
	}
	if !writer.committed || recorder.body.String() != "streamed" {
		t.Fatalf("implicit response state = committed %v code %d body %q", writer.committed, writer.code, recorder.body.String())
	}
	writer.WriteHeader(http.StatusAccepted)
	if recorder.status != 0 {
		t.Fatalf("committed writer forwarded duplicate header with status %d", recorder.status)
	}

	withoutFlush := &failoverResponseWriter{ResponseWriter: &proxyCoverageResponseWriter{}}
	withoutFlush.Flush()
}

func TestProxyCoverageHealthFailureUsesFailover(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unhealthy primary should not receive proxied request")
	}))
	primaryURL := primary.URL
	primary.Close()
	var fallbackCalls atomic.Int32
	fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fallbackCalls.Add(1)
		_, _ = io.WriteString(w, "fallback")
	}))
	defer fallback.Close()

	proxy, err := NewProxy(primaryURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy.WithHealthCheck("/health", time.Second).WithFailover(fallback.URL)
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/route", nil))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "fallback" || fallbackCalls.Load() != 1 {
		t.Fatalf("health-check failover response = %d %q, fallback calls=%d", recorder.Code, recorder.Body.String(), fallbackCalls.Load())
	}
}

func TestProxyCoverageRetryAndTimeoutTransportConfiguration(t *testing.T) {
	proxy, err := NewProxy("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := proxy.reverseProxy.Transport
	proxy.reverseProxy.Transport = nil
	if got := proxy.WithRetry(0, 0).reverseProxy.Transport; got != http.DefaultTransport {
		t.Fatalf("disabling retries with nil transport restored %T, want default transport", got)
	}

	proxy.reverseProxy.Transport = originalTransport
	proxy.WithRetry(1, 0).WithTimeout(time.Second)
	if _, ok := proxy.reverseProxy.Transport.(*proxyRetryTransport); !ok {
		t.Fatalf("timeout after retry configuration installed %T, want retry transport", proxy.reverseProxy.Transport)
	}
	proxy.WithTimeout(0)
	if _, ok := proxy.reverseProxy.Transport.(*proxyRetryTransport); !ok {
		t.Fatalf("zero timeout unexpectedly changed transport to %T", proxy.reverseProxy.Transport)
	}
}

func TestProxyCoverageCircuitBreakerRejectsAndHealthStatusHasNoFallback(t *testing.T) {
	proxy, err := NewProxy("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	proxy.WithCircuitBreaker(1, time.Minute, time.Minute)
	proxy.circuitBreaker.recordFailure()
	recorder := httptest.NewRecorder()
	proxy.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("open circuit response status = %d, want 503", recorder.Code)
	}

	unhealthy := &Proxy{reverseProxy: proxy.reverseProxy, healthCheckPath: "/health"}
	recorder = httptest.NewRecorder()
	unhealthy.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy proxy without failover status = %d, want 503", recorder.Code)
	}
}

func TestProxyCoverageRemoteAddressParsing(t *testing.T) {
	proxy, err := NewProxy("http://upstream.example")
	if err != nil {
		t.Fatal(err)
	}
	proxy.applyDirector()
	request := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/"}, RemoteAddr: "invalid"}
	proxy.reverseProxy.Director(request)
	if got := request.Header.Get("X-Forwarded-For"); got != "invalid" {
		t.Fatalf("malformed RemoteAddr forwarded as %q, want invalid", got)
	}

	request = httptest.NewRequest(http.MethodGet, "http://incoming.test/", nil)
	request.RemoteAddr = net.JoinHostPort("2001:db8::1", "8080")
	proxy.reverseProxy.Director(request)
	if got := request.Header.Get("X-Forwarded-For"); got != "2001:db8::1" {
		t.Fatalf("IPv6 RemoteAddr forwarded as %q, want bare client IP", got)
	}

	request = httptest.NewRequest(http.MethodGet, "http://incoming.test/", nil)
	request.Header.Set("X-Forwarded-For", "203.0.113.10")
	request.RemoteAddr = "192.0.2.15:8080"
	proxy.reverseProxy.Director(request)
	if got := request.Header.Get("X-Forwarded-For"); got != "203.0.113.10, 192.0.2.15" {
		t.Fatalf("existing X-Forwarded-For chain became %q, want appended client address", got)
	}
}
