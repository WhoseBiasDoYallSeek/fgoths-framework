// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
package generator

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

func TestGenerateStageErrorsCleanStagingDirectory(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.ProjectConfig
		set  func() func()
	}{
		{
			name: "base",
			cfg:  coverageProjectConfig(),
			set: func() func() {
				original := generateBaseStage
				generateBaseStage = func(string, TemplateData) error { return errors.New("base failed") }
				return func() { generateBaseStage = original }
			},
		},
		{
			name: "architecture",
			cfg:  coverageProjectConfig(),
			set: func() func() {
				original := generateArchitectureStage
				generateArchitectureStage = func(string, TemplateData) error { return errors.New("architecture failed") }
				return func() { generateArchitectureStage = original }
			},
		},
		{
			name: "database",
			cfg: config.ProjectConfig{
				Name: "app", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBSQLite,
			},
			set: func() func() {
				original := generateDatabaseStage
				generateDatabaseStage = func(string, TemplateData) error { return errors.New("database failed") }
				return func() { generateDatabaseStage = original }
			},
		},
		{
			name: "features",
			cfg: config.ProjectConfig{
				Name: "app", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone,
				Features: []config.Feature{config.FeatureHealth},
			},
			set: func() func() {
				original := generateFeaturesStage
				generateFeaturesStage = func(string, TemplateData) error { return errors.New("features failed") }
				return func() { generateFeaturesStage = original }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTempWorkingDirectory(t)
			restore := tt.set()
			t.Cleanup(restore)
			if err := Generate(tt.cfg); err == nil {
				t.Fatal("Generate() expected injected stage failure")
			}
			entries, err := os.ReadDir(".")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".fgoths-") {
					t.Errorf("staging directory %q was not cleaned up", entry.Name())
				}
			}
		})
	}
}

func TestGenerateWarnsWhenGitignoreRenameFails(t *testing.T) {
	withTempWorkingDirectory(t)
	original := renameGeneratedPath
	t.Cleanup(func() { renameGeneratedPath = original })
	renameGeneratedPath = func(oldPath, newPath string) error {
		if filepath.Base(oldPath) == "gitignore" {
			return errors.New("rename denied")
		}
		return original(oldPath, newPath)
	}

	output := captureGeneratorStdout(t, func() {
		if err := Generate(coverageProjectConfig()); err != nil {
			t.Fatalf("Generate() error = %v", err)
		}
	})
	if !strings.Contains(output, "could not rename gitignore") {
		t.Fatalf("expected warning about gitignore rename, got %q", output)
	}
}

func TestFinalizeProjectDirCrossDeviceFallback(t *testing.T) {
	original := renameGeneratedPath
	t.Cleanup(func() { renameGeneratedPath = original })

	t.Run("copies then removes staging directory", func(t *testing.T) {
		src := t.TempDir()
		dst := filepath.Join(t.TempDir(), "app")
		if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main"), 0o644); err != nil {
			t.Fatal(err)
		}
		renameGeneratedPath = func(string, string) error { return syscall.EXDEV }
		if err := finalizeProjectDir(src, dst); err != nil {
			t.Fatalf("finalizeProjectDir() error = %v", err)
		}
		if _, err := os.Stat(src); !os.IsNotExist(err) {
			t.Errorf("staging directory still exists after fallback: %v", err)
		}
		if content, err := os.ReadFile(filepath.Join(dst, "main.go")); err != nil || string(content) != "package main" {
			t.Errorf("copied content = %q, err = %v", content, err)
		}
	})

	t.Run("returns copy failure", func(t *testing.T) {
		src := t.TempDir()
		if err := os.WriteFile(filepath.Join(src, "main.go"), []byte("package main"), 0o644); err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(t.TempDir(), "not-a-directory")
		if err := os.WriteFile(dst, []byte("blocker"), 0o644); err != nil {
			t.Fatal(err)
		}
		renameGeneratedPath = func(string, string) error { return syscall.EXDEV }
		if err := finalizeProjectDir(src, dst); err == nil {
			t.Fatal("finalizeProjectDir() expected a copy failure")
		}
	})
}

func TestProjectRootReportsUnavailableWorkingDirectory(t *testing.T) {
	original := absoluteTargetPath
	t.Cleanup(func() { absoluteTargetPath = original })
	absoluteTargetPath = func(string) (string, error) { return "", errors.New("working directory unavailable") }
	if _, err := projectRoot("app", "relative"); err == nil {
		t.Fatal("projectRoot() expected to report failure resolving the target directory")
	}
}

func TestTemplateOutputPathShapes(t *testing.T) {
	tests := map[string]string{
		"templates/base/README.md.tpl":             "README.md",
		"templates/architectures/flat/main.go.tpl": "main.go",
		"templates/features/health":                "health",
		"templates/custom/file.tpl":                "file",
		"templates":                                "",
	}
	for path, want := range tests {
		if got := templateOutputPath(path); got != want {
			t.Errorf("templateOutputPath(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRenderAndFormatTemplateErrors(t *testing.T) {
	if _, err := renderTemplate("broken.tpl", "broken.tpl", "{{", TemplateData{}); err == nil || !strings.Contains(err.Error(), "failed to parse template") {
		t.Fatalf("renderTemplate() parse error = %v", err)
	}
	if _, err := renderTemplate("broken.tpl", "broken.tpl", "{{.Missing.Value}}", TemplateData{}); err == nil || !strings.Contains(err.Error(), "failed to execute template") {
		t.Fatalf("renderTemplate() execute error = %v", err)
	}
	if _, err := formatTemplateGo("broken.go.tpl", []byte("package main\nfunc {")); err == nil || !strings.Contains(err.Error(), "rendered invalid Go") {
		t.Fatalf("formatTemplateGo() error = %v", err)
	}
}

func TestProcessTemplateReportsRenderAndFormatErrors(t *testing.T) {
	withTempWorkingDirectory(t)
	originalRender, originalFormat := renderTemplateSource, formatTemplateSource
	t.Cleanup(func() {
		renderTemplateSource, formatTemplateSource = originalRender, originalFormat
	})

	renderTemplateSource = func(string, string, string, TemplateData) ([]byte, error) {
		return nil, errors.New("render failed")
	}
	if err := processTemplate("app", "templates/base/README.md.tpl", TemplateData{}); err == nil || !strings.Contains(err.Error(), "render failed") {
		t.Fatalf("processTemplate() render error = %v", err)
	}

	renderTemplateSource = originalRender
	formatTemplateSource = func(string, []byte) ([]byte, error) {
		return nil, errors.New("format failed")
	}
	if err := processTemplate("app", "templates/architectures/flat/main.go.tpl", TemplateData{}); err == nil || !strings.Contains(err.Error(), "format failed") {
		t.Fatalf("processTemplate() format error = %v", err)
	}
}

func coverageProjectConfig() config.ProjectConfig {
	return config.ProjectConfig{Name: "app", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone}
}

func captureGeneratorStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = original })
	fn()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = original
	captured, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(captured)
}
