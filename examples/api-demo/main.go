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

	server := runtime.NewServer(":" + port)

	server.Get("/status", handlers.Status("api-demo"))

	server.Get("/health", health.Live)
	server.Get("/health/live", health.Live)

	server.Get("/docs", openapi.Docs)
	server.Get("/openapi.yaml", openapi.Spec)

	server.Handle(http.MethodGet, "/metrics", metrics.Handler(server))
	server.Use(metrics.Middleware(server))

	if os.Getenv("FGOTHS_DEV") == "1" {
		server = server.WithReusePort().WithHMR()
	}

	addr := ":" + port
	log.Printf("api-demo listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
