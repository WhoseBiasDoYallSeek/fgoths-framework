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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// RunGenerate compiles Templ templates for the current project. It also
// dispatches `generate crud` (below).
func RunGenerate(args []string) {
	if len(args) > 0 && args[0] == "crud" {
		RunGenerateCRUD(args[1:])
		return
	}

	fs := flag.NewFlagSet("generate", flag.ExitOnError)
	watch := fs.Bool("watch", false, "Watch for file changes and auto-regenerate")
	_ = fs.Parse(args)
	if *watch {
		runGenerateWatch()
		return
	}

	runGenerateOnce()
}

func runGenerateOnce() bool {
	ran := false

	// Templ: compile *.templ in the project root and views/.
	templFiles, _ := filepath.Glob("*.templ")
	viewTempl, _ := filepath.Glob("views/*.templ")
	if len(templFiles)+len(viewTempl) > 0 {
		if err := ensureTool("templ", "go install github.com/a-h/templ/cmd/templ@latest"); err != nil {
			fmt.Printf("templ generate failed: %v\n", err)
			osExit(1)
		}
		if err := runTempl(); err != nil {
			fmt.Printf("templ generate failed: %v\n", err)
			osExit(1)
		}
		ran = true
		fmt.Println("Compiled Templ templates")
	}

	if !ran {
		fmt.Println("Nothing to generate (no .templ files in this directory).")
		return false
	}

	fmt.Println("Generation complete.")
	return true
}

func resolveToolPath(toolName string) (string, error) {
	if path, err := exec.LookPath(toolName); err == nil {
		return path, nil
	}
	if toolName == "templ" {
		if out, err := exec.Command("go", "env", "GOPATH").Output(); err == nil {
			candidate := filepath.Join(strings.TrimSpace(string(out)), "bin", toolName)
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("%s not found", toolName)
}

func ensureTool(toolName, installHint string) error {
	if _, err := resolveToolPath(toolName); err == nil {
		return nil
	}

	switch toolName {
	case "templ":
		if _, err := exec.LookPath("go"); err == nil {
			cmd := exec.Command("go", "install", "github.com/a-h/templ/cmd/templ@latest")
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err == nil {
				if _, err := resolveToolPath(toolName); err == nil {
					return nil
				}
			}
		}
		return fmt.Errorf("%s not found; install it with: %s", toolName, installHint)
	}
	return fmt.Errorf("%s not found; install with: %s", toolName, installHint)
}

func runTempl() error {
	if path, err := resolveToolPath("templ"); err == nil {
		return run(path, "generate")
	}
	if _, err := exec.LookPath("go"); err == nil {
		cmd := exec.Command("go", "run", "github.com/a-h/templ/cmd/templ@latest", "generate")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	return fmt.Errorf("templ not installed; install with: go install github.com/a-h/templ/cmd/templ@latest")
}

// runGenerateWatch watches the project for changes and re-runs generate with
// a short debounce so the generator doesn't recurse into its own output files.
func runGenerateWatch() {
	watcher := setupGenerateWatcher()
	if watcher == nil {
		return
	}
	defer func() { _ = watcher.Close() }()
	loopGenerateWatcher(watcher)
}

// setupGenerateWatcher builds and configures the fsnotify watcher for watch
// mode, returning nil after reporting a fatal setup error.
func setupGenerateWatcher() *fsnotify.Watcher {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("watch mode failed to initialize: %v\n", err)
		osExit(1)
		return nil
	}

	var walkMu sync.Mutex
	var watchedDirs []string
	if err := filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}
		if shouldSkipWatchDir(path) {
			return filepath.SkipDir
		}
		if err := watcher.Add(path); err != nil {
			return err
		}
		walkMu.Lock()
		watchedDirs = append(watchedDirs, path)
		walkMu.Unlock()
		return nil
	}); err != nil {
		_ = watcher.Close()
		fmt.Printf("watch setup failed: %v\n", err)
		osExit(1)
		return nil
	}

	if len(watchedDirs) == 0 {
		fmt.Println("Watching current directory for changes...")
	} else {
		fmt.Printf("Watching %d directories for changes...\n", len(watchedDirs))
	}

	runGenerateOnce()
	fmt.Println("Watch mode active. Press Ctrl+C to stop.")
	return watcher
}

// loopGenerateWatcher consumes watcher events until the watcher closes,
// debouncing regeneration runs.
func loopGenerateWatcher(watcher *fsnotify.Watcher) {
	var (
		regenMu    sync.Mutex
		regenTimer *time.Timer
	)
	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !shouldHandleWatchEvent(event.Name, event.Op) {
				continue
			}

			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := watcher.Add(event.Name); err == nil {
						fmt.Printf("Watching new directory: %s\n", event.Name)
					}
				}
			}

			regenMu.Lock()
			if regenTimer != nil {
				regenTimer.Stop()
			}
			fmt.Printf("Change detected (%s), regenerating...\n", event.Op)
			regenTimer = time.AfterFunc(250*time.Millisecond, func() {
				regenMu.Lock()
				regenTimer = nil
				regenMu.Unlock()
				runGenerateOnce()
			})
			regenMu.Unlock()
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("watch error: %v\n", err)
		}
	}
}

// RunDev delegates to `make dev` in the current directory. Generated
// projects already ship their own hot-reload watcher (cmd/dev) with
// build-ahead swaps and fragment-level HMR — reimplementing a second,
// simpler watcher here would just be a worse duplicate that's easy to
// accidentally reach for instead of the real one.
func RunDev(args []string) {
	if len(args) > 0 {
		fmt.Printf("dev does not accept positional arguments: %v\n", args)
		osExit(1)
		return
	}
	if _, err := os.Stat("Makefile"); err != nil {
		fmt.Println("dev requires a generated FGOTHS project (no Makefile found in the current directory)")
		osExit(1)
		return
	}

	cmd := exec.Command("make", "dev")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		fmt.Printf("failed to start make dev: %v\n", err)
		osExit(1)
		return
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		for sig := range sigCh {
			_ = cmd.Process.Signal(sig)
		}
	}()

	if err := cmd.Wait(); err != nil {
		osExit(1)
	}
}

func shouldSkipWatchDir(path string) bool {
	if path == "" {
		return true
	}
	clean := filepath.Clean(path)
	for _, segment := range []string{".git", "bin", "node_modules", "vendor", "dist", "coverage", "schemas/generated"} {
		if clean == segment || strings.HasSuffix(clean, "/"+segment) || strings.Contains(clean, "/"+segment+"/") {
			return true
		}
	}
	return false
}

func shouldHandleWatchEvent(path string, op fsnotify.Op) bool {
	if path == "" || op == 0 {
		return false
	}
	if isIgnoredWatchPath(path) {
		return false
	}
	if op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	clean := filepath.Clean(path)
	if strings.HasSuffix(clean, ".swp") || strings.HasSuffix(clean, ".tmp") || strings.HasSuffix(clean, "~") {
		return false
	}
	if info, err := os.Stat(clean); err == nil && info.IsDir() {
		return true
	}
	ext := strings.ToLower(filepath.Ext(clean))
	switch ext {
	case ".go", ".templ", ".fbs", ".css", ".js", ".ts", ".tsx", ".html", ".yaml", ".yml", ".json", ".sql", ".md", ".txt":
		return true
	}
	return strings.HasSuffix(clean, "/go.mod") || strings.HasSuffix(clean, "/Makefile") || strings.HasSuffix(clean, "/Dockerfile")
}

func isIgnoredWatchPath(path string) bool {
	if path == "" {
		return true
	}
	clean := filepath.Clean(path)
	if strings.HasPrefix(clean, ".git/") || clean == ".git" {
		return true
	}
	for _, segment := range []string{"bin", "node_modules", "vendor", "dist", "coverage", "schemas/generated"} {
		if clean == segment || strings.HasPrefix(clean, segment+string(os.PathSeparator)) || strings.Contains(clean, string(os.PathSeparator)+segment+string(os.PathSeparator)) {
			return true
		}
	}
	return false
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
