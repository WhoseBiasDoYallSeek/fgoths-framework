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

// RolloutStrategy is the deployment strategy used for progressive release
// management in the runtime control plane.
type RolloutStrategy string

const (
	RolloutStrategyCanary      RolloutStrategy = "canary"
	RolloutStrategyBlueGreen   RolloutStrategy = "blue-green"
	RolloutStrategyProgressive RolloutStrategy = "progressive"
	RolloutStrategyRollback    RolloutStrategy = "rollback"
)

// RolloutConfig models a release decision for a route in a runtime control plane.
type RolloutConfig struct {
	Name           string
	Route          string
	Strategy       RolloutStrategy
	Target         string
	CanaryWeight   int
	MaxWeight      int
	Step           int
	Enabled        bool
	Progressive    bool
	Rollback       bool
	ErrorThreshold float64
}

// RolloutMetrics captures observed behavior for a canary step during a rollout.
type RolloutMetrics struct {
	Requests  int
	ErrorRate float64
	Failed    int
	Succeeded int
}

// RolloutDecision is the runtime verdict for a rollout evaluation.
type RolloutDecision struct {
	Allow  bool
	Weight int
	Target string
	Reason string
}

// Validate ensures the rollout configuration is coherent and safe. It uses a
// pointer receiver so applied defaults (Strategy, MaxWeight, Step,
// ErrorThreshold) persist in the caller's config — registry methods rely on
// that to store the normalized form.
func (c *RolloutConfig) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("rollout name is required")
	}
	if strings.TrimSpace(c.Route) == "" {
		return fmt.Errorf("rollout route is required")
	}
	if strings.TrimSpace(c.Target) == "" {
		return fmt.Errorf("rollout target is required")
	}
	if c.Rollback {
		return fmt.Errorf("rollback rollout is explicitly rejected for progressive delivery")
	}
	if !c.Enabled {
		return fmt.Errorf("rollout is disabled")
	}
	if c.Strategy == "" {
		c.Strategy = RolloutStrategyCanary
	}
	if c.CanaryWeight < 0 {
		return fmt.Errorf("canary weight cannot be negative")
	}
	if c.MaxWeight <= 0 {
		c.MaxWeight = 100
	}
	if c.Step <= 0 {
		c.Step = 10
	}
	if c.CanaryWeight > c.MaxWeight {
		return fmt.Errorf("canary weight cannot exceed max weight")
	}
	if c.Progressive && c.CanaryWeight == 0 {
		return fmt.Errorf("progressive rollout requires a non-zero canary weight")
	}
	if c.ErrorThreshold < 0 {
		return fmt.Errorf("error threshold cannot be negative")
	}
	if c.ErrorThreshold > 1 {
		c.ErrorThreshold = c.ErrorThreshold / 100
	}
	return nil
}

// Decide determines whether a rollout should be allowed for a given traffic share.
func (c RolloutConfig) Decide(current int) (RolloutDecision, error) {
	if err := c.Validate(); err != nil {
		return RolloutDecision{}, err
	}
	if c.Strategy == RolloutStrategyRollback {
		return RolloutDecision{Allow: false, Weight: 0, Target: c.Target, Reason: "rollback is not allowed in progressive delivery"}, nil
	}
	if current < 0 {
		current = 0
	}
	if c.CanaryWeight <= 0 {
		return RolloutDecision{Allow: false, Weight: 0, Target: c.Target, Reason: "rollout is inactive"}, nil
	}
	weight := c.CanaryWeight
	if c.Progressive && current < weight {
		weight = current
	}
	return RolloutDecision{Allow: true, Weight: weight, Target: c.Target, Reason: "canary step approved"}, nil
}

// Evaluate determines whether the current canary step remains safe given observed
// traffic and failure rate. If the failure rate exceeds the configured threshold,
// the rollout is paused automatically to protect the route.
func (c RolloutConfig) Evaluate(metrics RolloutMetrics) (RolloutDecision, error) {
	if err := c.Validate(); err != nil {
		return RolloutDecision{}, err
	}
	if c.Strategy == RolloutStrategyRollback {
		return RolloutDecision{Allow: false, Weight: 0, Target: c.Target, Reason: "rollback is not allowed in progressive delivery"}, nil
	}
	if c.ErrorThreshold > 0 && metrics.ErrorRate > c.ErrorThreshold {
		return RolloutDecision{Allow: false, Weight: 0, Target: c.Target, Reason: fmt.Sprintf("canary blocked: error rate %.2f exceeds threshold %.2f", metrics.ErrorRate, c.ErrorThreshold)}, nil
	}
	return c.Decide(metrics.Requests)
}

// RolloutRegistry stores deployment decisions for declared routes.
type RolloutRegistry struct {
	mu       sync.RWMutex
	rollouts map[string]RolloutConfig
}

// NewRolloutRegistry creates an empty rollout registry.
func NewRolloutRegistry() *RolloutRegistry {
	return &RolloutRegistry{rollouts: make(map[string]RolloutConfig)}
}

// RegisterRollout adds a rollout config to the registry.
func (r *RolloutRegistry) RegisterRollout(cfg RolloutConfig) error {
	if r == nil {
		return fmt.Errorf("rollout registry is nil")
	}
	cfg.Name = strings.TrimSpace(cfg.Name)
	cfg.Route = strings.TrimSpace(cfg.Route)
	cfg.Target = strings.TrimSpace(cfg.Target)
	if err := cfg.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rollouts[cfg.Name]; exists {
		return fmt.Errorf("rollout %q already exists", cfg.Name)
	}
	r.rollouts[cfg.Name] = cfg
	return nil
}

// GetRollout retrieves a rollout by name.
func (r *RolloutRegistry) GetRollout(name string) (RolloutConfig, bool) {
	if r == nil {
		return RolloutConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.rollouts[strings.TrimSpace(name)]
	return cfg, ok
}

// Register adds a rollout to the registry via the route registry helper.
func (r *RouteRegistry) RegisterRollout(cfg RolloutConfig) error {
	if r == nil {
		return fmt.Errorf("route registry is nil")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.rollouts[cfg.Name]; exists {
		return fmt.Errorf("rollout %q already exists", cfg.Name)
	}
	r.rollouts[cfg.Name] = cfg
	return nil
}

// GetRollout retrieves a rollout by name from the route registry.
func (r *RouteRegistry) GetRollout(name string) (RolloutConfig, bool) {
	if r == nil {
		return RolloutConfig{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cfg, ok := r.rollouts[strings.TrimSpace(name)]
	return cfg, ok
}

func (r *RouteRegistry) ListRollouts() []RolloutConfig {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]RolloutConfig, 0, len(r.rollouts))
	for _, cfg := range r.rollouts {
		out = append(out, cfg)
	}
	return out
}
