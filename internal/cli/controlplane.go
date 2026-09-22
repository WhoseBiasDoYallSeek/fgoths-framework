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
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

// RunControlPlane starts the external control plane API server, exposing the
// route registry, upstreams, deployment ledger and release workflows over REST.
//
// Usage:
//
//	fgoths controlplane --addr=:9091 --store=ledger.json --token=<admin-token>
//
// The store accepts either a JSON file path or a sqlite:// URL (requires the
// `sqlite` build tag): --store=sqlite://controlplane.db
func RunControlPlane(args []string) {
	addr := ":9091"
	storePath := "controlplane-ledger.json"
	token := os.Getenv("FGOTHS_CP_TOKEN")

	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--addr="):
			addr = strings.TrimPrefix(arg, "--addr=")
		case strings.HasPrefix(arg, "--store="):
			storePath = strings.TrimPrefix(arg, "--store=")
		case strings.HasPrefix(arg, "--token="):
			token = strings.TrimPrefix(arg, "--token=")
		}
	}

	if strings.TrimSpace(token) == "" {
		fmt.Println("⚠️  No admin token configured.")
		fmt.Println("   Set FGOTHS_CP_TOKEN or pass --token=<admin-token>.")
		fmt.Println("   The API refuses to serve without a token (fails closed).")
		osExit(1)
	}

	cp, err := newControlPlaneServer(storePath, token)
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		osExit(1)
	}

	fmt.Println("🎛️  FGOTHS Control Plane API")
	fmt.Printf("   addr:  %s\n", addr)
	fmt.Printf("   store: %s\n", storePath)
	fmt.Println("   auth:  bearer token (Authorization: Bearer <token>)")
	fmt.Println("\n   Endpoints (all under /api/v1):")
	fmt.Println("     GET|POST /api/v1/upstreams")
	fmt.Println("     GET|POST /api/v1/routes")
	fmt.Println("     GET      /api/v1/routes/{name}/target")
	fmt.Println("     GET|POST /api/v1/deployments")
	fmt.Println("     GET      /api/v1/deployments/{svc}/{env}")
	fmt.Println("     GET      /api/v1/deployments/{svc}/{env}/history")
	fmt.Println("     GET|POST /api/v1/releases")
	fmt.Println("     GET      /api/v1/releases/{svc}/{env}/{version}")
	fmt.Println("     POST     /api/v1/releases/{svc}/{env}/{version}/approve")
	fmt.Println("     POST     /api/v1/releases/{svc}/{env}/{version}/rollback")
	fmt.Println("     POST     /api/v1/releases/{svc}/{env}/{version}/reject")
	fmt.Println("     GET      /healthz  (public)")

	server := runtime.NewServer(addr)
	server.Handler = cp.Handler()
	server.WithShutdownTimeout(30 * time.Second)
	server.OnShutdown(func(ctx context.Context) error {
		return cp.Close()
	})
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("control plane failed: %v", err)
		}
	}()

	fmt.Printf("\n✅ Control plane listening on http://localhost%s\n", addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	fmt.Println("\n👋 Shutting down control plane gracefully...")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
}

// newControlPlaneServer builds the control plane with the right persistence
// backend for the given store spec. "sqlite://path.db" selects the SQLite
// backend (requires the `sqlite` build tag); anything else is a JSON file path.
func newControlPlaneServer(storeSpec, token string) (*runtime.ControlPlaneServer, error) {
	if strings.HasPrefix(storeSpec, "sqlite://") {
		path := strings.TrimPrefix(storeSpec, "sqlite://")
		depStore, policyStore, err := sqliteStores(path)
		if err != nil {
			return nil, err
		}
		return runtime.NewControlPlaneServerWithStores(runtime.ControlPlaneConfig{
			AdminToken: token,
		}, depStore, policyStore), nil
	}
	return runtime.NewControlPlaneServer(runtime.ControlPlaneConfig{
		StorePath:  storeSpec,
		AdminToken: token,
	}), nil
}
