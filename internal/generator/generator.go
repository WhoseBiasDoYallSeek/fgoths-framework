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
	"embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

//go:embed all:templates
var templatesFS embed.FS

// TemplateData holds the data passed to templates
type TemplateData struct {
	ProjectName  string
	Type         config.ProjectType
	Architecture config.ArchPattern
	Database     config.DatabaseType
	Features     map[config.Feature]bool
}

func (d TemplateData) Has(feature string) bool {
	return d.Features[config.Feature(feature)]
}

func (d TemplateData) UseSQLite() bool   { return d.Database == config.DBSQLite }
func (d TemplateData) UsePostgres() bool { return d.Database == config.DBPostgres }
func (d TemplateData) UseMySQL() bool    { return d.Database == config.DBMySQL }
func (d TemplateData) UseDatabase() bool { return d.Database != config.DBNone }

func (d TemplateData) IsMVC() bool { return d.Architecture == config.ArchMVC }

func (d TemplateData) IsAPI() bool { return d.Type == config.TypeAPI }
func (d TemplateData) IsSSR() bool { return d.Type == config.TypeSSR }

// Placeholder returns the SQL bind placeholder for the configured database.
func (d TemplateData) Placeholder() string {
	if d.Database == config.DBPostgres {
		return "$1"
	}
	return "?"
}

// Generate creates a new project from configuration
func Generate(cfg config.ProjectConfig) (err error) {
	// Apply defaults then validate configuration
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	projectRoot, err := projectRoot(cfg.Name, cfg.Dir)
	if err != nil {
		return err
	}

	// Refuse existing directories so generation cannot overwrite a project.
	if _, err := os.Stat(projectRoot); err == nil {
		return fmt.Errorf("project directory %q already exists", cfg.Name)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("could not inspect project directory: %w", err)
	}

	stagingRoot, err := os.MkdirTemp(".", ".fgoths-")
	if err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(stagingRoot)
		}
	}()

	// Prepare template data
	data := TemplateData{
		ProjectName:  filepath.Base(projectRoot),
		Type:         cfg.Type,
		Architecture: cfg.Architecture,
		Database:     cfg.Database,
		Features:     make(map[config.Feature]bool),
	}
	for _, f := range cfg.Features {
		data.Features[f] = true
	}

	fmt.Printf("📦 Generating %s project: %s\n", cfg.Type, cfg.Name)
	fmt.Printf("🏗️  Architecture: %s\n", cfg.Architecture)
	fmt.Printf("🗄️  Database: %s\n", cfg.Database)
	if len(cfg.Features) > 0 {
		fmt.Printf("✨ Features: %v\n", cfg.Features)
	}
	fmt.Println()

	// Generate base files (always)
	if err := generateBase(stagingRoot, data); err != nil {
		return fmt.Errorf("failed to generate base: %w", err)
	}

	// Generate architecture-specific structure
	if err := generateArchitecture(stagingRoot, data); err != nil {
		return fmt.Errorf("failed to generate architecture: %w", err)
	}

	// Generate database layer
	if cfg.Database != config.DBNone {
		if err := generateDatabase(stagingRoot, data); err != nil {
			return fmt.Errorf("failed to generate database: %w", err)
		}
	}

	// Generate optional features
	if err := generateFeatures(stagingRoot, data); err != nil {
		return fmt.Errorf("failed to generate features: %w", err)
	}

	// Rename .gitignore
	gitignorePath := filepath.Join(stagingRoot, ".gitignore")
	if err := os.Rename(
		filepath.Join(stagingRoot, "gitignore"),
		gitignorePath,
	); err != nil && !os.IsNotExist(err) {
		fmt.Printf("  ⚠ Warning: could not rename gitignore: %v\n", err)
	}

	if err := finalizeProjectDir(stagingRoot, projectRoot); err != nil {
		return fmt.Errorf("failed to finalize project generation: %w", err)
	}

	return nil
}

// finalizeProjectDir moves the staging directory into its final location.
// os.Rename fails with EXDEV when stagingRoot and projectRoot live on
// different filesystems (e.g. --dir points outside the current volume), so
// that case falls back to a recursive copy followed by removing the staging
// directory.
func finalizeProjectDir(stagingRoot, projectRoot string) error {
	err := os.Rename(stagingRoot, projectRoot)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := copyDir(stagingRoot, projectRoot); err != nil {
		return err
	}
	return os.RemoveAll(stagingRoot)
}

// copyDir recursively copies src into dst, preserving file modes.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target, info.Mode())
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()

	_, err = io.Copy(out, in)
	return err
}

// projectRoot resolves the full path the project directory is created at:
// name must be a single directory name (no separators, no dot-dot); when
// dir is empty the project is created in the current working directory,
// otherwise dir (absolute or relative) is the parent.
func projectRoot(name, dir string) (string, error) {
	if filepath.Base(name) != name || name == "." || name == ".." || strings.ContainsRune(name, filepath.Separator) || strings.ContainsRune(name, '/') {
		return "", fmt.Errorf("project name must be a single directory name; use --dir to choose a target directory")
	}
	if dir == "" {
		return filepath.Clean(name), nil
	}
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("invalid target directory %q: %w", dir, err)
		}
		dir = abs
	}
	return filepath.Join(filepath.Clean(dir), name), nil
}

func generateBase(projectName string, data TemplateData) error {
	baseFiles := []string{
		"templates/base/go.mod.tpl",
		"templates/base/Makefile.tpl",
		"templates/base/Dockerfile.tpl",
		"templates/base/.dockerignore.tpl",
		"templates/base/README.md.tpl",
		"templates/base/gitignore.tpl",
		"templates/base/cmd/dev/main.go.tpl",
		"templates/base/static/css/app.css.tpl",
		"templates/base/pkg/runtime/server.go.tpl",
		"templates/base/pkg/runtime/reuseport_linux.go.tpl",
		"templates/base/pkg/runtime/reuseport_other.go.tpl",
		"templates/base/pkg/runtime/reuseport_posix.go.tpl",
		"templates/base/pkg/runtime/metrics.go.tpl",
		"templates/base/pkg/runtime/health.go.tpl",
		"templates/base/pkg/runtime/hmr_server.go.tpl",
		"templates/base/pkg/runtime/hmr/hmr.go.tpl",
		"templates/base/pkg/runtime/hmr/client.go.tpl",
		"templates/base/pkg/runtime/hmr/codec.go.tpl",
	}

	for _, file := range baseFiles {
		if err := processTemplate(projectName, file, data); err != nil {
			return err
		}
	}

	return nil
}

func generateArchitecture(projectName string, data TemplateData) error {
	archPath := fmt.Sprintf("templates/architectures/%s", data.Architecture)
	if err := walkAndProcess(projectName, archPath, data); err != nil {
		if strings.Contains(err.Error(), "file does not exist") || os.IsNotExist(err) {
			return fmt.Errorf("architecture %q is not implemented yet (implemented: flat, mvc)", data.Architecture)
		}
		return err
	}
	return nil
}

func generateDatabase(projectName string, data TemplateData) error {
	dbPath := fmt.Sprintf("templates/database/%s", data.Database)
	if err := walkAndProcess(projectName, dbPath, data); err != nil {
		if strings.Contains(err.Error(), "file does not exist") || os.IsNotExist(err) {
			return fmt.Errorf("database %q is not implemented yet (implemented: sqlite, postgres, mysql)", data.Database)
		}
		return err
	}
	return nil
}

func generateFeatures(projectName string, data TemplateData) error {
	for feature := range data.Features {
		featurePath := fmt.Sprintf("templates/features/%s", feature)
		if err := walkAndProcess(projectName, featurePath, data); err != nil {
			// Feature not found is OK (not all features have templates yet)
			if !strings.Contains(err.Error(), "file does not exist") {
				return err
			}
		}
	}
	return nil
}

func walkAndProcess(projectName, templateDir string, data TemplateData) error {
	entries, err := templatesFS.ReadDir(templateDir)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		path := filepath.Join(templateDir, entry.Name())

		if entry.IsDir() {
			if err := walkAndProcess(projectName, path, data); err != nil {
				return err
			}
		} else {
			if !strings.HasSuffix(entry.Name(), ".tpl") {
				continue
			}
			if err := processTemplate(projectName, path, data); err != nil {
				return err
			}
		}
	}

	return nil
}

func processTemplate(projectName, templatePath string, data TemplateData) error {
	// Read template content
	content, err := templatesFS.ReadFile(templatePath)
	if err != nil {
		return fmt.Errorf("failed to read template %s: %w", templatePath, err)
	}

	// Calculate output path
	parts := strings.Split(templatePath, "/")
	var relativePath string

	if len(parts) >= 3 {
		switch parts[1] {
		case "base":
			relativePath = strings.Join(parts[2:], "/")
		case "architectures", "database", "features":
			if len(parts) >= 4 {
				relativePath = strings.Join(parts[3:], "/")
			} else {
				relativePath = strings.Join(parts[2:], "/")
			}
		default:
			relativePath = strings.Join(parts[2:], "/")
		}
	}

	relativePath = strings.TrimSuffix(relativePath, ".tpl")
	targetPath := filepath.Join(projectName, relativePath)

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", filepath.Dir(targetPath), err)
	}

	// Parse and execute template
	tmpl, err := template.New(filepath.Base(templatePath)).Parse(string(content))
	if err != nil {
		return fmt.Errorf("failed to parse template %s: %w", templatePath, err)
	}

	// Create output file
	outFile, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", targetPath, err)
	}
	defer func() { _ = outFile.Close() }()

	// Execute template
	if err := tmpl.Execute(outFile, data); err != nil {
		return fmt.Errorf("failed to execute template %s: %w", templatePath, err)
	}

	fmt.Printf("  ✓ Created %s\n", relativePath)
	return nil
}
