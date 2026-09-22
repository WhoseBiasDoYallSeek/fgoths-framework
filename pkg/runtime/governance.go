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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// RouteRegistrySnapshot captures the desired state of a declarative config set.
type RouteRegistrySnapshot struct {
	Routes  []RouteConfig
	Rolls   []RolloutConfig
	Regions []RegionConfig
	Tenants []TenantConfig
}

// ConfigDiff describes the changes between two snapshots.
type ConfigDiff struct {
	HasChanges bool
	Routes     []RouteChange
	UpdatedAt  time.Time
}

// RouteChange records old/new values for a route that changed between snapshots.
type RouteChange struct {
	Name   string
	Before string
	After  string
}

// BuildConfigDiff produces a minimal diff between snapshots used for approval
// and release gates.
func BuildConfigDiff(before, after RouteRegistrySnapshot) ConfigDiff {
	diff := ConfigDiff{UpdatedAt: time.Now()}
	if len(before.Routes) == 0 && len(after.Routes) == 0 {
		return diff
	}
	index := make(map[string]RouteConfig, len(before.Routes))
	for _, route := range before.Routes {
		index[route.Name] = route
	}
	for _, route := range after.Routes {
		prev, ok := index[route.Name]
		if !ok || prev.Target != route.Target || prev.Weight != route.Weight || prev.Enabled != route.Enabled {
			diff.HasChanges = true
			diff.Routes = append(diff.Routes, RouteChange{
				Name:   route.Name,
				Before: prev.Target,
				After:  route.Target,
			})
		}
	}
	return diff
}

// ApprovalGate represents a manual approval threshold for a release.
type ApprovalGate struct {
	mu        sync.Mutex
	Name      string
	Required  int
	Approvals []string
}

// NewApprovalGate creates a pending approval gate.
func NewApprovalGate(name string, required int) *ApprovalGate {
	if required <= 0 {
		required = 1
	}
	return &ApprovalGate{Name: name, Required: required}
}

// Approve records one approval for the gate; the gate becomes approved once the
// required threshold is reached.
func (g *ApprovalGate) Approve(actor string) error {
	if g == nil {
		return fmt.Errorf("approval gate is nil")
	}
	if actor == "" {
		return fmt.Errorf("approver identity is required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, existing := range g.Approvals {
		if existing == actor {
			return nil
		}
	}
	g.Approvals = append(g.Approvals, actor)
	return nil
}

// IsApproved reports whether the gate has reached the threshold.
func (g *ApprovalGate) IsApproved() bool {
	if g == nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.Approvals) >= g.Required
}

// SLOPolicy defines the error-rate, latency and burn-rate guardrails that must be
// observed before a production rollout is considered safe.
type SLOPolicy struct {
	ErrorRateThreshold float64
	LatencyThresholdMS int
	BurnRateLimit      float64
}

// NewSLOPolicy creates a minimal SLO guardrail set for production operations.
func NewSLOPolicy(errorRateThreshold float64, latencyThresholdMS int, burnRateLimit float64) *SLOPolicy {
	if errorRateThreshold <= 0 {
		errorRateThreshold = 0.05
	}
	if latencyThresholdMS <= 0 {
		latencyThresholdMS = 250
	}
	if burnRateLimit <= 0 {
		burnRateLimit = 0.02
	}
	return &SLOPolicy{
		ErrorRateThreshold: errorRateThreshold,
		LatencyThresholdMS: latencyThresholdMS,
		BurnRateLimit:      burnRateLimit,
	}
}

// RollbackPlan defines the operational steps required to restore service during a failed rollout.
type RollbackPlan struct {
	Reason    string
	Previous  string
	Steps     []string
	Triggered bool
}

// NewRollbackPlan creates an ordered rollback procedure for production remediation.
func NewRollbackPlan(reason, previous string, steps []string) *RollbackPlan {
	if reason == "" {
		reason = "rollback requested"
	}
	if previous == "" {
		previous = "previous stable version"
	}
	return &RollbackPlan{Reason: reason, Previous: previous, Steps: append([]string(nil), steps...)}
}

// ReleasePlan wraps the approval, provenance, SLO and rollback requirements for a
// production deployment. It enforces a minimal release-readiness contract used by
// runtime governance and progressive delivery pipelines.
type ReleasePlan struct {
	Environment string
	Service     string
	Version     string
	Gate        *ApprovalGate
	Artifact    *ArtifactProvenance
	Runbook     string
	SLO         *SLOPolicy
	Rollback    *RollbackPlan
	ApprovedAt  time.Time
}

// NewReleasePlan creates a pending production runbook ready for validation.
func NewReleasePlan(environment, service, version string) *ReleasePlan {
	if environment == "" {
		environment = "prod"
	}
	return &ReleasePlan{
		Environment: environment,
		Service:     service,
		Version:     version,
	}
}

// Validate ensures the release has a signed artifact, required approvals, an SLO
// policy, a rollback plan and a runbook before it can be marked as ready.
func (p *ReleasePlan) Validate(secret string) error {
	if p == nil {
		return fmt.Errorf("release plan is nil")
	}
	if strings.TrimSpace(p.Service) == "" {
		return fmt.Errorf("service name is required")
	}
	if p.Gate == nil {
		return fmt.Errorf("approval gate is required")
	}
	if !p.Gate.IsApproved() {
		return fmt.Errorf("approval gate is not approved")
	}
	if p.Artifact == nil {
		return fmt.Errorf("artifact provenance is required")
	}
	if err := p.Artifact.Verify(secret); err != nil {
		return fmt.Errorf("artifact signature verification failed: %w", err)
	}
	if p.SLO == nil {
		return fmt.Errorf("SLO policy is required for production rollout")
	}
	if p.Rollback == nil {
		return fmt.Errorf("rollback plan is required for production rollout")
	}
	if strings.TrimSpace(p.Runbook) == "" {
		return fmt.Errorf("deployment runbook is required")
	}
	p.ApprovedAt = time.Now().UTC()
	return nil
}

// IsReady reports whether a release plan is valid for production rollout.
func (p *ReleasePlan) IsReady(secret string) bool {
	return p != nil && p.Validate(secret) == nil
}

// ComplianceReport captures the release evidence required before an enterprise
// deployment is considered compliant and ready for production operations.
type ComplianceReport struct {
	Environment string
	Service     string
	Version     string
	Compliant   bool
	Evidence    []string
}

// NewComplianceReport builds a compliance record from a validated release plan.
func NewComplianceReport(plan *ReleasePlan) *ComplianceReport {
	if plan == nil {
		return nil
	}
	report := &ComplianceReport{
		Environment: plan.Environment,
		Service:     plan.Service,
		Version:     plan.Version,
		Evidence:    make([]string, 0, 6),
	}
	if plan.Gate != nil && plan.Gate.IsApproved() {
		report.Evidence = append(report.Evidence, "approval gate satisfied")
	}
	if plan.Artifact != nil && strings.TrimSpace(plan.Artifact.Signature) != "" {
		report.Evidence = append(report.Evidence, "artifact signature verified")
	}
	if plan.SLO != nil {
		report.Evidence = append(report.Evidence, "SLO guardrails defined")
	}
	if plan.Rollback != nil {
		report.Evidence = append(report.Evidence, "rollback procedure defined")
	}
	if strings.TrimSpace(plan.Runbook) != "" {
		report.Evidence = append(report.Evidence, "deployment runbook available")
	}
	if len(report.Evidence) >= 5 {
		report.Compliant = true
	}
	return report
}

// PromotionPlan governs a staged environment promotion, ensuring a trusted
// artifact is approved and the target environment policy is satisfied.
type PromotionPlan struct {
	From       string
	To         string
	Service    string
	Version    string
	Gate       *ApprovalGate
	Artifact   *ArtifactProvenance
	Policy     RoutePolicy
	ApprovedAt time.Time
}

// NewPromotionPlan creates a staged environment promotion request.
func NewPromotionPlan(from, to, service, version string) *PromotionPlan {
	if from == "" {
		from = "staging"
	}
	if to == "" {
		to = "prod"
	}
	policy := RoutePolicy{
		Environment: to,
		Service:     service,
	}
	if to == "prod" {
		policy.RequireSignedArtifacts = true
		policy.RequireAudit = true
		policy.SLO = NewSLOPolicy(0.05, 250, 0.02)
	}
	return &PromotionPlan{
		From:    from,
		To:      to,
		Service: service,
		Version: version,
		Policy:  policy,
	}
}

// Validate checks that a promotion has a valid approval gate, signed artifact and
// environment policy for the target stage.
func (p *PromotionPlan) Validate(secret string) error {
	if p == nil {
		return fmt.Errorf("promotion plan is nil")
	}
	if strings.TrimSpace(p.Service) == "" {
		return fmt.Errorf("service name is required")
	}
	if p.Gate == nil {
		return fmt.Errorf("approval gate is required for promotion")
	}
	if !p.Gate.IsApproved() {
		return fmt.Errorf("approval gate is not approved")
	}
	if p.Artifact == nil {
		return fmt.Errorf("artifact provenance is required for promotion")
	}
	if err := p.Artifact.Verify(secret); err != nil {
		return fmt.Errorf("artifact signature verification failed: %w", err)
	}
	if strings.TrimSpace(p.To) == "" {
		return fmt.Errorf("target environment is required")
	}
	if p.Policy.Environment == "" {
		p.Policy.Environment = p.To
	}
	if p.Policy.Service == "" {
		p.Policy.Service = p.Service
	}
	if p.To == "prod" {
		if !p.Policy.RequireSignedArtifacts || !p.Policy.RequireAudit || p.Policy.SLO == nil {
			return fmt.Errorf("production promotion requires signed artifacts, audit logging, and SLO guardrails")
		}
		if len(p.Policy.AllowedTenants) == 0 || len(p.Policy.AllowedRegions) == 0 {
			return fmt.Errorf("production promotion requires explicit tenant and region allowlists")
		}
	}
	if p.Policy.RequireSignedArtifacts && p.Policy.SLO == nil {
		return fmt.Errorf("target stage policy requires a valid SLO guardrail")
	}
	if p.Policy.RequireAudit && p.Policy.SLO == nil {
		return fmt.Errorf("target stage policy requires audit logging and SLO guardrails")
	}
	if p.Policy.Environment != p.To {
		return fmt.Errorf("promotion target mismatch: policy environment %q does not match target %q", p.Policy.Environment, p.To)
	}
	if p.Policy.Service != p.Service {
		return fmt.Errorf("promotion target mismatch: policy service %q does not match service %q", p.Policy.Service, p.Service)
	}
	p.ApprovedAt = time.Now().UTC()
	return nil
}

// DeploymentManifest captures the declarative release state for a specific
// environment and service. It must include a valid stage policy and audit trail
// before the runtime accepts the deployment.
type DeploymentManifest struct {
	Environment string      `json:"environment"`
	Service     string      `json:"service"`
	Version     string      `json:"version"`
	Policy      RoutePolicy `json:"policy"`
	Audit       []string    `json:"audit"`
	CreatedAt   time.Time   `json:"created_at"`
}

// NewDeploymentManifest creates a staged deployment manifest for a target environment.
func NewDeploymentManifest(environment, service, version string) *DeploymentManifest {
	if environment == "" {
		environment = "prod"
	}
	policy := RoutePolicy{
		Environment: environment,
		Service:     service,
	}
	if environment == "prod" {
		policy.RequireSignedArtifacts = true
		policy.RequireAudit = true
		policy.SLO = NewSLOPolicy(0.05, 250, 0.02)
	}
	return &DeploymentManifest{
		Environment: environment,
		Service:     service,
		Version:     version,
		Policy:      policy,
		Audit:       make([]string, 0, 4),
		CreatedAt:   time.Now().UTC(),
	}
}

// Validate ensures the manifest is safe to apply to an environment using the
// target policy and a complete audit trail.
func (m *DeploymentManifest) Validate() error {
	if m == nil {
		return fmt.Errorf("deployment manifest is nil")
	}
	if strings.TrimSpace(m.Service) == "" {
		return fmt.Errorf("service name is required")
	}
	if strings.TrimSpace(m.Environment) == "" {
		return fmt.Errorf("environment is required")
	}
	if m.Policy.Environment == "" {
		m.Policy.Environment = m.Environment
	}
	if m.Policy.Service == "" {
		m.Policy.Service = m.Service
	}
	if m.Environment == "prod" {
		if !m.Policy.RequireSignedArtifacts || !m.Policy.RequireAudit || m.Policy.SLO == nil {
			return fmt.Errorf("production deployment manifest requires signed artifacts, audit logging, and SLO guardrails")
		}
		if len(m.Policy.AllowedTenants) == 0 || len(m.Policy.AllowedRegions) == 0 {
			return fmt.Errorf("production deployment manifest requires explicit tenant and region allowlists")
		}
	}
	if m.Policy.RequireSignedArtifacts && m.Policy.SLO == nil {
		return fmt.Errorf("target stage policy requires a valid SLO guardrail")
	}
	if m.Policy.RequireAudit && m.Policy.SLO == nil {
		return fmt.Errorf("target stage policy requires audit logging and SLO guardrails")
	}
	if m.Policy.Environment != m.Environment {
		return fmt.Errorf("deployment target mismatch: policy environment %q does not match environment %q", m.Policy.Environment, m.Environment)
	}
	if m.Policy.Service != m.Service {
		return fmt.Errorf("deployment target mismatch: policy service %q does not match service %q", m.Policy.Service, m.Service)
	}
	if len(m.Audit) == 0 {
		return fmt.Errorf("deployment audit trail is required")
	}
	return nil
}

// DeploymentManifestRegistry stores validated deployment manifests by service and environment.
type DeploymentManifestRegistry struct {
	mu        sync.RWMutex
	manifests map[string]map[string]*DeploymentManifest
}

// NewDeploymentManifestRegistry creates an empty deployment manifest registry.
func NewDeploymentManifestRegistry() *DeploymentManifestRegistry {
	return &DeploymentManifestRegistry{manifests: make(map[string]map[string]*DeploymentManifest)}
}

// Register stores a manifest for a service and environment, ensuring it validates before admission.
func (r *DeploymentManifestRegistry) Register(manifest *DeploymentManifest) error {
	if r == nil {
		return fmt.Errorf("deployment manifest registry is nil")
	}
	if manifest == nil {
		return fmt.Errorf("deployment manifest is nil")
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("manifest validation failed: %w", err)
	}
	service := strings.TrimSpace(manifest.Service)
	env := strings.TrimSpace(manifest.Environment)
	if service == "" || env == "" {
		return fmt.Errorf("service and environment are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.manifests[service] == nil {
		r.manifests[service] = make(map[string]*DeploymentManifest)
	}
	if _, exists := r.manifests[service][env]; exists {
		return fmt.Errorf("manifest already registered for service %q in environment %q", service, env)
	}
	r.manifests[service][env] = manifest
	return nil
}

// Get retrieves a manifest for the given service and environment.
func (r *DeploymentManifestRegistry) Get(service, environment string) (*DeploymentManifest, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	if service == "" || environment == "" {
		return nil, false
	}
	manifest, ok := r.manifests[service][environment]
	return manifest, ok
}

// Validate resolves and validates a manifest for a service and environment.
func (r *DeploymentManifestRegistry) Validate(service, environment string) error {
	if r == nil {
		return fmt.Errorf("deployment manifest registry is nil")
	}
	manifest, ok := r.Get(service, environment)
	if !ok {
		return fmt.Errorf("manifest for service %q in environment %q not found", service, environment)
	}
	return manifest.Validate()
}

// DeploymentLedger tracks the active deployed version and the rollout history for a
// service in an environment. It is the runtime state backing the operator control plane.
type DeploymentLedger struct {
	mu      sync.RWMutex
	current map[string]map[string]*DeploymentManifest
	history map[string]map[string][]*DeploymentManifest
}

// NewDeploymentLedger creates an empty deployment ledger.
func NewDeploymentLedger() *DeploymentLedger {
	return &DeploymentLedger{
		current: make(map[string]map[string]*DeploymentManifest),
		history: make(map[string]map[string][]*DeploymentManifest),
	}
}

// Record stores a new deployment manifest as the active version and keeps it in the release history.
func (l *DeploymentLedger) Record(manifest *DeploymentManifest) error {
	if l == nil {
		return fmt.Errorf("deployment ledger is nil")
	}
	if manifest == nil {
		return fmt.Errorf("deployment manifest is nil")
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("manifest validation failed: %w", err)
	}
	service := strings.TrimSpace(manifest.Service)
	env := strings.TrimSpace(manifest.Environment)
	if service == "" || env == "" {
		return fmt.Errorf("service and environment are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.current[service] == nil {
		l.current[service] = make(map[string]*DeploymentManifest)
	}
	if l.history[service] == nil {
		l.history[service] = make(map[string][]*DeploymentManifest)
	}
	for _, existing := range l.history[service][env] {
		if existing != nil && existing.Version == manifest.Version {
			return fmt.Errorf("version %q already exists for service %q in environment %q", manifest.Version, service, env)
		}
	}
	l.current[service][env] = manifest
	l.history[service][env] = append(l.history[service][env], manifest)
	return nil
}

// DeploymentLedgerState is the serializable snapshot of a DeploymentLedger,
// used to persist and restore the control plane's deployment state.
type DeploymentLedgerState struct {
	Current map[string]map[string]*DeploymentManifest   `json:"current"`
	History map[string]map[string][]*DeploymentManifest `json:"history"`
}

// DeploymentStore persists and restores a DeploymentLedgerState so the control
// plane can survive process restarts without an external database.
type DeploymentStore interface {
	Save(state DeploymentLedgerState) error
	Load() (DeploymentLedgerState, error)
	// Close releases any resources held by the store (open file handles,
	// database connections, etc.). It is safe to call on nil receivers and
	// must be idempotent. Backends without persistent resources should
	// return nil.
	Close() error
}

// FileDeploymentStore persists deployment ledger state as JSON on the local filesystem.
type FileDeploymentStore struct {
	path string
}

// NewFileDeploymentStore creates a file-backed deployment store at the given path.
func NewFileDeploymentStore(path string) *FileDeploymentStore {
	return &FileDeploymentStore{path: path}
}

// Save writes the deployment ledger state to disk as JSON.
func (s *FileDeploymentStore) Save(state DeploymentLedgerState) error {
	if s == nil || strings.TrimSpace(s.path) == "" {
		return fmt.Errorf("file deployment store requires a path")
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal deployment ledger state: %w", err)
	}
	if dir := filepath.Dir(s.path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create deployment store directory: %w", err)
		}
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		return fmt.Errorf("write deployment ledger state: %w", err)
	}
	return nil
}

// Load reads the deployment ledger state from disk. A missing file is treated
// as empty state rather than an error, so a fresh control plane can boot cleanly.
func (s *FileDeploymentStore) Load() (DeploymentLedgerState, error) {
	empty := DeploymentLedgerState{
		Current: make(map[string]map[string]*DeploymentManifest),
		History: make(map[string]map[string][]*DeploymentManifest),
	}
	if s == nil || strings.TrimSpace(s.path) == "" {
		return empty, fmt.Errorf("file deployment store requires a path")
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return empty, nil
		}
		return empty, fmt.Errorf("read deployment ledger state: %w", err)
	}
	var state DeploymentLedgerState
	if err := json.Unmarshal(data, &state); err != nil {
		return empty, fmt.Errorf("unmarshal deployment ledger state: %w", err)
	}
	if state.Current == nil {
		state.Current = make(map[string]map[string]*DeploymentManifest)
	}
	if state.History == nil {
		state.History = make(map[string]map[string][]*DeploymentManifest)
	}
	return state, nil
}

// Close is a no-op for file-backed stores; the file handle is released
// automatically when the process exits.
func (s *FileDeploymentStore) Close() error {
	return nil
}

// Persist writes the ledger's current state to the given store, making the
// control plane state durable across restarts.
// Snapshot returns a copy of the ledger's serializable state for inspection
// without exposing internal maps to mutation.
func (l *DeploymentLedger) Snapshot() DeploymentLedgerState {
	if l == nil {
		return DeploymentLedgerState{}
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	current := make(map[string]map[string]*DeploymentManifest, len(l.current))
	for svc, envs := range l.current {
		current[svc] = make(map[string]*DeploymentManifest, len(envs))
		for env, m := range envs {
			current[svc][env] = m
		}
	}
	return DeploymentLedgerState{Current: current}
}

func (l *DeploymentLedger) Persist(store DeploymentStore) error {
	if l == nil {
		return fmt.Errorf("deployment ledger is nil")
	}
	if store == nil {
		return fmt.Errorf("deployment store is required")
	}
	l.mu.RLock()
	state := DeploymentLedgerState{Current: l.current, History: l.history}
	l.mu.RUnlock()
	return store.Save(state)
}

// LoadFrom replaces the ledger's in-memory state with the state read from the
// given store, restoring the control plane after a restart.
func (l *DeploymentLedger) LoadFrom(store DeploymentStore) error {
	if l == nil {
		return fmt.Errorf("deployment ledger is nil")
	}
	if store == nil {
		return fmt.Errorf("deployment store is required")
	}
	state, err := store.Load()
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if state.Current == nil {
		state.Current = make(map[string]map[string]*DeploymentManifest)
	}
	if state.History == nil {
		state.History = make(map[string]map[string][]*DeploymentManifest)
	}
	l.current = state.Current
	l.history = state.History
	return nil
}

// Current returns the active manifest for a service in an environment.
func (l *DeploymentLedger) Current(service, environment string) (*DeploymentManifest, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	if service == "" || environment == "" {
		return nil, false
	}
	manifest, ok := l.current[service][environment]
	return manifest, ok
}

// History returns the release history for a service in an environment.
func (l *DeploymentLedger) History(service, environment string) ([]*DeploymentManifest, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	if service == "" || environment == "" {
		return nil, false
	}
	history, ok := l.history[service][environment]
	if !ok {
		return nil, false
	}
	out := append([]*DeploymentManifest(nil), history...)
	return out, true
}

// DeploymentEvent represents a single operator action in the deployment lifecycle
// such as promotion, rollback, or approval.
type DeploymentEvent struct {
	Service     string
	Environment string
	Version     string
	Action      string
	Actor       string
	Reason      string
	CreatedAt   time.Time
}

// DeploymentEventLog stores the operational timeline for a service in a target environment.
type DeploymentEventLog struct {
	mu  sync.RWMutex
	log map[string]map[string][]DeploymentEvent
}

// NewDeploymentEventLog creates an empty deployment event log.
func NewDeploymentEventLog() *DeploymentEventLog {
	return &DeploymentEventLog{log: make(map[string]map[string][]DeploymentEvent)}
}

// Record appends a deployment lifecycle event for a service and environment.
func (l *DeploymentEventLog) Record(service, environment, version, action, actor, reason string) error {
	if l == nil {
		return fmt.Errorf("deployment event log is nil")
	}
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	action = strings.TrimSpace(action)
	actor = strings.TrimSpace(actor)
	if service == "" || environment == "" || action == "" {
		return fmt.Errorf("service, environment, and action are required")
	}
	if actor == "" {
		return fmt.Errorf("actor is required for operator actions")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.log[service] == nil {
		l.log[service] = make(map[string][]DeploymentEvent)
	}
	l.log[service][environment] = append(l.log[service][environment], DeploymentEvent{
		Service:     service,
		Environment: environment,
		Version:     strings.TrimSpace(version),
		Action:      action,
		Actor:       actor,
		Reason:      strings.TrimSpace(reason),
		CreatedAt:   time.Now().UTC(),
	})
	return nil
}

// History returns the deployment event timeline for the given service and environment.
func (l *DeploymentEventLog) History(service, environment string) ([]DeploymentEvent, bool) {
	if l == nil {
		return nil, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	if service == "" || environment == "" {
		return nil, false
	}
	events, ok := l.log[service][environment]
	if !ok {
		return nil, false
	}
	out := append([]DeploymentEvent(nil), events...)
	return out, true
}

// Latest returns the most recent event for the given service and environment.
func (l *DeploymentEventLog) Latest(service, environment string) (DeploymentEvent, bool) {
	if l == nil {
		return DeploymentEvent{}, false
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	if service == "" || environment == "" {
		return DeploymentEvent{}, false
	}
	events, ok := l.log[service][environment]
	if !ok || len(events) == 0 {
		return DeploymentEvent{}, false
	}
	return events[len(events)-1], true
}

// ReleaseWorkflow tracks the state of a production deployment decision across
// approval, promotion, and rollback phases.
type ReleaseWorkflow struct {
	Service     string
	Environment string
	Version     string
	Gate        *ApprovalGate
	EventLog    *DeploymentEventLog
	State       string
	ApprovedAt  time.Time
	UpdatedAt   time.Time
}

// NewReleaseWorkflow creates a new release workflow for a service and target environment.
func NewReleaseWorkflow(service, environment, version string) *ReleaseWorkflow {
	if service == "" {
		service = "unknown-service"
	}
	if environment == "" {
		environment = "prod"
	}
	return &ReleaseWorkflow{
		Service:     service,
		Environment: environment,
		Version:     version,
		State:       "pending",
		EventLog:    NewDeploymentEventLog(),
		UpdatedAt:   time.Now().UTC(),
	}
}

// Approve records an approval for the workflow and advances the state once the gate threshold is reached.
func (w *ReleaseWorkflow) Approve(actor string) error {
	if w == nil {
		return fmt.Errorf("release workflow is nil")
	}
	if w.State == "rejected" || w.State == "rolled_back" {
		return fmt.Errorf("release workflow is terminal in state %q", w.State)
	}
	if w.Gate == nil {
		return fmt.Errorf("approval gate is required")
	}
	if actor == "" {
		return fmt.Errorf("approver identity is required")
	}
	if err := w.Gate.Approve(actor); err != nil {
		return err
	}
	w.UpdatedAt = time.Now().UTC()
	if w.Gate.IsApproved() {
		w.State = "approved"
		w.ApprovedAt = w.UpdatedAt
	}
	if w.EventLog != nil {
		if err := w.EventLog.Record(w.Service, w.Environment, w.Version, "approval", actor, "approval recorded"); err != nil {
			return err
		}
	}
	return nil
}

// IsApproved reports whether the workflow has reached the approval threshold.
func (w *ReleaseWorkflow) IsApproved() bool {
	if w == nil || w.Gate == nil {
		return false
	}
	return w.Gate.IsApproved()
}

// Rollback transitions the workflow to a rollback state and records the operator action.
func (w *ReleaseWorkflow) Rollback(actor, reason string) error {
	if w == nil {
		return fmt.Errorf("release workflow is nil")
	}
	if w.State == "rejected" || w.State == "rolled_back" {
		return fmt.Errorf("release workflow is terminal in state %q", w.State)
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("actor is required for rollback")
	}
	w.State = "rolled_back"
	w.UpdatedAt = time.Now().UTC()
	if w.EventLog != nil {
		return w.EventLog.Record(w.Service, w.Environment, w.Version, "rollback", actor, reason)
	}
	return nil
}

// Reject ends the release workflow in a rejected state, preventing additional transitions.
func (w *ReleaseWorkflow) Reject(actor, reason string) error {
	if w == nil {
		return fmt.Errorf("release workflow is nil")
	}
	if w.State == "rejected" || w.State == "rolled_back" {
		return fmt.Errorf("release workflow is terminal in state %q", w.State)
	}
	if strings.TrimSpace(actor) == "" {
		return fmt.Errorf("actor is required for rejection")
	}
	w.State = "rejected"
	w.UpdatedAt = time.Now().UTC()
	if w.EventLog != nil {
		return w.EventLog.Record(w.Service, w.Environment, w.Version, "reject", actor, reason)
	}
	return nil
}
