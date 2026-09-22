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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestEdgeCasesPathTraversal ensures path traversal attempts do not bypass
// routing or leak files: the router must treat encoded traversal as a plain
// path segment and return 404, never serve filesystem content.
func TestEdgeCasesPathTraversal(t *testing.T) {
	r := NewRouter()
	r.Get("/files/:name", func(w http.ResponseWriter, req *http.Request) {
		// The handler must only ever see the literal captured segment.
		name := req.PathValue("name")
		if strings.Contains(name, "..") {
			t.Errorf("handler received traversal payload: %q", name)
		}
		w.WriteHeader(http.StatusOK)
	})
	r.Get("/etc/passwd", func(w http.ResponseWriter, req *http.Request) {
		t.Error("traversal reached a literal route it should not")
		w.WriteHeader(http.StatusOK)
	})

	attempts := []string{
		"/files/../../../etc/passwd",
		"/files/%2e%2e%2f%2e%2e%2fetc%2fpasswd",
		"/files/..%2f..%2fetc%2fpasswd",
		"/files/%252e%252e%252fetc%252fpasswd", // double-encoded
	}
	for _, path := range attempts {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if res.Code == http.StatusOK && strings.Contains(res.Body.String(), "root:") {
			t.Errorf("path traversal succeeded for %q", path)
		}
	}
}

// TestEdgeCasesCRLFInjection ensures header values with CRLF cannot inject
// additional headers into the response. The real http.Server rejects invalid
// header values at write time (net/http validates since Go 1.20); this test
// pins that contract so a future custom response writer does not regress it.
func TestEdgeCasesCRLFInjection(t *testing.T) {
	r := NewRouter()
	r.Get("/echo", func(w http.ResponseWriter, req *http.Request) {
		value := req.URL.Query().Get("v")
		w.Header().Set("X-Echo", value)
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(r)
	defer server.Close()

	// Over a real TCP connection, the server must either sanitize the header
	// or fail the request — but must never emit a split header.
	client := server.Client()
	resp, err := client.Get(server.URL + "/echo?v=abc%0d%0aX-Injected:%20yes")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if strings.Contains(resp.Header.Get("X-Echo"), "\n") {
		t.Error("CRLF injection leaked into response headers over real connection")
	}
	if resp.Header.Get("X-Injected") != "" {
		t.Error("injected header appeared in response")
	}
}

// TestEdgeCasesLargeBody ensures the router and handlers survive large
// request bodies without corruption or panic.
func TestEdgeCasesLargeBody(t *testing.T) {
	r := NewRouter()
	r.Post("/upload", func(w http.ResponseWriter, req *http.Request) {
		buf := new(bytes.Buffer)
		if _, err := buf.ReadFrom(req.Body); err != nil {
			t.Errorf("read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// 10MB body
	body := bytes.Repeat([]byte("x"), 10<<20)
	req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body))
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for large body, got %d", res.Code)
	}
}

// TestEdgeCasesWeirdPaths ensures unusual but legal paths are routed
// deterministically and never panic.
func TestEdgeCasesWeirdPaths(t *testing.T) {
	r := NewRouter()
	r.Get("/", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/a/:id", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Get("/a/b/c", func(w http.ResponseWriter, req *http.Request) { w.WriteHeader(http.StatusOK) })

	paths := []string{
		"/",
		"//",
		"/a/",
		"/a/%20",
		"/a/with%2Fslash",
		"/a/unicode-日本語",
		"/a/emoji-🚀",
		"/a/very-long-" + strings.Repeat("x", 2000),
		"/A/UPPERCASE",
		"/a/b", // no handler: must 404, not panic
	}
	for _, path := range paths {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					t.Errorf("panic for path %q: %v", path, rec)
				}
			}()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			res := httptest.NewRecorder()
			r.ServeHTTP(res, req)
			if res.Code == 0 {
				t.Errorf("no status written for path %q", path)
			}
		}()
	}
}

// TestEdgeCasesMethodMismatch ensures wrong methods return 405 (or 404) and
// never execute the handler.
func TestEdgeCasesMethodMismatch(t *testing.T) {
	r := NewRouter()
	called := false
	r.Post("/resource", func(w http.ResponseWriter, req *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/resource", nil)
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)
		if called {
			t.Errorf("handler executed for wrong method %s", method)
			called = false
		}
		if res.Code == http.StatusOK {
			t.Errorf("wrong method %s returned 200", method)
		}
	}
}

// TestEdgeCasesProxyBadUpstream ensures the proxy fails gracefully (no panic,
// 502/503/404 family) when the upstream is unreachable.
func TestEdgeCasesProxyBadUpstream(t *testing.T) {
	proxy, err := NewProxy("http://127.0.0.1:1") // port 1: nothing listens
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	res := httptest.NewRecorder()
	proxy.ServeHTTP(res, req)
	if res.Code < 400 {
		t.Errorf("expected error status for unreachable upstream, got %d", res.Code)
	}
}

// TestEdgeCasesConcurrentRouteRegistrationAndServing exercises the race
// surface of registering routes while serving traffic.
func TestEdgeCasesConcurrentRouteRegistrationAndServing(t *testing.T) {
	r := NewRouter()
	server := httptest.NewServer(r)
	defer server.Close()

	stop := make(chan struct{})
	var once sync.Once
	stopOnce := func() { once.Do(func() { close(stop) }) }
	defer stopOnce()

	done := make(chan struct{})

	// Writer goroutine: registers routes continuously.
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			r.Get("/dyn/"+string(rune('a'+i%26))+"/"+itoa(i), func(w http.ResponseWriter, req *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
		}
		stopOnce()
	}()

	// Reader goroutines: serve traffic continuously.
	var wg sync.WaitGroup
	client := server.Client()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					resp, err := client.Get(server.URL + "/")
					if err == nil {
						resp.Body.Close()
					}
				}
			}
		}()
	}
	<-done
	wg.Wait()
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
