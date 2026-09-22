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

	"github.com/fsnotify/fsnotify"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

func TestParseFeaturesNormalizesAndDeduplicates(t *testing.T) {
	features, err := parseFeatures("health, metrics, JWT_AUTH, jwt_auth, mtls, MTLS")
	if err != nil {
		t.Fatalf("parseFeatures() unexpected error: %v", err)
	}

	seen := map[config.Feature]bool{}
	for _, feature := range features {
		seen[feature] = true
	}

	for _, feature := range []config.Feature{config.FeatureHealth, config.FeatureMetrics, config.FeatureJWTAuth, config.FeatureMTLS} {
		if !seen[feature] {
			t.Fatalf("feature %q was not included after normalization", feature)
		}
	}

	if len(features) != 4 {
		t.Fatalf("expected 4 unique features after normalization, got %d: %#v", len(features), features)
	}
}

func TestShouldSkipWatchDir(t *testing.T) {
	for _, dir := range []string{".git", "bin", "node_modules", "dist", "schemas/generated"} {
		if !shouldSkipWatchDir(dir) {
			t.Fatalf("expected %q to be ignored", dir)
		}
	}

	if shouldSkipWatchDir("internal") {
		t.Fatal("expected internal to remain watched")
	}
}

func TestIsIgnoredWatchPath(t *testing.T) {
	if !isIgnoredWatchPath(".git/config") {
		t.Fatal("expected git metadata to be ignored")
	}
	if !isIgnoredWatchPath("bin/app") {
		t.Fatal("expected generated binaries to be ignored")
	}
	if isIgnoredWatchPath("views/page.templ") {
		t.Fatal("expected app template files to be watched")
	}
}

func TestShouldHandleWatchEvent(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if shouldHandleWatchEvent("", fsnotify.Write) {
			t.Fatal("expected empty path to be ignored")
		}
		if shouldHandleWatchEvent("main.go", 0) {
			t.Fatal("expected zero op to be ignored")
		}
		if shouldHandleWatchEvent("bin/app", fsnotify.Write) {
			t.Fatal("expected ignored directory to be filtered out")
		}
		if shouldHandleWatchEvent("main.go.swp", fsnotify.Write) {
			t.Fatal("expected swap file to be filtered out")
		}
		if shouldHandleWatchEvent("main.go", fsnotify.Chmod) {
			t.Fatal("expected chmod-only events to be ignored")
		}
		if !shouldHandleWatchEvent("main.go", fsnotify.Write) {
			t.Fatal("expected a .go write event to be handled")
		}
		if !shouldHandleWatchEvent("project/go.mod", fsnotify.Write) {
			t.Fatal("expected go.mod writes to be handled")
		}
		if !shouldHandleWatchEvent("project/Makefile", fsnotify.Write) {
			t.Fatal("expected Makefile writes to be handled")
		}
		if shouldHandleWatchEvent("README", fsnotify.Write) {
			t.Fatal("expected unrecognized extension-less files to be ignored")
		}

		if err := os.Mkdir("subdir", 0o755); err != nil {
			t.Fatalf("mkdir subdir: %v", err)
		}
		if !shouldHandleWatchEvent("subdir", fsnotify.Create) {
			t.Fatal("expected new directories to be handled")
		}
	})
}

func TestResolveToolPathFindsBinaryOnPath(t *testing.T) {
	if _, err := resolveToolPath("go"); err != nil {
		t.Fatalf("expected to resolve the go binary on PATH: %v", err)
	}
}

func TestResolveToolPathMissingReturnsError(t *testing.T) {
	if _, err := resolveToolPath("fgoths-definitely-not-a-real-binary"); err == nil {
		t.Fatal("expected an error for a nonexistent tool")
	}
}

func TestEnsureToolFlatcMissingReturnsInstallHint(t *testing.T) {
	err := ensureTool("fgoths-definitely-not-a-real-binary", "https://example.com/install")
	if err == nil {
		t.Fatal("expected an error when the tool cannot be found or installed")
	}
}

func TestRunSucceedsAndFails(t *testing.T) {
	if err := run("go", "version"); err != nil {
		t.Fatalf("expected running `go version` to succeed: %v", err)
	}
	if err := run("fgoths-definitely-not-a-real-binary"); err == nil {
		t.Fatal("expected running a nonexistent binary to fail")
	}
}

func TestRunGenerateOnceReportsNothingToGenerate(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if runGenerateOnce() {
			t.Fatal("expected runGenerateOnce to report nothing to do in an empty directory")
		}
	})
}

func TestRunGenerateOnceCompilesTemplFiles(t *testing.T) {
	if _, err := resolveToolPath("templ"); err != nil {
		t.Skip("templ binary not available in this environment")
	}
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.MkdirAll("views", 0o755); err != nil {
			t.Fatalf("mkdir views: %v", err)
		}
		content := "package views\n\ntempl Hello() {\n\t<div>hello</div>\n}\n"
		if err := os.WriteFile(filepath.Join("views", "hello.templ"), []byte(content), 0o644); err != nil {
			t.Fatalf("write templ file: %v", err)
		}
		if !runGenerateOnce() {
			t.Fatal("expected runGenerateOnce to report work done for a project with .templ files")
		}
	})
}

func TestRunGenerateDispatchesToCRUD(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		setupSQLiteMVCProject(t)
		output := captureStdout(t, func() {
			RunGenerate([]string{"crud", "User", "name:string"})
		})
		if !strings.Contains(output, "CRUD User generated") {
			t.Fatalf("expected RunGenerate to dispatch to RunGenerateCRUD, got %q", output)
		}
	})
}

func TestRunGenerateOnceRun(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			RunGenerate(nil)
		})
		if !strings.Contains(output, "Nothing to generate") {
			t.Fatalf("expected RunGenerate() with no args to report nothing to generate, got %q", output)
		}
	})
}

func TestEnsureToolUnknownToolReturnsInstallHint(t *testing.T) {
	err := ensureTool("some-other-tool", "https://example.com/install")
	if err == nil {
		t.Fatal("expected an error for an unrecognized tool name")
	}
	if !strings.Contains(err.Error(), "https://example.com/install") {
		t.Fatalf("expected install hint in error, got %v", err)
	}
}
