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

package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	goruntime "runtime"
	"testing"
	"time"
)

func TestHealthHandler(t *testing.T) {
	response := httptest.NewRecorder()
	healthHandler(response, httptest.NewRequest(http.MethodGet, "/health", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if got := response.Body.String(); got != `{"status":"UP"}` {
		t.Fatalf("body = %q, want health response", got)
	}
}

func TestBenchmarkRuntimeHandler(t *testing.T) {
	response := httptest.NewRecorder()
	benchmarkRuntimeHandler(response, httptest.NewRequest("GET", "/debug/bench/runtime", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if got := response.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var snapshot struct {
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
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode runtime snapshot: %v", err)
	}
	if snapshot.Timestamp.IsZero() {
		t.Fatal("timestamp is zero")
	}
	if snapshot.GoVersion != goruntime.Version() {
		t.Fatalf("go_version = %q, want %q", snapshot.GoVersion, goruntime.Version())
	}
	if snapshot.GOMAXPROCS <= 0 || snapshot.Goroutines <= 0 {
		t.Fatalf("invalid runtime counts: GOMAXPROCS=%d goroutines=%d", snapshot.GOMAXPROCS, snapshot.Goroutines)
	}
}
