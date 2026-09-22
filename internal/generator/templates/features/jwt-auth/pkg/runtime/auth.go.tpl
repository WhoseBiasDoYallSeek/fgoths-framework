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
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	jwt "github.com/golang-jwt/jwt/v5"
)

// Policy describes required authentication and claim checks for a protected route.
type Policy struct {
	Roles       []string
	Scopes      []string
	Claims      map[string]any
	RolesClaim  string
	ScopesClaim string
}

func (p Policy) rolesClaimName() string {
	if p.RolesClaim == "" {
		return "roles"
	}
	return p.RolesClaim
}

func (p Policy) scopesClaimName() string {
	if p.ScopesClaim == "" {
		return "scope"
	}
	return p.ScopesClaim
}

func (p Policy) Check(claims map[string]any) error {
	if len(p.Roles) > 0 {
		roles, ok := claims[p.rolesClaimName()]
		if !ok {
			return errors.New("missing required role claim")
		}
		if !hasAnyValue(roles, p.Roles) {
			return errors.New("role requirement failed")
		}
	}
	if len(p.Scopes) > 0 {
		scopes, ok := claims[p.scopesClaimName()]
		if !ok {
			return errors.New("missing required scope claim")
		}
		if !hasAnyScope(scopes, p.Scopes) {
			return errors.New("scope requirement failed")
		}
	}
	for key, want := range p.Claims {
		if got, ok := claims[key]; !ok || !claimMatches(got, want) {
			return fmt.Errorf("claim %q requirement failed", key)
		}
	}
	return nil
}

var claimsContextKey = struct{}{}

// ContextClaims returns the validated JWT claims associated with the request.
func ContextClaims(r *http.Request) map[string]any {
	if r == nil {
		return nil
	}
	if claims, ok := r.Context().Value(claimsContextKey).(map[string]any); ok {
		return claims
	}
	return nil
}

// AccessPolicy describes an ABAC-style authorization requirement based on JWT claims.
type AccessPolicy struct {
	Claims map[string]any
}

func (p AccessPolicy) Check(claims map[string]any) error {
	for key, want := range p.Claims {
		if got, ok := claims[key]; !ok || !claimMatches(got, want) {
			return fmt.Errorf("claim %q requirement failed", key)
		}
	}
	return nil
}

// RequireRole authorizes only requests carrying the required role in the JWT claims.
func RequireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ContextClaims(r)
			if claims == nil {
				http.Error(w, "authorization required", http.StatusForbidden)
				return
			}
			if !hasAnyValue(claims["roles"], []string{role}) && !hasAnyValue(claims["role"], []string{role}) {
				http.Error(w, "role required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequirePermission authorizes only requests carrying the required permission in the JWT claims.
func RequirePermission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ContextClaims(r)
			if claims == nil {
				http.Error(w, "authorization required", http.StatusForbidden)
				return
			}
			if !hasAnyValue(claims["permissions"], []string{permission}) && !hasAnyValue(claims["scope"], []string{permission}) && !hasAnyValue(claims["scopes"], []string{permission}) {
				http.Error(w, "permission required", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAccess enforces ABAC-style claim matching against the validated JWT claims.
func RequireAccess(policy AccessPolicy) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ContextClaims(r)
			if claims == nil {
				http.Error(w, "authorization required", http.StatusForbidden)
				return
			}
			if err := policy.Check(claims); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireJWT validates a bearer token and enforces optional policy claims on the request.
// An empty secret is refused instead of silently falling back to a known default,
// since that would let any attacker forge valid tokens.
func RequireJWT(secret string, policy Policy) func(http.Handler) http.Handler {
	if secret == "" {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "server misconfiguration: JWT secret not configured", http.StatusInternalServerError)
			})
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenString, err := bearerToken(r)
			if err != nil {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) {
				if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
				}
				return []byte(secret), nil
			})
			if err != nil || !token.Valid {
				http.Error(w, "invalid token", http.StatusUnauthorized)
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				http.Error(w, "invalid claims", http.StatusUnauthorized)
				return
			}

			claimMap := make(map[string]any, len(claims))
			for key, value := range claims {
				claimMap[key] = value
			}
			if err := policy.Check(claimMap); err != nil {
				http.Error(w, err.Error(), http.StatusForbidden)
				return
			}

			ctx := context.WithValue(r.Context(), claimsContextKey, claimMap)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) (string, error) {
	if r == nil {
		return "", errors.New("request is nil")
	}
	authorization := r.Header.Get("Authorization")
	if authorization == "" {
		return "", errors.New("missing authorization header")
	}
	parts := strings.SplitN(authorization, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("authorization header must be Bearer <token>")
	}
	if strings.TrimSpace(parts[1]) == "" {
		return "", errors.New("bearer token is empty")
	}
	return strings.TrimSpace(parts[1]), nil
}

func hasAnyValue(value any, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	values := normalizeStringSlice(value)
	for _, want := range expected {
		for _, got := range values {
			if got == want {
				return true
			}
		}
	}
	return false
}

func hasAnyScope(value any, expected []string) bool {
	if len(expected) == 0 {
		return true
	}
	values := normalizeStringSlice(value)
	for _, want := range expected {
		for _, got := range values {
			if got == want {
				return true
			}
		}
	}
	return false
}

func normalizeStringSlice(value any) []string {
	switch v := value.(type) {
	case string:
		if v == "" {
			return nil
		}
		return strings.Fields(v)
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func claimMatches(got, want any) bool {
	if got == nil || want == nil {
		return got == want
	}
	if reflect.DeepEqual(got, want) {
		return true
	}

	gotFloat, gotIsFloat := asFloat64(got)
	wantFloat, wantIsFloat := asFloat64(want)
	if gotIsFloat && wantIsFloat {
		return gotFloat == wantFloat
	}
	return false
}

func asFloat64(v any) (float64, bool) {
	switch x := v.(type) {
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case int:
		return float64(x), true
	case int8:
		return float64(x), true
	case int16:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case uint:
		return float64(x), true
	case uint8:
		return float64(x), true
	case uint16:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	default:
		return 0, false
	}
}
