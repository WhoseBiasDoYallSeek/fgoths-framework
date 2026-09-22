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
	"math/rand"
	"net/url"
	"strings"
)

// UpstreamConfig is the declarative configuration of a backend upstream in the
// runtime control plane. Routes reference upstreams by name instead of holding
// raw target URLs, so backend topology becomes governed configuration.
type UpstreamConfig struct {
	Name     string   `json:"name"`
	Target   string   `json:"target"`
	Fallback string   `json:"fallback,omitempty"` // optional name of another upstream used when this one is unhealthy
	Services []string `json:"services,omitempty"` // optional logical service names this upstream serves
	Weight   int      `json:"weight"`
	Enabled  bool     `json:"enabled"`
	Healthy  bool     `json:"healthy"`
}

// RegisterUpstream adds or replaces an upstream definition in the registry.
// A disabled upstream is allowed: it is stored but never selected — this is
// how operators drain a backend (declare it disabled, later re-enable it).
func (r *RouteRegistry) RegisterUpstream(cfg UpstreamConfig) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Target = strings.TrimSpace(cfg.Target)
	cfg.Fallback = strings.TrimSpace(cfg.Fallback)
	if cfg.Name == "" {
		return fmt.Errorf("upstream name is required")
	}
	if cfg.Target == "" {
		return fmt.Errorf("upstream target is required")
	}
	if _, err := url.Parse(cfg.Target); err != nil || cfg.Target == "" {
		return fmt.Errorf("upstream target %q is not a valid URL", cfg.Target)
	}
	if cfg.Weight < 0 {
		cfg.Weight = 0
	}
	if cfg.Weight == 0 {
		cfg.Weight = 1
	}
	if cfg.Fallback == cfg.Name {
		return fmt.Errorf("upstream %q cannot be its own fallback", cfg.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.upstreams == nil {
		r.upstreams = make(map[string]UpstreamConfig)
	}
	if _, exists := r.upstreams[cfg.Name]; exists {
		return fmt.Errorf("upstream %q already exists", cfg.Name)
	}
	r.upstreams[cfg.Name] = cfg
	return nil
}

// GetUpstream returns an upstream by name.
func (r *RouteRegistry) GetUpstream(name string) (UpstreamConfig, bool) {
	if r == nil {
		return UpstreamConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	up, ok := r.upstreams[strings.TrimSpace(name)]
	return up, ok
}

// ListUpstreams returns all registered upstreams.
func (r *RouteRegistry) ListUpstreams() []UpstreamConfig {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]UpstreamConfig, 0, len(r.upstreams))
	for _, up := range r.upstreams {
		out = append(out, up)
	}
	return out
}

// SelectUpstream resolves an upstream honoring health and failover. If the
// requested upstream is unhealthy and declares a fallback, the fallback is
// returned when it is itself healthy and enabled.
func (r *RouteRegistry) SelectUpstream(name string) (UpstreamConfig, bool) {
	if r == nil {
		return UpstreamConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	up, ok := r.upstreams[strings.TrimSpace(name)]
	if !ok || !up.Enabled {
		return UpstreamConfig{}, false
	}
	if up.Healthy {
		return up, true
	}
	if up.Fallback != "" {
		if fb, ok := r.upstreams[up.Fallback]; ok && fb.Enabled && fb.Healthy {
			return fb, true
		}
	}
	return UpstreamConfig{}, false
}

// PickUpstream performs weighted random selection among healthy upstreams
// that serve the given logical service name. An empty service name selects
// among all healthy upstreams.
func (r *RouteRegistry) PickUpstream(service string) (UpstreamConfig, bool) {
	if r == nil {
		return UpstreamConfig{}, false
	}
	service = strings.TrimSpace(service)

	r.mu.RLock()
	pool := make([]UpstreamConfig, 0, len(r.upstreams))
	total := 0
	for _, up := range r.upstreams {
		if !up.Enabled || !up.Healthy {
			continue
		}
		if service != "" && !servesService(up, service) {
			continue
		}
		pool = append(pool, up)
		total += up.Weight
	}
	r.mu.RUnlock()

	if len(pool) == 0 {
		return UpstreamConfig{}, false
	}
	pick := rand.Intn(total)
	for _, up := range pool {
		pick -= up.Weight
		if pick < 0 {
			return up, true
		}
	}
	return pool[len(pool)-1], true
}

// ResolveRouteTarget returns the concrete target URL for a route. Routes that
// reference an upstream resolve through the upstream registry (with failover);
// routes with a legacy inline Target keep working unchanged.
func (r *RouteRegistry) ResolveRouteTarget(routeName string) (string, bool) {
	if r == nil {
		return "", false
	}
	r.mu.RLock()
	route, ok := r.routes[strings.TrimSpace(routeName)]
	r.mu.RUnlock()
	if !ok {
		return "", false
	}
	if route.Upstream != "" {
		// Weighted pool: when several enabled+healthy upstreams serve the
		// route's upstream's logical service, pick by Weight. A lone (or
		// unhealthy-with-fallback) upstream resolves 1:1 as before.
		if up, ok := r.pickForRoute(route.Upstream); ok {
			return up.Target, true
		}
		return "", false
	}
	if route.Target != "" {
		return route.Target, true
	}
	return "", false
}

// RouteHeaders returns the declarative headers configured for a route.
// Callers that proxy to the resolved target (gateway, control-plane-driven
// proxy) must apply them to the upstream request; the registry only stores
// and validates them.
func (r *RouteRegistry) RouteHeaders(routeName string) map[string]string {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	route, ok := r.routes[strings.TrimSpace(routeName)]
	if !ok || len(route.Header) == 0 {
		return nil
	}
	out := make(map[string]string, len(route.Header))
	for k, v := range route.Header {
		out[k] = v
	}
	return out
}

// NewProxy resolves a route target and builds a proxy that applies the route's
// declarative headers to every upstream request.
func (r *RouteRegistry) NewProxy(routeName string) (*Proxy, error) {
	target, ok := r.ResolveRouteTarget(routeName)
	if !ok {
		return nil, fmt.Errorf("route %q not found or has no healthy upstream", strings.TrimSpace(routeName))
	}
	proxy, err := NewProxy(target)
	if err != nil {
		return nil, err
	}
	for key, value := range r.RouteHeaders(routeName) {
		proxy.WithHeader(key, value)
	}
	return proxy, nil
}

// pickForRoute resolves a route's upstream honoring weight pools: when the
// named upstream belongs to a service with multiple healthy members, the
// target is chosen weighted-randomly among them; otherwise it falls back to
// the health/failover 1:1 selection.
func (r *RouteRegistry) pickForRoute(upstreamName string) (UpstreamConfig, bool) {
	r.mu.RLock()
	named, ok := r.upstreams[strings.TrimSpace(upstreamName)]
	r.mu.RUnlock()
	if !ok {
		return UpstreamConfig{}, false
	}
	// Only pool when the upstream declares a logical service — otherwise
	// the name IS the identity and 1:1 selection (with failover) applies.
	if len(named.Services) == 0 {
		up, ok := r.SelectUpstream(upstreamName)
		return up, ok
	}
	if up, ok := r.PickUpstream(named.Services[0]); ok {
		return up, true
	}
	// Pool empty (all members unhealthy/drained): fall back to the named
	// upstream's own failover chain.
	return r.SelectUpstream(upstreamName)
}

func servesService(up UpstreamConfig, service string) bool {
	if len(up.Services) == 0 {
		return true
	}
	for _, s := range up.Services {
		if strings.TrimSpace(s) == service {
			return true
		}
	}
	return false
}
