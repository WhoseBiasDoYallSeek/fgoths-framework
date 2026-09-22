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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// ArtifactProvenance captures the minimum signed metadata expected for release
// verification and compliance traceability in enterprise deployment flows.
type ArtifactProvenance struct {
	Name      string
	Version   string
	Digest    string
	Signature string
	CreatedAt time.Time
	Source    string
	Content   string
}

// NewArtifactProvenance creates a provenance record for a release artifact.
func NewArtifactProvenance(name, version string) *ArtifactProvenance {
	if name == "" {
		name = "unknown"
	}
	if version == "" {
		version = "dev"
	}
	return &ArtifactProvenance{
		Name:      name,
		Version:   version,
		CreatedAt: time.Now().UTC(),
		Source:    "fgoths-runtime",
	}
}

// SetContent stores the payload that will be hashed and signed.
func (p *ArtifactProvenance) SetContent(content string) {
	if p == nil {
		return
	}
	p.Content = content
	p.Digest = sha256Hex(content)
}

// Sign uses HMAC-SHA256 over the artifact payload and stores the signature.
func (p *ArtifactProvenance) Sign(secret, source string) {
	if p == nil {
		return
	}
	if strings.TrimSpace(source) != "" {
		p.Source = source
	}
	p.Signature = hmacSignature(secret, p.Content)
}

// Verify checks whether the current content and signature match the secret.
func (p *ArtifactProvenance) Verify(secret string) error {
	if p == nil {
		return fmt.Errorf("artifact provenance is nil")
	}
	if strings.TrimSpace(p.Content) == "" {
		return fmt.Errorf("artifact content is empty")
	}
	if strings.TrimSpace(p.Digest) == "" {
		return fmt.Errorf("artifact digest is empty")
	}
	want := hmacSignature(secret, p.Content)
	if !hmac.Equal([]byte(want), []byte(p.Signature)) {
		return fmt.Errorf("artifact signature mismatch")
	}
	if got := sha256Hex(p.Content); got != p.Digest {
		return fmt.Errorf("artifact digest mismatch")
	}
	return nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hmacSignature(secret, value string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
