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

func TestMergeFeaturesDedupesAndPreservesOrder(t *testing.T) {
	base := []config.Feature{config.FeatureHealth}
	got := mergeFeatures(base, []config.Feature{
		config.FeatureMetrics, config.FeatureHealth, config.FeatureOpenAPI,
	})
	if len(got) != 3 {
		t.Fatalf("expected 3 features after dedupe, got %#v", got)
	}
	want := []config.Feature{config.FeatureHealth, config.FeatureMetrics, config.FeatureOpenAPI}
	for i, f := range want {
		if got[i] != f {
			t.Errorf("merged[%d] = %v, want %v", i, got[i], f)
		}
	}
}

func TestMergeFeaturesEmptySides(t *testing.T) {
	if got := mergeFeatures(nil, nil); len(got) != 0 {
		t.Errorf("mergeFeatures(nil, nil) = %#v, want empty", got)
	}
	got := mergeFeatures([]config.Feature{config.FeatureHealth}, nil)
	if len(got) != 1 || got[0] != config.FeatureHealth {
		t.Errorf("mergeFeatures(base, nil) = %#v", got)
	}
}

func TestRunInitRequiresPreset(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			err := RunInit([]string{"--name=no-preset"})
			if err == nil {
				t.Fatal("expected error when --preset is missing")
			}
			if !strings.Contains(err.Error(), "--preset is required") {
				t.Fatalf("expected preset hint, got %v", err)
			}
		})
		for _, want := range []string{"api", "webapp"} {
			if !strings.Contains(output, want) {
				t.Errorf("expected %q in preset hint output", want)
			}
		}
	})
}

func TestRunInitRequiresName(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--preset=api"})
		if err == nil {
			t.Fatal("expected error when --name is missing")
		}
		if !strings.Contains(err.Error(), "project name is required") {
			t.Fatalf("expected name error, got %v", err)
		}
	})
}

func TestRunInitUnsupportedDBOverride(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--preset=api", "--name=bad-db", "--db=oracle"})
		if err == nil {
			t.Fatal("expected error for unsupported --db override")
		}
		if !strings.Contains(err.Error(), "unsupported database: oracle") {
			t.Fatalf("expected db validation error, got %v", err)
		}
	})
}

func TestRunInitUnknownFeatureReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--preset=webapp", "--name=bad-feature", "--features=not-a-feature"})
		if err == nil {
			t.Fatal("expected error for unknown feature")
		}
		if !strings.Contains(err.Error(), "unknown feature") {
			t.Fatalf("expected feature error, got %v", err)
		}
	})
}

func TestRunInitWebappPresetDefaultsToSQLite(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			if err := RunInit([]string{"--preset=webapp", "--name=web-sqlite"}); err != nil {
				t.Fatalf("RunInit: %v", err)
			}
		})
		if !strings.Contains(output, "Database: sqlite") {
			t.Fatalf("expected webapp preset to default to sqlite, got %q", output)
		}
		goMod, err := os.ReadFile(filepath.Join(dir, "web-sqlite", "go.mod"))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(goMod), "modernc.org/sqlite") {
			t.Error("expected sqlite dependency in webapp go.mod")
		}
	})
}

func TestRunInitDBOverrideOnWebappRemovesSQLite(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			if err := RunInit([]string{"--preset=webapp", "--name=web-nodb", "--db=none"}); err != nil {
				t.Fatalf("RunInit: %v", err)
			}
		})
		if !strings.Contains(output, "Database: none") {
			t.Fatalf("expected db override to none, got %q", output)
		}
	})
}

func TestRunBuildProducesBinary(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		// Minimal module so `go build` has something to compile.
		if err := os.WriteFile("go.mod", []byte("module buildcheck\n\ngo 1.26\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("cmd/app", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile("cmd/app/main.go", []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		output := captureStdout(t, func() { RunBuild(nil) })
		if !strings.Contains(output, "Building optimized binary") {
			t.Fatalf("expected build banner, got %q", output)
		}
		if _, err := os.Stat("bin/app"); err != nil {
			t.Fatalf("expected bin/app to exist: %v", err)
		}
	})
}

func TestRunRoutesOutsideProject(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() { RunRoutes(nil) })
		_ = output // must not panic; routes scanning on an empty dir is a no-op
	})
}
