# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
follows [Semantic Versioning](https://semver.org).

## [Unreleased]

## [1.0.0] - 2026-09-22

First versioned release. The generator, both project layouts (`flat`, `mvc`),
all optional features, and the embedded runtime are validated end-to-end
with reproducible benchmarks.

### Generator
- Two presets: `api` (flat JSON API) and `webapp` (MVC SSR + Templ + HTMX),
  each opt-in to a database (`sqlite`, `postgres`, `mysql`) and a feature set
- Opt-in features: `health`, `metrics`, `openapi`, `grpc`, `flatbuffers`,
  `htmx`, `jwt-auth`, `mtls`, `otel`, `ci-cd`
- Atomic generation: files are written to a staging directory and only
  renamed into place after every template renders successfully
- Template drift guard (`TestRuntimeTemplatesInSync`, `make check-templates`):
  the runtime templates shipped to generated projects are verified
  byte-identical to the framework's own tested `pkg/runtime` sources

### Runtime (`pkg/runtime`)
- Native HTTP router with path parameters, middleware chain, mount/prefix
  stripping, and path rewrite
- Reverse proxy with connection pooling, retry, circuit breaker, rate
  limiting, and health-checked failover
- TLS and mutual TLS with client identity routing (CN/SAN/SPIFFE)
- JWT policy middleware (roles, scopes, claims) that fails closed without a
  configured secret
- Request correlation IDs, structured logging, OpenTelemetry tracing
- Readiness/liveness probes and error-rate alerting

### Governance & control plane
- Declarative route/upstream registry with per-environment policies
- Deployment ledger and release workflows with multi-approver gates,
  terminal rollback/reject states
- Policy versioning with actor/reason audit trail and rollback-as-new-version
- Progressive delivery: canary weighting, staged rollout, error-threshold
  rollback
- REST API (`/api/v1/...`) with bearer-token auth that fails closed; file or
  SQLite (build tag `sqlite`) persistence

### Known limitations
- **Control plane persistence is single-process.** `FileDeploymentStore` and
  the SQLite backend are both single-machine stores: there is no distributed
  lock, so running more than one control plane process against the same
  store is unsafe (last write wins, silent data loss). Do not run it as a
  horizontally-scaled or multi-replica service without adding your own
  coordination in front of it.
- No remote/clustered backend (etcd, Postgres with row locking, etc.) is
  built in yet. Planned direction: a Postgres-backed `DeploymentStore` using
  transactions/row locking for safe multi-process writes — reuses a database
  the framework already supports instead of adding a new external dependency
  (etcd/Consul). Not started; only relevant if the control plane is run as a
  shared, multi-replica service rather than per-project via `fgoths init`.
- Multi-tenancy quotas exist but are not validated under real multi-tenant
  production load.

[Unreleased]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/releases/tag/v1.0.0
