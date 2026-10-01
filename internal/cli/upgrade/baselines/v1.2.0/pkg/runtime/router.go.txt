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
	"net/http"
	"strings"
	"sync"
	"unicode/utf8"
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

// ServeHTTP dispatches a request to the configured route. The matched route
// metadata (pattern + lazy param match) is injected once per request in a
// single context value — one allocation total — and read back with PathValue
// and routePattern. Static routes take the fast path with zero allocations.
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
	// A single context carries both the matched pattern (for bounded-
	// cardinality metrics/logging) and the lazy param match (for PathValue),
	// so dispatch costs one WithContext instead of two. Static exact-match
	// routes skip it entirely: the pattern equals the raw path (routePattern
	// falls back to it) and there are no params to extract — zero context
	// allocations on the hot static path.
	if entry.hasParams || pattern != path {
		req = req.WithContext(context.WithValue(req.Context(), routeInfoKey{}, routeInfo{pattern: pattern, params: paramMatch{entry: entry, path: path}}))
	}
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
		// Reject non-ASCII runes before narrowing: byte(r) would truncate
		// e.g. U+0167 to 'g' and wrongly accept it.
		if r >= utf8.RuneSelf || !validParamByte(byte(r)) { //nolint:gosec // G115: guarded by r < utf8.RuneSelf above
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

// routeInfoKey is the context key under which the matched route metadata
// (pattern + lazy param match) is stored — a single value per request.
type routeInfoKey struct{}

// routeInfo bundles the matched route pattern (for bounded-cardinality
// metrics/logging) and the lazy param match (for PathValue) into one
// context value, so dispatch costs a single WithContext allocation.
type routeInfo struct {
	pattern string
	params  paramMatch
}

// routePattern returns the matched route pattern for the request, falling
// back to the raw path when no route matched (404s) or the pattern is
// unavailable. Callers that aggregate per route must use this instead of
// r.URL.Path to avoid unbounded cardinality from raw IDs.
func (r *Router) routePattern(req *http.Request) string {
	if req == nil {
		return ""
	}
	if info, ok := req.Context().Value(routeInfoKey{}).(routeInfo); ok && info.pattern != "" {
		return info.pattern
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
	info, _ := req.Context().Value(routeInfoKey{}).(routeInfo)
	if info.params.entry == nil {
		return ""
	}
	return info.params.entry.paramValue(info.params.path, name)
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
