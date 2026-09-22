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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecretResolverReadsFromEnvironment(t *testing.T) {
	t.Setenv("FGOTHS_TEST_SECRET", "super-secret")
	resolver := NewSecretResolver(EnvironmentSecretSource())

	secret, err := resolver.Get("FGOTHS_TEST_SECRET")
	if err != nil {
		t.Fatalf("expected secret to resolve, got error: %v", err)
	}
	if secret != "super-secret" {
		t.Fatalf("expected secret super-secret, got %q", secret)
	}
}

func TestSecretResolverRejectsMissingSecret(t *testing.T) {
	resolver := NewSecretResolver(EnvironmentSecretSource())

	if _, err := resolver.Get("FGOTHS_MISSING_SECRET"); err == nil {
		t.Fatal("expected missing secret error")
	}
}

func TestRequireSecretAddsSecretToContext(t *testing.T) {
	t.Setenv("FGOTHS_CONTEXT_SECRET", "runtime-secret")
	mw := RequireSecret(NewSecretResolver(EnvironmentSecretSource()), "FGOTHS_CONTEXT_SECRET")
	req := httptest.NewRequest(http.MethodGet, "http://example.com/secure", nil)
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := ContextSecret(r, "FGOTHS_CONTEXT_SECRET"); got != "runtime-secret" {
			t.Fatalf("expected runtime-secret, got %q", got)
		}
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(res, req)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
}

func TestContextSecretReturnsEmptyWhenMissing(t *testing.T) {
	ctx := context.WithValue(context.Background(), secretContextKey{}, "value")
	req := httptest.NewRequest(http.MethodGet, "http://example.com", nil).WithContext(ctx)
	if got := ContextSecret(req, "missing"); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
}
