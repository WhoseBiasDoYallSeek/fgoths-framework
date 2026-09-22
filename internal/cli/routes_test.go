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
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanRoutesFindsMethodAndPathlessRegistrations(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.MkdirAll(filepath.Join("internal", "interface", "http"), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		source := `package httpapi

import "net/http"

func Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", nil)
	mux.HandleFunc("/", nil)
	mux.Handle("GET /metrics", nil)
}
`
		if err := os.WriteFile(filepath.Join("internal", "interface", "http", "routes.go"), []byte(source), 0o644); err != nil {
			t.Fatalf("write routes.go: %v", err)
		}

		routes, err := scanRoutes(".")
		if err != nil {
			t.Fatalf("scanRoutes failed: %v", err)
		}
		if len(routes) != 3 {
			t.Fatalf("expected 3 routes, got %d: %+v", len(routes), routes)
		}

		byPath := make(map[string]routeEntry)
		for _, route := range routes {
			byPath[route.Path] = route
		}
		if got := byPath["/health"].Method; got != "GET" {
			t.Fatalf("expected GET /health, got method %q", got)
		}
		if got := byPath["/"].Method; got != "ANY" {
			t.Fatalf("expected ANY for a pathless-method route, got %q", got)
		}
		if got := byPath["/metrics"].Method; got != "GET" {
			t.Fatalf("expected GET /metrics, got %q", got)
		}
	})
}

func TestScanRoutesIgnoresTestFiles(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.WriteFile("routes_test.go", []byte(`package cli
func x() { mux.HandleFunc("GET /ignored", nil) }
`), 0o644); err != nil {
			t.Fatalf("write test file: %v", err)
		}
		routes, err := scanRoutes(".")
		if err != nil {
			t.Fatalf("scanRoutes failed: %v", err)
		}
		if len(routes) != 0 {
			t.Fatalf("expected test files to be ignored, got %+v", routes)
		}
	})
}

func TestRunRoutesRequiresGoMod(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() { RunRoutes(nil) })
		if !strings.Contains(output, "go.mod not found") {
			t.Fatalf("expected a go.mod hint, got %q", output)
		}
	})
}

func TestRunRoutesPrintsDiscoveredRoutes(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\ngo 1.27\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		if err := os.WriteFile("main.go", []byte(`package main
func main() { mux.HandleFunc("GET /health", nil) }
`), 0o644); err != nil {
			t.Fatalf("write main.go: %v", err)
		}

		output := captureStdout(t, func() { RunRoutes(nil) })
		if !strings.Contains(output, "GET") || !strings.Contains(output, "/health") {
			t.Fatalf("expected the discovered route in output, got %q", output)
		}
	})
}

func TestRunRoutesReportsNoRoutes(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\ngo 1.27\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		output := captureStdout(t, func() { RunRoutes(nil) })
		if !strings.Contains(output, "No routes found") {
			t.Fatalf("expected a no-routes message, got %q", output)
		}
	})
}
