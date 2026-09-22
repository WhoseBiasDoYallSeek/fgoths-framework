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

import "testing"

func TestRouteRegistrySnapshotDiffTracksConfigChanges(t *testing.T) {
	before := RouteRegistrySnapshot{
		Routes: []RouteConfig{{
			Name:    "orders-api",
			Method:  "GET",
			Path:    "/orders",
			Target:  "http://orders.internal/v1",
			Weight:  100,
			Enabled: true,
		}},
	}
	after := RouteRegistrySnapshot{
		Routes: []RouteConfig{{
			Name:    "orders-api",
			Method:  "GET",
			Path:    "/orders",
			Target:  "http://orders.internal/v2",
			Weight:  50,
			Enabled: true,
		}},
	}

	diff := BuildConfigDiff(before, after)
	if !diff.HasChanges {
		t.Fatal("expected config diff to report changes")
	}
	if len(diff.Routes) != 1 {
		t.Fatalf("expected one changed route, got %d", len(diff.Routes))
	}
	if diff.Routes[0].Before != "http://orders.internal/v1" {
		t.Fatalf("expected previous target v1, got %q", diff.Routes[0].Before)
	}
}

func TestApprovalGateRequiresExplicitApproval(t *testing.T) {
	gate := NewApprovalGate("prod", 2)
	if gate.IsApproved() {
		t.Fatal("expected gate to require approval before being marked approved")
	}
	if err := gate.Approve("alice"); err != nil {
		t.Fatalf("approve first signoff: %v", err)
	}
	if gate.IsApproved() {
		t.Fatal("expected approval gate to remain pending until threshold is reached")
	}
	if err := gate.Approve("bob"); err != nil {
		t.Fatalf("approve second signoff: %v", err)
	}
	if !gate.IsApproved() {
		t.Fatal("expected gate to be approved after threshold")
	}
}

func TestReleasePlanRequiresApprovalRunbookAndSignedArtifact(t *testing.T) {
	artifact := NewArtifactProvenance("orders-api", "2.0.0")
	artifact.SetContent("orders-api release candidate")
	artifact.Sign("release-secret", "platform")

	gate := NewApprovalGate("prod", 2)
	if err := gate.Approve("alice"); err != nil {
		t.Fatalf("approve first signoff: %v", err)
	}
	if err := gate.Approve("bob"); err != nil {
		t.Fatalf("approve second signoff: %v", err)
	}

	plan := NewReleasePlan("prod", "orders-api", "2.0.0")
	plan.Gate = gate
	plan.Runbook = "check health, validate metrics, and verify rollback is available"
	plan.Artifact = artifact
	plan.SLO = NewSLOPolicy(0.05, 250, 0.02)
	plan.Rollback = NewRollbackPlan("rollback to previous stable image", "v1.9.0", []string{"pause traffic", "restore previous deployment", "verify health"})

	if err := plan.Validate("release-secret"); err != nil {
		t.Fatalf("expected a ready production release, got error: %v", err)
	}
	if !plan.IsReady("release-secret") {
		t.Fatal("expected the release plan to report ready")
	}
}

func TestReleasePlanRequiresSLOAndRollbackBeforeProduction(t *testing.T) {
	artifact := NewArtifactProvenance("orders-api", "2.0.0")
	artifact.SetContent("orders-api release candidate")
	artifact.Sign("release-secret", "platform")

	gate := NewApprovalGate("prod", 1)
	if err := gate.Approve("alice"); err != nil {
		t.Fatalf("approve signoff: %v", err)
	}

	plan := NewReleasePlan("prod", "orders-api", "2.0.0")
	plan.Gate = gate
	plan.Artifact = artifact
	plan.Runbook = "verify metrics and health"

	if err := plan.Validate("release-secret"); err == nil {
		t.Fatal("expected production release to require SLO policy and rollback plan")
	}
}

func TestComplianceReportMarksEnterpriseReleaseReady(t *testing.T) {
	artifact := NewArtifactProvenance("orders-api", "2.0.0")
	artifact.SetContent("orders-api release candidate")
	artifact.Sign("release-secret", "platform")

	gate := NewApprovalGate("prod", 2)
	if err := gate.Approve("alice"); err != nil {
		t.Fatalf("approve alice: %v", err)
	}
	if err := gate.Approve("bob"); err != nil {
		t.Fatalf("approve bob: %v", err)
	}

	plan := NewReleasePlan("prod", "orders-api", "2.0.0")
	plan.Gate = gate
	plan.Artifact = artifact
	plan.Runbook = "check health, validate metrics, and verify rollback is available"
	plan.SLO = NewSLOPolicy(0.05, 250, 0.02)
	plan.Rollback = NewRollbackPlan("rollback to previous stable image", "v1.9.0", []string{"pause traffic", "restore deployment", "verify health"})

	if err := plan.Validate("release-secret"); err != nil {
		t.Fatalf("unexpected validation failure: %v", err)
	}

	report := NewComplianceReport(plan)
	if report == nil {
		t.Fatal("expected compliance report to be generated")
	}
	if !report.Compliant {
		t.Fatal("expected enterprise release to be compliant")
	}
	if len(report.Evidence) == 0 {
		t.Fatal("expected compliance evidence to be captured")
	}
}

func TestPromotionPlanRequiresStagePolicyAndApproval(t *testing.T) {
	artifact := NewArtifactProvenance("orders-api", "2.1.0")
	artifact.SetContent("orders-api promotion candidate")
	artifact.Sign("release-secret", "platform")

	gate := NewApprovalGate("staging-to-prod", 2)
	if err := gate.Approve("alice"); err != nil {
		t.Fatalf("approve alice: %v", err)
	}
	if err := gate.Approve("bob"); err != nil {
		t.Fatalf("approve bob: %v", err)
	}

	plan := NewPromotionPlan("staging", "prod", "orders-api", "2.1.0")
	plan.Gate = gate
	plan.Artifact = artifact
	plan.Policy = RoutePolicy{
		Name:                   "orders-prod",
		Environment:            "prod",
		Service:                "orders-api",
		AllowedTenants:         []string{"tenant-a"},
		AllowedRegions:         []string{"us-east"},
		RequireSignedArtifacts: true,
		RequireAudit:           true,
		SLO:                    NewSLOPolicy(0.05, 250, 0.02),
	}

	if err := plan.Validate("release-secret"); err != nil {
		t.Fatalf("expected a valid production promotion, got error: %v", err)
	}

	blocked := NewPromotionPlan("dev", "prod", "orders-api", "2.1.0")
	blocked.Gate = NewApprovalGate("dev-to-prod", 1)
	if err := blocked.Gate.Approve("alice"); err != nil {
		t.Fatalf("approve blocked promotion: %v", err)
	}
	blocked.Artifact = artifact
	blocked.Policy = RoutePolicy{Name: "orders-prod", Environment: "prod", Service: "orders-api"}
	if err := blocked.Validate("release-secret"); err == nil {
		t.Fatal("expected promotion from dev to prod to require a valid stage policy and approval path")
	}
}

func TestDeploymentManifestRequiresEnvironmentPolicyAndAuditMetadata(t *testing.T) {
	manifest := NewDeploymentManifest("prod", "orders-api", "2.1.0")
	manifest.Policy = RoutePolicy{
		Name:                   "orders-prod",
		Environment:            "prod",
		Service:                "orders-api",
		AllowedTenants:         []string{"tenant-a"},
		AllowedRegions:         []string{"us-east"},
		RequireSignedArtifacts: true,
		RequireAudit:           true,
		SLO:                    NewSLOPolicy(0.05, 250, 0.02),
	}
	manifest.Audit = []string{"approvals recorded", "artifact signature verified", "slo guardrail loaded"}

	if err := manifest.Validate(); err != nil {
		t.Fatalf("expected a valid deployment manifest, got error: %v", err)
	}

	invalid := NewDeploymentManifest("prod", "", "2.1.0")
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected missing service name to fail deployment manifest validation")
	}
}

func TestDeploymentManifestRegistryStoresAndValidatesManifests(t *testing.T) {
	registry := NewDeploymentManifestRegistry()
	manifest := NewDeploymentManifest("prod", "orders-api", "2.1.0")
	manifest.Policy = RoutePolicy{
		Name:                   "orders-prod",
		Environment:            "prod",
		Service:                "orders-api",
		AllowedTenants:         []string{"tenant-a"},
		AllowedRegions:         []string{"us-east"},
		RequireSignedArtifacts: true,
		RequireAudit:           true,
		SLO:                    NewSLOPolicy(0.05, 250, 0.02),
	}
	manifest.Audit = []string{"approval", "artifact", "slo"}

	if err := registry.Register(manifest); err != nil {
		t.Fatalf("register manifest: %v", err)
	}
	stored, ok := registry.Get("orders-api", "prod")
	if !ok {
		t.Fatal("expected manifest to be stored by service and environment")
	}
	if stored.Version != "2.1.0" {
		t.Fatalf("expected version 2.1.0, got %q", stored.Version)
	}
	if err := registry.Validate("orders-api", "prod"); err != nil {
		t.Fatalf("expected manifest validation to pass: %v", err)
	}
	if err := registry.Validate("billing-api", "prod"); err == nil {
		t.Fatal("expected unknown manifest to fail validation")
	}
}

func TestDeploymentLedgerTracksActiveVersionAndReleaseHistory(t *testing.T) {
	ledger := NewDeploymentLedger()

	manifestV1 := NewDeploymentManifest("prod", "orders-api", "2.1.0")
	manifestV1.Policy = RoutePolicy{
		Name:                   "orders-prod",
		Environment:            "prod",
		Service:                "orders-api",
		AllowedTenants:         []string{"tenant-a"},
		AllowedRegions:         []string{"us-east"},
		RequireSignedArtifacts: true,
		RequireAudit:           true,
		SLO:                    NewSLOPolicy(0.05, 250, 0.02),
	}
	manifestV1.Audit = []string{"approval", "artifact", "slo"}
	if err := ledger.Record(manifestV1); err != nil {
		t.Fatalf("record v1: %v", err)
	}

	manifestV2 := NewDeploymentManifest("prod", "orders-api", "2.2.0")
	manifestV2.Policy = manifestV1.Policy
	manifestV2.Audit = []string{"approval", "artifact", "slo", "rollback-window"}
	if err := ledger.Record(manifestV2); err != nil {
		t.Fatalf("record v2: %v", err)
	}

	current, ok := ledger.Current("orders-api", "prod")
	if !ok {
		t.Fatal("expected current manifest to be tracked")
	}
	if current.Version != "2.2.0" {
		t.Fatalf("expected current version 2.2.0, got %q", current.Version)
	}

	history, ok := ledger.History("orders-api", "prod")
	if !ok {
		t.Fatal("expected release history to be tracked")
	}
	if len(history) != 2 {
		t.Fatalf("expected two versions in history, got %d", len(history))
	}
	if err := ledger.Record(manifestV2); err == nil {
		t.Fatal("expected duplicate version record to fail")
	}
}

func TestDeploymentEventLogTracksOperatorTransitions(t *testing.T) {
	log := NewDeploymentEventLog()

	if err := log.Record("orders-api", "prod", "2.1.0", "promote", "alice", "staging passed SLO checks"); err != nil {
		t.Fatalf("record promote event: %v", err)
	}
	if err := log.Record("orders-api", "prod", "2.1.0", "rollback", "sre-bot", "error budget breached"); err != nil {
		t.Fatalf("record rollback event: %v", err)
	}

	events, ok := log.History("orders-api", "prod")
	if !ok {
		t.Fatal("expected deployment events to be tracked")
	}
	if len(events) != 2 {
		t.Fatalf("expected two operator events, got %d", len(events))
	}
	if events[len(events)-1].Action != "rollback" {
		t.Fatalf("expected latest action to be rollback, got %q", events[len(events)-1].Action)
	}

	latest, ok := log.Latest("orders-api", "prod")
	if !ok {
		t.Fatal("expected latest event to be available")
	}
	if latest.Actor != "sre-bot" {
		t.Fatalf("expected latest actor to be sre-bot, got %q", latest.Actor)
	}
	if err := log.Record("orders-api", "prod", "2.2.0", "promote", "", "missing actor should fail"); err == nil {
		t.Fatal("expected empty actor to fail event registration")
	}
}

func TestReleaseWorkflowTracksApprovalAndRollbackState(t *testing.T) {
	workflow := NewReleaseWorkflow("orders-api", "prod", "2.2.0")
	workflow.Gate = NewApprovalGate("prod-release", 2)
	if err := workflow.Approve("alice"); err != nil {
		t.Fatalf("approve first actor: %v", err)
	}
	if workflow.IsApproved() {
		t.Fatal("expected workflow to remain pending until threshold is reached")
	}
	if err := workflow.Approve("bob"); err != nil {
		t.Fatalf("approve second actor: %v", err)
	}
	if !workflow.IsApproved() {
		t.Fatal("expected workflow to be approved after threshold")
	}
	if err := workflow.Rollback("sre-bot", "error budget exceeded"); err != nil {
		t.Fatalf("rollback workflow: %v", err)
	}
	if workflow.State != "rolled_back" {
		t.Fatalf("expected workflow state to be rolled_back, got %q", workflow.State)
	}
	if err := workflow.Reject("security", "artifact not signed"); err == nil {
		t.Fatal("expected a terminal workflow to reject additional transitions")
	}
}

func TestReleaseWorkflowRejectFromPendingState(t *testing.T) {
	workflow := NewReleaseWorkflow("orders-api", "staging", "1.0.0")
	if err := workflow.Reject("security", "artifact not signed"); err != nil {
		t.Fatalf("reject pending workflow: %v", err)
	}
	if workflow.State != "rejected" {
		t.Fatalf("expected workflow state to be rejected, got %q", workflow.State)
	}
	events, ok := workflow.EventLog.History("orders-api", "staging")
	if !ok || len(events) != 1 || events[0].Action != "reject" {
		t.Fatalf("expected reject event to be recorded, got %#v ok=%v", events, ok)
	}
	if err := workflow.Approve("alice"); err == nil {
		t.Fatal("expected a rejected workflow to reject further approvals")
	}
}

func TestReleaseWorkflowRequiresActorForRollbackAndReject(t *testing.T) {
	workflow := NewReleaseWorkflow("orders-api", "staging", "1.0.0")
	if err := workflow.Rollback("", "no actor"); err == nil {
		t.Fatal("expected rollback without an actor to fail")
	}
	if err := workflow.Reject("", "no actor"); err == nil {
		t.Fatal("expected reject without an actor to fail")
	}
}

func TestDeploymentLedgerPersistsAndRestoresStateFromFileStore(t *testing.T) {
	dir := t.TempDir()
	store := NewFileDeploymentStore(dir + "/ledger.json")

	ledger := NewDeploymentLedger()
	manifest := NewDeploymentManifest("staging", "orders-api", "1.0.0")
	manifest.Audit = append(manifest.Audit, "reviewed by alice")
	if err := ledger.Record(manifest); err != nil {
		t.Fatalf("record manifest: %v", err)
	}
	if err := ledger.Persist(store); err != nil {
		t.Fatalf("persist ledger: %v", err)
	}

	restored := NewDeploymentLedger()
	if err := restored.LoadFrom(store); err != nil {
		t.Fatalf("load ledger: %v", err)
	}
	current, ok := restored.Current("orders-api", "staging")
	if !ok {
		t.Fatal("expected restored ledger to have current manifest")
	}
	if current.Version != "1.0.0" {
		t.Fatalf("expected restored version 1.0.0, got %q", current.Version)
	}
	history, ok := restored.History("orders-api", "staging")
	if !ok || len(history) != 1 {
		t.Fatalf("expected restored history with one entry, got %v ok=%v", history, ok)
	}
}

func TestFileDeploymentStoreLoadMissingFileReturnsEmptyState(t *testing.T) {
	store := NewFileDeploymentStore(t.TempDir() + "/missing.json")
	state, err := store.Load()
	if err != nil {
		t.Fatalf("load missing store should not error: %v", err)
	}
	if len(state.Current) != 0 {
		t.Fatalf("expected empty state for missing file, got %+v", state)
	}
}

func TestNewApprovalGateDefaultsRequiredToOne(t *testing.T) {
	gate := NewApprovalGate("release", 0)
	if gate.Required != 1 {
		t.Fatalf("expected non-positive required count to default to 1, got %d", gate.Required)
	}
	gate2 := NewApprovalGate("release", -5)
	if gate2.Required != 1 {
		t.Fatalf("expected negative required count to default to 1, got %d", gate2.Required)
	}
}

func TestApprovalGateNilAndDuplicateSafety(t *testing.T) {
	var gate *ApprovalGate
	if err := gate.Approve("alice"); err == nil {
		t.Fatal("expected Approve on a nil gate to fail")
	}
	if gate.IsApproved() {
		t.Fatal("expected IsApproved on a nil gate to be false")
	}

	realGate := NewApprovalGate("release", 2)
	if err := realGate.Approve("alice"); err != nil {
		t.Fatalf("approve alice: %v", err)
	}
	if err := realGate.Approve("alice"); err != nil {
		t.Fatalf("expected re-approving the same actor to be a no-op, got error: %v", err)
	}
	if len(realGate.Approvals) != 1 {
		t.Fatalf("expected duplicate approval to not be recorded twice, got %d", len(realGate.Approvals))
	}
	if err := realGate.Approve(""); err == nil {
		t.Fatal("expected an empty actor to fail approval")
	}
}

func TestNewSLOPolicyAppliesDefaultsForNonPositiveValues(t *testing.T) {
	slo := NewSLOPolicy(0, 0, 0)
	if slo.ErrorRateThreshold != 0.05 || slo.LatencyThresholdMS != 250 || slo.BurnRateLimit != 0.02 {
		t.Fatalf("expected default SLO thresholds, got %+v", slo)
	}
	slo2 := NewSLOPolicy(-1, -1, -1)
	if slo2.ErrorRateThreshold != 0.05 || slo2.LatencyThresholdMS != 250 || slo2.BurnRateLimit != 0.02 {
		t.Fatalf("expected default SLO thresholds for negative inputs, got %+v", slo2)
	}
}

func TestNewRollbackPlanDefaultsReasonAndPrevious(t *testing.T) {
	plan := NewRollbackPlan("", "", []string{"drain traffic"})
	if plan.Reason != "rollback requested" {
		t.Fatalf("expected default reason, got %q", plan.Reason)
	}
	if plan.Previous != "previous stable version" {
		t.Fatalf("expected default previous version, got %q", plan.Previous)
	}
}

func TestReleasePlanIsReadyReflectsValidation(t *testing.T) {
	var nilPlan *ReleasePlan
	if nilPlan.IsReady("secret") {
		t.Fatal("expected IsReady on a nil release plan to be false")
	}

	plan := NewReleasePlan("prod", "orders-api", "1.0.0")
	if plan.IsReady("secret") {
		t.Fatal("expected an incomplete release plan to not be ready")
	}
}

func TestNewComplianceReportNilPlanReturnsNil(t *testing.T) {
	if report := NewComplianceReport(nil); report != nil {
		t.Fatalf("expected nil compliance report for a nil plan, got %+v", report)
	}
}

func TestNewComplianceReportPartialEvidenceIsNotCompliant(t *testing.T) {
	plan := NewReleasePlan("prod", "orders-api", "1.0.0")
	plan.Gate = NewApprovalGate("release", 1)
	if err := plan.Gate.Approve("alice"); err != nil {
		t.Fatalf("approve: %v", err)
	}
	report := NewComplianceReport(plan)
	if report.Compliant {
		t.Fatal("expected a plan with only partial evidence to be non-compliant")
	}
	if len(report.Evidence) != 1 {
		t.Fatalf("expected exactly one piece of evidence, got %#v", report.Evidence)
	}
}

func TestNewPromotionPlanDefaultsFromAndTo(t *testing.T) {
	plan := NewPromotionPlan("", "", "orders-api", "1.0.0")
	if plan.From != "staging" || plan.To != "prod" {
		t.Fatalf("expected default from/to staging->prod, got %q -> %q", plan.From, plan.To)
	}
	if !plan.Policy.RequireSignedArtifacts || !plan.Policy.RequireAudit || plan.Policy.SLO == nil {
		t.Fatal("expected default prod policy to require signed artifacts, audit, and SLO")
	}
}

func TestPromotionPlanValidateNilSafety(t *testing.T) {
	var plan *PromotionPlan
	if err := plan.Validate("secret"); err == nil {
		t.Fatal("expected Validate on a nil promotion plan to fail")
	}
}

func TestDeploymentManifestValidateNilSafety(t *testing.T) {
	var manifest *DeploymentManifest
	if err := manifest.Validate(); err == nil {
		t.Fatal("expected Validate on a nil manifest to fail")
	}
}

func TestNewDeploymentManifestDefaultsEnvironment(t *testing.T) {
	manifest := NewDeploymentManifest("", "orders-api", "1.0.0")
	if manifest.Environment != "prod" {
		t.Fatalf("expected default environment prod, got %q", manifest.Environment)
	}
}

func TestNewReleaseWorkflowDefaultsServiceAndEnvironment(t *testing.T) {
	workflow := NewReleaseWorkflow("", "", "1.0.0")
	if workflow.Service != "unknown-service" {
		t.Fatalf("expected default service name, got %q", workflow.Service)
	}
	if workflow.Environment != "prod" {
		t.Fatalf("expected default environment prod, got %q", workflow.Environment)
	}
}

func TestReleaseWorkflowNilSafety(t *testing.T) {
	var workflow *ReleaseWorkflow
	if err := workflow.Approve("alice"); err == nil {
		t.Fatal("expected Approve on a nil workflow to fail")
	}
	if workflow.IsApproved() {
		t.Fatal("expected IsApproved on a nil workflow to be false")
	}
	if err := workflow.Rollback("alice", "reason"); err == nil {
		t.Fatal("expected Rollback on a nil workflow to fail")
	}
	if err := workflow.Reject("alice", "reason"); err == nil {
		t.Fatal("expected Reject on a nil workflow to fail")
	}
}

func TestReleaseWorkflowApproveWithoutGateFails(t *testing.T) {
	workflow := NewReleaseWorkflow("orders-api", "prod", "1.0.0")
	if err := workflow.Approve("alice"); err == nil {
		t.Fatal("expected Approve without a configured gate to fail")
	}
}

func TestDeploymentLedgerNilAndInvalidInputSafety(t *testing.T) {
	var ledger *DeploymentLedger
	if err := ledger.Record(NewDeploymentManifest("prod", "orders-api", "1.0.0")); err == nil {
		t.Fatal("expected Record on a nil ledger to fail")
	}
	if _, ok := ledger.Current("orders-api", "prod"); ok {
		t.Fatal("expected Current on a nil ledger to report not found")
	}
	if _, ok := ledger.History("orders-api", "prod"); ok {
		t.Fatal("expected History on a nil ledger to report not found")
	}

	realLedger := NewDeploymentLedger()
	if err := realLedger.Record(nil); err == nil {
		t.Fatal("expected Record with a nil manifest to fail")
	}
}

func TestDeploymentEventLogNilSafety(t *testing.T) {
	var log *DeploymentEventLog
	if err := log.Record("svc", "prod", "1.0.0", "promote", "alice", "reason"); err == nil {
		t.Fatal("expected Record on a nil event log to fail")
	}
	if _, ok := log.History("svc", "prod"); ok {
		t.Fatal("expected History on a nil event log to report not found")
	}
	if _, ok := log.Latest("svc", "prod"); ok {
		t.Fatal("expected Latest on a nil event log to report not found")
	}
}

func TestDeploymentLedgerPersistAndLoadFromNilSafety(t *testing.T) {
	var ledger *DeploymentLedger
	if err := ledger.Persist(NewFileDeploymentStore(t.TempDir() + "/state.json")); err == nil {
		t.Fatal("expected Persist on a nil ledger to fail")
	}
	if err := ledger.LoadFrom(NewFileDeploymentStore(t.TempDir() + "/state.json")); err == nil {
		t.Fatal("expected LoadFrom on a nil ledger to fail")
	}

	realLedger := NewDeploymentLedger()
	if err := realLedger.Persist(nil); err == nil {
		t.Fatal("expected Persist with a nil store to fail")
	}
	if err := realLedger.LoadFrom(nil); err == nil {
		t.Fatal("expected LoadFrom with a nil store to fail")
	}
}

func TestFileDeploymentStoreRequiresPath(t *testing.T) {
	store := &FileDeploymentStore{}
	if err := store.Save(DeploymentLedgerState{}); err == nil {
		t.Fatal("expected Save without a path to fail")
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("expected Load without a path to fail")
	}
}

func TestPromotionPlanValidateBranches(t *testing.T) {
	validArtifact := func() *ArtifactProvenance {
		a := NewArtifactProvenance("orders-api", "1.0.0")
		a.SetContent("payload")
		a.Sign("secret", "platform")
		return a
	}
	approvedGate := func() *ApprovalGate {
		g := NewApprovalGate("gate", 1)
		_ = g.Approve("alice")
		return g
	}

	t.Run("missing service", func(t *testing.T) {
		p := NewPromotionPlan("staging", "prod", "", "1.0.0")
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected missing service to fail validation")
		}
	})
	t.Run("missing gate", func(t *testing.T) {
		p := NewPromotionPlan("staging", "prod", "orders-api", "1.0.0")
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected missing gate to fail validation")
		}
	})
	t.Run("gate not approved", func(t *testing.T) {
		p := NewPromotionPlan("staging", "prod", "orders-api", "1.0.0")
		p.Gate = NewApprovalGate("gate", 1)
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected an unapproved gate to fail validation")
		}
	})
	t.Run("missing artifact", func(t *testing.T) {
		p := NewPromotionPlan("staging", "prod", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected missing artifact to fail validation")
		}
	})
	t.Run("artifact verification fails", func(t *testing.T) {
		p := NewPromotionPlan("staging", "prod", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		if err := p.Validate("wrong-secret"); err == nil {
			t.Fatal("expected artifact verification failure to fail validation")
		}
	})
	t.Run("missing target environment", func(t *testing.T) {
		p := NewPromotionPlan("staging", "", "orders-api", "1.0.0")
		p.To = ""
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected missing target environment to fail validation")
		}
	})
	t.Run("prod missing tenant and region allowlists", func(t *testing.T) {
		p := NewPromotionPlan("staging", "prod", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		p.Policy.RequireSignedArtifacts = true
		p.Policy.RequireAudit = true
		p.Policy.SLO = NewSLOPolicy(0.05, 250, 0.02)
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected missing tenant/region allowlists to fail production validation")
		}
	})
	t.Run("non-prod signed artifacts require SLO", func(t *testing.T) {
		p := NewPromotionPlan("staging", "canary", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		p.Policy.RequireSignedArtifacts = true
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected RequireSignedArtifacts without SLO to fail validation")
		}
	})
	t.Run("non-prod audit requires SLO", func(t *testing.T) {
		p := NewPromotionPlan("staging", "canary", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		p.Policy.RequireAudit = true
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected RequireAudit without SLO to fail validation")
		}
	})
	t.Run("policy environment mismatch", func(t *testing.T) {
		p := NewPromotionPlan("staging", "canary", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		p.Policy.Environment = "other-env"
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected policy/target environment mismatch to fail validation")
		}
	})
	t.Run("policy service mismatch", func(t *testing.T) {
		p := NewPromotionPlan("staging", "canary", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		p.Policy.Service = "other-service"
		if err := p.Validate("secret"); err == nil {
			t.Fatal("expected policy/service mismatch to fail validation")
		}
	})
	t.Run("valid non-prod promotion succeeds", func(t *testing.T) {
		p := NewPromotionPlan("staging", "canary", "orders-api", "1.0.0")
		p.Gate = approvedGate()
		p.Artifact = validArtifact()
		if err := p.Validate("secret"); err != nil {
			t.Fatalf("expected a minimal non-prod promotion to succeed, got %v", err)
		}
	})
}

func TestDeploymentManifestValidateBranches(t *testing.T) {
	t.Run("missing service", func(t *testing.T) {
		m := NewDeploymentManifest("staging", "", "1.0.0")
		if err := m.Validate(); err == nil {
			t.Fatal("expected missing service to fail validation")
		}
	})
	t.Run("missing environment", func(t *testing.T) {
		m := NewDeploymentManifest("staging", "orders-api", "1.0.0")
		m.Environment = ""
		if err := m.Validate(); err == nil {
			t.Fatal("expected missing environment to fail validation")
		}
	})
	t.Run("non-prod signed artifacts require SLO", func(t *testing.T) {
		m := NewDeploymentManifest("canary", "orders-api", "1.0.0")
		m.Policy.RequireSignedArtifacts = true
		m.Audit = []string{"reviewed"}
		if err := m.Validate(); err == nil {
			t.Fatal("expected RequireSignedArtifacts without SLO to fail validation")
		}
	})
	t.Run("non-prod audit requires SLO", func(t *testing.T) {
		m := NewDeploymentManifest("canary", "orders-api", "1.0.0")
		m.Policy.RequireAudit = true
		m.Audit = []string{"reviewed"}
		if err := m.Validate(); err == nil {
			t.Fatal("expected RequireAudit without SLO to fail validation")
		}
	})
	t.Run("policy environment mismatch", func(t *testing.T) {
		m := NewDeploymentManifest("canary", "orders-api", "1.0.0")
		m.Policy.Environment = "other"
		m.Audit = []string{"reviewed"}
		if err := m.Validate(); err == nil {
			t.Fatal("expected policy/environment mismatch to fail validation")
		}
	})
	t.Run("policy service mismatch", func(t *testing.T) {
		m := NewDeploymentManifest("canary", "orders-api", "1.0.0")
		m.Policy.Service = "other-service"
		m.Audit = []string{"reviewed"}
		if err := m.Validate(); err == nil {
			t.Fatal("expected policy/service mismatch to fail validation")
		}
	})
	t.Run("missing audit trail", func(t *testing.T) {
		m := NewDeploymentManifest("canary", "orders-api", "1.0.0")
		if err := m.Validate(); err == nil {
			t.Fatal("expected an empty audit trail to fail validation")
		}
	})
	t.Run("valid non-prod manifest succeeds", func(t *testing.T) {
		m := NewDeploymentManifest("canary", "orders-api", "1.0.0")
		m.Audit = []string{"reviewed"}
		if err := m.Validate(); err != nil {
			t.Fatalf("expected a minimal non-prod manifest to succeed, got %v", err)
		}
	})
}
