package main

import (
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	goruntime "runtime"
	"strings"
	"time"

	"database/sql"
	"real-app/internal/database"

	"real-app/internal/health"

	"real-app/handlers"
	"real-app/internal/assets"
	"real-app/middleware"
	"real-app/pkg/runtime"
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
	if os.Getenv("FGOTHS_BENCH_PROFILE") == "1" {
		profilePort := os.Getenv("FGOTHS_BENCH_PROFILE_PORT")
		if profilePort == "" {
			profilePort = "18201"
		}
		if err := startBenchmarkProfileServer(net.JoinHostPort("127.0.0.1", profilePort)); err != nil {
			log.Fatalf("benchmark profile server: %v", err)
		}
	}
	addr := ":" + port
	var server interface{ ListenAndServe() error }
	if os.Getenv("FGOTHS_BENCH_ROUTER") == "stdlib" {
		server = newStdlibServer(addr, db)
	} else {
		fgothsServer := newServer(addr, db)
		if os.Getenv("FGOTHS_DEV") == "1" {
			fgothsServer = fgothsServer.WithReusePort().WithHMR()
		}
		server = fgothsServer
	}
	log.Printf("real-app listening on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("server failed: %v", err)
	}
}

func newServer(addr string, db *sql.DB) *runtime.Server {
	server := runtime.NewServer(addr)
	registerApplicationRoutes(server, db)
	server.Use(middleware.Logger)
	return server
}

type stdlibRegistrar struct {
	mux *http.ServeMux
}

func (r stdlibRegistrar) Handle(method, path string, handler http.Handler) {
	if strings.HasSuffix(path, "/*") {
		path = strings.TrimSuffix(path, "*")
	}
	r.mux.Handle(method+" "+path, handler)
}

func newStdlibServer(addr string, db *sql.DB) *http.Server {
	mux := http.NewServeMux()
	registerApplicationRoutes(stdlibRegistrar{mux: mux}, db)
	return &http.Server{Addr: addr, Handler: middleware.Logger(mux), ReadHeaderTimeout: 10 * time.Second}
}

func registerApplicationRoutes(registrar runtime.Registrar, db *sql.DB) {
	staticFiles := http.StripPrefix("/static/", http.FileServer(http.Dir("static")))
	registrar.Handle(http.MethodGet, "/static/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	registrar.Handle(http.MethodGet, "/", http.HandlerFunc(handlers.IndexHandler))
	registrar.Handle(http.MethodGet, "/about", http.HandlerFunc(handlers.AboutHandler))
	registrar.Handle(http.MethodPost, "/api/counter/increment", http.HandlerFunc(handlers.CounterIncrementHandler))
	registrar.Handle(http.MethodPost, "/api/counter/decrement", http.HandlerFunc(handlers.CounterDecrementHandler))
	counterHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		registrar.Handle(method, "/api/counter", counterHandler)
	}

	handlers.RegisterCRUDRoutes(registrar, db)

	registrar.Handle(http.MethodGet, "/health", http.HandlerFunc(health.Live))
	registrar.Handle(http.MethodGet, "/health/live", http.HandlerFunc(health.Live))
	registrar.Handle(http.MethodGet, "/health/ready", health.Ready(db))
}

func startBenchmarkProfileServer(addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	goruntime.SetMutexProfileFraction(10)
	goruntime.SetBlockProfileRate(int(time.Millisecond))

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	for _, profile := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		mux.Handle("/debug/pprof/"+profile, pprof.Handler(profile))
	}

	go func() {
		if err := (&http.Server{Handler: mux}).Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("benchmark profile server stopped: %v", err)
		}
	}()
	log.Printf("benchmark profiles listening on http://%s (loopback only)", listener.Addr())
	return nil
}
