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
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// runtimeSyncFiles maps runtime source files to their template counterparts.
// Every managed template is a verbatim copy of a pkg/runtime source: the
// generated project must ship the exact same runtime the framework tests.
// The map key is the source file (relative to runtimeSourceDir) and the value
// is the template path (relative to templateDestDir).
//
// Feature templates (jwt-auth, otel, mtls) are synced too: auth.go, otel.go,
// tls.go and identity.go all live in pkg/runtime, so their feature-scoped
// copies must stay in lockstep to avoid silent security drift between the
// framework runtime and generated projects.
// Control-plane-only files (governance, registry, controlplane, policy
// versioning, sqlite store) are NOT synced: they are irrelevant to generated
// projects.
var runtimeSyncFiles = map[string]string{
	// Base runtime templates (copied verbatim into every generated project).
	"server.go":  "base/pkg/runtime/server.go.tpl",
	"metrics.go": "base/pkg/runtime/metrics.go.tpl",
	"health.go":  "base/pkg/runtime/health.go.tpl",

	// HMR subpackage (copied verbatim into every generated project).
	"hmr/hmr.go":    "base/pkg/runtime/hmr/hmr.go.tpl",
	"hmr/client.go": "base/pkg/runtime/hmr/client.go.tpl",
	"hmr/codec.go":  "base/pkg/runtime/hmr/codec.go.tpl",

	// Feature templates (copied into projects that opt in to the feature).
	"auth.go":     "features/jwt-auth/pkg/runtime/auth.go.tpl",
	"otel.go":     "features/otel/pkg/runtime/otel.go.tpl",
	"tls.go":      "features/mtls/pkg/runtime/tls.go.tpl",
	"identity.go": "features/mtls/pkg/runtime/identity.go.tpl",
}

// nestedRuntimeSyncDirs are in-repo modules that carry their own verbatim
// copy of pkg/runtime (sample projects, test harnesses). They must stay in
// lockstep with the framework runtime too: a stale copy here drifts
// silently because the framework test suite never compiles it. server.go
// additionally requires the reuseport build-tagged files.
var nestedRuntimeSyncDirs = []struct {
	dir     string
	version string // module go directive to keep aligned
}{}

// nestedRuntimeFiles lists pkg/runtime files mirrored verbatim into each
// nested module directory above.
var nestedRuntimeFiles = []string{
	"server.go",
	"metrics.go",
	"health.go",
	"reuseport_linux.go",
	"reuseport_other.go",
	"reuseport_posix.go",
	"hmr/hmr.go",
	"hmr/client.go",
	"hmr/codec.go",
}
var (
	// runtimeSourceDir and templateDestDir are resolved relative to the
	// repository root so the drift check works from any working directory
	// (tests run with the package directory as CWD).
	runtimeSourceDir = "pkg/runtime"
	templateDestDir  = "internal/generator/templates"

	// runtimeRepoRootOverride, when non-empty, overrides repoRoot resolution.
	// Tests use it to point the sync at temporary directories.
	runtimeRepoRootOverride string
)

// repoRoot resolves the repository root from this source file's location,
// making path resolution independent of the process working directory.
func repoRoot() string {
	if runtimeRepoRootOverride != "" {
		return runtimeRepoRootOverride
	}
	_, thisFile, _, ok := runtimeCaller(0)
	if !ok {
		return "."
	}
	// thisFile: <repo>/internal/cli/sync_templates.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

// RunSyncTemplates regenerates the base runtime templates from pkg/runtime
// sources, keeping the generated projects in lockstep with the framework
// runtime. It is a developer command, not part of the user-facing workflow.
//
// Usage:
//
//	fgoths sync-templates [--check]
//
// With --check it only verifies that templates are up to date (used by tests
// and CI) and exits non-zero on drift.
func RunSyncTemplates(args []string) {
	checkOnly := false
	for _, arg := range args {
		if arg == "--check" {
			checkOnly = true
		}
	}

	drifted, err := syncRuntimeTemplates(checkOnly)
	if err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		osExit(1)
		return
	}

	if checkOnly {
		if drifted {
			fmt.Println("❌ Runtime templates are out of sync with pkg/runtime.")
			fmt.Println("   Run: go run ./cmd/fgoths sync-templates")
			osExit(1)
			return
		}
		fmt.Println("✅ Runtime templates are in sync with pkg/runtime.")
		return
	}
	if drifted {
		fmt.Println("🔄 Runtime templates regenerated from pkg/runtime.")
	} else {
		fmt.Println("✅ Runtime templates were already in sync.")
	}
}

// syncRuntimeTemplates copies each managed runtime source into its template,
// returning whether any file changed. With checkOnly it writes nothing and
// only reports drift.
func syncRuntimeTemplates(checkOnly bool) (bool, error) {
	root := repoRoot()
	drifted := false
	// Iterate deterministically so check/sync output is stable across runs.
	names := make([]string, 0, len(runtimeSyncFiles))
	for name := range runtimeSyncFiles {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		srcPath := filepath.Join(root, runtimeSourceDir, name)
		dstPath := filepath.Join(root, templateDestDir, runtimeSyncFiles[name])

		src, err := osReadFile(srcPath)
		if err != nil {
			return drifted, fmt.Errorf("read %s: %w", srcPath, err)
		}
		if strings.Contains(string(src), "{{") {
			return drifted, fmt.Errorf("%s contains Go template directives and cannot be synced verbatim", srcPath)
		}

		dst, err := osReadFile(dstPath)
		if err != nil && !osIsNotExist(err) {
			return drifted, fmt.Errorf("read %s: %w", dstPath, err)
		}

		if bytes.Equal(src, dst) {
			continue
		}
		drifted = true
		if checkOnly {
			continue
		}
		if err := osWriteFile(dstPath, src, 0o644); err != nil {
			return drifted, fmt.Errorf("write %s: %w", dstPath, err)
		}
	}

	// Nested-module runtime copies must be verbatim mirrors of pkg/runtime.
	// Only runs against the real repository root: unit tests override the
	// root with a temp dir that models just the template mapping, and the
	// nested-module invariant is covered by TestRuntimeTemplatesInSync
	// (which runs unoverridden) and by CI.
	if runtimeRepoRootOverride == "" {
		for _, nested := range nestedRuntimeSyncDirs {
			for _, name := range nestedRuntimeFiles {
				srcPath := filepath.Join(root, runtimeSourceDir, name)
				dstPath := filepath.Join(root, nested.dir, runtimeSourceDir, name)

				src, err := osReadFile(srcPath)
				if err != nil {
					if osIsNotExist(err) {
						continue // nested module does not mirror this file
					}
					return drifted, fmt.Errorf("read %s: %w", srcPath, err)
				}

				dst, err := osReadFile(dstPath)
				if err != nil && !osIsNotExist(err) {
					return drifted, fmt.Errorf("read %s: %w", dstPath, err)
				}
				if osIsNotExist(err) {
					continue // no local copy to sync; nothing to do
				}

				if bytes.Equal(src, dst) {
					continue
				}
				drifted = true
				if checkOnly {
					continue
				}
				if err := osWriteFile(dstPath, src, 0o644); err != nil {
					return drifted, fmt.Errorf("write %s: %w", dstPath, err)
				}
			}
		}
	}
	return drifted, nil
}
