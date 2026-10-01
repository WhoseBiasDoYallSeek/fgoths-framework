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
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
)

var errInjected = errors.New("injected failure")

func replaceHook[T any](t *testing.T, target *T, value T) {
	t.Helper()
	original := *target
	*target = value
	t.Cleanup(func() { *target = original })
}

func wantErrContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to contain %q", err, want)
	}
}

func writeUpgradeTestFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	path := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdirUpgradeTestDir(t *testing.T, root, relPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, relPath), 0o755); err != nil {
		t.Fatal(err)
	}
}

func failReadFor(t *testing.T, suffix string) {
	t.Helper()
	replaceHook(t, &readProjectFile, func(name string) ([]byte, error) {
		if strings.HasSuffix(name, suffix) {
			return nil, errInjected
		}
		return os.ReadFile(name)
	})
}

func runUpgradeQuietly(t *testing.T, args ...string) error {
	t.Helper()
	var err error
	captureStdout(t, func() { err = RunUpgrade(args) })
	return err
}

func TestExecuteUpgradeFailureExits(t *testing.T) {
	executeStubMu.Lock()
	defer executeStubMu.Unlock()

	originalArgs := os.Args
	os.Args = []string{"fgoths", "upgrade", "unexpected"}
	t.Cleanup(func() { os.Args = originalArgs })
	code := 0
	replaceHook(t, &osExit, func(c int) { code = c })

	output := captureStdout(t, Execute)
	if code != 1 || !strings.Contains(output, "positional arguments") {
		t.Fatalf("Execute(upgrade) code = %d, output = %q", code, output)
	}
}

func TestFrameworkVersionSources(t *testing.T) {
	replaceHook(t, &Version, "1.2.0")
	if got := frameworkVersion(); got != "1.2.0" {
		t.Fatalf("frameworkVersion() with release ldflags = %q", got)
	}

	replaceHook(t, &Version, "0.0.0-dev")
	replaceHook(t, &readBuildInfo, func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.2.0"}}, true
	})
	if got := frameworkVersion(); got != "1.2.0" {
		t.Fatalf("frameworkVersion() with module version = %q", got)
	}

	replaceHook(t, &readBuildInfo, func() (*debug.BuildInfo, bool) { return nil, false })
	if got := frameworkVersion(); got != "0.0.0-dev" {
		t.Fatalf("frameworkVersion() without build info = %q", got)
	}
}

func TestRunUpgradeRejectsInvalidInvocations(t *testing.T) {
	t.Run("mismatched CLI version", func(t *testing.T) {
		replaceHook(t, &Version, "1.3.0")
		wantErrContains(t, runUpgradeQuietly(t), "reports v1.3.0")
	})
	t.Run("unknown flag", func(t *testing.T) {
		wantErrContains(t, runUpgradeQuietly(t, "--unknown"), "flag provided but not defined")
	})
	t.Run("positional argument", func(t *testing.T) {
		wantErrContains(t, runUpgradeQuietly(t, "extra"), "positional arguments")
	})
	t.Run("absolute path failure", func(t *testing.T) {
		replaceHook(t, &resolveAbsPath, func(string) (string, error) { return "", errInjected })
		wantErrContains(t, runUpgradeQuietly(t), "resolve project directory")
	})
	t.Run("missing directory", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "missing")
		wantErrContains(t, runUpgradeQuietly(t, "--dir="+missing), "resolve project directory")
	})
	t.Run("planning failure", func(t *testing.T) {
		wantErrContains(t, runUpgradeQuietly(t, "--dir="+t.TempDir(), "--from=1.1.0"), "go.mod not found")
	})
}

func TestRunUpgradeReportsCurrentProject(t *testing.T) {
	root, _, _ := newLegacyUpgradeProject(t)
	writeUpgradeTestFile(t, root, ".fgoths/upgrade.json", `{"framework_version":"1.2.0"}`)
	var err error
	output := captureStdout(t, func() { err = RunUpgrade([]string{"--dir=" + root}) })
	if err != nil || !strings.Contains(output, "already at v1.2.0") {
		t.Fatalf("RunUpgrade() error = %v, output = %q", err, output)
	}
}

func TestRunUpgradeApplyFailures(t *testing.T) {
	t.Run("write failure", func(t *testing.T) {
		root, _, _ := newLegacyUpgradeProject(t)
		replaceHook(t, &renameProjectFile, func(string, string) error { return errInjected })
		wantErrContains(t, runUpgradeQuietly(t, "--dir="+root, "--from=1.1.0", "--apply"), "write updated")
	})
	t.Run("preserved conflict", func(t *testing.T) {
		root, _, _ := newLegacyUpgradeProject(t)
		writeUpgradeTestFile(t, root, "pkg/runtime/server.go", "package runtime\n\n// local edit\n")
		replaceHook(t, &mergeRuntimeFile, func(_, _, _ []byte) ([]byte, error) {
			return []byte("candidate"), errMergeConflict
		})
		wantErrContains(t, runUpgradeQuietly(t, "--dir="+root, "--from=1.1.0", "--apply"), "1 local conflict(s)")
		candidate, err := os.ReadFile(filepath.Join(root, ".fgoths", "upgrade-conflicts", "v1.2.0", "pkg", "runtime", "server.go.merge"))
		if err != nil || string(candidate) != "candidate" {
			t.Fatalf("merge candidate = %q, err = %v", candidate, err)
		}
	})
}

func TestPlanRuntimeUpgradeProjectValidation(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string) string
		want  string
	}{
		{name: "missing root", setup: func(t *testing.T, root string) string {
			return filepath.Join(root, "missing")
		}, want: "project directory is unavailable"},
		{name: "root is a file", setup: func(t *testing.T, root string) string {
			writeUpgradeTestFile(t, root, "file", "")
			return filepath.Join(root, "file")
		}, want: "is not a directory"},
		{name: "unsafe go.mod", setup: func(t *testing.T, root string) string {
			if err := os.Symlink(filepath.Join(root, "elsewhere"), filepath.Join(root, "go.mod")); err != nil {
				t.Fatal(err)
			}
			return root
		}, want: "refusing to follow symlink"},
		{name: "missing go.mod", setup: func(t *testing.T, root string) string {
			return root
		}, want: "go.mod not found"},
		{name: "unsafe runtime path", setup: func(t *testing.T, root string) string {
			writeUpgradeTestFile(t, root, "go.mod", "module example.test/app\n")
			writeUpgradeTestFile(t, root, "pkg/runtime", "")
			return root
		}, want: "is not a directory"},
		{name: "missing runtime", setup: func(t *testing.T, root string) string {
			writeUpgradeTestFile(t, root, "go.mod", "module example.test/app\n")
			return root
		}, want: "has no pkg/runtime/server.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := tc.setup(t, t.TempDir())
			_, err := planRuntimeUpgrade(root, "1.1.0", latestRuntimeUpgradeVersion)
			wantErrContains(t, err, tc.want)
		})
	}
}

func TestPlanRuntimeUpgradeManifestAndVersionErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		from  string
		to    string
		want  string
	}{
		{name: "unsafe manifest path", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, ".fgoths", "")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "is not a directory"},
		{name: "unreadable manifest", setup: func(t *testing.T, root string) {
			mkdirUpgradeTestDir(t, root, ".fgoths/upgrade.json")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "read upgrade metadata"},
		{name: "invalid manifest", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, ".fgoths/upgrade.json", "{")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "parse .fgoths/upgrade.json"},
		{name: "unsupported target", setup: func(t *testing.T, root string) {},
			from: "1.1.0", to: "9.9.9", want: "supported target: v" + latestRuntimeUpgradeVersion},
		{name: "unsupported source", setup: func(t *testing.T, root string) {},
			from: "1.0.0", to: latestRuntimeUpgradeVersion, want: "no runtime upgrade baseline"},
		{name: "artifact ignore is a directory", setup: func(t *testing.T, root string) {
			mkdirUpgradeTestDir(t, root, ".fgoths/.gitignore")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "not a regular file"},
		{name: "unsafe runtime source path", setup: func(t *testing.T, root string) {
			if err := os.RemoveAll(filepath.Join(root, "pkg", "runtime", "hmr")); err != nil {
				t.Fatal(err)
			}
			writeUpgradeTestFile(t, root, "pkg/runtime/hmr", "")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "is not a directory"},
		{name: "runtime source is a directory", setup: func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "pkg", "runtime", "auth.go")); err != nil {
				t.Fatal(err)
			}
			mkdirUpgradeTestDir(t, root, "pkg/runtime/auth.go")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "not a regular file"},
		{name: "unreadable runtime source", setup: func(t *testing.T, root string) {
			failReadFor(t, "proxy.go")
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "read pkg/runtime/proxy.go"},
		{name: "target sources unavailable", setup: func(t *testing.T, root string) {
			replaceHook(t, &managedRuntimeSources, func() (map[string][]byte, error) { return nil, errInjected })
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "load target runtime sources"},
		{name: "target sources incomplete", setup: func(t *testing.T, root string) {
			replaceHook(t, &managedRuntimeSources, func() (map[string][]byte, error) { return map[string][]byte{}, nil })
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "metadata is incomplete"},
		{name: "merge failure", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, "pkg/runtime/server.go", "package runtime\n\n// local edit\n")
			replaceHook(t, &mergeRuntimeFile, func(_, _, _ []byte) ([]byte, error) { return nil, errInjected })
		}, from: "1.1.0", to: latestRuntimeUpgradeVersion, want: "merge local changes in pkg/runtime/server.go"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, _, _ := newLegacyUpgradeProject(t)
			tc.setup(t, root)
			_, err := planRuntimeUpgrade(root, tc.from, tc.to)
			wantErrContains(t, err, tc.want)
		})
	}
}

func TestPlanRuntimeUpgradeClassifiesUnchangedFiles(t *testing.T) {
	root, _, targets := newLegacyUpgradeProject(t)
	if err := os.Remove(filepath.Join(root, "pkg", "runtime", "auth.go")); err != nil {
		t.Fatal(err)
	}
	writeUpgradeTestFile(t, root, "pkg/runtime/server.go", string(targets["pkg/runtime/server.go"]))
	localProxy := "package runtime\n\n// already merged locally\n"
	writeUpgradeTestFile(t, root, "pkg/runtime/proxy.go", localProxy)
	replaceHook(t, &mergeRuntimeFile, func(current, _, _ []byte) ([]byte, error) { return current, nil })

	report, err := planRuntimeUpgrade(root, "1.1.0", latestRuntimeUpgradeVersion)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(map[string]string)
	for _, action := range report.actions {
		statuses[action.path] = action.status
	}
	if _, ok := statuses["pkg/runtime/auth.go"]; ok {
		t.Fatal("missing auth.go should be skipped")
	}
	if statuses["pkg/runtime/server.go"] != "current" || statuses["pkg/runtime/proxy.go"] != "current" {
		t.Fatalf("statuses = %v, want server.go and proxy.go current", statuses)
	}
}

func TestMergeWithGitFailures(t *testing.T) {
	t.Run("git unavailable", func(t *testing.T) {
		replaceHook(t, &lookPathGit, func(string) (string, error) { return "", errInjected })
		_, err := mergeWithGit(nil, nil, nil)
		wantErrContains(t, err, "git is required")
	})
	t.Run("workspace failure", func(t *testing.T) {
		replaceHook(t, &lookPathGit, func(string) (string, error) { return "git", nil })
		replaceHook(t, &makeMergeWorkspace, func(string, string) (string, error) { return "", errInjected })
		_, err := mergeWithGit(nil, nil, nil)
		wantErrContains(t, err, "create merge workspace")
	})
	t.Run("input failure", func(t *testing.T) {
		replaceHook(t, &lookPathGit, func(string) (string, error) { return "git", nil })
		replaceHook(t, &writeMergeInput, func(string, []byte, os.FileMode) error { return errInjected })
		_, err := mergeWithGit(nil, nil, nil)
		wantErrContains(t, err, "write merge input")
	})
	t.Run("process failure", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "git")
		replaceHook(t, &lookPathGit, func(string) (string, error) { return missing, nil })
		_, err := mergeWithGit([]byte("a"), []byte("b"), []byte("c"))
		wantErrContains(t, err, "git merge-file failed")
	})
}

func TestRuntimeUpgradeBaselineMissingEmbeddedFile(t *testing.T) {
	paths := append(append([]string(nil), runtimeUpgradePaths...), "pkg/runtime/missing.go")
	replaceHook(t, &runtimeUpgradePaths, paths)
	_, err := runtimeUpgradeBaseline("1.1.0")
	wantErrContains(t, err, "read embedded v1.1.0 baseline for pkg/runtime/missing.go")
}

func TestApplyRuntimeUpgradeFileActionErrors(t *testing.T) {
	const relPath = "pkg/runtime/server.go"
	update := func(original string) upgradeFileAction {
		return upgradeFileAction{path: relPath, status: "update", original: []byte(original), content: []byte("new"), mode: 0o644}
	}
	cases := []struct {
		name   string
		setup  func(t *testing.T, root string)
		report upgradeReport
		want   string
	}{
		{name: "unsafe artifact ignore", report: upgradeReport{to: "1.2.0", artifactIgnore: &upgradeFileAction{path: "../x"}},
			want: "unsafe project path"},
		{name: "unsafe update path", report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: "../x", status: "update"}}},
			want: "unsafe project path"},
		{name: "update target missing", report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{update("old")}},
			want: "changed while upgrade was being applied"},
		{name: "update target unreadable", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old")
			failReadFor(t, "server.go")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{update("old")}}, want: "read pkg/runtime/server.go before update"},
		{name: "update target changed", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "changed")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{update("old")}}, want: "changed after the upgrade plan"},
		{name: "update backup failure", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old")
			writeUpgradeTestFile(t, root, ".fgoths/upgrade-backups", "")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{update("old")}}, want: "back up pkg/runtime/server.go"},
		{name: "update write failure", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old")
			replaceHook(t, &renameProjectFile, func(string, string) error { return errInjected })
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{update("old")}}, want: "write updated pkg/runtime/server.go"},
		{name: "unsafe conflict path", report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: "../x", status: "conflict"}}},
			want: "unsafe project path"},
		{name: "conflict original missing", report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: relPath, status: "conflict"}}},
			want: "before saving merge candidate"},
		{name: "conflict original changed", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "changed")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: relPath, status: "conflict", original: []byte("old")}}},
			want: "changed after the upgrade plan"},
		{name: "unsafe conflict candidate", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: relPath, status: "conflict", original: []byte("old"), conflictTo: "../x"}}},
			want: "save merge candidate"},
		{name: "different conflict candidate exists", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old")
			writeUpgradeTestFile(t, root, "candidate.merge", "other")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: relPath, status: "conflict", original: []byte("old"), conflict: []byte("merge"), conflictTo: "candidate.merge"}}},
			want: "save merge candidate"},
		{name: "unreadable conflict candidate", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old")
			mkdirUpgradeTestDir(t, root, "candidate.merge")
		}, report: upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{path: relPath, status: "conflict", original: []byte("old"), conflict: []byte("merge"), conflictTo: "candidate.merge"}}},
			want: "save merge candidate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, root)
			}
			var err error
			captureStdout(t, func() { _, err = applyRuntimeUpgrade(root, tc.report) })
			wantErrContains(t, err, tc.want)
		})
	}
}

func TestApplyRuntimeUpgradeAcceptsExistingIdenticalCandidate(t *testing.T) {
	root := t.TempDir()
	writeUpgradeTestFile(t, root, "pkg/runtime/server.go", "old")
	writeUpgradeTestFile(t, root, "candidate.merge", "merge")
	report := upgradeReport{to: "1.2.0", actions: []upgradeFileAction{{
		path: "pkg/runtime/server.go", status: "conflict", original: []byte("old"), conflict: []byte("merge"), conflictTo: "candidate.merge",
	}}}
	var conflicts int
	var err error
	captureStdout(t, func() { conflicts, err = applyRuntimeUpgrade(root, report) })
	if err != nil || conflicts != 1 {
		t.Fatalf("applyRuntimeUpgrade() = %d, %v; want 1 preserved conflict", conflicts, err)
	}
}

func TestApplyRuntimeUpgradeMetadataErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		{name: "unsafe metadata path", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, ".fgoths", "")
		}, want: "is not a directory"},
		{name: "unreadable metadata", setup: func(t *testing.T, root string) {
			mkdirUpgradeTestDir(t, root, ".fgoths/upgrade.json")
		}, want: "read existing upgrade metadata"},
		{name: "invalid metadata", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, ".fgoths/upgrade.json", "{")
		}, want: "parse existing upgrade metadata"},
		{name: "runtime sources unavailable", setup: func(t *testing.T, root string) {
			replaceHook(t, &managedRuntimeSources, func() (map[string][]byte, error) { return nil, errInjected })
		}, want: "load runtime sources for upgrade metadata"},
		{name: "unsafe runtime path", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, "pkg/runtime", "")
		}, want: "is not a directory"},
		{name: "metadata encoding failure", setup: func(t *testing.T, root string) {
			replaceHook(t, &marshalUpgradeManifest, func(any, string, string) ([]byte, error) { return nil, errInjected })
		}, want: "encode upgrade metadata"},
		{name: "metadata backup failure", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, ".fgoths/upgrade.json", `{"framework_version":"1.1.0"}`)
			writeUpgradeTestFile(t, root, ".fgoths/upgrade-backups", "")
		}, want: "back up existing upgrade metadata"},
		{name: "metadata write failure", setup: func(t *testing.T, root string) {
			replaceHook(t, &renameProjectFile, func(string, string) error { return errInjected })
		}, want: "write upgrade metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.setup(t, root)
			_, err := applyRuntimeUpgrade(root, upgradeReport{to: "1.2.0"})
			wantErrContains(t, err, tc.want)
		})
	}
}

func TestApplyRuntimeUpgradeInitializesMissingRuntimeHashes(t *testing.T) {
	root := t.TempDir()
	writeUpgradeTestFile(t, root, ".fgoths/upgrade.json", `{"framework_version":"1.1.0","runtime_files":null}`)
	if _, err := applyRuntimeUpgrade(root, upgradeReport{to: "1.2.0"}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".fgoths", "upgrade.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"framework_version": "1.2.0"`) || !strings.Contains(string(data), `"runtime_files": {}`) {
		t.Fatalf("metadata = %s", data)
	}
}

func TestPrintUpgradeReportVariants(t *testing.T) {
	empty := captureStdout(t, func() { printUpgradeReport(upgradeReport{from: "1.1.0", to: "1.2.0"}, false) })
	if !strings.Contains(empty, "No runtime source changes") {
		t.Fatalf("empty report = %q", empty)
	}
	report := upgradeReport{from: "1.1.0", to: "1.2.0", actions: []upgradeFileAction{
		{path: "a.go", status: "conflict", conflictTo: "a.go.merge"},
		{path: "b.go", status: "current"},
		{path: "c.go", status: "update"},
		{path: "d.go", status: "merge"},
	}}
	planned := captureStdout(t, func() { printUpgradeReport(report, false) })
	applying := captureStdout(t, func() { printUpgradeReport(report, true) })
	if !strings.Contains(planned, "(candidate: a.go.merge)") || strings.Contains(applying, "candidate:") {
		t.Fatalf("conflict output planned = %q, applying = %q", planned, applying)
	}
	if !strings.Contains(planned, "Current b.go") || !strings.Contains(planned, "Update c.go") || !strings.Contains(planned, "Merge local changes in d.go") {
		t.Fatalf("current output = %q", planned)
	}
}

func TestPlanUpgradeArtifactIgnoreVariants(t *testing.T) {
	t.Run("unsafe path", func(t *testing.T) {
		root := t.TempDir()
		writeUpgradeTestFile(t, root, ".fgoths", "")
		_, err := planUpgradeArtifactIgnore(root)
		wantErrContains(t, err, "is not a directory")
	})
	t.Run("unreadable file", func(t *testing.T) {
		root := t.TempDir()
		writeUpgradeTestFile(t, root, ".fgoths/.gitignore", "")
		failReadFor(t, ".gitignore")
		_, err := planUpgradeArtifactIgnore(root)
		wantErrContains(t, err, "read .fgoths/.gitignore")
	})
	t.Run("preserves CRLF and missing final newline", func(t *testing.T) {
		root := t.TempDir()
		writeUpgradeTestFile(t, root, ".fgoths/.gitignore", "local\r\nother")
		action, err := planUpgradeArtifactIgnore(root)
		if err != nil {
			t.Fatal(err)
		}
		want := "local\r\nother\r\n/upgrade-backups/\r\n/upgrade-conflicts/\r\n"
		if action == nil || string(action.content) != want {
			t.Fatalf("planned ignore = %+v, want %q", action, want)
		}
	})
}

func TestApplyUpgradeArtifactIgnoreErrors(t *testing.T) {
	const relPath = ".fgoths/.gitignore"
	action := upgradeFileAction{path: relPath, original: []byte("old\n"), content: []byte("new\n"), mode: 0o644}
	cases := []struct {
		name       string
		setup      func(t *testing.T, root string)
		action     upgradeFileAction
		backupRoot string
		want       string
	}{
		{name: "unsafe path", action: upgradeFileAction{path: "../x"}, want: "unsafe project path"},
		{name: "removed after planning", action: action, want: "changed after the upgrade plan"},
		{name: "new file write failure", setup: func(t *testing.T, root string) {
			replaceHook(t, &openNewProjectFile, func(string, int, os.FileMode) (*os.File, error) { return nil, errInjected })
		}, action: upgradeFileAction{path: relPath, content: []byte("new\n")}, want: "write .fgoths/.gitignore"},
		{name: "not a regular file", setup: func(t *testing.T, root string) {
			mkdirUpgradeTestDir(t, root, relPath)
		}, action: action, want: "changed after the upgrade plan"},
		{name: "unreadable file", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old\n")
			failReadFor(t, ".gitignore")
		}, action: action, want: "read .fgoths/.gitignore before update"},
		{name: "changed after planning", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "changed\n")
		}, action: action, want: "changed after the upgrade plan"},
		{name: "backup failure", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old\n")
		}, action: action, backupRoot: "../backups", want: "back up .fgoths/.gitignore"},
		{name: "write failure", setup: func(t *testing.T, root string) {
			writeUpgradeTestFile(t, root, relPath, "old\n")
			replaceHook(t, &renameProjectFile, func(string, string) error { return errInjected })
		}, action: action, want: "write .fgoths/.gitignore (backup at"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.setup != nil {
				tc.setup(t, root)
			}
			backupRoot := tc.backupRoot
			if backupRoot == "" {
				backupRoot = ".fgoths/upgrade-backups/test"
			}
			wantErrContains(t, applyUpgradeArtifactIgnore(root, backupRoot, tc.action), tc.want)
		})
	}
}

func TestSafeProjectPathRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	for _, relPath := range []string{"/absolute", ".", "..", "../outside"} {
		_, _, err := safeProjectPath(root, relPath)
		wantErrContains(t, err, "unsafe project path")
	}

	replaceHook(t, &lstatProjectPath, func(string) (os.FileInfo, error) { return nil, errInjected })
	_, _, err := safeProjectPath(root, "file")
	wantErrContains(t, err, "inspect project path file")
}

func TestProjectWritersRejectUnsafeStates(t *testing.T) {
	symlinkOnMkdir := func(t *testing.T) func(string, os.FileMode) error {
		target := t.TempDir()
		return func(path string, _ os.FileMode) error { return os.Symlink(target, path) }
	}

	t.Run("replace unsafe path", func(t *testing.T) {
		wantErrContains(t, writeProjectFile(t.TempDir(), "../x", nil, 0o644), "unsafe project path")
	})
	t.Run("replace mkdir failure", func(t *testing.T) {
		replaceHook(t, &mkdirProjectDir, func(string, os.FileMode) error { return errInjected })
		wantErrContains(t, writeProjectFile(t.TempDir(), "dir/file", nil, 0o644), errInjected.Error())
	})
	t.Run("replace parent swapped for symlink", func(t *testing.T) {
		replaceHook(t, &mkdirProjectDir, symlinkOnMkdir(t))
		wantErrContains(t, writeProjectFile(t.TempDir(), "dir/file", nil, 0o644), "refusing to follow symlink")
	})
	t.Run("replace temp file failure", func(t *testing.T) {
		replaceHook(t, &createProjectTemp, func(string, string) (*os.File, error) { return nil, errInjected })
		wantErrContains(t, writeProjectFile(t.TempDir(), "file", nil, 0o644), errInjected.Error())
	})
	t.Run("replace write failure", func(t *testing.T) {
		readOnly := filepath.Join(t.TempDir(), "read-only")
		if err := os.WriteFile(readOnly, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		replaceHook(t, &createProjectTemp, func(string, string) (*os.File, error) { return os.Open(readOnly) })
		if err := writeProjectFile(t.TempDir(), "file", []byte("content"), 0o644); err == nil {
			t.Fatal("writeProjectFile() succeeded with a read-only temp file")
		}
	})
	t.Run("create unsafe path", func(t *testing.T) {
		wantErrContains(t, writeNewProjectFile(t.TempDir(), "../x", nil, 0o644), "unsafe project path")
	})
	t.Run("create existing file", func(t *testing.T) {
		root := t.TempDir()
		writeUpgradeTestFile(t, root, "file", "")
		wantErrContains(t, writeNewProjectFile(root, "file", nil, 0o644), "refusing to overwrite")
	})
	t.Run("create mkdir failure", func(t *testing.T) {
		replaceHook(t, &mkdirProjectDir, func(string, os.FileMode) error { return errInjected })
		wantErrContains(t, writeNewProjectFile(t.TempDir(), "dir/file", nil, 0o644), errInjected.Error())
	})
	t.Run("create parent swapped for symlink", func(t *testing.T) {
		replaceHook(t, &mkdirProjectDir, symlinkOnMkdir(t))
		wantErrContains(t, writeNewProjectFile(t.TempDir(), "dir/file", nil, 0o644), "refusing to follow symlink")
	})
	t.Run("create open failure", func(t *testing.T) {
		replaceHook(t, &openNewProjectFile, func(string, int, os.FileMode) (*os.File, error) { return nil, errInjected })
		wantErrContains(t, writeNewProjectFile(t.TempDir(), "file", nil, 0o644), errInjected.Error())
	})
}
