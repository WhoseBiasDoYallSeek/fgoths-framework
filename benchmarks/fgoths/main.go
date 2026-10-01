// Copyright 2026 FGOTHS Framework Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Benchmark server for end-to-end (vegeta) comparisons.
//
// This server runs on the real FGOTHS runtime (pkg/runtime) — not on stdlib —
// so throughput numbers measured against it are attributable to the framework.
package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	goruntime "runtime"
	"sync"
	"time"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

// Simulating a simple User model
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

var (
	users              = make(map[int]User)
	usersMu            sync.RWMutex
	nextID             = 1
	healthResponseBody = []byte(`{"status":"UP"}`)
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	seedData()

	if os.Getenv("FGOTHS_BENCH_PROFILE") == "1" {
		profilePort := os.Getenv("FGOTHS_BENCH_PROFILE_PORT")
		if profilePort == "" {
			profilePort = "18081"
		}
		if err := startProfileServer(net.JoinHostPort("127.0.0.1", profilePort)); err != nil {
			log.Fatalf("start benchmark profile server: %v", err)
		}
	}

	srv := runtime.NewServer(":" + port)

	// Health check
	srv.Get("/health", healthHandler)

	// User endpoints. The runtime router matches and extracts `{id}` via a
	// lazy context-injected PathValue (no per-request allocations for values).
	srv.Get("/users", listUsers)
	srv.Post("/users", createUser)
	srv.Get("/users/{id}", getUser)

	log.Printf("🚀 FGOTHS Benchmark Server (runtime router) running on http://localhost:%s", port)
	log.Fatal(srv.ListenAndServe())
}

func startProfileServer(addr string) error {
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
	mux.HandleFunc("/debug/bench/runtime", benchmarkRuntimeHandler)

	go func() {
		if err := (&http.Server{Handler: mux}).Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("benchmark profile server failed: %v", err)
		}
	}()
	log.Printf("benchmark diagnostics listening on http://%s (loopback only)", listener.Addr())
	return nil
}

func benchmarkRuntimeHandler(w http.ResponseWriter, _ *http.Request) {
	var memory goruntime.MemStats
	goruntime.ReadMemStats(&memory)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(struct {
		Timestamp     time.Time `json:"timestamp"`
		GoVersion     string    `json:"go_version"`
		GOMAXPROCS    int       `json:"gomaxprocs"`
		Goroutines    int       `json:"goroutines"`
		HeapAlloc     uint64    `json:"heap_alloc_bytes"`
		HeapInuse     uint64    `json:"heap_inuse_bytes"`
		HeapSys       uint64    `json:"heap_sys_bytes"`
		TotalAlloc    uint64    `json:"total_alloc_bytes"`
		NumGC         uint32    `json:"num_gc"`
		PauseTotalNS  uint64    `json:"pause_total_ns"`
		LastPauseTime uint64    `json:"last_pause_ns"`
	}{
		Timestamp:     time.Now().UTC(),
		GoVersion:     goruntime.Version(),
		GOMAXPROCS:    goruntime.GOMAXPROCS(0),
		Goroutines:    goruntime.NumGoroutine(),
		HeapAlloc:     memory.HeapAlloc,
		HeapInuse:     memory.HeapInuse,
		HeapSys:       memory.HeapSys,
		TotalAlloc:    memory.TotalAlloc,
		NumGC:         memory.NumGC,
		PauseTotalNS:  memory.PauseTotalNs,
		LastPauseTime: memory.PauseNs[(memory.NumGC+255)%256],
	}); err != nil {
		log.Printf("write benchmark runtime diagnostics: %v", err)
	}
}

func seedData() {
	usersMu.Lock()
	defer usersMu.Unlock()
	for i := 1; i <= 100; i++ {
		users[i] = User{
			ID:    i,
			Name:  "User " + string(rune(i)),
			Email: "user" + string(rune(i)) + "@example.com",
		}
	}
	nextID = 101
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(healthResponseBody)
}

func listUsers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	usersMu.RLock()
	userList := make([]User, 0, len(users))
	for _, u := range users {
		userList = append(userList, u)
	}
	usersMu.RUnlock()

	json.NewEncoder(w).Encode(userList)
}

func getUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Simplified for benchmark parity with the go-zero server: both return
	// the same fixed user for any /users/<id>. The param is still extracted
	// (PathValue) so the benchmark exercises the real param dispatch path.
	_ = runtime.PathValue(r, "id")
	id := 1

	usersMu.RLock()
	user, ok := users[id]
	usersMu.RUnlock()

	if !ok {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	json.NewEncoder(w).Encode(user)
}

func createUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	usersMu.Lock()
	user.ID = nextID
	nextID++
	users[user.ID] = user
	usersMu.Unlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}
