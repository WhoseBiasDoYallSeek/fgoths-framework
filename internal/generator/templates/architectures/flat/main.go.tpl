// Package main is the FGOTHS flat API entrypoint: a single main.go plus a
// handlers package, ready for a scratch container.
package main

import (
	"log"
{{if or (not (.Has "health")) (.Has "metrics") (.Has "grpc")}}
	"net/http"
{{end}}
	"os"

	"{{.ProjectName}}/handlers"
	"{{.ProjectName}}/pkg/runtime"
{{if .UseSQLite}}
	"{{.ProjectName}}/internal/database"
{{end}}
{{if .Has "health"}}
	"{{.ProjectName}}/internal/health"
{{end}}
{{if .Has "metrics"}}
	"{{.ProjectName}}/internal/metrics"
{{end}}
{{if .Has "openapi"}}
	"{{.ProjectName}}/internal/openapi"
{{end}}
{{if .Has "grpc"}}
	"{{.ProjectName}}/internal/grpcapi"
{{end}}
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := runtime.NewServer(":" + port)
{{if .UseSQLite}}
	db, err := database.Open()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
{{if .Has "health"}}
	server.Get("/health/ready", health.Ready(db))
{{end}}
{{end}}

	server.Get("/status", handlers.Status("{{.ProjectName}}"))

{{if .Has "health"}}
	server.Get("/health", health.Live)
	server.Get("/health/live", health.Live)
{{else}}
	server.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"UP"}`))
	})
{{end}}

{{if .Has "grpc"}}
	grpcServer, err := grpcapi.NewServer()
	if err != nil {
		log.Fatalf("grpc server: %v", err)
	}
	go func() {
		if err := grpcServer.ListenAndServe(":9090"); err != nil {
			log.Printf("grpc server stopped: %v", err)
		}
	}()
	server.Handle(http.MethodGet, "/health/grpc", grpcServer.HealthHTTP())
{{end}}

{{if .Has "openapi"}}
	server.Get("/docs", openapi.Docs)
	server.Get("/openapi.yaml", openapi.Spec)
{{end}}

{{if .Has "metrics"}}
	server.Handle(http.MethodGet, "/metrics", metrics.Handler(server))
	server.Use(metrics.Middleware(server))
{{end}}
	if os.Getenv("FGOTHS_DEV") == "1" {
		server = server.WithReusePort().WithHMR()
	}

	addr := ":" + port
	log.Printf("{{.ProjectName}} listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
