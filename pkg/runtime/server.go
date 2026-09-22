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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Router is a lightweight HTTP router for generated FGOTHS projects.
type Router struct {
	mu         sync.RWMutex
	routes     map[string]map[string]*routeEntry
	middleware []func(http.Handler) http.Handler
}

// routeEntry is a route pattern compiled once at registration time.
type routeEntry struct {
	handler   http.Handler
	segments  []routeSegment // nil unless the pattern contains parameters
	hasParams bool
	literals  int // number of literal segments; specificity score
}

// routeSegment is one path segment of a compiled pattern. A segment is
// either a whole-segment parameter (param set, parts nil), a literal
// (literal set, parts nil), or a mixed segment compiled into parts
// (e.g. "post-{id}" -> [lit "post-", param "id"]).
type routeSegment struct {
	literal string
	param   string // non-empty when the segment is a whole-segment parameter
	parts   []segPart
}

// segPart is one piece of a mixed segment: a literal chunk or a parameter.
type segPart struct {
	literal string
	param   string
}

// NewRouter creates a new FGOTHS runtime router.
func NewRouter() *Router {
	return &Router{routes: make(map[string]map[string]*routeEntry)}
}

// Use registers middleware for all routes.
func (r *Router) Use(mws ...func(http.Handler) http.Handler) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.middleware = append(r.middleware, mws...)
}

// Handle registers a handler for a specific HTTP method and path.
// Middleware is NOT applied here: it is applied at dispatch time (see
// ServeHTTP), so routes registered before Use() still receive middleware
// registered later.
func (r *Router) Handle(method, path string, handler http.Handler) {
	if r == nil || handler == nil {
		return
	}
	method = strings.ToUpper(method)
	path = normalizePath(path)

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.routes[method] == nil {
		r.routes[method] = make(map[string]*routeEntry)
	}
	entry := &routeEntry{handler: handler}
	// Trailing-wildcard patterns ("/prefix/*") keep the legacy catch-all
	// semantics; only plain segment patterns participate in param matching.
	if !strings.HasSuffix(path, "/*") {
		if segments, hasParams := compilePattern(path); hasParams {
			entry.segments = segments
			entry.hasParams = true
			for _, seg := range segments {
				if seg.param == "" {
					entry.literals++
				}
			}
		}
	}
	r.routes[method][path] = entry
}

// Get registers a GET handler.
func (r *Router) Get(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodGet, path, handler)
}

// Post registers a POST handler.
func (r *Router) Post(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodPost, path, handler)
}

// Put registers a PUT handler.
func (r *Router) Put(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodPut, path, handler)
}

// Delete registers a DELETE handler.
func (r *Router) Delete(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodDelete, path, handler)
}

// Patch registers a PATCH handler.
func (r *Router) Patch(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodPatch, path, handler)
}

// Any registers the same handler for all common HTTP methods.
func (r *Router) Any(path string, handler http.Handler) {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions} {
		r.Handle(method, path, handler)
	}
}

// ServeHTTP dispatches a request to the configured route. When the matched
// route contains path parameters, a lazy paramMatch is injected into the
// request context and read back with PathValue — no per-request slice or map
// is built for the values themselves.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r == nil {
		http.NotFound(w, req)
		return
	}
	path := normalizePath(req.URL.Path)
	method := strings.ToUpper(req.Method)

	r.mu.RLock()
	entry, pattern, ok := r.matchRoute(method, path)
	middleware := r.middleware
	r.mu.RUnlock()
	if !ok {
		http.NotFound(w, req)
		return
	}
	if entry.hasParams {
		req = req.WithContext(context.WithValue(req.Context(), pathParamsKey{}, paramMatch{entry: entry, path: path}))
	}
	// Expose the matched route pattern for metrics/logging cardinality
	// control (record "/users/{id}", not "/users/123").
	req = req.WithContext(context.WithValue(req.Context(), routePatternKey{}, pattern))
	// Apply the middleware chain at dispatch time so the chain is always
	// the current one, regardless of route/middleware registration order.
	h := entry.handler
	for i := len(middleware) - 1; i >= 0; i-- {
		h = middleware[i](h)
	}
	h.ServeHTTP(w, req)
}

func (r *Router) matchRoute(method, path string) (*routeEntry, string, bool) {
	if r == nil {
		return nil, "", false
	}
	methodRoutes, ok := r.routes[method]
	if !ok {
		return nil, "", false
	}
	if entry, ok := methodRoutes[path]; ok {
		return entry, path, true
	}

	var (
		paramEntry    *routeEntry
		paramPattern  string
		paramLiterals int

		legacyEntry   *routeEntry
		legacyPattern string
		rootEntry     *routeEntry
	)
	for pattern, entry := range methodRoutes {
		switch {
		case entry.hasParams:
			if !entry.matchParams(path) {
				continue
			}
			// Most literal segments wins; tie-break on longer pattern.
			if paramEntry == nil || entry.literals > paramLiterals ||
				(entry.literals == paramLiterals && len(pattern) > len(paramPattern)) {
				paramEntry, paramPattern, paramLiterals = entry, pattern, entry.literals
			}
		case pattern == "/":
			// The root route is the deterministic fallback: it must never
			// outrank a more specific match regardless of map iteration order.
			if rootEntry == nil {
				rootEntry = entry
			}
		default:
			if routeMatches(pattern, path) {
				if legacyEntry == nil || len(pattern) > len(legacyPattern) {
					legacyEntry, legacyPattern = entry, pattern
				}
			}
		}
	}
	switch {
	case paramEntry != nil:
		return paramEntry, paramPattern, true
	case legacyEntry != nil:
		return legacyEntry, legacyPattern, true
	case rootEntry != nil:
		return rootEntry, "/", true
	}
	return nil, "", false
}

// matchParams reports whether the normalized request path matches the
// compiled pattern. It walks the path segment by segment without allocating.
func (e *routeEntry) matchParams(path string) bool {
	if len(path) <= 1 {
		return false // "/" has no segments; param patterns need at least one
	}
	i := 0
	off := 1 // skip the leading '/'
	for {
		idx := strings.IndexByte(path[off:], '/')
		var seg string
		if idx < 0 {
			seg = path[off:]
		} else {
			seg = path[off : off+idx]
		}
		if i >= len(e.segments) {
			return false
		}
		if !e.segments[i].matchSegment(seg) {
			return false
		}
		i++
		if idx < 0 {
			break
		}
		off += idx + 1
	}
	return i == len(e.segments)
}

// matchSegment reports whether a request path segment matches this compiled
// segment: whole-segment params match any non-empty value, literals compare
// exactly, and mixed segments match part by part.
func (s routeSegment) matchSegment(seg string) bool {
	switch {
	case s.param != "":
		return seg != ""
	case s.parts != nil:
		return matchSegParts(s.parts, seg)
	default:
		return s.literal == seg
	}
}

// matchSegParts matches a mixed segment part by part. Literal parts must
// match exactly; parameter parts consume at least one byte. Returns the
// number of bytes consumed when the whole part list matches, or -1.
func matchSegParts(parts []segPart, seg string) bool {
	off := 0
	for pi, part := range parts {
		if part.param != "" {
			// A param part consumes up to the next literal part's prefix
			// (or the rest of the segment for the last part).
			var end int
			if pi+1 < len(parts) && parts[pi+1].param == "" {
				next := strings.Index(seg[off:], parts[pi+1].literal)
				if next < 0 {
					return false
				}
				end = off + next
			} else {
				end = len(seg)
			}
			if end == off {
				return false // param part must consume at least one byte
			}
			off = end
			continue
		}
		if !strings.HasPrefix(seg[off:], part.literal) {
			return false
		}
		off += len(part.literal)
	}
	return off == len(seg)
}

// paramValue extracts the value of the named parameter from a request path
// that already matched this pattern. Allocation-free; used by PathValue.
func (e *routeEntry) paramValue(path, name string) string {
	if len(path) <= 1 {
		return ""
	}
	i := 0
	off := 1
	for {
		idx := strings.IndexByte(path[off:], '/')
		var seg string
		if idx < 0 {
			seg = path[off:]
		} else {
			seg = path[off : off+idx]
		}
		if i >= len(e.segments) {
			return ""
		}
		s := e.segments[i]
		i++
		switch {
		case s.param != "" && s.param == name:
			return seg
		case s.parts != nil:
			if v, ok := segPartValue(s.parts, seg, name); ok {
				return v
			}
		}
		if idx < 0 {
			break
		}
		off += idx + 1
	}
	return ""
}

// segPartValue extracts the value of a named parameter part from a request
// segment that already matched the part list.
func segPartValue(parts []segPart, seg, name string) (string, bool) {
	off := 0
	for pi, part := range parts {
		if part.param != "" {
			var end int
			if pi+1 < len(parts) && parts[pi+1].param == "" {
				next := strings.Index(seg[off:], parts[pi+1].literal)
				if next < 0 {
					return "", false
				}
				end = off + next
			} else {
				end = len(seg)
			}
			if part.param == name {
				return seg[off:end], true
			}
			off = end
			continue
		}
		if !strings.HasPrefix(seg[off:], part.literal) {
			return "", false
		}
		off += len(part.literal)
	}
	return "", false
}

// compilePattern splits a normalized pattern into segments, recognizing
// "{name}" (stdlib style) and ":name" (chi/gin/go-zero style) single-segment
// parameters, plus mixed segments that combine literals and parameters
// (e.g. "post-{id}" or "{id}.json"). Malformed segments are treated as
// literals.
func compilePattern(pattern string) ([]routeSegment, bool) {
	trimmed := strings.TrimPrefix(pattern, "/")
	if trimmed == "" {
		return nil, false
	}
	parts := strings.Split(trimmed, "/")
	segments := make([]routeSegment, 0, len(parts))
	hasParams := false
	for _, part := range parts {
		if name, ok := paramSegmentName(part); ok {
			segments = append(segments, routeSegment{param: name})
			hasParams = true
			continue
		}
		if segParts, ok := compileSegParts(part); ok {
			segments = append(segments, routeSegment{parts: segParts})
			hasParams = true
			continue
		}
		segments = append(segments, routeSegment{literal: part})
	}
	return segments, hasParams
}

// compileSegParts compiles a mixed segment (literals interleaved with
// parameters) into an ordered part list. Returns ok=false when the segment
// contains no parameter at all (pure literal) or is malformed.
func compileSegParts(segment string) ([]segPart, bool) {
	var parts []segPart
	lit := strings.Builder{}
	flush := func() {
		if lit.Len() > 0 {
			parts = append(parts, segPart{literal: lit.String()})
			lit.Reset()
		}
	}
	for i := 0; i < len(segment); {
		c := segment[i]
		switch c {
		case '{':
			end := strings.IndexByte(segment[i:], '}')
			if end < 0 {
				lit.WriteByte(c)
				i++
				continue
			}
			name := segment[i+1 : i+end]
			if name == "" || !validParamName(name) {
				lit.WriteByte(c)
				i++
				continue
			}
			flush()
			parts = append(parts, segPart{param: name})
			i += end + 1
		case ':':
			// ":name" inside a segment runs to the next '.', '-', '_' or end.
			j := i + 1
			for j < len(segment) {
				d := segment[j]
				if d == '.' || d == '-' || d == '_' || d == '{' {
					break
				}
				if !validParamByte(d) {
					break
				}
				j++
			}
			if j == i+1 {
				lit.WriteByte(c)
				i++
				continue
			}
			flush()
			parts = append(parts, segPart{param: segment[i+1 : j]})
			i = j
		default:
			lit.WriteByte(c)
			i++
		}
	}
	flush()
	for _, p := range parts {
		if p.param != "" {
			return parts, true
		}
	}
	return nil, false
}

// validParamName reports whether a parameter name consists only of
// alphanumeric characters and underscores.
func validParamName(name string) bool {
	for _, r := range name {
		if !validParamByte(byte(r)) {
			return false
		}
	}
	return true
}

func validParamByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// paramSegmentName reports the parameter name when the whole segment is a
// well-formed single-segment parameter.
func paramSegmentName(segment string) (string, bool) {
	var name string
	switch {
	case strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}"):
		name = segment[1 : len(segment)-1]
	case strings.HasPrefix(segment, ":"):
		name = segment[1:]
	default:
		return "", false
	}
	if name == "" {
		return "", false
	}
	for _, r := range name {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !isAlnum && r != '_' {
			return "", false
		}
	}
	return name, true
}

// pathParamsKey is the context key under which the matched param route is
// stored.
type pathParamsKey struct{}

// routePatternKey is the context key under which the matched route pattern
// (e.g. "/users/{id}") is stored, for bounded-cardinality metrics/logging.
type routePatternKey struct{}

// routePattern returns the matched route pattern for the request, falling
// back to the raw path when no route matched (404s) or the pattern is
// unavailable. Callers that aggregate per route must use this instead of
// r.URL.Path to avoid unbounded cardinality from raw IDs.
func (r *Router) routePattern(req *http.Request) string {
	if req == nil {
		return ""
	}
	if pattern, ok := req.Context().Value(routePatternKey{}).(string); ok && pattern != "" {
		return pattern
	}
	return normalizePath(req.URL.Path)
}

// paramMatch carries the matched route and the request path so PathValue can
// extract values lazily, without building per-request value collections.
type paramMatch struct {
	entry *routeEntry
	path  string
}

// PathValue returns the value of the named path parameter captured by the
// router for this request (patterns like "/users/{id}" or "/users/:id"), or
// an empty string when the route has no such parameter.
func PathValue(req *http.Request, name string) string {
	if req == nil {
		return ""
	}
	m, _ := req.Context().Value(pathParamsKey{}).(paramMatch)
	if m.entry == nil {
		return ""
	}
	return m.entry.paramValue(m.path, name)
}

func routeMatches(route, path string) bool {
	route = normalizePath(route)
	path = normalizePath(path)
	if route == path {
		return true
	}
	if strings.HasSuffix(route, "/*") {
		basePath := strings.TrimSuffix(route, "/*")
		if basePath == "" {
			return true
		}
		return basePath == path || strings.HasPrefix(path, basePath+"/")
	}
	return strings.HasPrefix(path, route+"/")
}

func withWildcardRoute(prefix string) string {
	prefix = normalizePath(prefix)
	if prefix == "/" {
		return "/*"
	}
	return prefix + "/*"
}

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
}

// NewServer creates a server with a FGOTHS router as its handler.
func NewServer(addr string) *Server {
	if addr == "" {
		addr = ":8080"
	}
	r := NewRouter()
	return &Server{
		Server:  &http.Server{Addr: addr, Handler: r},
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
		_ = os.WriteFile(readyFile, []byte(os.Getenv("FGOTHS_BUILD_ID")), 0o644)
	}
	return s.Server.Serve(ln)
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

type requestIDContextKey string

const requestIDContextKeyValue requestIDContextKey = "fgoths.request.id"

// WithRequestID injects a request correlation ID into the request context and
// response headers. If an incoming trace or correlation header already exists,
// it is reused to keep diagnostics consistent across the call chain.
func (s *Server) WithRequestID() *Server {
	if s == nil || s.router == nil {
		return s
	}
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			requestID := ensureRequestID(r)
			w.Header().Set("X-Request-ID", requestID)
			w.Header().Set("X-Correlation-ID", requestID)
			w.Header().Set("X-Trace-ID", requestID)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDContextKeyValue, requestID)))
		})
	})
	return s
}

func ensureRequestID(r *http.Request) string {
	if r == nil {
		return ""
	}
	if requestID := RequestIDFromRequest(r); requestID != "" {
		return requestID
	}
	requestID := newRequestID()
	if r.Header == nil {
		r.Header = make(http.Header)
	}
	for _, header := range []string{"X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if r.Header.Get(header) == "" {
			r.Header.Set(header, requestID)
		}
	}
	return requestID
}

// RequestIDFromRequest extracts a request correlation ID from the current request
// context or the canonical inbound headers.
func RequestIDFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if id, ok := r.Context().Value(requestIDContextKeyValue).(string); ok && id != "" {
		return id
	}
	for _, header := range []string{"X-Request-ID", "X-Correlation-ID", "X-Trace-ID"} {
		if id := strings.TrimSpace(r.Header.Get(header)); id != "" {
			return id
		}
	}
	return ""
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "fgoths-" + hex.EncodeToString(b[:])
	}
	return "fgoths-" + time.Now().UTC().Format("20060102150405.000000000")
}
