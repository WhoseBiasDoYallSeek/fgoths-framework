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
	"sync"
	"testing"
)

// executeStubMu serializes tests that mutate process-global state (os.Args,
// osExit). Go runs tests sequentially by default, but the mutex guards
// against accidental t.Parallel() adoption and produces clearer failures
// than a panicked index-out-of-range.
var executeStubMu sync.Mutex

// runWithExecuteStub locks executeStubMu, installs the stubbed os.Args and
// osExit, runs fn, restores the originals, and returns the captured exit
// code (0 if fn never invoked osExit).
func runWithExecuteStub(t *testing.T, args []string, fn func()) int {
	t.Helper()
	executeStubMu.Lock()
	defer executeStubMu.Unlock()

	originalArgs := os.Args
	originalExit := osExit
	t.Cleanup(func() { os.Args = originalArgs })
	t.Cleanup(func() { osExit = originalExit })
	os.Args = args
	var code int
	osExit = func(c int) { code = c }
	fn()
	return code
}

func TestRunInitMissingNameReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--preset=api", "--db=none"})
		if err == nil {
			t.Fatal("expected error when --name is missing")
		}
		if !strings.Contains(err.Error(), "project name is required") {
			t.Fatalf("expected helpful error, got %v", err)
		}
	})
}

func TestRunInitUnknownPresetReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		output := captureStdout(t, func() {
			err := RunInit([]string{"--preset=does-not-exist", "--name=foo"})
			if err == nil {
				t.Fatal("expected error for unknown preset")
			}
			if !strings.Contains(err.Error(), "unknown preset") {
				t.Fatalf("expected helpful error, got %v", err)
			}
		})
		if !strings.Contains(output, "Available presets: api, webapp") {
			t.Fatalf("expected preset list in output, got %q", output)
		}
		if _, err := os.Stat(filepath.Join(dir, "foo")); err == nil {
			t.Fatal("expected no project to be created when preset is invalid")
		}
	})
}

func TestRunInitUnknownPresetNameReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--name=foo", "--preset=banana"})
		if err == nil {
			t.Fatal("expected error for unknown preset")
		}
		if !strings.Contains(err.Error(), "unknown preset") {
			t.Fatalf("expected helpful error, got %v", err)
		}
	})
}

func TestRunInitUnsupportedDBOverrideReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--name=foo", "--preset=api", "--db=spaghetti"})
		if err == nil {
			t.Fatal("expected error for unsupported database")
		}
		if !strings.Contains(err.Error(), "unsupported database") {
			t.Fatalf("expected helpful error, got %v", err)
		}
	})
}

func TestRunInitUnsupportedDBReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--name=foo", "--preset=api", "--db=oracle"})
		if err == nil {
			t.Fatal("expected error for unsupported database")
		}
		if !strings.Contains(err.Error(), "unsupported database") {
			t.Fatalf("expected helpful error, got %v", err)
		}
	})
}

func TestRunInitInvalidFeaturesReturnsError(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		err := RunInit([]string{"--name=foo", "--preset=api", "--features=health,bogus"})
		if err == nil {
			t.Fatal("expected error for unknown feature")
		}
		if !strings.Contains(err.Error(), "unknown feature") {
			t.Fatalf("expected helpful error, got %v", err)
		}
	})
}

func TestExecuteUnknownCommandPrintsUsage(t *testing.T) {
	exitCode := runWithExecuteStub(t, []string{"fgoths", "explode"}, func() {
		output := captureStdout(t, Execute)
		if !strings.Contains(output, "Unknown command: explode") {
			t.Fatalf("expected unknown command message, got %q", output)
		}
		if !strings.Contains(output, "fgoths <command>") {
			t.Fatalf("expected usage hint, got %q", output)
		}
	})
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}

func TestExecuteWithNoArgsPrintsUsageAndExits(t *testing.T) {
	exitCode := runWithExecuteStub(t, []string{"fgoths"}, func() {
		output := captureStdout(t, Execute)
		if !strings.Contains(output, "fgoths <command>") {
			t.Fatalf("expected usage, got %q", output)
		}
	})
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
}

func TestExecuteInitErrorBubbles(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		exitCode := runWithExecuteStub(t, []string{"fgoths", "init", "--preset=api"}, func() {
			_ = captureStdout(t, Execute)
		})
		if exitCode != 1 {
			t.Fatalf("expected exit code 1 when --name is missing, got %d", exitCode)
		}
	})
}
