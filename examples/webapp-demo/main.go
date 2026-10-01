package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"database/sql"
	"webapp-demo/internal/database"

	"webapp-demo/internal/health"

	"webapp-demo/handlers"
	"webapp-demo/internal/assets"
	"webapp-demo/middleware"
	"webapp-demo/pkg/runtime"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	db, err := database.Open()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()
	server := newServer(":"+port, db)

	if os.Getenv("FGOTHS_DEV") == "1" {
		server = server.WithReusePort().WithHMR()
	}

	addr := ":" + port
	log.Printf("webapp-demo listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func newServer(addr string, db *sql.DB) *runtime.Server {

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

	handlers.RegisterCRUDRoutes(server, db)

	server.Get("/health", health.Live)
	server.Get("/health/live", health.Live)

	server.Get("/health/ready", health.Ready(db))

	server.Use(middleware.Logger)
	return server
}
