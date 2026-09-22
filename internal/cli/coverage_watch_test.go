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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEnsureToolTemplFallsBackToGoInstall covers the templ branch of
// ensureTool: when templ is not on PATH the code attempts `go install` and,
// when that fails under an empty PATH, returns the install hint.
func TestEnsureToolTemplFallsBackToGoInstall(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	// Isolate PATH so neither templ nor go can be resolved, forcing the
	// final error return without actually installing anything.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)

	err := ensureTool("templ", "go install github.com/a-h/templ/cmd/templ@latest")
	if err == nil {
		t.Fatal("expected error when templ cannot be resolved or installed")
	}
	if !strings.Contains(err.Error(), "install") {
		t.Errorf("error should contain the install hint, got: %v", err)
	}
}

// TestRunTemplWithoutTooling covers runTempl failure when neither templ nor go
// can be executed.
func TestRunTemplWithoutTooling(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if err := runTempl(); err == nil {
		t.Fatal("expected error when templ tooling is unavailable")
	}
}

// TestRunGenerateWatchSetupMessages verifies the watch-mode setup path in a
// subprocess so the infinite select loop and the global CWD never leak into
// other tests.
func TestRunGenerateWatchSetupMessages(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test skipped in short mode")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "views"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=TestGenerateWatchProcessHelper", "-test.timeout=5s")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FGOTHS_WATCH_HELPER=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Watching") {
		t.Errorf("expected watch setup output, got: %s", out)
	}
}

// TestGenerateWatchProcessHelper is never run directly; it exists so
// TestRunGenerateWatchSetupMessages can re-exec the test binary and exercise
// runGenerateWatch in an isolated process.
func TestGenerateWatchProcessHelper(t *testing.T) {
	if os.Getenv("FGOTHS_WATCH_HELPER") == "" {
		t.Skip("helper process only")
	}
	_, restore := stubOSExit(t)
	defer restore()
	runGenerateWatch()
}

// TestWatchDevProcessSetupMessages verifies the dev watch setup path in a
// subprocess (same isolation rationale as the generate watch test).
func TestRunDevDelegatesToMake(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test skipped in short mode")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	makefile := "dev:\n\t@echo delegated-to-make-dev\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=TestRunDevProcessHelper", "-test.timeout=10s")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FGOTHS_DEV_HELPER=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "delegated-to-make-dev") {
		t.Errorf("expected RunDev to delegate to `make dev`, got: %s", out)
	}
}

// TestRunDevProcessHelper is the re-exec target for the RunDev test.
func TestRunDevProcessHelper(t *testing.T) {
	if os.Getenv("FGOTHS_DEV_HELPER") == "" {
		t.Skip("helper process only")
	}
	_, restore := stubOSExit(t)
	defer restore()
	RunDev(nil)
}

// TestRunControlPlaneStartupAndShutdown runs the control plane server in a
// subprocess, covering the serve path without blocking the test binary.
func TestRunControlPlaneStartupAndShutdown(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test skipped in short mode")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=TestRunControlPlaneProcessHelper", "-test.timeout=10s")
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "FGOTHS_CP_HELPER=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Control plane listening") {
		t.Errorf("expected control plane startup output, got: %s", out)
	}
}

// TestRunControlPlaneProcessHelper is the re-exec target for the control
// plane startup test.
func TestRunControlPlaneProcessHelper(t *testing.T) {
	if os.Getenv("FGOTHS_CP_HELPER") == "" {
		t.Skip("helper process only")
	}
	_, restore := stubOSExit(t)
	defer restore()
	RunControlPlane([]string{"--addr=:0", "--token=secret"})
}

// TestRunSyncTemplatesRegenerateMode covers the non-check output branch.
func TestRunSyncTemplatesRegenerateMode(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunSyncTemplates(nil)
	}()
	select {
	case <-done:
	case <-timeAfter(5 * time.Second):
		t.Fatal("RunSyncTemplates regenerate mode did not return")
	}
}

// TestRunSyncTemplatesDriftExits covers the drift branch of --check using a
// temporary repository override.
func TestRunSyncTemplatesDriftExits(t *testing.T) {
	calls, restoreOSExit := stubOSExit(t)
	defer restoreOSExit()

	// Build a fake repo with drifting content.
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "pkg", "runtime")
	dstDir := filepath.Join(tmp, "internal", "generator", "templates", "base", "pkg", "runtime")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "fake.go"), []byte("package runtime\n\n// v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "fake.go.tpl"), []byte("package runtime\n\n// v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	origFiles := runtimeSyncFiles
	origSrc := runtimeSourceDir
	origDst := templateDestDir
	origRoot := runtimeRepoRootOverride
	t.Cleanup(func() {
		runtimeSyncFiles = origFiles
		runtimeSourceDir = origSrc
		templateDestDir = origDst
		runtimeRepoRootOverride = origRoot
	})

	runtimeSyncFiles = map[string]string{"fake.go": "base/pkg/runtime/fake.go.tpl"}
	runtimeSourceDir = "pkg/runtime"
	templateDestDir = "internal/generator/templates"
	runtimeRepoRootOverride = tmp

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunSyncTemplates([]string{"--check"})
	}()
	select {
	case <-done:
	case <-timeAfter(5 * time.Second):
		t.Fatal("RunSyncTemplates --check did not return on drift")
	}
	if len(*calls) == 0 {
		t.Error("expected osExit to be invoked on drift")
	}
}
