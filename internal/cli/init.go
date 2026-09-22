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
	"flag"
	"fmt"
	"strings"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/generator"
)

// validateArch reports whether the requested architecture name is supported and
// prints a helpful list when it is not.
func validateArch(arch string) bool {
	for _, valid := range config.ValidArchs {
		if arch == string(valid) {
			return true
		}
	}
	return false
}

// validateDB reports whether the requested database name is supported.
func validateDB(db string) bool {
	for _, valid := range config.ValidDBs {
		if db == string(valid) {
			return true
		}
	}
	return false
}

// validateType reports whether the requested project type is supported.
func validateType(t string) bool {
	for _, valid := range config.ValidTypes {
		if t == string(valid) {
			return true
		}
	}
	return false
}

func joinTypes(slice []config.ProjectType) string {
	parts := make([]string, 0, len(slice))
	for _, s := range slice {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ", ")
}

func joinArchs(slice []config.ArchPattern) string {
	parts := make([]string, 0, len(slice))
	for _, s := range slice {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ", ")
}

func joinDBs(slice []config.DatabaseType) string {
	parts := make([]string, 0, len(slice))
	for _, s := range slice {
		parts = append(parts, string(s))
	}
	return strings.Join(parts, ", ")
}

func RunInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)

	// Flags
	name := fs.String("name", "", "Project name")
	dir := fs.String("dir", "", "Target directory the project is created in (default: current directory)")
	db := fs.String("db", "", "Database override (none|sqlite); defaults to the preset's database")
	featuresStr := fs.String("features", "", "Comma-separated features added to the preset defaults")
	preset := fs.String("preset", "", "Project preset: api or webapp (required)")

	_ = fs.Parse(args)

	// Preset mode — the only mode. Two presets, everything else is opt-in.
	if *preset == "" {
		fmt.Println("❌ --preset is required: api or webapp")
		fmt.Println("   api    - flat JSON API + health + proxy (opt-ins: metrics, openapi, grpc, jwt-auth, mtls, otel, flatbuffers, sqlite)")
		fmt.Println("   webapp - MVC SSR + HTMX + SQLite + proxy (opt-ins: metrics, openapi, jwt-auth, mtls, otel, flatbuffers)")
		return fmt.Errorf("--preset is required: api or webapp")
	}
	if *name == "" {
		fmt.Println("❌ --name is required")
		return fmt.Errorf("project name is required")
	}

	p, ok := config.GetPreset(*preset)
	if !ok {
		fmt.Printf("❌ Unknown preset: %s\n", *preset)
		fmt.Println("Available presets: api, webapp")
		return fmt.Errorf("unknown preset: %s", *preset)
	}

	features := p.Features
	if *featuresStr != "" {
		extra, err := parseFeatures(*featuresStr)
		if err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			return err
		}
		features = mergeFeatures(features, extra)
	}

	cfg := config.ProjectConfig{
		Name:         *name,
		Dir:          *dir,
		Type:         p.Type,
		Architecture: p.Architecture,
		Database:     p.Database,
		Features:     features,
	}
	// Explicit --db wins over the preset default (mirrors the --features
	// merge behavior).
	if *db != "" {
		if !validateDB(*db) {
			fmt.Printf("❌ Unsupported database %q (available: %s)\n", *db, joinDBs(config.ValidDBs))
			return fmt.Errorf("unsupported database: %s", *db)
		}
		cfg.Database = config.DatabaseType(*db)
	}

	printConfig(cfg, *preset)

	// Generate project
	if err := generator.Generate(cfg); err != nil {
		fmt.Printf("❌ Error: %v\n", err)
		return err
	}

	printSuccess(cfg)
	return nil
}

func normalizeFeatureName(raw string) (config.Feature, bool) {
	normalized := strings.TrimSpace(strings.ToLower(raw))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")

	switch normalized {
	case "flatbuffers", "flat-buffers":
		return config.FeatureFlatBuffers, true
	case "htmx":
		return config.FeatureHTMX, true
	case "openapi", "swagger":
		return config.FeatureOpenAPI, true
	case "metrics", "prometheus":
		return config.FeatureMetrics, true
	case "health", "healthcheck", "health-check":
		return config.FeatureHealth, true
	case "grpc":
		return config.FeatureGRPC, true
	case "jwt-auth", "jwtauth", "jwt_auth":
		return config.FeatureJWTAuth, true
	case "mtls", "m-tls", "mutual-tls":
		return config.FeatureMTLS, true
	case "otel", "opentelemetry":
		return config.FeatureOTel, true
	case "ci-cd", "cicd", "ci", "github-actions", "gitlab-ci":
		return config.FeatureCICD, true
	default:
		return "", false
	}
}

// mergeFeatures appends extra features to a preset's defaults, skipping
// duplicates while preserving order (defaults first).
func mergeFeatures(base, extra []config.Feature) []config.Feature {
	seen := make(map[config.Feature]bool, len(base)+len(extra))
	merged := make([]config.Feature, 0, len(base)+len(extra))
	for _, f := range append(append([]config.Feature{}, base...), extra...) {
		if !seen[f] {
			seen[f] = true
			merged = append(merged, f)
		}
	}
	return merged
}

func parseFeatures(featuresStr string) ([]config.Feature, error) {
	if featuresStr == "" {
		return []config.Feature{}, nil
	}

	parts := strings.FieldsFunc(featuresStr, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	features := make([]config.Feature, 0, len(parts))
	seen := map[config.Feature]bool{}

	for _, part := range parts {
		feature, ok := normalizeFeatureName(part)
		if !ok {
			return nil, fmt.Errorf("unknown feature: %s", strings.TrimSpace(part))
		}
		if !seen[feature] {
			features = append(features, feature)
			seen[feature] = true
		}
	}

	return features, nil
}

func printConfig(cfg config.ProjectConfig, presetName string) {
	fmt.Println()
	if presetName != "" {
		fmt.Printf("📦 Using preset: %s\n", presetName)
	}
	fmt.Printf("🚀 Project: %s\n", cfg.Name)
	fmt.Printf("🏗️  Type: %s\n", cfg.Type)
	fmt.Printf("📐 Architecture: %s\n", cfg.Architecture)
	fmt.Printf("🗄️  Database: %s\n", cfg.Database)
	if len(cfg.Features) > 0 {
		fmt.Printf("✨ Features: %v\n", cfg.Features)
	}
	fmt.Println()
}

func printSuccess(cfg config.ProjectConfig) {
	fmt.Println()
	fmt.Println(bold("+========================================================+"))
	fmt.Println(bold("+") + "  " + green("Project generated successfully!") + strings.Repeat(" ", 17) + bold("+"))
	fmt.Println(bold("+========================================================+"))
	fmt.Println()
	fmt.Println(bold("  Next steps:"))
	fmt.Printf("    %s %s\n", gray("$"), cyan("cd "+cfg.Name))
	fmt.Printf("    %s %s\n", gray("$"), cyan("make run"))
	fmt.Println()
	fmt.Printf("  %s Open http://localhost:8080 in your browser\n", gray("->"))
	fmt.Println()

	fmt.Println(bold("  Available endpoints:"))
	fmt.Println("    /health           - Health checks")
	if cfg.HasFeature(config.FeatureMetrics) {
		fmt.Println("    /metrics          - Prometheus metrics (zero deps)")
	}
	if cfg.HasFeature(config.FeatureOpenAPI) {
		fmt.Println("    /docs             - API documentation")
		fmt.Println("    /openapi.yaml     - OpenAPI spec")
	}
	if cfg.HasFeature(config.FeatureGRPC) {
		fmt.Println("    :9090             - gRPC server")
	}
	fmt.Println()
	fmt.Println(bold("  Useful commands:"))
	fmt.Printf("    %s %s  %s\n", gray("$"), cyan("make build"), gray("- Build static binary"))
	fmt.Printf("    %s %s     %s\n", gray("$"), cyan("make test"), gray("- Run tests"))
	fmt.Printf("    %s %s  %s\n", gray("$"), cyan("make generate"), gray("- Regenerate code"))
	fmt.Println()
}
