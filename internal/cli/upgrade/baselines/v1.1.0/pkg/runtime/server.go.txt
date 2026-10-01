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
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"
)

// Server wraps an HTTP server with FGOTHS runtime conventions.
type Server struct {
	*http.Server
	router           *Router
	metrics          *Metrics
	logger           *slog.Logger
	readinessTimeout time.Duration
	shutdownTimeout  time.Duration
	onShutdown       []func(context.Context) error
	reusePort        bool
	injectedListener net.Listener
}

// defaultReadHeaderTimeout bounds how long a client may take to send request
// headers, closing the Slowloris vector (a client trickling headers to hold a
// connection open indefinitely). It only covers the header phase, so
// long-lived bodies and SSE/HMR streams are unaffected. Override via the
// embedded http.Server (s.ReadHeaderTimeout) when needed.
const defaultReadHeaderTimeout = 10 * time.Second

// NewServer creates a server with a FGOTHS router as its handler.
func NewServer(addr string) *Server {
	if addr == "" {
		addr = ":8080"
	}
	r := NewRouter()
	return &Server{
		Server:  &http.Server{Addr: addr, Handler: r, ReadHeaderTimeout: defaultReadHeaderTimeout},
		router:  r,
		metrics: NewMetrics(),
	}
}

// Use registers middleware for the internal router.
func (s *Server) Use(mws ...func(http.Handler) http.Handler) {
	if s == nil || s.router == nil {
		return
	}
	s.router.Use(mws...)
}

// WithMetrics enables request accounting for the server and exposes a collector
// on the runtime instance for explicit opt-in observability.
func (s *Server) WithMetrics() *Server {
	if s == nil {
		return nil
	}
	if s.metrics == nil {
		s.metrics = NewMetrics()
	}
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			recorder := newStatusRecorder(w)
			next.ServeHTTP(recorder, r)
			// Record under the matched route pattern (e.g. "/users/{id}"),
			// not the raw path — raw IDs would create unbounded metric
			// cardinality (one histogram per distinct URL).
			s.metrics.Record(r.Method, s.router.routePattern(r), recorder.StatusCode(), time.Since(start))
			releaseStatusRecorder(recorder)
		})
	})
	return s
}

// WithLogger enables opt-in structured request logging for server traffic.
func (s *Server) WithLogger(logger *slog.Logger) *Server {
	if s == nil {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	s.logger = logger
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			recorder := newStatusRecorder(w)
			next.ServeHTTP(recorder, r)
			status := recorder.StatusCode()
			releaseStatusRecorder(recorder)
			s.logger.Info("http.server.request",
				"method", r.Method,
				"path", normalizePath(r.URL.Path),
				"status", status,
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", RequestIDFromRequest(r),
			)
		})
	})
	return s
}

// Metrics returns the server collector when request metrics are enabled.
func (s *Server) Metrics() *Metrics {
	if s == nil {
		return nil
	}
	return s.metrics
}

// WithMetricsEndpoint exposes the in-memory runtime metrics snapshot as JSON on a dedicated HTTP endpoint.
func (s *Server) WithMetricsEndpoint(path string) *Server {
	if s == nil || s.router == nil {
		return s
	}
	if path == "" {
		path = "/metrics"
	}
	if s.metrics == nil {
		s.metrics = NewMetrics()
	}
	s.router.Get(path, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(s.metrics.Snapshot())
	})
	return s
}

// Handle delegates to the internal router.
func (s *Server) Handle(method, path string, handler http.Handler) {
	if s == nil || s.router == nil {
		return
	}
	s.router.Handle(method, path, handler)
}

// Get delegates to the internal router.
func (s *Server) Get(path string, handler http.HandlerFunc) {
	s.Handle(http.MethodGet, path, handler)
}

// Post delegates to the internal router.
func (s *Server) Post(path string, handler http.HandlerFunc) {
	s.Handle(http.MethodPost, path, handler)
}

// HandleFunc registers a handler by method and path.
func (s *Server) HandleFunc(method, path string, handler http.HandlerFunc) {
	s.Handle(method, path, handler)
}

// Mount registers a sub-handler under a prefix.
func (s *Server) Mount(prefix string, handler http.Handler) {
	if s == nil || s.router == nil || handler == nil {
		return
	}
	prefix = normalizePath(prefix)
	wildcard := withWildcardRoute(prefix)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions} {
		s.router.Handle(method, prefix, handler)
		s.router.Handle(method, wildcard, handler)
	}
}

// Any delegates to the internal router for all common HTTP methods.
func (s *Server) Any(path string, handler http.Handler) {
	if s == nil || s.router == nil {
		return
	}
	s.router.Any(path, handler)
}

// WithShutdownTimeout configures the maximum time Shutdown will wait for
// in-flight requests to drain before returning. A zero or negative value keeps
// the http.Server default (no additional bound on top of the caller's context).
func (s *Server) WithShutdownTimeout(d time.Duration) *Server {
	if s == nil {
		return nil
	}
	s.shutdownTimeout = d
	return s
}

// WithListener injects a pre-created listener so the next ListenAndServe
// serves on it instead of binding a new one. Useful for socket activation,
// tests, or when the caller owns the bind (e.g. systemd, in-memory pipes).
// The listener is consumed by the first ListenAndServe call.
func (s *Server) WithListener(ln net.Listener) *Server {
	if s == nil {
		return nil
	}
	s.injectedListener = ln
	return s
}

// WithReusePort makes the next ListenAndServe bind with SO_REUSEPORT, so
// multiple processes can hold the same port simultaneously. This is the
// foundation of the dev-mode zero-downtime swap: the watcher starts the
// freshly built binary (which takes over the port alongside the old
// process), waits for it to accept connections, then stops the old one —
// removing the stop/accept gap from every edit cycle. POSIX-only; on
// platforms without SO_REUSEPORT ListenAndServe falls back to the
// standard bind. Development-only: do not enable in production.
func (s *Server) WithReusePort() *Server {
	if s == nil {
		return nil
	}
	s.reusePort = true
	return s
}

// listen creates the listener honoring the reusePort setting.
func (s *Server) listen() (net.Listener, error) {
	if s.Server == nil {
		return nil, fmt.Errorf("server is not initialized")
	}
	if s.injectedListener != nil {
		ln := s.injectedListener
		s.injectedListener = nil
		return ln, nil
	}
	if !s.reusePort {
		return net.Listen("tcp", s.Addr)
	}
	var lc net.ListenConfig
	lc.Control = func(network, address string, c syscall.RawConn) error {
		var opErr error
		err := c.Control(func(fd uintptr) {
			opErr = setReusePort(fd)
		})
		if err != nil {
			return err
		}
		return opErr
	}
	return lc.Listen(context.Background(), "tcp", s.Addr)
}

// ListenAndServe serves on the configured address, binding with
// SO_REUSEPORT when WithReusePort was enabled.
func (s *Server) ListenAndServe() error {
	if s == nil || s.Server == nil {
		return http.ErrServerClosed
	}
	ln, err := s.listen()
	if err != nil {
		return err
	}
	if readyFile := os.Getenv("FGOTHS_READY_FILE"); readyFile != "" {
		_ = os.WriteFile(readyFile, []byte(os.Getenv("FGOTHS_BUILD_ID")), 0o600) //nolint:gosec // G703: path set by the fgoths dev watcher, not request input
	}
	return s.Serve(ln)
}

// OnShutdown registers a hook invoked after the HTTP listener stops accepting
// new connections and in-flight requests have drained. Hooks run sequentially
// in the order they were registered; the first non-nil error short-circuits
// the remaining hooks and is returned to the caller of Shutdown.
func (s *Server) OnShutdown(fn func(context.Context) error) *Server {
	if s == nil || fn == nil {
		return s
	}
	s.onShutdown = append(s.onShutdown, fn)
	return s
}

// Shutdown gracefully stops the HTTP server, allowing in-flight requests to
// complete before returning. When a non-zero shutdownTimeout is configured, the
// caller's context is wrapped with a derived context that cancels after the
// timeout so that long-running handlers do not block process exit. Registered
// OnShutdown hooks (e.g. closing backing stores) run after the listener drains.
func (s *Server) Shutdown(ctx context.Context) error {
	if s == nil || s.Server == nil {
		return nil
	}
	if s.shutdownTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.shutdownTimeout)
		defer cancel()
	}
	if err := s.Server.Shutdown(ctx); err != nil {
		return err
	}
	for _, fn := range s.onShutdown {
		if err := fn(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Proxy registers a reverse proxy under a route prefix for the common HTTP methods.
func (s *Server) Proxy(prefix, target string) error {
	if s == nil || s.router == nil {
		return fmt.Errorf("server is not initialized")
	}
	proxy, err := NewProxy(target)
	if err != nil {
		return err
	}
	proxy.WithStripPrefix(prefix)
	wildcard := withWildcardRoute(prefix)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions} {
		s.router.Handle(method, prefix, proxy)
		s.router.Handle(method, wildcard, proxy)
	}
	return nil
}
