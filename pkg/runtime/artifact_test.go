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
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestArtifactProvenanceTracksDigestAndSignature(t *testing.T) {
	payload := "hello-world"
	h := sha256.Sum256([]byte(payload))
	digest := hex.EncodeToString(h[:])

	prov := NewArtifactProvenance("orders-api", "v1.2.3")
	prov.SetContent(payload)
	prov.Sign("release-secret", "platform")

	if prov.Digest == "" {
		t.Fatal("expected digest to be populated")
	}
	if prov.Digest != digest {
		t.Fatalf("expected digest %s, got %s", digest, prov.Digest)
	}
	if prov.Signature == "" {
		t.Fatal("expected signature to be populated")
	}
	if err := prov.Verify("release-secret"); err != nil {
		t.Fatalf("expected valid provenance to verify, got %v", err)
	}
}

func TestArtifactProvenanceRejectsTamperedPayload(t *testing.T) {
	prov := NewArtifactProvenance("orders-api", "v1.2.3")
	prov.SetContent("hello-world")
	prov.Sign("release-secret", "platform")

	prov.SetContent("hello-world-evil")
	if err := prov.Verify("release-secret"); err == nil {
		t.Fatal("expected tampered artifact to fail verification")
	}
}
