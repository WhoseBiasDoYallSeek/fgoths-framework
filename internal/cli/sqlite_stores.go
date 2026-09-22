// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build sqlite

package cli

import (
	"fmt"

	"github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
)

// sqliteStores opens the SQLite-backed deployment and policy stores from a
// single database file. Requires the `sqlite` build tag.
func sqliteStores(path string) (runtime.DeploymentStore, runtime.PolicyVersionStore, error) {
	depStore, err := runtime.NewSQLiteDeploymentStore(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open sqlite deployment store: %w", err)
	}
	policyStore, err := runtime.NewSQLitePolicyVersionStore(path)
	if err != nil {
		_ = depStore.Close()
		return nil, nil, fmt.Errorf("open sqlite policy store: %w", err)
	}
	return depStore, policyStore, nil
}
