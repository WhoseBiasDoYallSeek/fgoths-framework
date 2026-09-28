package main

import (
	"log"
	"net/http"
	"os"

	"database/sql"
	"webapp-demo/internal/database"

	"webapp-demo/internal/health"

	"webapp-demo/handlers"
	"webapp-demo/middleware"
	"webapp-demo/pkg/runtime"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var db *sql.DB
	var err error
	db, err = database.Open()
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer db.Close()

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

	handlers.RegisterCRUDRoutes(mux, db)

	// Health endpoints
	mux.HandleFunc("GET /health", health.Live)
	mux.HandleFunc("GET /health/live", health.Live)

	mux.HandleFunc("GET /health/ready", health.Ready(db))

	// Apply middleware
	handler := middleware.Logger(mux)
	server := runtime.NewServer(":" + port)

	server.Handler = handler

	if os.Getenv("FGOTHS_DEV") == "1" {
		server = server.WithReusePort().WithHMR()
	}

	addr := ":" + port
	log.Printf("webapp-demo listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}
