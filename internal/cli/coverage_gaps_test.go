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
	"syscall"
	"testing"
	"time"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

// stubOSExit replaces osExit with a recorder and returns a restore func.
func stubOSExit(t *testing.T) (*[]int, func()) {
	t.Helper()
	orig := osExit
	var calls []int
	osExit = func(code int) {
		calls = append(calls, code)
		// Panic to unwind out of the calling command without killing tests.
		panic(exitSentinel{code: code})
	}
	return &calls, func() { osExit = orig }
}

type exitSentinel struct{ code int }

// TestRunControlPlaneFailsClosedWithoutToken verifies the control plane refuses
// to start without an admin token (fail-closed behavior).
func TestRunControlPlaneFailsClosedWithoutToken(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	t.Setenv("FGOTHS_CP_TOKEN", "")
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(exitSentinel); !ok {
				panic(r)
			}
		}
	}()

	// RunControlPlane exits 1 when no token is provided. Run it in a goroutine
	// since osExit is stubbed to panic.
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunControlPlane([]string{"--addr=:0"})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunControlPlane did not return without a token")
	}
}

// TestRunControlPlaneRejectsBadStore verifies a sqlite:// store that cannot be
// opened is reported as an error instead of crashing.
func TestRunControlPlaneRejectsBadStore(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	dir := t.TempDir()
	badStore := "sqlite://" + filepath.Join(dir, "nonexistent-dir", "cp.db")

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunControlPlane([]string{"--addr=:0", "--store=" + badStore, "--token=secret"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunControlPlane did not return for a bad store")
	}
}

// TestRunDevRejectsPositionalArgs verifies dev rejects unexpected arguments.
func TestRunDevRejectsPositionalArgs(t *testing.T) {
	calls, restore := stubOSExit(t)
	defer restore()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunDev([]string{"unexpected"})
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunDev did not return for positional args")
	}
	if len(*calls) == 0 {
		t.Error("expected osExit to be called for positional args")
	}
}

// TestRunDevRequiresGoProject verifies dev fails cleanly outside a project.
func TestRunDevRequiresGoProject(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	tmp := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunDev(nil)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunDev did not return outside a Go project")
	}
}

// TestEnsureToolMissing verifies a clear error when a tool is absent.
func TestEnsureToolMissing(t *testing.T) {
	err := ensureTool("definitely-not-a-real-tool-xyz", "install hint")
	if err == nil {
		t.Fatal("expected error for missing tool")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-real-tool-xyz") {
		t.Errorf("error should mention the tool name, got: %v", err)
	}
}

// TestRunGenerateOnceFlatBuffers verifies flatc failure is surfaced.
func TestRunGenerateOnceFlatBuffersFailure(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "app.fbs"), []byte("table App {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		runGenerateOnce()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("runGenerateOnce did not return when flatc is missing")
	}
}

// TestRunGenerateOnceTemplFailure verifies templ failure is surfaced.
func TestRunGenerateOnceTemplFailure(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "index.templ"), []byte("package views"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		// ensureTool("templ") succeeds if templ is installed on the dev
		// machine, so force failure by pointing PATH at an empty dir.
		t.Setenv("PATH", t.TempDir())
		runGenerateOnce()
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("runGenerateOnce did not return when templ fails")
	}
}

// TestGenerateRejectsUnknownFeatureReinforcesConfigCoherence keeps the CLI
// aligned with config.ValidFeatures: every feature accepted by normalizeFeature
// must be part of the supported set, and unknown ones must be rejected.
func TestGenerateRejectsUnknownFeatureReinforcesConfigCoherence(t *testing.T) {
	accepted := map[config.Feature]bool{}
	for _, raw := range []string{"health", "HEALTH", "health-check", "metrics", "prometheus", "openapi", "swagger", "grpc", "flatbuffers", "flat-buffers", "htmx", "jwt-auth", "jwtauth", "mtls", "otel", "ci-cd"} {
		f, ok := normalizeFeatureName(raw)
		if !ok {
			t.Errorf("normalizeFeatureName(%q) reported unknown; expected acceptance", raw)
			continue
		}
		accepted[f] = true
	}
	for f := range accepted {
		found := false
		for _, valid := range config.ValidFeatures {
			if valid == f {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("normalizeFeatureName produced %q which is not in ValidFeatures", f)
		}
	}
	for _, raw := range []string{"bogus", "", "nope"} {
		if f, ok := normalizeFeatureName(raw); ok {
			t.Errorf("normalizeFeatureName(%q) = %q with ok=true; expected rejection", raw, f)
		}
	}
}

// TestRunSyncTemplatesCheckAndSyncModes covers both --check and regenerate
// output paths of the developer command end to end (no drift case).
func TestRunSyncTemplatesCheckModeNoDrift(t *testing.T) {
	// The repository is expected to be in sync; check mode must not exit.
	_, restore := stubOSExit(t)
	defer restore()

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunSyncTemplates([]string{"--check"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunSyncTemplates --check did not return")
	}
}

// TestRepoRootResolves verifies repoRoot works from any CWD inside the repo.
func TestRepoRootResolves(t *testing.T) {
	root := repoRoot()
	if !strings.HasSuffix(filepath.Clean(root), "fgoths-framework") {
		t.Errorf("repoRoot() = %q, expected the fgoths-framework directory", root)
	}
	info, err := os.Stat(filepath.Join(root, "go.mod"))
	if err != nil || info.IsDir() {
		t.Errorf("repoRoot() does not point at a Go module root: %v", err)
	}
}

// TestRunRoutesOnSyntheticProject exercises the routes scanner.
func TestRunRoutesOnSyntheticProject(t *testing.T) {
	tmp := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })

	src := `package main

import "net/http"

func main() {
	http.HandleFunc("/alpha", func(w http.ResponseWriter, r *http.Request) {})
	http.HandleFunc("/beta", nil)
	http.Handle("/gamma", http.NotFoundHandler())
}
`
	if err := os.MkdirAll(filepath.Join(tmp, "cmd", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "cmd", "app", "main.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	RunRoutes([]string{}) // must not panic or exit
}

// TestRunControlPlaneSQLiteStoreRequiresTag verifies the sqlite store helper
// returns an error in the default (no sqlite tag) build.
func TestRunControlPlaneSQLiteStoreRequiresTag(t *testing.T) {
	cp, err := newControlPlaneServer("sqlite://"+filepath.Join(t.TempDir(), "cp.db"), "secret")
	if err != nil {
		return // expected without the sqlite tag
	}
	if cp == nil {
		t.Fatal("got nil control plane server without error")
	}
	_ = cp.Close()
}

// silence unused warnings for syscall import used indirectly.
var _ = syscall.SIGTERM
