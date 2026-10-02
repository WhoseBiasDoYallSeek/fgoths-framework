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
	"embed"
	"fmt"
	"io/fs"
)

//go:embed upgrade/baselines/v1.1.0/pkg/runtime upgrade/baselines/v1.2.0/pkg/runtime upgrade/baselines/v1.3.0/pkg/runtime upgrade/baselines/v1.4.0/pkg/runtime
var upgradeBaselines embed.FS

const latestRuntimeUpgradeVersion = "1.4.1"

// supportedRuntimeUpgradeSources lists the releases whose generated runtime is
// embedded as a three-way merge ancestor.
var supportedRuntimeUpgradeSources = map[string]bool{
	"1.1.0": true,
	"1.2.0": true,
	"1.3.0": true,
	"1.4.0": true,
}

var runtimeUpgradePaths = []string{
	"pkg/runtime/server.go",
	"pkg/runtime/router.go",
	"pkg/runtime/proxy.go",
	"pkg/runtime/metrics.go",
	"pkg/runtime/hmr/hmr.go",
	"pkg/runtime/auth.go",
}

func runtimeUpgradeBaseline(version string) (map[string][]byte, error) {
	if !supportedRuntimeUpgradeSources[version] {
		return nil, fmt.Errorf("no runtime upgrade baseline is available for v%s (supported sources: v1.1.0, v1.2.0, v1.3.0, v1.4.0)", version)
	}
	baseline := make(map[string][]byte, len(runtimeUpgradePaths))
	for _, path := range runtimeUpgradePaths {
		content, err := fs.ReadFile(upgradeBaselines, "upgrade/baselines/v"+version+"/"+path+".txt")
		if err != nil {
			return nil, fmt.Errorf("read embedded v%s baseline for %s: %w", version, path, err)
		}
		baseline[path] = content
	}
	return baseline, nil
}
