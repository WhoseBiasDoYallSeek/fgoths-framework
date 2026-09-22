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
package generator

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

// TestGRPCRuntimeServesUnaryCalls compiles the generated gRPC runtime template
// into a real HTTP handler and exercises the wire contract end to end.
func TestGRPCRuntimeServesUnaryCalls(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "grpc-runtime-check",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureGRPC},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	runtimeDir := filepath.Join(cfg.Name, "pkg", "runtime")
	grpcSrc, err := os.ReadFile(filepath.Join(runtimeDir, "grpc.go"))
	if err != nil {
		t.Fatalf("missing grpc runtime: %v", err)
	}

	// Build the handler in an isolated module inside the generated project so
	// the test uses the exact generated source, not a copy in this repo.
	tmp := t.TempDir()
	goMod := "module grpccheck\n\ngo 1.27.0\n"
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "grpc.go"), grpcSrc, 0o644); err != nil {
		t.Fatal(err)
	}
	mainSrc := `package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

type pingReq struct{ Message string ` + "`" + `json:"message"` + "`" + ` }

func TestGRPCWire(t *testing.T) {
	srv := NewGRPCServer()
	if err := srv.Register(&GRPCService{
		Name: "check.v1.Example",
		Methods: map[string]GRPCMethod{
			"Ping": func(ctx context.Context, req json.RawMessage) (any, *GRPCError) {
				var in pingReq
				if err := json.Unmarshal(req, &in); err != nil {
					return nil, &GRPCError{Code: GRPCInvalidArgument, Message: "bad json"}
				}
				if in.Message == "" {
					return nil, &GRPCError{Code: GRPCInvalidArgument, Message: "message is required"}
				}
				return map[string]string{"reply": "pong: " + in.Message}, nil
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	// happy path
	body := bytes.NewReader([]byte(` + "`" + `{"message":"hi"}` + "`" + `))
	req := httptest.NewRequest("POST", "/check.v1.Example/Ping", body)
	req.Header.Set("Content-Type", "application/grpc+json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != 200 || !bytes.Contains(rec.Body.Bytes(), []byte("pong: hi")) {
		t.Fatalf("unexpected response: %d %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("X-Grpc-Status"); got != "0" {
		t.Fatalf("expected X-Grpc-Status 0, got %q", got)
	}

	// validation error maps to InvalidArgument (3)
	req = httptest.NewRequest("POST", "/check.v1.Example/Ping", strings.NewReader(` + "`" + `{}` + "`" + `))
	req.Header.Set("Content-Type", "application/grpc+json")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Grpc-Status"); got != "3" {
		t.Fatalf("expected X-Grpc-Status 3, got %q", got)
	}

	// unknown service maps to NotFound (5)
	req = httptest.NewRequest("POST", "/ghost.v1.Svc/Call", strings.NewReader(` + "`" + `{}` + "`" + `))
	req.Header.Set("Content-Type", "application/grpc+json")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Grpc-Status"); got != "5" {
		t.Fatalf("expected X-Grpc-Status 5, got %q", got)
	}

	// health endpoint reports SERVING for overall server health
	req = httptest.NewRequest("GET", "/health/grpc", nil)
	rec = httptest.NewRecorder()
	srv.HealthHTTP().ServeHTTP(rec, req)
	if !bytes.Contains(rec.Body.Bytes(), []byte("SERVING")) {
		t.Fatalf("expected SERVING health, got %s", rec.Body.String())
	}

	// SetServingState(false) flips health to NOT_SERVING and calls fail
	srv.SetServingState("check.v1.Example", false)
	req = httptest.NewRequest("POST", "/check.v1.Example/Ping", strings.NewReader(` + "`" + `{"message":"x"}` + "`" + `))
	req.Header.Set("Content-Type", "application/grpc+json")
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Grpc-Status"); got != "14" {
		t.Fatalf("expected X-Grpc-Status 14 (Unavailable), got %q", got)
	}
}
`
	if err := os.WriteFile(filepath.Join(tmp, "main_test.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = tmp
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("generated grpc runtime failed tests: %v\n%s", err, out.String())
	}
}
