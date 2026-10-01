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
	"debug/buildinfo"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

func RunBuild(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	output := fs.String("out", "bin/app", "Output binary path")
	scratch := fs.Bool("scratch", false, "Generate Dockerfile for scratch container")
	sbom := fs.Bool("sbom", false, "Generate Software Bill of Materials (SBOM)")
	_ = fs.Parse(args)
	pkg := "./cmd/app"
	if _, err := os.Stat("cmd/app/main.go"); err != nil {
		pkg = "."
	}

	fmt.Println("Building optimized binary...")

	cmd := exec.Command("go", "build",
		"-ldflags=-s -w",
		"-trimpath",
		"-o", *output,
		pkg,
	)

	goos := runtime.GOOS
	goarch := runtime.GOARCH
	if *scratch {
		goos = "linux"
		goarch = "amd64"
	}

	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+goos,
		"GOARCH="+goarch,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Build failed: %v\n", err)
		osExit(1)
		return
	}

	if stat, err := os.Stat(*output); err == nil {
		fmt.Printf("Binary: %s (%.2f MB)\n", *output, float64(stat.Size())/1024/1024)
	}

	if *scratch {
		if err := generateDockerfile(*output); err != nil {
			fmt.Printf("Could not generate Dockerfile: %v\n", err)
		} else {
			fmt.Println("Dockerfile generated")
		}
	}

	if *sbom {
		if err := generateSBOM(*output); err != nil {
			fmt.Printf("Could not generate SBOM: %v\n", err)
		} else {
			fmt.Println("SBOM generated (sbom.json)")
		}
	}
}

func generateDockerfile(binaryPath string) error {
	dockerfile := `FROM scratch
COPY ` + binaryPath + ` /app
EXPOSE 8080
ENTRYPOINT ["/app"]
`
	return os.WriteFile("Dockerfile", []byte(dockerfile), 0644)
}

// generateSBOM writes a CycloneDX 1.5 SBOM for the compiled binary. It reads
// the module list embedded by the Go linker, so it lists exactly the modules
// linked into the artifact (with versions, replacements and go.sum hashes)
// rather than the whole module graph. Set SOURCE_DATE_EPOCH for a
// reproducible timestamp.
func generateSBOM(binaryPath string) error {
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("read build info from %s: %w", binaryPath, err)
	}
	timestamp, err := sbomTimestamp()
	if err != nil {
		return err
	}
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	encoder.SetIndent("", "  ")
	// The document only holds strings and slices, so encoding cannot fail.
	_ = encoder.Encode(newCycloneDXBOM(info, timestamp))
	return os.WriteFile("sbom.json", content.Bytes(), 0o644)
}

type cycloneDXBOM struct {
	BOMFormat   string               `json:"bomFormat"`
	SpecVersion string               `json:"specVersion"`
	Version     int                  `json:"version"`
	Metadata    cycloneDXMetadata    `json:"metadata"`
	Components  []cycloneDXComponent `json:"components"`
}

type cycloneDXMetadata struct {
	Timestamp string             `json:"timestamp"`
	Tools     cycloneDXTools     `json:"tools"`
	Component cycloneDXComponent `json:"component"`
}

type cycloneDXTools struct {
	Components []cycloneDXComponent `json:"components"`
}

type cycloneDXComponent struct {
	Type       string              `json:"type"`
	BOMRef     string              `json:"bom-ref,omitempty"`
	Name       string              `json:"name"`
	Version    string              `json:"version,omitempty"`
	PURL       string              `json:"purl,omitempty"`
	Properties []cycloneDXProperty `json:"properties,omitempty"`
}

type cycloneDXProperty struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func newCycloneDXBOM(info *debug.BuildInfo, timestamp time.Time) cycloneDXBOM {
	app := goModuleComponent("application", info.Main.Path, info.Main.Version)
	app.Properties = append(app.Properties, cycloneDXProperty{Name: "golang:toolchain", Value: info.GoVersion})
	for _, setting := range info.Settings {
		app.Properties = append(app.Properties, cycloneDXProperty{Name: "golang:build:" + setting.Key, Value: setting.Value})
	}

	stdlibVersion := strings.TrimPrefix(info.GoVersion, "go")
	components := []cycloneDXComponent{{
		Type:    "library",
		BOMRef:  "pkg:golang/stdlib@" + stdlibVersion,
		Name:    "stdlib",
		Version: info.GoVersion,
		PURL:    "pkg:golang/stdlib@" + stdlibVersion,
	}}
	for _, dep := range info.Deps {
		linked := dep
		if dep.Replace != nil {
			linked = dep.Replace
		}
		component := goModuleComponent("library", linked.Path, linked.Version)
		if dep.Replace != nil {
			component.Properties = append(component.Properties, cycloneDXProperty{Name: "golang:replaces", Value: dep.Path + "@" + dep.Version})
		}
		if linked.Sum != "" {
			component.Properties = append(component.Properties, cycloneDXProperty{Name: "golang:sum", Value: linked.Sum})
		}
		components = append(components, component)
	}

	return cycloneDXBOM{
		BOMFormat:   "CycloneDX",
		SpecVersion: "1.5",
		Version:     1,
		Metadata: cycloneDXMetadata{
			Timestamp: timestamp.UTC().Format(time.RFC3339),
			Tools:     cycloneDXTools{Components: []cycloneDXComponent{{Type: "application", Name: "fgoths", Version: frameworkVersion()}}},
			Component: app,
		},
		Components: components,
	}
}

// goModuleComponent describes a Go module with a pkg:golang purl. Local
// builds report "(devel)" or no version, which is omitted from the purl.
func goModuleComponent(kind, path, version string) cycloneDXComponent {
	purl := "pkg:golang/" + path
	if version != "" && version != "(devel)" {
		purl += "@" + version
	}
	return cycloneDXComponent{Type: kind, BOMRef: purl, Name: path, Version: version, PURL: purl}
}

func sbomTimestamp() (time.Time, error) {
	epoch := os.Getenv("SOURCE_DATE_EPOCH")
	if epoch == "" {
		return time.Now(), nil
	}
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid SOURCE_DATE_EPOCH %q: %w", epoch, err)
	}
	return time.Unix(seconds, 0), nil
}
