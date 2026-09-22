# FGOTHS Architecture Documentation

## Overview
**FGOTHS** (Flatbuffers, Go, Orchestration, Templates, HTMX, SQL/Scratch) is an opinionated, high-performance web framework designed for modular project generation and pure Server-Side Rendering (SSR). The framework combines a generator CLI, architecture presets, and production-oriented generated Go projects into a streamlined DX for mission-critical services.

> Current status (v1.0.0): the generator, presets, and runtime behavior are
> validated end-to-end, with performance claims measured and reproducible
> (`benchmarks/run-benchmarks.sh`). The embedded runtime is a production-ready
> native Go layer: routing, proxy with connection pooling, retry, circuit
> breaker, health-checked failover, governance and control plane APIs. It
> remains intentionally a lightweight embedded runtime, not a full enterprise
> ingress platform.

---

## Architectures

FGOTHS generates projects in exactly two structural patterns. Fewer moving
parts, deeper polish: each layout is tuned end-to-end, from scaffolding to a
`FROM scratch` container image.

### Flat API (default for `--type=api`)

```
main.go              # entrypoint: server, routes, graceful shutdown
handlers/            # HTTP handlers — add your endpoints here
internal/            # optional feature packages (database, health, metrics,
                     # openapi, grpcapi) — generated only when selected
pkg/runtime/         # FGOTHS runtime (verbatim copy of the tested framework runtime)
```

**Principle:** zero ceremony. A JSON API should be a folder you can read in a
minute. The static binary (~6.3 MB, `CGO_ENABLED=0`) runs in a scratch
container with no base image at all.

### MVC (the `webapp` preset)

```
main.go              # entrypoint
handlers/            # controllers: page handlers + JSON API endpoints
models/              # domain entities + data transfer objects
views/               # Templ components compiled to Go (SSR + HTMX)
middleware/          # shared middleware (logging, cors)
internal/database/   # SQLite/Postgres/MySQL connection + repositories
pkg/runtime/         # FGOTHS runtime
```

**Principle:** traditional Model-View-Controller, fast to understand and fast
to prototype. `fgoths generate crud` scaffolds a complete vertical slice
(model + handler + repository + migration + tests) inside this layout.

---

## Runtime

The FGOTHS runtime is the execution layer embedded in every generated project. It is a verbatim copy of `pkg/runtime` from this framework — not a reimplementation — validated by the same test suite. This eliminates the gap between "what we test" and "what you run."

### Request lifecycle

```
HTTP Request
    ↓
Router (FGOTHS, in-memory map lookup by method+path)
    ↓ (matched route)
Middleware chain (user-defined, then audit, then metrics)
    ↓
Handler (user Go code)
    ↓ (optional)
Proxy (reverse proxy with circuit breaker, retry, failover)
    ↓
Response
```

### Proxy capabilities

The embedded proxy (`runtime.Proxy`) provides:
- **Connection pooling** — reuses upstream connections via `http.Transport`
- **Retry** — configurable retry count and backoff on failure
- **Circuit breaker** — progressive backoff on consecutive failures
- **Health-checked failover** — fallback to secondary upstream when primary is unhealthy
- **Rate limiting** — token bucket per route or global
- **Latency percentiles guard** — optional p99/p99.9 enforcement with hard exit on breach

### Server lifecycle

```
runtime.NewServer(addr)
    ↓
server.Use(middleware...)      # register middleware
server.Handle(method, path, h)   # register routes
server.WithMetrics()            # optional metrics recording
server.WithShutdownTimeout(30s)  # graceful drain timeout
server.OnShutdown(fn)           # cleanup hooks (close stores, flush buffers)
    ↓
server.ListenAndServe()        # blocking
    ↓ (SIGINT/SIGTERM)
server.Shutdown(ctx)            # drain in-flight, run OnShutdown hooks
```

---

## Hot Module Replacement (HMR) & Dev Loop

Development-mode hot reload is a first-class protocol, not an afterthought. Every dev-mode change restarts the compiled Go process; the HMR layer is what makes the browser experience seamless on top of that.

### Wire protocol (versioned)

HMR events travel as FlatBuffers tables over SSE (`GET /hmr/events`,
base64-encoded `event: fb` frames), hand-encoded against this schema:

```
table Event {
  kind:         string;  // reload | fragment | ping
  target:       string;  // CSS selector (fragment only)
  payload:      string;  // HTML fragment (fragment only)
  source:       string;  // what changed, for dev console logs
  route:        string;  // page path (fragment only)
  schema_major: ushort;  // wire schema version, breaking
  schema_minor: ushort;  // wire schema version, additive
  build_id:     string;  // dev build identity stamped per boot
  ts:           ulong;   // emission time, unix nanos
}
```

**Compatibility rules** (`hmr.IsCompatible`):
- Same `schema_major` and a `schema_minor` at or below the client's own → event is dispatched normally.
- A newer minor or a foreign major → the client refuses to interpret the buffer and falls back to a safe full-page reload.

The browser client (`pkg/runtime/hmr/client.go`) carries the same schema
constants, injected from the Go source via `fmt.Sprintf` at init — both
sides of the wire share one source of truth and cannot drift.

### Reload vs Fragment

| Event | Trigger | Browser behavior |
|---|---|---|
| `reload` | Go code, `views/layout.templ`, or any change whose blast radius is not scoped to one view | full page reload |
| `fragment` | a `views/<name>.templ` change (except layout) | fetches the fresh render of that route, swaps `#fgoths-content` innerHTML in place — scroll, focus and form state survive |
| `ping` | keep-alive | confirms the stream is live |

### Dev loop observability

`make dev` prints what happened and how long it took at every step: initial
build duration, per-change build time, process-swap duration, readiness
confirmation, and fragment patch size — e.g.
`⚡ rebuilt 842ms · swap 310ms · app ready`. Every broadcast is stamped with
a `build_id` (`dev-<boot-millis>`, overridable via `FGOTHS_BUILD_ID`) so the
browser console identifies which build produced each change. A bounded
replay window (2s) re-delivers the last event to clients reconnecting after
a restart, closing the lost-event race.

### Server integration

`WithHMR()` wires the endpoints (`/hmr/events`, `/hmr/hmr.js`,
`POST /hmr/broadcast`) and injects the client script into HTML responses.
Development-only: guard with `FGOTHS_DEV` in `main()`. Production builds
(`fgoths build`) exclude the dev watcher entirely.

---

## Governance & Control Plane

FGOTHS ships governance primitives that replace operational tooling (Istio, Vault, ArgoCD) with in-process Go code:

### Deployment Ledger
`DeploymentLedger` persists the current and historical state of every deployment across environments. Survives restarts via file or SQLite.

### Release Workflows
`ReleaseWorkflow` enforces `ApprovalGate` before promoting artifacts. Terminal states (approved/rejected/rolled-back) are immutable. Rollback is a new version, never a rewrite.

### Policy Versioning
`PolicyVersioner` maintains append-only policy history with actor, reason, and human-readable diffs. Rollback restores a previous policy as a new version.

### Artifact Provenance
`ArtifactProvenance` signs artifacts with HMAC-SHA256 and verifies on promotion. Chain of custody is auditable from build to deployment.

### Control Plane API
`ControlPlaneServer` exposes governance over REST (`/api/v1/{routes,upstreams,deployments,releases,policies}`) with bearer-token auth that fails closed.

### What FGOTHS intentionally does NOT replace

| Concern | Recommended external tool |
|---|---|
| Dynamic secrets (PKI, dynamic DB creds) | HashiCorp Vault, AWS Secrets Manager |
| Full GitOps reconciliation | ArgoCD, Flux |
| Service mesh observability (distributed tracing beyond a single binary) | Istio, Linkerd, OpenTelemetry Collector |
| Certificate management (ACME, rotation) | cert-manager |

For internal applications, regulated environments, and single-binary deployments where the above are overkill, FGOTHS governance is sufficient.

---

## Generator

The `fgoths init` CLI scaffolds projects using Go templates located in `internal/generator/templates/`, organized into:

| Directory | Content |
|---|---|
| `base/` | Always generated: `go.mod`, `Makefile`, README, dev server, and the four core runtime files |
| `architectures/` | `flat` and `mvc` layouts |
| `database/` | `sqlite`, `postgres`, `mysql` layers with embedded migrations |
| `features/` | `jwt-auth`, `otel`, `mtls`, `grpc`, `openapi`, `health`, `metrics`, `htmx`, `flatbuffers`, `ci-cd` |

The core runtime files are verbatim copies (not templates) of `pkg/runtime` —
synchronized via `sync-templates` and guarded by `TestRuntimeTemplatesInSync`.
The sync covers eight managed pairs: the four base files plus the feature
templates `jwt-auth/auth.go`, `otel/otel.go`, `mtls/tls.go` and
`mtls/identity.go`, so security-critical code cannot silently drift between the
framework runtime and generated projects. Run `make check-templates` (wired into
CI) to enforce it.

Feature-only templates (metrics, openapi, grpc, etc.) contain Go `{{}}`
directive syntax that must be templated per project, so they are not synced.
MVC webapps use Templ for SSR; API projects do not generate view
templates. Generation is atomic: files are written to a staging directory and
renamed into place only after every template renders successfully.
