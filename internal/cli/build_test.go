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
	"strings"
	"testing"
)

func TestFormatComponents(t *testing.T) {
	output := `go: downloading modules
"Path": "example.com/foo"
"Version": "v1.2.3"
"Path": "example.com/bar"
`
	got := formatComponents(output)
	if !strings.Contains(got, `"name": "example.com/foo"`) {
		t.Fatalf("expected foo component, got %q", got)
	}
	if !strings.Contains(got, `"name": "example.com/bar"`) {
		t.Fatalf("expected bar component, got %q", got)
	}
	if strings.Count(got, "{") != 2 {
		t.Fatalf("expected exactly 2 components, got %q", got)
	}
}

func TestFormatComponentsEmpty(t *testing.T) {
	if got := formatComponents("no matches here"); got != "" {
		t.Fatalf("expected empty components list, got %q", got)
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
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.WriteFile("go.mod", []byte("module sbom-test\n\ngo 1.23\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		if err := generateSBOM(); err != nil {
			t.Fatalf("generateSBOM failed: %v", err)
		}
	})

	content, err := os.ReadFile(filepath.Join(dir, "sbom.json"))
	if err != nil {
		t.Fatalf("expected sbom.json to be written: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(content, &parsed); err != nil {
		t.Fatalf("expected sbom.json to be valid JSON: %v", err)
	}
	if parsed["bomFormat"] != "CycloneDX" {
		t.Fatalf("expected CycloneDX bomFormat, got %v", parsed["bomFormat"])
	}
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
