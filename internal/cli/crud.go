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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
)

var (
	renderCRUDSource         = renderCRUDTemplate
	renderCRUDRegistrySource = renderCRUDRouteRegistry
)

type crudField struct {
	Name      string
	Exported  string
	GoType    string
	SQLType   string
	TestValue string
}

type crudData struct {
	ProjectName   string
	Entity        string
	EntityLower   string
	Table         string
	Fields        []crudField
	Columns       string
	Placeholders  string
	ArgumentList  string
	ScanArguments string
	Assignments   string
	IDPlaceholder string
}

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// RunGenerateCRUD creates a SQLite-backed CRUD vertical slice inside an MVC
// webapp project: a model in models/, a JSON handler in handlers/, a
// repository in internal/database/, a migration and generated tests. It
// refuses to overwrite existing code.
func RunGenerateCRUD(args []string) {
	data, err := parseCRUDArgs(args)
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		return
	}
	if err := requireMVCProject(); err != nil {
		fmt.Printf("❌ %v\n", err)
		return
	}
	projectName, err := currentModuleName()
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		return
	}
	data.ProjectName = projectName
	if err := writeCRUDFiles(data); err != nil {
		fmt.Printf("❌ Could not generate CRUD: %v\n", err)
		return
	}

	fmt.Printf("✅ CRUD %s generated (model, handler, repository, migration + tests)\n", data.Entity)
	fmt.Printf("   GET, POST /api/%s\n", data.Table)
	fmt.Printf("   GET, PUT, DELETE /api/%s/{id}\n", data.Table)
	fmt.Println("   Run `go test ./...` to validate the generated project.")
}

func parseCRUDArgs(args []string) (crudData, error) {
	if len(args) < 2 {
		return crudData{}, fmt.Errorf("usage: fgoths generate crud Entity field:type [field:type...]")
	}
	entity := args[0]
	if !regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`).MatchString(entity) {
		return crudData{}, fmt.Errorf("entity must be a PascalCase identifier")
	}

	data := crudData{Entity: entity, EntityLower: lowerFirst(entity), Table: pluralize(toSnake(entity))}
	seen := make(map[string]bool)
	for _, argument := range args[1:] {
		parts := strings.SplitN(argument, ":", 2)
		if len(parts) != 2 || !identifierPattern.MatchString(parts[0]) {
			return crudData{}, fmt.Errorf("invalid field %q; use lower_snake:type", argument)
		}
		if seen[parts[0]] {
			return crudData{}, fmt.Errorf("field %q is repeated", parts[0])
		}
		seen[parts[0]] = true

		goType, sqlType, ok := supportedCRUDType(parts[1])
		if !ok {
			return crudData{}, fmt.Errorf("unsupported type %q; use string, bool, int, int64 or float64", parts[1])
		}
		data.Fields = append(data.Fields, crudField{Name: parts[0], Exported: exportName(parts[0]), GoType: goType, SQLType: sqlType, TestValue: testValueFor(goType)})
	}

	columns := make([]string, 0, len(data.Fields))
	placeholders := make([]string, 0, len(data.Fields))
	arguments := make([]string, 0, len(data.Fields))
	assignments := make([]string, 0, len(data.Fields))
	scans := []string{"&item.ID"}
	for index, field := range data.Fields {
		columns = append(columns, field.Name)
		placeholders = append(placeholders, fmt.Sprintf("?%d", index+1))
		assignments = append(assignments, fmt.Sprintf("%s = ?%d", field.Name, index+1))
		arguments = append(arguments, "in."+field.Exported)
		scans = append(scans, "&item."+field.Exported)
	}
	data.Columns = strings.Join(columns, ", ")
	data.Placeholders = strings.Join(placeholders, ", ")
	data.ArgumentList = strings.Join(arguments, ", ")
	data.ScanArguments = strings.Join(scans, ", ")
	data.Assignments = strings.Join(assignments, ", ")
	data.IDPlaceholder = fmt.Sprintf("?%d", len(data.Fields)+1)
	return data, nil
}

func supportedCRUDType(value string) (string, string, bool) {
	switch value {
	case "string":
		return "string", "TEXT", true
	case "bool":
		return "bool", "INTEGER", true
	case "int":
		return "int", "INTEGER", true
	case "int64":
		return "int64", "INTEGER", true
	case "float64":
		return "float64", "REAL", true
	default:
		return "", "", false
	}
}

// testValueFor returns a Go literal used to populate the generated repository
// test fixture for the given field type.
func testValueFor(goType string) string {
	switch goType {
	case "string":
		return `"test"`
	case "bool":
		return "true"
	case "int", "int64":
		return "1"
	case "float64":
		return "1.5"
	default:
		return ""
	}
}

// requireMVCProject verifies the current directory is an MVC webapp with
// SQLite — the only layout `generate crud` supports.
func requireMVCProject() error {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return fmt.Errorf("go.mod not found; run this inside a project generated by `fgoths init`")
	}
	if !bytes.Contains(goMod, []byte("modernc.org/sqlite")) {
		return fmt.Errorf("CRUD generation requires SQLite; regenerate with `fgoths init --db=sqlite` or `--preset=webapp`")
	}
	if _, err := os.Stat("models"); err != nil {
		return fmt.Errorf("CRUD generation requires an MVC webapp; regenerate with `fgoths init --preset=webapp`")
	}
	return nil
}

func currentModuleName() (string, error) {
	content, err := os.ReadFile("go.mod")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", fmt.Errorf("could not determine module name from go.mod")
}

// writeCRUDFiles renders and writes the entity files plus regenerates
// handlers/routes_gen.go, the single source of truth for CRUD route
// registration (so repeated runs never patch main.go text).
func writeCRUDFiles(data crudData) error {
	handlerPath := filepath.Join("handlers", toSnake(data.Entity)+"_handler.go")
	files := map[string]string{
		filepath.Join("models", toSnake(data.Entity)+".go"):                               crudModelTemplate,
		filepath.Join("internal", "database", toSnake(data.Entity)+"_repository.go"):      crudRepositoryTemplate,
		filepath.Join("internal", "database", toSnake(data.Entity)+"_repository_test.go"): crudRepositoryTestTemplate,
		filepath.Join("migrations", "002_create_"+data.Table+".sql"):                      crudMigrationTemplate,
	}

	for path := range files {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("refusing to overwrite existing file %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := os.Stat(handlerPath); err == nil {
		return fmt.Errorf("refusing to overwrite existing file %s", handlerPath)
	}

	for _, dir := range []string{"handlers", "models", "migrations", filepath.Join("internal", "database")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	handlerTestPath := handlerPath[:len(handlerPath)-len(".go")] + "_test.go"
	for path, source := range map[string]string{handlerPath: crudHandlerTemplate, handlerTestPath: crudHandlerTestTemplate} {
		content, err := renderCRUDSource(source, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", path, err)
		}
		if err := osWriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}

	for path, source := range files {
		content, err := renderCRUDSource(source, data)
		if err != nil {
			return fmt.Errorf("render %s: %w", path, err)
		}
		if err := osWriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return writeCRUDRouteRegistry(data.ProjectName)
}

// writeCRUDRouteRegistry regenerates handlers/routes_gen.go listing every
// entity generated so far by scanning the handlers directory. Being fully
// regenerated (not patched) makes repeated `generate crud` runs idempotent.
func writeCRUDRouteRegistry(projectName string) error {
	entries, err := os.ReadDir("handlers")
	if err != nil {
		return fmt.Errorf("scan handlers: %w", err)
	}
	var entities []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_handler.go") {
			continue
		}
		entities = append(entities, exportName(strings.TrimSuffix(name, "_handler.go")))
	}
	sort.Strings(entities)

	tmplSrc := `// Code generated by fgoths generate crud; DO NOT EDIT.
package handlers

import (
	"database/sql"

	__RUNTIME_IMPORT__
)

// RegisterCRUDRoutes mounts every entity generated with ` + "`fgoths generate crud`" + `.
func RegisterCRUDRoutes(registrar runtime.Registrar, db *sql.DB) {
	if db == nil {
		return
	}
{{range .}}	register{{.}}Routes(registrar, db)
{{end}}}
`
	output, err := renderCRUDRegistrySource(tmplSrc, projectName, entities)
	if err != nil {
		return err
	}
	return osWriteFile(filepath.Join("handlers", "routes_gen.go"), output, 0o644)
}

func renderCRUDRouteRegistry(source, projectName string, entities []string) ([]byte, error) {
	source = strings.ReplaceAll(source, "__RUNTIME_IMPORT__", strconv.Quote(projectName+"/pkg/runtime"))
	var output bytes.Buffer
	tmpl, err := template.New("routes").Parse(source)
	if err != nil {
		return nil, err
	}
	if err := tmpl.Execute(&output, entities); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func renderCRUDTemplate(source string, data crudData) ([]byte, error) {
	tmpl, err := template.New("crud").Parse(source)
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, data); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func toSnake(value string) string {
	var out []rune
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				out = append(out, '_')
			}
			out = append(out, r+'a'-'A')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

func pluralize(value string) string  { return value + "s" }
func lowerFirst(value string) string { return strings.ToLower(value[:1]) + value[1:] }

func exportName(value string) string {
	parts := strings.Split(value, "_")
	var out strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		out.WriteString(strings.ToUpper(part[:1]) + part[1:])
	}
	return out.String()
}

const crudModelTemplate = `package models

import "time"

// {{.Entity}} is a persisted entity managed by the {{.Table}} CRUD slice.
type {{.Entity}} struct {
	ID uint64 ` + "`" + `json:"id"` + "`" + `
{{range .Fields}}	{{.Exported}} {{.GoType}} ` + "`" + `json:"{{.Name}}"` + "`" + `
{{end}}	CreatedAt time.Time ` + "`" + `json:"created_at"` + "`" + `
}

// {{.Entity}}Input is the client-writable subset of {{.Entity}}. The server
// owns ID and CreatedAt, so requests can never set them.
type {{.Entity}}Input struct {
{{range .Fields}}	{{.Exported}} {{.GoType}} ` + "`" + `json:"{{.Name}}"` + "`" + `
{{end}}}
`

const crudRepositoryTemplate = `package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"{{.ProjectName}}/models"
)

const {{.EntityLower}}Columns = "id, {{.Columns}}, created_at"

// {{.Entity}}Repository persists {{.EntityLower}} rows in SQLite. Lookups of
// missing rows return an error wrapping sql.ErrNoRows.
type {{.Entity}}Repository struct{ db *sql.DB }

func New{{.Entity}}Repository(db *sql.DB) {{.Entity}}Repository { return {{.Entity}}Repository{db: db} }

func (r {{.Entity}}Repository) Create(ctx context.Context, in models.{{.Entity}}Input) (models.{{.Entity}}, error) {
	row := r.db.QueryRowContext(ctx, "INSERT INTO {{.Table}} ({{.Columns}}) VALUES ({{.Placeholders}}) RETURNING "+{{.EntityLower}}Columns, {{.ArgumentList}})
	item, err := scan{{.Entity}}(row)
	if err != nil {
		return models.{{.Entity}}{}, fmt.Errorf("insert {{.EntityLower}}: %w", err)
	}
	return item, nil
}

func (r {{.Entity}}Repository) Get(ctx context.Context, id uint64) (models.{{.Entity}}, error) {
	row := r.db.QueryRowContext(ctx, "SELECT "+{{.EntityLower}}Columns+" FROM {{.Table}} WHERE id = ?1", id)
	item, err := scan{{.Entity}}(row)
	if err != nil {
		return models.{{.Entity}}{}, fmt.Errorf("get {{.EntityLower}} %d: %w", id, err)
	}
	return item, nil
}

func (r {{.Entity}}Repository) List(ctx context.Context) ([]models.{{.Entity}}, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT "+{{.EntityLower}}Columns+" FROM {{.Table}} ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("list {{.Table}}: %w", err)
	}
	defer rows.Close()
	items := make([]models.{{.Entity}}, 0)
	for rows.Next() {
		item, err := scan{{.Entity}}(rows)
		if err != nil {
			return nil, fmt.Errorf("list {{.Table}}: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r {{.Entity}}Repository) Update(ctx context.Context, id uint64, in models.{{.Entity}}Input) (models.{{.Entity}}, error) {
	row := r.db.QueryRowContext(ctx, "UPDATE {{.Table}} SET {{.Assignments}} WHERE id = {{.IDPlaceholder}} RETURNING "+{{.EntityLower}}Columns, {{.ArgumentList}}, id)
	item, err := scan{{.Entity}}(row)
	if err != nil {
		return models.{{.Entity}}{}, fmt.Errorf("update {{.EntityLower}} %d: %w", id, err)
	}
	return item, nil
}

func (r {{.Entity}}Repository) Delete(ctx context.Context, id uint64) error {
	res, err := r.db.ExecContext(ctx, "DELETE FROM {{.Table}} WHERE id = ?1", id)
	if err != nil {
		return fmt.Errorf("delete {{.EntityLower}} %d: %w", id, err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete {{.EntityLower}} %d: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("delete {{.EntityLower}} %d: %w", id, sql.ErrNoRows)
	}
	return nil
}

func scan{{.Entity}}(row interface{ Scan(...any) error }) (models.{{.Entity}}, error) {
	var item models.{{.Entity}}
	var createdAt string
	if err := row.Scan({{.ScanArguments}}, &createdAt); err != nil {
		return models.{{.Entity}}{}, err
	}
	parsed, err := parse{{.Entity}}Time(createdAt)
	if err != nil {
		return models.{{.Entity}}{}, err
	}
	item.CreatedAt = parsed
	return item, nil
}

// parse{{.Entity}}Time accepts SQLite's CURRENT_TIMESTAMP layout and RFC 3339.
func parse{{.Entity}}Time(value string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02 15:04:05", time.RFC3339Nano} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized created_at timestamp %q", value)
}
`

const crudHandlerTemplate = `package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"{{.ProjectName}}/internal/database"
	"{{.ProjectName}}/models"
	"{{.ProjectName}}/pkg/runtime"
)

// max{{.Entity}}Body caps request bodies for the {{.Table}} API.
const max{{.Entity}}Body = 1 << 20

func write{{.Entity}}JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// write{{.Entity}}Error maps repository errors to HTTP responses without
// leaking internal details to the client.
func write{{.Entity}}Error(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		write{{.Entity}}JSON(w, http.StatusNotFound, map[string]string{"error": "{{.EntityLower}} not found"})
		return
	}
	slog.ErrorContext(r.Context(), "{{.Table}} request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	write{{.Entity}}JSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
}

// decode{{.Entity}}Input reads exactly one JSON object of known fields.
func decode{{.Entity}}Input(w http.ResponseWriter, r *http.Request) (models.{{.Entity}}Input, bool) {
	var in models.{{.Entity}}Input
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, max{{.Entity}}Body))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(&in)
	if err == nil && decoder.Decode(&struct{}{}) != io.EOF {
		err = errors.New("request body must contain a single JSON object")
	}
	if err != nil {
		status := http.StatusBadRequest
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		write{{.Entity}}JSON(w, status, map[string]string{"error": err.Error()})
		return in, false
	}
	return in, true
}

func parse{{.Entity}}ID(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	id, err := strconv.ParseUint(runtime.PathValue(r, "id"), 10, 63)
	if err != nil || id == 0 {
		write{{.Entity}}JSON(w, http.StatusBadRequest, map[string]string{"error": "invalid {{.EntityLower}} id"})
		return 0, false
	}
	return id, true
}

// register{{.Entity}}Routes mounts the JSON API for {{.Table}}:
// GET/POST /api/{{.Table}} and GET/PUT/DELETE /api/{{.Table}}/{id}.
func register{{.Entity}}Routes(registrar runtime.Registrar, db *sql.DB) {
	repo := database.New{{.Entity}}Repository(db)
	registrar.Handle(http.MethodGet, "/api/{{.Table}}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items, err := repo.List(r.Context())
		if err != nil {
			write{{.Entity}}Error(w, r, err)
			return
		}
		write{{.Entity}}JSON(w, http.StatusOK, items)
	}))
	registrar.Handle(http.MethodPost, "/api/{{.Table}}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, ok := decode{{.Entity}}Input(w, r)
		if !ok {
			return
		}
		created, err := repo.Create(r.Context(), in)
		if err != nil {
			write{{.Entity}}Error(w, r, err)
			return
		}
		write{{.Entity}}JSON(w, http.StatusCreated, created)
	}))
	registrar.Handle(http.MethodGet, "/api/{{.Table}}/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parse{{.Entity}}ID(w, r)
		if !ok {
			return
		}
		item, err := repo.Get(r.Context(), id)
		if err != nil {
			write{{.Entity}}Error(w, r, err)
			return
		}
		write{{.Entity}}JSON(w, http.StatusOK, item)
	}))
	registrar.Handle(http.MethodPut, "/api/{{.Table}}/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parse{{.Entity}}ID(w, r)
		if !ok {
			return
		}
		in, ok := decode{{.Entity}}Input(w, r)
		if !ok {
			return
		}
		updated, err := repo.Update(r.Context(), id, in)
		if err != nil {
			write{{.Entity}}Error(w, r, err)
			return
		}
		write{{.Entity}}JSON(w, http.StatusOK, updated)
	}))
	registrar.Handle(http.MethodDelete, "/api/{{.Table}}/{id}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := parse{{.Entity}}ID(w, r)
		if !ok {
			return
		}
		if err := repo.Delete(r.Context(), id); err != nil {
			write{{.Entity}}Error(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
`

const crudMigrationTemplate = `CREATE TABLE IF NOT EXISTS {{.Table}} (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
{{range .Fields}}    {{.Name}} {{.SQLType}} NOT NULL,
{{end}}    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`

const crudRepositoryTestTemplate = `package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"{{.ProjectName}}/models"

	_ "modernc.org/sqlite"
)

func open{{.Entity}}TestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(` + "`CREATE TABLE {{.Table}} (\n\t\tid INTEGER PRIMARY KEY AUTOINCREMENT,\n{{range .Fields}}\t\t{{.Name}} {{.SQLType}} NOT NULL,\n{{end}}\t\tcreated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP\n\t)`" + `); err != nil {
		t.Fatalf("create {{.Table}} table: %v", err)
	}
	return db
}

func new{{.Entity}}TestInput() models.{{.Entity}}Input {
	return models.{{.Entity}}Input{
{{range .Fields}}		{{.Exported}}: {{.TestValue}},
{{end}}	}
}

func TestNew{{.Entity}}RepositoryCreateAndList(t *testing.T) {
	db := open{{.Entity}}TestDB(t)
	repo := New{{.Entity}}Repository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, new{{.Entity}}TestInput())
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("expected Create to assign a non-zero ID")
	}
	if created.CreatedAt.IsZero() {
		t.Fatal("expected Create to return the stored created_at")
	}

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 {{.EntityLower}}, got %d", len(items))
	}
	if items[0].ID != created.ID || !items[0].CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("listed {{.EntityLower}} = %+v, want %+v", items[0], created)
	}
}

func TestNew{{.Entity}}RepositoryGetUpdateDelete(t *testing.T) {
	db := open{{.Entity}}TestDB(t)
	repo := New{{.Entity}}Repository(db)
	ctx := context.Background()

	created, err := repo.Create(ctx, new{{.Entity}}TestInput())
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	got, err := repo.Get(ctx, created.ID)
	if err != nil || got.ID != created.ID {
		t.Fatalf("Get = %+v, %v; want ID %d", got, err, created.ID)
	}
	updated, err := repo.Update(ctx, created.ID, new{{.Entity}}TestInput())
	if err != nil || updated.ID != created.ID {
		t.Fatalf("Update = %+v, %v; want ID %d", updated, err, created.ID)
	}
	if err := repo.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := repo.Get(ctx, created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Get after Delete error = %v, want sql.ErrNoRows", err)
	}
	if _, err := repo.Update(ctx, created.ID, new{{.Entity}}TestInput()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Update of a missing row error = %v, want sql.ErrNoRows", err)
	}
	if err := repo.Delete(ctx, created.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Delete of a missing row error = %v, want sql.ErrNoRows", err)
	}
}
`

const crudHandlerTestTemplate = `package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"{{.ProjectName}}/pkg/runtime"

	_ "modernc.org/sqlite"
)

func open{{.Entity}}HandlerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(` + "`CREATE TABLE {{.Table}} (\n\t\tid INTEGER PRIMARY KEY AUTOINCREMENT,\n{{range .Fields}}\t\t{{.Name}} {{.SQLType}} NOT NULL,\n{{end}}\t\tcreated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP\n\t)`" + `); err != nil {
		t.Fatalf("create {{.Table}} table: %v", err)
	}
	return db
}

func new{{.Entity}}RouteTestServer(t *testing.T) *runtime.Server {
	t.Helper()
	server := runtime.NewServer("")
	register{{.Entity}}Routes(server, open{{.Entity}}HandlerTestDB(t))
	return server
}

func serve{{.Entity}}(server *runtime.Server, method, target string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	res := httptest.NewRecorder()
	server.Handler.ServeHTTP(res, req)
	return res
}

func {{.EntityLower}}TestBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
{{range .Fields}}		"{{.Name}}": {{.TestValue}},
{{end}}	})
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return body
}

func TestRegister{{.Entity}}RoutesCreateAndList(t *testing.T) {
	server := new{{.Entity}}RouteTestServer(t)

	postRes := serve{{.Entity}}(server, http.MethodPost, "/api/{{.Table}}", {{.EntityLower}}TestBody(t))
	if postRes.Code != http.StatusCreated {
		t.Fatalf("POST /api/{{.Table}} = %d, want %d; body=%s", postRes.Code, http.StatusCreated, postRes.Body.String())
	}

	getRes := serve{{.Entity}}(server, http.MethodGet, "/api/{{.Table}}", nil)
	if getRes.Code != http.StatusOK {
		t.Fatalf("GET /api/{{.Table}} = %d, want %d; body=%s", getRes.Code, http.StatusOK, getRes.Body.String())
	}

	var items []map[string]any
	if err := json.Unmarshal(getRes.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 {{.EntityLower}}, got %d", len(items))
	}
}

func TestRegister{{.Entity}}RoutesItemLifecycle(t *testing.T) {
	server := new{{.Entity}}RouteTestServer(t)

	postRes := serve{{.Entity}}(server, http.MethodPost, "/api/{{.Table}}", {{.EntityLower}}TestBody(t))
	var created struct {
		ID        uint64 ` + "`" + `json:"id"` + "`" + `
		CreatedAt string ` + "`" + `json:"created_at"` + "`" + `
	}
	if err := json.Unmarshal(postRes.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatalf("POST response = %s (%v), want a created {{.EntityLower}}", postRes.Body.String(), err)
	}
	if strings.HasPrefix(created.CreatedAt, "0001-") {
		t.Fatalf("POST created_at = %q, want the stored timestamp", created.CreatedAt)
	}
	itemPath := fmt.Sprintf("/api/{{.Table}}/%d", created.ID)

	for _, step := range []struct {
		method, target string
		body           []byte
		want           int
	}{
		{http.MethodGet, itemPath, nil, http.StatusOK},
		{http.MethodPut, itemPath, {{.EntityLower}}TestBody(t), http.StatusOK},
		{http.MethodDelete, itemPath, nil, http.StatusNoContent},
		{http.MethodGet, itemPath, nil, http.StatusNotFound},
		{http.MethodPut, itemPath, {{.EntityLower}}TestBody(t), http.StatusNotFound},
		{http.MethodDelete, itemPath, nil, http.StatusNotFound},
		{http.MethodGet, "/api/{{.Table}}/not-a-number", nil, http.StatusBadRequest},
		{http.MethodPost, "/api/{{.Table}}", []byte(` + "`" + `{"id":99}` + "`" + `), http.StatusBadRequest},
		{http.MethodPost, "/api/{{.Table}}", []byte(` + "`" + `{} {}` + "`" + `), http.StatusBadRequest},
	} {
		res := serve{{.Entity}}(server, step.method, step.target, step.body)
		if res.Code != step.want {
			t.Fatalf("%s %s = %d, want %d; body=%s", step.method, step.target, res.Code, step.want, res.Body.String())
		}
	}
}
`
