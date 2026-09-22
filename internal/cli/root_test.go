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
	"io"
	"os"
	"strings"
	"testing"
)

func TestPrintUsage(t *testing.T) {
	output := captureStdout(t, PrintUsage)
	for _, want := range []string{"fgoths <command>", "init", "generate", "dev", "build"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected usage output to mention %q, got %q", want, output)
		}
	}
}

func TestRunPresets(t *testing.T) {
	output := captureStdout(t, func() { RunPresets(nil) })
	if !strings.Contains(output, "webapp") {
		t.Fatalf("expected webapp preset, got %q", output)
	}
	if !strings.Contains(output, "Feature aliases:") {
		t.Fatalf("expected feature aliases section, got %q", output)
	}
}

func TestExecuteDispatchesSafeCommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "presets", args: []string{"fgoths", "presets"}, want: "webapp"},
		{name: "list-presets", args: []string{"fgoths", "list-presets"}, want: "webapp"},
		{name: "version", args: []string{"fgoths", "version"}, want: "FGOTHS Framework"},
		{name: "version --flag", args: []string{"fgoths", "--version"}, want: "FGOTHS Framework"},
		{name: "version -v flag", args: []string{"fgoths", "-v"}, want: "FGOTHS Framework"},
		{name: "help", args: []string{"fgoths", "help"}, want: "fgoths <command>"},
		{name: "-h flag", args: []string{"fgoths", "-h"}, want: "fgoths <command>"},
		{name: "--help flag", args: []string{"fgoths", "--help"}, want: "fgoths <command>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			executeStubMu.Lock()
			defer executeStubMu.Unlock()

			original := os.Args
			os.Args = tc.args
			defer func() { os.Args = original }()

			output := captureStdout(t, Execute)
			if !strings.Contains(output, tc.want) {
				t.Fatalf("Execute(%v) output = %q, want to contain %q", tc.args, output, tc.want)
			}
		})
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was written.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stdout = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return buf.String()
}
