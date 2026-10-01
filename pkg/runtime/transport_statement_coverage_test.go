// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
package runtime

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var _ syscall.RawConn = transportCoverageRawConn{}

type transportCoverageRawConn struct{ err error }

func (c transportCoverageRawConn) Control(func(uintptr)) error { return c.err }
func (c transportCoverageRawConn) Read(func(uintptr) bool) error {
	return c.err
}
func (c transportCoverageRawConn) Write(func(uintptr) bool) error {
	return c.err
}

func TestTransportCoverageRequestIDNilAndContextPaths(t *testing.T) {
	if got := ensureRequestID(nil); got != "" {
		t.Fatalf("ensureRequestID(nil) = %q, want empty", got)
	}
	if got := RequestIDFromRequest(nil); got != "" {
		t.Fatalf("RequestIDFromRequest(nil) = %q, want empty", got)
	}

	r := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/"}, Header: nil}
	id := ensureRequestID(r)
	if id == "" || r.Header.Get("X-Request-ID") != id {
		t.Fatalf("ensureRequestID did not initialize missing headers: id=%q headers=%v", id, r.Header)
	}

	withContext := r.WithContext(context.WithValue(r.Context(), requestIDContextKeyValue, "context-id"))
	withContext.Header.Set("X-Request-ID", "header-id")
	if got := RequestIDFromRequest(withContext); got != "context-id" {
		t.Fatalf("context request ID = %q, want context-id", got)
	}
}

func TestTransportCoverageNilRequestsPassThroughMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		middleware func(http.Handler) http.Handler
	}{
		{name: "request id", middleware: NewServer("").WithRequestID().router.middleware[0]},
		{name: "server tracing", middleware: NewServer("").WithOpenTelemetry("").router.middleware[0]},
		{name: "standalone tracing", middleware: TraceMiddleware("")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				called = r == nil
			})
			tc.middleware(next).ServeHTTP(httptest.NewRecorder(), nil)
			if !called {
				t.Fatal("middleware did not pass the nil request to its next handler")
			}
		})
	}
}

func TestTransportCoverageRouteMatcherEdgeCases(t *testing.T) {
	entry := &routeEntry{segments: []routeSegment{{param: "id"}}}
	for _, path := range []string{"/", "/one/two"} {
		if entry.matchParams(path) {
			t.Errorf("matchParams(%q) unexpectedly matched a single-segment parameter", path)
		}
	}
	if got := entry.paramValue("/", "id"); got != "" {
		t.Errorf("paramValue on root = %q, want empty", got)
	}
	if got := entry.paramValue("/one/two", "id"); got != "one" {
		t.Errorf("paramValue for a present first-segment parameter = %q, want one", got)
	}
	extraSegments := &routeEntry{segments: []routeSegment{{literal: "one"}}}
	if got := extraSegments.paramValue("/one/two", "id"); got != "" {
		t.Errorf("paramValue on an extra path segment = %q, want empty", got)
	}

	if _, ok := compileSegParts("{"); ok {
		t.Error("an unclosed parameter must remain a literal")
	}
	if _, ok := compileSegParts("{}"); ok {
		t.Error("an empty parameter must remain a literal")
	}
	if _, ok := compileSegParts("pre{é}"); ok {
		t.Error("a non-ASCII parameter name must remain a literal")
	}
	if _, ok := compileSegParts("item-:"); ok {
		t.Error("an empty colon parameter must remain a literal")
	}
	if parts, ok := compileSegParts("pre{id}!"); !ok || len(parts) != 3 {
		t.Errorf("mixed parameter with invalid colon terminator = %#v, %v; want literal/param/literal", parts, ok)
	}
	if parts, ok := compileSegParts(":id!"); !ok || len(parts) != 2 {
		t.Errorf("colon parameter with an invalid byte terminator = %#v, %v; want param/literal", parts, ok)
	}

	if value, ok := segPartValue([]segPart{{param: "id"}, {literal: "-"}}, "abc", "id"); ok || value != "" {
		t.Errorf("segPartValue without its delimiter = (%q, %v), want empty/false", value, ok)
	}
	if value, ok := segPartValue([]segPart{{literal: "prefix"}}, "other", "id"); ok || value != "" {
		t.Errorf("segPartValue with a mismatched literal = (%q, %v), want empty/false", value, ok)
	}
	if value, ok := segPartValue([]segPart{{param: "id"}}, "abc", "other"); ok || value != "" {
		t.Errorf("segPartValue for an absent name = (%q, %v), want empty/false", value, ok)
	}

	router := NewRouter()
	if got := router.routePattern(nil); got != "" {
		t.Errorf("routePattern(nil) = %q, want empty", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/raw/path", nil)
	if got := router.routePattern(req); got != "/raw/path" {
		t.Errorf("routePattern without a match = %q, want /raw/path", got)
	}
	req = req.WithContext(context.WithValue(req.Context(), routeInfoKey{}, routeInfo{pattern: "/items/{id}"}))
	if got := router.routePattern(req); got != "/items/{id}" {
		t.Errorf("routePattern with a match = %q, want /items/{id}", got)
	}
	req = httptest.NewRequest(http.MethodGet, "/fallback", nil)
	req = req.WithContext(context.WithValue(req.Context(), routeInfoKey{}, routeInfo{}))
	if got := router.routePattern(req); got != "/fallback" {
		t.Errorf("routePattern with empty route metadata = %q, want /fallback", got)
	}

	if !routeMatches("/api", "/api") {
		t.Error("identical route and path did not match")
	}
	if !routeMatches("/*", "/anything") {
		t.Error("root wildcard did not match a path")
	}
	if !routeMatches("/api/*", "/api") || !routeMatches("/api/*", "/api/v1/items") {
		t.Error("prefixed wildcard did not match its base and descendant paths")
	}
	if routeMatches("/api/*", "/apix") || routeMatches("/api/items", "/other") {
		t.Error("wildcard or ordinary route matched an unrelated path")
	}
}

func TestTransportCoverageMetricsWindowAndSmallHistogram(t *testing.T) {
	histogram := newRouteHistogram(1)
	histogram.Record(time.Millisecond)
	histogram.Record(2 * time.Millisecond)
	if got := histogram.Snapshot().P50; got != 2*time.Millisecond {
		t.Fatalf("small-capacity histogram retained P50 %v, want 2ms", got)
	}

	empty := &RouteHistogram{percentilesDirty: true}
	if got := empty.Snapshot(); got != (DurationPercentiles{}) {
		t.Fatalf("empty dirty histogram snapshot = %+v, want zero percentiles", got)
	}

	metrics := NewMetrics()
	if requests, rate := metrics.ErrorWindow(10); requests != 0 || rate != 0 {
		t.Fatalf("empty error window = (%d, %v), want (0, 0)", requests, rate)
	}
	if requests, rate := metrics.ErrorWindow(0); requests != 0 || rate != 0 {
		t.Fatalf("zero-sized error window = (%d, %v), want (0, 0)", requests, rate)
	}
	for i := 0; i < errorWindowCap; i++ {
		metrics.Record(http.MethodGet, "/window", http.StatusOK)
	}
	metrics.Record(http.MethodGet, "/window", http.StatusInternalServerError)
	if requests, rate := metrics.ErrorWindow(1); requests != 1 || rate != 1 {
		t.Fatalf("last-request error window = (%d, %v), want (1, 1)", requests, rate)
	}
	allErrors := NewMetrics()
	for i := 0; i < errorWindowCap; i++ {
		allErrors.Record(http.MethodGet, "/window", http.StatusInternalServerError)
	}
	allErrors.Record(http.MethodGet, "/window", http.StatusOK)
	if requests, rate := allErrors.ErrorWindow(errorWindowCap); requests != errorWindowCap || rate != 1-float64(1)/errorWindowCap {
		t.Fatalf("evicted error window = (%d, %v), want %d requests and one fewer error", requests, rate, errorWindowCap)
	}
}

func TestTransportCoverageServerMiddlewareAndShutdownErrors(t *testing.T) {
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer slog.SetDefault(previousLogger)

	server := &Server{Server: &http.Server{}, router: NewRouter()}
	server.WithMetrics()
	server.router.middleware[0](http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r != nil {
			t.Error("expected nil request to pass through metrics middleware")
		}
	})).ServeHTTP(httptest.NewRecorder(), nil)

	logServer := &Server{Server: &http.Server{}, router: NewRouter()}
	logServer.WithLogger(nil)
	logServer.router.middleware[0](http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r != nil {
			t.Error("expected nil request to pass through logger middleware")
		}
	})).ServeHTTP(httptest.NewRecorder(), nil)

	metricsEndpoint := &Server{Server: &http.Server{}, router: NewRouter()}
	metricsEndpoint.WithMetricsEndpoint("")
	recorder := httptest.NewRecorder()
	metricsEndpoint.router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "total_requests") {
		t.Fatalf("default metrics endpoint response = %d %q", recorder.Code, recorder.Body.String())
	}

	blocking := make(chan struct{})
	started := make(chan struct{})
	listener, err := netListenTCP()
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	active := NewServer(listener.Addr().String())
	active.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-blocking
		w.WriteHeader(http.StatusNoContent)
	})
	serveDone := make(chan error, 1)
	go func() { serveDone <- active.Serve(listener) }()
	clientDone := make(chan struct{})
	go func() {
		defer close(clientDone)
		_, _ = http.Get("http://" + listener.Addr().String())
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		close(blocking)
		t.Fatal("handler did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := active.Shutdown(ctx); !errors.Is(err, context.Canceled) {
		close(blocking)
		t.Fatalf("Shutdown with an active request and cancelled context = %v, want context.Canceled", err)
	}
	close(blocking)
	select {
	case <-clientDone:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not finish after releasing the handler")
	}
	select {
	case err := <-serveDone:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("Serve returned %v, want http.ErrServerClosed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not stop after Shutdown")
	}

	var nilServer *Server
	if err := nilServer.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown on nil server = %v, want nil", err)
	}
}

func TestTransportCoverageReusePortControlError(t *testing.T) {
	wantErr := errors.New("raw connection control failed")
	err := reusePortListenConfig().Control("tcp", "127.0.0.1:0", transportCoverageRawConn{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("reuse-port control error = %v, want %v", err, wantErr)
	}
}

func TestTransportCoverageOpenTelemetryOptionalAttributes(t *testing.T) {
	server := NewServer("")
	server.WithOpenTelemetry("")
	req := httptest.NewRequest(http.MethodGet, "http://service.test/secure", nil)
	req.TLS = &tls.ConnectionState{}
	server.router.middleware[len(server.router.middleware)-1](http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || r.URL.Path != "/secure" {
			t.Error("server tracing did not preserve TLS state and request path")
		}
	})).ServeHTTP(httptest.NewRecorder(), req)

	proxy, err := NewProxy("http://upstream.example")
	if err != nil {
		t.Fatal(err)
	}
	var observed int
	proxy.WithObserver(func(r *http.Request, _ *http.Response, _ time.Duration) {
		observed++
		if observed == 2 && r == nil {
			t.Error("upstream observer did not receive its non-nil request")
		}
	})
	proxy.WithOpenTelemetry("")
	proxy.observer(nil, nil, 0)
	proxy.observer(httptest.NewRequest(http.MethodGet, "http://upstream.example/x", nil),
		&http.Response{StatusCode: http.StatusInternalServerError}, time.Millisecond)
	if observed != 2 {
		t.Fatalf("previous observer calls = %d, want 2", observed)
	}
}

func TestTransportCoverageMutualTLSErrorPropagation(t *testing.T) {
	server := NewServer("")
	if err := server.WithMutualTLS("", "", ""); err == nil {
		t.Fatal("WithMutualTLS accepted missing certificate and key files")
	}
}

func TestTransportCoverageUpstreamTLSInitializationAndReadFailure(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := writeCertificateAuthority(t, dir, "coverage-ca")
	clientCert, clientKey := writeSignedCert(t, dir, "coverage-client", caCert, caKey, false)

	previousDefaultTransport := http.DefaultTransport
	protocols := &http.Protocols{}
	protocols.SetHTTP1(true)
	http.DefaultTransport = &http.Transport{Protocols: protocols}
	defer func() { http.DefaultTransport = previousDefaultTransport }()

	proxy, err := NewProxy("https://upstream.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := proxy.WithUpstreamTLS(clientCert, clientKey, caCert); err != nil {
		t.Fatalf("WithUpstreamTLS with a valid client certificate and CA failed: %v", err)
	}
	transport := proxy.reverseProxy.Transport.(*http.Transport)
	if transport.TLSClientConfig == nil || len(transport.TLSClientConfig.Certificates) != 1 ||
		transport.TLSClientConfig.RootCAs == nil || transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("upstream TLS config was incomplete: %#v", transport.TLSClientConfig)
	}

	caOnly, err := NewProxy("https://upstream.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := caOnly.WithUpstreamTLS("", "", caCert); err != nil {
		t.Fatalf("WithUpstreamTLS with a CA only failed: %v", err)
	}
	if got := caOnly.reverseProxy.Transport.(*http.Transport).TLSClientConfig; got == nil || got.RootCAs == nil {
		t.Fatalf("CA-only configuration did not initialize TLSClientConfig: %#v", got)
	}

	missingCA, err := NewProxy("https://upstream.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingCA.WithUpstreamTLS(clientCert, clientKey, filepath.Join(dir, "missing-ca.pem")); err == nil {
		t.Fatal("WithUpstreamTLS accepted a missing CA file")
	}
}

func netListenTCP() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
