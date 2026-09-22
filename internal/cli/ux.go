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
// WITHOUT WARRANTIES OF CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/config"
)

// Terminal colors
const (
	colorReset = "\033[0m"
	colorBold  = "\033[1m"
	colorCyan  = "\033[36m"
	colorGreen = "\033[32m"
	colorRed   = "\033[31m"
	colorGray  = "\033[90m"
)

// supportsColor checks if the terminal supports color output.
func supportsColor() bool {
	// Check if NO_COLOR is set
	if _, ok := os.LookupEnv("NO_COLOR"); ok {
		return false
	}
	// Check if terminal supports ANSI escape codes
	return true
}

// color returns the string wrapped in ANSI color codes if the terminal supports it.
func color(s string, c string) string {
	if !supportsColor() {
		return s
	}
	return c + s + colorReset
}

// bold returns bold text
func bold(s string) string { return color(s, colorBold) }

// cyan returns cyan text
func cyan(s string) string { return color(s, colorCyan) }

// green returns green text
func green(s string) string { return color(s, colorGreen) }

// red returns red text
func red(s string) string { return color(s, colorRed) }

// gray returns gray text
func gray(s string) string { return color(s, colorGray) }

// emoji returns the emoji part of a string (before the space)
func emoji(s string) string {
	if idx := strings.Index(s, " "); idx > 0 {
		return s[:idx]
	}
	return s
}

// RunPresets prints available presets with details.
func RunPresets(args []string) {
	fmt.Println()
	fmt.Println(bold("FGOTHS Project Presets"))
	fmt.Println(strings.Repeat("─", 50))
	fmt.Println()

	for _, p := range config.ListPresets() {
		preset, _ := config.GetPreset(p.Name)
		fmt.Printf("  %s%s%s\n", cyan(p.Name), color(":"+strings.Repeat(" ", 14-len(p.Name)), colorGray), p.Description)
		fmt.Printf("     Type: %s | Arch: %s | DB: %s\n", string(preset.Type), string(preset.Architecture), string(preset.Database))

		if len(preset.Features) > 0 {
			featureList := make([]string, len(preset.Features))
			for i, f := range preset.Features {
				featureList[i] = string(f)
			}
			fmt.Printf("     %s %s\n", gray("Features:"), strings.Join(featureList, cyan(", ")))
		}
		fmt.Println()
	}

	fmt.Println(bold("Feature aliases:"))
	featureAliases := []string{
		cyan("health") + " - Health check endpoints (/health, /health/live, /health/ready)",
		cyan("metrics") + " - Prometheus-compatible metrics (zero deps, /metrics)",
		cyan("openapi") + " - OpenAPI spec + docs (/docs, /openapi.yaml)",
		cyan("grpc") + " - gRPC-style JSON API (:9090)",
		cyan("flatbuffers") + " - FlatBuffers schema serialization",
		cyan("htmx") + " - HTMX for SSR without JavaScript",
		cyan("jwt-auth") + " - JWT middleware with roles/scopes",
		cyan("mtls") + " - Mutual TLS + client identity routing",
		cyan("otel") + " - OpenTelemetry distributed tracing",
		cyan("ci-cd") + " - GitHub Actions + GitLab CI templates",
	}
	for _, alias := range featureAliases {
		fmt.Printf("  %s\n", alias)
	}
	fmt.Println()

	fmt.Println(bold("Quick start:"))
	fmt.Printf("  %s %s\n", gray("$"), cyan("fgoths init --name=my-api --preset=api"))
	fmt.Printf("  %s %s\n", gray("$"), cyan("fgoths init --name=my-site --preset=webapp --features=metrics,openapi"))
	fmt.Println()
}
