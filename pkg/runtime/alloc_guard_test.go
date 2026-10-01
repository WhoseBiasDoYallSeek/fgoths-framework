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
	"testing"
)

// nopResponseWriter isolates router allocations from recorder overhead.
type nopResponseWriter struct{ h http.Header }

func (w *nopResponseWriter) Header() http.Header         { return w.h }
func (w *nopResponseWriter) Write(b []byte) (int, error) { return len(b), nil }
func (w *nopResponseWriter) WriteHeader(int)             {}

// Allocation ceilings for the hot dispatch path, measured in isolation (no
// httptest.Recorder overhead). These are deterministic regression gates that
// run on every `go test`, unlike the noisy nightly benchmarks. If a change
// legitimately needs more allocations, raise the ceiling in the same PR and
// justify it in the CHANGELOG.
const (
	maxAllocsStaticDispatch  = 0 // exact-match static route: no context injection
	maxAllocsParamDispatch   = 0 // pattern recorded in place; PathValue is lazy
	maxAllocsMetricsDispatch = 1 // param dispatch + metrics (pooled statusRecorder)
)

func newAllocGuardRouter() *Router {
	r := NewRouter()
	r.Handle(http.MethodGet, "/health", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r.Handle(http.MethodGet, "/users/{id}", http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		_ = PathValue(req, "id")
	}))
	return r
}

func TestDispatchAllocationCeilings(t *testing.T) {
	r := newAllocGuardRouter()
	w := &nopResponseWriter{h: http.Header{}}
	static := httptest.NewRequest(http.MethodGet, "/health", nil)
	param := httptest.NewRequest(http.MethodGet, "/users/42", nil)

	if got := testing.AllocsPerRun(1000, func() { r.ServeHTTP(w, static) }); got > maxAllocsStaticDispatch {
		t.Errorf("static dispatch allocs = %v, ceiling %d", got, maxAllocsStaticDispatch)
	}
	if got := testing.AllocsPerRun(1000, func() { r.ServeHTTP(w, param) }); got > maxAllocsParamDispatch {
		t.Errorf("param dispatch allocs = %v, ceiling %d", got, maxAllocsParamDispatch)
	}

	s := NewServer(":0").WithMetrics()
	s.Get("/users/{id}", func(_ http.ResponseWriter, req *http.Request) { _ = PathValue(req, "id") })
	if got := testing.AllocsPerRun(1000, func() { s.Handler.ServeHTTP(w, param) }); got > maxAllocsMetricsDispatch {
		t.Errorf("metrics dispatch allocs = %v, ceiling %d", got, maxAllocsMetricsDispatch)
	}
}
