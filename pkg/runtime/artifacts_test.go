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
package runtime

import (
	"strings"
	"testing"
)

func TestArtifactProvenanceNewDefaults(t *testing.T) {
	p := NewArtifactProvenance("", "")
	if p == nil {
		t.Fatal("expected non-nil provenance")
	}
	if p.Name != "unknown" {
		t.Errorf("default name = %q, want %q", p.Name, "unknown")
	}
	if p.Version != "dev" {
		t.Errorf("default version = %q, want %q", p.Version, "dev")
	}
	if p.Source != "fgoths-runtime" {
		t.Errorf("default source = %q, want %q", p.Source, "fgoths-runtime")
	}
}

func TestArtifactProvenanceSignAndVerify(t *testing.T) {
	p := NewArtifactProvenance("release.tar", "1.0.0")
	p.SetContent("hello world")
	p.Sign("secret", "ci-pipeline")
	if p.Source != "ci-pipeline" {
		t.Errorf("source override = %q, want %q", p.Source, "ci-pipeline")
	}
	if err := p.Verify("secret"); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestArtifactProvenanceVerifyWrongSecret(t *testing.T) {
	p := NewArtifactProvenance("release.tar", "1.0.0")
	p.SetContent("hello world")
	p.Sign("secret", "")
	if err := p.Verify("other-secret"); err == nil {
		t.Fatal("expected signature mismatch")
	}
}

func TestArtifactProvenanceVerifyTamperedContent(t *testing.T) {
	p := NewArtifactProvenance("release.tar", "1.0.0")
	p.SetContent("hello world")
	p.Sign("secret", "")
	p.SetContent("hello WORLD")
	if err := p.Verify("secret"); err == nil {
		t.Fatal("expected digest mismatch after content tampering")
	}
}

func TestArtifactProvenanceVerifyNil(t *testing.T) {
	var p *ArtifactProvenance
	if err := p.Verify("secret"); err == nil {
		t.Fatal("expected error for nil receiver")
	}
}

func TestArtifactProvenanceVerifyEmptyContent(t *testing.T) {
	p := NewArtifactProvenance("release.tar", "1.0.0")
	if err := p.Verify("secret"); err == nil {
		t.Fatal("expected error for empty content")
	}
}

func TestArtifactProvenanceMethodsAreNilSafe(t *testing.T) {
	var p *ArtifactProvenance
	p.SetContent("data")
	p.Sign("secret", "src")
}

func TestSha256Hex(t *testing.T) {
	got := sha256Hex("abc")
	if len(got) != 64 {
		t.Errorf("expected 64-char hex digest, got %d chars", len(got))
	}
	if !strings.HasPrefix(got, "ba7816bf") {
		t.Errorf("unexpected digest for 'abc': %s", got)
	}
}
