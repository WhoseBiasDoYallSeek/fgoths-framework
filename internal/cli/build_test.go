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
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

func TestNewCycloneDXBOMListsLinkedModules(t *testing.T) {
	info := &debug.BuildInfo{
		GoVersion: "go1.25.1",
		Main:      debug.Module{Path: "example.com/app", Version: "(devel)"},
		Deps: []*debug.Module{
			{Path: "example.com/lib", Version: "v1.2.3", Sum: "h1:abc="},
			{Path: "example.com/old", Version: "v0.1.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v0.1.1"}},
		},
		Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "deadbeef"}},
	}
	bom := newCycloneDXBOM(info, time.Unix(0, 0))

	if bom.SpecVersion != "1.5" || bom.Metadata.Timestamp != "1970-01-01T00:00:00Z" {
		t.Fatalf("unexpected header: spec=%q timestamp=%q", bom.SpecVersion, bom.Metadata.Timestamp)
	}
	app := bom.Metadata.Component
	if app.Name != "example.com/app" || app.PURL != "pkg:golang/example.com/app" {
		t.Fatalf("devel builds must omit the purl version, got %+v", app)
	}
	wantProps := []cycloneDXProperty{{"golang:toolchain", "go1.25.1"}, {"golang:build:vcs.revision", "deadbeef"}}
	if !reflect.DeepEqual(app.Properties, wantProps) {
		t.Fatalf("application properties = %+v, want %+v", app.Properties, wantProps)
	}

	want := []cycloneDXComponent{
		{Type: "library", BOMRef: "pkg:golang/stdlib@1.25.1", Name: "stdlib", Version: "go1.25.1", PURL: "pkg:golang/stdlib@1.25.1"},
		{Type: "library", BOMRef: "pkg:golang/example.com/lib@v1.2.3", Name: "example.com/lib", Version: "v1.2.3", PURL: "pkg:golang/example.com/lib@v1.2.3",
			Properties: []cycloneDXProperty{{"golang:sum", "h1:abc="}}},
		{Type: "library", BOMRef: "pkg:golang/example.com/fork@v0.1.1", Name: "example.com/fork", Version: "v0.1.1", PURL: "pkg:golang/example.com/fork@v0.1.1",
			Properties: []cycloneDXProperty{{"golang:replaces", "example.com/old@v0.1.0"}}},
	}
	if !reflect.DeepEqual(bom.Components, want) {
		t.Fatalf("components = %+v\nwant %+v", bom.Components, want)
	}
}

func TestGenerateDockerfile(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := generateDockerfile("bin/app"); err != nil {
			t.Fatalf("generateDockerfile failed: %v", err)
		}
	})

	content, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatalf("expected Dockerfile to be written: %v", err)
	}
	if !strings.Contains(string(content), "FROM scratch") {
		t.Fatalf("expected scratch base image, got %q", content)
	}
	if !strings.Contains(string(content), "COPY bin/app /app") {
		t.Fatalf("expected binary copy instruction, got %q", content)
	}
}

func TestGenerateSBOM(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	dir := t.TempDir()
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	withWorkingDir(t, dir, func() {
		if err := generateSBOM(binary); err != nil {
			t.Fatalf("generateSBOM failed: %v", err)
		}
	})

	content, err := os.ReadFile(filepath.Join(dir, "sbom.json"))
	if err != nil {
		t.Fatalf("expected sbom.json to be written: %v", err)
	}
	var parsed cycloneDXBOM
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("expected sbom.json to be valid JSON: %v", err)
	}
	if parsed.BOMFormat != "CycloneDX" || parsed.Metadata.Timestamp != "2023-11-14T22:13:20Z" {
		t.Fatalf("unexpected SBOM header: %+v", parsed.Metadata)
	}
	if parsed.Metadata.Component.Name != "github.com/WhoseBiasDoYallSeek/fgoths-framework" {
		t.Fatalf("SBOM must name the main module, got %q", parsed.Metadata.Component.Name)
	}
	if len(parsed.Components) == 0 || parsed.Components[0].Name != "stdlib" {
		t.Fatalf("SBOM must start with the Go stdlib component, got %+v", parsed.Components)
	}
}

func TestGenerateSBOMErrors(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test binary: %v", err)
	}
	withWorkingDir(t, t.TempDir(), func() {
		if err := os.WriteFile("not-a-binary", []byte("text"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := generateSBOM("not-a-binary"); err == nil || !strings.Contains(err.Error(), "read build info") {
			t.Fatalf("generateSBOM(non-binary) error = %v, want a build info error", err)
		}

		t.Setenv("SOURCE_DATE_EPOCH", "yesterday")
		if err := generateSBOM(binary); err == nil || !strings.Contains(err.Error(), "SOURCE_DATE_EPOCH") {
			t.Fatalf("generateSBOM with a bad SOURCE_DATE_EPOCH error = %v", err)
		}

		t.Setenv("SOURCE_DATE_EPOCH", "")
		if err := os.Mkdir("sbom.json", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := generateSBOM(binary); err == nil {
			t.Fatal("generateSBOM must report write errors")
		}
	})
}

func TestRunBuildProducesBinaryWithDockerfileAndSBOM(t *testing.T) {
	dir := t.TempDir()
	moduleName := "build-test-app"
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+moduleName+"\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mainSrc := "package main\n\nfunc main() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			RunBuild([]string{"--out=bin/app", "--scratch", "--sbom"})
		})
		if !strings.Contains(output, "Building optimized binary") {
			t.Fatalf("expected build banner, got %q", output)
		}
		if !strings.Contains(output, "Dockerfile generated") {
			t.Fatalf("expected Dockerfile generation message, got %q", output)
		}
	})

	if _, err := os.Stat(filepath.Join(dir, "bin", "app")); err != nil {
		t.Fatalf("expected compiled binary: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err != nil {
		t.Fatalf("expected generated Dockerfile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sbom.json")); err != nil {
		t.Fatalf("expected generated sbom.json: %v", err)
	}
}

// withWorkingDir runs fn with the process working directory temporarily set to dir,
// restoring the original directory afterward.
func withWorkingDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		if err := os.Chdir(original); err != nil {
			t.Fatalf("restore chdir: %v", err)
		}
	}()
	fn()
}
