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

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

func TestValidateType(t *testing.T) {
	for _, valid := range []string{"api", "ssr"} {
		if !validateType(valid) {
			t.Fatalf("expected %q to be a valid type", valid)
		}
	}
	if validateType("unknown") {
		t.Fatal("expected unknown type to be invalid")
	}
}

func TestValidateArch(t *testing.T) {
	for _, valid := range []string{"flat", "mvc"} {
		if !validateArch(valid) {
			t.Fatalf("expected %q to be a valid architecture", valid)
		}
	}
	if validateArch("spaghetti") {
		t.Fatal("expected unknown architecture to be invalid")
	}
}

func TestValidateDB(t *testing.T) {
	for _, valid := range []string{"none", "sqlite", "postgres", "mysql"} {
		if !validateDB(valid) {
			t.Fatalf("expected %q to be a valid database", valid)
		}
	}
	if validateDB("oracle") {
		t.Fatal("expected unsupported database to be invalid")
	}
}

func TestJoinHelpers(t *testing.T) {
	if got := joinTypes(config.ValidTypes); got != "api, ssr" {
		t.Fatalf("unexpected joinTypes output: %q", got)
	}
	if got := joinArchs(config.ValidArchs); got != "flat, mvc" {
		t.Fatalf("unexpected joinArchs output: %q", got)
	}
	if got := joinDBs(config.ValidDBs); got != "none, sqlite, postgres, mysql" {
		t.Fatalf("unexpected joinDBs output: %q", got)
	}
}

func TestNormalizeFeatureNameAliases(t *testing.T) {
	cases := map[string]config.Feature{
		"health":        config.FeatureHealth,
		"health-check":  config.FeatureHealth,
		"healthcheck":   config.FeatureHealth,
		"metrics":       config.FeatureMetrics,
		"prometheus":    config.FeatureMetrics,
		"openapi":       config.FeatureOpenAPI,
		"swagger":       config.FeatureOpenAPI,
		"flatbuffers":   config.FeatureFlatBuffers,
		"flat-buffers":  config.FeatureFlatBuffers,
		"htmx":          config.FeatureHTMX,
		"grpc":          config.FeatureGRPC,
		"jwt-auth":      config.FeatureJWTAuth,
		"jwt_auth":      config.FeatureJWTAuth,
		"jwtauth":       config.FeatureJWTAuth,
		"mtls":          config.FeatureMTLS,
		"m-tls":         config.FeatureMTLS,
		"mutual-tls":    config.FeatureMTLS,
		"otel":          config.FeatureOTel,
		"opentelemetry": config.FeatureOTel,
		" JWT_AUTH ":    config.FeatureJWTAuth,
		"Health-Check":  config.FeatureHealth,
	}
	for input, want := range cases {
		got, ok := normalizeFeatureName(input)
		if !ok {
			t.Fatalf("normalizeFeatureName(%q) expected ok=true", input)
		}
		if got != want {
			t.Fatalf("normalizeFeatureName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeFeatureNameUnknown(t *testing.T) {
	if _, ok := normalizeFeatureName("not-a-feature"); ok {
		t.Fatal("expected unknown feature alias to be rejected")
	}
}

func TestParseFeaturesEmptyString(t *testing.T) {
	features, err := parseFeatures("")
	if err != nil {
		t.Fatalf("parseFeatures(\"\") unexpected error: %v", err)
	}
	if len(features) != 0 {
		t.Fatalf("expected no features, got %#v", features)
	}
}

func TestParseFeaturesUnknownFeatureErrors(t *testing.T) {
	if _, err := parseFeatures("health,bogus-feature"); err == nil {
		t.Fatal("expected an error for an unknown feature")
	}
}

func TestParseFeaturesAcceptsMultipleSeparators(t *testing.T) {
	features, err := parseFeatures("health;metrics\nopenapi")
	if err != nil {
		t.Fatalf("parseFeatures() unexpected error: %v", err)
	}
	if len(features) != 3 {
		t.Fatalf("expected 3 features, got %#v", features)
	}
}

func TestRunInitWithFlagsGeneratesProject(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			if err := RunInit([]string{"--name=my-app", "--preset=api"}); err != nil {
				t.Fatalf("RunInit: %v", err)
			}
		})
		if !strings.Contains(output, "Project generated successfully") {
			t.Fatalf("expected success message, got %q", output)
		}
		if _, err := os.Stat(filepath.Join(dir, "my-app", "go.mod")); err != nil {
			t.Fatalf("expected generated project go.mod: %v", err)
		}
	})
}

func TestRunInitWithPresetGeneratesProject(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			if err := RunInit([]string{"--preset=api", "--name=preset-app"}); err != nil {
				t.Fatalf("RunInit: %v", err)
			}
		})
		if !strings.Contains(output, "Using preset: api") {
			t.Fatalf("expected preset banner, got %q", output)
		}
		if !strings.Contains(output, "Project generated successfully") {
			t.Fatalf("expected success message, got %q", output)
		}
		if _, err := os.Stat(filepath.Join(dir, "preset-app", "go.mod")); err != nil {
			t.Fatalf("expected generated project go.mod: %v", err)
		}
	})
}

// TestRunInitPresetExplicitDBOverridesDefault guards the same override rule as
// --features: explicit flags must win over preset defaults. The api preset
// defaults to no database; passing --db=sqlite must generate the database layer
// and its dependency instead of being silently ignored.
func TestRunInitPresetExplicitDBOverridesDefault(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			if err := RunInit([]string{"--preset=api", "--name=preset-db-app", "--db=sqlite"}); err != nil {
				t.Fatalf("RunInit: %v", err)
			}
		})
		if !strings.Contains(output, "Database: sqlite") {
			t.Fatalf("expected sqlite database in config output, got %q", output)
		}

		goMod, err := os.ReadFile(filepath.Join(dir, "preset-db-app", "go.mod"))
		if err != nil {
			t.Fatalf("read go.mod: %v", err)
		}
		if !strings.Contains(string(goMod), "modernc.org/sqlite") {
			t.Error("expected sqlite dependency in generated go.mod")
		}
		if _, err := os.Stat(filepath.Join(dir, "preset-db-app", "internal", "database")); err != nil {
			t.Errorf("expected internal/database layer: %v", err)
		}
	})
}

func TestPrintConfigAndPrintSuccess(t *testing.T) {
	cfg := config.ProjectConfig{
		Name:         "demo",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBSQLite,
		Features:     []config.Feature{config.FeatureMetrics, config.FeatureOpenAPI},
	}

	configOutput := captureStdout(t, func() { printConfig(cfg, "api") })
	if !strings.Contains(configOutput, "Using preset: api") {
		t.Fatalf("expected preset name in config output, got %q", configOutput)
	}
	if !strings.Contains(configOutput, "demo") {
		t.Fatalf("expected project name in config output, got %q", configOutput)
	}

	successOutput := captureStdout(t, func() { printSuccess(cfg) })
	if !strings.Contains(successOutput, "cd demo") {
		t.Fatalf("expected next-steps cd command, got %q", successOutput)
	}
	if !strings.Contains(successOutput, "/metrics") {
		t.Fatalf("expected metrics endpoint in success output, got %q", successOutput)
	}
	if !strings.Contains(successOutput, "/docs") {
		t.Fatalf("expected openapi endpoint in success output, got %q", successOutput)
	}
}

// type/arch/db/feature answers used to be accepted silently (or crash the
// process at the very end) instead of being rejected immediately.
