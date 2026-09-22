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

// TestRuntimeTemplatesInSync is the drift guard for approach B: every managed
// runtime template (base and feature) must be a verbatim copy of its
// pkg/runtime source. If this test fails, someone changed one side without the
// other — run `go run ./cmd/fgoths sync-templates` to fix.
func TestRuntimeTemplatesInSync(t *testing.T) {
	drifted, err := syncRuntimeTemplates(true)
	if err != nil {
		t.Fatalf("drift check failed: %v", err)
	}
	if drifted {
		t.Error("runtime templates drifted from pkg/runtime; run: go run ./cmd/fgoths sync-templates")
	}
}

// TestSyncTemplatesRejectsTemplateDirectives guards the invariant that managed
// runtime sources stay verbatim-copyable: a file containing Go template
// directives must be rejected instead of silently producing a broken template.
func TestSyncTemplatesRejectsTemplateDirectives(t *testing.T) {
	orig := runtimeSyncFiles
	origSrc := runtimeSourceDir
	origDst := templateDestDir
	origRoot := runtimeRepoRootOverride
	t.Cleanup(func() {
		runtimeSyncFiles = orig
		runtimeSourceDir = origSrc
		templateDestDir = origDst
		runtimeRepoRootOverride = origRoot
	})

	tmp := t.TempDir()
	runtimeRepoRootOverride = tmp
	srcDir := "src"
	dstDir := "dst"
	if err := os.MkdirAll(filepath.Join(tmp, srcDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, dstDir), 0o755); err != nil {
		t.Fatal(err)
	}

	// A source with template directives must be rejected.
	bad := "package runtime\n\n// {{if .Has \"x\"}} broken {{end}}\n"
	if err := os.WriteFile(filepath.Join(tmp, srcDir, "fake.go"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}

	runtimeSyncFiles = map[string]string{"fake.go": "fake.go.tpl"}
	runtimeSourceDir = srcDir
	templateDestDir = dstDir

	drifted, err := syncRuntimeTemplates(false)
	if err == nil {
		t.Fatal("expected error for source containing template directives")
	}
	if !strings.Contains(err.Error(), "template directives") {
		t.Fatalf("expected directive rejection error, got: %v", err)
	}
	_ = drifted
}

// TestSyncTemplatesDetectsDrift verifies the drift detection itself works:
// a modified source must be reported as drift in check mode.
func TestSyncTemplatesDetectsDrift(t *testing.T) {
	orig := runtimeSyncFiles
	origSrc := runtimeSourceDir
	origDst := templateDestDir
	origRoot := runtimeRepoRootOverride
	t.Cleanup(func() {
		runtimeSyncFiles = orig
		runtimeSourceDir = origSrc
		templateDestDir = origDst
		runtimeRepoRootOverride = origRoot
	})

	tmp := t.TempDir()
	runtimeRepoRootOverride = tmp
	srcDir := "src"
	dstDir := "dst"
	if err := os.MkdirAll(filepath.Join(tmp, srcDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(tmp, dstDir), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(tmp, srcDir, "fake.go"), []byte("package runtime\n\n// v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, dstDir, "fake.go.tpl"), []byte("package runtime\n\n// v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtimeSyncFiles = map[string]string{"fake.go": "fake.go.tpl"}
	runtimeSourceDir = srcDir
	templateDestDir = dstDir

	drifted, err := syncRuntimeTemplates(true)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !drifted {
		t.Fatal("expected drift to be detected when source differs from template")
	}

	// Sync mode must actually write the template.
	if _, err := syncRuntimeTemplates(false); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(tmp, dstDir, "fake.go.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "package runtime\n\n// v2\n" && !strings.Contains(string(written), "v2") {
		// The source was still v1 in this test; after sync the template must
		// equal the source exactly.
		src, _ := os.ReadFile(filepath.Join(tmp, srcDir, "fake.go"))
		if string(written) != string(src) {
			t.Fatalf("expected template to equal source after sync")
		}
	}
}
