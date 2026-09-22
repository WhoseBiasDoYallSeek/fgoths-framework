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
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func TestRequireJWTAllowsValidToken(t *testing.T) {
	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   "user-42",
		"roles": []string{"admin", "ops"},
		"scope": "orders:read orders:write",
	})
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+tokenString)
	res := httptest.NewRecorder()

	mw := RequireJWT(secret, Policy{Roles: []string{"admin"}, Scopes: []string{"orders:read"}})
	mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := ContextClaims(req)["sub"]; got != "user-42" {
			t.Fatalf("expected sub user-42, got %v", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})).ServeHTTP(res, r)

	if res.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.Code)
	}
}

func TestRequireJWTRejectsMissingToken(t *testing.T) {
	mw := RequireJWT("secret", Policy{})
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, r)

	if res.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", res.Code)
	}
}

func TestRequireJWTRejectsEmptySecret(t *testing.T) {
	mw := RequireJWT("", Policy{})
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer anything")
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, r)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", res.Code)
	}
}

func TestRequireJWTRejectsMissingRole(t *testing.T) {
	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"roles": []string{"viewer"},
	})
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mw := RequireJWT(secret, Policy{Roles: []string{"admin"}})
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+tokenString)
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(res, r)

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestRequireJWTCustomClaims(t *testing.T) {
	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "acme",
		"exp":       time.Now().Add(time.Minute).Unix(),
	})
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mw := RequireJWT(secret, Policy{Claims: map[string]any{"tenant_id": "acme"}})
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+tokenString)
	res := httptest.NewRecorder()

	mw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})).ServeHTTP(res, r)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
}

func TestRequireRoleAllowsAuthorizedIdentity(t *testing.T) {
	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"roles": []string{"admin", "ops"},
		"exp":   time.Now().Add(time.Minute).Unix(),
	})
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mw := RequireJWT(secret, Policy{})
	roleMw := RequireRole("admin")
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+tokenString)
	res := httptest.NewRecorder()

	mw(roleMw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))).ServeHTTP(res, r)

	if res.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", res.Code)
	}
}

func TestRequirePermissionRejectsMissingPermission(t *testing.T) {
	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"permissions": []string{"orders:read"},
		"exp":         time.Now().Add(time.Minute).Unix(),
	})
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mw := RequireJWT(secret, Policy{})
	permMw := RequirePermission("orders:write")
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+tokenString)
	res := httptest.NewRecorder()

	mw(permMw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))).ServeHTTP(res, r)

	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", res.Code)
	}
}

func TestRequireAccessMatchesCustomABACClaim(t *testing.T) {
	secret := "super-secret"
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"tenant_id": "acme",
		"region":    "us-east",
		"exp":       time.Now().Add(time.Minute).Unix(),
	})
	tokenString, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	mw := RequireJWT(secret, Policy{})
	accessMw := RequireAccess(AccessPolicy{Claims: map[string]any{"tenant_id": "acme", "region": "us-east"}})
	r := httptest.NewRequest(http.MethodGet, "/secure", nil)
	r.Header.Set("Authorization", "Bearer "+tokenString)
	res := httptest.NewRecorder()

	mw(accessMw(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))).ServeHTTP(res, r)

	if res.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", res.Code)
	}
}

func TestClaimMatches(t *testing.T) {
	if !claimMatches("acme", "acme") {
		t.Fatal("expected identical strings to match")
	}
	if claimMatches("acme", "beta") {
		t.Fatal("expected different strings to not match")
	}
	if !claimMatches(float64(42), 42) {
		t.Fatal("expected numerically equal values of different types to match")
	}
	if !claimMatches(nil, nil) {
		t.Fatal("expected nil to match nil")
	}
	if claimMatches(nil, "acme") {
		t.Fatal("expected nil to not match a non-nil value")
	}
	if claimMatches("acme", nil) {
		t.Fatal("expected a non-nil value to not match nil")
	}
	if claimMatches(struct{}{}, 42) {
		t.Fatal("expected incompatible types to not match")
	}
}

func TestAsFloat64(t *testing.T) {
	cases := []any{
		float32(1), float64(1), int(1), int8(1), int16(1), int32(1), int64(1),
		uint(1), uint8(1),
	}
	for _, v := range cases {
		got, ok := asFloat64(v)
		if !ok || got != 1 {
			t.Fatalf("asFloat64(%#v) = (%v, %v), want (1, true)", v, got, ok)
		}
	}
	if _, ok := asFloat64("not a number"); ok {
		t.Fatal("expected asFloat64 to reject non-numeric types")
	}
}

func TestPolicyRolesAndScopesClaimNameOverrides(t *testing.T) {
	policy := Policy{RolesClaim: "groups", ScopesClaim: "permissions"}
	if got := policy.rolesClaimName(); got != "groups" {
		t.Fatalf("rolesClaimName() = %q, want groups", got)
	}
	if got := policy.scopesClaimName(); got != "permissions" {
		t.Fatalf("scopesClaimName() = %q, want permissions", got)
	}

	defaultPolicy := Policy{}
	if got := defaultPolicy.rolesClaimName(); got != "roles" {
		t.Fatalf("default rolesClaimName() = %q, want roles", got)
	}
	if got := defaultPolicy.scopesClaimName(); got != "scope" {
		t.Fatalf("default scopesClaimName() = %q, want scope", got)
	}
}

func TestPolicyCheckBranches(t *testing.T) {
	t.Run("missing roles claim", func(t *testing.T) {
		policy := Policy{Roles: []string{"admin"}}
		if err := policy.Check(map[string]any{}); err == nil {
			t.Fatal("expected a missing roles claim to fail")
		}
	})
	t.Run("role mismatch", func(t *testing.T) {
		policy := Policy{Roles: []string{"admin"}}
		if err := policy.Check(map[string]any{"roles": []any{"viewer"}}); err == nil {
			t.Fatal("expected a role mismatch to fail")
		}
	})
	t.Run("missing scopes claim", func(t *testing.T) {
		policy := Policy{Scopes: []string{"orders:read"}}
		if err := policy.Check(map[string]any{}); err == nil {
			t.Fatal("expected a missing scopes claim to fail")
		}
	})
	t.Run("scope mismatch", func(t *testing.T) {
		policy := Policy{Scopes: []string{"orders:read"}}
		if err := policy.Check(map[string]any{"scope": "orders:write"}); err == nil {
			t.Fatal("expected a scope mismatch to fail")
		}
	})
	t.Run("missing custom claim", func(t *testing.T) {
		policy := Policy{Claims: map[string]any{"tenant_id": "acme"}}
		if err := policy.Check(map[string]any{}); err == nil {
			t.Fatal("expected a missing custom claim to fail")
		}
	})
	t.Run("custom claim mismatch", func(t *testing.T) {
		policy := Policy{Claims: map[string]any{"tenant_id": "acme"}}
		if err := policy.Check(map[string]any{"tenant_id": "beta"}); err == nil {
			t.Fatal("expected a custom claim mismatch to fail")
		}
	})
	t.Run("satisfied policy passes", func(t *testing.T) {
		policy := Policy{Roles: []string{"admin"}, Scopes: []string{"orders:read"}, Claims: map[string]any{"tenant_id": "acme"}}
		claims := map[string]any{"roles": []string{"admin"}, "scope": "orders:read", "tenant_id": "acme"}
		if err := policy.Check(claims); err != nil {
			t.Fatalf("expected a satisfied policy to pass, got %v", err)
		}
	})
}

func TestContextClaimsWithoutJWTMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if claims := ContextClaims(req); claims != nil {
		t.Fatalf("expected nil claims without the JWT middleware, got %#v", claims)
	}
	if claims := ContextClaims(nil); claims != nil {
		t.Fatalf("expected nil claims for a nil request, got %#v", claims)
	}
}

func TestRequireRoleAndPermissionRequireJWTClaims(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	res := httptest.NewRecorder()
	RequireRole("admin")(next).ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without claims, got %d", res.Code)
	}

	res2 := httptest.NewRecorder()
	RequirePermission("orders:read")(next).ServeHTTP(res2, req)
	if res2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without claims, got %d", res2.Code)
	}

	res3 := httptest.NewRecorder()
	RequireAccess(AccessPolicy{Claims: map[string]any{"tenant_id": "acme"}})(next).ServeHTTP(res3, req)
	if res3.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without claims, got %d", res3.Code)
	}
}

func TestRequireRoleAndPermissionMismatch(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	claims := map[string]any{"roles": []string{"viewer"}, "permissions": []string{"orders:write"}}
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsContextKey, claims))

	res := httptest.NewRecorder()
	RequireRole("admin")(next).ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a role mismatch, got %d", res.Code)
	}

	res2 := httptest.NewRecorder()
	RequirePermission("orders:read")(next).ServeHTTP(res2, req)
	if res2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for a permission mismatch, got %d", res2.Code)
	}

	res3 := httptest.NewRecorder()
	RequireAccess(AccessPolicy{Claims: map[string]any{"tenant_id": "acme"}})(next).ServeHTTP(res3, req)
	if res3.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for an access claim mismatch, got %d", res3.Code)
	}
}

func TestRequireRoleAndPermissionAllowedPaths(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	claims := map[string]any{"roles": []string{"admin"}, "permissions": []string{"orders:read"}}
	req := httptest.NewRequest(http.MethodGet, "/secure", nil)
	req = req.WithContext(context.WithValue(req.Context(), claimsContextKey, claims))

	res := httptest.NewRecorder()
	RequireRole("admin")(next).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("expected 200 for a matching role, got %d", res.Code)
	}

	res2 := httptest.NewRecorder()
	RequirePermission("orders:read")(next).ServeHTTP(res2, req)
	if res2.Code != http.StatusOK {
		t.Fatalf("expected 200 for a matching permission, got %d", res2.Code)
	}
}

func TestBearerTokenBranches(t *testing.T) {
	if _, err := bearerToken(nil); err == nil {
		t.Fatal("expected a nil request to fail bearer token extraction")
	}
	if _, err := bearerToken(httptest.NewRequest(http.MethodGet, "/x", nil)); err == nil {
		t.Fatal("expected a missing authorization header to fail")
	}
	bad := httptest.NewRequest(http.MethodGet, "/x", nil)
	bad.Header.Set("Authorization", "Basic abc")
	if _, err := bearerToken(bad); err == nil {
		t.Fatal("expected a non-bearer scheme to fail")
	}
	empty := httptest.NewRequest(http.MethodGet, "/x", nil)
	empty.Header.Set("Authorization", "Bearer ")
	if _, err := bearerToken(empty); err == nil {
		t.Fatal("expected an empty bearer token to fail")
	}
	good := httptest.NewRequest(http.MethodGet, "/x", nil)
	good.Header.Set("Authorization", "Bearer  token-123 ")
	got, err := bearerToken(good)
	if err != nil || got != "token-123" {
		t.Fatalf("bearerToken() = (%q, %v), want (token-123, nil)", got, err)
	}
}

func TestNormalizeStringSliceBranches(t *testing.T) {
	if got := normalizeStringSlice(""); got != nil {
		t.Fatalf("normalizeStringSlice(\"\") = %#v, want nil", got)
	}
	if got := normalizeStringSlice("a b c"); len(got) != 3 {
		t.Fatalf("normalizeStringSlice(\"a b c\") = %#v, want 3 fields", got)
	}
	if got := normalizeStringSlice([]string{"a", "b"}); len(got) != 2 {
		t.Fatalf("normalizeStringSlice([]string) = %#v, want 2 items", got)
	}
	if got := normalizeStringSlice([]any{"a", 1, "b"}); len(got) != 2 {
		t.Fatalf("normalizeStringSlice([]any) = %#v, want 2 string items", got)
	}
	if got := normalizeStringSlice(42); got != nil {
		t.Fatalf("normalizeStringSlice(42) = %#v, want nil", got)
	}
}

func TestHasAnyValueAndScope(t *testing.T) {
	if !hasAnyValue("anything", nil) {
		t.Fatal("expected an empty expectation to always match")
	}
	if !hasAnyScope("anything", nil) {
		t.Fatal("expected an empty expectation to always match")
	}
	if !hasAnyValue([]string{"admin"}, []string{"viewer", "admin"}) {
		t.Fatal("expected a matching role to be found")
	}
	if hasAnyValue(42, []string{"admin"}) {
		t.Fatal("expected a non-string value to not match")
	}
}
