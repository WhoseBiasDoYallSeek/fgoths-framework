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
// Package runtime provides the embedded execution layer for FGOTHS apps.
//
// The initial implementation focuses on a native Go HTTP router and reverse
// proxy primitives that are compatible with the runtime design described in the
// ADRs without requiring CGO or external Envoy/native dependencies.
package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Proxy is a native Go reverse proxy compatible with the FGOTHS runtime.
type RequestObserver func(*http.Request, *http.Response, time.Duration)

type proxyRetryTransport struct {
	base    http.RoundTripper
	retries int
	delay   time.Duration
}

func (rt *proxyRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt == nil || rt.base == nil {
		return http.DefaultTransport.RoundTrip(req)
	}
	var (
		resp *http.Response
		err  error
	)
	if req.Body != nil && req.GetBody == nil {
		body, readErr := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(body)), nil
		}
	}
	for attempt := 0; attempt <= rt.retries; attempt++ {
		attemptReq := req.Clone(req.Context())
		if req.GetBody != nil {
			attemptReq.Body, err = req.GetBody()
			if err != nil {
				return nil, err
			}
		}
		resp, err = rt.base.RoundTrip(attemptReq)
		if err == nil && resp != nil && resp.StatusCode < http.StatusInternalServerError && resp.StatusCode != http.StatusTooManyRequests {
			return resp, nil
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if attempt == rt.retries {
			break
		}
		if rt.delay > 0 {
			time.Sleep(rt.delay)
		}
	}
	return resp, err
}

type proxyCircuitBreaker struct {
	mu          sync.Mutex
	threshold   int
	window      time.Duration
	openFor     time.Duration
	failures    int
	resetAt     time.Time
	openUntil   time.Time
	lastFailure time.Time
}

func newProxyCircuitBreaker(threshold int, window, openFor time.Duration) *proxyCircuitBreaker {
	if threshold <= 0 {
		return nil
	}
	if window <= 0 {
		window = time.Minute
	}
	if openFor <= 0 {
		openFor = 15 * time.Second
	}
	return &proxyCircuitBreaker{threshold: threshold, window: window, openFor: openFor}
}

func (cb *proxyCircuitBreaker) allow() bool {
	if cb == nil {
		return true
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if !cb.openUntil.IsZero() {
		if time.Now().Before(cb.openUntil) {
			return false
		}
		cb.openUntil = time.Time{}
	}
	if cb.resetAt.IsZero() || time.Since(cb.resetAt) > cb.window {
		cb.failures = 0
		cb.resetAt = time.Now()
	}
	return true
}

func (cb *proxyCircuitBreaker) recordSuccess() {
	if cb == nil {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures = 0
	cb.resetAt = time.Now()
	cb.openUntil = time.Time{}
}

func (cb *proxyCircuitBreaker) recordFailure() {
	if cb == nil {
		return
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	now := time.Now()
	if cb.resetAt.IsZero() || now.Sub(cb.resetAt) > cb.window {
		cb.failures = 0
		cb.resetAt = now
	}
	cb.failures++
	cb.lastFailure = now
	if cb.failures >= cb.threshold {
		cb.openUntil = now.Add(cb.openFor)
	}
}

type proxyRateLimiter struct {
	mu     sync.Mutex
	limit  float64
	burst  float64
	tokens float64
	last   time.Time
}

func newProxyRateLimiter(requestsPerSecond, burst int) *proxyRateLimiter {
	if requestsPerSecond <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = requestsPerSecond
	}
	return &proxyRateLimiter{
		limit:  float64(requestsPerSecond),
		burst:  float64(burst),
		tokens: float64(burst),
		last:   time.Now(),
	}
}

func (rl *proxyRateLimiter) allow() bool {
	if rl == nil {
		return true
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	if rl.last.IsZero() {
		rl.last = now
	}
	elapsed := now.Sub(rl.last).Seconds()
	rl.last = now
	rl.tokens += elapsed * rl.limit
	if rl.tokens > rl.burst {
		rl.tokens = rl.burst
	}
	if rl.tokens >= 1 {
		rl.tokens -= 1
		return true
	}
	return false
}

type Proxy struct {
	reverseProxy        *httputil.ReverseProxy
	targetURL           *url.URL
	stripPrefix         string
	targetPathPrefix    string
	rewriteFrom         string
	rewriteTo           string
	headers             http.Header
	timeout             time.Duration
	observer            RequestObserver
	logger              *slog.Logger
	metrics             *Metrics
	circuitBreaker      *proxyCircuitBreaker
	rateLimiter         *proxyRateLimiter
	retryAttempts       int
	retryDelay          time.Duration
	healthCheckPath     string
	healthCheckInterval time.Duration
	healthCheckTimeout  time.Duration
	failoverProxy       *Proxy
	healthMu            sync.RWMutex
	healthy             bool
	lastHealthCheck     time.Time
}

// NewProxy creates a reverse proxy to a backend target.
type proxyContextKey string

const proxyStartKey proxyContextKey = "fgoths.proxy.start"

func NewProxy(target string) (*Proxy, error) {
	if target == "" {
		return nil, fmt.Errorf("target url is required")
	}
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("target must be a full URL such as http://localhost:8081")
	}

	reverseProxy := httputil.NewSingleHostReverseProxy(u)
	// Connection pooling: the default transport keeps only 2 idle conns per
	// host, which forces a fresh dial per request under concurrency (measured
	// 88% of proxy CPU time in syscalls). A dedicated transport reuses
	// connections and removes the per-request dial from the hot path.
	reverseProxy.Transport = &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	proxy := &Proxy{reverseProxy: reverseProxy, targetURL: u, headers: make(http.Header), metrics: NewMetrics()}
	reverseProxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		if proxy.circuitBreaker != nil {
			proxy.circuitBreaker.recordFailure()
		}
		http.Error(w, fmt.Sprintf("proxy error: %v", err), http.StatusBadGateway)
	}
	reverseProxy.ModifyResponse = func(resp *http.Response) error {
		if resp == nil || resp.Request == nil {
			return nil
		}
		if resp.StatusCode >= http.StatusInternalServerError || resp.StatusCode == http.StatusTooManyRequests {
			if proxy.circuitBreaker != nil {
				proxy.circuitBreaker.recordFailure()
			}
		} else if proxy.circuitBreaker != nil {
			proxy.circuitBreaker.recordSuccess()
		}
		if proxy.observer != nil {
			start, ok := resp.Request.Context().Value(proxyStartKey).(time.Time)
			if !ok {
				start = time.Now()
			}
			took := time.Since(start)
			if took <= 0 {
				took = time.Nanosecond
			}
			proxy.observer(resp.Request, resp, took)
		}
		return nil
	}

	return proxy, nil
}

func (p *Proxy) applyDirector() {
	if p == nil || p.reverseProxy == nil || p.targetURL == nil {
		return
	}

	//nolint:staticcheck // SA1019: Rewrite cannot mutate req.URL.Host the way the failover path requires.
	p.reverseProxy.Director = func(req *http.Request) {
		*req = *req.WithContext(context.WithValue(req.Context(), proxyStartKey, time.Now()))
		req.URL.Scheme = p.targetURL.Scheme
		req.URL.Host = p.targetURL.Host
		req.URL.Path = joinProxyPath(p.targetURL.Path, req.URL.Path, p.stripPrefix, p.targetPathPrefix, p.rewriteFrom, p.rewriteTo)
		req.Host = p.targetURL.Host
		if req.Header == nil {
			req.Header = make(http.Header)
		}
		if requestID := RequestIDFromRequest(req); requestID != "" {
			req.Header.Set("X-Request-ID", requestID)
			req.Header.Set("X-Correlation-ID", requestID)
			req.Header.Set("X-Trace-ID", requestID)
		}
		if req.URL.Scheme == "http" || req.URL.Scheme == "https" {
			req.Header.Set("X-Forwarded-Proto", req.URL.Scheme)
		}
		if req.RemoteAddr != "" {
			// Append to any existing X-Forwarded-For chain (audit trail
			// through multiple proxies) instead of overwriting it, and
			// parse the client host with net.SplitHostPort so IPv6
			// addresses ("[::1]:8080") are handled correctly.
			clientIP, _, err := net.SplitHostPort(req.RemoteAddr)
			if err != nil {
				clientIP = req.RemoteAddr
			}
			if prior := strings.TrimSpace(req.Header.Get("X-Forwarded-For")); prior != "" {
				req.Header.Set("X-Forwarded-For", prior+", "+clientIP)
			} else {
				req.Header.Set("X-Forwarded-For", clientIP)
			}
		}
		for key, values := range p.headers {
			for _, value := range values {
				req.Header.Set(key, value)
			}
		}
	}
}

// WithPathPrefix prepends a path segment to the upstream request before the
// stripped route tail is applied, which is useful for multi-service gateways.
func (p *Proxy) WithPathPrefix(prefix string) *Proxy {
	if p == nil {
		return nil
	}
	p.targetPathPrefix = normalizePath(prefix)
	if p.targetPathPrefix == "/" {
		p.targetPathPrefix = ""
	}
	p.applyDirector()
	return p
}

// WithStripPrefix rewrites the proxied request path by removing the configured prefix.
func (p *Proxy) WithStripPrefix(prefix string) *Proxy {
	if p == nil {
		return nil
	}
	p.stripPrefix = normalizePath(prefix)
	if p.stripPrefix == "/" {
		p.stripPrefix = ""
	}
	p.applyDirector()
	return p
}

// WithHeader adds a request header to every upstream proxied request.
func (p *Proxy) WithHeader(key, value string) *Proxy {
	if p == nil {
		return nil
	}
	if p.headers == nil {
		p.headers = make(http.Header)
	}
	p.headers.Set(key, value)
	p.applyDirector()
	return p
}

// WithHealthCheck performs a lightweight probe against the upstream and rejects
// traffic while the target is considered unhealthy.
func (p *Proxy) WithHealthCheck(path string, interval time.Duration) *Proxy {
	if p == nil {
		return nil
	}
	if path == "" {
		path = "/health"
	}
	p.healthCheckPath = normalizePath(path)
	if interval <= 0 {
		interval = time.Second
	}
	p.healthCheckInterval = interval
	if p.healthCheckTimeout <= 0 {
		p.healthCheckTimeout = 2 * time.Second
	}
	p.refreshHealthStatus()
	return p
}

// WithFailover redirects traffic to a secondary target when the primary target
// fails a request or health check.
func (p *Proxy) WithFailover(target string) *Proxy {
	if p == nil {
		return nil
	}
	if target == "" {
		return p
	}
	fallback, err := NewProxy(target)
	if err != nil {
		return p
	}
	p.failoverProxy = fallback
	return p
}

// WithObserver records per-request metrics and timing for upstream requests.
func (p *Proxy) WithObserver(observer RequestObserver) *Proxy {
	if p == nil {
		return nil
	}
	p.observer = observer
	return p
}

// WithLogger enables opt-in structured request logging for proxied traffic.
func (p *Proxy) WithLogger(logger *slog.Logger) *Proxy {
	if p == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	p.logger = logger
	previous := p.observer
	p.observer = func(req *http.Request, resp *http.Response, elapsed time.Duration) {
		if previous != nil {
			previous(req, resp, elapsed)
		}
		if req == nil {
			return
		}
		status := http.StatusOK
		if resp != nil {
			status = resp.StatusCode
		}
		p.logger.Info("http.proxy.request",
			"method", req.Method,
			"path", normalizePath(req.URL.Path),
			"status", status,
			"duration_ms", elapsed.Milliseconds(),
			"request_id", RequestIDFromRequest(req),
			"upstream", p.TargetURL(),
		)
	}
	return p
}

// WithMetrics enables request accounting on the proxy for opt-in observability.
func (p *Proxy) WithMetrics() *Proxy {
	if p == nil {
		return nil
	}
	if p.metrics == nil {
		p.metrics = NewMetrics()
	}
	previous := p.observer
	p.observer = func(req *http.Request, resp *http.Response, elapsed time.Duration) {
		if previous != nil {
			previous(req, resp, elapsed)
		}
		if req == nil {
			return
		}
		status := http.StatusOK
		if resp != nil {
			status = resp.StatusCode
		}
		p.metrics.Record(req.Method, req.URL.Path, status, elapsed)
	}
	return p
}

// Metrics returns the proxy collector, if metrics tracking is enabled.
func (p *Proxy) Metrics() *Metrics {
	if p == nil {
		return nil
	}
	return p.metrics
}

// WithRetry retries upstream requests on transient failures. attempts <= 0
// disables retrying while keeping the pooled transport (and any other
// transport configuration) intact.
func (p *Proxy) WithRetry(attempts int, delay time.Duration) *Proxy {
	if p == nil {
		return nil
	}
	p.retryAttempts = attempts
	p.retryDelay = delay
	base := p.reverseProxy.Transport
	// Unwrap a previous retry transport so repeated WithRetry calls do not
	// nest wrappers, and so disabling retry restores the pooled transport.
	if rt, ok := base.(*proxyRetryTransport); ok {
		base = rt.base
	}
	if base == nil {
		base = http.DefaultTransport
	}
	if attempts <= 0 {
		p.reverseProxy.Transport = base
		return p
	}
	p.reverseProxy.Transport = &proxyRetryTransport{base: base, retries: attempts, delay: delay}
	return p
}

// WithRateLimit throttles proxied requests per second with a small burst bucket.
func (p *Proxy) WithRateLimit(requestsPerSecond, burst int) *Proxy {
	if p == nil {
		return nil
	}
	p.rateLimiter = newProxyRateLimiter(requestsPerSecond, burst)
	return p
}

// WithCircuitBreaker opens the proxy when too many failures are observed.
func (p *Proxy) WithCircuitBreaker(threshold int, window, openFor time.Duration) *Proxy {
	if p == nil {
		return nil
	}
	p.circuitBreaker = newProxyCircuitBreaker(threshold, window, openFor)
	return p
}

// WithRewrite rewrites a path prefix before forwarding it to the upstream.
func (p *Proxy) WithRewrite(from, to string) *Proxy {
	if p == nil {
		return nil
	}
	p.rewriteFrom = normalizePath(from)
	if p.rewriteFrom == "/" {
		p.rewriteFrom = ""
	}
	p.rewriteTo = normalizePath(to)
	if p.rewriteTo == "/" {
		p.rewriteTo = ""
	}
	p.applyDirector()
	return p
}

// WithTimeout configures upstream HTTP timeouts on the proxy transport.
func (p *Proxy) WithTimeout(timeout time.Duration) *Proxy {
	if p == nil {
		return nil
	}
	p.timeout = timeout
	if timeout > 0 {
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   timeout,
			ResponseHeaderTimeout: timeout,
			ExpectContinueTimeout: 1 * time.Second,
		}
		if p.retryAttempts > 0 {
			p.reverseProxy.Transport = &proxyRetryTransport{base: transport, retries: p.retryAttempts, delay: p.retryDelay}
			return p
		}
		p.reverseProxy.Transport = transport
	}
	return p
}

// ServeHTTP acts as a native Go reverse proxy for a configured upstream.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p == nil || p.reverseProxy == nil {
		http.NotFound(w, r)
		return
	}
	if p.rateLimiter != nil && !p.rateLimiter.allow() {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	if p.circuitBreaker != nil && !p.circuitBreaker.allow() {
		http.Error(w, "upstream temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	if p.healthCheckPath != "" && !p.refreshHealthStatus() {
		if p.failoverProxy != nil {
			p.failoverProxy.ServeHTTP(w, r)
			return
		}
		http.Error(w, "upstream unavailable", http.StatusServiceUnavailable)
		return
	}
	if p.failoverProxy != nil {
		// Streaming-safe failover: the primary attempt writes straight to
		// the real ResponseWriter through a status-intercepting wrapper. If
		// the upstream returns 5xx/429 before any body bytes are written,
		// the wrapper suppresses the response and the failover proxy takes
		// over. Once the body has started flowing, the response is committed
		// — failover is no longer possible (and not needed: the request
		// already succeeded from the client's perspective).
		fw := &failoverResponseWriter{ResponseWriter: w, code: http.StatusOK}
		p.reverseProxy.ServeHTTP(fw, r)
		if fw.failed && !fw.committed {
			p.failoverProxy.ServeHTTP(w, r)
		}
		return
	}
	p.reverseProxy.ServeHTTP(w, r)
}

// failoverResponseWriter intercepts the status code of the primary upstream
// attempt without buffering the body. WriteHeader below 500 (and not 429)
// marks the response as committed; a failing status is suppressed so the
// failover proxy can produce the real response.
type failoverResponseWriter struct {
	http.ResponseWriter
	code      int
	failed    bool
	committed bool
}

func (fw *failoverResponseWriter) WriteHeader(code int) {
	if fw.committed {
		return
	}
	if code >= http.StatusInternalServerError || code == http.StatusTooManyRequests {
		fw.failed = true
		return // suppress; failover will write the real response
	}
	fw.committed = true
	fw.code = code
	fw.ResponseWriter.WriteHeader(code)
}

func (fw *failoverResponseWriter) Write(b []byte) (int, error) {
	if fw.failed && !fw.committed {
		return len(b), nil // swallow body bytes of a failed attempt
	}
	if !fw.committed {
		// Implicit 200: the upstream started streaming a success response.
		fw.committed = true
	}
	return fw.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer when it supports flushing, so
// streaming responses (SSE, chunked) work through the failover path.
func (fw *failoverResponseWriter) Flush() {
	if f, ok := fw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (p *Proxy) refreshHealthStatus() bool {
	if p == nil || p.healthCheckPath == "" {
		return true
	}
	p.healthMu.Lock()
	defer p.healthMu.Unlock()
	if p.healthCheckInterval > 0 && !p.lastHealthCheck.IsZero() && time.Since(p.lastHealthCheck) < p.healthCheckInterval {
		return p.healthy
	}
	if p.healthCheckTimeout <= 0 {
		p.healthCheckTimeout = 2 * time.Second
	}
	if p.targetURL == nil {
		p.healthy = false
		p.lastHealthCheck = time.Now()
		return false
	}
	probeURL := *p.targetURL
	probeURL.Path = joinProxyPath(p.targetURL.Path, p.healthCheckPath, p.stripPrefix, p.targetPathPrefix, p.rewriteFrom, p.rewriteTo)
	if probeURL.Path == "" {
		probeURL.Path = "/"
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.healthCheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL.String(), nil)
	if err != nil {
		p.healthy = false
		p.lastHealthCheck = time.Now()
		return false
	}
	// A dedicated client with the proxy's own transport: connection reuse
	// for probes, no interference with http.DefaultClient's global state.
	client := &http.Client{
		Transport: p.reverseProxy.Transport,
		Timeout:   p.healthCheckTimeout,
	}
	if client.Transport == nil {
		client.Transport = http.DefaultTransport
	}
	resp, err := client.Do(req)
	if err != nil {
		p.healthy = false
		p.lastHealthCheck = time.Now()
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	p.healthy = resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusInternalServerError
	p.lastHealthCheck = time.Now()
	return p.healthy
}

func joinProxyPath(basePath, incomingPath, stripPrefix, targetPathPrefix, rewriteFrom, rewriteTo string) string {
	path := incomingPath
	if stripPrefix != "" {
		path = strings.TrimPrefix(path, stripPrefix)
	}
	if rewriteFrom != "" {
		if path == rewriteFrom {
			path = rewriteTo
		} else if strings.HasPrefix(path, rewriteFrom+"/") {
			path = rewriteTo + strings.TrimPrefix(path, rewriteFrom)
		}
	}
	if path == "" {
		path = "/"
	}
	if targetPathPrefix != "" {
		targetPathPrefix = normalizePath(targetPathPrefix)
		if targetPathPrefix != "/" {
			path = targetPathPrefix + path
		}
	}
	if basePath == "" || basePath == "/" {
		return path
	}
	if strings.HasSuffix(basePath, "/") {
		return strings.TrimSuffix(basePath, "/") + path
	}
	return basePath + path
}

// TargetURL returns the configured upstream URL.
func (p *Proxy) TargetURL() string {
	if p == nil || p.targetURL == nil {
		return ""
	}
	return p.targetURL.String()
}

func normalizePath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if path != "/" && strings.HasSuffix(path, "/") {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}
