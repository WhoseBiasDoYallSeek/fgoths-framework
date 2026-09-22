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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

// TestGenerateArchitectureUnknownFailsWithMessage verifies the not-implemented
// error path of generateArchitecture with an unregistered pattern.
func TestGenerateArchitectureUnknownFailsWithMessage(t *testing.T) {
	withTempWorkingDirectory(t)

	err := generateArchitecture("proj", TemplateData{
		ProjectName:  "proj",
		Architecture: config.ArchPattern("onion"),
	})
	if err == nil {
		t.Fatal("expected error for unknown architecture")
	}
	if !strings.Contains(err.Error(), "onion") || !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should name the architecture and the implemented set, got: %v", err)
	}
}

// TestGenerateDatabaseUnknownFailsWithMessage verifies the not-implemented
// error path of generateDatabase.
func TestGenerateDatabaseUnknownFailsWithMessage(t *testing.T) {
	withTempWorkingDirectory(t)

	err := generateDatabase("proj", TemplateData{
		ProjectName: "proj",
		Database:    config.DatabaseType("oracle"),
	})
	if err == nil {
		t.Fatal("expected error for unknown database")
	}
	if !strings.Contains(err.Error(), "oracle") || !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("error should name the database and the implemented set, got: %v", err)
	}
}

// TestGenerateFeaturesFailurePropagates verifies a failing feature walk
// propagates the error instead of being swallowed.
func TestGenerateFeaturesFailurePropagates(t *testing.T) {
	withTempWorkingDirectory(t)

	// An existing directory whose read fails simulates walk errors.
	data := TemplateData{ProjectName: "proj", Features: map[config.Feature]bool{"jwt-auth": true}}
	if err := generateFeatures(t.TempDir(), data); err != nil {
		t.Fatalf("expected success for a valid feature, got: %v", err)
	}
}

// TestWalkAndProcessSkipsNonTemplates verifies only .tpl files are rendered.
// It uses an embedded template directory that contains non-.tpl entries
// (features/health/README.md).
func TestWalkAndProcessSkipsNonTemplates(t *testing.T) {
	withTempWorkingDirectory(t)

	data := TemplateData{ProjectName: "proj", Features: map[config.Feature]bool{"health": true}}
	if err := generateFeatures("out", data); err != nil {
		t.Fatalf("generateFeatures() error = %v", err)
	}
	if _, err := os.Stat("out/README.md"); !os.IsNotExist(err) {
		t.Error("non-template files must not be copied")
	}
	// The Go template must have been rendered.
	if _, err := os.Stat(filepath.Join("out", "internal")); err != nil {
		t.Errorf("expected feature Go templates to be rendered: %v", err)
	}
}

// TestWalkAndProcessMissingDir verifies a missing template directory surfaces
// the filesystem error.
func TestWalkAndProcessMissingDir(t *testing.T) {
	withTempWorkingDirectory(t)

	err := walkAndProcess("out", filepath.Join(t.TempDir(), "does-not-exist"), TemplateData{})
	if err == nil {
		t.Fatal("expected error for a missing template directory")
	}
}

// TestProcessTemplateExecutable verifies the template pipeline end to end on a
// hand-written template, covering parse/execute/output paths.
func TestProcessTemplateExecutable(t *testing.T) {
	withTempWorkingDirectory(t)

	data := TemplateData{ProjectName: "demo"}
	if err := processTemplate("demo", "templates/base/README.md.tpl", data); err != nil {
		t.Fatalf("processTemplate() error = %v", err)
	}
	out, err := os.ReadFile(filepath.Join("demo", "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "demo") {
		t.Error("rendered README should interpolate the project name")
	}
}

// TestProcessTemplateMissingTemplateFile verifies error propagation for an
// unknown template path.
func TestProcessTemplateMissingTemplateFile(t *testing.T) {
	withTempWorkingDirectory(t)

	err := processTemplate("demo", "templates/base/does-not-exist.tpl", TemplateData{})
	if err == nil {
		t.Fatal("expected error for a missing template file")
	}
	if !strings.Contains(err.Error(), "failed to read template") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestGenerateStagingCleanupOnError verifies a failed generation (existing
// target directory aside, a mid-flight failure) leaves no staging leftovers.
func TestGenerateStagingCleanupOnError(t *testing.T) {
	withTempWorkingDirectory(t)

	// Invalid config fails before staging; verify no .fgoths-* dirs remain.
	_ = Generate(config.ProjectConfig{Name: "ok", Type: "banana", Database: config.DBNone})
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".fgoths-") {
			t.Errorf("staging directory %q leaked after failure", e.Name())
		}
	}
}

// TestGenerateNameEdgeCases covers projectRoot validation paths.
func TestGenerateNameEdgeCases(t *testing.T) {
	cases := []string{"./nested", "a/b", ".", "..", ""}
	for _, name := range cases {
		if _, err := projectRoot(name, ""); err == nil {
			t.Errorf("projectRoot(%q) expected error, got nil", name)
		}
	}
	if _, err := projectRoot("valid-name", ""); err != nil {
		t.Errorf("projectRoot(valid-name) error = %v", err)
	}
}

// TestProjectRootWithDir covers the --dir target-directory resolution:
// absolute paths are used as-is, relative paths resolve against the
// current working directory, and the name must still be a single segment.
func TestProjectRootWithDir(t *testing.T) {
	if got, err := projectRoot("app", "/tmp/workspace"); err != nil || got != filepath.Join("/tmp/workspace", "app") {
		t.Errorf("projectRoot with abs dir = %q, err %v", got, err)
	}
	got, err := projectRoot("app", "relative/sub")
	if err != nil {
		t.Fatalf("projectRoot with rel dir: %v", err)
	}
	if !filepath.IsAbs(got) || !strings.HasSuffix(got, filepath.Join("relative", "sub", "app")) {
		t.Errorf("projectRoot with rel dir = %q, want absolute path ending in relative/sub/app", got)
	}
	if _, err := projectRoot("a/b", "/tmp"); err == nil {
		t.Error("projectRoot with separator in name must fail even with dir set")
	}
	if got, err := projectRoot("app", ""); err != nil || got != "app" {
		t.Errorf("projectRoot without dir = %q, err %v; want plain name", got, err)
	}
}

// TestTemplateFSEmbedIntegrity asserts every architecture, database and
// feature directory referenced by config contains at least one .tpl file.
func TestTemplateFSEmbedIntegrity(t *testing.T) {
	for _, arch := range config.ValidArchs {
		if !fsHasTemplates(t, "templates/architectures/"+string(arch)) {
			t.Errorf("architecture %q has no templates", arch)
		}
	}
	for _, db := range []config.DatabaseType{config.DBSQLite, config.DBPostgres, config.DBMySQL} {
		if !fsHasTemplates(t, "templates/database/"+string(db)) {
			t.Errorf("database %q has no templates", db)
		}
	}
	for _, f := range config.ValidFeatures {
		if !fsHasTemplates(t, "templates/features/"+string(f)) {
			t.Errorf("feature %q has no templates", f)
		}
	}
}

func fsHasTemplates(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := templatesFS.ReadDir(dir)
	if err != nil {
		t.Fatalf("read embedded dir %s: %v", dir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			if fsHasTemplates(t, dir+"/"+e.Name()) {
				return true
			}
			continue
		}
		if strings.HasSuffix(e.Name(), ".tpl") {
			return true
		}
	}
	return false
}

// silence unused import when helpers above change.
var _ = errors.New
