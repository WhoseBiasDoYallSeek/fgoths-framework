# Changelog

All notable changes to this project are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
follows [Semantic Versioning](https://semver.org).

## [Unreleased]

## [1.4.1] - 2026-10-01

### Added
- `fgoths upgrade` accepts projects created with v1.4.0. The runtime itself
  is unchanged in this release.

### Changed
- The `ci-cd` release workflow builds Linux (amd64, arm64) by default from a
  single `PLATFORMS` list (Windows targets get `.exe`) and publishes
  `SHA256SUMS` next to the binaries and SBOM.
- The generated GitLab pipeline uses one Go image (`GO_VERSION`, matching
  `go.mod`) for every job.
- Generated project and contributor docs point to official install guides
  instead of OS-specific commands.

### Fixed
- `ci-cd` pipelines built `./cmd/app`, which generated projects do not have,
  and skipped code generation; they now run `assetmanifest` (and `templ` for
  webapp) and build the root package.

## [1.4.0] - 2026-10-01

### Added
- `fgoths generate crud` now produces a complete resource: list, create,
  get, update, and delete by id, with a separate input type, a 1 MB body
  limit, unknown-field rejection, 400/404 responses, and generic 500 errors.
- `fgoths build --sbom` writes a CycloneDX 1.5 SBOM read from the binary's
  embedded build info: only the modules actually linked, with versions,
  checksums, replacements, and the Go toolchain. Output is reproducible
  under `SOURCE_DATE_EPOCH`.
- `fgoths upgrade` accepts projects created with v1.3.0 and now manages
  `pkg/runtime/router.go`, so existing projects receive the new dispatch.
- `make benchmark-surface` (`benchmarks/run-dep-surface.sh`) generates
  projects and measures the modules linked into their binaries next to chi,
  gin, and go-zero. The full benchmark and comparison scripts reuse it.

### Changed
- Documentation reorganized for humans: the README is now a short product
  overview that leads with real code and a real upgrade transcript, with new task-focused guides in `docs/` (getting started, CLI
  reference, upgrading, performance). Deep technical material stays in
  `ARCHITECTURE.md`, `COMPARISON.md`, and the ADRs.
- `fgoths init --help` now lists every supported `--db` value
  (`none|sqlite|postgres|mysql`).
- Product positioning rewritten around what sets FGOTHS apart: an owned yet
  upgradable runtime, a minimal supply chain, sidecar-free resilience,
  built-in governance, and hot reload without Node.
- Dependency-surface figures now measure what ships: a default generated
  API project links 1 external module (vs gin 18, go-zero 44). The previous
  "5 packages" figure measured the whole framework runtime package instead.
- Parameterized route dispatch no longer allocates in the router: the
  matched pattern is recorded in `Request.Pattern` and `PathValue` is
  resolved lazily, falling back to the standard library. In-process median:
  401 ns / 1024 B / 10 allocs (was 471 ns / 1440 B / 13 allocs), on par with
  the stdlib mux and ahead of chi and go-zero. Static routes stay the fastest
  of the group at 338 ns.

### Fixed
- Generated CRUD handlers no longer accept client-supplied `id` or
  `created_at` values. `fgoths upgrade` does not rewrite CRUD code you already
  generated; regenerate a resource or port the input-type pattern by hand.
- The SBOM previously listed `go.mod` requirements without versions; it now
  lists linked modules with exact versions.
- `fgoths version` reported `0.0.0-dev` when installed with
  `go install …@vX.Y.Z`; it now reports the module version.
- Removed a race in the `fgoths dev` signal-forwarding test that could
  intermittently abort the `internal/cli` test binary under load.

## [1.3.0] - 2026-10-01

### Added
- `fgoths upgrade` plans and applies versioned embedded-runtime updates in
  existing generated projects, records project version/configuration, backs
  up changed files, and three-way merges non-overlapping local edits while
  preserving originals on conflicts. It keeps upgrade artifacts out of Git
  without hiding the project metadata. Supported sources are v1.1.0 and
  v1.2.0; generated projects now record `.fgoths/upgrade.json` so future
  upgrades no longer need `--from`.

## [1.2.0] - 2026-10-01

### Fixed
- Generated applications now register routes through the FGOTHS runtime router
  instead of replacing `http.Server.Handler` with a separate `ServeMux`.
- Generated projects now create deterministic static asset manifests. MVC
  projects use fingerprints for immutable production caching, disable browser
  caching in development, and broadcast reloads after CSS, JS, or HTML changes.
- `runtime.Server` now provides `Put`, `Patch`, and `Delete` delegates, and
  generated CRUD route registries accept the runtime registrar interface.

### Added
- Runtime contract coverage for middleware registration order; generator tests
  now assert the actual flat and MVC entrypoint locations.
- Repository-wide short-mode statement coverage now reaches 100%, enforced by
  `make cover`, with focused tests for uncovered CLI, generator, and runtime
  paths.

### Documentation
- Clarified the generated CRUD endpoint scope, runtime/template synchronization
  boundary, representative generated-project test matrix, and local coverage
  commands. Corrected references to CI workflows that are no longer committed.
- Positioned FGOTHS as an organization-specific internal service platform and
  clarified that the framework comparisons are engineering references, not
  claims of universal superiority or security guarantees.

## [1.1.0] - 2026-09-27

Second release: runtime performance pass, server lifecycle hardening, and
living examples generated by the framework itself.

### Performance
- **`Metrics.Record` contention eliminated** (`pkg/runtime/metrics.go`).
  Scalar counters (`totalRequests`, `totalErrors`, `totalLatency`) moved from
  the global mutex to `atomic.Int64`, shrinking the mutex's critical section
  to the maps and the error ring. Under `BenchmarkMetricsRecordParallel`
  (4 routes, contended): **~27 µs/op → ~210 ns/op, 6 → 0 allocs/op**.
  Serial throughput unchanged (~46 ns/op).
- **Router dispatch: fewer context allocations** (`pkg/runtime/router.go`).
  `ServeHTTP` previously issued two `req.WithContext(context.WithValue(...))`
  per request (one for path params, one for the route pattern) — 4 allocations.
  Both values now travel in a single `routeInfo` context value (2 allocations),
  and static exact-match routes skip the context injection entirely (0
  allocations). Measured with `benchmarks/comparison` (in-process dispatch,
  3 runs): param routes **16 → 13 allocs/op** (~520 → ~495 ns/op), static
  routes **13 → 10 allocs/op** (~390 → ~355 ns/op) — static dispatch now
  matches or beats stdlib `http.ServeMux` and gin on both time and
  allocations.
- **`RouteHistogram.Record` is now O(1) amortized** (`pkg/runtime/metrics.go`).
  Percentile computation (full copy + `sort.Slice`, O(n log n) per request)
  moved from `Record` to `Snapshot` behind a `percentilesDirty` flag —
  servers with `WithMetrics` no longer pay a sort per request; percentiles
  are recomputed only when observed. Snapshot values are unchanged.
- **`statusRecorder` recycled via `sync.Pool`** (`pkg/runtime/metrics.go`).
  The per-request response wrapper used by `WithMetrics`, `WithLogger`,
  and `WithAlertThreshold` is now pooled instead of allocated
  per request; wrappers are released after the request completes.

### Changed
- **Runtime split into focused files.** `pkg/runtime/server.go` (1.673 lines)
  was decomposed: the router moved to `router.go`, the reverse proxy to
  `proxy.go`, and request-ID middleware to `requestid.go`. The embedded
  templates were split accordingly (`router.go.tpl`, `proxy.go.tpl`,
  `requestid.go.tpl`) and `sync-templates` now tracks all six base runtime
  files, keeping the verbatim-copy guarantee (ADR 0002) per file.
- **`Server.WithListener` added** (`pkg/runtime`): injects a pre-created
  `net.Listener` consumed by the next `ListenAndServe` — enables socket
  activation, systemd handoff, and sandbox-safe tests without binding a port.
- **Control plane shutdown refactored** (`internal/cli`): signal handling
  extracted into `serveControlPlaneUntilSignal` with a stubbable
  `signalStopChan` indirection, mirroring the existing `osExit`/`osReadFile`
  test-indirection pattern (os helpers are now package-level vars).
- `RunControlPlane` now returns after `osExit(1)` paths (defensive; keeps
  coverage instrumentation accurate).

### Added
- **Examples regenerated with the framework itself**: `examples/api-demo`
  (flat API preset with metrics + OpenAPI) and `examples/webapp-demo` (MVC
  preset with Templ + HTMX + SQLite) replace the hand-written
  `examples/proxy-demo`, serving as living proof of both presets (ADR 0003).
- **CI workflows**: nightly benchmark runs and tag-triggered release builds
  with version metadata via `make print-ldflags` were added in this release
  and later removed from the repository (the maintainer runs CI externally);
  the `ci-cd` feature templates for generated projects are unaffected.
- **ADRs**: 0002 (selected runtime sources embedded verbatim with drift checks) and
  0003 (exactly two architecture presets).
- **Tests**: `internal/cli` coverage raised to **87.1%** (target ≥85%) with
  new CRUD error-path tests, `cmd/fgoths` main tests, and an in-process
  control-plane graceful-shutdown test (`TestRunControlPlaneShutdownOnSignal`)
  that uses an in-memory `net.Pipe` listener and a synthetic signal channel —
  no subprocess, no real socket bind, race-detector clean.
- **Server lifecycle tests** (`pkg/runtime`): `WithListener` (injection,
  single-consumption, nil guards), `ListenAndServe` error paths
  (uninitialized server, bind failure surfacing), the `FGOTHS_READY_FILE`
  dev-watcher handshake, `OnShutdown`/`WithShutdownTimeout`/`WithReusePort`
  nil guards, and the `statusRecorder` pool round-trip. `pkg/runtime`
  coverage rose to **90.8%** (from 89.8%).
- **Request-ID fallback test**: forced `crypto/rand` failure path via a
  `randRead` indirection, plus TLS/URL-scheme branches of
  `schemeFromRequest` and upstream registry edge cases
  (`ResolveRouteTarget` nil/unknown/no-healthy-upstream, `NewProxy` error).
- **Toolchain-path tests** (`internal/cli/generate.go`): `ensureTool`
  success branch (templ resolvable from a stubbed PATH) and the
  `runGenerateOnce` error branches (real `go install` fallback under an
  isolated HOME/GOPATH) — `ensureTool` **50% → 100%**,
  `internal/cli` **86.5% → 87.1%**. The `TestRunGenerateOnceErrorPaths`
  scratch lives outside `t.TempDir()` because Go's read-only modcache
  breaks `RemoveAll` cleanup; a chmod-before-remove cleanup handles it.
- **Contention benchmark**: `BenchmarkMetricsRecordParallel`/`Serial`
  (`pkg/runtime/metrics_bench_test.go`) lock in the Record-contention
  regression guard for future runtime changes.

### Removed
- Dead `nestedRuntimeSyncDirs`/`nestedRuntimeFiles` machinery in
  `sync-templates` (no nested modules remain to keep in lockstep) and its
  orphaned test file.
- **Orphaned `audit.go` template** (`base/pkg/runtime/audit.go.tpl`): it was
  never wired into the generator or `sync-templates`, and referenced
  `ContextClaims`/`ContextTenant` that only exist in the `jwt-auth` feature —
  the copy shipped in `examples/api-demo` did not even compile. The audit
  middleware remains available in `pkg/runtime` (`AuditLogger`) for direct
  consumers; a future feature-scoped template can reintroduce it properly.

## [1.0.0] - 2026-09-22

First versioned release. The generator, both project layouts (`flat`, `mvc`),
and the embedded runtime have representative end-to-end validation and
reproducible benchmarks. The generated-project test matrix is not exhaustive.

### Generator
- Two presets: `api` (flat JSON API) and `webapp` (MVC SSR + Templ + HTMX),
  each opt-in to a database (`sqlite`, `postgres`, `mysql`) and a feature set
- Opt-in features: `health`, `metrics`, `openapi`, `grpc`, `flatbuffers`,
  `htmx`, `jwt-auth`, `mtls`, `otel`, `ci-cd`
- Atomic generation: files are written to a staging directory and only
  renamed into place after every template renders successfully
- Template drift guard (`TestRuntimeTemplatesInSync`, `make check-templates`):
  managed source/template pairs for selected runtime files are verified
  byte-identical to the framework's tested `pkg/runtime` sources

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

[Unreleased]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.4.1...HEAD
[1.4.1]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/WhoseBiasDoYallSeek/fgoths-framework/releases/tag/v1.0.0
