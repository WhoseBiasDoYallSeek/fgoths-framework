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
	"testing"
)

// TestTestValueForAllTypes verifies the CRUD test-value helper covers every
// supported field type and returns a sentinel for unsupported ones.
func TestTestValueForAllTypes(t *testing.T) {
	cases := map[string]string{
		"string":  `"test"`,
		"bool":    "true",
		"int":     "1",
		"int64":   "1",
		"float64": "1.5",
		"unknown": "",
	}
	for goType, want := range cases {
		if got := testValueFor(goType); got != want {
			t.Errorf("testValueFor(%q) = %q, want %q", goType, got, want)
		}
	}
}

// TestRequireSQLiteProjectErrors covers the failure paths of the CRUD
// preconditions using synthetic project layouts.
func TestRequireSQLiteProjectErrors(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Run("no go.mod", func(t *testing.T) {
		if err := os.Chdir(t.TempDir()); err != nil {
			t.Fatal(err)
		}
		if err := requireMVCProject(); err == nil {
			t.Error("expected error when go.mod is missing")
		}
	})

	t.Run("no sqlite dependency", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.27\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		if err := requireMVCProject(); err == nil {
			t.Error("expected error when sqlite dependency is absent")
		}
	})

	t.Run("sqlite without mvc layout", func(t *testing.T) {
		dir := t.TempDir()
		goMod := "module demo\n\nrequire modernc.org/sqlite v1.34.4\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		if err := requireMVCProject(); err == nil {
			t.Error("expected error when the MVC layout (models/) is absent")
		}
	})

	t.Run("valid sqlite clean project", func(t *testing.T) {
		dir := t.TempDir()
		goMod := "module demo\n\nrequire modernc.org/sqlite v1.34.4\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(goMod), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "models"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(dir); err != nil {
			t.Fatal(err)
		}
		if err := requireMVCProject(); err != nil {
			t.Errorf("expected success, got: %v", err)
		}
	})
}
