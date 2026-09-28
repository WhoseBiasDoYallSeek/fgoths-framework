// Package main is the FGOTHS flat API entrypoint: a single main.go plus a
// handlers package, ready for a scratch container.
package main

import (
	"log"
	"net/http"
	"os"

	"api-demo/handlers"
	"api-demo/pkg/runtime"

	"api-demo/internal/health"

	"api-demo/internal/metrics"

	"api-demo/internal/openapi"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", handlers.Status("api-demo"))

	mux.HandleFunc("GET /health", health.Live)
	mux.HandleFunc("GET /health/live", health.Live)

	mux.HandleFunc("GET /docs", openapi.Docs)
	mux.HandleFunc("GET /openapi.yaml", openapi.Spec)

	server := runtime.NewServer(":" + port)

	mux.Handle("GET /metrics", metrics.Handler(server))
	server.Handler = metrics.Middleware(server)(mux)

	if os.Getenv("FGOTHS_DEV") == "1" {
		server = server.WithReusePort().WithHMR()
	}

	addr := ":" + port
	log.Printf("api-demo listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
