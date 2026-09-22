// Package main is the FGOTHS flat API entrypoint: a single main.go plus a
// handlers package, ready for a scratch container.
package main

import (
	"log"
	"net/http"
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

	mux := http.NewServeMux()

{{if .UseSQLite}}
	db, err := database.Open()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
{{if .Has "health"}}
	mux.HandleFunc("GET /health/ready", health.Ready(db))
{{end}}
{{end}}

	mux.HandleFunc("GET /status", handlers.Status("{{.ProjectName}}"))

{{if .Has "health"}}
	mux.HandleFunc("GET /health", health.Live)
	mux.HandleFunc("GET /health/live", health.Live)
{{else}}
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"UP"}`))
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
	mux.Handle("GET /health/grpc", grpcServer.HealthHTTP())
{{end}}

{{if .Has "openapi"}}
	mux.HandleFunc("GET /docs", openapi.Docs)
	mux.HandleFunc("GET /openapi.yaml", openapi.Spec)
{{end}}

	server := runtime.NewServer(":" + port)
{{if .Has "metrics"}}
	mux.Handle("GET /metrics", metrics.Handler(server))
	server.Handler = metrics.Middleware(server)(mux)
{{else}}
	server.Handler = mux
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
