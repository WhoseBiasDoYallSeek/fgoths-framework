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
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// PolicyVersionRecord is an immutable snapshot of a route policy at a specific
// version, with the operator context (actor, reason) and a human-readable diff
// against the previous version. Records are append-only: a policy change never
// overwrites history, it appends a new version.
type PolicyVersionRecord struct {
	PolicyName  string      `json:"policy_name"`
	Environment string      `json:"environment"`
	Version     int         `json:"version"`
	Policy      RoutePolicy `json:"policy"`
	Actor       string      `json:"actor"`
	Reason      string      `json:"reason"`
	Diff        string      `json:"diff,omitempty"`
	CreatedAt   time.Time   `json:"created_at"`
}

// PolicyVersionState is the serializable snapshot of the version store.
type PolicyVersionState struct {
	// key is "name/environment"; value is the ordered version list.
	Versions map[string][]PolicyVersionRecord `json:"versions"`
}

// PolicyVersionStore persists and restores policy version history.
type PolicyVersionStore interface {
	Save(state PolicyVersionState) error
	Load() (PolicyVersionState, error)
	// Close releases any resources held by the store. It is safe to call on
	// nil receivers and must be idempotent. Backends without persistent
	// resources should return nil.
	Close() error
}

// FilePolicyVersionStore persists policy versions as a JSON file, keeping the
// control plane dependency-free while surviving process restarts.
type FilePolicyVersionStore struct {
	path string
}

// NewFilePolicyVersionStore creates a file-backed policy version store.
func NewFilePolicyVersionStore(path string) *FilePolicyVersionStore {
	return &FilePolicyVersionStore{path: path}
}

// Save writes the full version state atomically (write + rename).
func (s *FilePolicyVersionStore) Save(state PolicyVersionState) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("policy version store path is required")
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal policy versions: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("failed to write policy versions: %w", err)
	}
	return os.Rename(tmp, s.path)
}

// Load reads the version state; a missing file yields an empty state.
func (s *FilePolicyVersionStore) Load() (PolicyVersionState, error) {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return PolicyVersionState{}, fmt.Errorf("policy version store path is required")
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return PolicyVersionState{Versions: make(map[string][]PolicyVersionRecord)}, nil
		}
		return PolicyVersionState{}, fmt.Errorf("failed to read policy versions: %w", err)
	}
	var state PolicyVersionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return PolicyVersionState{}, fmt.Errorf("failed to unmarshal policy versions: %w", err)
	}
	if state.Versions == nil {
		state.Versions = make(map[string][]PolicyVersionRecord)
	}
	return state, nil
}

// Close is a no-op for file-backed stores; the file handle is released
// automatically when the process exits.
func (s *FilePolicyVersionStore) Close() error {
	return nil
}

// PolicyVersioner layers versioned, audited policy changes on top of a
// RouteRegistry. Create registers a new policy as version 1; Update replaces
// the active policy and appends a new version with a diff; Rollback restores a
// previous version as a new version (history is never rewritten).
type PolicyVersioner struct {
	mu       sync.Mutex
	registry *RouteRegistry
	store    PolicyVersionStore
	versions map[string][]PolicyVersionRecord
}

// NewPolicyVersioner builds a versioner over the given registry and store.
// Existing persisted history is loaded on construction, and the latest version
// of each policy is restored into the registry so the control plane resumes
// with the active policy state after a restart.
func NewPolicyVersioner(registry *RouteRegistry, store PolicyVersionStore) *PolicyVersioner {
	pv := &PolicyVersioner{
		registry: registry,
		store:    store,
		versions: make(map[string][]PolicyVersionRecord),
	}
	if store != nil {
		if state, err := store.Load(); err == nil && state.Versions != nil {
			pv.versions = state.Versions
			// Restore the latest version of each policy into the registry.
			for _, history := range pv.versions {
				if len(history) == 0 {
					continue
				}
				latest := history[len(history)-1]
				if _, exists := registry.GetPolicy(latest.PolicyName); !exists {
					_ = registry.RegisterPolicy(latest.Policy)
				}
			}
		}
	}
	return pv
}

func policyKey(name, environment string) string {
	return strings.TrimSpace(name) + "/" + strings.TrimSpace(environment)
}

// Create registers a new policy as version 1. It fails if the policy already
// exists in the registry.
func (pv *PolicyVersioner) Create(policy RoutePolicy, actor, reason string) error {
	if pv == nil {
		return fmt.Errorf("policy versioner is nil")
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("actor is required")
	}
	if err := pv.registry.RegisterPolicy(policy); err != nil {
		return err
	}

	pv.mu.Lock()
	defer pv.mu.Unlock()
	key := policyKey(policy.Name, policy.Environment)
	record := PolicyVersionRecord{
		PolicyName:  policy.Name,
		Environment: policy.Environment,
		Version:     1,
		Policy:      policy,
		Actor:       strings.TrimSpace(actor),
		Reason:      strings.TrimSpace(reason),
		CreatedAt:   time.Now().UTC(),
	}
	pv.versions[key] = append(pv.versions[key], record)
	return pv.persistLocked()
}

// Update replaces the active policy and records a new version with a diff
// against the current one. Updating an unknown policy fails; use Create first.
func (pv *PolicyVersioner) Update(policy RoutePolicy, actor, reason string) error {
	if pv == nil {
		return fmt.Errorf("policy versioner is nil")
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("actor is required")
	}
	current, ok := pv.registry.GetPolicy(policy.Name)
	if !ok {
		return fmt.Errorf("policy %q not found; use Create to register it first", policy.Name)
	}
	if current.Environment != policy.Environment {
		return fmt.Errorf("policy %q is registered for environment %q; cannot update to environment %q", policy.Name, current.Environment, policy.Environment)
	}
	// Normalize the incoming policy the same way RegisterPolicy does, so the
	// recorded snapshot matches what is actually active (e.g. default SLO).
	policy.Name = strings.TrimSpace(policy.Name)
	policy.Environment = strings.TrimSpace(policy.Environment)
	policy.Service = strings.TrimSpace(policy.Service)
	if policy.Environment == "" {
		policy.Environment = "prod"
	}
	if policy.SLO == nil {
		policy.SLO = NewSLOPolicy(0.05, 250, 0.02)
	}
	if err := pv.replacePolicy(policy); err != nil {
		return err
	}

	pv.mu.Lock()
	defer pv.mu.Unlock()
	key := policyKey(policy.Name, policy.Environment)
	nextVersion := len(pv.versions[key]) + 1
	record := PolicyVersionRecord{
		PolicyName:  policy.Name,
		Environment: policy.Environment,
		Version:     nextVersion,
		Policy:      policy,
		Actor:       strings.TrimSpace(actor),
		Reason:      strings.TrimSpace(reason),
		Diff:        diffPolicies(current, policy),
		CreatedAt:   time.Now().UTC(),
	}
	pv.versions[key] = append(pv.versions[key], record)
	return pv.persistLocked()
}

// Rollback restores a previous version as a new version. History is append-only:
// the rollback itself becomes the latest version with a rollback reason.
func (pv *PolicyVersioner) Rollback(name, environment string, toVersion int, actor, reason string) error {
	if pv == nil {
		return fmt.Errorf("policy versioner is nil")
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("actor is required")
	}
	pv.mu.Lock()
	defer pv.mu.Unlock()
	key := policyKey(name, environment)
	history := pv.versions[key]
	if len(history) == 0 {
		return fmt.Errorf("policy %q has no version history", name)
	}
	if toVersion < 1 || toVersion > len(history) {
		return fmt.Errorf("version %d is out of range (1..%d)", toVersion, len(history))
	}
	target := history[toVersion-1]
	current, _ := pv.registry.GetPolicy(name)

	if err := pv.replacePolicy(target.Policy); err != nil {
		return err
	}
	record := PolicyVersionRecord{
		PolicyName:  name,
		Environment: environment,
		Version:     len(history) + 1,
		Policy:      target.Policy,
		Actor:       strings.TrimSpace(actor),
		Reason:      fmt.Sprintf("rollback to v%d: %s", toVersion, strings.TrimSpace(reason)),
		Diff:        diffPolicies(current, target.Policy),
		CreatedAt:   time.Now().UTC(),
	}
	pv.versions[key] = append(pv.versions[key], record)
	return pv.persistLocked()
}

// History returns the ordered version history for a policy.
func (pv *PolicyVersioner) History(name, environment string) ([]PolicyVersionRecord, error) {
	if pv == nil {
		return nil, fmt.Errorf("policy versioner is nil")
	}
	pv.mu.Lock()
	defer pv.mu.Unlock()
	history := pv.versions[policyKey(name, environment)]
	if len(history) == 0 {
		return nil, fmt.Errorf("policy %q has no version history", name)
	}
	out := make([]PolicyVersionRecord, len(history))
	copy(out, history)
	return out, nil
}

// replacePolicy overwrites an existing policy in the registry (update path).
func (pv *PolicyVersioner) replacePolicy(policy RoutePolicy) error {
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
	pv.registry.mu.Lock()
	defer pv.registry.mu.Unlock()
	if pv.registry.policies == nil {
		pv.registry.policies = make(map[string]RoutePolicy)
	}
	if _, exists := pv.registry.policies[policy.Name]; !exists {
		return fmt.Errorf("policy %q not found", policy.Name)
	}
	pv.registry.policies[policy.Name] = policy
	return nil
}

func (pv *PolicyVersioner) persistLocked() error {
	if pv.store == nil {
		return nil
	}
	return pv.store.Save(PolicyVersionState{Versions: pv.versions})
}

// Close releases any resources held by the PolicyVersioner by closing the
// underlying store. It is safe to call on a nil receiver and is idempotent.
func (pv *PolicyVersioner) Close() error {
	if pv == nil || pv.store == nil {
		return nil
	}
	return pv.store.Close()
}

// diffPolicies produces a compact, human-readable summary of the fields that
// changed between two policy versions.
func diffPolicies(before, after RoutePolicy) string {
	var changes []string
	appendChange := func(field string, changed bool, detail string) {
		if changed {
			changes = append(changes, field+": "+detail)
		}
	}

	appendChange("service", before.Service != after.Service,
		fmt.Sprintf("%q -> %q", before.Service, after.Service))
	appendChange("allowed_tenants", !equalStringSlices(before.AllowedTenants, after.AllowedTenants),
		fmt.Sprintf("%v -> %v", before.AllowedTenants, after.AllowedTenants))
	appendChange("allowed_regions", !equalStringSlices(before.AllowedRegions, after.AllowedRegions),
		fmt.Sprintf("%v -> %v", before.AllowedRegions, after.AllowedRegions))
	appendChange("require_signed_artifacts", before.RequireSignedArtifacts != after.RequireSignedArtifacts,
		fmt.Sprintf("%v -> %v", before.RequireSignedArtifacts, after.RequireSignedArtifacts))
	appendChange("require_audit", before.RequireAudit != after.RequireAudit,
		fmt.Sprintf("%v -> %v", before.RequireAudit, after.RequireAudit))

	switch {
	case before.SLO == nil && after.SLO != nil:
		changes = append(changes, "slo: added")
	case before.SLO != nil && after.SLO == nil:
		changes = append(changes, "slo: removed")
	case before.SLO != nil && after.SLO != nil:
		appendChange("slo", *before.SLO != *after.SLO,
			fmt.Sprintf("{error_rate:%g, latency_ms:%d, burn_rate:%g} -> {error_rate:%g, latency_ms:%d, burn_rate:%g}",
				before.SLO.ErrorRateThreshold, before.SLO.LatencyThresholdMS, before.SLO.BurnRateLimit,
				after.SLO.ErrorRateThreshold, after.SLO.LatencyThresholdMS, after.SLO.BurnRateLimit))
	}

	if len(changes) == 0 {
		return "no changes"
	}
	sort.Strings(changes)
	return strings.Join(changes, "; ")
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := make([]string, len(a))
	sb := make([]string, len(b))
	copy(sa, a)
	copy(sb, b)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}
