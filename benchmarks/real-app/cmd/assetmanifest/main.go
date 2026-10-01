package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	if err := generateManifest("."); err != nil {
		fmt.Fprintln(os.Stderr, "asset manifest:", err)
		os.Exit(1)
	}
}

func generateManifest(root string) error {
	staticRoot := filepath.Join(root, "static")
	var entries []string
	versions := make(map[string]string)
	if err := filepath.WalkDir(staticRoot, func(filePath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(staticRoot, filePath)
		if err != nil {
			return err
		}
		name = filepath.ToSlash(name)
		sum := sha256.Sum256(content)
		versions[name] = hex.EncodeToString(sum[:])
		entries = append(entries, name)
		return nil
	}); err != nil {
		return fmt.Errorf("scan static assets: %w", err)
	}

	target := filepath.Join(root, "internal", "assets", "manifest_gen.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("create manifest directory: %w", err)
	}
	var source strings.Builder
	source.WriteString("package assets\n\nfunc init() {\n")
	for _, name := range entries {
		fmt.Fprintf(&source, "\tversions[%s] = %s\n", strconv.Quote(name), strconv.Quote(versions[name]))
	}
	source.WriteString("}\n")
	formatted, err := format.Source([]byte(source.String()))
	if err != nil {
		return fmt.Errorf("format manifest: %w", err)
	}
	if current, err := os.ReadFile(target); err == nil && string(current) == string(formatted) {
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing manifest: %w", err)
	}
	if err := os.WriteFile(target, formatted, 0o644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
