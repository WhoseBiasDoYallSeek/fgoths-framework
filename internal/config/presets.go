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

// Preset defines a complete project configuration
type Preset struct {
	Name         string
	Description  string
	Type         ProjectType
	Architecture ArchPattern
	Database     DatabaseType
	Features     []Feature
}

// Two presets only — the deliberate API surface of the CLI. Everything else
// is opt-in per project via --features/--db.
var PresetAPI = Preset{
	Name:         "api",
	Description:  "Flat JSON API + health + proxy (opt-ins: metrics, openapi, grpc, jwt-auth, mtls, otel, flatbuffers, sqlite)",
	Type:         TypeAPI,
	Architecture: ArchFlat,
	Database:     DBNone,
	Features:     []Feature{FeatureHealth},
}

var PresetWebapp = Preset{
	Name:         "webapp",
	Description:  "MVC SSR + HTMX + SQLite + proxy (opt-ins: metrics, openapi, jwt-auth, mtls, otel, flatbuffers)",
	Type:         TypeSSR,
	Architecture: ArchMVC,
	Database:     DBSQLite,
	Features:     []Feature{FeatureHTMX, FeatureHealth},
}

// GetPreset returns a preset by name.
func GetPreset(name string) (*Preset, bool) {
	if name == PresetAPI.Name {
		p := PresetAPI
		return &p, true
	}
	if name == PresetWebapp.Name {
		p := PresetWebapp
		return &p, true
	}
	return nil, false
}

// ListPresets returns all available presets.
func ListPresets() []Preset {
	return []Preset{PresetAPI, PresetWebapp}
}

// ApplyPreset applies a preset to a config
func ApplyPreset(config *ProjectConfig, presetName string) error {
	preset, ok := GetPreset(presetName)
	if !ok {
		return nil // Not an error, just no preset
	}

	// Only override if not already set
	if config.Type == "" {
		config.Type = preset.Type
	}
	if config.Architecture == "" {
		config.Architecture = preset.Architecture
	}
	if config.Database == "" {
		config.Database = preset.Database
	}
	if len(config.Features) == 0 {
		config.Features = preset.Features
	}

	return nil
}
