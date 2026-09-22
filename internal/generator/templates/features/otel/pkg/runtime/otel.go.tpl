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
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// WithOpenTelemetry adds request spans to the server and propagates trace headers.
func (s *Server) WithOpenTelemetry(serviceName string) *Server {
	if s == nil || s.router == nil {
		return s
	}
	if serviceName == "" {
		serviceName = "fgoths-runtime"
	}
	s.router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			requestID := ensureRequestID(r)
			ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := otel.Tracer(serviceName).Start(ctx, "fgoths.http.server")
			defer span.End()
			if requestID != "" {
				span.SetAttributes(attribute.String("request.id", requestID))
			}
			span.SetAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.route", normalizePath(r.URL.Path)),
				attribute.String("server.address", s.Addr),
				attribute.String("url.scheme", schemeFromRequest(r)),
			)
			if r.TLS != nil {
				span.SetAttributes(attribute.Bool("tls.enabled", true))
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
	return s
}

// WithOpenTelemetry adds tracing for upstream proxy requests and emits a child span around each gateway hop.
func (p *Proxy) WithOpenTelemetry(serviceName string) *Proxy {
	if p == nil {
		return nil
	}
	if serviceName == "" {
		serviceName = "fgoths-runtime"
	}
	previous := p.observer
	p.observer = func(req *http.Request, resp *http.Response, elapsed time.Duration) {
		if req == nil {
			if previous != nil {
				previous(req, resp, elapsed)
			}
			return
		}
		ctx, span := otel.Tracer(serviceName).Start(req.Context(), "fgoths.http.upstream")
		defer span.End()
		span.SetAttributes(
			attribute.String("http.method", req.Method),
			attribute.String("http.route", normalizePath(req.URL.Path)),
			attribute.String("upstream.target", p.TargetURL()),
			attribute.Int("http.response.status_code", statusCode(resp)),
			attribute.Int64("http.server.request.duration_ms", elapsed.Milliseconds()),
		)
		if resp != nil && resp.StatusCode >= http.StatusInternalServerError {
			span.SetAttributes(attribute.Bool("error", true))
		}
		if previous != nil {
			previous(req.WithContext(ctx), resp, elapsed)
		}
	}
	return p
}

func schemeFromRequest(r *http.Request) string {
	if r == nil {
		return "http"
	}
	if r.TLS != nil {
		return "https"
	}
	if r.URL != nil && r.URL.Scheme != "" {
		return r.URL.Scheme
	}
	return "http"
}

func statusCode(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

// TraceMiddleware creates a reusable request-tracing middleware compatible with the runtime router.
func TraceMiddleware(serviceName string) func(http.Handler) http.Handler {
	if serviceName == "" {
		serviceName = "fgoths-runtime"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r == nil {
				next.ServeHTTP(w, r)
				return
			}
			requestID := ensureRequestID(r)
			ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := otel.Tracer(serviceName).Start(ctx, "fgoths.http.middleware")
			defer span.End()
			if requestID != "" {
				span.SetAttributes(attribute.String("request.id", requestID))
			}
			span.SetAttributes(attribute.String("http.method", r.Method), attribute.String("http.route", normalizePath(r.URL.Path)))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SpanFromRequest extracts the current trace span for request context.
func SpanFromRequest(r *http.Request) trace.Span {
	if r == nil {
		return trace.SpanFromContext(context.Background())
	}
	return trace.SpanFromContext(r.Context())
}
