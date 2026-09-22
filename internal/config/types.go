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
// Package config defines the project configuration types and enums
package config

import "fmt"

// ProjectConfig holds all configuration for generating a new project
type ProjectConfig struct {
	Name string
	// Dir is the target directory the project directory is created in.
	// Empty means the current working directory (the default behavior).
	Dir          string
	Type         ProjectType
	Architecture ArchPattern
	Database     DatabaseType
	Features     []Feature
}

// ProjectType defines the type of project to generate
type ProjectType string

const (
	TypeAPI ProjectType = "api" // Pure JSON REST API
	TypeSSR ProjectType = "ssr" // Server-Side Rendering web app
)

// ArchPattern defines the project layout. FGOTHS keeps two: a flat layout
// for APIs and MVC for server-rendered webapps. Everything else is the
// developer's choice, not the framework's.
type ArchPattern string

const (
	ArchFlat ArchPattern = "flat" // single-package API (cmd/app + handlers)
	ArchMVC  ArchPattern = "mvc"  // Model-View-Controller webapp
)

// DatabaseType defines the database layer
type DatabaseType string

const (
	DBSQLite   DatabaseType = "sqlite"   // SQLite with WAL
	DBPostgres DatabaseType = "postgres" // PostgreSQL
	DBMySQL    DatabaseType = "mysql"    // MySQL/MariaDB
	DBNone     DatabaseType = "none"     // No database (stateless)
)

// Feature defines optional features
type Feature string

const (
	FeatureFlatBuffers Feature = "flatbuffers" // FlatBuffers schemas
	FeatureHTMX        Feature = "htmx"        // HTMX for SSR
	FeatureOpenAPI     Feature = "openapi"     // OpenAPI/Swagger
	FeatureMetrics     Feature = "metrics"     // Prometheus metrics
	FeatureHealth      Feature = "health"      // Health check endpoints
	FeatureGRPC        Feature = "grpc"        // gRPC support
	FeatureCICD        Feature = "ci-cd"       // GitHub Actions + GitLab CI pipeline templates
	FeatureJWTAuth     Feature = "jwt-auth"    // JWT policy middleware (roles/scopes/claims)
	FeatureMTLS        Feature = "mtls"        // Server-side mTLS and client identity-aware routing
	FeatureOTel        Feature = "otel"        // OpenTelemetry tracing for server and proxy
)

// Valid configuration values
var (
	ValidTypes    = []ProjectType{TypeAPI, TypeSSR}
	ValidArchs    = []ArchPattern{ArchFlat, ArchMVC}
	ValidDBs      = []DatabaseType{DBNone, DBSQLite, DBPostgres, DBMySQL}
	ValidFeatures = []Feature{FeatureHealth, FeatureMetrics, FeatureOpenAPI, FeatureFlatBuffers, FeatureHTMX, FeatureGRPC, FeatureJWTAuth, FeatureMTLS, FeatureOTel, FeatureCICD}
)

// Validate checks that the configuration contains only supported values.
func (c *ProjectConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("project name cannot be empty")
	}
	if !containsType(ValidTypes, c.Type) {
		return fmt.Errorf("invalid project type %q (available: %s)", c.Type, joinTypes(ValidTypes))
	}
	if !containsArch(ValidArchs, c.Architecture) {
		return fmt.Errorf("invalid architecture %q (available: %s)", c.Architecture, joinArchs(ValidArchs))
	}
	if !containsDB(ValidDBs, c.Database) {
		return fmt.Errorf("invalid database %q (available: %s)", c.Database, joinDBs(ValidDBs))
	}
	for _, feature := range c.Features {
		if !containsFeature(ValidFeatures, feature) {
			return fmt.Errorf("unknown feature %q (available: %s)", feature, joinFeatures(ValidFeatures))
		}
	}
	return nil
}

// Normalize applies sensible defaults to any unset configuration field.
func (c *ProjectConfig) Normalize() {
	if c.Type == "" {
		c.Type = TypeAPI
	}
	if c.Architecture == "" {
		if c.Type == TypeSSR {
			c.Architecture = ArchMVC
		} else {
			c.Architecture = ArchFlat
		}
	}
	if c.Database == "" {
		c.Database = DBNone
	}
	if c.Features == nil {
		c.Features = []Feature{}
	}
}

// HasFeature checks if a feature is enabled
func (c *ProjectConfig) HasFeature(feature Feature) bool {
	for _, f := range c.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// UseSQLite reports whether the project uses SQLite.
func (c *ProjectConfig) UseSQLite() bool { return c.Database == DBSQLite }

// UsePostgres reports whether the project uses PostgreSQL.
func (c *ProjectConfig) UsePostgres() bool { return c.Database == DBPostgres }

// UseMySQL reports whether the project uses MySQL.
func (c *ProjectConfig) UseMySQL() bool { return c.Database == DBMySQL }

// UseDatabase reports whether the project uses any database.
func (c *ProjectConfig) UseDatabase() bool { return c.Database != DBNone }

// Placeholder returns the SQL bind placeholder used by the configured database.
func (c *ProjectConfig) Placeholder() string {
	if c.Database == DBPostgres {
		return "$1"
	}
	return "?"
}

func containsType(slice []ProjectType, item ProjectType) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsArch(slice []ArchPattern, item ArchPattern) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsDB(slice []DatabaseType, item DatabaseType) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsFeature(slice []Feature, item Feature) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func joinTypes(slice []ProjectType) string {
	out := ""
	for i, s := range slice {
		if i > 0 {
			out += ", "
		}
		out += string(s)
	}
	return out
}

func joinArchs(slice []ArchPattern) string {
	out := ""
	for i, s := range slice {
		if i > 0 {
			out += ", "
		}
		out += string(s)
	}
	return out
}

func joinDBs(slice []DatabaseType) string {
	out := ""
	for i, s := range slice {
		if i > 0 {
			out += ", "
		}
		out += string(s)
	}
	return out
}

func joinFeatures(slice []Feature) string {
	out := ""
	for i, s := range slice {
		if i > 0 {
			out += ", "
		}
		out += string(s)
	}
	return out
}
