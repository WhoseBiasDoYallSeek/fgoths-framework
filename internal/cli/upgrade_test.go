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
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/generator"
)

func TestUpgradeDryRunDoesNotModifyProject(t *testing.T) {
	root, baseline, _ := newLegacyUpgradeProject(t)
	before, err := os.ReadFile(filepath.Join(root, runtimeUpgradePaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	report, err := planRuntimeUpgrade(root, "v1.1.0", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.actions) != len(runtimeUpgradePaths) {
		t.Fatalf("planned %d files, want %d", len(report.actions), len(runtimeUpgradePaths))
	}
	if report.artifactIgnore == nil ||
		!hasIgnoreRule(report.artifactIgnore.content, "/upgrade-backups/") ||
		!hasIgnoreRule(report.artifactIgnore.content, "/upgrade-conflicts/") {
		t.Fatal("dry-run did not plan Git ignores for upgrade artifacts")
	}
	for _, action := range report.actions {
		if action.status != "update" {
			t.Errorf("%s status = %q, want update", action.path, action.status)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, runtimeUpgradePaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("dry-run modified a runtime file")
	}
	if !bytes.Equal(before, baseline[runtimeUpgradePaths[0]]) {
		t.Fatal("test project did not start with the expected v1.1.0 runtime")
	}
	if _, err := os.Stat(filepath.Join(root, ".fgoths")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created metadata or backups: %v", err)
	}
}

func TestRunUpgradeDryRunAndApplyFlags(t *testing.T) {
	root, baseline, targets := newLegacyUpgradeProject(t)
	var dryRunErr error
	output := captureStdout(t, func() {
		dryRunErr = RunUpgrade([]string{"--from=1.1.0", "--dir=" + root})
	})
	if dryRunErr != nil {
		t.Fatalf("RunUpgrade dry-run error = %v", dryRunErr)
	}
	if !strings.Contains(output, "Dry run only") {
		t.Fatalf("dry-run output missing confirmation: %s", output)
	}
	serverBefore, err := os.ReadFile(filepath.Join(root, "pkg/runtime/server.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(serverBefore, baseline["pkg/runtime/server.go"]) {
		t.Fatal("RunUpgrade modified project without --apply")
	}

	var applyErr error
	output = captureStdout(t, func() {
		applyErr = RunUpgrade([]string{"--from=1.1.0", "--dir=" + root, "--apply"})
	})
	if applyErr != nil {
		t.Fatalf("RunUpgrade --apply error = %v", applyErr)
	}
	if !strings.Contains(output, "Runtime upgraded to v1.3.0") {
		t.Fatalf("apply output missing success confirmation: %s", output)
	}
	serverAfter, err := os.ReadFile(filepath.Join(root, "pkg/runtime/server.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(serverAfter, targets["pkg/runtime/server.go"]) {
		t.Fatal("RunUpgrade --apply did not install the target runtime")
	}
}

func TestRunUpgradeHelp(t *testing.T) {
	var runErr error
	output := captureStdout(t, func() {
		runErr = RunUpgrade([]string{"--help"})
	})
	if runErr != nil {
		t.Fatalf("RunUpgrade --help error = %v", runErr)
	}
	if !strings.Contains(output, "Usage: fgoths upgrade") || !strings.Contains(output, "--apply") {
		t.Fatalf("upgrade help is incomplete: %s", output)
	}
}

func TestUpgradeApplyBacksUpAndWritesMetadata(t *testing.T) {
	root, baseline, targets := newLegacyUpgradeProject(t)
	report, err := planRuntimeUpgrade(root, "1.1.0", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	conflicts, err := applyRuntimeUpgrade(root, report)
	if err != nil || conflicts != 0 {
		t.Fatalf("applyRuntimeUpgrade() = (%d, %v), want (0, nil)", conflicts, err)
	}
	for _, relPath := range runtimeUpgradePaths {
		got, err := os.ReadFile(filepath.Join(root, relPath))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, targets[relPath]) {
			t.Errorf("%s was not updated to current runtime", relPath)
		}
		backupGlob := filepath.Join(root, ".fgoths", "upgrade-backups", "v1.3.0-*", relPath)
		matches, err := filepath.Glob(backupGlob)
		if err != nil || len(matches) != 1 {
			t.Errorf("backup for %s: matches=%v err=%v", relPath, matches, err)
			continue
		}
		original, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(original, baseline[relPath]) {
			t.Errorf("backup for %s does not contain the original runtime", relPath)
		}
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, ".fgoths", "upgrade.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest upgradeManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != "1.3.0" {
		t.Errorf("metadata version = %q, want 1.3.0", manifest.Version)
	}
	if len(manifest.RuntimeFiles) != len(runtimeUpgradePaths) {
		t.Errorf("metadata tracks %d runtime files, want %d", len(manifest.RuntimeFiles), len(runtimeUpgradePaths))
	}
	ignoreFile, err := os.ReadFile(filepath.Join(root, ".fgoths", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range upgradeArtifactIgnoreRules {
		if !hasIgnoreRule(ignoreFile, rule) {
			t.Errorf("upgrade artifact ignore file is missing %q", rule)
		}
	}
}

func TestUpgradeMergesNonOverlappingLocalEdit(t *testing.T) {
	root, baseline, targets := newLegacyUpgradeProject(t)
	relPath := "pkg/runtime/server.go"
	local := append(append([]byte(nil), baseline[relPath]...), []byte("\n// organization-owned extension\n")...)
	if err := os.WriteFile(filepath.Join(root, relPath), local, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := planRuntimeUpgrade(root, "1.1.0", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	var serverAction *upgradeFileAction
	for i := range report.actions {
		if report.actions[i].path == relPath {
			serverAction = &report.actions[i]
			break
		}
	}
	if serverAction == nil || serverAction.status != "merge" {
		t.Fatalf("server action = %#v, want merge", serverAction)
	}
	if conflicts, err := applyRuntimeUpgrade(root, report); err != nil || conflicts != 0 {
		t.Fatalf("applyRuntimeUpgrade() = (%d, %v), want (0, nil)", conflicts, err)
	}
	got, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("// organization-owned extension")) ||
		!bytes.Contains(got, []byte("type Registrar interface")) ||
		bytes.Equal(got, targets[relPath]) {
		t.Fatal("clean merge did not preserve the local edit and upstream change")
	}
}

func TestUpgradePreservesOriginalOnMergeConflict(t *testing.T) {
	root, baseline, _ := newLegacyUpgradeProject(t)
	relPath := "pkg/runtime/metrics.go"
	local := bytes.Replace(baseline[relPath], []byte(`if route == ""`), []byte(`if route == "local"`), 1)
	if bytes.Equal(local, baseline[relPath]) {
		t.Fatal("test did not modify the baseline line removed upstream")
	}
	if err := os.WriteFile(filepath.Join(root, relPath), local, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := planRuntimeUpgrade(root, "1.1.0", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	var conflictAction *upgradeFileAction
	for i := range report.actions {
		if report.actions[i].path == relPath {
			conflictAction = &report.actions[i]
			break
		}
	}
	if conflictAction == nil || conflictAction.status != "conflict" {
		t.Fatalf("metrics action = %#v, want conflict", conflictAction)
	}
	conflicts, err := applyRuntimeUpgrade(root, report)
	if err != nil || conflicts != 1 {
		t.Fatalf("applyRuntimeUpgrade() = (%d, %v), want (1, nil)", conflicts, err)
	}
	got, err := os.ReadFile(filepath.Join(root, relPath))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, local) {
		t.Fatal("conflict handling modified the user's original file")
	}
	candidate, err := os.ReadFile(filepath.Join(root, conflictAction.conflictTo))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(candidate, []byte("<<<<<<<")) {
		t.Fatal("saved merge candidate has no conflict markers")
	}
	if _, err := os.Stat(filepath.Join(root, ".fgoths", "upgrade.json")); !os.IsNotExist(err) {
		t.Fatalf("metadata advanced despite unresolved conflict: %v", err)
	}
}

func TestUpgradeRequiresVersionForLegacyProjectAndRejectsUnsupportedVersion(t *testing.T) {
	root, _, _ := newLegacyUpgradeProject(t)
	if _, err := planRuntimeUpgrade(root, "", "1.3.0"); err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("missing legacy version error = %v, want --from guidance", err)
	}
	if _, err := planRuntimeUpgrade(root, "1.0.0", "1.3.0"); err == nil || !strings.Contains(err.Error(), "unsupported runtime upgrade") {
		t.Fatalf("unsupported version error = %v", err)
	}
}

func TestUpgradeUsesProjectMetadataVersion(t *testing.T) {
	root, _, _ := newLegacyUpgradeProject(t)
	metaDir := filepath.Join(root, ".fgoths")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metaDir, "upgrade.json"), []byte(`{"framework_version":"1.1.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := planRuntimeUpgrade(root, "", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.from != "1.1.0" {
		t.Fatalf("source version = %q, want 1.1.0", report.from)
	}
	if _, err := planRuntimeUpgrade(root, "1.0.0", "1.3.0"); err == nil {
		t.Fatal("expected explicit version mismatch to be rejected")
	}
}

func TestUpgradePreservesProjectConfigurationMetadata(t *testing.T) {
	root, _, _ := newLegacyUpgradeProject(t)
	metaDir := filepath.Join(root, ".fgoths")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	originalMetadata := []byte(`{
		"framework_version": "1.1.0",
		"project_type": "ssr",
		"architecture": "mvc",
		"database": "sqlite",
		"features": ["htmx", "jwt-auth"],
		"runtime_files": {
			"pkg/runtime/router.go": "preserve-router-hash"
		}
	}`)
	if err := os.WriteFile(filepath.Join(metaDir, "upgrade.json"), originalMetadata, 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := planRuntimeUpgrade(root, "", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if conflicts, err := applyRuntimeUpgrade(root, report); err != nil || conflicts != 0 {
		t.Fatalf("applyRuntimeUpgrade() = (%d, %v), want (0, nil)", conflicts, err)
	}
	updatedBytes, err := os.ReadFile(filepath.Join(metaDir, "upgrade.json"))
	if err != nil {
		t.Fatal(err)
	}
	var updated upgradeManifest
	if err := json.Unmarshal(updatedBytes, &updated); err != nil {
		t.Fatal(err)
	}
	if updated.Version != "1.3.0" ||
		updated.ProjectType != "ssr" ||
		updated.Architecture != "mvc" ||
		updated.Database != "sqlite" ||
		strings.Join(updated.Features, ",") != "htmx,jwt-auth" ||
		updated.RuntimeFiles["pkg/runtime/router.go"] != "preserve-router-hash" {
		t.Fatalf("updated metadata lost project configuration: %#v", updated)
	}
	matches, err := filepath.Glob(filepath.Join(root, ".fgoths", "upgrade-backups", "v1.3.0-*", ".fgoths", "upgrade.json"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("metadata backup matches=%v err=%v", matches, err)
	}
	backedUp, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backedUp, originalMetadata) {
		t.Fatal("upgrade did not preserve previous metadata in its backup")
	}
}

func TestUpgradePreservesExistingArtifactIgnoreRules(t *testing.T) {
	root, _, _ := newLegacyUpgradeProject(t)
	metaDir := filepath.Join(root, ".fgoths")
	if err := os.MkdirAll(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ignorePath := filepath.Join(metaDir, ".gitignore")
	original := []byte("# local FGOTHS ignores\ncustom-artifact/\n")
	if err := os.WriteFile(ignorePath, original, 0o640); err != nil {
		t.Fatal(err)
	}
	report, err := planRuntimeUpgrade(root, "1.1.0", "1.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if report.artifactIgnore == nil {
		t.Fatal("upgrade plan omitted missing artifact ignore rules")
	}
	if conflicts, err := applyRuntimeUpgrade(root, report); err != nil || conflicts != 0 {
		t.Fatalf("applyRuntimeUpgrade() = (%d, %v), want (0, nil)", conflicts, err)
	}
	updated, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(updated, original) {
		t.Fatal("existing .fgoths/.gitignore rules were not preserved")
	}
	for _, rule := range upgradeArtifactIgnoreRules {
		if !hasIgnoreRule(updated, rule) {
			t.Errorf("updated artifact ignore file is missing %q", rule)
		}
	}
	matches, err := filepath.Glob(filepath.Join(root, ".fgoths", "upgrade-backups", "v1.3.0-*", ".fgoths", ".gitignore"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("ignore file backup matches=%v err=%v", matches, err)
	}
	backedUp, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backedUp, original) {
		t.Fatal("upgrade did not preserve previous .fgoths/.gitignore in its backup")
	}
}

func TestPlanUpgradeArtifactIgnoreDoesNothingWhenRulesExist(t *testing.T) {
	root := t.TempDir()
	metaDir := filepath.Join(root, ".fgoths")
	if err := os.Mkdir(metaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("/upgrade-backups/\r\n/upgrade-conflicts/\r\n")
	if err := os.WriteFile(filepath.Join(metaDir, ".gitignore"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	action, err := planUpgradeArtifactIgnore(root)
	if err != nil {
		t.Fatal(err)
	}
	if action != nil {
		t.Fatalf("planUpgradeArtifactIgnore() = %#v, want nil", action)
	}
}

func TestSafeProjectPathRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "runtime")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "pkg")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, _, err := safeProjectPath(root, "pkg/runtime/server.go"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink path error = %v, want refusal", err)
	}
}

func newLegacyUpgradeProject(t *testing.T) (string, map[string][]byte, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/legacy\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline, err := runtimeUpgradeBaseline("1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	targets, err := generator.ManagedRuntimeSources()
	if err != nil {
		t.Fatal(err)
	}
	for _, relPath := range runtimeUpgradePaths {
		path := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, baseline[relPath], 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root, baseline, targets
}

func TestMergeWithGitReportsConflict(t *testing.T) {
	if _, err := execLookPathGit(); err != nil {
		t.Skip("git unavailable")
	}
	current := []byte("first\nlocal\n")
	base := []byte("first\nbase\n")
	target := []byte("first\nupstream\n")
	merged, err := mergeWithGit(current, base, target)
	if !errors.Is(err, errMergeConflict) {
		t.Fatalf("mergeWithGit() error = %v, want conflict", err)
	}
	if !bytes.Contains(merged, []byte("<<<<<<<")) {
		t.Fatal("merge conflict output is missing markers")
	}
}

func execLookPathGit() (string, error) {
	return exec.LookPath("git")
}

func TestUpgradeFromV120PreservesLocalEditsAndRecordsMetadata(t *testing.T) {
	if _, err := execLookPathGit(); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/v120\n\ngo 1.26.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline, err := runtimeUpgradeBaseline("1.2.0")
	if err != nil {
		t.Fatal(err)
	}
	const edited = "pkg/runtime/server.go"
	localServer := append(append([]byte(nil), baseline[edited]...), []byte("\n// local customization\n")...)
	for _, relPath := range runtimeUpgradePaths {
		content := baseline[relPath]
		if relPath == edited {
			content = localServer
		}
		path := filepath.Join(root, relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report, err := planRuntimeUpgrade(root, "1.2.0", latestRuntimeUpgradeVersion)
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range report.actions {
		if action.status != "current" {
			t.Errorf("%s status = %q, want current", action.path, action.status)
		}
	}
	captureStdout(t, func() {
		err = RunUpgrade([]string{"--from=v1.2.0", "--dir=" + root, "--apply"})
	})
	if err != nil {
		t.Fatalf("RunUpgrade --apply error = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, edited))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, localServer) {
		t.Fatal("upgrade from v1.2.0 discarded a local runtime edit")
	}
	data, err := os.ReadFile(filepath.Join(root, ".fgoths", "upgrade.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest upgradeManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != latestRuntimeUpgradeVersion {
		t.Fatalf("metadata version = %q, want %s", manifest.Version, latestRuntimeUpgradeVersion)
	}
}
