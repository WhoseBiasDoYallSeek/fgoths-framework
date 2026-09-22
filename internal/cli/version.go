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
	"runtime"
)

// Version is the framework semver. The default is the development fallback;
// release builds override it via ldflags:
//
//	go build -ldflags="-X github.com/WhoseBiasDoYallSeek/fgoths-framework/internal/cli.Version=1.0.0"
//
// Maintenance and future releases only need to change this value (or pass the
// ldflags flag at build time) — everything else derives from it.
var Version = "0.0.0-dev"

// Commit is the git commit hash at build time (optional, set via ldflags).
var Commit = "unknown"

// BuildDate is the build timestamp (optional, set via ldflags).
var BuildDate = "unknown"

// RunVersion prints the CLI/framework version information.
func RunVersion(_ []string) {
	fmt.Println("FGOTHS Framework")
	fmt.Printf("  version:   %s\n", Version)
	fmt.Printf("  commit:    %s\n", Commit)
	fmt.Printf("  built:     %s\n", BuildDate)
	fmt.Printf("  go:        %s\n", runtime.Version())
	fmt.Printf("  platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
}
