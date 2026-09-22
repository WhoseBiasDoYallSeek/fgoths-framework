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
//
// Route dispatch benchmarks: FGOTHS vs stdlib (Go 1.22+ patterns) vs chi vs
// gin vs go-zero.
//
// Methodology notes (kept honest on purpose):
//
//   - Group 1/2 run in-process (httptest.NewRequest + ResponseRecorder): they
//     measure routing + handler dispatch only, no network stack.
//   - Group 3 runs over real loopback TCP (httptest.NewServer + http.Client),
//     which is the number that matters for real deployments.
//   - All five frameworks register a real single-segment param route
//     ("{id}" stdlib style; ":id" for gin/go-zero) and a static route.
package compbench

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/go-chi/chi/v5"
	zrouter "github.com/zeromicro/go-zero/rest/router"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

const (
	paramPath  = "/api/users/42"
	staticPath = "/health"
)

func jsonHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"id":42}`))
}

// --- handlers (in-process) ---------------------------------------------------

func fgothsSetup() http.Handler {
	r := runtime.NewRouter()
	r.Get("/api/users/{id}", jsonHandler)
	r.Get("/health", jsonHandler)
	return r
}

func stdlibSetup() http.Handler {
	mux := http.NewServeMux()
	// Modern Go 1.22+ pattern: method matching + "{id}" wildcard extraction.
	mux.HandleFunc("GET /api/users/{id}", jsonHandler)
	mux.HandleFunc("GET /health", jsonHandler)
	return mux
}

func chiSetup() http.Handler {
	r := chi.NewRouter()
	r.Get("/api/users/{id}", jsonHandler)
	r.Get("/health", jsonHandler)
	return r
}

func ginSetup() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.GET("/api/users/:id", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", []byte(`{"id":42}`))
	})
	r.GET("/health", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/json", []byte(`{"id":42}`))
	})
	return r
}

func gozeroSetup() http.Handler {
	r := zrouter.NewRouter()
	_ = r.Handle(http.MethodGet, "/api/users/:id", http.HandlerFunc(jsonHandler))
	_ = r.Handle(http.MethodGet, "/health", http.HandlerFunc(jsonHandler))
	return r
}

// --- in-process dispatch benchmarks ------------------------------------------

func benchmarkDispatch(b *testing.B, h http.Handler, path string) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
	}
}

func BenchmarkParamDispatchFGOTHS(b *testing.B) { benchmarkDispatch(b, fgothsSetup(), paramPath) }
func BenchmarkParamDispatchStdlib(b *testing.B) { benchmarkDispatch(b, stdlibSetup(), paramPath) }
func BenchmarkParamDispatchChi(b *testing.B)    { benchmarkDispatch(b, chiSetup(), paramPath) }
func BenchmarkParamDispatchGin(b *testing.B)    { benchmarkDispatch(b, ginSetup(), paramPath) }
func BenchmarkParamDispatchGoZero(b *testing.B) { benchmarkDispatch(b, gozeroSetup(), paramPath) }

func BenchmarkStaticDispatchFGOTHS(b *testing.B) { benchmarkDispatch(b, fgothsSetup(), staticPath) }
func BenchmarkStaticDispatchStdlib(b *testing.B) { benchmarkDispatch(b, stdlibSetup(), staticPath) }
func BenchmarkStaticDispatchChi(b *testing.B)    { benchmarkDispatch(b, chiSetup(), staticPath) }
func BenchmarkStaticDispatchGin(b *testing.B)    { benchmarkDispatch(b, ginSetup(), staticPath) }
func BenchmarkStaticDispatchGoZero(b *testing.B) { benchmarkDispatch(b, gozeroSetup(), staticPath) }

// --- end-to-end over real loopback TCP ---------------------------------------

var e2eClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 64,
	},
}

func benchmarkE2E(b *testing.B, h http.Handler, path string) {
	srv := httptest.NewServer(h)
	defer srv.Close()

	url := srv.URL + path
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := e2eClient.Get(url)
		if err != nil {
			b.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			b.Fatalf("unexpected status %d", resp.StatusCode)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

func BenchmarkE2EParamFGOTHS(b *testing.B) { benchmarkE2E(b, fgothsSetup(), paramPath) }
func BenchmarkE2EParamStdlib(b *testing.B) { benchmarkE2E(b, stdlibSetup(), paramPath) }
func BenchmarkE2EParamChi(b *testing.B)    { benchmarkE2E(b, chiSetup(), paramPath) }
func BenchmarkE2EParamGin(b *testing.B)    { benchmarkE2E(b, ginSetup(), paramPath) }
func BenchmarkE2EParamGoZero(b *testing.B) { benchmarkE2E(b, gozeroSetup(), paramPath) }

func BenchmarkE2EStaticFGOTHS(b *testing.B) { benchmarkE2E(b, fgothsSetup(), staticPath) }
func BenchmarkE2EStaticStdlib(b *testing.B) { benchmarkE2E(b, stdlibSetup(), staticPath) }
func BenchmarkE2EStaticChi(b *testing.B)    { benchmarkE2E(b, chiSetup(), staticPath) }
func BenchmarkE2EStaticGin(b *testing.B)    { benchmarkE2E(b, ginSetup(), staticPath) }
func BenchmarkE2EStaticGoZero(b *testing.B) { benchmarkE2E(b, gozeroSetup(), staticPath) }
