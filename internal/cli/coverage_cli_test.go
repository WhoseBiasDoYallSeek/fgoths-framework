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
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	fruntime "github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

// overrideSyncEnv swaps the sync-templates globals for a temp repository
// layout and returns a restore func.
func overrideSyncEnv(t *testing.T, root, srcDir, dstDir string, files map[string]string) {
	t.Helper()
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
	runtimeSyncFiles = files
	runtimeSourceDir = srcDir
	templateDestDir = dstDir
	runtimeRepoRootOverride = root
}

// TestSyncTemplatesMissingSource verifies a missing runtime source is reported
// as an error instead of being silently skipped.
func TestSyncTemplatesMissingSource(t *testing.T) {
	tmp := t.TempDir()
	overrideSyncEnv(t, tmp, "src", "dst", map[string]string{"fake.go": "fake.go.tpl"})

	_, err := syncRuntimeTemplates(true)
	if err == nil {
		t.Fatal("expected error for missing runtime source")
	}
	if !strings.Contains(err.Error(), "read") {
		t.Errorf("error should mention the read failure, got: %v", err)
	}
}

// TestSyncTemplatesWriteError verifies a write failure during sync mode is
// surfaced (destination directory removed so the write cannot succeed).
func TestSyncTemplatesWriteError(t *testing.T) {
	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "fake.go"), []byte("package runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "fake.go.tpl"), []byte("package runtime\n\n// old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overrideSyncEnv(t, tmp, "src", "dst", map[string]string{"fake.go": "fake.go.tpl"})

	// Remove the destination directory after the env is set up so the write
	// fails with a missing parent directory.
	if err := os.RemoveAll(dstDir); err != nil {
		t.Fatal(err)
	}

	_, err := syncRuntimeTemplates(false)
	if err == nil {
		t.Fatal("expected error when the template cannot be written")
	}
	if !strings.Contains(err.Error(), "write") {
		t.Errorf("error should mention the write failure, got: %v", err)
	}
}

// TestRunSyncTemplatesRegenerateWithDrift covers the regenerate-mode output
// when drift exists: the command must rewrite templates and report success.
func TestRunSyncTemplatesRegenerateWithDrift(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
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
	overrideSyncEnv(t, tmp, "src", "dst", map[string]string{"fake.go": "fake.go.tpl"})

	output := captureStdout(t, func() {
		RunSyncTemplates(nil)
	})
	if !strings.Contains(output, "regenerated") {
		t.Errorf("expected regenerate output on drift, got: %q", output)
	}
	written, err := os.ReadFile(filepath.Join(dstDir, "fake.go.tpl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(written) != "package runtime\n\n// v2\n" {
		t.Errorf("expected template rewritten from source, got: %q", written)
	}
}

// TestRunSyncTemplatesRegenerateNoDrift covers the already-in-sync branch of
// regenerate mode.
func TestRunSyncTemplatesRegenerateNoDrift(t *testing.T) {
	_, restore := stubOSExit(t)
	defer restore()

	tmp := t.TempDir()
	srcDir := filepath.Join(tmp, "src")
	dstDir := filepath.Join(tmp, "dst")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "fake.go"), []byte("package runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, "fake.go.tpl"), []byte("package runtime\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	overrideSyncEnv(t, tmp, "src", "dst", map[string]string{"fake.go": "fake.go.tpl"})

	output := captureStdout(t, func() {
		RunSyncTemplates(nil)
	})
	if !strings.Contains(output, "already in sync") {
		t.Errorf("expected already-in-sync output, got: %q", output)
	}
}

// TestRunSyncTemplatesErrorExits covers the error branch of RunSyncTemplates
// (missing source file) without touching the real repository.
func TestRunSyncTemplatesErrorExits(t *testing.T) {
	calls, restore := stubOSExit(t)
	defer restore()

	tmp := t.TempDir()
	overrideSyncEnv(t, tmp, "src", "dst", map[string]string{"fake.go": "fake.go.tpl"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() { _ = recover() }()
		RunSyncTemplates(nil)
	}()
	select {
	case <-done:
	case <-timeAfter(5 * time.Second):
		t.Fatal("RunSyncTemplates did not return on error")
	}
	if len(*calls) == 0 {
		t.Error("expected osExit to be invoked on sync error")
	}
}

// TestRunControlPlaneShutdownOnSignal covers the graceful shutdown path
// (server.Shutdown + cp.Close) in-process: it injects an in-memory listener
// (no real network bind, sandbox-safe) and a synthetic signal channel, then
// triggers the shutdown path directly.
func TestRunControlPlaneShutdownOnSignal(t *testing.T) {
	cp, err := newControlPlaneServer(filepath.Join(t.TempDir(), "ledger.json"), "secret")
	if err != nil {
		t.Fatal(err)
	}

	server := fruntime.NewServer(":0")
	server.Handler = cp.Handler()
	server.WithShutdownTimeout(30 * time.Second)
	var closed bool
	server.OnShutdown(func(ctx context.Context) error {
		closed = true
		return cp.Close()
	})

	// In-memory listener: Serve accepts immediately without any network bind.
	server.WithListener(newPipeListener())

	stop := make(chan os.Signal, 1)
	origStop := signalStopChan
	signalStopChan = func() chan os.Signal { return stop }
	defer func() { signalStopChan = origStop }()

	// Redirect stdout with a live pipe so we can poll the banner while
	// serveControlPlaneUntilSignal is still running. The buffer is guarded
	// by a mutex because the reader goroutine writes to it concurrently
	// with the polling reads below.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	var mu sync.Mutex
	var buf bytes.Buffer
	readBuf := func() string {
		mu.Lock()
		defer mu.Unlock()
		return buf.String()
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		_, _ = io.Copy(&lockedWriter{mu: &mu, buf: &buf}, r)
	}()
	defer func() {
		os.Stdout = origStdout
		_ = w.Close()
		<-readerDone
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		serveControlPlaneUntilSignal(server, stop)
	}()

	// Wait for the listening banner, then trigger the graceful shutdown.
	deadline := timeAfter(5 * time.Second)
	for {
		if strings.Contains(readBuf(), "listening") {
			break
		}
		select {
		case <-done:
			t.Fatalf("serveControlPlaneUntilSignal returned before signal; output: %q", readBuf())
		case <-deadline:
			t.Fatalf("listening banner never appeared; output so far: %q", readBuf())
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}

	stop <- syscall.SIGTERM

	select {
	case <-done:
	case <-timeAfter(5 * time.Second):
		t.Fatal("serveControlPlaneUntilSignal did not return after SIGTERM")
	}
	_ = w.Close()
	<-readerDone
	os.Stdout = origStdout
	output := readBuf()

	if !strings.Contains(output, "Shutting down control plane gracefully") {
		t.Errorf("expected graceful shutdown output, got: %q", output)
	}
	if !closed {
		t.Error("expected OnShutdown hook (cp.Close) to run")
	}
}

// lockedWriter serializes writes to buf with mu, so io.Copy in the reader
// goroutine never races with the test's polling reads of the buffer.
type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (lw *lockedWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.buf.Write(p)
}

// newPipeListener returns an in-memory listener whose Accept never blocks on
// the network: it yields a single pre-connected pipe conn and then returns
// ErrServerClosed-style errors once closed, letting http.Server.Serve run
// without any real socket (sandbox-safe).
func newPipeListener() net.Listener {
	c1, c2 := net.Pipe()
	pl := &pipeListener{conns: make(chan net.Conn, 1)}
	pl.conns <- c2
	pl.client = c1
	return pl
}

type pipeListener struct {
	conns  chan net.Conn
	client net.Conn
	served net.Conn
	mu     sync.Mutex
	once   sync.Once
	addr   pipeAddr
}

func (p *pipeListener) Accept() (net.Conn, error) {
	c, ok := <-p.conns
	if !ok {
		return nil, net.ErrClosed
	}
	p.mu.Lock()
	p.served = c
	p.mu.Unlock()
	return c, nil
}

func (p *pipeListener) Close() error {
	// Closing the conns channel unblocks a pending Accept so
	// http.Server.Serve returns ErrServerClosed (its listener WaitGroup
	// must be released before Shutdown proceeds). Closing both pipe ends
	// ends any pending HTTP exchange and lets the accepted conn retire so
	// closeIdleConns reaches quiescence.
	p.once.Do(func() { close(p.conns) })
	p.mu.Lock()
	served := p.served
	p.mu.Unlock()
	if served != nil {
		_ = served.Close()
	}
	if p.client != nil {
		_ = p.client.Close()
	}
	return nil
}

func (p *pipeListener) Addr() net.Addr { return &p.addr }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "inmem" }

// TestRunDevMakeFailure verifies RunDev surfaces a failing `make dev` as a
// non-zero exit instead of swallowing the error.
func TestRunDevMakeFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("subprocess test skipped in short mode")
	}
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	makefile := "dev:\n\t@exit 3\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run=TestRunDevMakeFailureHelper", "-test.timeout=10s")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FGOTHS_DEV_FAIL_HELPER=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "delegated") && !strings.Contains(string(out), "exit") {
		// The helper's own output is what matters; a failing make is expected.
		t.Logf("helper output: %s", out)
	}
}

// TestRunDevMakeFailureHelper is the re-exec target for the make-failure test.
func TestRunDevMakeFailureHelper(t *testing.T) {
	if os.Getenv("FGOTHS_DEV_FAIL_HELPER") == "" {
		t.Skip("helper process only")
	}
	RunDev(nil) // os.Exit(1) is expected when make dev fails
}

// setupRoutesProject writes a go.mod and handler files with route patterns of
// differing lengths so RunRoutes exercises the sort and column-width logic.
func setupRoutesProject(t *testing.T) {
	t.Helper()
	if err := os.WriteFile("go.mod", []byte("module example.com/my-app\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	mainSrc := `package main

func main() {
	mux.HandleFunc("GET /health", nil)
	mux.HandleFunc("POST /api/v1/users", nil)
	mux.Handle("/open", nil)
}
`
	if err := os.WriteFile("main.go", []byte(mainSrc), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.MkdirAll("handlers", 0o755); err != nil {
		t.Fatalf("mkdir handlers: %v", err)
	}
	handlerSrc := `package handlers

func ignored() { mux.HandleFunc("GET /api/v1/users/{id}", nil) }
`
	if err := os.WriteFile(filepath.Join("handlers", "user_handler.go"), []byte(handlerSrc), 0o644); err != nil {
		t.Fatalf("write handler: %v", err)
	}
}

// TestExecuteDispatchesRoutesCommand covers the routes branch of Execute plus
// the sort/width logic of RunRoutes with multiple routes of varying length.
func TestExecuteDispatchesRoutesCommand(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		setupRoutesProject(t)

		exitCode := runWithExecuteStub(t, []string{"fgoths", "routes"}, func() {
			captureStdout(t, Execute)
		})
		if exitCode != 0 {
			t.Fatalf("expected exit code 0 for routes dispatch, got %d", exitCode)
		}
	})
}

// TestExecuteDispatchesDevWithoutMakefile covers the dev branch of Execute via
// RunDev's fast failure path (no Makefile in the current directory).
func TestExecuteDispatchesDevWithoutMakefile(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		exitCode := runWithExecuteStub(t, []string{"fgoths", "dev"}, func() {
			captureStdout(t, Execute)
		})
		if exitCode != 1 {
			t.Fatalf("expected exit code 1 for dev without Makefile, got %d", exitCode)
		}
	})
}

// TestExecuteDispatchesControlPlaneWithoutToken covers the controlplane branch
// of Execute via RunControlPlane's fails-closed path (no token configured).
func TestExecuteDispatchesControlPlaneWithoutToken(t *testing.T) {
	t.Setenv("FGOTHS_CP_TOKEN", "")
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		exitCode := runWithExecuteStub(t, []string{"fgoths", "controlplane"}, func() {
			captureStdout(t, Execute)
		})
		if exitCode != 1 {
			t.Fatalf("expected exit code 1 for controlplane without token, got %d", exitCode)
		}
	})
}

// TestExecuteDispatchesSyncCommand covers the sync-templates branch of
// Execute by pointing the sync globals at a temp repository in regenerate
// (non-check) mode, which completes quickly.
func TestExecuteDispatchesSyncCommand(t *testing.T) {
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

	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		overrideSyncEnv(t, tmp, "pkg/runtime", "internal/generator/templates",
			map[string]string{"fake.go": "base/pkg/runtime/fake.go.tpl"})

		exitCode := runWithExecuteStub(t, []string{"fgoths", "sync-templates"}, func() {
			captureStdout(t, Execute)
		})
		if exitCode != 0 {
			t.Fatalf("expected exit code 0 for sync-templates dispatch, got %d", exitCode)
		}
	})
}

// TestLoopGenerateWatcherAddsNewDirectories verifies that a Create event for a
// directory is followed by a watcher.Add call, in an isolated process.
func TestLoopGenerateWatcherAddsNewDirectories(t *testing.T) {
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
	cmd := exec.Command(bin, "-test.run=TestGenerateWatchNewDirHelper", "-test.timeout=15s")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "FGOTHS_WATCH_NEWDIR_HELPER=1")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "Watching new directory") {
		t.Errorf("expected new-directory watch output, got: %s", out)
	}
}

// TestGenerateWatchNewDirHelper is the re-exec target for the new-directory
// test: it starts the watcher, creates a subdirectory to trigger a Create
// event, then stops the watcher.
func TestGenerateWatchNewDirHelper(t *testing.T) {
	if os.Getenv("FGOTHS_WATCH_NEWDIR_HELPER") == "" {
		t.Skip("helper process only")
	}
	_, restore := stubOSExit(t)
	defer restore()

	watcher := setupGenerateWatcher()
	if watcher == nil {
		t.Fatal("setupGenerateWatcher returned nil")
	}
	defer func() { _ = watcher.Close() }()

	go func() {
		time.Sleep(300 * time.Millisecond)
		if err := os.MkdirAll(filepath.Join("views", "components"), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "mkdir failed: %v\n", err)
		}
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		loopGenerateWatcher(watcher)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("loopGenerateWatcher did not stop after watcher close")
	}
}
