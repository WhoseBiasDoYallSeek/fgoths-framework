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
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
)

// ControlPlaneConfig configures the external control plane API server.
type ControlPlaneConfig struct {
	// Addr is the listen address, e.g. ":9091".
	Addr string
	// StorePath is the file path backing the deployment ledger.
	StorePath string
	// AdminToken is the required bearer token for all /api/v1 endpoints.
	// Empty disables auth (development only); production must set it.
	AdminToken string
	// ApprovalRequirement is the default approval gate size for releases.
	ApprovalRequirement int
}

// ControlPlaneServer exposes the runtime control plane (route registry,
// upstreams, deployment ledger, release workflows) as an external REST API.
// It uses only the standard library and fails closed on authentication.
type ControlPlaneServer struct {
	mu             sync.RWMutex
	registry       *RouteRegistry
	ledger         *DeploymentLedger
	store          DeploymentStore
	workflows      map[string]*ReleaseWorkflow
	workflowGates  map[string]int
	policyVersions *PolicyVersioner
	config         ControlPlaneConfig
}

// NewControlPlaneServer builds a control plane API server backed by the given
// route registry and deployment ledger. When storePath is non-empty the ledger
// persists across restarts.
func NewControlPlaneServer(cfg ControlPlaneConfig) *ControlPlaneServer {
	cp := &ControlPlaneServer{
		registry:      NewRouteRegistry(),
		ledger:        NewDeploymentLedger(),
		workflows:     make(map[string]*ReleaseWorkflow),
		workflowGates: make(map[string]int),
		config:        cfg,
	}
	if cfg.StorePath != "" {
		cp.store = NewFileDeploymentStore(cfg.StorePath)
		// A missing file is fine on first boot; any other load failure is
		// logged because silent persistence loss is hard to diagnose.
		if err := cp.ledger.LoadFrom(cp.store); err != nil {
			log.Printf("control plane: could not restore deployment ledger from %s: %v", cfg.StorePath, err)
		}
		// Policy version history lives next to the deployment ledger.
		policyStorePath := strings.TrimSuffix(cfg.StorePath, ".json") + "-policies.json"
		cp.policyVersions = NewPolicyVersioner(cp.registry, NewFilePolicyVersionStore(policyStorePath))
	} else {
		cp.policyVersions = NewPolicyVersioner(cp.registry, nil)
	}
	return cp
}

// NewControlPlaneServerWithStores builds a control plane with explicit store
// implementations, enabling alternative persistence backends (e.g. SQLite via
// the `sqlite` build tag). Nil stores fall back to in-memory state.
func NewControlPlaneServerWithStores(cfg ControlPlaneConfig, deploymentStore DeploymentStore, policyStore PolicyVersionStore) *ControlPlaneServer {
	cp := &ControlPlaneServer{
		registry:      NewRouteRegistry(),
		ledger:        NewDeploymentLedger(),
		workflows:     make(map[string]*ReleaseWorkflow),
		workflowGates: make(map[string]int),
		config:        cfg,
	}
	if deploymentStore != nil {
		cp.store = deploymentStore
		_ = cp.ledger.LoadFrom(cp.store)
	}
	cp.policyVersions = NewPolicyVersioner(cp.registry, policyStore)
	return cp
}

// PolicyVersioner exposes the versioned policy store for programmatic use.
func (cp *ControlPlaneServer) PolicyVersioner() *PolicyVersioner {
	if cp == nil {
		return nil
	}
	return cp.policyVersions
}

// Reload re-reads persisted deployment state from the store.
func (cp *ControlPlaneServer) Reload() error {
	if cp == nil || cp.store == nil {
		return fmt.Errorf("control plane has no store configured")
	}
	return cp.ledger.LoadFrom(cp.store)
}

// StorePath returns the configured persistence path, if any.
func (cp *ControlPlaneServer) StorePath() string {
	if cp == nil || cp.store == nil {
		return ""
	}
	if f, ok := cp.store.(*FileDeploymentStore); ok {
		return f.path
	}
	return ""
}

// Registry exposes the underlying route registry for programmatic use.
func (cp *ControlPlaneServer) Registry() *RouteRegistry {
	if cp == nil {
		return nil
	}
	return cp.registry
}

// Ledger exposes the underlying deployment ledger for programmatic use.
func (cp *ControlPlaneServer) Ledger() *DeploymentLedger {
	if cp == nil {
		return nil
	}
	return cp.ledger
}

// SetWorkflowGate configures the approval requirement for new release workflows
// targeting the exact service/environment pair.
func (cp *ControlPlaneServer) SetWorkflowGate(service, environment string, required int) {
	if cp == nil {
		return
	}
	service = strings.TrimSpace(service)
	environment = strings.TrimSpace(environment)
	if service == "" || environment == "" {
		return
	}
	if required <= 0 {
		required = 1
	}
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if cp.workflowGates == nil {
		cp.workflowGates = make(map[string]int)
	}
	cp.workflowGates[workflowGateKey(service, environment)] = required
}

// Close releases any resources held by the control plane server
// (file handles, database connections, etc.). It is idempotent and safe to
// call on a nil receiver. Callers are expected to invoke Close during
// graceful shutdown so that SQLite and file-backed stores do not leak
// descriptors across process restarts.
func (cp *ControlPlaneServer) Close() error {
	if cp == nil {
		return nil
	}
	var firstErr error
	if cp.store != nil {
		if err := cp.store.Close(); err != nil {
			firstErr = err
		}
		cp.store = nil
	}
	if cp.policyVersions != nil {
		if err := cp.policyVersions.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// workflowKey identifies a workflow by service/env/version.
func workflowKey(service, environment, version string) string {
	return service + "/" + environment + "/" + version
}

func workflowGateKey(service, environment string) string {
	return strings.TrimSpace(service) + "/" + strings.TrimSpace(environment)
}

// Handler returns the full HTTP handler of the control plane API.
// Routing is implemented explicitly to avoid dependency on ServeMux patterns
// beyond what the Go standard library guarantees.
func (cp *ControlPlaneServer) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public endpoints.
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
	})

	// Authenticated API.
	mux.Handle("/api/v1/", cp.requireAuth(http.HandlerFunc(cp.routeAPI)))

	return mux
}

// requireAuth enforces bearer token auth, failing closed when a token is
// configured and missing/mismatched, and refusing to serve the API when no
// token is configured outside development.
func (cp *ControlPlaneServer) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(cp.config.AdminToken)
		if token == "" {
			// Fail closed: an unauthenticated control plane is a misconfiguration.
			writeJSON(w, http.StatusInternalServerError, map[string]any{
				"error": "control plane has no AdminToken configured; refusing to serve",
			})
			return
		}
		got := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(got, prefix) || !checkToken(strings.TrimPrefix(got, prefix), token) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// checkToken compares tokens in constant time using the standard library.
func checkToken(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// apiRoute is one declarative entry of the control plane routing table.
// Segments are matched literally unless written as "{param}", which captures
// exactly one path segment into params.
type apiRoute struct {
	// segments is the path pattern after /api/v1, e.g. {"releases", "{service}", "{env}", "{version}", "approve"}.
	segments []string
	// methods maps an HTTP method to the handler for that route.
	methods map[string]apiHandler
}

// apiHandler executes a matched route. parts are the captured path values in
// pattern order.
type apiHandler func(cp *ControlPlaneServer, w http.ResponseWriter, r *http.Request, parts []string)

// apiRoutingTable is the single source of truth for control plane API routing.
// Adding an endpoint means adding one entry here plus its handler method.
var apiRoutingTable = []apiRoute{
	{segments: []string{"upstreams"}, methods: map[string]apiHandler{
		http.MethodGet:  (*ControlPlaneServer).listUpstreams,
		http.MethodPost: (*ControlPlaneServer).createUpstream,
	}},
	{segments: []string{"routes"}, methods: map[string]apiHandler{
		http.MethodGet:  (*ControlPlaneServer).listRoutes,
		http.MethodPost: (*ControlPlaneServer).createRoute,
	}},
	{segments: []string{"routes", "{name}", "target"}, methods: map[string]apiHandler{
		http.MethodGet: (*ControlPlaneServer).resolveRoute,
	}},
	{segments: []string{"deployments"}, methods: map[string]apiHandler{
		http.MethodGet:  (*ControlPlaneServer).listDeployments,
		http.MethodPost: (*ControlPlaneServer).createDeployment,
	}},
	{segments: []string{"deployments", "{service}", "{environment}"}, methods: map[string]apiHandler{
		http.MethodGet: (*ControlPlaneServer).getDeployment,
	}},
	{segments: []string{"deployments", "{service}", "{environment}", "history"}, methods: map[string]apiHandler{
		http.MethodGet: (*ControlPlaneServer).getDeploymentHistory,
	}},
	{segments: []string{"releases"}, methods: map[string]apiHandler{
		http.MethodGet:  (*ControlPlaneServer).listReleases,
		http.MethodPost: (*ControlPlaneServer).createRelease,
	}},
	{segments: []string{"releases", "{service}", "{environment}", "{version}"}, methods: map[string]apiHandler{
		http.MethodGet: (*ControlPlaneServer).getRelease,
	}},
	{segments: []string{"releases", "{service}", "{environment}", "{version}", "approve"}, methods: map[string]apiHandler{
		http.MethodPost: (*ControlPlaneServer).approveRelease,
	}},
	{segments: []string{"releases", "{service}", "{environment}", "{version}", "rollback"}, methods: map[string]apiHandler{
		http.MethodPost: (*ControlPlaneServer).rollbackRelease,
	}},
	{segments: []string{"releases", "{service}", "{environment}", "{version}", "reject"}, methods: map[string]apiHandler{
		http.MethodPost: (*ControlPlaneServer).rejectRelease,
	}},
	{segments: []string{"policies"}, methods: map[string]apiHandler{
		http.MethodGet:  (*ControlPlaneServer).listPolicies,
		http.MethodPost: (*ControlPlaneServer).createPolicy,
	}},
	{segments: []string{"policies", "{name}"}, methods: map[string]apiHandler{
		http.MethodGet: (*ControlPlaneServer).getPolicy,
		http.MethodPut: (*ControlPlaneServer).updatePolicy,
	}},
	{segments: []string{"policies", "{name}", "history"}, methods: map[string]apiHandler{
		http.MethodGet: (*ControlPlaneServer).policyHistory,
	}},
	{segments: []string{"policies", "{name}", "rollback"}, methods: map[string]apiHandler{
		http.MethodPost: (*ControlPlaneServer).rollbackPolicy,
	}},
}

// routeAPI dispatches an authenticated API request through the declarative
// routing table. It matches the most specific pattern (longest segment list
// first) so literal segments win over parameters.
func (cp *ControlPlaneServer) routeAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	parts := splitPath(path)

	for _, route := range apiRoutingTable {
		params, ok := matchRoute(route.segments, parts)
		if !ok {
			continue
		}
		handler, allowed := route.methods[r.Method]
		if !allowed {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
			return
		}
		handler(cp, w, r, params)
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
}

// matchRoute matches request segments against a route pattern, returning the
// captured parameter values in pattern order.
func matchRoute(pattern, parts []string) ([]string, bool) {
	if len(pattern) != len(parts) {
		return nil, false
	}
	params := make([]string, 0, len(pattern))
	for i, seg := range pattern {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			params = append(params, parts[i])
			continue
		}
		if seg != parts[i] {
			return nil, false
		}
	}
	return params, true
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{""}
	}
	return strings.Split(trimmed, "/")
}

// ---- handlers ----

func (cp *ControlPlaneServer) listUpstreams(w http.ResponseWriter, _ *http.Request, _ []string) {
	items := cp.registry.ListUpstreams()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (cp *ControlPlaneServer) createUpstream(w http.ResponseWriter, r *http.Request, _ []string) {
	var body UpstreamConfig
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := cp.registry.RegisterUpstream(body); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "created", "name": body.Name})
}

func (cp *ControlPlaneServer) listRoutes(w http.ResponseWriter, _ *http.Request, _ []string) {
	items := cp.registry.List()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (cp *ControlPlaneServer) createRoute(w http.ResponseWriter, r *http.Request, _ []string) {
	var body RouteConfig
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := cp.registry.Register(body); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "created", "name": body.Name})
}

func (cp *ControlPlaneServer) resolveRoute(w http.ResponseWriter, _ *http.Request, parts []string) {
	name := parts[0]
	target, ok := cp.registry.ResolveRouteTarget(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("route %q not found or no healthy upstream", name)})
		return
	}
	// Declarative headers travel with the resolution so proxy callers can
	// apply them to the upstream request.
	resp := map[string]any{"route": name, "target": target}
	if headers := cp.registry.RouteHeaders(name); len(headers) > 0 {
		resp["headers"] = headers
	}
	writeJSON(w, http.StatusOK, resp)
}

// policyRequest carries the policy payload plus operator context.
type policyRequest struct {
	RoutePolicy
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

func (cp *ControlPlaneServer) listPolicies(w http.ResponseWriter, _ *http.Request, _ []string) {
	items := cp.registry.ListPolicies()
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (cp *ControlPlaneServer) createPolicy(w http.ResponseWriter, r *http.Request, _ []string) {
	var body policyRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := cp.policyVersions.Create(body.RoutePolicy, body.Actor, body.Reason); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "created", "name": body.Name, "version": 1})
}

func (cp *ControlPlaneServer) getPolicy(w http.ResponseWriter, _ *http.Request, parts []string) {
	name := parts[0]
	policy, ok := cp.registry.GetPolicy(name)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("policy %q not found", name)})
		return
	}
	writeJSON(w, http.StatusOK, policy)
}

func (cp *ControlPlaneServer) updatePolicy(w http.ResponseWriter, r *http.Request, parts []string) {
	name := parts[0]
	var body policyRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	body.Name = name
	if err := cp.policyVersions.Update(body.RoutePolicy, body.Actor, body.Reason); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		writeConflictOrInvalid(w, err)
		return
	}
	history, _ := cp.policyVersions.History(name, body.Environment)
	version := 0
	if len(history) > 0 {
		version = history[len(history)-1].Version
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "updated", "name": name, "version": version})
}

func (cp *ControlPlaneServer) policyHistory(w http.ResponseWriter, _ *http.Request, parts []string) {
	name := parts[0]
	// History is keyed by name+environment; return all environments for the name.
	pv := cp.policyVersions
	pv.mu.Lock()
	defer pv.mu.Unlock()
	var items []PolicyVersionRecord
	for key, versions := range pv.versions {
		if strings.SplitN(key, "/", 2)[0] == strings.TrimSpace(name) {
			items = append(items, versions...)
		}
	}
	if len(items) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("policy %q has no version history", name)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type policyRollbackRequest struct {
	Version int    `json:"version"`
	Actor   string `json:"actor"`
	Reason  string `json:"reason"`
}

func (cp *ControlPlaneServer) rollbackPolicy(w http.ResponseWriter, r *http.Request, parts []string) {
	name := parts[0]
	var body policyRollbackRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	// Resolve the environment from the latest history entry.
	history, err := cp.policyVersions.History(name, "")
	if err != nil {
		// Try to find any environment with history for this policy.
		pv := cp.policyVersions
		pv.mu.Lock()
		for key, versions := range pv.versions {
			if strings.SplitN(key, "/", 2)[0] == strings.TrimSpace(name) && len(versions) > 0 {
				history = versions
				break
			}
		}
		pv.mu.Unlock()
		if len(history) == 0 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": fmt.Sprintf("policy %q has no version history", name)})
			return
		}
	}
	environment := history[0].Environment
	if err := cp.policyVersions.Rollback(name, environment, body.Version, body.Actor, body.Reason); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "rolled_back", "name": name, "to_version": body.Version})
}

type deploymentRequest struct {
	Service     string   `json:"service"`
	Environment string   `json:"environment"`
	Version     string   `json:"version"`
	Audit       []string `json:"audit"`
}

func (cp *ControlPlaneServer) createDeployment(w http.ResponseWriter, r *http.Request, _ []string) {
	var body deploymentRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	manifest := NewDeploymentManifest(body.Environment, body.Service, body.Version)
	manifest.Audit = body.Audit
	if err := cp.ledger.Record(manifest); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	if err := cp.persist(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to persist deployment state"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"status": "recorded", "service": body.Service, "environment": manifest.Environment, "version": body.Version,
	})
}

func (cp *ControlPlaneServer) listDeployments(w http.ResponseWriter, _ *http.Request, _ []string) {
	state := cp.ledger.Snapshot()
	writeJSON(w, http.StatusOK, map[string]any{"current": state.Current})
}

func (cp *ControlPlaneServer) getDeployment(w http.ResponseWriter, _ *http.Request, parts []string) {
	service, environment := parts[0], parts[1]
	manifest, ok := cp.ledger.Current(service, environment)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "deployment not found"})
		return
	}
	writeJSON(w, http.StatusOK, manifest)
}

func (cp *ControlPlaneServer) getDeploymentHistory(w http.ResponseWriter, _ *http.Request, parts []string) {
	service, environment := parts[0], parts[1]
	history, ok := cp.ledger.History(service, environment)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no history for deployment"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": history})
}

type releaseRequest struct {
	Service     string `json:"service"`
	Environment string `json:"environment"`
	Version     string `json:"version"`
}

func (cp *ControlPlaneServer) createRelease(w http.ResponseWriter, r *http.Request, _ []string) {
	var body releaseRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	key := workflowKey(body.Service, body.Environment, body.Version)
	cp.mu.Lock()
	defer cp.mu.Unlock()
	if _, exists := cp.workflows[key]; exists {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "release workflow already exists"})
		return
	}
	wf := NewReleaseWorkflow(body.Service, body.Environment, body.Version)
	required := cp.config.ApprovalRequirement
	if scoped, ok := cp.workflowGates[workflowGateKey(body.Service, body.Environment)]; ok {
		required = scoped
	}
	if required <= 0 {
		required = 1
	}
	wf.Gate = NewApprovalGate(key, required)
	cp.workflows[key] = wf
	writeJSON(w, http.StatusCreated, map[string]any{"status": "pending", "workflow": key})
}

func (cp *ControlPlaneServer) getRelease(w http.ResponseWriter, _ *http.Request, parts []string) {
	service, environment, version := parts[0], parts[1], parts[2]
	wf, ok := cp.workflow(service, environment, version)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "release workflow not found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":     wf.Service,
		"environment": wf.Environment,
		"version":     wf.Version,
		"state":       wf.State,
		"approved":    wf.IsApproved(),
	})
}

func (cp *ControlPlaneServer) listReleases(w http.ResponseWriter, _ *http.Request, _ []string) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	items := make([]map[string]any, 0, len(cp.workflows))
	for key, wf := range cp.workflows {
		items = append(items, map[string]any{
			"workflow": key, "service": wf.Service, "environment": wf.Environment,
			"version": wf.Version, "state": wf.State,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

type actorRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

func (cp *ControlPlaneServer) approveRelease(w http.ResponseWriter, req *http.Request, parts []string) {
	service, environment, version := parts[0], parts[1], parts[2]
	var body actorRequest
	if !decodeJSON(w, req, &body) {
		return
	}
	wf, ok := cp.workflow(service, environment, version)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "release workflow not found"})
		return
	}
	if err := wf.Approve(body.Actor); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": wf.State, "approved": wf.IsApproved()})
}

func (cp *ControlPlaneServer) rollbackRelease(w http.ResponseWriter, req *http.Request, parts []string) {
	service, environment, version := parts[0], parts[1], parts[2]
	var body actorRequest
	if !decodeJSON(w, req, &body) {
		return
	}
	wf, ok := cp.workflow(service, environment, version)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "release workflow not found"})
		return
	}
	if err := wf.Rollback(body.Actor, body.Reason); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": wf.State})
}

func (cp *ControlPlaneServer) rejectRelease(w http.ResponseWriter, req *http.Request, parts []string) {
	service, environment, version := parts[0], parts[1], parts[2]
	var body actorRequest
	if !decodeJSON(w, req, &body) {
		return
	}
	wf, ok := cp.workflow(service, environment, version)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "release workflow not found"})
		return
	}
	if err := wf.Reject(body.Actor, body.Reason); err != nil {
		writeConflictOrInvalid(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": wf.State})
}

func (cp *ControlPlaneServer) workflow(service, environment, version string) (*ReleaseWorkflow, bool) {
	cp.mu.RLock()
	defer cp.mu.RUnlock()
	wf, ok := cp.workflows[workflowKey(service, environment, version)]
	return wf, ok
}

func (cp *ControlPlaneServer) persist() error {
	if cp.store == nil {
		return nil
	}
	if err := cp.ledger.Persist(cp.store); err != nil {
		return fmt.Errorf("persist deployment ledger: %w", err)
	}
	return nil
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "malformed json body"})
		return false
	}
	return true
}

func writeConflictOrInvalid(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "already exists"):
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg})
	default:
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": msg})
	}
}
