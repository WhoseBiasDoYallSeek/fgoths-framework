// Copyright (c) 2026, srars-tech
// SPDX-License-Identifier: Apache-2.0
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build sqlite

package runtime

import (
	"path/filepath"
	"testing"
)

func TestSQLiteStoreNewWithEmptyPath(t *testing.T) {
	if _, err := NewSQLiteDeploymentStore(""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestSQLiteStoreSaveAfterClose(t *testing.T) {
	store := openTestSQLiteStore(t)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	l := NewDeploymentLedger()
	_ = l.Record(manifestFor("svc", "prod", "v1.0.0"))
	if err := l.Persist(store); err == nil {
		t.Fatal("expected error when persisting to a closed store")
	}
}

func TestSQLiteStoreRejectsFutureVersion(t *testing.T) {
	// A database from a newer binary must not be silently downgraded.
	path := filepath.Join(t.TempDir(), "corrupt_future.db")
	store, err := NewSQLiteDeploymentStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := store.db.Exec("INSERT OR REPLACE INTO schema_version (version) VALUES (999)"); err != nil {
		t.Fatalf("corrupt version: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := NewSQLiteDeploymentStore(path); err == nil {
		t.Fatal("expected reopening a future schema to fail")
	}
}

func openTestSQLiteStore(t *testing.T) *SQLiteDeploymentStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "controlplane.db")
	store, err := NewSQLiteDeploymentStore(path)
	if err != nil {
		t.Fatalf("open sqlite store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func manifestFor(service, env, version string) *DeploymentManifest {
	m := NewDeploymentManifest(env, service, version)
	m.Audit = []string{"test audit trail"}
	if env == "prod" {
		m.Policy.AllowedTenants = []string{"acme"}
		m.Policy.AllowedRegions = []string{"us-east"}
	}
	return m
}

func TestSQLiteDeploymentStoreRoundTrip(t *testing.T) {
	store := openTestSQLiteStore(t)

	ledger := NewDeploymentLedger()
	for _, m := range []*DeploymentManifest{
		manifestFor("orders", "staging", "v1.0.0"),
		manifestFor("orders", "staging", "v1.1.0"),
		manifestFor("orders", "prod", "v1.0.0"),
	} {
		if err := ledger.Record(m); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if err := ledger.Persist(store); err != nil {
		t.Fatalf("persist: %v", err)
	}

	// Fresh ledger restored from SQLite.
	restored := NewDeploymentLedger()
	if err := restored.LoadFrom(store); err != nil {
		t.Fatalf("load: %v", err)
	}
	current, ok := restored.Current("orders", "staging")
	if !ok || current.Version != "v1.1.0" {
		t.Fatalf("expected current v1.1.0 for staging, got %+v", current)
	}
	prod, ok := restored.Current("orders", "prod")
	if !ok || prod.Version != "v1.0.0" {
		t.Fatalf("expected current v1.0.0 for prod, got %+v", prod)
	}
	history, ok := restored.History("orders", "staging")
	if !ok || len(history) != 2 {
		t.Fatalf("expected 2 history entries for staging, got %d", len(history))
	}
}

func TestSQLiteDeploymentStoreEmptyLoad(t *testing.T) {
	store := openTestSQLiteStore(t)

	// Loading from a fresh database must yield an empty, usable state.
	restored := NewDeploymentLedger()
	if err := restored.LoadFrom(store); err != nil {
		t.Fatalf("load from empty store: %v", err)
	}
	if _, ok := restored.Current("ghost", "prod"); ok {
		t.Fatal("expected no deployments in empty store")
	}
}

func TestSQLiteDeploymentStoreOverwrite(t *testing.T) {
	store := openTestSQLiteStore(t)

	l1 := NewDeploymentLedger()
	if err := l1.Record(manifestFor("orders", "staging", "v1.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := l1.Persist(store); err != nil {
		t.Fatal(err)
	}

	// Persisting a different ledger replaces the previous state.
	l2 := NewDeploymentLedger()
	if err := l2.Record(manifestFor("billing", "prod", "v2.0.0")); err != nil {
		t.Fatal(err)
	}
	if err := l2.Persist(store); err != nil {
		t.Fatal(err)
	}

	restored := NewDeploymentLedger()
	if err := restored.LoadFrom(store); err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Current("orders", "staging"); ok {
		t.Fatal("expected old state to be replaced")
	}
	if _, ok := restored.Current("billing", "prod"); !ok {
		t.Fatal("expected new state to be present")
	}
}

func TestSQLitePolicyVersionStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policies.db")
	store, err := NewSQLitePolicyVersionStore(path)
	if err != nil {
		t.Fatalf("open policy store: %v", err)
	}
	defer store.Close()

	registry := NewRouteRegistry()
	pv := NewPolicyVersioner(registry, store)
	if err := pv.Create(basePolicy("checkout", "prod"), "alice", "initial"); err != nil {
		t.Fatal(err)
	}
	updated := basePolicy("checkout", "prod")
	updated.SLO = NewSLOPolicy(0.01, 150, 0.02)
	if err := pv.Update(updated, "bob", "tighten SLO"); err != nil {
		t.Fatal(err)
	}

	// Fresh versioner on the same SQLite store sees full history and restores
	// the active policy.
	store2, err := NewSQLitePolicyVersionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store2.Close()
	pv2 := NewPolicyVersioner(NewRouteRegistry(), store2)
	history, err := pv2.History("checkout", "prod")
	if err != nil {
		t.Fatalf("history after restart: %v", err)
	}
	if len(history) != 2 {
		t.Fatalf("expected 2 persisted versions, got %d", len(history))
	}
	if _, ok := pv2.registry.GetPolicy("checkout"); !ok {
		t.Fatal("expected active policy restored after restart")
	}
}

func TestSQLiteStoreSchemaVersion(t *testing.T) {
	store := openTestSQLiteStore(t)
	if got := store.SchemaVersion(); got != expectedSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", expectedSchemaVersion, got)
	}
}

func TestSQLiteStoreConcurrentPersist(t *testing.T) {
	store := openTestSQLiteStore(t)

	done := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			l := NewDeploymentLedger()
			if err := l.Record(manifestFor("svc", "staging", "v1.0.0")); err != nil {
				done <- err
				return
			}
			done <- l.Persist(store)
		}()
	}
	for i := 0; i < 4; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent persist failed: %v", err)
		}
	}
}
