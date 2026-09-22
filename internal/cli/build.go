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
	"runtime"
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
		os.Exit(1)
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
		if err := generateSBOM(); err != nil {
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

func generateSBOM() error {
	cmd := exec.Command("go", "list", "-json", "-m", "all")
	output, err := cmd.Output()
	if err != nil {
		return err
	}

	sbom := fmt.Sprintf(`{
  "bomFormat": "CycloneDX",
  "specVersion": "1.4",
  "version": 1,
  "metadata": {
    "timestamp": "%s",
    "component": {
      "type": "application",
      "name": "fgoths-app"
    }
  },
  "components": [
%s
  ]
}`, time.Now().UTC().Format(time.RFC3339), formatComponents(string(output)))

	return os.WriteFile("sbom.json", []byte(sbom), 0644)
}

func formatComponents(goListOutput string) string {
	lines := strings.Split(goListOutput, "\n")
	var components []string

	for _, line := range lines {
		if strings.Contains(line, `"Path"`) {
			path := strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
			path = strings.Trim(path, `",`)
			components = append(components, fmt.Sprintf(`    {"type": "library", "name": %q}`, path))
		}
	}

	return strings.Join(components, ",\n")
}
