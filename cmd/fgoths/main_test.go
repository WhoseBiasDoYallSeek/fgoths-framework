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
package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestCLISmoke builds the real binary and exercises safe commands end-to-end.
// The dispatch logic itself is unit-tested in internal/cli; this covers the
// packaged entrypoint (main -> cli.Execute) as a user would invoke it.
func TestCLISmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary build in -short mode")
	}

	bin := t.TempDir() + "/fgoths"
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("go build: %v", err)
	}

	cases := []struct {
		name    string
		args    []string
		wantOut string
		wantErr bool
	}{
		{name: "version", args: []string{"version"}, wantOut: "FGOTHS Framework"},
		{name: "presets", args: []string{"presets"}, wantOut: "webapp"},
		{name: "help", args: []string{"help"}, wantOut: "fgoths <command>"},
		{name: "unknown command", args: []string{"bogus"}, wantOut: "Unknown command", wantErr: true},
		{name: "no command", args: nil, wantOut: "fgoths <command>", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			out, err := cmd.CombinedOutput()
			got := string(out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected non-zero exit for %v, output: %s", tc.args, got)
				}
			} else if err != nil {
				t.Fatalf("%v failed: %v\noutput: %s", tc.args, err, got)
			}
			if !strings.Contains(got, tc.wantOut) {
				t.Fatalf("output %q does not contain %q", got, tc.wantOut)
			}
		})
	}
}
