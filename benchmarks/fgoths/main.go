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
	"net/http"
	"os"
	"sync"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

// Simulating a simple User model
type User struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

var (
	users   = make(map[int]User)
	usersMu sync.RWMutex
	nextID  = 1
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	seedData()

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
	w.Write([]byte(`{"status":"UP"}`))
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
