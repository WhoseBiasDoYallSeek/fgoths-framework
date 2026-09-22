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
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

type upstreamPayload struct {
	Service string `json:"service"`
	Method  string `json:"method"`
	Path    string `json:"path"`
	OK      bool   `json:"ok"`
}

func main() {
	upstream := &http.Server{
		Addr:              ":18080",
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			payload := upstreamPayload{
				Service: "orders-upstream",
				Method:  r.Method,
				Path:    strings.TrimPrefix(r.URL.Path, "/"),
				OK:      true,
			}
			if err := json.NewEncoder(w).Encode(payload); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}),
	}

	go func() {
		if err := upstream.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("upstream listener failed: %v", err)
		}
	}()

	proxy, err := runtime.NewProxy("http://localhost:18080")
	if err != nil {
		log.Fatalf("failed to create proxy: %v", err)
	}

	proxy.
		WithStripPrefix("/gateway").
		WithHeader("X-Proxy-Source", "fgoths-demo").
		WithHeader("Authorization", "Bearer demo-token").
		WithTimeout(3*time.Second).
		WithRetry(2, 150*time.Millisecond).
		WithRateLimit(10, 20).
		WithCircuitBreaker(3, 10*time.Second, 5*time.Second).
		WithMetrics().
		WithObserver(func(req *http.Request, resp *http.Response, took time.Duration) {
			log.Printf("proxy %s %s -> %d in %s", req.Method, req.URL.Path, resp.StatusCode, took.Round(time.Millisecond))
		})

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	server := runtime.NewServer(":8080").
		WithRequestID().
		WithMetrics().
		WithLogger(logger).
		WithLiveness("/health/live").
		WithReadiness("/health/ready", func(ctx context.Context) error {
			// In a real project this would ping a DB, cache, or upstream dependency.
			return nil
		}).
		WithAlertThreshold(0.5, func(snapshot runtime.MetricsSnapshot, errorRate float64) {
			log.Printf("ALERT: gateway error rate %.0f%% (total=%d errors=%d)", errorRate*100, snapshot.TotalRequests, snapshot.TotalErrors)
		})
	server.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"ok":true,"service":"gateway-demo","route":"/health"}`)
	})
	server.Get("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(server.Metrics().Snapshot())
	})
	server.Handle(http.MethodGet, "/gateway/*", proxy)
	server.Handle(http.MethodPost, "/gateway/*", proxy)
	server.Handle(http.MethodPut, "/gateway/*", proxy)
	server.Handle(http.MethodDelete, "/gateway/*", proxy)

	log.Printf("gateway-demo ready: http://localhost:8080/health")
	log.Printf("gateway-demo liveness: http://localhost:8080/health/live")
	log.Printf("gateway-demo readiness: http://localhost:8080/health/ready")
	log.Printf("gateway-demo metrics: http://localhost:8080/metrics")
	log.Printf("gateway-demo proxy: http://localhost:8080/gateway/orders/42")

	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("gateway server failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	_ = upstream.Shutdown(shutdownCtx)
}
