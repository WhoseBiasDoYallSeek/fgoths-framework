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
package config

import "testing"

func TestProjectConfigValidateRequiresName(t *testing.T) {
	cfg := ProjectConfig{Type: TypeAPI, Architecture: ArchFlat, Database: DBNone}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when name is empty")
	}
}

func TestProjectConfigValidateRejectsUnknownType(t *testing.T) {
	cfg := ProjectConfig{Name: "app", Type: "bogus", Architecture: ArchFlat, Database: DBNone}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown project type")
	}
}

func TestProjectConfigValidateRejectsUnknownArch(t *testing.T) {
	cfg := ProjectConfig{Name: "app", Type: TypeAPI, Architecture: "bogus", Database: DBNone}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown architecture")
	}
}

func TestProjectConfigValidateRejectsUnknownDB(t *testing.T) {
	cfg := ProjectConfig{Name: "app", Type: TypeAPI, Architecture: ArchFlat, Database: "bogus"}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown database")
	}
}

func TestProjectConfigValidateRejectsUnknownFeature(t *testing.T) {
	cfg := ProjectConfig{Name: "app", Type: TypeAPI, Architecture: ArchFlat, Database: DBNone, Features: []Feature{"bogus"}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for unknown feature")
	}
}

func TestProjectConfigValidateAcceptsFullyValidConfig(t *testing.T) {
	cfg := ProjectConfig{
		Name:         "app",
		Type:         TypeSSR,
		Architecture: ArchFlat,
		Database:     DBMySQL,
		Features:     []Feature{FeatureHealth, FeatureMetrics, FeatureOpenAPI, FeatureFlatBuffers, FeatureHTMX, FeatureGRPC, FeatureJWTAuth, FeatureMTLS, FeatureOTel},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected valid config, got error: %v", err)
	}
}

func TestProjectConfigNormalizeAppliesDefaults(t *testing.T) {
	cfg := ProjectConfig{Name: "app"}
	cfg.Normalize()
	if cfg.Type != TypeAPI {
		t.Fatalf("expected default type api, got %q", cfg.Type)
	}
	if cfg.Architecture != ArchFlat {
		t.Fatalf("expected default architecture clean, got %q", cfg.Architecture)
	}
	if cfg.Database != DBNone {
		t.Fatalf("expected default database none, got %q", cfg.Database)
	}
	if cfg.Features == nil {
		t.Fatal("expected features to be initialized to an empty slice")
	}
}

func TestProjectConfigNormalizeSSRDefaultsToMVC(t *testing.T) {
	cfg := ProjectConfig{Name: "app", Type: TypeSSR}
	cfg.Normalize()
	if cfg.Architecture != ArchMVC {
		t.Fatalf("expected SSR to default to MVC architecture, got %q", cfg.Architecture)
	}
}

func TestProjectConfigNormalizePreservesExplicitValues(t *testing.T) {
	cfg := ProjectConfig{Name: "app", Type: TypeSSR, Architecture: ArchMVC, Database: DBPostgres, Features: []Feature{FeatureHealth}}
	cfg.Normalize()
	if cfg.Type != TypeSSR || cfg.Architecture != ArchMVC || cfg.Database != DBPostgres {
		t.Fatalf("expected Normalize to preserve explicit values, got %+v", cfg)
	}
	if len(cfg.Features) != 1 {
		t.Fatalf("expected explicit features to be preserved, got %#v", cfg.Features)
	}
}

func TestProjectConfigHasFeature(t *testing.T) {
	cfg := ProjectConfig{Features: []Feature{FeatureHealth, FeatureMetrics}}
	if !cfg.HasFeature(FeatureHealth) {
		t.Fatal("expected HasFeature to find an enabled feature")
	}
	if cfg.HasFeature(FeatureOpenAPI) {
		t.Fatal("expected HasFeature to reject a disabled feature")
	}
}

func TestProjectConfigDatabaseHelpers(t *testing.T) {
	cases := []struct {
		db                                                   DatabaseType
		wantSQLite, wantPostgres, wantMySQL, wantUseDatabase bool
		wantPlaceholder                                      string
	}{
		{db: DBNone, wantPlaceholder: "?"},
		{db: DBSQLite, wantSQLite: true, wantUseDatabase: true, wantPlaceholder: "?"},
		{db: DBPostgres, wantPostgres: true, wantUseDatabase: true, wantPlaceholder: "$1"},
		{db: DBMySQL, wantMySQL: true, wantUseDatabase: true, wantPlaceholder: "?"},
	}
	for _, tc := range cases {
		cfg := ProjectConfig{Database: tc.db}
		if got := cfg.UseSQLite(); got != tc.wantSQLite {
			t.Errorf("UseSQLite() for %v = %v, want %v", tc.db, got, tc.wantSQLite)
		}
		if got := cfg.UsePostgres(); got != tc.wantPostgres {
			t.Errorf("UsePostgres() for %v = %v, want %v", tc.db, got, tc.wantPostgres)
		}
		if got := cfg.UseMySQL(); got != tc.wantMySQL {
			t.Errorf("UseMySQL() for %v = %v, want %v", tc.db, got, tc.wantMySQL)
		}
		if got := cfg.UseDatabase(); got != tc.wantUseDatabase {
			t.Errorf("UseDatabase() for %v = %v, want %v", tc.db, got, tc.wantUseDatabase)
		}
		if got := cfg.Placeholder(); got != tc.wantPlaceholder {
			t.Errorf("Placeholder() for %v = %q, want %q", tc.db, got, tc.wantPlaceholder)
		}
	}
}

func TestJoinHelpersProduceCommaSeparatedLists(t *testing.T) {
	if got := joinTypes(ValidTypes); got != "api, ssr" {
		t.Fatalf("joinTypes() = %q", got)
	}
	if got := joinArchs(ValidArchs); got != "flat, mvc" {
		t.Fatalf("joinArchs() = %q", got)
	}
	if got := joinDBs(ValidDBs); got != "none, sqlite, postgres, mysql" {
		t.Fatalf("joinDBs() = %q", got)
	}
	if got := joinFeatures(ValidFeatures); got == "" || got[:6] != "health" {
		t.Fatalf("joinFeatures() = %q, want to start with health", got)
	}
}

func TestJoinHelpersHandleEmptySlices(t *testing.T) {
	if got := joinTypes(nil); got != "" {
		t.Fatalf("joinTypes(nil) = %q, want empty string", got)
	}
	if got := joinArchs(nil); got != "" {
		t.Fatalf("joinArchs(nil) = %q, want empty string", got)
	}
	if got := joinDBs(nil); got != "" {
		t.Fatalf("joinDBs(nil) = %q, want empty string", got)
	}
	if got := joinFeatures(nil); got != "" {
		t.Fatalf("joinFeatures(nil) = %q, want empty string", got)
	}
}

func TestContainsHelpers(t *testing.T) {
	if !containsType(ValidTypes, TypeAPI) {
		t.Fatal("expected containsType to find TypeAPI")
	}
	if containsType(ValidTypes, "bogus") {
		t.Fatal("expected containsType to reject unknown type")
	}
	if !containsArch(ValidArchs, ArchFlat) {
		t.Fatal("expected containsArch to find ArchFlat")
	}
	if containsArch(ValidArchs, "bogus") {
		t.Fatal("expected containsArch to reject unknown arch")
	}
	if !containsDB(ValidDBs, DBSQLite) {
		t.Fatal("expected containsDB to find DBSQLite")
	}
	if containsDB(ValidDBs, "bogus") {
		t.Fatal("expected containsDB to reject unknown db")
	}
	if !containsFeature(ValidFeatures, FeatureHealth) {
		t.Fatal("expected containsFeature to find FeatureHealth")
	}
	if containsFeature(ValidFeatures, "bogus") {
		t.Fatal("expected containsFeature to reject unknown feature")
	}
}
