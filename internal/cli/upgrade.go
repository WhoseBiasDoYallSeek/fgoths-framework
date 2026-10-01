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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/generator"
)

type upgradeManifest struct {
	Version      string            `json:"framework_version"`
	UpdatedAt    string            `json:"updated_at"`
	ProjectType  string            `json:"project_type,omitempty"`
	Architecture string            `json:"architecture,omitempty"`
	Database     string            `json:"database,omitempty"`
	Features     []string          `json:"features,omitempty"`
	RuntimeFiles map[string]string `json:"runtime_files"`
}

type upgradeFileAction struct {
	path       string
	status     string
	original   []byte
	content    []byte
	mode       os.FileMode
	conflict   []byte
	conflictTo string
}

type upgradeReport struct {
	from           string
	to             string
	actions        []upgradeFileAction
	artifactIgnore *upgradeFileAction
}

// Filesystem, process, and generator hooks are variables so failure paths can
// be tested deterministically without depending on host permissions.
var (
	mergeRuntimeFile       = mergeWithGit
	managedRuntimeSources  = generator.ManagedRuntimeSources
	resolveAbsPath         = filepath.Abs
	readProjectFile        = os.ReadFile
	lstatProjectPath       = os.Lstat
	mkdirProjectDir        = os.MkdirAll
	createProjectTemp      = os.CreateTemp
	openNewProjectFile     = os.OpenFile
	renameProjectFile      = os.Rename
	marshalUpgradeManifest = json.MarshalIndent
	lookPathGit            = exec.LookPath
	makeMergeWorkspace     = os.MkdirTemp
	writeMergeInput        = os.WriteFile
)

// RunUpgrade updates only framework-managed runtime source files in an
// existing generated project. It plans changes by default; --apply is required
// to write files.
func RunUpgrade(args []string) error {
	cliVersion := frameworkVersion()
	if cliVersion != "0.0.0-dev" && cliVersion != latestRuntimeUpgradeVersion {
		return fmt.Errorf("this CLI contains the v%s runtime upgrade path, but reports v%s", latestRuntimeUpgradeVersion, cliVersion)
	}

	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	fs.Usage = func() {
		fmt.Fprintln(os.Stdout, "Usage: fgoths upgrade [--dir=PATH] [--from=VERSION] [--apply]")
		fs.PrintDefaults()
	}
	projectDir := fs.String("dir", ".", "Generated project directory")
	fromVersion := fs.String("from", "", "Current FGOTHS version (required when project has no upgrade metadata)")
	apply := fs.Bool("apply", false, "Apply safe updates and back up original files")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("upgrade does not accept positional arguments")
	}
	root, err := resolveAbsPath(*projectDir)
	if err != nil {
		return fmt.Errorf("resolve project directory: %w", err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve project directory %q: %w", *projectDir, err)
	}

	report, err := planRuntimeUpgrade(root, *fromVersion, latestRuntimeUpgradeVersion)
	if err != nil {
		return err
	}
	if report.from == report.to {
		fmt.Printf("Project runtime is already at v%s; no changes are needed.\n", report.to)
		return nil
	}
	printUpgradeReport(report, *apply)
	if !*apply {
		fmt.Println("Dry run only. Re-run with --apply to write safe updates.")
		return nil
	}

	conflicts, err := applyRuntimeUpgrade(root, report)
	if err != nil {
		return err
	}
	if conflicts > 0 {
		return fmt.Errorf("%d local conflict(s) were preserved; resolve the reported merge files and rerun upgrade", conflicts)
	}
	fmt.Printf("Runtime upgraded to v%s. Review the backup directory and run your project tests.\n", report.to)
	return nil
}

func planRuntimeUpgrade(root, fromArg, to string) (upgradeReport, error) {
	var report upgradeReport
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		if err != nil {
			return report, fmt.Errorf("project directory is unavailable: %w", err)
		}
		return report, fmt.Errorf("%q is not a directory", root)
	}
	_, goModInfo, err := safeProjectPath(root, "go.mod")
	if err != nil || goModInfo == nil || !goModInfo.Mode().IsRegular() {
		if err != nil {
			return report, err
		}
		return report, fmt.Errorf("%q is not a generated Go project (go.mod not found)", root)
	}
	_, runtimeInfo, err := safeProjectPath(root, "pkg/runtime/server.go")
	if err != nil {
		return report, err
	}
	if runtimeInfo == nil || !runtimeInfo.Mode().IsRegular() {
		return report, fmt.Errorf("%q has no pkg/runtime/server.go; this command only upgrades embedded FGOTHS runtimes", root)
	}

	manifestPath, manifestInfo, err := safeProjectPath(root, ".fgoths/upgrade.json")
	if err != nil {
		return report, err
	}
	var manifest upgradeManifest
	if manifestInfo != nil {
		data, err := readProjectFile(manifestPath)
		if err != nil {
			return report, fmt.Errorf("read upgrade metadata: %w", err)
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			return report, fmt.Errorf("parse .fgoths/upgrade.json: %w", err)
		}
	}

	from := strings.TrimPrefix(strings.TrimSpace(fromArg), "v")
	if manifest.Version != "" {
		if from != "" && from != manifest.Version {
			return report, fmt.Errorf("--from=v%s does not match project upgrade metadata v%s", from, manifest.Version)
		}
		from = manifest.Version
	} else if from == "" {
		return report, fmt.Errorf("project has no FGOTHS upgrade metadata; specify its current version, e.g. --from=1.1.0")
	}

	report = upgradeReport{from: from, to: to}
	if from == to {
		return report, nil
	}
	if to != latestRuntimeUpgradeVersion {
		return report, fmt.Errorf("unsupported runtime upgrade v%s -> v%s (supported target: v%s)", from, to, latestRuntimeUpgradeVersion)
	}
	baseline, err := runtimeUpgradeBaseline(from)
	if err != nil {
		return report, fmt.Errorf("unsupported runtime upgrade v%s -> v%s: %w", from, to, err)
	}
	report.artifactIgnore, err = planUpgradeArtifactIgnore(root)
	if err != nil {
		return report, err
	}
	targetSources, err := managedRuntimeSources()
	if err != nil {
		return report, fmt.Errorf("load target runtime sources: %w", err)
	}
	for _, relPath := range runtimeUpgradePaths {
		base, baseOK := baseline[relPath]
		target, targetOK := targetSources[relPath]
		if !baseOK || !targetOK {
			return report, fmt.Errorf("runtime upgrade metadata is incomplete for %s", relPath)
		}
		targetPath, info, err := safeProjectPath(root, relPath)
		if err != nil {
			return report, err
		}
		if info == nil {
			continue
		}
		if !info.Mode().IsRegular() {
			return report, fmt.Errorf("%s is not a regular file; refusing to update it", relPath)
		}
		current, err := readProjectFile(targetPath)
		if err != nil {
			return report, fmt.Errorf("read %s: %w", relPath, err)
		}
		action := upgradeFileAction{path: relPath, original: append([]byte(nil), current...), content: target, mode: info.Mode().Perm()}
		switch {
		case bytes.Equal(current, target):
			action.status = "current"
			action.content = nil
		case bytes.Equal(current, base):
			action.status = "update"
		default:
			merged, mergeErr := mergeRuntimeFile(current, base, target)
			if mergeErr != nil {
				if errors.Is(mergeErr, errMergeConflict) {
					action.status = "conflict"
					action.conflict = merged
					action.conflictTo = filepath.Join(".fgoths", "upgrade-conflicts", "v"+to, relPath+".merge")
				} else {
					return report, fmt.Errorf("merge local changes in %s: %w", relPath, mergeErr)
				}
			} else if bytes.Equal(current, merged) {
				action.status = "current"
				action.content = nil
			} else {
				action.status = "merge"
				action.content = merged
			}
		}
		report.actions = append(report.actions, action)
	}

	return report, nil
}

var errMergeConflict = errors.New("three-way merge conflict")

func mergeWithGit(current, base, target []byte) ([]byte, error) {
	gitPath, err := lookPathGit("git")
	if err != nil {
		return nil, fmt.Errorf("git is required to merge locally edited runtime files")
	}
	dir, err := makeMergeWorkspace("", "fgoths-upgrade-merge-*")
	if err != nil {
		return nil, fmt.Errorf("create merge workspace: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	paths := []string{
		filepath.Join(dir, "current.go"),
		filepath.Join(dir, "base.go"),
		filepath.Join(dir, "target.go"),
	}
	for i, content := range [][]byte{current, base, target} {
		if err := writeMergeInput(paths[i], content, 0o600); err != nil {
			return nil, fmt.Errorf("write merge input: %w", err)
		}
	}
	cmd := exec.Command(gitPath, "merge-file", "-p", paths[0], paths[1], paths[2])
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err = cmd.Run()
	if err == nil {
		return output.Bytes(), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return output.Bytes(), errMergeConflict
	}
	return nil, fmt.Errorf("git merge-file failed: %s: %w", strings.TrimSpace(output.String()), err)
}

func applyRuntimeUpgrade(root string, report upgradeReport) (int, error) {
	stamp := time.Now().UTC().Format("20060102T150405.000000000Z")
	backupRoot := filepath.Join(".fgoths", "upgrade-backups", "v"+report.to+"-"+stamp)
	conflicts := 0
	if report.artifactIgnore != nil {
		if err := applyUpgradeArtifactIgnore(root, backupRoot, *report.artifactIgnore); err != nil {
			return conflicts, err
		}
	}
	for _, action := range report.actions {
		switch action.status {
		case "update", "merge":
			targetPath, info, err := safeProjectPath(root, action.path)
			if err != nil {
				return conflicts, err
			}
			if info == nil || !info.Mode().IsRegular() {
				return conflicts, fmt.Errorf("%s changed while upgrade was being applied; no content was written", action.path)
			}
			original, err := readProjectFile(targetPath)
			if err != nil {
				return conflicts, fmt.Errorf("read %s before update: %w", action.path, err)
			}
			if !bytes.Equal(original, action.original) {
				return conflicts, fmt.Errorf("%s changed after the upgrade plan was created; no content was written", action.path)
			}
			backupPath := filepath.Join(backupRoot, action.path)
			if err := writeNewProjectFile(root, backupPath, original, info.Mode().Perm()); err != nil {
				return conflicts, fmt.Errorf("back up %s: %w", action.path, err)
			}
			if err := writeProjectFile(root, action.path, action.content, action.mode); err != nil {
				return conflicts, fmt.Errorf("write updated %s (backup at %s): %w", action.path, backupPath, err)
			}
			fmt.Printf("  Updated %s (backup: %s)\n", action.path, backupPath)
		case "conflict":
			conflicts++
			originalPath, _, err := safeProjectPath(root, action.path)
			if err != nil {
				return conflicts, err
			}
			original, err := readProjectFile(originalPath)
			if err != nil {
				return conflicts, fmt.Errorf("read %s before saving merge candidate: %w", action.path, err)
			}
			if !bytes.Equal(original, action.original) {
				return conflicts, fmt.Errorf("%s changed after the upgrade plan was created; no content was written", action.path)
			}
			if err := writeNewProjectFile(root, action.conflictTo, action.conflict, 0o600); err != nil {
				candidatePath, info, pathErr := safeProjectPath(root, action.conflictTo)
				if pathErr != nil || info == nil {
					return conflicts, fmt.Errorf("save merge candidate for %s: %w", action.path, err)
				}
				existing, readErr := readProjectFile(candidatePath)
				if readErr != nil || !bytes.Equal(existing, action.conflict) {
					return conflicts, fmt.Errorf("save merge candidate for %s: %w", action.path, err)
				}
			}
			fmt.Printf("  Conflict %s: original preserved; review %s\n", action.path, action.conflictTo)
		}
	}
	if conflicts > 0 {
		return conflicts, nil
	}

	metadataPath, metadataInfo, err := safeProjectPath(root, ".fgoths/upgrade.json")
	if err != nil {
		return conflicts, err
	}
	metadata := upgradeManifest{RuntimeFiles: make(map[string]string)}
	var originalMetadata []byte
	if metadataInfo != nil {
		originalMetadata, err = readProjectFile(metadataPath)
		if err != nil {
			return conflicts, fmt.Errorf("read existing upgrade metadata: %w", err)
		}
		if err := json.Unmarshal(originalMetadata, &metadata); err != nil {
			return conflicts, fmt.Errorf("parse existing upgrade metadata: %w", err)
		}
		if metadata.RuntimeFiles == nil {
			metadata.RuntimeFiles = make(map[string]string)
		}
	}
	hashes := make(map[string]string, len(metadata.RuntimeFiles))
	for path, hash := range metadata.RuntimeFiles {
		hashes[path] = hash
	}
	sources, err := managedRuntimeSources()
	if err != nil {
		return conflicts, fmt.Errorf("load runtime sources for upgrade metadata: %w", err)
	}
	for _, relPath := range runtimeUpgradePaths {
		_, info, err := safeProjectPath(root, relPath)
		if err != nil {
			return conflicts, err
		}
		if info == nil {
			delete(hashes, relPath)
			continue
		}
		content := sources[relPath]
		hash := sha256.Sum256(content)
		hashes[relPath] = hex.EncodeToString(hash[:])
	}
	metadata.Version = report.to
	metadata.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	metadata.RuntimeFiles = hashes
	encodedMetadata, err := marshalUpgradeManifest(metadata, "", "  ")
	if err != nil {
		return conflicts, fmt.Errorf("encode upgrade metadata: %w", err)
	}
	manifestBackupPath := filepath.Join(backupRoot, ".fgoths", "upgrade.json")
	if metadataInfo != nil {
		if err := writeNewProjectFile(root, manifestBackupPath, originalMetadata, metadataInfo.Mode().Perm()); err != nil {
			return conflicts, fmt.Errorf("back up existing upgrade metadata: %w", err)
		}
	}
	if err := writeProjectFile(root, ".fgoths/upgrade.json", append(encodedMetadata, '\n'), 0o644); err != nil {
		return conflicts, fmt.Errorf("write upgrade metadata: %w", err)
	}
	return conflicts, nil
}

func printUpgradeReport(report upgradeReport, applying bool) {
	fmt.Printf("FGOTHS runtime upgrade plan: v%s -> v%s\n", report.from, report.to)
	if len(report.actions) == 0 && report.artifactIgnore == nil {
		fmt.Println("  No runtime source changes are required.")
		return
	}
	for _, action := range report.actions {
		switch action.status {
		case "update":
			fmt.Printf("  Update %s\n", action.path)
		case "merge":
			fmt.Printf("  Merge local changes in %s\n", action.path)
		case "conflict":
			fmt.Printf("  Conflict in %s; original will be preserved", action.path)
			if !applying {
				fmt.Printf(" (candidate: %s)", action.conflictTo)
			}
			fmt.Println()
		case "current":
			fmt.Printf("  Current %s\n", action.path)
		}
	}
	if report.artifactIgnore != nil {
		fmt.Println("  Add .fgoths/.gitignore to keep upgrade backups and conflicts out of Git")
	}
}

var upgradeArtifactIgnoreRules = []string{
	"/upgrade-backups/",
	"/upgrade-conflicts/",
}

func planUpgradeArtifactIgnore(root string) (*upgradeFileAction, error) {
	const relPath = ".fgoths/.gitignore"
	path, info, err := safeProjectPath(root, relPath)
	if err != nil {
		return nil, err
	}
	var original []byte
	mode := os.FileMode(0o644)
	if info != nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a regular file; refusing to update it", relPath)
		}
		original, err = readProjectFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", relPath, err)
		}
		mode = info.Mode().Perm()
	}
	content := append([]byte(nil), original...)
	for _, rule := range upgradeArtifactIgnoreRules {
		if hasIgnoreRule(content, rule) {
			continue
		}
		newline := "\n"
		if bytes.Contains(content, []byte("\r\n")) {
			newline = "\r\n"
		}
		if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
			content = append(content, newline...)
		}
		content = append(content, rule...)
		content = append(content, newline...)
	}
	if bytes.Equal(content, original) {
		return nil, nil
	}
	return &upgradeFileAction{
		path:     relPath,
		status:   "ignore",
		original: original,
		content:  content,
		mode:     mode,
	}, nil
}

func hasIgnoreRule(content []byte, rule string) bool {
	for _, line := range strings.Split(string(content), "\n") {
		if strings.TrimSpace(line) == rule {
			return true
		}
	}
	return false
}

func applyUpgradeArtifactIgnore(root, backupRoot string, action upgradeFileAction) error {
	path, info, err := safeProjectPath(root, action.path)
	if err != nil {
		return err
	}
	if info == nil {
		if len(action.original) != 0 {
			return fmt.Errorf("%s changed after the upgrade plan was created; no content was written", action.path)
		}
		if err := writeNewProjectFile(root, action.path, action.content, action.mode); err != nil {
			return fmt.Errorf("write %s: %w", action.path, err)
		}
		fmt.Printf("  Added %s\n", action.path)
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s changed after the upgrade plan was created; no content was written", action.path)
	}
	original, err := readProjectFile(path)
	if err != nil {
		return fmt.Errorf("read %s before update: %w", action.path, err)
	}
	if !bytes.Equal(original, action.original) {
		return fmt.Errorf("%s changed after the upgrade plan was created; no content was written", action.path)
	}
	backupPath := filepath.Join(backupRoot, action.path)
	if err := writeNewProjectFile(root, backupPath, original, info.Mode().Perm()); err != nil {
		return fmt.Errorf("back up %s: %w", action.path, err)
	}
	if err := writeProjectFile(root, action.path, action.content, action.mode); err != nil {
		return fmt.Errorf("write %s (backup at %s): %w", action.path, backupPath, err)
	}
	fmt.Printf("  Updated %s (backup: %s)\n", action.path, backupPath)
	return nil
}

func safeProjectPath(root, relPath string) (string, os.FileInfo, error) {
	clean := filepath.Clean(relPath)
	if filepath.IsAbs(clean) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", nil, fmt.Errorf("unsafe project path %q", relPath)
	}
	current := root
	parts := strings.Split(clean, string(filepath.Separator))
	var info os.FileInfo
	for i, part := range parts {
		current = filepath.Join(current, part)
		var err error
		info, err = lstatProjectPath(current)
		if os.IsNotExist(err) {
			return filepath.Join(root, clean), nil, nil
		}
		if err != nil {
			return "", nil, fmt.Errorf("inspect project path %s: %w", filepath.Join(parts[:i+1]...), err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, fmt.Errorf("refusing to follow symlink in project path %s", filepath.Join(parts[:i+1]...))
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", nil, fmt.Errorf("project path component %s is not a directory", filepath.Join(parts[:i+1]...))
		}
	}
	return current, info, nil
}

func writeProjectFile(root, relPath string, content []byte, mode os.FileMode) error {
	path, _, err := safeProjectPath(root, relPath)
	if err != nil {
		return err
	}
	if err := mkdirProjectDir(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, _, err := safeProjectPath(root, relPath); err != nil {
		return err
	}
	temp, err := createProjectTemp(filepath.Dir(path), ".fgoths-upgrade-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	_, writeErr := temp.Write(content)
	if err := errors.Join(writeErr, temp.Chmod(mode.Perm()), temp.Close()); err != nil {
		return err
	}
	return renameProjectFile(tempName, path)
}

func writeNewProjectFile(root, relPath string, content []byte, mode os.FileMode) error {
	path, info, err := safeProjectPath(root, relPath)
	if err != nil {
		return err
	}
	if info != nil {
		return fmt.Errorf("%s already exists; refusing to overwrite it", relPath)
	}
	if err := mkdirProjectDir(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, _, err := safeProjectPath(root, relPath); err != nil {
		return err
	}
	file, err := openNewProjectFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode.Perm())
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	return errors.Join(writeErr, file.Close())
}
