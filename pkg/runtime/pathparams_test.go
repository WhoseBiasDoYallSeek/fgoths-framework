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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func paramEchoHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":%q,"missing":%q}`, PathValue(req, "id"), PathValue(req, "missing"))
}

func TestPathParamsExtraction(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		wantID  string
	}{
		{"/users/{id}", "/users/42", "42"},
		{"/users/:id", "/users/42", "42"},
		{"/api/v1/users/{id}/posts/{postId}", "/api/v1/users/42/posts/7", "42"},
		{"/files/{id}", "/files/report.pdf", "report.pdf"},
		{"/files/{id}", "/files/unicode-日本語", "unicode-日本語"},
	}
	for _, tc := range tests {
		r := NewRouter()
		r.Get(tc.pattern, paramEchoHandler)
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s -> %s: status %d, want 200", tc.pattern, tc.path, res.Code)
		}
		want := fmt.Sprintf(`{"id":%q,"missing":%q}`, tc.wantID, "")
		if got := res.Body.String(); got != want {
			t.Errorf("%s -> %s: body %s, want %s", tc.pattern, tc.path, got, want)
		}
	}
}

func TestPathParamsPrecedence(t *testing.T) {
	r := NewRouter()
	r.Get("/users/me", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/users/{id}", paramEchoHandler)
	r.Get("/api/{a}/{b}", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `{"a":%q,"b":%q}`, PathValue(req, "a"), PathValue(req, "b"))
	})
	r.Get("/api/users/{id}", paramEchoHandler)

	// Static exact beats param.
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/users/me", nil))
	if res.Code != http.StatusOK || res.Body.Len() != 0 {
		t.Errorf("/users/me: status %d body %q, want static handler", res.Code, res.Body.String())
	}

	// More literal segments wins among param routes.
	res = httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/users/42", nil))
	if got := res.Body.String(); got != `{"id":"42","missing":""}` {
		t.Errorf("/api/users/42: body %q, want the /api/users/{id} handler", got)
	}

	// Param beats legacy prefix route.
	r2 := NewRouter()
	r2.Get("/users", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("list"))
	})
	r2.Get("/users/{id}", paramEchoHandler)
	res = httptest.NewRecorder()
	r2.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/users/42", nil))
	if got := res.Body.String(); got != `{"id":"42","missing":""}` {
		t.Errorf("/users/42 with /users prefix registered: body %q, want param handler", got)
	}
	res = httptest.NewRecorder()
	r2.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/users", nil))
	if got := res.Body.String(); got != "list" {
		t.Errorf("/users: body %q, want list handler", got)
	}
}

func TestPathParamsNoMatch(t *testing.T) {
	r := NewRouter()
	r.Get("/users/{id}", paramEchoHandler)

	for _, path := range []string{
		"/users",          // fewer segments
		"/users/42/extra", // more segments
		"/users/",         // normalized to /users
		"/users//",        // normalized to /users
		"/other/42",
	} {
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusNotFound {
			t.Errorf("%q: status %d, want 404", path, res.Code)
		}
	}

	// Empty segment between slashes must not satisfy a param.
	r.Get("/a/{id}/b", paramEchoHandler)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/a//b", nil))
	if res.Code != http.StatusNotFound {
		t.Errorf("/a//b: status %d, want 404 (empty segment is not a param value)", res.Code)
	}
}

func TestPathParamsMalformedPatternsAreLiterals(t *testing.T) {
	r := NewRouter()
	r.Get("/x/{}", paramEchoHandler)    // empty name -> literal
	r.Get("/y/{a b}", paramEchoHandler) // invalid name -> literal
	r.Get("/z/:", paramEchoHandler)     // empty name -> literal
	r.Get("/w/{a-b}", paramEchoHandler) // invalid name -> literal

	for _, path := range []string{"/x/{}", "/y/{a%20b}", "/z/:", "/w/{a-b}"} {
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
		if res.Code != http.StatusOK {
			t.Errorf("%q: status %d, want 200 (malformed segment treated as literal)", path, res.Code)
		}
	}
}

func TestPathParamsWildcardStillWorks(t *testing.T) {
	r := NewRouter()
	r.Get("/static/*", func(w http.ResponseWriter, req *http.Request) {
		_, _ = w.Write([]byte("wildcard:" + strings.TrimPrefix(req.URL.Path, "/static/")))
	})
	r.Get("/files/{id}", paramEchoHandler)

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/static/js/app.js", nil))
	if got := res.Body.String(); got != "wildcard:js/app.js" {
		t.Errorf("wildcard route broken: body %q", got)
	}

	// Param route wins over a wildcard covering the same depth.
	r.Get("/files/*", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("wildcard")) })
	res = httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/files/a", nil))
	if got := res.Body.String(); got != `{"id":"a","missing":""}` {
		t.Errorf("param should beat wildcard: body %q", got)
	}
}

func TestPathParamsRootFallbackDeterministic(t *testing.T) {
	// Regression: the root route used to be able to outrank a more specific
	// match depending on map iteration order.
	for i := 0; i < 50; i++ {
		r := NewRouter()
		r.Get("/", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("root")) })
		r.Get("/api/*", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("api")) })

		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/api/users", nil))
		if got := res.Body.String(); got != "api" {
			t.Fatalf("iteration %d: /api/users served %q, want api handler", i, got)
		}
	}
}

func TestPathParamsWithMiddlewareAndServer(t *testing.T) {
	r := NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// Middleware must be able to read path params too.
			w.Header().Set("X-Param", PathValue(req, "id"))
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/users/{id}", paramEchoHandler)

	req := httptest.NewRequest(http.MethodGet, "/users/7", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if got := res.Header().Get("X-Param"); got != "7" {
		t.Errorf("middleware saw param %q, want 7", got)
	}
	if got := res.Body.String(); got != `{"id":"7","missing":""}` {
		t.Errorf("handler body %q", got)
	}

	// Server delegates to the same router behavior.
	s := NewServer(":0")
	s.Get("/orders/{oid}", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `{"oid":%q}`, PathValue(req, "oid"))
	})
	res = httptest.NewRecorder()
	s.router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/orders/99", nil))
	if got := res.Body.String(); got != `{"oid":"99"}` {
		t.Errorf("server route body %q", got)
	}
}

func TestPathValueNilRequest(t *testing.T) {
	if got := PathValue(nil, "id"); got != "" {
		t.Errorf("PathValue(nil) = %q, want empty", got)
	}
}

// mixedEchoHandler echoes two mixed-segment params.
func mixedEchoHandler(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"id":%q,"ext":%q}`, PathValue(req, "id"), PathValue(req, "ext"))
}

func TestPartialSegmentParams(t *testing.T) {
	tests := []struct {
		pattern    string
		path       string
		wantStatus int
		wantID     string
		wantExt    string
	}{
		// {id}.json — param prefix, literal suffix (".json" is a literal,
		// so ext stays empty; the file id is extracted).
		{"/files/{id}.json", "/files/report.json", http.StatusOK, "report", ""},
		// post-{id} — literal prefix, param suffix.
		{"/posts/post-{id}", "/posts/post-0042", http.StatusOK, "0042", ""},
		// v{v}-api — literal on both sides of the param; the handler echoes
		// "id" which this pattern does not define, so both stay empty.
		{"/api/v{v}-api/status", "/api/v2-api/status", http.StatusOK, "", ""},
		// :id.json — gin-style partial param with literal suffix.
		{"/files/:id.json", "/files/data.json", http.StatusOK, "data", ""},
		// No match: literal part differs.
		{"/posts/post-{id}", "/posts/page-0042", http.StatusNotFound, "", ""},
		// No match: param part empty (".json" alone).
		{"/files/{id}.json", "/files/.json", http.StatusNotFound, "", ""},
		// No match: suffix missing.
		{"/files/{id}.json", "/files/report", http.StatusNotFound, "", ""},
	}
	for _, tc := range tests {
		r := NewRouter()
		r.Get(tc.pattern, mixedEchoHandler)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if res.Code != tc.wantStatus {
			t.Errorf("%s -> %s: status %d, want %d", tc.pattern, tc.path, res.Code, tc.wantStatus)
			continue
		}
		if tc.wantStatus != http.StatusOK {
			continue
		}
		want := fmt.Sprintf(`{"id":%q,"ext":%q}`, tc.wantID, tc.wantExt)
		if got := res.Body.String(); got != want {
			t.Errorf("%s -> %s: body %s, want %s", tc.pattern, tc.path, got, want)
		}
	}
}

func TestPartialSegmentParamsPrecedence(t *testing.T) {
	r := NewRouter()
	r.Get("/posts/post-{id}", mixedEchoHandler)
	r.Get("/posts/{slug}", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `{"slug":%q}`, PathValue(req, "slug"))
	})

	// The more specific mixed segment wins over the whole-segment param.
	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/posts/post-0042", nil))
	if got := res.Body.String(); got != `{"id":"0042","ext":""}` {
		t.Errorf("/posts/post-0042: body %s, want the mixed-segment handler", got)
	}

	// Non-matching slugs fall through to the whole-segment param.
	res = httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/posts/anything-else", nil))
	if got := res.Body.String(); got != `{"slug":"anything-else"}` {
		t.Errorf("/posts/anything-else: body %s, want the slug handler", got)
	}
}

func TestPartialSegmentMultipleParams(t *testing.T) {
	r := NewRouter()
	r.Get("/files/{name}.{ext}", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"name":%q,"ext":%q}`, PathValue(req, "name"), PathValue(req, "ext"))
	})

	res := httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/files/report.pdf", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("/files/report.pdf: status %d, want 200", res.Code)
	}
	if got, want := res.Body.String(), `{"name":"report","ext":"pdf"}`; got != want {
		t.Errorf("/files/report.pdf: body %s, want %s", got, want)
	}

	// Non-greedy params: a param part stops at the FIRST occurrence of the
	// next literal part (same semantics as gin/chi segment params).
	res = httptest.NewRecorder()
	r.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/files/archive.tar.gz", nil))
	if got, want := res.Body.String(), `{"name":"archive","ext":"tar.gz"}`; got != want {
		t.Errorf("/files/archive.tar.gz: body %s, want %s", got, want)
	}
}
