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
	"testing"
)

// TestNestedRuntimeCopiesInSync is the drift guard for in-repo modules that
// carry their own verbatim copy of pkg/runtime (see nestedRuntimeSyncDirs).
// A stale copy drifts silently because the framework test suite never
// compiles those modules. If this fails, run:
//
//	go run ./cmd/fgoths sync-templates
func TestNestedRuntimeCopiesInSync(t *testing.T) {
	// Ensure no test override is in play; this guard is repo-rooted.
	origRoot := runtimeRepoRootOverride
	runtimeRepoRootOverride = ""
	t.Cleanup(func() { runtimeRepoRootOverride = origRoot })

	root := repoRoot()
	for _, nested := range nestedRuntimeSyncDirs {
		for _, name := range nestedRuntimeFiles {
			src, err := os.ReadFile(filepath.Join(root, runtimeSourceDir, name))
			if err != nil {
				t.Fatalf("read source %s: %v", name, err)
			}
			dst, err := os.ReadFile(filepath.Join(root, nested.dir, runtimeSourceDir, name))
			if os.IsNotExist(err) {
				continue // nested module does not mirror this file
			}
			if err != nil {
				t.Fatalf("read %s/%s: %v", nested.dir, name, err)
			}
			if string(src) != string(dst) {
				t.Errorf("%s/pkg/runtime/%s drifted from pkg/runtime; run: go run ./cmd/fgoths sync-templates", nested.dir, name)
			}
		}
	}
}

// TestNestedRuntimeSyncWritesFiles verifies sync mode actually rewrites a
// drifted nested copy (using a temp override is not possible for the nested
// pass, so this test drives the real repo paths but restores the originals).
func TestNestedRuntimeSyncWritesFiles(t *testing.T) {
	if len(nestedRuntimeSyncDirs) == 0 {
		t.Skip("no nested runtime sync dirs configured")
	}
	origRoot := runtimeRepoRootOverride
	runtimeRepoRootOverride = ""
	t.Cleanup(func() { runtimeRepoRootOverride = origRoot })

	root := repoRoot()
	target := filepath.Join(root, nestedRuntimeSyncDirs[0].dir, runtimeSourceDir, "context.go")

	orig, err := os.ReadFile(target)
	if err != nil {
		t.Skipf("nested context.go not present: %v", err)
	}
	t.Cleanup(func() {
		_ = os.WriteFile(target, orig, 0o644)
	})

	// Introduce drift.
	if err := os.WriteFile(target, append(orig, []byte("\n// drifted\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	drifted, err := syncRuntimeTemplates(true)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if !drifted {
		t.Fatal("expected drift to be detected after mutating a nested copy")
	}

	// Sync mode must repair it.
	if _, err := syncRuntimeTemplates(false); err != nil {
		t.Fatalf("sync failed: %v", err)
	}
	repaired, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	src, _ := os.ReadFile(filepath.Join(root, runtimeSourceDir, "context.go"))
	if string(repaired) != string(src) {
		t.Fatal("expected nested copy to equal pkg/runtime source after sync")
	}
}
