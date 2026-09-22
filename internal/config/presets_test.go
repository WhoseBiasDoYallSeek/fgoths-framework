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

func TestGetPresetKnownNames(t *testing.T) {
	names := []string{"api", "webapp"}
	for _, name := range names {
		preset, ok := GetPreset(name)
		if !ok {
			t.Fatalf("expected preset %q to be found", name)
		}
		if preset.Name != name {
			t.Fatalf("GetPreset(%q).Name = %q, want %q", name, preset.Name, name)
		}
	}
}

func TestGetPresetUnknownName(t *testing.T) {
	if _, ok := GetPreset("does-not-exist"); ok {
		t.Fatal("expected unknown preset name to report not found")
	}
}

func TestListPresetsReturnsAllPresets(t *testing.T) {
	presets := ListPresets()
	if len(presets) != 2 {
		t.Fatalf("expected 2 presets, got %d", len(presets))
	}
	seen := map[string]bool{}
	for _, p := range presets {
		seen[p.Name] = true
	}
	for _, name := range []string{"api", "webapp"} {
		if !seen[name] {
			t.Fatalf("expected ListPresets to include %q", name)
		}
	}
}

func TestApplyPresetFillsUnsetFields(t *testing.T) {
	cfg := &ProjectConfig{}
	if err := ApplyPreset(cfg, "api"); err != nil {
		t.Fatalf("ApplyPreset() error = %v", err)
	}
	if cfg.Type != TypeAPI || cfg.Architecture != ArchFlat || cfg.Database != DBNone {
		t.Fatalf("expected api preset fields to be applied, got %+v", cfg)
	}
	if len(cfg.Features) != 1 || cfg.Features[0] != FeatureHealth {
		t.Fatalf("expected api preset features, got %#v", cfg.Features)
	}
}

func TestApplyPresetPreservesExplicitFields(t *testing.T) {
	cfg := &ProjectConfig{Type: TypeSSR, Architecture: ArchMVC, Database: DBPostgres, Features: []Feature{FeatureOTel}}
	if err := ApplyPreset(cfg, "api"); err != nil {
		t.Fatalf("ApplyPreset() error = %v", err)
	}
	if cfg.Type != TypeSSR || cfg.Architecture != ArchMVC || cfg.Database != DBPostgres {
		t.Fatalf("expected explicit fields to be preserved, got %+v", cfg)
	}
	if len(cfg.Features) != 1 || cfg.Features[0] != FeatureOTel {
		t.Fatalf("expected explicit features to be preserved, got %#v", cfg.Features)
	}
}

func TestApplyPresetUnknownNameIsNotAnError(t *testing.T) {
	cfg := &ProjectConfig{}
	if err := ApplyPreset(cfg, "does-not-exist"); err != nil {
		t.Fatalf("expected unknown preset to be a no-op, got error: %v", err)
	}
	if cfg.Type != "" {
		t.Fatalf("expected config to remain untouched, got %+v", cfg)
	}
}
