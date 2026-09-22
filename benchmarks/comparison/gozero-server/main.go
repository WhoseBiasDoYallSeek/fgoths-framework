// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// go-zero benchmark server for end-to-end (vegeta) comparisons.
//
// Mirrors benchmarks/fgoths: same endpoints, same JSON responses, same
// in-memory store — so throughput differences are attributable to the
// frameworks, not the workload. Runs on the full go-zero rest.Server
// (its middleware chain included), which is how go-zero runs in production.
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"

	"github.com/zeromicro/go-zero/rest"
)

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
		port = "8081"
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		log.Fatalf("invalid PORT %q: %v", port, err)
	}

	seedData()

	var c rest.RestConf
	c.Name = "gozero-bench"
	c.Port = portNum
	c.Timeout = 3000 // ms

	srv := rest.MustNewServer(c)

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/health", Handler: healthHandler})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/users", Handler: listUsers})
	srv.AddRoute(rest.Route{Method: http.MethodPost, Path: "/users", Handler: createUser})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/users/:id", Handler: getUser})

	log.Printf("🚀 go-zero Benchmark Server running on http://localhost:%s", port)
	srv.Start()
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

	// Benchmark parity with the FGOTHS server: both return the same fixed
	// user for any /users/<id>.
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
