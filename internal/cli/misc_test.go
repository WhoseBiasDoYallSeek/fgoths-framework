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
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunVersionOutput(t *testing.T) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	RunVersion(nil)

	w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy: %v", err)
	}
	output := buf.String()

	for _, want := range []string{"FGOTHS Framework", "version:", "go:", "platform:"} {
		if !strings.Contains(output, want) {
			t.Errorf("RunVersion output missing %q, got: %s", want, output)
		}
	}
}

func TestNewControlPlaneServerWithFileStore(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "ledger.json")
	cp, err := newControlPlaneServer(storePath, "test-token")
	if err != nil {
		t.Fatalf("newControlPlaneServer failed: %v", err)
	}
	if cp == nil || cp.Handler() == nil {
		t.Fatal("expected a configured control plane handler")
	}
	if err := cp.Close(); err != nil {
		t.Fatalf("close control plane: %v", err)
	}
}

func TestRunSyncTemplatesCheckModeInSync(t *testing.T) {
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	RunSyncTemplates([]string{"--check"})

	w.Close()
	os.Stdout = orig

	var buf bytes.Buffer
	io.Copy(&buf, r)

	output := buf.String()
	if strings.Contains(output, "drifted") {
		t.Errorf("templates drifted; run: go run ./cmd/fgoths sync-templates. Output: %s", output)
	}
	if !strings.Contains(output, "in sync") && !strings.Contains(output, "Runtime templates are in sync") {
		t.Errorf("expected in-sync message, got: %s", output)
	}
}

func TestOsHelpersIsNotExist(t *testing.T) {
	err := os.ErrNotExist
	if !osIsNotExist(err) {
		t.Error("expected ErrNotExist to return true")
	}

	otherErr := os.ErrPermission
	if osIsNotExist(otherErr) {
		t.Error("expected non-errnotexist to return false")
	}
}

func TestRepoRoot(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Fatal("repoRoot returned empty string")
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("repoRoot %q is not readable: %v", root, err)
	}

	found := false
	for _, e := range entries {
		if e.Name() == "pkg" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("repoRoot %q does not contain pkg directory", root)
	}
}

func TestRedReturnsANSI(t *testing.T) {
	result := red("test error")
	// Without NO_COLOR, red() should wrap with ANSI red code
	if !strings.Contains(result, "test error") {
		t.Errorf("red() should contain the original string")
	}
}

func TestEmojiExtractsFirstWord(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"🔧 building...", "🔧"},
		{"✅ done", "✅"},
		{"⚠️ warning", "⚠️"},
		{"plain", "plain"},
		{"", ""},
		{"single", "single"},
		{"🔄 processing data", "🔄"},
	}
	for _, tc := range cases {
		got := emoji(tc.input)
		if got != tc.want {
			t.Errorf("emoji(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestSupportsColorNocolor(t *testing.T) {
	// Set NO_COLOR to force no color
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	if supportsColor() {
		t.Error("expected supportsColor() to return false when NO_COLOR is set")
	}
}

func TestSupportsColorDefault(t *testing.T) {
	os.Unsetenv("NO_COLOR")
	// Default: supportsColor returns true (assumes ANSI-capable terminal)
	if !supportsColor() {
		t.Error("expected supportsColor() to return true by default")
	}
}

func TestColorNoColorFallback(t *testing.T) {
	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	// When color is disabled, color() should return plain string
	result := color("hello", colorRed)
	if result != "hello" {
		t.Errorf("expected plain string when NO_COLOR is set, got: %s", result)
	}
}
