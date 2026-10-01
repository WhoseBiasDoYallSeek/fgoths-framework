// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
	"github.com/fsnotify/fsnotify"
)

func TestRunBuildFailureAndOptionalOutputErrors(t *testing.T) {
	t.Run("build failure exits", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			bin := t.TempDir()
			writeCLIScript(t, bin, "go", "#!/bin/sh\nexit 1\n")
			t.Setenv("PATH", bin)
			codes := recordCLIExits(t, func() {
				output := captureStdout(t, func() { RunBuild(nil) })
				if !strings.Contains(output, "Build failed:") {
					t.Fatalf("expected build failure message, got %q", output)
				}
			})
			if fmt.Sprint(codes) != "[1]" {
				t.Fatalf("exit codes = %v, want [1]", codes)
			}
		})
	})

	t.Run("optional artifacts report write errors", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			bin := t.TempDir()
			writeCLIScript(t, bin, "go", "#!/bin/sh\nif [ \"$1\" = \"list\" ]; then printf '\"Path\": \"example.com/app\"\\n'; fi\nexit 0\n")
			t.Setenv("PATH", bin)
			if err := os.Mkdir("Dockerfile", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir("sbom.json", 0o755); err != nil {
				t.Fatal(err)
			}
			output := captureStdout(t, func() {
				RunBuild([]string{"--scratch", "--sbom"})
			})
			for _, want := range []string{"Could not generate Dockerfile:", "Could not generate SBOM:"} {
				if !strings.Contains(output, want) {
					t.Errorf("expected %q in output %q", want, output)
				}
			}
		})
	})
}

func TestGenerateSBOMCommandError(t *testing.T) {
	withWorkingDir(t, t.TempDir(), func() {
		bin := t.TempDir()
		writeCLIScript(t, bin, "go", "#!/bin/sh\nexit 1\n")
		t.Setenv("PATH", bin)
		if err := generateSBOM(); err == nil {
			t.Fatal("generateSBOM() expected go list failure")
		}
	})
}

func TestControlPlaneStartupAndSignalShutdown(t *testing.T) {
	originalSignal := signalStopChan
	t.Cleanup(func() { signalStopChan = originalSignal })
	stop := make(chan os.Signal, 1)
	signalStopChan = func() chan os.Signal {
		time.AfterFunc(50*time.Millisecond, func() { stop <- syscall.SIGTERM })
		return stop
	}

	store := filepath.Join(t.TempDir(), "control-plane.json")
	output := captureStdout(t, func() {
		RunControlPlane([]string{"--addr=:0", "--store=" + store, "--token=secret"})
	})
	if !strings.Contains(output, "FGOTHS Control Plane API") || !strings.Contains(output, "Shutting down control plane gracefully") {
		t.Fatalf("expected startup and shutdown banners, got %q", output)
	}
}

func TestControlPlaneServeErrorBranches(t *testing.T) {
	t.Run("unexpected listener error is reported", func(t *testing.T) {
		server := runtime.NewServer(":0")
		stop := make(chan os.Signal, 1)
		fatal := make(chan error, 1)
		listenerErr := errors.New("listen failed")
		done := make(chan struct{})
		go func() {
			serveControlPlaneUntilSignalWith(server, stop, func() error { return listenerErr }, func(err error) {
				fatal <- err
			})
			close(done)
		}()
		select {
		case got := <-fatal:
			if !errors.Is(got, listenerErr) {
				t.Fatalf("fatal error = %v, want %v", got, listenerErr)
			}
		case <-time.After(time.Second):
			t.Fatal("listener failure was not reported")
		}
		stop <- syscall.SIGTERM
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("control-plane shutdown did not return")
		}
	})

	t.Run("ErrServerClosed is not fatal", func(t *testing.T) {
		server := runtime.NewServer(":0")
		stop := make(chan os.Signal, 1)
		fatal := make(chan error, 1)
		done := make(chan struct{})
		go func() {
			serveControlPlaneUntilSignalWith(server, stop, func() error { return http.ErrServerClosed }, func(err error) {
				fatal <- err
			})
			close(done)
		}()
		stop <- syscall.SIGTERM
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("control-plane shutdown did not return")
		}
		select {
		case err := <-fatal:
			t.Fatalf("ErrServerClosed was reported as fatal: %v", err)
		default:
		}
	})

	t.Run("shutdown hook error is logged", func(t *testing.T) {
		server := runtime.NewServer(":0").OnShutdown(func(context.Context) error {
			return errors.New("close failed")
		})
		stop := make(chan os.Signal, 1)
		done := make(chan struct{})
		go func() {
			serveControlPlaneUntilSignal(server, stop)
			close(done)
		}()
		time.Sleep(30 * time.Millisecond)
		stop <- syscall.SIGTERM
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("control-plane shutdown did not return")
		}
	})

	t.Run("wrapper forwards unexpected listen errors to fatal logger", func(t *testing.T) {
		originalFatal := controlPlaneFatalf
		t.Cleanup(func() { controlPlaneFatalf = originalFatal })
		fatal := make(chan error, 1)
		controlPlaneFatalf = func(_ string, args ...any) {
			if len(args) > 0 {
				if err, ok := args[0].(error); ok {
					fatal <- err
				}
			}
		}
		stop := make(chan os.Signal, 1)
		done := make(chan struct{})
		go func() {
			serveControlPlaneUntilSignal(runtime.NewServer("invalid address"), stop)
			close(done)
		}()
		select {
		case err := <-fatal:
			if err == nil {
				t.Fatal("expected listener error")
			}
		case <-time.After(time.Second):
			t.Fatal("fatal logger did not receive the listener error")
		}
		stop <- syscall.SIGTERM
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("wrapper shutdown did not return")
		}
	})
}

func TestSignalStopChanRegistration(t *testing.T) {
	stop := signalStopChan()
	if stop == nil {
		t.Fatal("signalStopChan() returned nil")
	}
	signal.Stop(stop)
}

func TestNewControlPlaneSQLiteStoreSelection(t *testing.T) {
	original := openSQLiteControlPlaneStores
	t.Cleanup(func() { openSQLiteControlPlaneStores = original })
	openSQLiteControlPlaneStores = func(path string) (runtime.DeploymentStore, runtime.PolicyVersionStore, error) {
		if path != "ledger.db" {
			t.Errorf("sqlite path = %q, want ledger.db", path)
		}
		return nil, nil, nil
	}
	server, err := newControlPlaneServer("sqlite://ledger.db", "secret")
	if err != nil || server == nil {
		t.Fatalf("newControlPlaneServer() = (%v, %v)", server, err)
	}
}

func TestGenerateCommandToolResolutionAndWatchPaths(t *testing.T) {
	t.Run("templ install failure", func(t *testing.T) {
		bin, gopath := t.TempDir(), t.TempDir()
		writeCLIScript(t, bin, "go", "#!/bin/sh\nif [ \"$1\" = \"env\" ]; then printf '%s\\n' \"$FAKE_GOPATH\"; exit 0; fi\nexit 1\n")
		t.Setenv("PATH", bin)
		t.Setenv("FAKE_GOPATH", gopath)
		if err := ensureTool("templ", "templ installation hint"); err == nil || !strings.Contains(err.Error(), "installation hint") {
			t.Fatalf("ensureTool() error = %v", err)
		}
	})

	t.Run("templ is found after successful install", func(t *testing.T) {
		bin, gopath := t.TempDir(), t.TempDir()
		script := "#!/bin/sh\n" +
			"if [ \"$1\" = \"env\" ]; then printf '%s\\n' \"$FAKE_GOPATH\"; exit 0; fi\n" +
			"if [ \"$1\" = \"install\" ]; then /bin/mkdir -p \"$FAKE_GOPATH/bin\"; " +
			"printf '#!/bin/sh\\nexit 0\\n' > \"$FAKE_GOPATH/bin/templ\"; " +
			"/bin/chmod +x \"$FAKE_GOPATH/bin/templ\"; exit 0; fi\n" +
			"exit 1\n"
		writeCLIScript(t, bin, "go", script)
		t.Setenv("PATH", bin)
		t.Setenv("FAKE_GOPATH", gopath)
		if err := ensureTool("templ", "templ installation hint"); err != nil {
			t.Fatalf("ensureTool() error = %v", err)
		}
	})

	t.Run("templ fallback through go fails", func(t *testing.T) {
		bin := t.TempDir()
		writeCLIScript(t, bin, "go", "#!/bin/sh\nexit 1\n")
		t.Setenv("PATH", bin)
		if err := runTempl(); err == nil {
			t.Fatal("runTempl() expected fallback command failure")
		}
	})

	t.Run("watch option with no watcher", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			original := setupGenerateWatcherFunc
			t.Cleanup(func() { setupGenerateWatcherFunc = original })
			setupGenerateWatcherFunc = func() *fsnotify.Watcher { return nil }
			RunGenerate([]string{"--watch"})
		})
	})

	t.Run("watch helper handles a real closed watcher", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			watcher, err := fsnotify.NewWatcher()
			if err != nil {
				t.Fatal(err)
			}
			original := setupGenerateWatcherFunc
			setupGenerateWatcherFunc = func() *fsnotify.Watcher { return watcher }
			t.Cleanup(func() { setupGenerateWatcherFunc = original })
			done := make(chan struct{})
			go func() {
				runGenerateWatch()
				close(done)
			}()
			if err := watcher.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("watch mode did not return after watcher closure")
			}
		})
	})

	t.Run("templ compilation error exits", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("index.templ", []byte("package views"), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			writeCLIScript(t, bin, "templ", "#!/bin/sh\nexit 1\n")
			t.Setenv("PATH", bin)
			codes := recordCLIExits(t, func() {
				_ = runGenerateOnce()
			})
			if fmt.Sprint(codes) != "[1]" {
				t.Fatalf("exit codes = %v, want [1]", codes)
			}
		})
	})
}

func TestGenerateWatcherSetupFailures(t *testing.T) {
	origNew, origWalk, origAdd := newGenerateWatcher, walkGenerateDirectories, addGenerateWatch
	t.Cleanup(func() {
		newGenerateWatcher, walkGenerateDirectories, addGenerateWatch = origNew, origWalk, origAdd
	})

	t.Run("watcher creation fails", func(t *testing.T) {
		newGenerateWatcher = func() (*fsnotify.Watcher, error) { return nil, errors.New("watcher unavailable") }
		codes := recordCLIExits(t, func() {
			if got := setupGenerateWatcher(); got != nil {
				t.Fatal("expected nil watcher after creation failure")
			}
		})
		if fmt.Sprint(codes) != "[1]" {
			t.Fatalf("exit codes = %v, want [1]", codes)
		}
	})

	t.Run("directory walk fails", func(t *testing.T) {
		newGenerateWatcher = fsnotify.NewWatcher
		walkGenerateDirectories = func(string, filepath.WalkFunc) error { return errors.New("walk failed") }
		codes := recordCLIExits(t, func() {
			if got := setupGenerateWatcher(); got != nil {
				t.Fatal("expected nil watcher after walk failure")
			}
		})
		if fmt.Sprint(codes) != "[1]" {
			t.Fatalf("exit codes = %v, want [1]", codes)
		}
	})

	t.Run("directory registration fails", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			newGenerateWatcher = fsnotify.NewWatcher
			walkGenerateDirectories = filepath.Walk
			addGenerateWatch = func(*fsnotify.Watcher, string) error { return errors.New("add failed") }
			codes := recordCLIExits(t, func() {
				if got := setupGenerateWatcher(); got != nil {
					t.Fatal("expected nil watcher after directory registration failure")
				}
			})
			if fmt.Sprint(codes) != "[1]" {
				t.Fatalf("exit codes = %v, want [1]", codes)
			}
		})
	})

	t.Run("walker callback errors are ignored", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			newGenerateWatcher = fsnotify.NewWatcher
			walkGenerateDirectories = func(_ string, fn filepath.WalkFunc) error {
				return fn(".", nil, errors.New("stat failed"))
			}
			watcher := setupGenerateWatcher()
			if watcher == nil {
				t.Fatal("expected watcher after an ignored callback error")
			}
			_ = watcher.Close()
		})
	})

	t.Run("non-directory entries are ignored", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			file := filepath.Join(".", "file.txt")
			if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			newGenerateWatcher = fsnotify.NewWatcher
			walkGenerateDirectories = func(_ string, fn filepath.WalkFunc) error {
				return fn(file, info, nil)
			}
			watcher := setupGenerateWatcher()
			if watcher == nil {
				t.Fatal("expected watcher after ignoring a file entry")
			}
			_ = watcher.Close()
		})
	})
}

func TestLoopGenerateWatcherClosedChannelsAndDirectoryEvents(t *testing.T) {
	t.Run("closed event channel", func(t *testing.T) {
		watcher := &fsnotify.Watcher{Events: make(chan fsnotify.Event), Errors: make(chan error)}
		close(watcher.Events)
		loopGenerateWatcher(watcher)
	})
	t.Run("watcher error channel", func(t *testing.T) {
		watcher := &fsnotify.Watcher{Events: make(chan fsnotify.Event), Errors: make(chan error, 1)}
		watcher.Errors <- errors.New("watch failed")
		close(watcher.Errors)
		loopGenerateWatcher(watcher)
	})
	for _, addErr := range []error{nil, errors.New("duplicate watch")} {
		t.Run(fmt.Sprintf("directory create add error=%v", addErr), func(t *testing.T) {
			withWorkingDir(t, t.TempDir(), func() {
				dir := filepath.Join(".", "new-directory")
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				original := addGenerateWatch
				addGenerateWatch = func(_ *fsnotify.Watcher, path string) error {
					if path != dir {
						t.Errorf("watch path = %q, want %q", path, dir)
					}
					return addErr
				}
				t.Cleanup(func() { addGenerateWatch = original })
				watcher := &fsnotify.Watcher{Events: make(chan fsnotify.Event, 1), Errors: make(chan error)}
				watcher.Events <- fsnotify.Event{Name: dir, Op: fsnotify.Create}
				close(watcher.Events)
				loopGenerateWatcher(watcher)
				time.Sleep(300 * time.Millisecond)
			})
		})
	}
}

func TestRunDevMakeFailures(t *testing.T) {
	t.Run("make cannot start", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("Makefile", []byte("dev:\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", t.TempDir())
			codes := recordCLIExits(t, func() { RunDev(nil) })
			if fmt.Sprint(codes) != "[1]" {
				t.Fatalf("exit codes = %v, want [1]", codes)
			}
		})
	})

	t.Run("make command fails", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("Makefile", []byte("dev:\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			writeCLIScript(t, bin, "make", "#!/bin/sh\nexit 7\n")
			t.Setenv("PATH", bin)
			codes := recordCLIExits(t, func() { RunDev(nil) })
			if fmt.Sprint(codes) != "[1]" {
				t.Fatalf("exit codes = %v, want [1]", codes)
			}
		})
	})

	t.Run("forwards received signals to make", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("Makefile", []byte("dev:\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			writeCLIScript(t, bin, "make", "#!/bin/sh\ntrap '' TERM\n/bin/sleep 1\n")
			t.Setenv("PATH", bin)
			originalNotify := notifyDevSignals
			notifyDevSignals = func(ch chan<- os.Signal, signals ...os.Signal) {
				_ = signals
				time.Sleep(100 * time.Millisecond)
				ch <- syscall.SIGTERM
			}
			t.Cleanup(func() { notifyDevSignals = originalNotify })
			RunDev(nil)
		})
	})
}

func TestInitSuccessAndGenerationFailure(t *testing.T) {
	t.Run("feature merge and grpc success output", func(t *testing.T) {
		dir := t.TempDir()
		withWorkingDir(t, dir, func() {
			output := captureStdout(t, func() {
				if err := RunInit([]string{"--name=app", "--preset=api", "--db=none", "--features=grpc"}); err != nil {
					t.Fatalf("RunInit() error = %v", err)
				}
			})
			if !strings.Contains(output, ":9090") {
				t.Fatalf("expected gRPC endpoint in success output, got %q", output)
			}
			if _, err := os.Stat(filepath.Join("app", "go.mod")); err != nil {
				t.Fatalf("expected generated project: %v", err)
			}
		})
	})

	t.Run("generation reports an existing destination", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.Mkdir("taken", 0o755); err != nil {
				t.Fatal(err)
			}
			err := RunInit([]string{"--name=taken", "--preset=api"})
			if err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("RunInit() error = %v, want already-exists", err)
			}
		})
	})
}

func TestRoutesCommandAndScannerEdges(t *testing.T) {
	t.Run("routes command without module", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			output := captureStdout(t, func() { RunRoutes(nil) })
			if !strings.Contains(output, "go.mod not found") {
				t.Fatalf("unexpected output: %q", output)
			}
		})
	})

	t.Run("routes command reports scan failure", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("go.mod", []byte("module routes.test\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			original := scanRoutesInCurrentProject
			scanRoutesInCurrentProject = func(string) ([]routeEntry, error) { return nil, errors.New("scan failed") }
			t.Cleanup(func() { scanRoutesInCurrentProject = original })
			output := captureStdout(t, func() { RunRoutes(nil) })
			if !strings.Contains(output, "Could not scan routes") {
				t.Fatalf("unexpected output: %q", output)
			}
		})
	})

	t.Run("routes sort and print methods and legacy routes", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("go.mod", []byte("module routes.test\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			src := `package handlers
func register(server *Server) {
	server.Delete("/long/path")
	server.Get("/long/path")
	server.Handle(http.MethodPatch, "/patch", handler)
	mux.Handle("/legacy", handler)
	mux.Handle("OPTIONS /alternate", handler)
}`
			if err := os.WriteFile("routes.go", []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("routes_test.go", []byte(`server.Get("/test-only")`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("notes.txt", []byte(`server.Get("/not-go")`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(".git", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(".git", "ignored.go"), []byte(`server.Get("/ignored")`), 0o644); err != nil {
				t.Fatal(err)
			}
			output := captureStdout(t, func() { RunRoutes(nil) })
			for _, want := range []string{"DELETE", "GET", "PATCH", "ANY", "OPTIONS", "/long/path", "/legacy", "/alternate"} {
				if !strings.Contains(output, want) {
					t.Errorf("routes output does not contain %q: %s", want, output)
				}
			}
			for _, excluded := range []string{"/test-only", "/not-go", "/ignored"} {
				if strings.Contains(output, excluded) {
					t.Errorf("routes output unexpectedly contains %q: %s", excluded, output)
				}
			}
		})
	})

	t.Run("scanner ignores callback and file read errors", func(t *testing.T) {
		originalWalk, originalRead := walkRouteSources, readRouteSource
		t.Cleanup(func() { walkRouteSources, readRouteSource = originalWalk, originalRead })
		walkRouteSources = func(_ string, fn filepath.WalkFunc) error {
			if err := fn("route.go", nil, errors.New("stat denied")); err != nil {
				return err
			}
			return nil
		}
		routes, err := scanRoutes(".")
		if err != nil || len(routes) != 0 {
			t.Fatalf("scanRoutes() = (%v, %v), want empty result", routes, err)
		}

		walkRouteSources = filepath.Walk
		readRouteSource = func(string) ([]byte, error) { return nil, errors.New("read denied") }
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "routes.go"), []byte("package handlers"), 0o644); err != nil {
			t.Fatal(err)
		}
		routes, err = scanRoutes(dir)
		if err != nil || len(routes) != 0 {
			t.Fatalf("scanRoutes() = (%v, %v), want empty result", routes, err)
		}
	})

	t.Run("missing scan root returns filesystem error", func(t *testing.T) {
		routes, err := scanRoutes(filepath.Join(t.TempDir(), "missing"))
		if err != nil || len(routes) != 0 {
			t.Fatalf("scanRoutes() = (%v, %v), want empty result", routes, err)
		}
	})
}

func TestWatchPathFiltersEmptyAndIgnoredPaths(t *testing.T) {
	if !shouldSkipWatchDir("") {
		t.Fatal("empty watch directory should be skipped")
	}
	if !isIgnoredWatchPath("") {
		t.Fatal("empty watch path should be ignored")
	}
	if shouldSkipWatchDir("src") || isIgnoredWatchPath("src/main.go") {
		t.Fatal("ordinary source paths should remain watchable")
	}
}

func TestExecuteGenerateAndBuildDispatch(t *testing.T) {
	withWorkingDir(t, t.TempDir(), func() {
		exitCode := runWithExecuteStub(t, []string{"fgoths", "generate"}, func() {
			output := captureStdout(t, Execute)
			if !strings.Contains(output, "Nothing to generate") {
				t.Fatalf("expected generate dispatch output, got %q", output)
			}
		})
		if exitCode != 0 {
			t.Fatalf("generate dispatch exit code = %d, want 0", exitCode)
		}
	})

	withWorkingDir(t, t.TempDir(), func() {
		bin := t.TempDir()
		writeCLIScript(t, bin, "go", "#!/bin/sh\nexit 1\n")
		t.Setenv("PATH", bin)
		exitCode := runWithExecuteStub(t, []string{"fgoths", "build"}, func() { _ = captureStdout(t, Execute) })
		if exitCode != 1 {
			t.Fatalf("build dispatch exit code = %d, want 1", exitCode)
		}
	})
}

func TestCRUDWriterErrorPathsAndTemplateRendering(t *testing.T) {
	data, err := parseCRUDArgs([]string{"User", "name:string"})
	if err != nil {
		t.Fatal(err)
	}
	data.ProjectName = "example.com/app"

	t.Run("stat error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("models", []byte("not a directory"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := writeCRUDFiles(data); err == nil {
				t.Fatal("writeCRUDFiles() expected a non-not-exist stat error")
			}
		})
	})

	t.Run("handler overwrite", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.Mkdir("handlers", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join("handlers", "user_handler.go"), []byte("existing"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := writeCRUDFiles(data); err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
				t.Fatalf("writeCRUDFiles() error = %v", err)
			}
		})
	})

	t.Run("directory creation error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.WriteFile("handlers", []byte("not a directory"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := writeCRUDFiles(data); err == nil {
				t.Fatal("writeCRUDFiles() expected mkdir error")
			}
		})
	})

	t.Run("write error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			original := osWriteFile
			osWriteFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
			t.Cleanup(func() { osWriteFile = original })
			if err := writeCRUDFiles(data); err == nil || !strings.Contains(err.Error(), "write") {
				t.Fatalf("writeCRUDFiles() error = %v", err)
			}
		})
	})

	t.Run("handler render error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			original := renderCRUDSource
			renderCRUDSource = func(string, crudData) ([]byte, error) { return nil, errors.New("handler render failed") }
			t.Cleanup(func() { renderCRUDSource = original })
			if err := writeCRUDFiles(data); err == nil || !strings.Contains(err.Error(), "render") {
				t.Fatalf("writeCRUDFiles() error = %v", err)
			}
		})
	})

	t.Run("model render error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			original, calls := renderCRUDSource, 0
			renderCRUDSource = func(source string, data crudData) ([]byte, error) {
				calls++
				if calls <= 2 {
					return original(source, data)
				}
				return nil, errors.New("model render failed")
			}
			t.Cleanup(func() { renderCRUDSource = original })
			if err := writeCRUDFiles(data); err == nil || !strings.Contains(err.Error(), "render") {
				t.Fatalf("writeCRUDFiles() error = %v", err)
			}
		})
	})

	t.Run("model write error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			original, calls := osWriteFile, 0
			osWriteFile = func(path string, data []byte, mode os.FileMode) error {
				calls++
				if calls <= 2 {
					return original(path, data, mode)
				}
				return errors.New("model write failed")
			}
			t.Cleanup(func() { osWriteFile = original })
			if err := writeCRUDFiles(data); err == nil || !strings.Contains(err.Error(), "write") {
				t.Fatalf("writeCRUDFiles() error = %v", err)
			}
		})
	})

	t.Run("route registry errors and filtering", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := writeCRUDRouteRegistry("example.com/app"); err == nil || !strings.Contains(err.Error(), "scan handlers") {
				t.Fatalf("writeCRUDRouteRegistry() error = %v", err)
			}
			if err := os.Mkdir("handlers", 0o755); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"alpha_handler.go", "beta_handler.go", "beta_handler_test.go", "handlers.go", "routes_gen.go"} {
				if err := os.WriteFile(filepath.Join("handlers", name), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Mkdir(filepath.Join("handlers", "nested_handler.go"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := writeCRUDRouteRegistry("example.com/app"); err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(filepath.Join("handlers", "routes_gen.go"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(content), "registerAlphaRoutes") || !strings.Contains(string(content), "registerBetaRoutes") || strings.Contains(string(content), "registerHandlersRoutes") {
				t.Fatalf("unexpected route registry: %s", content)
			}
		})
	})

	t.Run("route registry render errors", func(t *testing.T) {
		if _, err := renderCRUDRouteRegistry("{{", "example.com/app", nil); err == nil {
			t.Fatal("expected route registry parse error")
		}
		if _, err := renderCRUDRouteRegistry("{{index . 2}}", "example.com/app", []string{"Only"}); err == nil {
			t.Fatal("expected route registry execution error")
		}
		if _, err := renderCRUDTemplate("{{", data); err == nil {
			t.Fatal("expected CRUD template parse error")
		}
		if _, err := renderCRUDTemplate("{{.Missing.Value}}", data); err == nil {
			t.Fatal("expected CRUD template execution error")
		}
		if got := exportName("two__words_"); got != "TwoWords" {
			t.Fatalf("exportName() = %q, want TwoWords", got)
		}
	})

	t.Run("registry write error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.Mkdir("handlers", 0o755); err != nil {
				t.Fatal(err)
			}
			original := osWriteFile
			osWriteFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }
			t.Cleanup(func() { osWriteFile = original })
			if err := writeCRUDRouteRegistry("example.com/app"); err == nil {
				t.Fatal("writeCRUDRouteRegistry() expected write error")
			}
		})
	})

	t.Run("route registry render error", func(t *testing.T) {
		withWorkingDir(t, t.TempDir(), func() {
			if err := os.Mkdir("handlers", 0o755); err != nil {
				t.Fatal(err)
			}
			original := renderCRUDRegistrySource
			renderCRUDRegistrySource = func(string, string, []string) ([]byte, error) {
				return nil, errors.New("registry render failed")
			}
			t.Cleanup(func() { renderCRUDRegistrySource = original })
			if err := writeCRUDRouteRegistry("example.com/app"); err == nil || !strings.Contains(err.Error(), "registry render failed") {
				t.Fatalf("writeCRUDRouteRegistry() error = %v", err)
			}
		})
	})
}

func TestRunGenerateCrudDispatch(t *testing.T) {
	withWorkingDir(t, t.TempDir(), func() {
		setupSQLiteMVCProject(t)
		RunGenerate([]string{"crud", "User", "name:string"})
		if _, err := os.Stat(filepath.Join("models", "user.go")); err != nil {
			t.Fatalf("generate CRUD dispatch did not create model: %v", err)
		}
	})
}

func TestSyncTemplateReadAndRepoRootFailures(t *testing.T) {
	originalCaller := runtimeCaller
	originalFiles, originalSource, originalDest, originalRoot := runtimeSyncFiles, runtimeSourceDir, templateDestDir, runtimeRepoRootOverride
	t.Cleanup(func() {
		runtimeCaller = originalCaller
		runtimeSyncFiles, runtimeSourceDir, templateDestDir, runtimeRepoRootOverride = originalFiles, originalSource, originalDest, originalRoot
	})

	runtimeRepoRootOverride = ""
	if _, file, line, ok := runtimeCaller(0); !ok || file == "" || line == 0 {
		t.Fatalf("runtimeCaller(0) = (%q, %d, %v), want a source location", file, line, ok)
	}
	runtimeCaller = func(int) (uintptr, string, int, bool) { return 0, "", 0, false }
	if got := repoRoot(); got != "." {
		t.Fatalf("repoRoot() = %q, want dot", got)
	}

	root := t.TempDir()
	runtimeRepoRootOverride = root
	runtimeSyncFiles = map[string]string{"one.go": "one.go.tpl"}
	runtimeSourceDir, templateDestDir = "src", "dst"
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "dst"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "one.go"), []byte("package one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dst", "one.go.tpl"), []byte("package old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	originalRead, originalNotExist := osReadFile, osIsNotExist
	osReadFile = func(path string) ([]byte, error) {
		if strings.HasSuffix(path, "one.go.tpl") {
			return nil, errors.New("read denied")
		}
		return originalRead(path)
	}
	osIsNotExist = func(error) bool { return false }
	t.Cleanup(func() { osReadFile, osIsNotExist = originalRead, originalNotExist })
	if _, err := syncRuntimeTemplates(true); err == nil || !strings.Contains(err.Error(), "read denied") {
		t.Fatalf("syncRuntimeTemplates() error = %v", err)
	}
}

func recordCLIExits(t *testing.T, fn func()) []int {
	t.Helper()
	original := osExit
	var codes []int
	osExit = func(code int) { codes = append(codes, code) }
	t.Cleanup(func() { osExit = original })
	fn()
	return codes
}

func writeCLIScript(t *testing.T, dir, name, contents string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}
