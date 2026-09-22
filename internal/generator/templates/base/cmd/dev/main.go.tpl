// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Dev mode: watches project files and keeps the app hot.
//
// Change routing:
//   - .css/.js/.html      → asset served straight from disk; no restart
//   - .go/.templ/.fbs     → regenerate outputs (templ/fbs only), build a
//     new binary into a temp path WHILE THE OLD PROCESS KEEPS SERVING,
//     then swap: stop the old process and start the freshly built one.
//     Go is compiled, so new view/handler code only takes effect after a
//     fresh binary — building ahead of the swap keeps the downtime to the
//     process-restart cost instead of the full compile time.
//
// Once the new process answers again, a views/<name>.templ change (other
// than layout.templ) fetches that view's fresh HTML and pushes it as a
// "fragment" event — the browser patches #fgoths-content in place instead
// of navigating away. Anything else (Go code, layout.templ) broadcasts a
// full "reload" instead, since its effect isn't scoped to one view.
//
// Uses pure Go (fsnotify) with a polling fallback for filesystems where
// inotify events are unreliable (e.g. network or external volumes).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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

const watchDebounce = 150 * time.Millisecond

var (
	skipDirs = map[string]bool{
		"bin":          true,
		"tmp":          true,
		".git":         true,
		"vendor":       true,
		"node_modules": true,
	}
	watchExts = map[string]bool{
		".go":    true,
		".templ": true,
		".html":  true,
		".tpl":   true,
		".css":   true,
		".js":    true,
		".fbs":   true,
	}
{{if .IsMVC}}
	buildTarget = "."
{{else}}
	buildTarget = "./cmd/app"
{{end}}
	appURL = "http://localhost:" + envOr("PORT", "8080")
)

func main() {
	fmt.Printf("🔄 {{.ProjectName}} dev mode — watching for changes...\n\n")

	if err := runGenerate(); err != nil {
		fmt.Printf("⚠️  initial generate failed: %v\n", err)
	}

	binPath, buildDur, err := buildServer()
	if err != nil {
		fmt.Printf("❌ initial build failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("   🏗️  initial build ok in %s\n", durationStr(buildDur))

	initialBuildID := newBuildID()
	proc, err := startServer(binPath, initialBuildID, readyFileFor(initialBuildID))
	if err != nil {
		fmt.Printf("   ❌ failed to start server: %v\n", err)
		os.Exit(1)
	}
	setupWatcher(proc, binPath)
}

// durationStr renders a duration compactly for the dev status lines
// (e.g. "842ms", "1.3s"), avoiding long float representations.
func durationStr(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

func runGenerate() error {
	fmt.Println("   🔁 running make generate-assets...")
	cmd := exec.Command("make", "generate-assets")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func newBuildID() string {
	return fmt.Sprintf("dev-%d", time.Now().UnixNano())
}

// buildServer compiles the app into a fresh temp binary without touching
// the one currently serving requests, so the old process stays up for the
// full duration of the (slow) compile step. Returns the binary path and
// how long the compile took (for the dev-loop status line).
func buildServer() (string, time.Duration, error) {
	start := time.Now()
	binPath := filepath.Join(os.TempDir(), fmt.Sprintf("fgoths-dev-%d-%d", os.Getpid(), time.Now().UnixNano()))
	cmd := exec.Command("go", "build", "-o", binPath, buildTarget)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", 0, err
	}
	return binPath, time.Since(start), nil
}

func startServer(binPath, buildID, readyFile string) (*exec.Cmd, error) {
	fmt.Println("   🚀 starting server...")
	cmd := exec.Command(binPath)
	_ = os.Remove(readyFile)
	cmd.Env = append(os.Environ(), "FGOTHS_DEV=1", "FGOTHS_BUILD_ID="+buildID, "FGOTHS_READY_FILE="+readyFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Own process group so a restart can kill the whole process tree and
	// free the port immediately.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func readyFileFor(buildID string) string {
	return filepath.Join(os.TempDir(), "fgoths-ready-"+buildID)
}

func stopServer(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
}

// waitForBuild waits until the newly started process proves it opened its
// listener by writing its build ID into FGOTHS_READY_FILE. This avoids the
// SO_REUSEPORT ambiguity where HTTP probes can keep landing on the old process.
func waitForBuild(readyFile, buildID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(readyFile)
		if err == nil && string(data) == buildID {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

// fragmentContainerID is the element the client patches in place instead
// of navigating away. Must match the id layout.templ.tpl wraps children
// in.
const fragmentContainerID = `id="fgoths-content"`

// routeForView maps a views/*.templ source path to the URL route it
// renders, when that mapping is unambiguous. layout.templ affects every
// page's chrome (header/nav/footer, outside #fgoths-content) so it is
// deliberately excluded; anything outside views/ can touch arbitrary code
// paths. Both fall back to a full reload.
func routeForView(sourcePath string) (string, bool) {
	clean := filepath.ToSlash(filepath.Clean(sourcePath))
	if !strings.HasPrefix(clean, "views/") || !strings.HasSuffix(clean, ".templ") {
		return "", false
	}
	name := strings.TrimSuffix(strings.TrimPrefix(clean, "views/"), ".templ")
	if name == "layout" || strings.Contains(name, "/") {
		return "", false
	}
	if name == "index" {
		return "/", true
	}
	return "/" + name, true
}

// fetchFragment renders route on the (already restarted) app and extracts
// the #fgoths-content payload, so the browser can patch just that element
// instead of reloading the whole page.
func fetchFragment(route string) (string, bool) {
	resp, err := http.Get(appURL + route)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false
	}
	return extractFragment(string(body))
}

// extractFragment returns the inner HTML of the first element carrying
// fragmentContainerID, tracking nested <div>...</div> pairs by hand (the
// generated HTML is well-formed, so this avoids pulling in an HTML parser
// dependency just for dev tooling).
func extractFragment(html string) (string, bool) {
	idx := strings.Index(html, fragmentContainerID)
	if idx == -1 {
		return "", false
	}
	tagStart := strings.LastIndex(html[:idx], "<")
	if tagStart == -1 {
		return "", false
	}
	tagEnd := strings.Index(html[tagStart:], ">")
	if tagEnd == -1 {
		return "", false
	}
	pos := tagStart + tagEnd + 1
	contentStart := pos

	depth := 1
	for depth > 0 {
		nextOpen := strings.Index(html[pos:], "<div")
		nextClose := strings.Index(html[pos:], "</div>")
		if nextClose == -1 {
			return "", false
		}
		if nextOpen != -1 && nextOpen < nextClose {
			depth++
			pos += nextOpen + len("<div")
		} else {
			depth--
			pos += nextClose + len("</div>")
		}
	}
	return html[contentStart : pos-len("</div>")], true
}

// broadcastMessage is the JSON body POSTed to /hmr/broadcast, mirrored by
// the server-side struct of the same name in pkg/runtime/hmr_server.go.
type broadcastMessage struct {
	Kind    string `json:"kind"`
	Target  string `json:"target,omitempty"`
	Payload string `json:"payload,omitempty"`
	Source  string `json:"source,omitempty"`
	Route   string `json:"route,omitempty"`
}

func postBroadcast(msg broadcastMessage) {
	body, err := json.Marshal(msg)
	if err != nil {
		fmt.Printf("   ⚠️  hmr broadcast encode failed: %v\n", err)
		return
	}
	resp, err := http.Post(appURL+"/hmr/broadcast", "application/json", bytes.NewReader(body))
	if err != nil {
		fmt.Printf("   ⚠️  hmr broadcast unavailable (%v)\n", err)
		return
	}
	resp.Body.Close()
}

func setupWatcher(proc *exec.Cmd, binPath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Printf("   ❌ fsnotify init failed: %v\n", err)
		os.Exit(1)
	}
	defer watcher.Close()
	// fsnotify is the primary signal; this ticker is only a fallback for
	// filesystems where inotify events are unreliable (network volumes).
	// 300ms keeps fallback latency low without measurable CPU cost.
	pollTicker := time.NewTicker(300 * time.Millisecond)
	defer pollTicker.Stop()

	watchTree(watcher)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	var (
		mu           sync.Mutex
		restartTimer *time.Timer
		busy         bool
		pending      bool
		pendingSrc   string
		pendingGen   bool
		snapshot     = snapshotWatchedFiles()
	)

	fmt.Println("   📺 Press Ctrl+C to stop")

	// broadcastReload tells connected browsers to fully reload — used
	// when a change's effect isn't safely scoped to a single view (Go
	// code, layout.templ, or anything routeForView can't map).
	broadcastReload := func(source string) {
		postBroadcast(broadcastMessage{Kind: "reload", Source: source})
	}

	// broadcastFragment ships a freshly rendered view's HTML so the
	// browser can patch #fgoths-content in place instead of navigating
	// away — no lost scroll position, no full-page flash.
	broadcastFragment := func(route, html, source string) {
		postBroadcast(broadcastMessage{Kind: "fragment", Target: "#fgoths-content", Payload: html, Route: route, Source: source})
	}

	// runCycle regenerates (if needed) and builds a new binary WHILE THE
	// CURRENT PROCESS KEEPS SERVING, then swaps: start new, wait until it
	// accepts on the shared dev port (SO_REUSEPORT), then stop old. Only one cycle runs
	// at a time; a change that arrives mid-cycle is coalesced into a single
	// follow-up cycle instead of racing a second build/swap.
	var runCycle func(source string, needGenerate bool)
	runCycle = func(source string, needGenerate bool) {
		if needGenerate {
			if err := runGenerate(); err != nil {
				fmt.Printf("   ⚠️  generate failed: %v\n", err)
				mu.Lock()
				busy = false
				mu.Unlock()
				return
			}
		}

		newBin, buildDur, err := buildServer()
		if err != nil {
			fmt.Printf("   ❌ build failed: %v\n", err)
			mu.Lock()
			busy = false
			mu.Unlock()
			return
		}

		cycleStart := time.Now()
		oldBin := binPath
		oldProc := proc
		buildID := newBuildID()
		readyFile := readyFileFor(buildID)
		newProc, err := startServer(newBin, buildID, readyFile)
		if err != nil {
			fmt.Printf("   ❌ failed to start rebuilt server: %v (keeping previous server alive)\n", err)
			_ = os.Remove(readyFile)
			_ = os.Remove(newBin)
			mu.Lock()
			busy = false
			mu.Unlock()
			return
		}

		if waitForBuild(readyFile, buildID, 3*time.Second) {
			_ = os.Remove(readyFile)
			proc = newProc
			binPath = newBin
			stopServer(oldProc)
			_ = os.Remove(oldBin)
			fmt.Printf("   ⚡ rebuilt %s · swap %s · app ready\n",
				durationStr(buildDur), durationStr(time.Since(cycleStart)))
			if route, ok := routeForView(source); ok {
				if html, ok := fetchFragment(route); ok {
					fmt.Printf("   🧩 fragment patch: %s -> %s (%d bytes)\n", route, fragmentContainerID, len(html))
					broadcastFragment(route, html, filepath.Base(source))
				} else {
					fmt.Printf("   ⚠️  could not extract fragment for %s; falling back to full reload\n", route)
					broadcastReload(filepath.Base(source))
				}
			} else {
				fmt.Println("   🔄 full reload (change not scoped to a single view)")
				broadcastReload(filepath.Base(source))
			}
		} else {
			stopServer(newProc)
			_ = os.Remove(readyFile)
			_ = os.Remove(newBin)
			fmt.Println("   ⚠️  rebuilt server did not become ready; keeping previous server alive")
		}

		mu.Lock()
		if pending {
			pending = false
			nextSrc, nextGen := pendingSrc, pendingGen
			mu.Unlock()
			runCycle(nextSrc, nextGen)
			return
		}
		busy = false
		mu.Unlock()
	}

	scheduleChange := func(changed string, needGenerate bool) {
		mu.Lock()
		defer mu.Unlock()
		if busy {
			pending = true
			pendingSrc = changed
			pendingGen = pendingGen || needGenerate
			fmt.Printf("   ⏳ change queued (build in progress): %s\n", filepath.Base(changed))
			return
		}
		if restartTimer != nil {
			restartTimer.Stop()
		}
		fmt.Printf("   ✏️  change (building): %s\n", filepath.Base(changed))
		restartTimer = time.AfterFunc(watchDebounce, func() {
			mu.Lock()
			restartTimer = nil
			busy = true
			mu.Unlock()
			runCycle(changed, needGenerate)
		})
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !shouldHandle(event) {
				continue
			}
			ext := filepath.Ext(event.Name)
			if ext == ".css" || ext == ".js" || ext == ".html" {
				fmt.Printf("   🎨 asset changed: %s (refresh the browser to see it)\n", filepath.Base(event.Name))
				continue
			}
			scheduleChange(event.Name, ext == ".templ" || ext == ".fbs")

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Printf("   ⚠️  watch error: %v\n", err)

		case <-pollTicker.C:
			current := snapshotWatchedFiles()
			changed, needGenerate := detectSnapshotChanges(snapshot, current)
			snapshot = current
			if len(changed) == 0 {
				continue
			}
			scheduleChange(changed[0], needGenerate)

		case sig := <-sigCh:
			fmt.Printf("\n   👋 received %v, stopping...\n", sig)
			stopServer(proc)
			_ = os.Remove(binPath)
			return
		}
	}
}

// watchTree registers fsnotify watchers over the project tree.
func watchTree(watcher *fsnotify.Watcher) {
	_ = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		clean := filepath.Clean(path)
		base := filepath.Base(clean)
		if base != "." && (base == ".git" || strings.HasPrefix(base, ".")) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if skipDirs[base] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			_ = watcher.Add(path)
		} else if watchExts[filepath.Ext(clean)] && !isGenerated(clean) {
			_ = watcher.Add(clean)
		}
		return nil
	})
}

func shouldHandle(event fsnotify.Event) bool {
	if event.Name == "" || event.Op == 0 {
		return false
	}
	if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	clean := filepath.Clean(event.Name)
	base := filepath.Base(clean)
	if base == "" || base[0] == '.' || base[0] == '#' {
		return false
	}
	if strings.HasSuffix(base, ".swp") || strings.HasSuffix(base, "~") {
		return false
	}
	if isGenerated(clean) {
		return false
	}
	if !watchExts[filepath.Ext(clean)] {
		info, err := os.Stat(clean)
		if err != nil || !info.IsDir() {
			return false
		}
	}
	return true
}

// isGenerated reports whether the path is produced by code generation
// (Templ outputs, FlatBuffers outputs). Watching those would create
// restart loops, since generation is itself triggered by watch events.
func isGenerated(clean string) bool {
	return strings.HasSuffix(clean, "_templ.go") ||
		strings.Contains(clean, "schemas/generated")
}

type fileStamp struct {
	modUnixNano int64
	size        int64
}

func snapshotWatchedFiles() map[string]fileStamp {
	snapshot := make(map[string]fileStamp)
	_ = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil {
			return nil
		}
		clean := filepath.Clean(path)
		base := filepath.Base(clean)
		if base != "." && (base == ".git" || strings.HasPrefix(base, ".")) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if skipDirs[base] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if !watchExts[filepath.Ext(clean)] || isGenerated(clean) {
			return nil
		}
		snapshot[clean] = fileStamp{modUnixNano: info.ModTime().UnixNano(), size: info.Size()}
		return nil
	})
	return snapshot
}

func detectSnapshotChanges(prev, curr map[string]fileStamp) ([]string, bool) {
	var changed []string
	needGenerate := false
	for path, stamp := range curr {
		old, ok := prev[path]
		if !ok || old != stamp {
			changed = append(changed, path)
			ext := filepath.Ext(path)
			if ext == ".fbs" || ext == ".templ" {
				needGenerate = true
			}
		}
	}
	for path := range prev {
		if _, ok := curr[path]; !ok {
			changed = append(changed, path)
		}
	}
	return changed, needGenerate
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
