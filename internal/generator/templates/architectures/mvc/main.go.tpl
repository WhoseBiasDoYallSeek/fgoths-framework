package main

import (
	"log"
	"net/http"
	"os"
{{if .UseDatabase}}
	"database/sql"
	"{{.ProjectName}}/internal/database"
{{end}}
{{if .Has "health"}}
	"{{.ProjectName}}/internal/health"
{{end}}
{{if .Has "metrics"}}
	"{{.ProjectName}}/internal/metrics"
{{end}}
	"{{.ProjectName}}/handlers"
	"{{.ProjectName}}/middleware"
	"{{.ProjectName}}/pkg/runtime"
{{if .Has "grpc"}}
	"{{.ProjectName}}/internal/interface/grpcapi"
{{end}}
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

{{if .UseDatabase}}
	var db *sql.DB
	var err error
	db, err = database.Open()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
{{end}}

	mux := http.NewServeMux()

	// Static assets (embedded)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Routes
	mux.HandleFunc("/", handlers.IndexHandler)
	mux.HandleFunc("/about", handlers.AboutHandler)

	// HTMX API endpoints
	mux.HandleFunc("/api/counter/increment", handlers.CounterIncrementHandler)
	mux.HandleFunc("/api/counter/decrement", handlers.CounterDecrementHandler)

	// REST API endpoints (GET/PUT/DELETE)
	mux.HandleFunc("/api/counter", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			handlers.CounterGetHandler(w, r)
		case http.MethodPut:
			handlers.CounterPutHandler(w, r)
		case http.MethodDelete:
			handlers.CounterDeleteHandler(w, r)
		default:
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		}
	})

        // CRUD endpoints generated with `fgoths generate crud` (no-op if none).
{{if .UseDatabase}}
        handlers.RegisterCRUDRoutes(mux, db)
{{end}}

{{if .Has "health"}}
        // Health endpoints
	mux.HandleFunc("GET /health", health.Live)
	mux.HandleFunc("GET /health/live", health.Live)
{{if .UseDatabase}}
	mux.HandleFunc("GET /health/ready", health.Ready(db))
{{else}}
	mux.HandleFunc("GET /health/ready", health.Ready(nil))
{{end}}
{{end}}

{{if .Has "grpc"}}
	// gRPC-style server on :9090 with health endpoint on the HTTP mux
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

	// Apply middleware
	handler := middleware.Logger(mux)
	server := runtime.NewServer(":" + port)
{{if .Has "metrics"}}
	// Prometheus metrics: route + request recording middleware
	mux.Handle("GET /metrics", metrics.Handler(server))
	server.Handler = metrics.Middleware(server)(handler)
{{else}}
	server.Handler = handler
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
