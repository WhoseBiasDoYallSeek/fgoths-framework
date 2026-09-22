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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCRUDArgsValid(t *testing.T) {
	data, err := parseCRUDArgs([]string{"User", "name:string", "email:string", "active:bool"})
	if err != nil {
		t.Fatalf("parseCRUDArgs failed: %v", err)
	}
	if data.Entity != "User" || data.EntityLower != "user" || data.Table != "users" {
		t.Fatalf("unexpected derived names: %+v", data)
	}
	if data.Columns != "name, email, active" {
		t.Fatalf("unexpected columns: %q", data.Columns)
	}
	if data.Placeholders != "?1, ?2, ?3" {
		t.Fatalf("unexpected placeholders: %q", data.Placeholders)
	}
	if data.ArgumentList != "item.Name, item.Email, item.Active" {
		t.Fatalf("unexpected argument list: %q", data.ArgumentList)
	}
	if data.ScanArguments != "&item.ID, &item.Name, &item.Email, &item.Active" {
		t.Fatalf("unexpected scan arguments: %q", data.ScanArguments)
	}
	wantValues := map[string]string{"name": `"test"`, "email": `"test"`, "active": "true"}
	for _, field := range data.Fields {
		if field.TestValue != wantValues[field.Name] {
			t.Fatalf("field %q TestValue = %q, want %q", field.Name, field.TestValue, wantValues[field.Name])
		}
	}
}

func TestParseCRUDArgsRequiresEntityAndField(t *testing.T) {
	if _, err := parseCRUDArgs([]string{"User"}); err == nil {
		t.Fatal("expected error when no fields are given")
	}
	if _, err := parseCRUDArgs(nil); err == nil {
		t.Fatal("expected error when no arguments are given")
	}
}

func TestParseCRUDArgsRejectsLowerCaseEntity(t *testing.T) {
	if _, err := parseCRUDArgs([]string{"user", "name:string"}); err == nil {
		t.Fatal("expected error for a non-PascalCase entity name")
	}
}

func TestParseCRUDArgsRejectsInvalidFieldSyntax(t *testing.T) {
	if _, err := parseCRUDArgs([]string{"User", "Name-string"}); err == nil {
		t.Fatal("expected error for a field without a type separator")
	}
	if _, err := parseCRUDArgs([]string{"User", "1name:string"}); err == nil {
		t.Fatal("expected error for a field name starting with a digit")
	}
}

func TestParseCRUDArgsRejectsDuplicateField(t *testing.T) {
	if _, err := parseCRUDArgs([]string{"User", "name:string", "name:string"}); err == nil {
		t.Fatal("expected error for a repeated field")
	}
}

func TestParseCRUDArgsRejectsUnsupportedType(t *testing.T) {
	if _, err := parseCRUDArgs([]string{"User", "name:uuid"}); err == nil {
		t.Fatal("expected error for an unsupported field type")
	}
}

func TestSupportedCRUDType(t *testing.T) {
	cases := map[string][2]string{
		"string":  {"string", "TEXT"},
		"bool":    {"bool", "INTEGER"},
		"int":     {"int", "INTEGER"},
		"int64":   {"int64", "INTEGER"},
		"float64": {"float64", "REAL"},
	}
	for input, want := range cases {
		goType, sqlType, ok := supportedCRUDType(input)
		if !ok || goType != want[0] || sqlType != want[1] {
			t.Fatalf("supportedCRUDType(%q) = (%q, %q, %v), want (%q, %q, true)", input, goType, sqlType, ok, want[0], want[1])
		}
	}
	if _, _, ok := supportedCRUDType("uuid"); ok {
		t.Fatal("expected uuid to be unsupported")
	}
}

func TestToSnakeAndCasingHelpers(t *testing.T) {
	if got := toSnake("UserProfile"); got != "user_profile" {
		t.Fatalf("toSnake() = %q, want %q", got, "user_profile")
	}
	if got := pluralize("user"); got != "users" {
		t.Fatalf("pluralize() = %q, want %q", got, "users")
	}
	if got := lowerFirst("User"); got != "user" {
		t.Fatalf("lowerFirst() = %q, want %q", got, "user")
	}
	if got := exportName("first_name"); got != "FirstName" {
		t.Fatalf("exportName() = %q, want %q", got, "FirstName")
	}
}

func TestCurrentModuleName(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\ngo 1.23\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		name, err := currentModuleName()
		if err != nil {
			t.Fatalf("currentModuleName failed: %v", err)
		}
		if name != "example.com/my-app" {
			t.Fatalf("currentModuleName() = %q, want %q", name, "example.com/my-app")
		}
	})
}

func TestCurrentModuleNameMissingGoMod(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if _, err := currentModuleName(); err == nil {
			t.Fatal("expected error when go.mod is missing")
		}
	})
}

func TestRequireSQLiteProject(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		if err := requireMVCProject(); err == nil {
			t.Fatal("expected error when go.mod is missing")
		}

		if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\nrequire github.com/lib/pq v1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		if err := requireMVCProject(); err == nil {
			t.Fatal("expected error when project does not use modernc.org/sqlite")
		}

		if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\nrequire modernc.org/sqlite v1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write go.mod: %v", err)
		}
		if err := requireMVCProject(); err == nil {
			t.Fatal("expected error when the MVC layout (models/) is missing")
		}

		if err := os.MkdirAll("models", 0o755); err != nil {
			t.Fatalf("mkdir models dir: %v", err)
		}
		if err := requireMVCProject(); err != nil {
			t.Fatalf("expected a valid sqlite project to pass, got: %v", err)
		}
	})
}

func TestWriteCRUDFilesRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		for _, sub := range []string{
			"models",
			"handlers",
			filepath.Join("internal", "database"),
			"migrations",
		} {
			if err := os.MkdirAll(sub, 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", sub, err)
			}
		}

		data, err := parseCRUDArgs([]string{"User", "name:string"})
		if err != nil {
			t.Fatalf("parseCRUDArgs failed: %v", err)
		}
		data.ProjectName = "example.com/my-app"

		if err := writeCRUDFiles(data); err != nil {
			t.Fatalf("writeCRUDFiles failed on first run: %v", err)
		}
		if _, err := os.Stat(filepath.Join("models", "user.go")); err != nil {
			t.Fatalf("expected domain file to be written: %v", err)
		}

		if err := writeCRUDFiles(data); err == nil {
			t.Fatal("expected writeCRUDFiles to refuse overwriting existing files")
		}
	})
}

func setupSQLiteMVCProject(t *testing.T) {
	t.Helper()
	if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\nrequire modernc.org/sqlite v1.0.0\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	for _, sub := range []string{
		"models",
		"handlers",
		filepath.Join("internal", "database"),
		"migrations",
	} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
}

func TestRunGenerateCRUDGeneratesFiles(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		setupSQLiteMVCProject(t)

		output := captureStdout(t, func() {
			RunGenerateCRUD([]string{"User", "name:string", "active:bool"})
		})
		if !strings.Contains(output, "CRUD User generated") {
			t.Fatalf("expected success message, got %q", output)
		}
		if _, err := os.Stat(filepath.Join("models", "user.go")); err != nil {
			t.Fatalf("expected domain file to be written: %v", err)
		}
		if _, err := os.Stat(filepath.Join("migrations", "002_create_users.sql")); err != nil {
			t.Fatalf("expected migration file to be written: %v", err)
		}
		testFile := filepath.Join("internal", "database", "user_repository_test.go")
		content, err := os.ReadFile(testFile)
		if err != nil {
			t.Fatalf("expected repository test file to be written: %v", err)
		}
		if !strings.Contains(string(content), "TestNewUserRepositoryCreateAndList") {
			t.Fatalf("expected generated test function, got %q", content)
		}
		handlerTestFile := filepath.Join("handlers", "user_handler_test.go")
		handlerContent, err := os.ReadFile(handlerTestFile)
		if err != nil {
			t.Fatalf("expected handler test file to be written: %v", err)
		}
		if !strings.Contains(string(handlerContent), "TestRegisterUserRoutesCreateAndList") {
			t.Fatalf("expected generated handler test function, got %q", handlerContent)
		}
	})
}

func TestRunGenerateCRUDRejectsInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			RunGenerateCRUD([]string{"User"})
		})
		if !strings.Contains(output, "usage:") {
			t.Fatalf("expected usage error, got %q", output)
		}
	})
}

func TestRunGenerateCRUDRejectsNonSQLiteProject(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			RunGenerateCRUD([]string{"User", "name:string"})
		})
		if !strings.Contains(output, "go.mod not found") {
			t.Fatalf("expected go.mod error, got %q", output)
		}
	})
}

func TestRunGenerateCRUDRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		setupSQLiteMVCProject(t)
		captureStdout(t, func() {
			RunGenerateCRUD([]string{"User", "name:string"})
		})
		output := captureStdout(t, func() {
			RunGenerateCRUD([]string{"User", "name:string"})
		})
		if !strings.Contains(output, "Could not generate CRUD") {
			t.Fatalf("expected overwrite refusal message, got %q", output)
		}
	})
}
