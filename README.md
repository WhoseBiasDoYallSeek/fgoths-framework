# FGOTHS Framework

> **An auditable Go toolkit for building your organization's services**
>
> `v1.2.0` · Apache 2.0 · Go 1.26+

**FGOTHS** (*FOR THE GOTH STACK*) is a modular Go scaffolding toolkit with an
embedded runtime. Pick a project type, architecture, database
and optional features — it generates a real, compiling project with
observability, governance and a minimal footprint.

FGOTHS is the service-generation and embedded-runtime foundation for one
organization's application platform. It standardizes how that organization
creates, audits, deploys, and operates its services; it is intentionally not a
general-purpose Rails-style web framework or a claim of fit for every
organization. Its small dependency surface and resilience primitives support
audit workflows, but FGOTHS is not a compliance certification.

| | |
|---|---|
| 📦 **Dependencies** | Standard-library runtime; generated projects add only selected features |
| ⚡ **Throughput** | 99.91k req/s median, 6 × 15s saturation runs at 150 workers (92.05k–103.75k, 100% success) |
| 🎯 **Latency** | p99 = 3.00ms median at 150 workers · 686µs p99 TCP guard |
| 💾 **Footprint** | ~8.8 MB static binary · 0 CGO · scratch images <10MB |
| 🧪 **Quality** | `make test` (race detector) · `make cover` (100% statement coverage) |

The saturation figure above is a recorded result, not a per-machine guarantee.
For repeatable local measurements with per-run latency reports and separate
CPU and heap/mutex/block profiling runs, use `make benchmark-saturation`. Results
are written to a temporary directory by default; set `RESULTS_DIR=/path/to/dir`
to retain them at a chosen location. Tune the experiment with
`SATURATION_RUNS`, `SATURATION_DURATION`, `SATURATION_WARMUP_DURATION`, and
`SATURATION_WORKERS` (default: 150). Profiling is loopback-only and runs
separately so its overhead does not affect the clean throughput summary.
Repeated on Apple M4 (10 logical CPUs, macOS arm64, Go 1.27.0), 150 workers
measured a median 99.91k req/s and 3.00ms p99 across six runs. At 100 workers,
eight runs measured 92.08k req/s and 2.26ms p99; 200 workers measured 96.83k
req/s and 3.96ms p99 across three runs. The 150-worker setting therefore
improved measured throughput by about 8.5% versus 100 workers, with a roughly
33% p99 latency increase; tune concurrency to the application's latency
target rather than treating the highest throughput as universally best.

Profiles attributed most sampled CPU to system calls and the local TCP path;
request parsing and HTTP header handling dominated allocated bytes. The router
and health handler were not significant CPU hotspots, so no router or pool
change is justified by this workload alone. The benchmark primarily measures
the complete local `net/http` + loopback path, not application middleware,
database, or production-network performance.

To measure generated SQLite-backed CRUD traffic and compare the FGOTHS router
with `net/http`'s `ServeMux` using the same handlers and middleware, run
`make benchmark-real-app`; see [the benchmark app guide](./benchmarks/real-app/README.md).

---

## 🔒 Why FGOTHS?

### Dependency surface (the batteries-included tax)
```
go-zero rest:   280 non-stdlib packages
  gin:           90 non-stdlib packages
  FGOTHS:         5 non-stdlib packages (jwt, otel, go-logr)
  stdlib:         0  (baseline)
```

Full measured comparison against Go batteries-included frameworks:
[COMPARISON.md](./COMPARISON.md).

### Measured performance, stress tests and edge cases (see [benchmarks/run-benchmarks.sh](./benchmarks/run-benchmarks.sh))
```
FGOTHS Benchmark Suite (2026-09-21 19:21)
   Machine: Darwin arm64, 10 cores

===================================================================
  1/5  Route dispatch comparison: FGOTHS vs stdlib (1.22+ patterns) vs chi vs gin vs go-zero
===================================================================
goos: darwin
goarch: arm64
pkg: compbench
cpu: Apple M4
BenchmarkParamDispatchFGOTHS-10     	 9799910	       366.1 ns/op	      1024 B/op	      10 allocs/op
BenchmarkParamDispatchStdlib-10     	 9615567	       376.9 ns/op	      1040 B/op	      11 allocs/op
BenchmarkParamDispatchChi-10        	 7298365	       493.5 ns/op	      1728 B/op	      14 allocs/op
BenchmarkParamDispatchGin-10        	10058604	       358.6 ns/op	      1072 B/op	      11 allocs/op
BenchmarkParamDispatchGoZero-10     	 6626764	       543.1 ns/op	      1744 B/op	      15 allocs/op
BenchmarkStaticDispatchFGOTHS-10    	11517087	       314.1 ns/op	      1024 B/op	      10 allocs/op
BenchmarkStaticDispatchStdlib-10    	10608198	       340.6 ns/op	      1024 B/op	      10 allocs/op
BenchmarkStaticDispatchChi-10       	 8763014	       410.8 ns/op	      1392 B/op	      12 allocs/op
BenchmarkStaticDispatchGin-10       	10147164	       357.2 ns/op	      1072 B/op	      11 allocs/op
BenchmarkStaticDispatchGoZero-10    	 9731166	       362.9 ns/op	      1024 B/op	      10 allocs/op
BenchmarkE2EParamFGOTHS-10          	 127616	      27656 ns/op	     5963 B/op	      67 allocs/op
BenchmarkE2EParamStdlib-10          	 123373	      27957 ns/op	     5987 B/op	      68 allocs/op
BenchmarkE2EParamChi-10             	 128101	      28004 ns/op	     6698 B/op	      71 allocs/op
BenchmarkE2EParamGin-10             	 130659	      27746 ns/op	     6027 B/op	      68 allocs/op
BenchmarkE2EParamGoZero-10          	 127664	      28108 ns/op	     6699 B/op	      72 allocs/op
BenchmarkE2EStaticFGOTHS-10         	 131946	      27318 ns/op	     5957 B/op	      67 allocs/op
BenchmarkE2EStaticStdlib-10         	 131211	      27424 ns/op	     5950 B/op	      67 allocs/op
BenchmarkE2EStaticChi-10            	 130450	      27575 ns/op	     6333 B/op	      69 allocs/op
BenchmarkE2EStaticGin-10            	 131268	      27474 ns/op	     6016 B/op	      68 allocs/op
BenchmarkE2EStaticGoZero-10         	 130422	      27316 ns/op	     5955 B/op	      67 allocs/op
PASS
ok  	compbench	78.959s

Dependency surface (go list -deps, packages pulled in):
  stdlib net/http (baseline)           187 packages total (0 non-stdlib)
  FGOTHS pkg/runtime                   223 packages total (5 non-stdlib)
  chi                                  190 packages total (1 non-stdlib)
  gin                                  309 packages total (90 non-stdlib)
  go-zero rest                         507 packages total (280 non-stdlib)

> Note: the FGOTHS param route matches and extracts `{id}` via a lazy
> context-injected `PathValue` (no per-request allocations for the values).
> stdlib/chi/gin/go-zero all parse and extract `{id}` too — the dispatch
> numbers are now apples-to-apples. Over real TCP all frameworks converge
> (~28µs e2e); see COMPARISON.md for the honest reading.

===================================================================
  2/5  Runtime percentile benchmarks (router + proxy)
===================================================================
goos: darwin
goarch: arm64
pkg: github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime
cpu: Apple M4
BenchmarkRouterPercentiles-10            9366830               346.3 ns/op            7615 max-µs                0 p50-µs            1.000 p90-µs            3.000 p99-µs           51.00 p999-µs
BenchmarkProxyPercentiles-10               49626            107412 ns/op             1147 p50-µs          1314 p99-µs          1372 p999-µs
PASS
ok      github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime        13.242s

===================================================================
  3/5  Vegeta stress tests
===================================================================
2026/09/21 19:22:44 🚀 FGOTHS Benchmark Server (runtime router) running on http://localhost:18080
✅ benchmark server running on :18080 (pid 27397)

--- Fixed rate: 500 req/s for 10s ---
Requests      [total, rate, throughput]         5000, 500.10, 500.10
Duration      [total, attack, wait]             9.998s, 9.998s, 95.958µs
Latencies     [min, mean, 50, 90, 95, 99, max]  54.208µs, 110.333µs, 100.98µs, 142.489µs, 150.261µs, 174.344µs, 1.112ms
Bytes In      [total, mean]                     75000, 15.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:5000
Error Set:

--- Fixed rate: 2000 req/s for 10s ---
Requests      [total, rate, throughput]         20000, 2000.11, 2000.09
Duration      [total, attack, wait]             10s, 9.999s, 67.708µs
Latencies     [min, mean, 50, 90, 95, 99, max]  36.75µs, 69.05µs, 70.867µs, 81.146µs, 90.814µs, 121.413µs, 901.875µs
Bytes In      [total, mean]                     300000, 15.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:20000
Error Set:

--- Fixed rate: 5000 req/s for 10s ---
Requests      [total, rate, throughput]         50000, 5000.10, 5000.08
Duration      [total, attack, wait]             10s, 10s, 30.125µs
Latencies     [min, mean, 50, 90, 95, 99, max]  22.792µs, 35.413µs, 33.764µs, 41.152µs, 45.991µs, 57.682µs, 725.459µs
Bytes In      [total, mean]                     750000, 15.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:50000
Error Set:

--- Saturation: max workers for 15s ---
Requests      [total, rate, throughput]         1550064, 103336.03, 103332.99
Duration      [total, attack, wait]             15.001s, 15s, 441.791µs
Latencies     [min, mean, 50, 90, 95, 99, max]  18.791µs, 623.974µs, 463.72µs, 1.351ms, 1.621ms, 2.225ms, 5.101ms
Bytes In      [total, mean]                     23250960, 15.00
Bytes Out     [total, mean]                     0, 0.00
Success       [ratio]                           100.00%
Status Codes  [code:count]                      200:1550064
Error Set:

> Saturation measured against the real FGOTHS runtime router
> (benchmarks/fgoths now runs on pkg/runtime, not stdlib). Head-to-head with
> go-zero's full rest.Server: 109,877 vs 107,884 req/s — parity. See
> ./benchmarks/run-comparison.sh.

===================================================================
  4/5  Network tail-latency guard (p99 / p99.9 over real TCP)
===================================================================
    latency_percentiles_test.go:168: samples=288639 errors=0 p50=100.042µs p99=512.208µs p99.9=706.375µs max=2.573583ms
--- PASS: TestNetworkLatencyPercentilesGuard (2.02s)
PASS

===================================================================
  5/5  Edge case suite
===================================================================
--- PASS: TestEdgeCasesPathTraversal (0.00s)
--- PASS: TestEdgeCasesCRLFInjection (0.00s)
--- PASS: TestEdgeCasesLargeBody (0.03s)
--- PASS: TestEdgeCasesWeirdPaths (0.00s)
--- PASS: TestEdgeCasesMethodMismatch (0.00s)
--- PASS: TestEdgeCasesProxyBadUpstream (0.00s)
--- PASS: TestEdgeCasesConcurrentRouteRegistrationAndServing (0.00s)
--- PASS: TestServesServiceEdgeCases (0.00s)
ok      github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime        1.265s

```

> **Reproduce every claim yourself:** `make benchmark` (or
> `./benchmarks/run-benchmarks.sh --quick`). Requires Go 1.26+ and vegeta.
> Head-to-head vs go-zero over real TCP: `./benchmarks/run-comparison.sh`.
>
> **v1.1.0 update:** the dispatch comparison above predates the runtime
> allocation pass. Fresh numbers on the same machine (in-process dispatch,
> `cd benchmarks/comparison && go test -bench Dispatch -benchmem`): param
> routes now **13 allocs/op** (~495 ns) and static routes **10 allocs/op**
> (~355 ns) — down from 16/13 in the block above. Static dispatch now matches
> or beats stdlib `http.ServeMux` on both time and allocations.

### Deployment simplicity
```bash
# Traditional stack: Docker, K8s, helm, pipelines...
# FGOTHS:
scp bin/app server:/opt/app && ssh server "systemctl restart app"
# DONE! ✅
```

---

## 🚀 The FGOTHS Stack

| Letter | Technology | Role |
|--------|-----------|------|
| **F** | FlatBuffers (`.fbs`) | Optional zero-copy schemas (`fgoths generate`) |
| **G** | Golang | Fast, statically-typed, single-binary |
| **O** | Orchestration | Two focused layouts: flat API or MVC webapp |
| **T** | Templates | Templ-based server-side rendering for webapps |
| **H** | HTMX | Optional server-driven reactivity |
| **S** | SQL / Scratch | SQLite WAL + scratch containers (<10MB) |

---

## ⚡ Quick Start

```bash
# Install
git clone https://github.com/WhoseBiasDoYallSeek/fgoths-framework.git
cd fgoths-framework
make build
./bin/fgoths version   # → 1.2.0

# Create your first API (simplest way)
./bin/fgoths init --name=user-service --preset=api
cd user-service && make run
# → /health on :8080
```

### The fastest path: two presets

| Preset | What you get | Opt-ins (`--features`, `--db`) |
|--------|--------------|--------------------------------|
| `api` | Flat JSON API + health + proxy | metrics, openapi, grpc, jwt-auth, mtls, otel, flatbuffers, `--db=sqlite` |
| `webapp` | MVC SSR + Templ + HTMX + SQLite + proxy | metrics, openapi, jwt-auth, mtls, otel, flatbuffers |

Two layouts, deliberately:

- **`flat`** — one `main.go` and a `handlers/` package. No ceremony. Ideal for
  JSON APIs and service-to-service traffic; compiles to a ~6.3 MB static binary
  that runs in a `FROM scratch` container (`make docker-build`).
- **`mvc`** — `main.go` + `handlers/` + `models/` + `views/` (Templ + HTMX) +
  `middleware/`. For server-rendered sites and CRUD apps. Its handlers serve
  JSON too, so "both in one binary" is just `webapp`.

### Which preset?

- JSON API or service-to-service? → `api`
- Rendering HTML pages? → `webapp`
- Both in one binary? → `webapp` (add `--features=openapi` if you want docs)

Everything else is opt-in per project — the generated code is yours to extend.

```bash
# Examples
./bin/fgoths init --preset=api --name=user-service
./bin/fgoths init --preset=webapp --name=my-site

# Opt in to what you need
./bin/fgoths init --name=my-api --preset=api --db=sqlite --features=metrics,openapi

# Choose where the project is created (--dir, default: current directory)
./bin/fgoths init --name=user-service --preset=api --dir=~/code/services
./bin/fgoths init --name=my-site --preset=webapp --dir=/absolute/path/to/workspace

# List presets and every opt-in feature
./bin/fgoths presets
```

Every generated project starts with a compiling application. It includes the
runtime source files and feature code selected by its configuration; managed
source/template pairs are checked by the test suite and `make check-templates`.
Release-generated projects record the framework version and selected project
configuration in `.fgoths/upgrade.json`.

> **SSR note:** MVC/SSR projects compile `views/*.templ` via the pinned Templ
> toolchain. Run `make generate` once after `init` (or just use `make run` /
> `make build`, which depend on it).

### Updating an existing project's runtime

Generated applications embed selected runtime source files instead of
depending on the FGOTHS module, so installing a newer CLI does not change an
existing application. `fgoths upgrade` updates the runtime in place. It runs
as a dry run by default, backs up every file it changes, and uses a three-way
merge for local edits. If a merge conflicts, the original file stays untouched
and the proposed merge is written under `.fgoths/upgrade-conflicts/`; the
backup and conflict directories are ignored by Git, while the upgrade
metadata is intended to be committed. Upgrades add a dedicated
`.fgoths/.gitignore` when needed, preserving any existing rules.

Projects created before upgrade metadata was introduced must specify their
current version. The current migration supports v1.1.0 projects. Use a CLI
build that includes `upgrade`; while this feature is unreleased, build it from
the framework checkout with `make build` and invoke that checkout's
`bin/fgoths`:

```bash
# Inspect changes without modifying the site.
/path/to/fgoths-framework/bin/fgoths upgrade --from=1.1.0 --dir=/path/to/my-site

# Apply safe runtime updates and keep backups.
/path/to/fgoths-framework/bin/fgoths upgrade --from=1.1.0 --dir=/path/to/my-site --apply
```

This version updates only changed embedded runtime source files; it does not
regenerate application handlers, route registries, views, migrations, or
configuration. Git must be installed when a runtime file has local edits.
Review any conflict candidate, then run `go test -race ./...` in the application
and its normal build/deploy checks before shipping. The command refuses
unknown source versions rather than guessing.

---

## 🏢 Enterprise features (opt-in, modular)

The generated runtime uses a small standard-library-first dependency surface.
Projects add only the selected features; MVC webapps also use the pinned Templ
toolchain:

| Feature | What it adds | Extra dependency |
|---|---|---|
| `jwt-auth` | `RequireJWT` policy middleware (roles/scopes/claims), fails closed without a secret | `golang-jwt/jwt/v5` |
| `mtls` | `WithMutualTLS` + `RequireClientIdentity` (CN/SAN/SPIFFE routing) | none (stdlib crypto) |
| `otel` | `WithOpenTelemetry` tracing, `TraceMiddleware`, correlation headers | `go.opentelemetry.io/otel` |

```bash
./bin/fgoths init --name=secure-svc --features=health,jwt-auth,mtls,otel
```

---

## 🎛️ Control plane (built-in governance)

Generated projects can embed the same governance primitives the framework
uses — or you can run the standalone control plane API:

```bash
FGOTHS_CP_TOKEN=op-secret ./bin/fgoths controlplane --addr=:9091 \
  --store=sqlite://controlplane.db
```

- **Declarative routes & policies** with per-environment enforcement
- **Policy versioning**: append-only history with actor, reason and readable diffs; rollback as a new version
- **Release workflows**: multi-approver gates, terminal rollback/reject states
- **Deployment ledger**: survives restarts (file or SQLite persistence)
- **Progressive delivery**: canary weighting, staged rollout, error-threshold rollback
- **Multi-region failover**: health-aware region selection

All operations are available over REST (`/api/v1/...`) with bearer-token auth
that fails closed.

---

## 🔄 Built-in reverse proxy

The embedded runtime includes a native Go reverse proxy — no Envoy, no CGO:

```go
proxy, _ := runtime.NewProxy("http://localhost:8081")
proxy.WithMetrics().WithRetry(2, 150*time.Millisecond).
     WithHealthCheck("/health", time.Second).
     WithFailover("http://localhost:8082")
```

Retry, circuit breaker, rate limiting, health-checked failover, request
observers and connection pooling are all in the box. See
[examples/api-demo](./examples/api-demo) for a complete generated project.

---

## 🎯 CLI Commands

```bash
# Create projects
./bin/fgoths init --preset=api --name=user-service            # fastest
./bin/fgoths init --preset=webapp --name=my-site
./bin/fgoths init --preset=api --name=my-api --db=sqlite --features=metrics,openapi

# In an existing generated project (run these inside the project's own
# directory, not inside the fgoths-framework repo — the framework itself
# only needs `make build` / `make test`, see Contributing below)
./bin/fgoths generate     # compile .templ files (webapp/SSR projects)
./bin/fgoths dev          # runs `make dev`: build-ahead restarts + fragment HMR
./bin/fgoths build        # compile to bin/app
./bin/fgoths build --scratch  # static binary + Dockerfile

# Explore
./bin/fgoths presets      # list all presets with details
./bin/fgoths version      # show version

# Standalone control plane (governance API)
FGOTHS_CP_TOKEN=secret ./bin/fgoths controlplane --store=sqlite://cp.db
```

### Available options

| Flag | Description | Default |
|------|-------------|---------|
| `--name` | Project directory name | required |
| `--preset` | `api` · `webapp` | required |
| `--db` | Override the preset database: `none` · `sqlite` · `postgres` · `mysql` | preset default |
| `--features` | Comma-separated features added to the preset defaults | preset defaults |
| `--dir` | Parent directory where the project is created | current directory |

### Available features

| Feature | What it adds |
|---------|--------------|
| `health` | `/health`, `/health/live`, `/health/ready` |
| `metrics` | `/metrics` (Prometheus-compatible, zero deps) |
| `openapi` | `/docs` + `/openapi.yaml` |
| `grpc` | gRPC-style JSON API on `:9090` |
| `flatbuffers` | Zero-copy binary serialization |
| `htmx` | Server-driven reactivity without JS |
| `jwt-auth` | JWT middleware with roles/scopes |
| `mtls` | Mutual TLS + client identity routing |
| `otel` | OpenTelemetry distributed tracing |
| `ci-cd` | GitHub Actions + GitLab CI templates |

---

## ✨ Feature status

### Implemented and validated
- Modular generator (type × architecture × database × features)
- 2 focused layouts (flat API, MVC webapp), 3 databases with embedded migrations
- Health, Metrics (Prometheus-compatible output, zero deps), OpenAPI (spec + docs)
- gRPC-style server (JSON transcoding, health protocol — zero deps)
- CI/CD pipeline templates (GitHub Actions + GitLab CI)
- `fgoths build --sbom --scratch` — static binary + SBOM + Dockerfile
- Safe project names, non-overwriting generation, template drift guard
- Edge-case hardened: path traversal, CRLF, large bodies, concurrent registration (race-detector clean)
- Server lifecycle: `WithListener` (socket activation / systemd handoff),
  `WithReusePort` (zero-downtime dev swap), `WithShutdownTimeout`, `OnShutdown` hooks
- Request correlation: `WithRequestID` (X-Request-ID / X-Correlation-ID / X-Trace-ID)
- OpenTelemetry tracing (server spans + proxy hops, W3C TraceContext propagation)

### Verified in this release
- API project: `/health`, `/metrics`, `/docs`, `/openapi.yaml` respond correctly
- SSR MVC project: `/` renders HTML + HTMX (run `make generate` once so the
  pinned Templ toolchain compiles `views/*.templ`; `make run` / `make build`
  depend on it)
- Generated projects use the FGOTHS registrar and all five HTTP verb helpers;
  MVC static assets get deterministic fingerprints, development no-store
  caching, and browser reloads after CSS, JS, or HTML changes.
- Generated-project integration tests compile representative flat projects
  (minimal, health, and health + metrics + OpenAPI + gRPC) and an MVC/SQLite
  project with generated collection routes. Generator unit tests exercise
  each supported database option and the supported feature selections; they
  do not compile every possible combination.
- `make cover` reports 100% statement coverage for the root module's short-mode
  test suite. The generated SQLite CRUD benchmark compares identical handlers
  and middleware on FGOTHS and `net/http`; its recorded runs show overlapping
  throughput ranges, not a material router advantage.
- Control plane: survives restarts, auth fails closed, policy rollback works over REST
- Saturation benchmark: see the repeated 100/150/200-worker measurements above
  and `make benchmark-saturation` for the reproducible measurement and profiling
  workflow.

---

## 📁 Project structure

**`api` preset (flat):**
```
user-service/
├── go.mod                  # module + conditional deps
├── Makefile                # run / build / generate / test / docker-*
├── main.go                 # entrypoint: server, routes, graceful shutdown
├── cmd/
│   ├── dev/                # development watcher
│   └── assetmanifest/      # deterministic static asset fingerprints
├── handlers/               # HTTP handlers — add your endpoints here
├── internal/database/      # only when --db is set: connection + migrations
├── internal/assets/        # asset URL helper and generated manifest
├── pkg/runtime/            # selected embedded runtime sources
└── static/css/app.css
```

**`webapp` preset (mvc):**
```
my-site/
├── go.mod
├── Makefile
├── main.go                 # entrypoint
├── cmd/                    # dev watcher and asset manifest generator
├── handlers/               # page handlers + JSON API endpoints
├── models/                 # domain entities
├── views/                  # Templ components (SSR + HTMX)
├── middleware/             # shared middleware (logging, cors)
├── internal/database/      # SQLite/Postgres/MySQL connection + repositories
├── internal/assets/        # asset URL helper and generated manifest
├── pkg/runtime/            # embedded FGOTHS runtime
└── static/
```

---

## 📚 Documentation

| Document | Content |
|---|---|
| [ARCHITECTURE.md](./ARCHITECTURE.md) | Technical design and principles |
| [BOILERPLATE.md](./BOILERPLATE.md) | Generated folder structure and data flow |
| [COMPARISON.md](./COMPARISON.md) | Measured comparison vs Go batteries-included frameworks (go-zero, gin, chi, stdlib) |
| [CHANGELOG.md](./CHANGELOG.md) | Version history (Keep a Changelog format) |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | How to contribute |
| [benchmarks/run-benchmarks.sh](./benchmarks/run-benchmarks.sh) | Reproduce every performance claim |
| [benchmarks/run-comparison.sh](./benchmarks/run-comparison.sh) | FGOTHS vs go-zero head-to-head over real TCP |
| [benchmarks/check-perf-regression.sh](./benchmarks/check-perf-regression.sh) | Dispatch allocation regression gate |

---

## 🔖 Versioning & releases

This project follows [Semantic Versioning](https://semver.org). The current
release line is **1.2.0**.

- **Release builds** stamp version metadata at compile time:
  ```bash
  make build          # VERSION ?= 1.2.0 in the Makefile
  ./bin/fgoths version
  ```
- **Maintenance updates** only require bumping `VERSION` in the `Makefile`
  (or passing `VERSION=x.y.z make build`) — the CLI reports it via
  `fgoths version`.
- A release candidate becomes GA after a real-world adoption cycle
  with no blocking issues.

---

## 🤝 Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](./CONTRIBUTING.md) and
the [Code of Conduct](./CODE_OF_CONDUCT.md).

```bash
git checkout -b feature/amazing-feature
make test            # race detector on
make check-templates # template drift guard
make check-perf      # dispatch allocation gate
git commit -m "Add amazing feature"
```

---

## 📄 License

Apache 2.0 — see [LICENSE](./LICENSE).

---

**Built with ❤️ for Go developers who value simplicity, performance, and
operational honesty.**
