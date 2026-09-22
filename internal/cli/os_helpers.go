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
	"errors"
	"os"
	"runtime"
)

// Small indirections over the os package so sync-templates can be tested
// without touching the real repository files.

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

func osWriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func osIsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }

// osExit is a package-level indirection over os.Exit so tests can stub it
// without actually terminating the test binary. Production code reassigns it
// to the real os.Exit by default.
var osExit = func(code int) { os.Exit(code) }

func runtimeCaller(skip int) (pc uintptr, file string, line int, ok bool) {
	return runtime.Caller(skip + 1)
}
