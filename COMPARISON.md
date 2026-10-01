# FGOTHS vs the Go ecosystem — measured comparison

> All numbers below were measured locally on this repository (Apple M4,
> Darwin arm64, 10 cores, Go 1.27). Reproduce everything yourself:
> `./benchmarks/run-comparison.sh`, `make benchmark-saturation`, and
> `make benchmark-surface`.
>
> **TL;DR:** FGOTHS has the fastest static-route dispatch of the group,
> sits mid-pack on parameterized routes, and matches go-zero under sustained
> load, all while a default generated project links **1** external module
> against gin's 18 and go-zero's 44. Over real TCP every framework
> converges because the network stack dominates. So the lasting
> differences are supply-chain surface, the code-ownership and upgrade
> model, and built-in operations. That is where FGOTHS leads.
>
> **Scope:** FGOTHS is the service-platform foundation for one organization,
> and this document benchmarks it for that role. Package and module counts
> measure audit surface. They are not a vulnerability score.

## Route dispatch (in-process: routing + handler, no network)

Median of 6 runs (`go test -bench Dispatch -benchmem -count=6`):

| Framework | Static ns/op | B/op | allocs | Param ns/op | B/op | allocs |
|---|---:|---:|---:|---:|---:|---:|
| **FGOTHS** | **338** | **1024** | **10** | 401 | **1024** | **10** |
| stdlib ServeMux (Go 1.22+ patterns) | 366 | 1024 | 10 | 412 | 1040 | 11 |
| go-zero (PatRouter) | 371 | 1024 | 10 | 582 | 1744 | 15 |
| gin | 385 | 1072 | 11 | **388** | 1072 | 11 |
| chi | 440 | 1392 | 12 | 532 | 1729 | 14 |

How to read it:

- **Static routes** (health checks, fixed endpoints): FGOTHS's exact-match
  map hit is the fastest of the group, 8% ahead of the modern stdlib and
  23% ahead of chi.
- **Parameterized routes:** FGOTHS beats the stdlib, chi, and go-zero, and
  is within 13 ns (3%) of gin, a radix-tree router built for this case. It
  allocates the least of the group (1024 B, 10 allocs, the same as a static
  route): the matched pattern is recorded on the request itself and
  parameters are extracted lazily, so dispatch adds no allocation.
- The stdlib row uses the **modern** `GET /api/users/{id}` pattern (method
  matching plus wildcard extraction), so every column does the same work.

## End-to-end over real loopback TCP (httptest.NewServer + http.Client)

| Framework | Param route ns/op | Static route ns/op |
|---|---|---|
| FGOTHS | ~28.2µs | ~27.5µs |
| stdlib | ~28.0µs | ~27.6µs |
| gin | ~28.0µs | ~27.6µs |
| go-zero | ~28.3µs | ~27.4µs |
| chi | ~28.3µs | ~27.7µs |

All frameworks are within noise of each other.
Choose on dependency surface, features and operational model.

## Sustained load (vegeta, real TCP, FGOTHS runtime router vs go-zero rest.Server)

Same API on both servers (`/health`, `/users`, `/users/<id>`, `POST /users`),
identical JSON responses, 100% success on every scenario
(`./benchmarks/run-comparison.sh`):

| Scenario | FGOTHS | go-zero |
|---|---|---|
| Saturation `/health`, 100 workers | **109,877 req/s**, p99 2.05ms | 107,884 req/s, p99 2.12ms |
| 2000 req/s `/users/42`, mean latency | 106µs | 104µs |
| 500 req/s `/users/42`, mean latency | 190µs | 171µs |

Parity. go-zero's full middleware chain costs nothing measurable at this
workload, and FGOTHS's ~2% saturation edge is within run-to-run variance.

## Dependency surface: batteries without the tax

What ships in the binary (`go list -deps`). FGOTHS rows are **generated
projects** with their batteries included. The other rows are the bare
framework, before you add a logger, metrics, health checks, or a proxy.

| Shipped binary | External modules | External packages | Total packages |
|---|---:|---:|---:|
| stdlib `net/http` (baseline) | 0 | 0 | 187 |
| **FGOTHS api project, default** | **1** | **1** | **206** |
| chi | 1 | 1 | 190 |
| FGOTHS api project, every feature | 8 | 23 | 231 |
| gin | 18 | 90 | 309 |
| go-zero `rest` (full server) | 44 | 280 | 507 |

A default FGOTHS service has router, middleware, health checks, request
IDs, graceful shutdown, socket activation, a resilient reverse proxy, and
hot reload, with the same module footprint as bare chi. Its one module
(`google/flatbuffers`) is the hot-reload wire format. Even with every
feature on (metrics, OpenAPI, JWT, mTLS, OpenTelemetry, gRPC), FGOTHS links
fewer modules than bare gin, and 6 of its 8 come from OpenTelemetry.

That is the real cost of batteries-included elsewhere: audit surface, CVE
exposure, and upgrade churn. FGOTHS avoids it by building on the stdlib and
making every addition opt-in. Reproduce with `make benchmark-surface`.

## What each stack actually is

| | FGOTHS | go-zero | gin / chi + stdlib |
|---|---|---|---|
| Model | Generator + owned runtime, upgradable via 3-way merge | Full framework + codegen (goctl) | Compose-it-yourself |
| Routing | Fastest static dispatch, path params via lazy `PathValue` | Full-featured, slower dispatch | Full-featured |
| Batteries | Health, resilient proxy, HMR built in; metrics, OpenAPI, auth, tracing, governance opt-in | RPC, JWT, monitoring, service framework | None — you assemble |
| Supply chain | 1 module by default | 44 modules | gin 18, chi 1 (before you add anything) |
| Upgrades | `fgoths upgrade` keeps local edits | Bump the import | Bump each import |
| Footprint | ~6 MB static, 0 CGO, scratch images | Larger; more deps to audit | Small, but you build the ops layer |
| Best for | One organization's auditable service platform | Teams standardizing on one service framework | Teams that want zero framework lock-in |
