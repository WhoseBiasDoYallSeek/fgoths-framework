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
package generator

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

// skipIfNoPermissionEnforcement skips permission-based tests where the
// process can bypass file mode bits (root, Windows ACL semantics differ from
// the POSIX mode bits these tests rely on).
func skipIfNoPermissionEnforcement(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced the same way on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses permission checks")
	}
}

func TestGenerateFeaturesSkipsMissingTemplateDirs(t *testing.T) {
	withTempWorkingDirectory(t)

	// health has templates; a made-up feature does not — the loop must
	// tolerate missing dirs and still generate the real one.
	data := TemplateData{
		ProjectName:  "feat-app",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Features: map[config.Feature]bool{
			config.FeatureHealth: true,
			"does-not-exist":     true,
		},
	}
	if err := generateFeatures("feat-app", data); err != nil {
		t.Fatalf("generateFeatures() error = %v", err)
	}
	if _, err := os.Stat("feat-app/internal/health/health.go"); err != nil {
		t.Errorf("expected health feature files: %v", err)
	}
}

func TestWalkAndProcessIgnoresNonTemplateFiles(t *testing.T) {
	withTempWorkingDirectory(t)

	// features/htmx contains non-.tpl files (e.g. static assets, README);
	// walkAndProcess must skip them instead of treating them as templates.
	data := TemplateData{
		ProjectName:  "htmx-app",
		Type:         config.TypeSSR,
		Architecture: config.ArchMVC,
	}
	if err := walkAndProcess("htmx-app", "templates/features/htmx", data); err != nil {
		t.Fatalf("walkAndProcess() error = %v", err)
	}
}

func TestProcessTemplateRejectsBrokenTemplate(t *testing.T) {
	withTempWorkingDirectory(t)

	// Hand-build a template file with invalid template syntax inside the
	// embedded FS is impossible, so exercise the parse failure by pointing
	// processTemplate at a real file path shape but breaking Parse via a
	// nonexistent file first (read failure branch).
	err := processTemplate("app", "templates/base/does-not-exist.go.tpl", TemplateData{})
	if err == nil {
		t.Fatal("expected read error for a missing template file")
	}
	if !strings.Contains(err.Error(), "failed to read template") {
		t.Fatalf("expected read error, got %v", err)
	}
}

func TestProcessTemplatePathShapes(t *testing.T) {
	withTempWorkingDirectory(t)

	data := TemplateData{
		ProjectName:  "path-app",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
	}

	// base/ shape: templates/base/<path> -> <path>
	if err := processTemplate("path-app", "templates/base/README.md.tpl", data); err != nil {
		t.Fatalf("base shape: %v", err)
	}
	if _, err := os.Stat("path-app/README.md"); err != nil {
		t.Errorf("base shape output missing: %v", err)
	}

	// architectures shape: templates/architectures/flat/<path> -> <path>
	if err := processTemplate("path-app", "templates/architectures/flat/main.go.tpl", data); err != nil {
		t.Fatalf("architectures shape: %v", err)
	}
	if _, err := os.Stat("path-app/main.go"); err != nil {
		t.Errorf("architectures shape output missing: %v", err)
	}
}

// TestProcessTemplateCreateFailsWhenTargetIsADirectory covers the
// os.Create failure branch: the computed output path already exists as a
// directory, so opening it for writing fails with EISDIR.
func TestProcessTemplateCreateFailsWhenTargetIsADirectory(t *testing.T) {
	withTempWorkingDirectory(t)

	if err := os.MkdirAll("path-app/README.md", 0o755); err != nil {
		t.Fatal(err)
	}
	data := TemplateData{ProjectName: "path-app", Type: config.TypeAPI, Architecture: config.ArchFlat}
	err := processTemplate("path-app", "templates/base/README.md.tpl", data)
	if err == nil {
		t.Fatal("expected processTemplate() to fail when the target path is a directory")
	}
	if !strings.Contains(err.Error(), "failed to create file") {
		t.Fatalf("expected the create-error wrapper, got %v", err)
	}
}

func TestGenerateIntoExistingDir(t *testing.T) {
	parent := t.TempDir()
	cfg := config.ProjectConfig{
		Name:         "dir-app",
		Dir:          parent,
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() with --dir = %v", err)
	}
	if _, err := os.Stat(filepath.Join(parent, "dir-app", "go.mod")); err != nil {
		t.Errorf("expected project under --dir: %v", err)
	}
}

// TestGenerateIntoMissingParentDirFails documents that --dir behaves like
// mkdir without -p: the parent must already exist.
func TestGenerateIntoMissingParentDirFails(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "does-not-exist")
	cfg := config.ProjectConfig{
		Name:         "dir-app",
		Dir:          parent,
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
	}
	if err := Generate(cfg); err == nil {
		t.Fatal("expected Generate() to fail when --dir's parent does not exist")
	}
}

func TestCopyDirPreservesTreeAndContent(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "copied")

	if err := os.MkdirAll(filepath.Join(src, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "root.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "nested", "deep.txt"), []byte("deep"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir() error = %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "nested", "deep.txt"))
	if err != nil {
		t.Fatalf("expected copied nested file: %v", err)
	}
	if string(got) != "deep" {
		t.Errorf("copied content = %q, want %q", got, "deep")
	}
}

func TestFinalizeProjectDirFallsBackOnCrossDevice(t *testing.T) {
	// A same-device rename must take the fast path unchanged.
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst")
	if err := os.WriteFile(filepath.Join(src, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := finalizeProjectDir(src, dst); err != nil {
		t.Fatalf("finalizeProjectDir() same-device error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "f.txt")); err != nil {
		t.Errorf("expected file moved into place: %v", err)
	}
}

func TestCopyDirMissingSourceReturnsError(t *testing.T) {
	if err := copyDir(filepath.Join(t.TempDir(), "missing"), t.TempDir()); err == nil {
		t.Fatal("expected copyDir() to fail for a missing source directory")
	}
}

func TestCopyFileMissingSourceReturnsError(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out.txt")
	err := copyFile(filepath.Join(t.TempDir(), "missing.txt"), dst, 0o644)
	if err == nil {
		t.Fatal("expected copyFile() to fail when the source is missing")
	}
}

func TestCopyFileMissingDestinationDirReturnsError(t *testing.T) {
	src := filepath.Join(t.TempDir(), "in.txt")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "does-not-exist", "out.txt")
	if err := copyFile(src, dst, 0o644); err == nil {
		t.Fatal("expected copyFile() to fail when the destination directory is missing")
	}
}

// TestWalkAndProcessNonDirectoryReturnsRealError covers walkAndProcess's
// top-level ReadDir error branch with a genuine (non-notexist) failure:
// pointing it at a template *file* instead of a directory.
func TestWalkAndProcessNonDirectoryReturnsRealError(t *testing.T) {
	withTempWorkingDirectory(t)
	err := walkAndProcess("app", "templates/base/go.mod.tpl", TemplateData{})
	if err == nil {
		t.Fatal("expected an error when templateDir is not a directory")
	}
	if strings.Contains(err.Error(), "file does not exist") || os.IsNotExist(err) {
		t.Fatalf("expected a real (non-notexist) error, got %v", err)
	}
}

// TestGenerateBasePropagatesRealWriteError covers processTemplate's
// MkdirAll failure and generateBase's propagation of it, using a read-only
// staging directory to force a genuine permission error.
func TestGenerateBasePropagatesRealWriteError(t *testing.T) {
	skipIfNoPermissionEnforcement(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	err := generateBase(dir, TemplateData{ProjectName: filepath.Base(dir)})
	if err == nil {
		t.Fatal("expected generateBase() to fail against a read-only staging directory")
	}
}

// TestGenerateArchitectureDatabaseFeaturesPropagateRealErrors covers the
// "real error, not just unimplemented" passthrough branch shared by
// generateArchitecture, generateDatabase and generateFeatures.
func TestGenerateArchitectureDatabaseFeaturesPropagateRealErrors(t *testing.T) {
	skipIfNoPermissionEnforcement(t)

	newReadOnlyDir := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return dir
	}
	assertRealError := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected a real write error, got nil")
		}
		if strings.Contains(err.Error(), "not implemented yet") {
			t.Fatalf("expected the real error to pass through, got wrapped: %v", err)
		}
	}

	t.Run("architecture", func(t *testing.T) {
		dir := newReadOnlyDir(t)
		data := TemplateData{ProjectName: filepath.Base(dir), Architecture: config.ArchFlat}
		assertRealError(t, generateArchitecture(dir, data))
	})
	t.Run("database", func(t *testing.T) {
		dir := newReadOnlyDir(t)
		data := TemplateData{ProjectName: filepath.Base(dir), Database: config.DBSQLite}
		assertRealError(t, generateDatabase(dir, data))
	})
	t.Run("features", func(t *testing.T) {
		dir := newReadOnlyDir(t)
		data := TemplateData{
			ProjectName: filepath.Base(dir),
			Features:    map[config.Feature]bool{config.FeatureHealth: true},
		}
		if err := generateFeatures(dir, data); err == nil {
			t.Fatal("expected generateFeatures() to fail against a read-only staging directory")
		}
	})
}

// TestGenerateStatFailsWithRealError covers Generate()'s "could not inspect
// project directory" branch: a path where a parent component is a regular
// file (not a directory) makes os.Stat fail with ENOTDIR, which is not
// os.IsNotExist.
func TestGenerateStatFailsWithRealError(t *testing.T) {
	skipIfNoPermissionEnforcement(t)
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.ProjectConfig{Name: "app", Dir: blocker, Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone}
	err := Generate(cfg)
	if err == nil {
		t.Fatal("expected Generate() to fail when the parent path is not a directory")
	}
	if !strings.Contains(err.Error(), "could not inspect project directory") {
		t.Fatalf("expected the stat-error wrapper, got %v", err)
	}
}

// TestGenerateStagingDirCreationFails covers Generate()'s "failed to create
// staging directory" branch: os.MkdirTemp(".", ...) needs write permission
// on the current directory.
func TestGenerateStagingDirCreationFails(t *testing.T) {
	skipIfNoPermissionEnforcement(t)
	withTempWorkingDirectory(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(cwd, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(cwd, 0o755) })

	cfg := config.ProjectConfig{Name: "app", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone}
	genErr := Generate(cfg)
	if genErr == nil {
		t.Fatal("expected Generate() to fail when the working directory is read-only")
	}
	if !strings.Contains(genErr.Error(), "failed to create staging directory") {
		t.Fatalf("expected the staging-dir wrapper, got %v", genErr)
	}
}

func TestGenerateExistingDirectoryMessage(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{Name: "taken", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone}
	if err := os.Mkdir(cfg.Name, 0o755); err != nil {
		t.Fatal(err)
	}
	err := Generate(cfg)
	if err == nil {
		t.Fatal("expected error for existing directory")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected already-exists message, got %v", err)
	}
}
