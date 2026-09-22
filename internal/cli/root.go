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
	"fmt"
	"os"
)

func Execute() {
	if len(os.Args) < 2 {
		PrintUsage()
		osExit(1)
		return
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "init", "new", "create":
		if err := RunInit(args); err != nil {
			fmt.Printf("❌ Error: %v\n", err)
			osExit(1)
			return
		}
	case "generate", "gen":
		RunGenerate(args)
	case "dev", "run":
		RunDev(args)
	case "build":
		RunBuild(args)
	case "controlplane", "cp":
		RunControlPlane(args)
	case "sync-templates", "sync":
		RunSyncTemplates(args)
	case "version", "--version", "-v":
		RunVersion(args)
	case "presets", "list-presets", "templates":
		RunPresets(args)
	case "routes":
		RunRoutes(args)
	case "help", "-h", "--help":
		PrintUsage()
	default:
		fmt.Printf("Unknown command: %s\n\n", command)
		PrintUsage()
		osExit(1)
		return
	}
}

func PrintUsage() {
	fmt.Println()
	fmt.Println(bold("FGOTHS CLI") + " - the secure alternative to Node.js for mission-critical services")
	fmt.Println()
	fmt.Println(bold("Usage:"))
	fmt.Println("  fgoths <command> [options]")
	fmt.Println()
	fmt.Println(bold("Commands:"))
	fmt.Println("  " + cyan("init") + "           " + gray("Generate a new project [--name --dir --preset --db --features]") + "")
	fmt.Println("  " + cyan("presets") + "        " + gray("Show the two presets and every opt-in feature") + "")
	fmt.Println("  " + cyan("routes") + "         " + gray("List registered HTTP routes in the current project") + "")
	fmt.Println("  " + cyan("generate") + "       " + gray("Compile .templ files (webapp projects)") + "")
	fmt.Println("  " + cyan("dev") + "            " + gray("Hot reload dev loop with fragment HMR") + "")
	fmt.Println("  " + cyan("build") + "          " + gray("Compile the static production binary") + "")
	fmt.Println("  " + cyan("version") + "        " + gray("Print framework version") + "")
	fmt.Println("  " + cyan("controlplane") + "   " + gray("Run governance API [--addr --store --token]") + "")
	fmt.Println("  " + cyan("sync-templates") + " " + gray("Framework maintainers only: sync pkg/runtime -> templates [--check]") + "")
	fmt.Println()
	fmt.Println(bold("Presets — pick one, opt in to the rest:"))
	fmt.Println("  " + cyan("api") + "     Flat JSON API + health + proxy")
	fmt.Println("          " + gray("opt-ins: metrics, openapi, grpc, jwt-auth, mtls, otel, flatbuffers, --db=sqlite"))
	fmt.Println("  " + cyan("webapp") + "  MVC SSR + HTMX + SQLite + proxy")
	fmt.Println("          " + gray("opt-ins: metrics, openapi, jwt-auth, mtls, otel, flatbuffers"))
	fmt.Println()
	fmt.Println(bold("Choosing:"))
	fmt.Println("  " + gray("JSON API or service-to-service?") + " " + cyan("api"))
	fmt.Println("  " + gray("Rendering HTML pages?") + " " + cyan("webapp"))
	fmt.Println("  " + gray("Both in one binary?") + " " + cyan("webapp") + gray(" - its handlers serve JSON too"))
	fmt.Println()
	fmt.Println(bold("Examples:"))
	fmt.Println("  " + gray("$") + " " + cyan("fgoths init --name=user-service --preset=api"))
	fmt.Println("  " + gray("$") + " " + cyan("fgoths init --name=my-app --preset=api --db=sqlite --features=metrics,openapi"))
	fmt.Println("  " + gray("$") + " " + cyan("fgoths init --name=my-site --preset=webapp"))
	fmt.Println("  " + gray("$") + " " + cyan("fgoths presets"))
	fmt.Println()
}
