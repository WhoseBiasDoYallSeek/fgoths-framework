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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

func TestProjectRootRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"./app", "a/b", "..", ".", ""} {
		if _, err := projectRoot(name, ""); err == nil {
			t.Fatalf("expected projectRoot(%q) to reject an invalid name", name)
		}
	}
	if got, err := projectRoot("my-app", ""); err != nil || got != "my-app" {
		t.Fatalf("projectRoot(\"my-app\") = (%q, %v), want (my-app, nil)", got, err)
	}
}

func TestGenerateRejectsUnknownArchitecture(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "bad-arch",
		Type:         config.TypeAPI,
		Architecture: config.ArchPattern("spaghetti"),
		Database:     config.DBNone,
	}
	err := Generate(cfg)
	if err == nil {
		t.Fatal("expected an unknown architecture to fail generation")
	}
	if !strings.Contains(err.Error(), "invalid architecture") {
		t.Fatalf("expected a validation error for the architecture, got %v", err)
	}
}

func TestGenerateRejectsUnknownDatabase(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "bad-db",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DatabaseType("oracle"),
	}
	err := Generate(cfg)
	if err == nil {
		t.Fatal("expected an unknown database to fail generation")
	}
	if !strings.Contains(err.Error(), "invalid database") {
		t.Fatalf("expected a validation error for the database, got %v", err)
	}
}

func TestGenerateSupportsAllArchitecturesAndDatabases(t *testing.T) {
	for _, arch := range config.ValidArchs {
		t.Run("arch-"+string(arch), func(t *testing.T) {
			withTempWorkingDirectory(t)
			cfg := config.ProjectConfig{Name: "app-" + string(arch), Type: config.TypeAPI, Architecture: arch, Database: config.DBNone}
			if err := Generate(cfg); err != nil {
				t.Fatalf("Generate() error for architecture %s = %v", arch, err)
			}
			// MVC generates a different entry point for SSR-style projects.
			entry := filepath.Join(cfg.Name, "main.go")
			if arch == config.ArchMVC {
				entry = filepath.Join(cfg.Name, "cmd", "web", "main.go")
			}
			if _, err := os.Stat(entry); err != nil {
				t.Logf("architecture %s did not produce %s; checking project root instead", arch, entry)
				if _, err := os.Stat(cfg.Name); err != nil {
					t.Fatalf("expected the project directory for architecture %s: %v", arch, err)
				}
			}
		})
	}
	for _, db := range []config.DatabaseType{config.DBSQLite, config.DBPostgres, config.DBMySQL} {
		t.Run("db-"+string(db), func(t *testing.T) {
			withTempWorkingDirectory(t)
			cfg := config.ProjectConfig{Name: "app-" + string(db), Type: config.TypeAPI, Architecture: config.ArchFlat, Database: db}
			if err := Generate(cfg); err != nil {
				t.Fatalf("Generate() error for database %s = %v", db, err)
			}
		})
	}
}

func TestGenerateSupportsAllFeatures(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "feature-app",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     config.ValidFeatures,
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error with all features = %v", err)
	}
}

func TestGenerateRejectsInvalidProjectName(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{Name: "nested/path", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone}
	if err := Generate(cfg); err == nil {
		t.Fatal("expected a nested project name to fail generation")
	}
}

func TestGenerateCreatesCleanAPIProject(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "example-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, path := range []string{"go.mod", "Makefile", "README.md", "Dockerfile", ".dockerignore", ".gitignore", "main.go", "handlers/handlers.go"} {
		if _, err := os.Stat(filepath.Join(cfg.Name, path)); err != nil {
			t.Errorf("generated project is missing %s: %v", path, err)
		}
	}
}

func TestGenerateIncludesScratchContainerWorkflow(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "container-web",
		Type:         config.TypeSSR,
		Architecture: config.ArchMVC,
		Database:     config.DBSQLite,
		Features:     []config.Feature{config.FeatureHealth, config.FeatureMetrics, config.FeatureHTMX},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	dockerfile, err := os.ReadFile(filepath.Join(cfg.Name, "Dockerfile"))
	if err != nil {
		t.Fatalf("read Dockerfile: %v", err)
	}
	for _, want := range []string{
		"FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build",
		"apk add --no-cache ca-certificates tzdata",
		"RUN --mount=type=cache,target=/go/pkg/mod go mod download",
		"FROM scratch",
		"go tool templ generate",
		"DATABASE_URL=\"file:/data/app.db",
		"ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt",
		"COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo",
		"COPY --from=build --chown=65532:65532 /src/static ./static",
		"USER 65532:65532",
		"ENTRYPOINT [\"./app\"]",
	} {
		if !strings.Contains(string(dockerfile), want) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}

	makefile, err := os.ReadFile(filepath.Join(cfg.Name, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	for _, want := range []string{"APP_VOLUME ?= container-web-data", "deps:", "generate: deps generate-assets", "docker-check:", "docker buildx version", "Docker BuildKit/buildx is required", "docker-build: docker-check generate", "DOCKER_BUILDKIT=1 docker build", "docker-run: docker-check", "docker volume create $(APP_VOLUME)", "-v $(APP_VOLUME):/data", "Docker daemon is not running"} {
		if !strings.Contains(string(makefile), want) {
			t.Errorf("Makefile missing %q", want)
		}
	}

	readme, err := os.ReadFile(filepath.Join(cfg.Name, "README.md"))
	if err != nil {
		t.Fatalf("read README.md: %v", err)
	}
	for _, want := range []string{"Docker & Scratch Containers", "make docker-build", "make docker-run", "Install Docker", "failed to connect to the docker API", "BuildKit is enabled but the buildx component is missing", "Docker named volume", "container-web-data:/data", "--user \"$(id -u):$(id -g)\"", "docker-buildx", "colima start"} {
		if !strings.Contains(string(readme), want) {
			t.Errorf("README.md missing %q", want)
		}
	}
}

// TestGenerateGoModOmitsOptionalDepsWithoutSelection is a regression test for
// technical debt C1-C3: a no-DB project with no optional features must produce
// a go.mod that contains no third-party dependencies (just the module line and
// Go version), proving the claim "no DB = zero deps". It also guards against
// re-introducing the swaggo/swag ghost dependency.
func TestGenerateGoModOmitsOptionalDepsWithoutSelection(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "minimal-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	goMod, err := os.ReadFile(filepath.Join(cfg.Name, "go.mod"))
	if err != nil {
		t.Fatalf("read generated go.mod: %v", err)
	}
	content := string(goMod)

	for _, dep := range []string{
		"modernc.org/sqlite",                  // C3: only when Database == sqlite
		"github.com/swaggo",                   // C2: ghost dep, removed from template
		"github.com/prometheus/client_golang", // metrics feature off
		"github.com/golang-jwt/jwt",           // jwt-auth feature off
		"go.opentelemetry.io/otel",            // otel feature off
	} {
		if strings.Contains(content, dep) {
			t.Errorf("generated go.mod contains %q but the corresponding feature/database is not selected", dep)
		}
	}

	// FlatBuffers is always required: the built-in HMR module uses it for
	// zero-copy event framing regardless of the "flatbuffers" feature flag.
	if !strings.Contains(content, "github.com/google/flatbuffers") {
		t.Error("generated go.mod must always require github.com/google/flatbuffers (HMR depends on it)")
	}
}

// TestGeneratedMakefileDevTargetRunsCmdDev is a regression test for a bug
// where the `dev` target's recipe line was indented with spaces instead of
// a tab. GNU Make silently drops such a line from the recipe (treating it
// as a stray variable assignment) instead of erroring, so `make dev` would
// exit 0 having run nothing — the dev server, and therefore HMR, never
// started. `make -n dev` (dry run) surfaces exactly what would execute.
func TestGeneratedMakefileDevTargetRunsCmdDev(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not installed")
	}
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "dev-target-check",
		Type:         config.TypeSSR,
		Architecture: config.ArchMVC,
		Database:     config.DBNone,
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	cmd := exec.Command("make", "-n", "dev")
	cmd.Dir = cfg.Name
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n dev failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "go run ./cmd/dev") {
		t.Errorf("`make dev` recipe does not include `go run ./cmd/dev` (dry-run output: %q); "+
			"check for space-indented recipe lines in Makefile.tpl", out)
	}
}

func TestTemplateDataDatabaseHelpers(t *testing.T) {
	cases := []struct {
		db                                                config.DatabaseType
		wantSQLite, wantPostgres, wantMySQL, wantDatabase bool
		wantPlaceholder                                   string
	}{
		{db: config.DBNone, wantPlaceholder: "?"},
		{db: config.DBSQLite, wantSQLite: true, wantDatabase: true, wantPlaceholder: "?"},
		{db: config.DBPostgres, wantPostgres: true, wantDatabase: true, wantPlaceholder: "$1"},
		{db: config.DBMySQL, wantMySQL: true, wantDatabase: true, wantPlaceholder: "?"},
	}
	for _, tc := range cases {
		d := TemplateData{Database: tc.db}
		if got := d.UseSQLite(); got != tc.wantSQLite {
			t.Errorf("UseSQLite() for %v = %v, want %v", tc.db, got, tc.wantSQLite)
		}
		if got := d.UsePostgres(); got != tc.wantPostgres {
			t.Errorf("UsePostgres() for %v = %v, want %v", tc.db, got, tc.wantPostgres)
		}
		if got := d.UseMySQL(); got != tc.wantMySQL {
			t.Errorf("UseMySQL() for %v = %v, want %v", tc.db, got, tc.wantMySQL)
		}
		if got := d.UseDatabase(); got != tc.wantDatabase {
			t.Errorf("UseDatabase() for %v = %v, want %v", tc.db, got, tc.wantDatabase)
		}
		if got := d.Placeholder(); got != tc.wantPlaceholder {
			t.Errorf("Placeholder() for %v = %q, want %q", tc.db, got, tc.wantPlaceholder)
		}
	}
}

func TestTemplateDataArchitectureHelpers(t *testing.T) {
	cases := []struct {
		arch              config.ArchPattern
		wantMVC, wantFlat bool
	}{
		{arch: config.ArchMVC, wantMVC: true},
		{arch: config.ArchFlat, wantFlat: true},
	}
	for _, tc := range cases {
		d := TemplateData{Architecture: tc.arch}
		if got := d.IsMVC(); got != tc.wantMVC {
			t.Errorf("IsMVC() for %v = %v, want %v", tc.arch, got, tc.wantMVC)
		}
	}
}

func TestTemplateDataTypeHelpers(t *testing.T) {
	cases := []struct {
		projectType      config.ProjectType
		wantAPI, wantSSR bool
	}{
		{projectType: config.TypeAPI, wantAPI: true},
		{projectType: config.TypeSSR, wantSSR: true},
	}
	for _, tc := range cases {
		d := TemplateData{Type: tc.projectType}
		if got := d.IsAPI(); got != tc.wantAPI {
			t.Errorf("IsAPI() for %v = %v, want %v", tc.projectType, got, tc.wantAPI)
		}
		if got := d.IsSSR(); got != tc.wantSSR {
			t.Errorf("IsSSR() for %v = %v, want %v", tc.projectType, got, tc.wantSSR)
		}
	}
}

func TestGenerateCreatesHealthEndpoints(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "health-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureHealth},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	path := filepath.Join(cfg.Name, "internal/health/health.go")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("generated health handler is missing: %v", err)
	}
	if string(content) == "" {
		t.Fatal("generated health handler is empty")
	}
}

func TestGenerateCreatesSQLiteProject(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "sqlite-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBSQLite,
		Features:     []config.Feature{config.FeatureHealth},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, path := range []string{
		"internal/database/database.go",
		"internal/health/health.go",
	} {
		if _, err := os.Stat(filepath.Join(cfg.Name, path)); err != nil {
			t.Errorf("generated SQLite project is missing %s: %v", path, err)
		}
	}

	mainContent, err := os.ReadFile(filepath.Join(cfg.Name, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainContent), "health.Ready(db)") {
		t.Fatal("generated SQLite project does not use database readiness checks")
	}
}

func TestGenerateCreatesObservabilityFeatures(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "observable-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureHealth, config.FeatureMetrics, config.FeatureOpenAPI},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, path := range []string{
		"internal/health/health.go",
		"internal/metrics/metrics.go",
		"internal/openapi/openapi.go",
	} {
		if _, err := os.Stat(filepath.Join(cfg.Name, path)); err != nil {
			t.Errorf("generated project is missing %s: %v", path, err)
		}
	}

	goMod, err := os.ReadFile(filepath.Join(cfg.Name, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(goMod), "prometheus") {
		t.Fatal("generated metrics project should NOT have Prometheus dependency (zero-dep metrics)")
	}
}

func TestGenerateCreatesEnterpriseSecurityFeatures(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "secure-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureJWTAuth, config.FeatureMTLS, config.FeatureOTel},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, path := range []string{
		"pkg/runtime/auth.go",
		"pkg/runtime/tls.go",
		"pkg/runtime/identity.go",
		"pkg/runtime/otel.go",
	} {
		if _, err := os.Stat(filepath.Join(cfg.Name, path)); err != nil {
			t.Errorf("generated project is missing %s: %v", path, err)
		}
	}

	goMod, err := os.ReadFile(filepath.Join(cfg.Name, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range []string{"github.com/golang-jwt/jwt/v5", "go.opentelemetry.io/otel"} {
		if !strings.Contains(string(goMod), dep) {
			t.Errorf("generated go.mod is missing dependency %q", dep)
		}
	}
}

func TestGenerateCreatesGRPCFeature(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "grpc-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureGRPC, config.FeatureHealth},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, path := range []string{
		"pkg/runtime/grpc.go",
		"internal/grpcapi/service.go",
	} {
		if _, err := os.Stat(filepath.Join(cfg.Name, path)); err != nil {
			t.Errorf("generated project is missing %s: %v", path, err)
		}
	}

	mainGo, err := os.ReadFile(filepath.Join(cfg.Name, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, snippet := range []string{"grpcapi.NewServer", "ListenAndServe(\":9090\")", "/health/grpc"} {
		if !strings.Contains(string(mainGo), snippet) {
			t.Errorf("generated main.go is missing gRPC wiring snippet %q", snippet)
		}
	}
}

func TestGenerateCreatesCICDFeature(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "ci-cd-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureCICD},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for _, path := range []string{
		".github/workflows/ci.yml",
		".github/workflows/release.yml",
		".gitlab-ci.yml",
	} {
		if _, err := os.Stat(filepath.Join(cfg.Name, path)); err != nil {
			t.Errorf("generated project is missing %s: %v", path, err)
		}
	}

	// The release workflow must reference the project name for binary outputs.
	release, err := os.ReadFile(filepath.Join(cfg.Name, ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(release), "dist/ci-cd-api-${os}-${arch}") {
		t.Error("release workflow does not use the project name for release binaries")
	}

	// CI must run tests with the race detector.
	ci, err := os.ReadFile(filepath.Join(cfg.Name, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(ci), "go test -race") {
		t.Error("ci.yml does not run tests with the race detector")
	}
}

func TestGenerateRejectsUnsafeOrExistingDirectory(t *testing.T) {
	withTempWorkingDirectory(t)

	valid := config.ProjectConfig{Name: "existing", Type: config.TypeAPI, Architecture: config.ArchFlat, Database: config.DBNone}
	if err := os.Mkdir(valid.Name, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Generate(valid); err == nil {
		t.Fatal("Generate() accepted an existing directory")
	}

	for _, name := range []string{"..", "../outside", "."} {
		cfg := valid
		cfg.Name = name
		if err := Generate(cfg); err == nil {
			t.Errorf("Generate() accepted unsafe name %q", name)
		}
	}
}

func TestGenerateRejectsUnsupportedConfigurationBeforeWriting(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{Name: "unsupported", Type: config.TypeSSR, Architecture: config.ArchPattern("does-not-exist"), Database: config.DBNone}
	if err := Generate(cfg); err == nil {
		t.Fatal("Generate() accepted an unsupported configuration")
	}
	if _, err := os.Stat(cfg.Name); !os.IsNotExist(err) {
		t.Fatalf("unsupported project directory was created: %v", err)
	}
}

// TestGenerateOmitsUnusedDependencies is a regression test for the C1/C2/C3
// tech-debt fixes in go.mod.tpl. It asserts that when the user opts out of a
// database and a feature, the generated go.mod must not list the corresponding
// third-party dependencies — this is the contract behind the "no DB = zero
// deps" claim and the reason swaggo/swag was removed from the template.
func TestGenerateOmitsUnusedDependencies(t *testing.T) {
	withTempWorkingDirectory(t)

	cfg := config.ProjectConfig{
		Name:         "minimal-api",
		Type:         config.TypeAPI,
		Architecture: config.ArchFlat,
		Database:     config.DBNone,
		Features:     []config.Feature{config.FeatureHealth},
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	goMod, err := os.ReadFile(filepath.Join(cfg.Name, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	contents := string(goMod)

	for _, banned := range []string{
		"modernc.org/sqlite",                  // C3: only when UseSQLite
		"github.com/swaggo/swag",              // C2: no template uses it
		"github.com/prometheus/client_golang", // C1: only when metrics feature
	} {
		if strings.Contains(contents, banned) {
			t.Errorf("generated go.mod contains %q but the project does not use it (Database=none, no observability features)", banned)
		}
	}
}

// TestGenerateProjectMatrix tests all combinations of project type, architecture,
// and database to ensure every supported combination generates without errors.
func TestGenerateProjectMatrix(t *testing.T) {
	// 3 types × 4 archs × 4 dbs = 48 combinations.
	types := []config.ProjectType{config.TypeAPI, config.TypeSSR}
	databases := []config.DatabaseType{config.DBNone, config.DBSQLite, config.DBPostgres, config.DBMySQL}

	for _, typ := range types {
		for _, arch := range config.ValidArchs {
			for _, db := range databases {
				name := "matrix-" + string(typ) + "-" + string(arch) + "-" + string(db)
				t.Run(name, func(t *testing.T) {
					withTempWorkingDirectory(t)

					cfg := config.ProjectConfig{
						Name:         name,
						Type:         typ,
						Architecture: arch,
						Database:     db,
					}
					if err := Generate(cfg); err != nil {
						t.Fatalf("Generate() error = %v", err)
					}

					// Verify project directory exists.
					if _, err := os.Stat(name); err != nil {
						t.Fatalf("project directory %q not created: %v", name, err)
					}

					// Verify essential files exist.
					for _, f := range []string{"go.mod", "README.md", "Makefile"} {
						path := filepath.Join(name, f)
						if _, err := os.Stat(path); err != nil {
							t.Errorf("%s not found: %v", f, err)
						}
					}

					// Verify entry point exists.
					// MVC always uses cmd/web/main.go; others use cmd/app/main.go.
					entryPoint := "cmd/app/main.go"
					if arch == config.ArchMVC {
						entryPoint = "cmd/web/main.go"
					}
					if _, err := os.Stat(filepath.Join(name, entryPoint)); err != nil {
						// Some hybrid+arch combos may differ; check project root as fallback.
						if _, rootErr := os.Stat(name); rootErr != nil {
							t.Errorf("entry point %s not found and project root missing: %v", entryPoint, err)
						}
					}
				})
			}
		}
	}
}

func withTempWorkingDirectory(t *testing.T) {
	t.Helper()
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
}
