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
	"fmt"
	"net/http"
	"os"
	"strings"
)

// SecretResolver resolves secrets from configured sources such as environment variables.
type SecretResolver struct {
	sources []SecretSource
}

// SecretSource resolves a secret name to a secret value.
type SecretSource func(name string) (string, error)

// NewSecretResolver builds a resolver from one or more sources.
func NewSecretResolver(sources ...SecretSource) *SecretResolver {
	if len(sources) == 0 {
		return &SecretResolver{sources: []SecretSource{EnvironmentSecretSource()}}
	}
	return &SecretResolver{sources: sources}
}

// EnvironmentSecretSource resolves secrets from process environment variables.
func EnvironmentSecretSource() SecretSource {
	return func(name string) (string, error) {
		key := strings.TrimSpace(name)
		if key == "" {
			return "", fmt.Errorf("secret name is empty")
		}
		value, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(value) == "" {
			return "", fmt.Errorf("secret %q not found", key)
		}
		return value, nil
	}
}

// Get resolves a secret from the configured sources.
func (r *SecretResolver) Get(name string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("secret resolver is nil")
	}
	for _, source := range r.sources {
		if source == nil {
			continue
		}
		value, err := source(name)
		if err == nil && strings.TrimSpace(value) != "" {
			return value, nil
		}
	}
	return "", fmt.Errorf("secret %q not found", strings.TrimSpace(name))
}

type secretContextKey struct{}

// ContextSecret returns a secret value previously injected into the request context.
func ContextSecret(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	if values, ok := r.Context().Value(secretContextKey{}).(map[string]string); ok {
		if value, ok := values[name]; ok {
			return value
		}
	}
	return ""
}

// RequireSecret resolves a named secret and places it in the request context for downstream handlers.
func RequireSecret(resolver *SecretResolver, name string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if resolver == nil {
				http.Error(w, "server misconfiguration: secret resolver not configured", http.StatusInternalServerError)
				return
			}
			secret, err := resolver.Get(name)
			if err != nil {
				http.Error(w, "server misconfiguration: secret unavailable", http.StatusInternalServerError)
				return
			}
			values := map[string]string{name: secret}
			if existing, ok := r.Context().Value(secretContextKey{}).(map[string]string); ok {
				for k, v := range existing {
					values[k] = v
				}
			}
			ctx := context.WithValue(r.Context(), secretContextKey{}, values)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
