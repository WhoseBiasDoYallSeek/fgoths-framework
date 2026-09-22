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

// Package runtime provides an optional SQLite-backed persistence backend for
// the control plane. It is guarded by the `sqlite` build tag so the default
// build keeps zero extra dependencies; enable it with:
//
//	go build -tags sqlite ./...
//
// The backend uses modernc.org/sqlite (pure Go, no CGO) and serves both the
// deployment ledger (DeploymentStore) and the policy version history
// (PolicyVersionStore) from a single database file with WAL journaling.
package runtime

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// expectedSchemaVersion is bumped whenever the persistence schema changes.
// Migrations run sequentially on open.
const expectedSchemaVersion = 1

// SQLiteDeploymentStore persists the deployment ledger in SQLite.
// It satisfies the DeploymentStore interface.
type SQLiteDeploymentStore struct {
	db *sql.DB
	mu sync.Mutex // serializes writes; SQLite handles the rest
}

// NewSQLiteDeploymentStore opens (or creates) a SQLite database at path and
// applies pending schema migrations.
func NewSQLiteDeploymentStore(path string) (*SQLiteDeploymentStore, error) {
	db, err := openControlPlaneDB(path)
	if err != nil {
		return nil, err
	}
	return &SQLiteDeploymentStore{db: db}, nil
}

// SchemaVersion reports the applied schema version of the backing database.
func (s *SQLiteDeploymentStore) SchemaVersion() int {
	if s == nil || s.db == nil {
		return 0
	}
	var v int
	if err := s.db.QueryRow("SELECT version FROM schema_version LIMIT 1").Scan(&v); err != nil {
		return 0
	}
	return v
}

// Save replaces the persisted deployment ledger state inside a transaction.
func (s *SQLiteDeploymentStore) Save(state DeploymentLedgerState) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite deployment store is not open")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("DELETE FROM deployment_current"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear current: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM deployment_history"); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("clear history: %w", err)
	}

	for service, envs := range state.Current {
		for env, manifest := range envs {
			raw, err := json.Marshal(manifest)
			if err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("marshal manifest: %w", err)
			}
			if _, err := tx.Exec(
				"INSERT INTO deployment_current (service, environment, manifest) VALUES (?, ?, ?)",
				service, env, raw,
			); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("insert current: %w", err)
			}
		}
	}

	for service, envs := range state.History {
		for env, manifests := range envs {
			for i, manifest := range manifests {
				raw, err := json.Marshal(manifest)
				if err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("marshal history manifest: %w", err)
				}
				if _, err := tx.Exec(
					"INSERT INTO deployment_history (service, environment, seq, manifest) VALUES (?, ?, ?, ?)",
					service, env, i, raw,
				); err != nil {
					_ = tx.Rollback()
					return fmt.Errorf("insert history: %w", err)
				}
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Load reads the full deployment ledger state from the database.
func (s *SQLiteDeploymentStore) Load() (DeploymentLedgerState, error) {
	if s == nil || s.db == nil {
		return DeploymentLedgerState{}, fmt.Errorf("sqlite deployment store is not open")
	}
	state := DeploymentLedgerState{
		Current: make(map[string]map[string]*DeploymentManifest),
		History: make(map[string]map[string][]*DeploymentManifest),
	}

	rows, err := s.db.Query("SELECT service, environment, manifest FROM deployment_current")
	if err != nil {
		return state, fmt.Errorf("query current: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var service, env string
		var raw []byte
		if err := rows.Scan(&service, &env, &raw); err != nil {
			return state, fmt.Errorf("scan current: %w", err)
		}
		var manifest DeploymentManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return state, fmt.Errorf("unmarshal current manifest: %w", err)
		}
		if state.Current[service] == nil {
			state.Current[service] = make(map[string]*DeploymentManifest)
		}
		state.Current[service][env] = &manifest
	}
	if err := rows.Err(); err != nil {
		return state, err
	}

	hrows, err := s.db.Query("SELECT service, environment, manifest FROM deployment_history ORDER BY service, environment, seq")
	if err != nil {
		return state, fmt.Errorf("query history: %w", err)
	}
	defer hrows.Close()
	for hrows.Next() {
		var service, env string
		var raw []byte
		if err := hrows.Scan(&service, &env, &raw); err != nil {
			return state, fmt.Errorf("scan history: %w", err)
		}
		var manifest DeploymentManifest
		if err := json.Unmarshal(raw, &manifest); err != nil {
			return state, fmt.Errorf("unmarshal history manifest: %w", err)
		}
		if state.History[service] == nil {
			state.History[service] = make(map[string][]*DeploymentManifest)
		}
		state.History[service][env] = append(state.History[service][env], &manifest)
	}
	if err := hrows.Err(); err != nil {
		return state, err
	}
	return state, nil
}

// Close closes the underlying database connection.
func (s *SQLiteDeploymentStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// SQLitePolicyVersionStore persists policy version history in the same SQLite
// backend, satisfying PolicyVersionStore.
type SQLitePolicyVersionStore struct {
	db *sql.DB
	mu sync.Mutex
}

// NewSQLitePolicyVersionStore opens (or creates) a policy version database.
func NewSQLitePolicyVersionStore(path string) (*SQLitePolicyVersionStore, error) {
	db, err := openControlPlaneDB(path)
	if err != nil {
		return nil, err
	}
	return &SQLitePolicyVersionStore{db: db}, nil
}

// Save replaces the persisted policy version state inside a transaction.
func (s *SQLitePolicyVersionStore) Save(state PolicyVersionState) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("sqlite policy store is not open")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec("DELETE FROM policy_versions"); err != nil {
		return fmt.Errorf("clear policy versions: %w", err)
	}
	for key, versions := range state.Versions {
		parts := splitPolicyKey(key)
		for _, record := range versions {
			raw, err := json.Marshal(record)
			if err != nil {
				return fmt.Errorf("marshal policy record: %w", err)
			}
			if _, err := tx.Exec(
				"INSERT INTO policy_versions (policy_name, environment, version, record) VALUES (?, ?, ?, ?)",
				parts.name, parts.environment, record.Version, raw,
			); err != nil {
				return fmt.Errorf("insert policy record: %w", err)
			}
		}
	}
	return tx.Commit()
}

// Load reads the full policy version state from the database.
func (s *SQLitePolicyVersionStore) Load() (PolicyVersionState, error) {
	if s == nil || s.db == nil {
		return PolicyVersionState{}, fmt.Errorf("sqlite policy store is not open")
	}
	state := PolicyVersionState{Versions: make(map[string][]PolicyVersionRecord)}
	rows, err := s.db.Query("SELECT policy_name, environment, record FROM policy_versions ORDER BY policy_name, environment, version")
	if err != nil {
		return state, fmt.Errorf("query policy versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, env string
		var raw []byte
		if err := rows.Scan(&name, &env, &raw); err != nil {
			return state, fmt.Errorf("scan policy record: %w", err)
		}
		var record PolicyVersionRecord
		if err := json.Unmarshal(raw, &record); err != nil {
			return state, fmt.Errorf("unmarshal policy record: %w", err)
		}
		key := name + "/" + env
		state.Versions[key] = append(state.Versions[key], record)
	}
	return state, rows.Err()
}

// Close closes the underlying database connection.
func (s *SQLitePolicyVersionStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// openControlPlaneDB opens a SQLite database with production-friendly pragmas
// and ensures the schema is at the expected version.
func openControlPlaneDB(path string) (*sql.DB, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite database path is required")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// WAL allows concurrent readers with a single writer; busy_timeout avoids
	// spurious SQLITE_BUSY under concurrent control plane writes.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %s: %w", pragma, err)
		}
	}
	if err := migrateControlPlaneSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// controlPlaneSchema holds the DDL for each schema version, in order.
var controlPlaneSchema = []string{
	// v1
	`
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL
	);
	CREATE TABLE IF NOT EXISTS deployment_current (
		service     TEXT NOT NULL,
		environment TEXT NOT NULL,
		manifest    BLOB NOT NULL,
		PRIMARY KEY (service, environment)
	);
	CREATE TABLE IF NOT EXISTS deployment_history (
		service     TEXT NOT NULL,
		environment TEXT NOT NULL,
		seq         INTEGER NOT NULL,
		manifest    BLOB NOT NULL,
		PRIMARY KEY (service, environment, seq)
	);
	CREATE TABLE IF NOT EXISTS policy_versions (
		policy_name TEXT NOT NULL,
		environment TEXT NOT NULL,
		version     INTEGER NOT NULL,
		record      BLOB NOT NULL,
		PRIMARY KEY (policy_name, environment, version)
	);
	`,
}

// migrateControlPlaneSchema applies pending migrations inside a transaction.
func migrateControlPlaneSchema(db *sql.DB) error {
	current := 0
	if err := db.QueryRow("SELECT version FROM schema_version ORDER BY version DESC LIMIT 1").Scan(&current); err != nil {
		// Table missing or unreadable: start from v0 (fresh DB).
		current = 0
	}
	// A newer schema may contain structures this binary cannot safely interpret.
	// Never downgrade it or rewrite the version marker.
	if current > len(controlPlaneSchema) {
		return fmt.Errorf("unsupported sqlite schema version %d (latest supported: %d)", current, len(controlPlaneSchema))
	}

	for v := current; v < len(controlPlaneSchema); v++ {
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", v+1, err)
		}
		if _, err := tx.Exec(controlPlaneSchema[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema version %d: %w", v+1, err)
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO schema_version (version) VALUES (?)", v+1); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record schema version %d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", v+1, err)
		}
	}
	return nil
}

type sqlitePolicyKey struct{ name, environment string }

func splitPolicyKey(key string) sqlitePolicyKey {
	for i := 0; i < len(key); i++ {
		if key[i] == '/' {
			return sqlitePolicyKey{name: key[:i], environment: key[i+1:]}
		}
	}
	return sqlitePolicyKey{name: key}
}
