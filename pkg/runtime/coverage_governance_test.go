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
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func TestGovernanceCoverageArtifactAndAuthenticationFailures(t *testing.T) {
	const secret = "coverage-secret"

	provenance := NewArtifactProvenance("release", "v1")
	provenance.SetContent("signed payload")
	provenance.Sign(secret, "build")
	provenance.Digest = ""
	if err := provenance.Verify(secret); err == nil || !strings.Contains(err.Error(), "digest is empty") {
		t.Fatalf("Verify with an empty digest error = %v", err)
	}
	provenance.Sign(secret, "build")
	provenance.Signature = hmacSignature(secret, provenance.Content)
	provenance.Digest = "not-the-content-digest"
	if err := provenance.Verify(secret); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("Verify with a correctly signed but altered digest error = %v", err)
	}
	provenance.Sign(secret, "build")
	provenance.Signature = "incorrect-signature"
	if err := provenance.Verify(secret); err == nil || !strings.Contains(err.Error(), "signature mismatch") {
		t.Fatalf("Verify with an altered signature error = %v", err)
	}

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	makeRequest := func(token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		recorder := httptest.NewRecorder()
		RequireJWT(secret, Policy{})(next).ServeHTTP(recorder, req)
		return recorder
	}

	if got := makeRequest("not.a.jwt").Code; got != http.StatusUnauthorized {
		t.Fatalf("malformed JWT status = %d, want 401", got)
	}
	unsigned, err := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "operator"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("create unsigned JWT: %v", err)
	}
	if got := makeRequest(unsigned).Code; got != http.StatusUnauthorized {
		t.Fatalf("unsigned JWT status = %d, want 401", got)
	}
	valid, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "operator"}).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("create signed JWT: %v", err)
	}
	if got := makeRequest(valid).Code; got != http.StatusOK {
		t.Fatalf("valid JWT status = %d, want 200", got)
	}
	if _, ok := jwtClaimsMap(jwt.RegisteredClaims{}); ok {
		t.Fatal("registered claims unexpectedly converted to map claims")
	}
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer injected-token")
	recorder := httptest.NewRecorder()
	requireJWT(secret, Policy{}, func(string, jwt.Keyfunc, ...jwt.ParserOption) (*jwt.Token, error) {
		return &jwt.Token{Claims: &jwt.RegisteredClaims{}, Valid: true}, nil
	})(next).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("non-map JWT claims status = %d, want 401", recorder.Code)
	}
}

func TestGovernanceCoverageRegistryValidationEdges(t *testing.T) {
	var nilRoutes *RouteRegistry
	if err := nilRoutes.Register(RouteConfig{}); err == nil {
		t.Fatal("nil route registry accepted a route")
	}
	if _, ok := nilRoutes.Get("missing"); ok || nilRoutes.List() != nil {
		t.Fatal("nil route registry returned route data")
	}
	if err := nilRoutes.RegisterPolicy(RoutePolicy{}); err == nil {
		t.Fatal("nil route registry accepted a policy")
	}
	if _, ok := nilRoutes.GetPolicy("missing"); ok || nilRoutes.ListPolicies() != nil {
		t.Fatal("nil route registry returned policy data")
	}

	registry := NewRouteRegistry()
	for name, cfg := range map[string]RouteConfig{
		"root without method": {Name: "root", Path: "/", Target: "http://localhost"},
		"unknown upstream":    {Name: "unknown-upstream", Path: "/api", Upstream: "missing", Enabled: true},
		"disabled no weight":  {Name: "disabled-empty", Path: "/disabled", Target: "http://localhost", Enabled: false},
		"disabled weighted":   {Name: "disabled-weighted", Path: "/weighted", Target: "http://localhost", Enabled: false, Weight: 10},
	} {
		if err := registry.Register(cfg); err == nil {
			t.Errorf("Register(%s) unexpectedly succeeded", name)
		}
	}
	if err := registry.Register(RouteConfig{Name: "ready", Method: "GET", Path: "/ready", Target: "http://localhost", Enabled: true}); err != nil {
		t.Fatalf("register route: %v", err)
	}
	if err := registry.Register(RouteConfig{Name: "ready", Method: "GET", Path: "/other", Target: "http://localhost", Enabled: true}); err == nil {
		t.Fatal("duplicate route name was accepted")
	}

	if err := registry.RegisterPolicy(RoutePolicy{Name: "policy", Service: "svc"}); err != nil {
		t.Fatalf("register policy with defaults: %v", err)
	}
	registry.policies = nil
	if err := registry.RegisterPolicy(RoutePolicy{Name: "policy", Service: "svc"}); err != nil {
		t.Fatalf("register policy after map reset: %v", err)
	}
	if err := registry.RegisterPolicy(RoutePolicy{Name: "policy-after-reset", Service: "svc"}); err != nil {
		t.Fatalf("register policy after map reset: %v", err)
	}
	if policy, ok := registry.GetPolicy(" policy "); !ok || policy.Environment != "prod" || policy.SLO == nil {
		t.Fatalf("registered policy did not receive defaults: %+v, %v", policy, ok)
	}
	if err := registry.RegisterPolicy(RoutePolicy{Name: "policy", Service: "svc"}); err == nil {
		t.Fatal("duplicate policy name was accepted")
	}

	if err := (*RouteRegistry)(nil).RegisterTenant(TenantConfig{}); err == nil {
		t.Fatal("nil registry accepted tenant")
	}
	for _, cfg := range []TenantConfig{
		{Name: " "},
		{Name: "disabled"},
	} {
		if err := registry.RegisterTenant(cfg); err == nil {
			t.Errorf("RegisterTenant(%+v) unexpectedly succeeded", cfg)
		}
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "acme", Enabled: true, AllowedRoutes: []string{" ready "}, AllowedRegions: []string{" east "}}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "acme", Enabled: true}); err == nil {
		t.Fatal("duplicate tenant was accepted")
	}
	registry.tenants = nil
	if err := registry.RegisterTenant(TenantConfig{Name: "beta", Enabled: true}); err != nil {
		t.Fatalf("register tenant after map reset: %v", err)
	}
	if err := registry.RegisterTenant(TenantConfig{Name: "acme", Enabled: true, AllowedRoutes: []string{"ready"}, AllowedRegions: []string{"east"}}); err != nil {
		t.Fatal(err)
	}
	registry.tenants["disabled"] = TenantConfig{Name: "disabled", Enabled: false, AllowedRoutes: []string{"ready"}}
	if registry.CanAccessRoute("missing", "ready") || registry.CanAccessRoute("acme", "missing") {
		t.Fatal("tenant route access unexpectedly allowed")
	}
	if registry.CanAccessRoute("disabled", "ready") {
		t.Fatal("disabled tenant was allowed to access route")
	}
	if !registry.CanAccessRoute("acme", "ready") || !registry.CanAccessRegion("acme", "east") {
		t.Fatal("configured tenant access was denied")
	}
	if registry.CanAccessRegion("acme", "west") || (*RouteRegistry)(nil).CanAccessRegion("acme", "east") {
		t.Fatal("tenant region access unexpectedly allowed")
	}
	if (*RouteRegistry)(nil).CanAccessRoute("acme", "ready") {
		t.Fatal("nil tenant registry allowed route access")
	}
}

func TestGovernanceCoverageRegionAndRolloutEdges(t *testing.T) {
	var nilRegistry *RouteRegistry
	if _, ok := nilRegistry.SelectRegion("east"); ok {
		t.Fatal("nil registry selected a region")
	}
	if _, ok := nilRegistry.EvaluateRegion("east", RegionMetrics{}); ok {
		t.Fatal("nil registry evaluated a region")
	}
	if err := nilRegistry.RegisterRegion(RegionConfig{}); err == nil {
		t.Fatal("nil registry accepted a region")
	}
	if _, ok := nilRegistry.GetRegion("east"); ok || nilRegistry.ListRegions() != nil {
		t.Fatal("nil registry returned region data")
	}

	registry := NewRouteRegistry()
	for _, cfg := range []RegionConfig{
		{Region: "east", Target: "http://east", Enabled: true},
		{Name: "east", Target: "http://east", Enabled: true},
		{Name: "east", Region: "east-1", Enabled: true},
		{Name: "disabled", Region: "east-1", Target: "http://east", Enabled: false},
	} {
		if err := registry.RegisterRegion(cfg); err == nil {
			t.Errorf("RegisterRegion(%+v) unexpectedly succeeded", cfg)
		}
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "east", Region: "east-1", Target: "http://east", Enabled: true, Healthy: false}); err != nil {
		t.Fatalf("register degraded primary: %v", err)
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "west", Region: "west-1", Target: "http://west", Enabled: true, Healthy: true, Weight: -1}); err != nil {
		t.Fatalf("register healthy fallback: %v", err)
	}
	if west, ok := registry.GetRegion("west"); !ok || west.Weight != 100 {
		t.Fatalf("negative region weight normalization = %+v %v", west, ok)
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "west", Region: "west-2", Target: "http://west-2", Enabled: true}); err == nil {
		t.Fatal("duplicate region name was accepted")
	}
	if got, ok := registry.SelectRegion(" east "); !ok || got.Name != "west" {
		t.Fatalf("SelectRegion fallback = %+v, %v", got, ok)
	}
	if got, ok := registry.SelectRegion("unknown"); !ok || got.Name != "west" {
		t.Fatalf("SelectRegion for unknown primary = %+v, %v", got, ok)
	}
	if _, ok := registry.SelectRegion("absent-without-fallback"); !ok {
		t.Fatal("configured healthy fallback was not selected")
	}
	if _, ok := registry.GetRegion(" absent "); ok {
		t.Fatal("GetRegion failed lookup or trim behavior")
	}
	if got, ok := registry.GetRegion(" west "); !ok || got.Name != "west" {
		t.Fatalf("GetRegion trim behavior = %+v, %v", got, ok)
	}
	if got, ok := registry.SelectRegion("west"); !ok || got.Name != "west" {
		t.Fatalf("SelectRegion healthy primary = %+v, %v", got, ok)
	}
	if len(registry.ListRegions()) != 2 {
		t.Fatalf("ListRegions returned %d entries, want 2", len(registry.ListRegions()))
	}
	if _, ok := (*RouteRegistry)(nil).GetRegion("east"); ok || (*RouteRegistry)(nil).ListRegions() != nil {
		t.Fatal("nil region registry returned data")
	}
	if decision, ok := registry.EvaluateRegion("missing", RegionMetrics{}); ok || decision.Allow {
		t.Fatalf("EvaluateRegion for missing primary = %+v, %v", decision, ok)
	}
	if err := registry.RegisterRegion(RegionConfig{Name: "disabled-primary", Region: "x", Target: "http://x", Enabled: false}); err == nil {
		t.Fatal("disabled region was accepted")
	}

	noFallback := NewRouteRegistry()
	if err := noFallback.RegisterRegion(RegionConfig{Name: "primary", Region: "p", Target: "http://p", Enabled: true, Healthy: false}); err != nil {
		t.Fatal(err)
	}
	if decision, ok := noFallback.EvaluateRegion("primary", RegionMetrics{LatencyMS: 250}); !ok || decision.Allow {
		t.Fatalf("degraded region without fallback = %+v, %v", decision, ok)
	}
	if decision, ok := noFallback.EvaluateRegion("primary", RegionMetrics{}); !ok || decision.Allow {
		t.Fatalf("unhealthy region without fallback = %+v, %v", decision, ok)
	}
	if got, ok := noFallback.SelectRegion("primary"); !ok || got.Name != "primary" {
		t.Fatalf("SelectRegion should retain the configured primary when no fallback exists: %+v %v", got, ok)
	}
	if _, ok := NewRouteRegistry().SelectRegion("unknown"); ok {
		t.Fatal("SelectRegion found unknown primary without a fallback")
	}
	acceptable := NewRouteRegistry()
	if err := acceptable.RegisterRegion(RegionConfig{Name: "primary", Region: "p", Target: "http://p", Enabled: true, Healthy: true}); err != nil {
		t.Fatal(err)
	}
	if decision, ok := acceptable.EvaluateRegion("primary", RegionMetrics{LatencyMS: 250}); !ok || !decision.Allow || decision.Reason != "primary region remains acceptable" {
		t.Fatalf("acceptable degraded metrics decision = %+v %v", decision, ok)
	}
	registry.regions = nil
	if err := registry.RegisterRegion(RegionConfig{Name: "reinitialized", Region: "r", Target: "http://r", Enabled: true}); err != nil {
		t.Fatalf("register region after map reset: %v", err)
	}

	config := RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", Enabled: true}
	if err := config.Validate(); err != nil {
		t.Fatalf("default rollout config should validate: %v", err)
	}
	config = RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, CanaryWeight: 101, MaxWeight: 100, Enabled: true}
	if err := config.Validate(); err == nil {
		t.Fatal("rollout above max weight was accepted")
	}
	config = RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Progressive: true, Enabled: true}
	if err := config.Validate(); err == nil {
		t.Fatal("progressive rollout without canary weight was accepted")
	}
	config = RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, ErrorThreshold: -0.1, Enabled: true}
	if err := config.Validate(); err == nil {
		t.Fatal("negative error threshold was accepted")
	}
	config = RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", ErrorThreshold: 10, Enabled: true}
	if err := config.Validate(); err != nil || config.ErrorThreshold != 0.1 {
		t.Fatalf("percentage threshold normalization: cfg=%+v err=%v", config, err)
	}
	if _, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyRollback, Target: "http://canary"}).Decide(1); err == nil {
		t.Fatal("invalid rollback config unexpectedly validated")
	}
	if decision, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyRollback, Target: "http://x", Enabled: true}).Decide(1); err != nil || decision.Allow {
		t.Fatalf("rollback strategy decision = %+v, %v", decision, err)
	}
	if decision, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyRollback, Target: "http://x", Enabled: true}).Evaluate(RolloutMetrics{}); err != nil || decision.Allow {
		t.Fatalf("rollback strategy evaluation = %+v, %v", decision, err)
	}
	if decision, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", Enabled: true}).Decide(-1); err != nil || decision.Allow {
		t.Fatalf("inactive rollout decision = %+v, %v", decision, err)
	}
	if decision, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", CanaryWeight: 30, Progressive: true, Enabled: true}).Decide(5); err != nil || decision.Weight != 5 {
		t.Fatalf("progressive rollout decision = %+v, %v", decision, err)
	}
	if decision, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", CanaryWeight: 20, ErrorThreshold: 0.05, Enabled: true}).Evaluate(RolloutMetrics{ErrorRate: 0.06}); err != nil || decision.Allow {
		t.Fatalf("unsafe rollout evaluation = %+v, %v", decision, err)
	}
	if decision, err := (RolloutConfig{Name: "rollout", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", CanaryWeight: 20, Enabled: true}).Evaluate(RolloutMetrics{Requests: 15}); err != nil || !decision.Allow || decision.Weight != 20 {
		t.Fatalf("safe rollout evaluation = %+v, %v", decision, err)
	}

	var nilRollouts *RolloutRegistry
	if err := nilRollouts.RegisterRollout(RolloutConfig{}); err == nil {
		t.Fatal("nil rollout registry accepted config")
	}
	if _, ok := nilRollouts.GetRollout("x"); ok {
		t.Fatal("nil rollout registry returned data")
	}
	if err := registry.RegisterRollout(RolloutConfig{Name: "bad", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", CanaryWeight: 101, MaxWeight: 100, Enabled: true}); err == nil {
		t.Fatal("route registry accepted invalid rollout")
	}
	validRollout := RolloutConfig{Name: "registered", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", CanaryWeight: 10, Enabled: true}
	if err := registry.RegisterRollout(validRollout); err != nil {
		t.Fatalf("route registry rejected valid rollout: %v", err)
	}
	if err := registry.RegisterRollout(validRollout); err == nil {
		t.Fatal("route registry accepted duplicate rollout")
	}
	if _, ok := registry.GetRollout("missing"); ok {
		t.Fatal("route registry returned missing rollout")
	}
	standalone := NewRolloutRegistry()
	if err := standalone.RegisterRollout(validRollout); err != nil {
		t.Fatalf("standalone rollout registry rejected valid config: %v", err)
	}
	if err := standalone.RegisterRollout(validRollout); err == nil {
		t.Fatal("standalone rollout registry accepted duplicate")
	}
	if err := standalone.RegisterRollout(RolloutConfig{Name: "invalid", Route: "ready", Strategy: RolloutStrategyCanary, Target: "http://canary", CanaryWeight: 101, MaxWeight: 100, Enabled: true}); err == nil {
		t.Fatal("standalone rollout registry accepted invalid config")
	}
	if _, ok := (*RouteRegistry)(nil).GetRollout("missing"); ok {
		t.Fatal("nil route registry returned rollout")
	}
}

func TestGovernanceCoverageSecretAndIdentityRequestEdges(t *testing.T) {
	if got := ContextSecret(nil, "key"); got != "" {
		t.Fatalf("ContextSecret(nil) = %q", got)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := ContextSecret(req, "missing"); got != "" {
		t.Fatalf("missing context secret = %q", got)
	}
	req = req.WithContext(withCoverageContextValue(req, secretContextKey{}, "not a secret map"))
	if got := ContextSecret(req, "key"); got != "" {
		t.Fatalf("ContextSecret with unexpected context value = %q", got)
	}

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.TLS = &tls.ConnectionState{}
	if got := clientIdentityFromRequest(req); got != nil {
		t.Fatalf("request with no peer cert produced identity: %+v", got)
	}
	identityURI, err := url.Parse("spiffe://cluster/ns/prod/sa/coverage")
	if err != nil {
		t.Fatal(err)
	}
	req.TLS.PeerCertificates = []*x509.Certificate{{URIs: []*url.URL{identityURI}}}
	if got := clientIdentityFromRequest(req); got == nil || len(got.URIs) != 1 || got.URIs[0] != identityURI.String() {
		t.Fatalf("client URI identity = %+v", got)
	}
	recorder := httptest.NewRecorder()
	RequireClientIdentity(IdentityPolicy{AllowedCommonNames: []string{"service"}})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("request without client certificate reached handler")
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing client certificate status = %d", recorder.Code)
	}
}

func withCoverageContextValue(req *http.Request, key, value any) context.Context {
	return context.WithValue(req.Context(), key, value)
}

func TestGovernanceCoverageTenantQuotaPrunesExpiredEntries(t *testing.T) {
	middleware := TenantQuotaMiddleware(1, time.Nanosecond)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	request := func(tenant string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("X-Tenant-ID", tenant)
		recorder := httptest.NewRecorder()
		middleware(next).ServeHTTP(recorder, req)
		return recorder.Code
	}
	if got := request("stale"); got != http.StatusNoContent {
		t.Fatalf("initial tenant request status = %d", got)
	}
	for i := 0; i < 254; i++ {
		if got := request("other-" + strconv.Itoa(i)); got != http.StatusNoContent {
			t.Fatalf("request %d while priming cleanup status = %d", i, got)
		}
	}
	time.Sleep(time.Millisecond)
	if got := request("cleanup-trigger"); got != http.StatusNoContent {
		t.Fatalf("cleanup-trigger request status = %d", got)
	}
}

type coverageDeploymentStore struct {
	state    DeploymentLedgerState
	loadErr  error
	saveErr  error
	closeErr error
}

func (s *coverageDeploymentStore) Save(state DeploymentLedgerState) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.state = state
	return nil
}

func (s *coverageDeploymentStore) Load() (DeploymentLedgerState, error) {
	return s.state, s.loadErr
}

func (s *coverageDeploymentStore) Close() error { return s.closeErr }

type coveragePolicyStore struct {
	state    PolicyVersionState
	loadErr  error
	saveErr  error
	closeErr error
}

func (s *coveragePolicyStore) Save(state PolicyVersionState) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.state = state
	return nil
}

func (s *coveragePolicyStore) Load() (PolicyVersionState, error) {
	return s.state, s.loadErr
}

func (s *coveragePolicyStore) Close() error { return s.closeErr }

func TestGovernanceCoverageControlPlaneAccessorsAndStoreErrors(t *testing.T) {
	var nilCP *ControlPlaneServer
	if nilCP.PolicyVersioner() != nil || nilCP.Registry() != nil || nilCP.Ledger() != nil || nilCP.StorePath() != "" {
		t.Fatal("nil control plane returned configured state")
	}
	if err := nilCP.Reload(); err == nil {
		t.Fatal("nil control plane Reload unexpectedly succeeded")
	}

	dir := t.TempDir()
	badLedgerPath := filepath.Join(dir, "ledger")
	if err := os.Mkdir(badLedgerPath, 0o700); err != nil {
		t.Fatal(err)
	}
	cp := NewControlPlaneServer(ControlPlaneConfig{StorePath: badLedgerPath})
	if err := cp.Reload(); err == nil {
		t.Fatal("reload from directory unexpectedly succeeded")
	}
	if got := cp.StorePath(); got != badLedgerPath {
		t.Fatalf("StorePath() = %q, want %q", got, badLedgerPath)
	}
	if err := cp.Close(); err != nil {
		t.Fatalf("Close() after restore error: %v", err)
	}

	deploymentStore := &coverageDeploymentStore{loadErr: errors.New("load unavailable")}
	policyStore := &coveragePolicyStore{loadErr: errors.New("policy load unavailable")}
	cp = NewControlPlaneServerWithStores(ControlPlaneConfig{}, deploymentStore, policyStore)
	if got := cp.StorePath(); got != "" {
		t.Fatalf("custom store path = %q, want empty", got)
	}
	if err := cp.Reload(); err == nil {
		t.Fatal("Reload hid the injected load failure")
	}
	cp.workflowGates = nil
	cp.SetWorkflowGate(" ", "prod", 2)
	cp.SetWorkflowGate("svc", "prod", 0)
	if cp.workflowGates[workflowGateKey("svc", "prod")] != 1 {
		t.Fatalf("SetWorkflowGate did not initialize/default gates: %v", cp.workflowGates)
	}
	cp.Close()

	storeCloseErr := errors.New("deployment close failed")
	policyCloseErr := errors.New("policy close failed")
	cp = NewControlPlaneServerWithStores(ControlPlaneConfig{}, &coverageDeploymentStore{closeErr: storeCloseErr}, &coveragePolicyStore{closeErr: policyCloseErr})
	if err := cp.Close(); !errors.Is(err, storeCloseErr) {
		t.Fatalf("Close() error = %v, want deployment store error", err)
	}
	cp = NewControlPlaneServerWithStores(ControlPlaneConfig{}, nil, &coveragePolicyStore{closeErr: policyCloseErr})
	if err := cp.Close(); !errors.Is(err, policyCloseErr) {
		t.Fatalf("Close() error = %v, want policy store error", err)
	}

	handler := NewControlPlaneServer(ControlPlaneConfig{}).Handler()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/routes", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("missing AdminToken status = %d", recorder.Code)
	}
	cp = NewControlPlaneServer(ControlPlaneConfig{AdminToken: "expected"})
	recorder = httptest.NewRecorder()
	cp.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unauthorized request reached handler")
	})).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer token status = %d", recorder.Code)
	}
	recorder = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Basic expected")
	cp.requireAuth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("non-bearer request reached handler")
	})).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong auth scheme status = %d", recorder.Code)
	}
	if got := splitPath("/"); len(got) != 1 || got[0] != "" {
		t.Fatalf("splitPath root = %#v", got)
	}
}

func TestGovernanceCoverageControlPlaneHandlerFailures(t *testing.T) {
	cp := NewControlPlaneServer(ControlPlaneConfig{AdminToken: "token"})
	defer cp.Close()
	call := func(handler func(http.ResponseWriter, *http.Request, []string), body string, parts ...string) *httptest.ResponseRecorder {
		t.Helper()
		recorder := httptest.NewRecorder()
		handler(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), parts)
		return recorder
	}

	if got := call(cp.updatePolicy, "{", "missing").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed policy update status = %d", got)
	}
	if got := call(cp.updatePolicy, `{"service":"svc","environment":"prod","actor":"operator"}`, "missing").Code; got != http.StatusNotFound {
		t.Fatalf("unknown policy update status = %d", got)
	}
	if got := call(cp.updatePolicy, `{"service":"svc","environment":"prod"}`, "missing").Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("invalid policy update status = %d", got)
	}
	if got := call(cp.rollbackPolicy, "{", "missing").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed policy rollback status = %d", got)
	}
	if got := call(cp.rollbackPolicy, `{"version":1,"actor":"operator"}`, "missing").Code; got != http.StatusNotFound {
		t.Fatalf("rollback missing policy status = %d", got)
	}
	if got := call(cp.createDeployment, `{}`, "").Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("invalid deployment status = %d", got)
	}
	if got := call(cp.createDeployment, "{", "").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed deployment status = %d", got)
	}
	if got := call(cp.createRelease, "{").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed release status = %d", got)
	}
	if got := call(cp.approveRelease, "{", "missing", "prod", "v1").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed approval status = %d", got)
	}
	if got := call(cp.rollbackRelease, `{"actor":"operator"}`, "missing", "prod", "v1").Code; got != http.StatusNotFound {
		t.Fatalf("unknown rollback release status = %d", got)
	}
	if got := call(cp.rollbackRelease, "{", "missing", "prod", "v1").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed rollback request status = %d", got)
	}
	if got := call(cp.rejectRelease, `{"actor":"operator"}`, "missing", "prod", "v1").Code; got != http.StatusNotFound {
		t.Fatalf("unknown reject release status = %d", got)
	}
	if got := call(cp.rejectRelease, "{", "missing", "prod", "v1").Code; got != http.StatusBadRequest {
		t.Fatalf("malformed reject request status = %d", got)
	}
	if got := call(cp.createRelease, `{"service":"reject-me","environment":"staging","version":"v1"}`).Code; got != http.StatusCreated {
		t.Fatalf("setup reject workflow status = %d", got)
	}
	if got := call(cp.rejectRelease, `{"actor":""}`, "reject-me", "staging", "v1").Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("reject without actor status = %d", got)
	}
	if err := NewControlPlaneServer(ControlPlaneConfig{}).persist(); err != nil {
		t.Fatalf("persist without store should be a no-op: %v", err)
	}

	if err := cp.policyVersions.Create(RoutePolicy{Name: "present", Service: "svc", Environment: "prod"}, "operator", "created"); err != nil {
		t.Fatal(err)
	}
	if err := cp.policyVersions.Create(RoutePolicy{Name: "present", Service: "svc", Environment: "prod"}, "operator", "duplicate"); err == nil {
		t.Fatal("duplicate versioned policy unexpectedly created")
	}
	if got := call(cp.rollbackPolicy, `{"version":99,"actor":"operator"}`, "present").Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("out-of-range rollback status = %d", got)
	}
	if got := call(cp.updatePolicy, `{"service":"svc","environment":"other","actor":"operator"}`, "present").Code; got != http.StatusUnprocessableEntity {
		t.Fatalf("cross-environment policy update status = %d", got)
	}

	// Validating an active deployment reaches the persistence failure after the
	// ledger has accepted the staging manifest.
	failingStore := &coverageDeploymentStore{saveErr: errors.New("disk full")}
	cp = NewControlPlaneServerWithStores(ControlPlaneConfig{AdminToken: "token"}, failingStore, nil)
	recorder := call(cp.createDeployment, `{"service":"api","environment":"staging","version":"v1","audit":["change approved"]}`)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("failed deployment persistence status = %d", recorder.Code)
	}
	if _, ok := cp.ledger.Current("api", "staging"); !ok {
		t.Fatal("ledger should retain the validated in-memory deployment after save failure")
	}
	cp.Close()

	cp = NewControlPlaneServer(ControlPlaneConfig{AdminToken: "token", ApprovalRequirement: 0})
	defer cp.Close()
	recorder = call(cp.createRelease, `{"service":"api","environment":"staging","version":"v1"}`)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("create release status = %d", recorder.Code)
	}
	if wf, ok := cp.workflow("api", "staging", "v1"); !ok || wf.Gate.Required != 1 {
		t.Fatalf("default workflow gate = %+v, %v", wf, ok)
	}
	if err := cp.registry.Register(RouteConfig{
		Name: "resolved", Method: http.MethodGet, Path: "/resolved", Target: "http://localhost",
		Enabled: true, Header: map[string]string{"X-Test": "coverage"},
	}); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	cp.resolveRoute(recorder, httptest.NewRequest(http.MethodGet, "/", nil), []string{"resolved"})
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if headers, ok := response["headers"].(map[string]any); !ok || headers["X-Test"] != "coverage" {
		t.Fatalf("resolved route headers = %#v", response["headers"])
	}
}

func TestGovernanceCoverageStorageAndVersioningErrors(t *testing.T) {
	if err := (*FileDeploymentStore)(nil).Save(DeploymentLedgerState{}); err == nil {
		t.Fatal("nil deployment store saved state")
	}
	if _, err := (*FileDeploymentStore)(nil).Load(); err == nil {
		t.Fatal("nil deployment store loaded state")
	}
	if err := NewFileDeploymentStore(" ").Save(DeploymentLedgerState{}); err == nil {
		t.Fatal("empty deployment store path saved state")
	}
	if _, err := NewFileDeploymentStore(" ").Load(); err == nil {
		t.Fatal("empty deployment store path loaded state")
	}

	dir := t.TempDir()
	blockedDir := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blockedDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewFileDeploymentStore(filepath.Join(blockedDir, "ledger.json")).Save(DeploymentLedgerState{}); err == nil {
		t.Fatal("deployment store wrote beneath a regular file")
	}
	marshalFailure := errors.New("marshal failed")
	if err := (&FileDeploymentStore{
		path: filepath.Join(dir, "marshal-error.json"),
		marshal: func(DeploymentLedgerState) ([]byte, error) {
			return nil, marshalFailure
		},
	}).Save(DeploymentLedgerState{}); !errors.Is(err, marshalFailure) {
		t.Fatalf("deployment store did not propagate marshal error: %v", err)
	}
	invalidLedger := filepath.Join(dir, "invalid-ledger.json")
	if err := os.WriteFile(invalidLedger, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileDeploymentStore(invalidLedger).Load(); err == nil {
		t.Fatal("deployment store accepted malformed JSON")
	}
	readDirectory := filepath.Join(dir, "directory")
	if err := os.Mkdir(readDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := NewFileDeploymentStore(readDirectory).Save(DeploymentLedgerState{}); err == nil {
		t.Fatal("deployment store overwrote a directory")
	}
	if _, err := NewFileDeploymentStore(readDirectory).Load(); err == nil {
		t.Fatal("deployment store read a directory as state")
	}
	if err := NewFilePolicyVersionStore(" ").Save(PolicyVersionState{}); err == nil {
		t.Fatal("empty policy store path saved state")
	}
	if _, err := NewFilePolicyVersionStore(" ").Load(); err == nil {
		t.Fatal("empty policy store path loaded state")
	}
	if err := NewFilePolicyVersionStore(filepath.Join(blockedDir, "policies.json")).Save(PolicyVersionState{}); err == nil {
		t.Fatal("policy store wrote beneath a regular file")
	}
	if err := NewFilePolicyVersionStore(readDirectory).Save(PolicyVersionState{}); err == nil {
		t.Fatal("policy store renamed state over a directory")
	}
	if err := (&FilePolicyVersionStore{
		path: filepath.Join(dir, "marshal-error-policies.json"),
		marshal: func(PolicyVersionState) ([]byte, error) {
			return nil, marshalFailure
		},
	}).Save(PolicyVersionState{}); !errors.Is(err, marshalFailure) {
		t.Fatalf("policy store did not propagate marshal error: %v", err)
	}
	invalidPolicies := filepath.Join(dir, "invalid-policies.json")
	if err := os.WriteFile(invalidPolicies, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFilePolicyVersionStore(invalidPolicies).Load(); err == nil {
		t.Fatal("policy store accepted malformed JSON")
	}
	if _, err := NewFilePolicyVersionStore(readDirectory).Load(); err == nil {
		t.Fatal("policy store read a directory as state")
	}
	if state, err := NewFilePolicyVersionStore(filepath.Join(dir, "missing.json")).Load(); err != nil || state.Versions == nil {
		t.Fatalf("missing policy history state=%+v err=%v", state, err)
	}

	failure := errors.New("storage unavailable")
	pv := NewPolicyVersioner(NewRouteRegistry(), &coveragePolicyStore{loadErr: failure})
	if err := pv.Create(RoutePolicy{Name: "x", Service: "svc"}, "operator", "create"); err != nil {
		t.Fatalf("versioner should start empty when load fails: %v", err)
	}
	if err := pv.Close(); err != nil {
		t.Fatalf("versioner close without error: %v", err)
	}
	failingSave := NewPolicyVersioner(NewRouteRegistry(), &coveragePolicyStore{saveErr: failure})
	if err := failingSave.Create(RoutePolicy{Name: "persist-fail", Service: "svc"}, "operator", "create"); !errors.Is(err, failure) {
		t.Fatalf("create should report store save failure, got %v", err)
	}
	if err := (*PolicyVersioner)(nil).Create(RoutePolicy{}, "operator", "create"); err == nil {
		t.Fatal("nil versioner created policy")
	}
	if err := (*PolicyVersioner)(nil).Update(RoutePolicy{}, "operator", "update"); err == nil {
		t.Fatal("nil versioner updated policy")
	}
	if err := (*PolicyVersioner)(nil).Rollback("x", "prod", 1, "operator", "rollback"); err == nil {
		t.Fatal("nil versioner rolled back policy")
	}
	if _, err := (*PolicyVersioner)(nil).History("x", "prod"); err == nil {
		t.Fatal("nil versioner returned history")
	}

	registry := NewRouteRegistry()
	versioner := NewPolicyVersioner(registry, nil)
	if err := versioner.Create(RoutePolicy{Name: "x", Service: "svc", Environment: "prod"}, "", "create"); err == nil {
		t.Fatal("versioner accepted empty actor")
	}
	if err := versioner.Update(RoutePolicy{Name: "missing", Service: "svc", Environment: "prod"}, "operator", "update"); err == nil {
		t.Fatal("versioner updated missing policy")
	}
	if err := versioner.Create(RoutePolicy{Name: "x", Service: "svc", Environment: "prod"}, "operator", "create"); err != nil {
		t.Fatal(err)
	}
	if err := versioner.Update(RoutePolicy{Name: "x", Service: "svc", Environment: "prod", AllowedTenants: []string{"acme"}}, "operator", "updated"); err != nil {
		t.Fatalf("normal policy update failed: %v", err)
	}
	if err := versioner.Update(RoutePolicy{Name: "x", Service: "svc", Environment: "staging"}, "operator", "update"); err == nil {
		t.Fatal("versioner changed policy environment")
	}
	if err := versioner.Update(RoutePolicy{Name: "x", Service: " ", Environment: "prod"}, "operator", "update"); err == nil {
		t.Fatal("versioner accepted update without service")
	}
	if err := versioner.Update(RoutePolicy{Name: "x", Service: "svc", Environment: "prod"}, "", "update"); err == nil {
		t.Fatal("versioner accepted empty update actor")
	}
	if err := versioner.Rollback("missing", "prod", 1, "operator", "rollback"); err == nil {
		t.Fatal("versioner rolled back a policy with no history")
	}
	if err := versioner.Rollback("x", "prod", 0, "operator", "rollback"); err == nil {
		t.Fatal("versioner accepted an out-of-range rollback")
	}
	if err := versioner.Rollback("x", "prod", 1, "", "rollback"); err == nil {
		t.Fatal("versioner accepted empty rollback actor")
	}
	if err := versioner.Rollback("x", "prod", 1, "operator", "restore"); err != nil {
		t.Fatalf("valid rollback failed: %v", err)
	}
	if history, err := versioner.History("x", "prod"); err != nil || len(history) != 3 || history[2].Version != 3 {
		t.Fatalf("rollback history = %+v err=%v", history, err)
	}
	if err := versioner.replacePolicy(RoutePolicy{Name: "missing", Service: "svc"}); err == nil {
		t.Fatal("replacePolicy created a policy that was not registered")
	}
	if err := versioner.replacePolicy(RoutePolicy{Name: " ", Service: "svc"}); err == nil {
		t.Fatal("replacePolicy accepted missing name")
	}
	if err := versioner.replacePolicy(RoutePolicy{Name: "x", Service: " "}); err == nil {
		t.Fatal("replacePolicy accepted missing service")
	}
	if err := versioner.replacePolicy(RoutePolicy{Name: "x", Service: "svc"}); err != nil {
		t.Fatalf("replacePolicy did not supply optional policy defaults: %v", err)
	}
	emptyRegistry := NewRouteRegistry()
	emptyRegistry.policies = nil
	emptyVersioner := NewPolicyVersioner(emptyRegistry, nil)
	if err := emptyVersioner.replacePolicy(RoutePolicy{Name: "missing", Service: "svc"}); err == nil || emptyRegistry.policies == nil {
		t.Fatalf("replacePolicy failed nil policy-map handling: err=%v map=%v", err, emptyRegistry.policies)
	}

	restoredPolicy := RoutePolicy{Name: "restored", Environment: "prod", Service: "svc"}
	restoredStore := &coveragePolicyStore{state: PolicyVersionState{Versions: map[string][]PolicyVersionRecord{
		"restored/prod": {{PolicyName: "restored", Environment: "prod", Policy: restoredPolicy}},
		"empty/prod":    {},
	}}}
	restoredRegistry := NewRouteRegistry()
	_ = NewPolicyVersioner(restoredRegistry, restoredStore)
	if policy, ok := restoredRegistry.GetPolicy("restored"); !ok || policy.Name != "restored" {
		t.Fatalf("persisted policy was not restored: %+v %v", policy, ok)
	}
	legacyRegistry := NewRouteRegistry()
	legacyRegistry.policies["legacy"] = RoutePolicy{Name: "legacy", Service: "svc"}
	legacyVersioner := NewPolicyVersioner(legacyRegistry, nil)
	if err := legacyVersioner.Update(RoutePolicy{Name: "legacy", Service: "svc"}, "operator", "normalize"); err != nil {
		t.Fatalf("legacy policy with blank environment could not be normalized: %v", err)
	}
	failedRollback := &PolicyVersioner{
		registry: NewRouteRegistry(),
		versions: map[string][]PolicyVersionRecord{
			"orphan/prod": {{PolicyName: "orphan", Environment: "prod", Policy: RoutePolicy{Name: "orphan", Environment: "prod", Service: "svc"}}},
		},
	}
	if err := failedRollback.Rollback("orphan", "prod", 1, "operator", "rollback"); err == nil {
		t.Fatal("rollback succeeded without a registered current policy")
	}
}

func TestGovernanceCoveragePolicyDiffAndUpstreamEdges(t *testing.T) {
	if got := diffPolicies(RoutePolicy{}, RoutePolicy{}); got != "no changes" {
		t.Fatalf("identical policy diff = %q", got)
	}
	if !equalStringSlices([]string{"b", "a"}, []string{"a", "b"}) {
		t.Fatal("equalStringSlices should ignore ordering")
	}
	if equalStringSlices([]string{"a"}, []string{"a", "b"}) || equalStringSlices([]string{"a", "b"}, []string{"a", "c"}) {
		t.Fatal("equalStringSlices accepted different values")
	}
	_ = diffPolicies(RoutePolicy{SLO: NewSLOPolicy(.1, 300, .05)}, RoutePolicy{})
	_ = diffPolicies(RoutePolicy{}, RoutePolicy{SLO: NewSLOPolicy(.1, 300, .05)})

	var nilRegistry *RouteRegistry
	if err := nilRegistry.RegisterUpstream(UpstreamConfig{}); err == nil {
		t.Fatal("nil registry accepted upstream")
	}
	if _, ok := nilRegistry.GetUpstream("x"); ok || nilRegistry.ListUpstreams() != nil {
		t.Fatal("nil upstream registry returned data")
	}
	if _, ok := nilRegistry.SelectUpstream("x"); ok {
		t.Fatal("nil upstream registry selected a backend")
	}
	if _, ok := nilRegistry.PickUpstream("svc"); ok {
		t.Fatal("nil upstream registry selected a backend")
	}
	if _, ok := nilRegistry.ResolveRouteTarget("x"); ok || nilRegistry.RouteHeaders("x") != nil {
		t.Fatal("nil upstream registry resolved route")
	}
	if _, err := nilRegistry.NewProxy("x"); err == nil {
		t.Fatal("nil upstream registry created proxy")
	}

	registry := NewRouteRegistry()
	invalid := []UpstreamConfig{
		{Target: "http://localhost"},
		{Name: "missing-target"},
		{Name: "invalid-url", Target: "://"},
		{Name: "self", Target: "http://localhost", Fallback: "self"},
	}
	for _, cfg := range invalid {
		if err := registry.RegisterUpstream(cfg); err == nil {
			t.Errorf("RegisterUpstream(%+v) unexpectedly succeeded", cfg)
		}
	}
	registry.upstreams = nil
	if err := registry.RegisterUpstream(UpstreamConfig{Name: "primary", Target: "http://localhost", Enabled: true, Healthy: false, Fallback: "fallback", Weight: -1, Services: []string{" svc "}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterUpstream(UpstreamConfig{Name: "fallback", Target: "http://localhost:2", Enabled: true, Healthy: true, Services: []string{"svc"}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterUpstream(UpstreamConfig{Name: "disabled", Target: "http://localhost:3", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterUpstream(UpstreamConfig{Name: "fallback", Target: "http://localhost:4"}); err == nil {
		t.Fatal("duplicate upstream name was accepted")
	}
	if got, ok := registry.GetUpstream(" primary "); !ok || got.Weight != 1 {
		t.Fatalf("normalized upstream = %+v, %v", got, ok)
	}
	if _, ok := registry.GetUpstream("absent"); ok || len(registry.ListUpstreams()) != 3 {
		t.Fatal("upstream lookup or listing failed")
	}
	if _, ok := registry.SelectUpstream("disabled"); ok {
		t.Fatal("disabled upstream selected")
	}
	if got, ok := registry.SelectUpstream("primary"); !ok || got.Name != "fallback" {
		t.Fatalf("fallback selection = %+v, %v", got, ok)
	}
	if _, ok := registry.SelectUpstream("absent"); ok {
		t.Fatal("unknown upstream selected")
	}
	if _, ok := registry.pickForRoute("absent"); ok {
		t.Fatal("missing upstream route resolved")
	}
	if _, ok := registry.PickUpstream("missing"); ok {
		t.Fatal("upstream without matching service selected")
	}
	if got, ok := registry.PickUpstream("svc"); !ok || got.Name != "fallback" {
		t.Fatalf("service pool selection = %+v, %v", got, ok)
	}
	if !servesService(UpstreamConfig{}, "any") || servesService(UpstreamConfig{Services: []string{"other"}}, "svc") {
		t.Fatal("service matching returned incorrect result")
	}
	if !servesService(UpstreamConfig{Services: []string{" svc "}}, "svc") {
		t.Fatal("service matching did not trim service names")
	}
	if err := registry.Register(RouteConfig{Name: "legacy", Path: "/legacy", Method: "GET", Target: "http://localhost", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RouteConfig{Name: "missing-target", Path: "/missing", Method: "GET", Upstream: "primary", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got, ok := registry.ResolveRouteTarget("legacy"); !ok || got != "http://localhost" {
		t.Fatalf("legacy route target = %q, %v", got, ok)
	}
	if _, ok := registry.ResolveRouteTarget("absent"); ok {
		t.Fatal("unknown route resolved")
	}
	if got, ok := registry.ResolveRouteTarget("missing-target"); !ok || got != "http://localhost:2" {
		t.Fatalf("route fallback target = %q, %v", got, ok)
	}
	if got := registry.RouteHeaders("missing-target"); got != nil {
		t.Fatalf("route without headers returned %#v", got)
	}
	if _, err := registry.NewProxy("absent"); err == nil {
		t.Fatal("proxy creation accepted missing route")
	}
	if _, err := registry.NewProxy("legacy"); err != nil {
		t.Fatalf("proxy creation for legacy route failed: %v", err)
	}
	if err := registry.Register(RouteConfig{Name: "invalid-target", Path: "/invalid", Method: "GET", Target: "http://%zz", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.NewProxy("invalid-target"); err == nil {
		t.Fatal("proxy accepted an invalid configured target")
	}
	registry.routes["targetless"] = RouteConfig{Name: "targetless"}
	if _, ok := registry.ResolveRouteTarget("targetless"); ok {
		t.Fatal("targetless route resolved successfully")
	}
	if err := registry.RegisterUpstream(UpstreamConfig{
		Name: "drained", Target: "http://localhost:5", Fallback: "fallback",
		Services: []string{"drained-service"}, Enabled: true, Healthy: false,
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(RouteConfig{Name: "drained-route", Path: "/drained", Method: "GET", Upstream: "drained", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if got, ok := registry.ResolveRouteTarget("drained-route"); !ok || got != "http://localhost:2" {
		t.Fatalf("drained upstream fallback = %q, %v", got, ok)
	}
	if got, ok := pickWeightedUpstream([]UpstreamConfig{{Name: "first", Weight: 1}, {Name: "last", Weight: 1}}, 4); !ok || got.Name != "last" {
		t.Fatalf("weighted-selection fallback = %+v, %v", got, ok)
	}
}

func TestGovernanceCoverageGovernancePlansAndLedgers(t *testing.T) {
	if diff := BuildConfigDiff(RouteRegistrySnapshot{}, RouteRegistrySnapshot{}); diff.HasChanges || len(diff.Routes) != 0 {
		t.Fatalf("empty config diff = %+v", diff)
	}
	if err := (*ReleasePlan)(nil).Validate("secret"); err == nil {
		t.Fatal("nil release plan validated")
	}
	if err := NewReleasePlan("prod", " ", "v1").Validate("secret"); err == nil {
		t.Fatal("release without a service validated")
	}
	if (*ReleasePlan)(nil).IsReady("secret") {
		t.Fatal("nil release plan reported ready")
	}
	plan := NewReleasePlan("", "svc", "v1")
	if err := plan.Validate("secret"); err == nil {
		t.Fatal("release without gate validated")
	}
	plan.Gate = NewApprovalGate("release", 1)
	if err := plan.Validate("secret"); err == nil {
		t.Fatal("unapproved release validated")
	}
	if err := plan.Gate.Approve("operator"); err != nil {
		t.Fatal(err)
	}
	if err := plan.Validate("secret"); err == nil {
		t.Fatal("release without artifact validated")
	}
	plan.Artifact = NewArtifactProvenance("artifact", "v1")
	plan.Artifact.SetContent("payload")
	plan.Artifact.Sign("secret", "builder")
	if err := plan.Validate("wrong-secret"); err == nil {
		t.Fatal("release with invalid signature validated")
	}
	if report := NewComplianceReport(nil); report != nil {
		t.Fatal("nil plan produced compliance report")
	}
	if report := NewComplianceReport(plan); report == nil || report.Compliant {
		t.Fatalf("incomplete plan produced compliant report: %+v", report)
	}
	plan.SLO = NewSLOPolicy(0.05, 250, 0.02)
	if err := plan.Validate("secret"); err == nil {
		t.Fatal("release without rollback validated")
	}
	plan.Rollback = NewRollbackPlan("", "", nil)
	if err := plan.Validate("secret"); err == nil {
		t.Fatal("release without runbook validated")
	}
	plan.Runbook = "runbook"
	if err := plan.Validate("secret"); err != nil || !plan.IsReady("secret") {
		t.Fatalf("complete release plan validation: err=%v ready=%v", err, plan.IsReady("secret"))
	}
	if report := NewComplianceReport(plan); report == nil || !report.Compliant {
		t.Fatalf("complete plan compliance = %+v", report)
	}

	if err := (*PromotionPlan)(nil).Validate("secret"); err == nil {
		t.Fatal("nil promotion plan validated")
	}
	promotion := NewPromotionPlan("", "", "svc", "v1")
	if err := promotion.Validate("secret"); err == nil {
		t.Fatal("promotion without approval gate validated")
	}
	promotion.Gate = NewApprovalGate("promotion", 1)
	if err := promotion.Gate.Approve("operator"); err != nil {
		t.Fatal(err)
	}
	if err := promotion.Validate("secret"); err == nil {
		t.Fatal("promotion without artifact validated")
	}
	promotion.Artifact = NewArtifactProvenance("artifact", "v1")
	promotion.Artifact.SetContent("payload")
	promotion.Artifact.Sign("secret", "builder")
	if err := promotion.Validate("secret"); err == nil {
		t.Fatal("production promotion without allowlists validated")
	}
	promotion.Policy.AllowedTenants = []string{"acme"}
	promotion.Policy.AllowedRegions = []string{"east"}
	promotion.Policy.Environment = "staging"
	if err := promotion.Validate("secret"); err == nil {
		t.Fatal("promotion with mismatched target policy validated")
	}
	promotion.Policy.Environment = "prod"
	promotion.Policy.Service = "other"
	if err := promotion.Validate("secret"); err == nil {
		t.Fatal("promotion with mismatched service policy validated")
	}
	promotion.Policy.Service = "svc"
	if err := promotion.Validate("secret"); err != nil {
		t.Fatalf("valid promotion failed: %v", err)
	}
	promotionDefaults := &PromotionPlan{
		To: "staging", Service: "svc", Gate: NewApprovalGate("stage", 1),
		Artifact: NewArtifactProvenance("stage-artifact", "v1"),
	}
	promotionDefaults.Artifact.SetContent("payload")
	promotionDefaults.Artifact.Sign("secret", "builder")
	if err := promotionDefaults.Gate.Approve("operator"); err != nil {
		t.Fatal(err)
	}
	if err := promotionDefaults.Validate("secret"); err != nil {
		t.Fatalf("promotion policy defaults failed: %v", err)
	}
	if promotionDefaults.Policy.Environment != "staging" || promotionDefaults.Policy.Service != "svc" {
		t.Fatalf("promotion policy defaults = %+v", promotionDefaults.Policy)
	}

	if err := (*DeploymentManifest)(nil).Validate(); err == nil {
		t.Fatal("nil deployment manifest validated")
	}
	if err := (&DeploymentManifest{Service: " ", Environment: "staging"}).Validate(); err == nil {
		t.Fatal("deployment without service validated")
	}
	if err := (&DeploymentManifest{Service: "svc", Environment: " "}).Validate(); err == nil {
		t.Fatal("deployment without environment validated")
	}
	manifest := NewDeploymentManifest("staging", "svc", "v1")
	if err := manifest.Validate(); err == nil {
		t.Fatal("deployment without audit trail validated")
	}
	manifest.Audit = []string{"approved"}
	manifest.Policy.Service = "other"
	if err := manifest.Validate(); err == nil {
		t.Fatal("deployment with mismatched service validated")
	}
	manifest.Policy.Service = "svc"
	manifest.Policy.Environment = "prod"
	if err := manifest.Validate(); err == nil {
		t.Fatal("deployment with mismatched environment validated")
	}
	manifest.Policy.Environment = "staging"
	if err := manifest.Validate(); err != nil {
		t.Fatalf("valid staging deployment rejected: %v", err)
	}
	defaultedManifest := &DeploymentManifest{Service: "svc", Environment: "staging", Audit: []string{"approved"}}
	if err := defaultedManifest.Validate(); err != nil {
		t.Fatalf("manifest with inferred policy fields rejected: %v", err)
	}
	if defaultedManifest.Policy.Service != "svc" || defaultedManifest.Policy.Environment != "staging" {
		t.Fatalf("manifest policy defaults = %+v", defaultedManifest.Policy)
	}
	productionManifest := NewDeploymentManifest("prod", "svc", "v2")
	productionManifest.Audit = []string{"approved"}
	if err := productionManifest.Validate(); err == nil {
		t.Fatal("production manifest without tenant/region allowlists validated")
	}
	incompleteProductionManifest := &DeploymentManifest{
		Service: "svc", Environment: "prod", Audit: []string{"approved"},
		Policy: RoutePolicy{Environment: "prod", Service: "svc"},
	}
	if err := incompleteProductionManifest.Validate(); err == nil {
		t.Fatal("production manifest without required controls validated")
	}
	signedWithoutSLO := &DeploymentManifest{
		Service: "svc", Environment: "staging", Audit: []string{"approved"},
		Policy: RoutePolicy{Environment: "staging", Service: "svc", RequireSignedArtifacts: true},
	}
	if err := signedWithoutSLO.Validate(); err == nil {
		t.Fatal("signed stage policy without SLO validated")
	}

	registry := NewDeploymentManifestRegistry()
	if err := (*DeploymentManifestRegistry)(nil).Register(manifest); err == nil {
		t.Fatal("nil manifest registry accepted manifest")
	}
	if err := registry.Register(nil); err == nil {
		t.Fatal("manifest registry accepted nil manifest")
	}
	if err := registry.Register(manifest); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&DeploymentManifest{}); err == nil {
		t.Fatal("manifest registry accepted invalid manifest")
	}
	if err := registry.Register(manifest); err == nil {
		t.Fatal("duplicate manifest was accepted")
	}
	if _, ok := registry.Get(" ", "staging"); ok {
		t.Fatal("manifest lookup accepted blank service")
	}
	if _, ok := (*DeploymentManifestRegistry)(nil).Get("svc", "staging"); ok {
		t.Fatal("nil manifest registry returned manifest")
	}
	if err := registry.Validate("missing", "staging"); err == nil {
		t.Fatal("manifest registry validated missing service")
	}
	if err := (*DeploymentManifestRegistry)(nil).Validate("svc", "staging"); err == nil {
		t.Fatal("nil manifest registry validated service")
	}

	ledger := NewDeploymentLedger()
	if err := (*DeploymentLedger)(nil).Record(manifest); err == nil {
		t.Fatal("nil deployment ledger recorded manifest")
	}
	if err := ledger.Record(nil); err == nil {
		t.Fatal("deployment ledger recorded nil manifest")
	}
	if err := ledger.Record(manifest); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Record(manifest); err == nil {
		t.Fatal("deployment ledger recorded duplicate version")
	}
	if ledger.Snapshot().Current["svc"]["staging"] != manifest {
		t.Fatal("deployment ledger snapshot omitted current manifest")
	}
	if _, ok := (*DeploymentLedger)(nil).Current("svc", "staging"); ok {
		t.Fatal("nil ledger returned active manifest")
	}
	if _, ok := (*DeploymentLedger)(nil).History("svc", "staging"); ok {
		t.Fatal("nil ledger returned deployment history")
	}
	if _, ok := ledger.Current("", "staging"); ok {
		t.Fatal("ledger returned manifest for blank service")
	}
	if _, ok := ledger.History("svc", ""); ok {
		t.Fatal("ledger returned history for blank environment")
	}

	if err := (*DeploymentLedger)(nil).Persist(nil); err == nil {
		t.Fatal("nil ledger persisted state")
	}
	if err := ledger.Persist(nil); err == nil {
		t.Fatal("ledger persisted without a store")
	}
	loadFailure := errors.New("load failed")
	if err := ledger.LoadFrom(&coverageDeploymentStore{loadErr: loadFailure}); !errors.Is(err, loadFailure) {
		t.Fatalf("LoadFrom did not propagate load error: %v", err)
	}
	if err := (*DeploymentLedger)(nil).LoadFrom(&coverageDeploymentStore{}); err == nil {
		t.Fatal("nil ledger loaded state")
	}
	if err := ledger.LoadFrom(nil); err == nil {
		t.Fatal("ledger loaded without store")
	}
	emptyStateStore := &coverageDeploymentStore{state: DeploymentLedgerState{}}
	if err := ledger.LoadFrom(emptyStateStore); err != nil {
		t.Fatalf("LoadFrom empty state failed: %v", err)
	}
	if ledger.current == nil || ledger.history == nil {
		t.Fatal("LoadFrom failed to initialize absent maps")
	}
}

func TestGovernanceCoverageEventsAndWorkflowErrors(t *testing.T) {
	if err := (*DeploymentEventLog)(nil).Record("svc", "prod", "v1", "approve", "operator", ""); err == nil {
		t.Fatal("nil deployment event log recorded event")
	}
	log := NewDeploymentEventLog()
	if err := log.Record(" ", "prod", "v1", "approve", "operator", ""); err == nil {
		t.Fatal("event log accepted missing service")
	}
	if err := log.Record("svc", "prod", "v1", "approve", " ", ""); err == nil {
		t.Fatal("event log accepted missing actor")
	}
	if err := log.Record(" svc ", " prod ", " v1 ", " approve ", " operator ", " reason "); err != nil {
		t.Fatal(err)
	}
	if _, ok := (*DeploymentEventLog)(nil).History("svc", "prod"); ok {
		t.Fatal("nil event log returned history")
	}
	if _, ok := log.History("", "prod"); ok {
		t.Fatal("event log returned history for empty service")
	}
	if _, ok := log.History("svc", "missing"); ok {
		t.Fatal("event log returned history for missing environment")
	}
	if _, ok := log.Latest("", "prod"); ok {
		t.Fatal("event log returned latest for empty service")
	}
	if _, ok := log.Latest("svc", "missing"); ok {
		t.Fatal("event log returned latest event for missing environment")
	}
	log.log["svc"]["empty"] = nil
	if _, ok := log.Latest("svc", "empty"); ok {
		t.Fatal("event log returned latest for empty history")
	}
	if event, ok := log.Latest("svc", "prod"); !ok || event.Actor != "operator" {
		t.Fatalf("Latest = %+v, %v", event, ok)
	}

	if err := (*ReleaseWorkflow)(nil).Approve("operator"); err == nil {
		t.Fatal("nil workflow approved release")
	}
	workflow := NewReleaseWorkflow("", "", "v1")
	if workflow.Service != "unknown-service" || workflow.Environment != "prod" {
		t.Fatalf("workflow defaults: %+v", workflow)
	}
	if err := workflow.Approve("operator"); err == nil {
		t.Fatal("workflow approved without gate")
	}
	workflow.Gate = NewApprovalGate("release", 1)
	if err := workflow.Approve(""); err == nil {
		t.Fatal("workflow accepted empty approver")
	}
	if err := workflow.Approve("operator"); err != nil {
		t.Fatal(err)
	}
	if err := workflow.Approve("operator"); err != nil {
		t.Fatalf("duplicate approval should be idempotent: %v", err)
	}
	workflow.State = "rejected"
	if err := workflow.Approve("other"); err == nil {
		t.Fatal("terminal workflow accepted approval")
	}
	if err := workflow.Rollback("operator", "reason"); err == nil {
		t.Fatal("terminal workflow accepted rollback")
	}
	if err := workflow.Reject("operator", "reason"); err == nil {
		t.Fatal("terminal workflow accepted rejection")
	}

	workflow = NewReleaseWorkflow("svc", "prod", "v2")
	workflow.EventLog = nil
	workflow.Gate = NewApprovalGate("release", 1)
	if err := workflow.Approve("operator"); err != nil {
		t.Fatalf("approval without event log failed: %v", err)
	}
	if err := workflow.Rollback(" ", "reason"); err == nil {
		t.Fatal("workflow accepted empty rollback actor")
	}
	if err := workflow.Rollback("operator", "reason"); err != nil {
		t.Fatalf("rollback without event log failed: %v", err)
	}
	if err := workflow.Reject("operator", "reason"); err == nil {
		t.Fatal("rolled-back workflow accepted rejection")
	}
	if err := (*ReleaseWorkflow)(nil).Rollback("operator", "reason"); err == nil {
		t.Fatal("nil workflow rolled back release")
	}
	if err := (*ReleaseWorkflow)(nil).Reject("operator", "reason"); err == nil {
		t.Fatal("nil workflow rejected release")
	}
	if err := workflow.Reject("", "reason"); err == nil {
		t.Fatal("workflow accepted empty reject actor")
	}
	workflow = NewReleaseWorkflow("svc", "prod", "v3")
	workflow.EventLog = nil
	if err := workflow.Reject("operator", "reason"); err != nil {
		t.Fatalf("rejection without event log failed: %v", err)
	}

	workflow = &ReleaseWorkflow{
		Service: " ", Environment: "prod", Version: "v4",
		Gate: NewApprovalGate("release", 1), EventLog: NewDeploymentEventLog(),
	}
	if err := workflow.Approve(""); err == nil {
		t.Fatal("workflow accepted empty actor through approval gate")
	}
	if err := workflow.Approve("operator"); err == nil {
		t.Fatal("workflow hid event-log failure after approval")
	}

	workflow = NewReleaseWorkflow(" ", " ", "v3")
	if err := workflow.Reject("operator", "reason"); err == nil {
		t.Fatal("workflow accepted rejection with invalid event-log identity")
	}
	if workflow.State != "rejected" {
		t.Fatalf("failed event record should retain current terminal state, got %q", workflow.State)
	}
}

func TestGovernanceCoverageFileStateNormalization(t *testing.T) {
	dir := t.TempDir()
	ledgerPath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(ledgerPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := NewFileDeploymentStore(ledgerPath).Load()
	if err != nil || state.Current == nil || state.History == nil {
		t.Fatalf("deployment state normalization: state=%+v err=%v", state, err)
	}
	policyPath := filepath.Join(dir, "policy-state.json")
	if err := os.WriteFile(policyPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	policyState, err := NewFilePolicyVersionStore(policyPath).Load()
	if err != nil || policyState.Versions == nil {
		t.Fatalf("policy state normalization: state=%+v err=%v", policyState, err)
	}

	data, err := json.Marshal(DeploymentLedgerState{Current: map[string]map[string]*DeploymentManifest{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileDeploymentStore(ledgerPath).Load(); err != nil {
		t.Fatalf("valid deployment JSON rejected: %v", err)
	}
}
