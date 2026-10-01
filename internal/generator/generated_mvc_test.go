package generator

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

func TestGeneratedFlatApplicationsCompileWithRuntimeRoutes(t *testing.T) {
	withTempWorkingDirectory(t)
	tests := []struct {
		name     string
		features []config.Feature
	}{
		{name: "no-features"},
		{name: "health-only", features: []config.Feature{config.FeatureHealth}},
		{
			name: "health-metrics-openapi-grpc",
			features: []config.Feature{
				config.FeatureHealth,
				config.FeatureMetrics,
				config.FeatureOpenAPI,
				config.FeatureGRPC,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := config.ProjectConfig{
				Name:         "flat-" + test.name,
				Type:         config.TypeAPI,
				Architecture: config.ArchFlat,
				Database:     config.DBNone,
				Features:     test.features,
			}
			if err := Generate(cfg); err != nil {
				t.Fatalf("Generate() error = %v", err)
			}
			projectDir := filepath.Join(".", cfg.Name)
			for _, args := range [][]string{{"mod", "tidy"}, {"test", "./..."}} {
				cmd := exec.Command("go", args...)
				cmd.Dir = projectDir
				runGeneratedCommand(t, cmd)
			}
		})
	}
}

func TestGeneratedMVCCRUDFollowsRuntimeServer(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate generator test source")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "../.."))
	cliPath := filepath.Join(t.TempDir(), "fgoths")
	buildCLI := exec.Command("go", "build", "-o", cliPath, "./cmd/fgoths")
	buildCLI.Dir = repoRoot
	runGeneratedCommand(t, buildCLI)

	withTempWorkingDirectory(t)
	cfg := config.ProjectConfig{
		Name:         "mvc-e2e",
		Type:         config.TypeSSR,
		Architecture: config.ArchMVC,
		Database:     config.DBSQLite,
	}
	if err := Generate(cfg); err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	projectDir := filepath.Join(".", cfg.Name)

	generateCRUD := exec.Command(cliPath, "generate", "crud", "Task", "title:string")
	generateCRUD.Dir = projectDir
	runGeneratedCommand(t, generateCRUD)

	for _, args := range [][]string{
		{"mod", "tidy"},
		{"run", "./cmd/assetmanifest"},
		{"tool", "templ", "generate"},
	} {
		cmd := exec.Command("go", args...)
		cmd.Dir = projectDir
		runGeneratedCommand(t, cmd)
	}

	manifestPath := filepath.Join(projectDir, "internal", "assets", "manifest_gen.go")
	manifestBefore, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read generated asset manifest: %v", err)
	}
	regenerateManifest := exec.Command("go", "run", "./cmd/assetmanifest")
	regenerateManifest.Dir = projectDir
	runGeneratedCommand(t, regenerateManifest)
	manifestAfter, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read regenerated asset manifest: %v", err)
	}
	if !bytes.Equal(manifestBefore, manifestAfter) {
		t.Fatal("asset manifest output is not deterministic")
	}

	mainTest := `package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mvc-e2e/internal/assets"

	_ "modernc.org/sqlite"
)

func TestGeneratedCRUDAndAssetsUseRuntimeServer(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec("CREATE TABLE tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP)")
	if err != nil {
		t.Fatal(err)
	}

	server := newServer(":0", db)
	create := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader("{\"title\":\"generated\"}"))
	createResult := httptest.NewRecorder()
	server.Handler.ServeHTTP(createResult, create)
	if createResult.Code != http.StatusCreated {
		t.Fatalf("POST /api/tasks status = %d, body = %s", createResult.Code, createResult.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	listResult := httptest.NewRecorder()
	server.Handler.ServeHTTP(listResult, list)
	if listResult.Code != http.StatusOK || !strings.Contains(listResult.Body.String(), "\"title\":\"generated\"") {
		t.Fatalf("GET /api/tasks response = %d %s", listResult.Code, listResult.Body.String())
	}

	assetURL := assets.URL("css/app.css")
	assetRequest := httptest.NewRequest(http.MethodGet, assetURL, nil)
	assetResult := httptest.NewRecorder()
	server.Handler.ServeHTTP(assetResult, assetRequest)
	if assetResult.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d", assetURL, assetResult.Code)
	}
	if got := assetResult.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("fingerprinted asset Cache-Control = %q", got)
	}

	t.Setenv("FGOTHS_DEV", "1")
	devResult := httptest.NewRecorder()
	server.Handler.ServeHTTP(devResult, assetRequest)
	if got := devResult.Header().Get("Cache-Control"); got != "no-store, no-cache, must-revalidate" {
		t.Fatalf("development asset Cache-Control = %q", got)
	}
}
`
	if err := os.WriteFile(filepath.Join(projectDir, "main_test.go"), []byte(mainTest), 0o644); err != nil {
		t.Fatalf("write generated integration test: %v", err)
	}
	devTest := `package main

import "testing"

func TestAssetSnapshotChangesRegenerateAndPrioritizeReload(t *testing.T) {
	if got := mergeChangeSource("views/index.templ", "static/app.css", true); got != "static/app.css" {
		t.Fatalf("queued static assets must force a full reload, got %q", got)
	}
	if got := mergeChangeSource("static/app.css", "views/index.templ", true); got != "static/app.css" {
		t.Fatalf("later view changes must not suppress an asset reload, got %q", got)
	}
	if got := mergeChangeSource("", "views/index.templ", false); got != "views/index.templ" {
		t.Fatalf("first change source = %q, want the changed path", got)
	}

	prev := map[string]fileStamp{
		"views/index.templ": {modUnixNano: 1, size: 1},
	}
	curr := map[string]fileStamp{
		"views/index.templ": {modUnixNano: 2, size: 2},
		"static/app.css":    {modUnixNano: 2, size: 2},
	}
	changed, regenerate := detectSnapshotChanges(prev, curr)
	if !regenerate {
		t.Fatal("asset change must regenerate the manifest")
	}
	if len(changed) == 0 || changed[0] != "static/app.css" {
		t.Fatalf("asset change must be prioritized for a full reload, got %v", changed)
	}

	for _, path := range []string{"static/app.css", "static/app.js", "static/page.html"} {
		_, regenerate := detectSnapshotChanges(
			map[string]fileStamp{path: {modUnixNano: 1, size: 1}},
			map[string]fileStamp{},
		)
		if !regenerate {
			t.Errorf("removed asset %s must regenerate the manifest", path)
		}
	}
	if !isGenerated("internal/assets/manifest_gen.go") {
		t.Fatal("generated asset manifest must not trigger watcher restart loops")
	}
}
`
	if err := os.WriteFile(filepath.Join(projectDir, "cmd", "dev", "assets_test.go"), []byte(devTest), 0o644); err != nil {
		t.Fatalf("write generated watcher test: %v", err)
	}

	testGenerated := exec.Command("go", "test", "./...")
	testGenerated.Dir = projectDir
	runGeneratedCommand(t, testGenerated)
}

func runGeneratedCommand(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		t.Fatalf("command %v failed: %v\n%s", cmd.Args, err, output.String())
	}
}
