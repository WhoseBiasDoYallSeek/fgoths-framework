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
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

var errTest = errors.New("test resolver failure")

func TestNewSecretResolverDefaultsToEnvironmentSource(t *testing.T) {
	resolver := NewSecretResolver()
	if len(resolver.sources) != 1 || resolver.sources[0] == nil {
		t.Fatalf("expected the default environment source to be installed, got %#v", resolver.sources)
	}
	t.Setenv("FGOTHS_DEFAULT_SOURCE_SECRET", "resolved")
	if got, err := resolver.Get("FGOTHS_DEFAULT_SOURCE_SECRET"); err != nil || got != "resolved" {
		t.Fatalf("expected the default environment source to resolve, got (%q, %v)", got, err)
	}
}

func TestSecretResolverNilSafety(t *testing.T) {
	var resolver *SecretResolver
	if _, err := resolver.Get("anything"); err == nil {
		t.Fatal("expected Get on a nil resolver to fail")
	}
}

func TestSecretResolverSkipsNilSourcesAndWhitespaceValues(t *testing.T) {
	resolver := NewSecretResolver(nil, func(name string) (string, error) { return "  ", nil }, func(name string) (string, error) { return "real", nil })
	got, err := resolver.Get("key")
	if err != nil || got != "real" {
		t.Fatalf("expected resolver to skip nil sources and blank values, got (%q, %v)", got, err)
	}
}

func TestEnvironmentSecretSourceRejectsEmptyName(t *testing.T) {
	source := EnvironmentSecretSource()
	if _, err := source("  "); err == nil {
		t.Fatal("expected an empty secret name to fail")
	}
}

func TestRequireSecretRejectsMissingResolverOrSecret(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)

	res := httptest.NewRecorder()
	RequireSecret(nil, "key")(next).ServeHTTP(res, req)
	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 with a nil resolver, got %d", res.Code)
	}

	resolver := NewSecretResolver(func(name string) (string, error) { return "", errTest })
	res2 := httptest.NewRecorder()
	RequireSecret(resolver, "key")(next).ServeHTTP(res2, req)
	if res2.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 with an unresolvable secret, got %d", res2.Code)
	}
}

func TestRequireSecretMergesWithExistingContextSecrets(t *testing.T) {
	resolver := NewSecretResolver(func(name string) (string, error) {
		if name == "second" {
			return "value-2", nil
		}
		return "", errTest
	})

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := ContextSecret(r, "second"); got != "value-2" {
			t.Fatalf("expected second secret value-2, got %q", got)
		}
		if got := ContextSecret(r, "first"); got != "value-1" {
			t.Fatalf("expected the pre-existing first secret to be preserved, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	first := RequireSecret(NewSecretResolver(func(name string) (string, error) { return "value-1", nil }), "first")
	second := RequireSecret(resolver, "second")
	first(second(next)).ServeHTTP(httptest.NewRecorder(), req)
}

func TestArtifactProvenanceDefaultsAndNilSafety(t *testing.T) {
	prov := NewArtifactProvenance("", "")
	if prov.Name != "unknown" || prov.Version != "dev" {
		t.Fatalf("expected unknown/dev defaults, got %q/%q", prov.Name, prov.Version)
	}

	var nilProv *ArtifactProvenance
	nilProv.SetContent("x")          // must not panic
	nilProv.Sign("secret", "source") // must not panic
	if err := nilProv.Verify("secret"); err == nil {
		t.Fatal("expected Verify on a nil provenance to fail")
	}
}

func TestArtifactProvenanceVerifyBranches(t *testing.T) {
	prov := NewArtifactProvenance("orders-api", "1.0.0")
	if err := prov.Verify("secret"); err == nil {
		t.Fatal("expected empty content to fail verification")
	}

	prov.SetContent("payload")
	if err := prov.Verify("secret"); err == nil {
		t.Fatal("expected an unsigned artifact to fail verification")
	}
	prov.Sign("secret", "platform")
	if err := prov.Verify("secret"); err != nil {
		t.Fatalf("expected a signed artifact to verify, got %v", err)
	}
	if err := prov.Verify("wrong-secret"); err == nil {
		t.Fatal("expected a wrong secret to fail verification")
	}

	tampered := NewArtifactProvenance("orders-api", "1.0.0")
	tampered.SetContent("payload")
	tampered.Sign("secret", "platform")
	tampered.Digest = "deadbeef"
	if err := tampered.Verify("secret"); err == nil {
		t.Fatal("expected a tampered digest to fail verification")
	}
}

func TestArtifactProvenanceSignKeepsSourceWhenBlank(t *testing.T) {
	prov := NewArtifactProvenance("orders-api", "1.0.0")
	prov.SetContent("payload")
	prov.Sign("secret", "  ")
	if prov.Source != "fgoths-runtime" {
		t.Fatalf("expected the default source to be kept for a blank override, got %q", prov.Source)
	}
}
