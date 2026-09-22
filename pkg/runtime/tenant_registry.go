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
)

// TenantConfig defines the allowed route and region scope for a tenant.
type TenantConfig struct {
	Name           string
	AllowedRoutes  []string
	AllowedRegions []string
	Enabled        bool
}

// RegisterTenant adds tenant scoping metadata used to enforce multi-tenant
// boundaries at the runtime control-plane layer.
func (r *RouteRegistry) RegisterTenant(cfg TenantConfig) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	if cfg.Name == "" {
		return fmt.Errorf("tenant name is required")
	}
	if !cfg.Enabled {
		return fmt.Errorf("tenant is disabled")
	}
	if r.tenants == nil {
		r.tenants = make(map[string]TenantConfig)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tenants[cfg.Name]; exists {
		return fmt.Errorf("tenant %q already exists", cfg.Name)
	}
	r.tenants[cfg.Name] = cfg
	return nil
}

// GetTenant retrieves a tenant configuration.
func (r *RouteRegistry) GetTenant(name string) (TenantConfig, bool) {
	if r == nil {
		return TenantConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.tenants[strings.TrimSpace(name)]
	return cfg, ok
}

// ListTenants returns the currently configured tenant scopes in the registry.
func (r *RouteRegistry) ListTenants() []TenantConfig {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]TenantConfig, 0, len(r.tenants))
	for _, cfg := range r.tenants {
		out = append(out, cfg)
	}
	return out
}

// CanAccessRoute checks whether a tenant is allowed to use a specific route.
func (r *RouteRegistry) CanAccessRoute(tenantName, routeName string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.tenants[strings.TrimSpace(tenantName)]
	if !ok || !cfg.Enabled {
		return false
	}
	for _, route := range cfg.AllowedRoutes {
		if strings.TrimSpace(route) == strings.TrimSpace(routeName) {
			return true
		}
	}
	return false
}

// CanAccessRegion ensures the tenant is permitted to use the region.
func (r *RouteRegistry) CanAccessRegion(tenantName, regionName string) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.tenants[strings.TrimSpace(tenantName)]
	if !ok || !cfg.Enabled {
		return false
	}
	for _, region := range cfg.AllowedRegions {
		if strings.TrimSpace(region) == strings.TrimSpace(regionName) {
			return true
		}
	}
	return false
}
