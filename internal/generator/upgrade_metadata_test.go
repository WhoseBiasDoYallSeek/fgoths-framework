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

func withManagedRuntimeTemplates(t *testing.T, templates map[string]string) {
	t.Helper()
	original := managedRuntimeTemplates
	managedRuntimeTemplates = templates
	t.Cleanup(func() { managedRuntimeTemplates = original })
}

func TestManagedRuntimeSourcesRejectsUnreadableAndTemplatedSources(t *testing.T) {
	withManagedRuntimeTemplates(t, map[string]string{"pkg/runtime/missing.go": "templates/missing.go.tpl"})
	if _, err := ManagedRuntimeSources(); err == nil || !strings.Contains(err.Error(), "read managed runtime template") {
		t.Fatalf("missing template error = %v", err)
	}

	withManagedRuntimeTemplates(t, map[string]string{"go.mod": "templates/base/go.mod.tpl"})
	if _, err := ManagedRuntimeSources(); err == nil || !strings.Contains(err.Error(), "unsupported template directives") {
		t.Fatalf("templated source error = %v", err)
	}
}

func TestGenerateFailsWhenUpgradeMetadataCannotBeWritten(t *testing.T) {
	withTempWorkingDirectory(t)
	withManagedRuntimeTemplates(t, map[string]string{"pkg/runtime/missing.go": "templates/missing.go.tpl"})
	cfg := config.ProjectConfig{
		Name:             "metadata-failure",
		Type:             config.TypeAPI,
		Architecture:     config.ArchFlat,
		Database:         config.DBNone,
		FrameworkVersion: "1.2.0",
	}
	if err := Generate(cfg); err == nil || !strings.Contains(err.Error(), "failed to write upgrade metadata") {
		t.Fatalf("Generate() error = %v, want metadata failure", err)
	}
	if _, err := os.Stat(cfg.Name); !os.IsNotExist(err) {
		t.Fatalf("project directory exists after metadata failure: %v", err)
	}
}

func TestWriteProjectUpgradeMetadataErrors(t *testing.T) {
	cfg := config.ProjectConfig{FrameworkVersion: "1.2.0"}

	t.Run("runtime file is unreadable", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "pkg", "runtime", "server.go"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := writeProjectUpgradeMetadata(root, cfg); err == nil || !strings.Contains(err.Error(), "read generated runtime file") {
			t.Fatalf("writeProjectUpgradeMetadata() error = %v", err)
		}
	})

	t.Run("metadata cannot be encoded", func(t *testing.T) {
		original := marshalUpgradeMetadata
		marshalUpgradeMetadata = func(any, string, string) ([]byte, error) { return nil, errors.New("encode failed") }
		t.Cleanup(func() { marshalUpgradeMetadata = original })
		if err := writeProjectUpgradeMetadata(t.TempDir(), cfg); err == nil || !strings.Contains(err.Error(), "encode failed") {
			t.Fatalf("writeProjectUpgradeMetadata() error = %v", err)
		}
	})

	t.Run("metadata directory cannot be created", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".fgoths"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := writeProjectUpgradeMetadata(root, cfg); err == nil {
			t.Fatal("writeProjectUpgradeMetadata() succeeded with .fgoths as a file")
		}
	})
}

func TestIsReleaseVersion(t *testing.T) {
	cases := map[string]bool{
		"1.2.0":     true,
		"v10.20.30": true,
		"1.2":       false,
		"1..0":      false,
		"1.2.x":     false,
		"0.0.0-dev": false,
		"(devel)":   false,
	}
	for version, want := range cases {
		if got := IsReleaseVersion(version); got != want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", version, got, want)
		}
	}
}
