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

// RegionConfig defines a deployment region and its health/failover state.
type RegionConfig struct {
	Name    string
	Region  string
	Target  string
	Weight  int
	Enabled bool
	Primary bool
	Healthy bool
}

// RegionMetrics captures observed region health for failover decisions.
type RegionMetrics struct {
	LatencyMS int
	ErrorRate float64
}

// RegionDecision is the runtime verdict for region selection and failover.
type RegionDecision struct {
	Allow  bool
	Name   string
	Target string
	Reason string
}

// SelectRegion picks the healthiest available region for a given primary region or
// returns the primary if it remains healthy.
func (r *RouteRegistry) SelectRegion(primaryName string) (RegionConfig, bool) {
	if r == nil {
		return RegionConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	primary, ok := r.regions[strings.TrimSpace(primaryName)]
	if ok && primary.Enabled && primary.Healthy {
		return primary, true
	}
	for _, region := range r.regions {
		if region.Enabled && region.Healthy && region.Name != primaryName {
			return region, true
		}
	}
	if ok {
		return primary, true
	}
	return RegionConfig{}, false
}

// EvaluateRegion decides whether to keep traffic on the current region or shift to
// a healthier fallback when the current region is degraded by latency or error rate.
func (r *RouteRegistry) EvaluateRegion(primaryName string, metrics RegionMetrics) (RegionDecision, bool) {
	if r == nil {
		return RegionDecision{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	primary, ok := r.regions[strings.TrimSpace(primaryName)]
	if !ok || !primary.Enabled {
		return RegionDecision{}, false
	}
	if primary.Healthy && metrics.LatencyMS < 250 && metrics.ErrorRate <= 0.05 {
		return RegionDecision{Allow: true, Name: primary.Name, Target: primary.Target, Reason: "primary region is healthy"}, true
	}
	for _, region := range r.regions {
		if region.Enabled && region.Healthy && region.Name != primaryName {
			if metrics.LatencyMS >= 250 || metrics.ErrorRate > 0.05 {
				return RegionDecision{Allow: true, Name: region.Name, Target: region.Target, Reason: "failover to healthy fallback region"}, true
			}
		}
	}
	if primary.Healthy {
		return RegionDecision{Allow: true, Name: primary.Name, Target: primary.Target, Reason: "primary region remains acceptable"}, true
	}
	return RegionDecision{Allow: false, Name: primary.Name, Target: primary.Target, Reason: "primary region is degraded and no healthy fallback exists"}, true
}

// RegisterRegion adds a deployment region to the registry.
func (r *RouteRegistry) RegisterRegion(cfg RegionConfig) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Region = strings.TrimSpace(cfg.Region)
	cfg.Target = strings.TrimSpace(cfg.Target)
	if cfg.Name == "" {
		return fmt.Errorf("region name is required")
	}
	if cfg.Region == "" {
		return fmt.Errorf("region identifier is required")
	}
	if cfg.Target == "" {
		return fmt.Errorf("region target is required")
	}
	if cfg.Weight < 0 {
		cfg.Weight = 0
	}
	if cfg.Weight == 0 && cfg.Enabled {
		cfg.Weight = 100
	}
	if !cfg.Enabled {
		return fmt.Errorf("region is disabled")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.regions == nil {
		r.regions = make(map[string]RegionConfig)
	}
	if _, exists := r.regions[cfg.Name]; exists {
		return fmt.Errorf("region %q already exists", cfg.Name)
	}
	r.regions[cfg.Name] = cfg
	return nil
}

// GetRegion retrieves a region by name.
func (r *RouteRegistry) GetRegion(name string) (RegionConfig, bool) {
	if r == nil {
		return RegionConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.regions[strings.TrimSpace(name)]
	return cfg, ok
}

// ListRegions returns all regions in the registry.
func (r *RouteRegistry) ListRegions() []RegionConfig {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RegionConfig, 0, len(r.regions))
	for _, cfg := range r.regions {
		out = append(out, cfg)
	}
	return out
}
