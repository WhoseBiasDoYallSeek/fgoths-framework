package main

import (
	"log"
	"net/http"
	"os"
	"strings"
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
	"{{.ProjectName}}/internal/assets"
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
	db, err := database.Open()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
	server := newServer(":"+port, db)
{{else}}
	server := newServer(":" + port)
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

{{if .UseDatabase}}
func newServer(addr string, db *sql.DB) *runtime.Server {
{{else}}
func newServer(addr string) *runtime.Server {
{{end}}
	server := runtime.NewServer(addr)

	staticFiles := http.StripPrefix("/static/", http.FileServer(http.Dir("static")))
	server.Handle(http.MethodGet, "/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("FGOTHS_DEV") == "1" {
			w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
			w.Header().Set("Pragma", "no-cache")
			w.Header().Set("Expires", "0")
		} else if assets.IsVersioned(strings.TrimPrefix(r.URL.Path, "/static/"), r.URL.Query().Get("v")) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		staticFiles.ServeHTTP(w, r)
	}))

	server.Get("/", handlers.IndexHandler)
	server.Get("/about", handlers.AboutHandler)
	server.Post("/api/counter/increment", handlers.CounterIncrementHandler)
	server.Post("/api/counter/decrement", handlers.CounterDecrementHandler)
	server.Any("/api/counter", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))

{{if .UseDatabase}}
	handlers.RegisterCRUDRoutes(server, db)
{{end}}

{{if .Has "health"}}
	server.Get("/health", health.Live)
	server.Get("/health/live", health.Live)
{{if .UseDatabase}}
	server.Get("/health/ready", health.Ready(db))
{{else}}
	server.Get("/health/ready", health.Ready(nil))
{{end}}
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

{{if .Has "metrics"}}
	server.Handle(http.MethodGet, "/metrics", metrics.Handler(server))
	server.Use(metrics.Middleware(server))
{{end}}
	server.Use(middleware.Logger)
	return server
}
