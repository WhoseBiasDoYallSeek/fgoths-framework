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
	"fmt"
	"strings"
	"sync"
)

// RouteConfig is the declarative configuration for a route registered in a
// runtime control plane. It is intentionally small and explicit: enough for
// routing and progressive rollouts without requiring an external control plane.
type RouteConfig struct {
	Name   string `json:"name"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Target string `json:"target,omitempty"` // legacy inline target; preferred is Upstream reference
	// Upstream is the name of an UpstreamConfig registered via RegisterUpstream.
	// When set, the concrete target is resolved through the upstream registry.
	Upstream string            `json:"upstream,omitempty"`
	Weight   int               `json:"weight"`
	Tenant   string            `json:"tenant,omitempty"`
	Enabled  bool              `json:"enabled"`
	Header   map[string]string `json:"headers,omitempty"`
}

// RouteRegistry stores named route configuration for runtime governance.
type RouteRegistry struct {
	mu        sync.RWMutex
	routes    map[string]RouteConfig
	rollouts  map[string]RolloutConfig
	regions   map[string]RegionConfig
	tenants   map[string]TenantConfig
	policies  map[string]RoutePolicy
	upstreams map[string]UpstreamConfig
}

// RoutePolicy declares the environment-specific guarantees required before a
// route or rollout may be consumed by a tenant in a given region.
type RoutePolicy struct {
	Name                   string     `json:"name"`
	Environment            string     `json:"environment"`
	Service                string     `json:"service"`
	AllowedTenants         []string   `json:"allowed_tenants,omitempty"`
	AllowedRegions         []string   `json:"allowed_regions,omitempty"`
	RequireSignedArtifacts bool       `json:"require_signed_artifacts"`
	RequireAudit           bool       `json:"require_audit"`
	SLO                    *SLOPolicy `json:"slo,omitempty"`
}

// NewRouteRegistry creates an empty route registry.
func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{
		routes:    make(map[string]RouteConfig),
		rollouts:  make(map[string]RolloutConfig),
		regions:   make(map[string]RegionConfig),
		tenants:   make(map[string]TenantConfig),
		policies:  make(map[string]RoutePolicy),
		upstreams: make(map[string]UpstreamConfig),
	}
}

// Register adds or replaces a route config.
func (r *RouteRegistry) Register(cfg RouteConfig) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Method = strings.ToUpper(strings.TrimSpace(cfg.Method))
	cfg.Path = normalizePath(cfg.Path)
	cfg.Target = strings.TrimSpace(cfg.Target)
	if cfg.Name == "" {
		return fmt.Errorf("route name is required")
	}
	if cfg.Path == "" || cfg.Path == "/" && cfg.Method == "" {
		return fmt.Errorf("route path is required")
	}
	if cfg.Target == "" && cfg.Upstream == "" {
		return fmt.Errorf("route target is required (inline Target or Upstream reference)")
	}
	if cfg.Upstream != "" {
		cfg.Upstream = strings.TrimSpace(cfg.Upstream)
		r.mu.RLock()
		_, upstreamExists := r.upstreams[cfg.Upstream]
		r.mu.RUnlock()
		if !upstreamExists {
			return fmt.Errorf("route references unknown upstream %q", cfg.Upstream)
		}
	}
	if cfg.Method == "" {
		cfg.Method = httpMethodAny
	}
	if cfg.Weight < 0 {
		cfg.Weight = 0
	}
	if !cfg.Enabled {
		if cfg.Weight == 0 {
			return fmt.Errorf("disabled route must define a non-zero traffic weight")
		}
		return fmt.Errorf("disabled route is not allowed in the active registry")
	}
	if cfg.Enabled && cfg.Weight == 0 {
		cfg.Weight = 100
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.routes[cfg.Name]; exists {
		return fmt.Errorf("route %q already exists", cfg.Name)
	}
	r.routes[cfg.Name] = cfg
	return nil
}

// Get returns the route config by name.
func (r *RouteRegistry) Get(name string) (RouteConfig, bool) {
	if r == nil {
		return RouteConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.routes[strings.TrimSpace(name)]
	return cfg, ok
}

// List returns all routes in the registry.
func (r *RouteRegistry) List() []RouteConfig {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RouteConfig, 0, len(r.routes))
	for _, cfg := range r.routes {
		out = append(out, cfg)
	}
	return out
}

// RegisterPolicy stores a declarative route policy for an environment.
func (r *RouteRegistry) RegisterPolicy(policy RoutePolicy) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	policy.Name = strings.TrimSpace(policy.Name)
	policy.Environment = strings.TrimSpace(policy.Environment)
	policy.Service = strings.TrimSpace(policy.Service)
	if policy.Name == "" {
		return fmt.Errorf("policy name is required")
	}
	if policy.Environment == "" {
		policy.Environment = "prod"
	}
	if policy.Service == "" {
		return fmt.Errorf("policy service is required")
	}
	if policy.SLO == nil {
		policy.SLO = NewSLOPolicy(0.05, 250, 0.02)
	}
	if r.policies == nil {
		r.policies = make(map[string]RoutePolicy)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.policies[policy.Name]; exists {
		return fmt.Errorf("policy %q already exists", policy.Name)
	}
	r.policies[policy.Name] = policy
	return nil
}

// GetPolicy returns a registered policy by name.
func (r *RouteRegistry) GetPolicy(name string) (RoutePolicy, bool) {
	if r == nil {
		return RoutePolicy{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.policies[strings.TrimSpace(name)]
	return cfg, ok
}

// ListPolicies returns all registered route policies.
func (r *RouteRegistry) ListPolicies() []RoutePolicy {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RoutePolicy, 0, len(r.policies))
	for _, cfg := range r.policies {
		out = append(out, cfg)
	}
	return out
}

// ValidatePolicy ensures the tenant and region are allowed and the policy guardrails are satisfied.
func (r *RouteRegistry) ValidatePolicy(name, tenantName, regionName string) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	policy, ok := r.GetPolicy(name)
	if !ok {
		return fmt.Errorf("policy %q not found", name)
	}
	if strings.TrimSpace(tenantName) == "" {
		return fmt.Errorf("tenant name is required")
	}
	if strings.TrimSpace(regionName) == "" {
		return fmt.Errorf("region name is required")
	}
	if len(policy.AllowedTenants) > 0 {
		allowed := false
		for _, tenant := range policy.AllowedTenants {
			if strings.TrimSpace(tenant) == strings.TrimSpace(tenantName) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("tenant %q is not allowed under policy %q", tenantName, name)
		}
	}
	if len(policy.AllowedRegions) > 0 {
		allowed := false
		for _, region := range policy.AllowedRegions {
			if strings.TrimSpace(region) == strings.TrimSpace(regionName) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("region %q is not allowed under policy %q", regionName, name)
		}
	}
	if policy.RequireSignedArtifacts && policy.SLO == nil {
		return fmt.Errorf("policy %q requires signed artifacts and an SLO policy", name)
	}
	if policy.RequireAudit && policy.SLO == nil {
		return fmt.Errorf("policy %q requires audit logging and an SLO policy", name)
	}
	return nil
}

const httpMethodAny = "ANY"
