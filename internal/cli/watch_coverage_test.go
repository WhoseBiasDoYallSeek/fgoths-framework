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
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestSetupGenerateWatcherWatchesProjectTree(t *testing.T) {
	dir := t.TempDir()
	withWorkingDir(t, dir, func() {
		// A project-shaped tree: views/ is watched, bin/ is skipped.
		for _, d := range []string{"views", "handlers", "bin", ".git"} {
			if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}

		output := captureStdout(t, func() {
			w := setupGenerateWatcher()
			if w == nil {
				t.Fatal("expected a configured watcher")
			}
			w.Close()
		})

		if !strings.Contains(output, "Watching") {
			t.Errorf("expected watch setup output, got %q", output)
		}
		if !strings.Contains(output, "Watch mode active") {
			t.Errorf("setup should run the initial generate and print the banner, got %q", output)
		}
	})
}

func TestLoopGenerateWatcherReturnsOnClosedWatcher(t *testing.T) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	// Closing the watcher closes its event channels, so the loop must return
	// on its own instead of blocking forever.
	w.Close()

	done := make(chan struct{})
	go func() {
		captureStdout(t, func() { loopGenerateWatcher(w) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loopGenerateWatcher did not return after the watcher closed")
	}
}

func TestLoopGenerateWatcherDebouncesEvents(t *testing.T) {
	dir := t.TempDir()
	templ := filepath.Join(dir, "index.templ")
	if err := os.WriteFile(templ, []byte("package views\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Add(dir); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		captureStdout(t, func() { loopGenerateWatcher(w) })
		close(done)
	}()

	// Two rapid writes must coalesce into one debounced regeneration.
	for i := 0; i < 2; i++ {
		if err := os.WriteFile(templ, []byte("package views\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	// The loop keeps running; give the debounce window time to fire, then
	// close the watcher to end the loop.
	time.Sleep(400 * time.Millisecond)
	w.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loopGenerateWatcher did not return after the watcher closed")
	}
}
