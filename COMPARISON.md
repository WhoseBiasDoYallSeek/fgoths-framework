# FGOTHS vs the Go ecosystem — measured comparison

> All numbers below were measured locally on this repository (Apple M4,
> Darwin arm64, 10 cores, Go 1.26). Reproduce everything yourself:
> `./benchmarks/run-benchmarks.sh` and `./benchmarks/run-comparison.sh`.
>
> **TL;DR:** in-process route dispatch puts FGOTHS in the same band as gin and
> the Go 1.22+ stdlib, ahead of chi and go-zero. Over real TCP every framework
> converges — the network stack dominates. The durable differences are
> dependency surface, binary footprint and operational model, not ns/op.

## Route dispatch (in-process: routing + handler, no network)

| Framework | Param route ns/op | B/op | Static route ns/op | B/op |
|---|---|---|---|---|
| **FGOTHS** | **370** | **1024** | **313** | **1024** |
| gin | 364 | 1072 | 360 | 1072 |
| stdlib ServeMux (Go 1.22+ patterns) | 378 | 1040 | 341 | 1024 |
| go-zero (PatRouter) | 542 | 1744 | 349 | 1024 |
| chi | 503 | 1728 | 410 | 1392 |

**Read this table honestly:**

- The stdlib row uses the **modern** `GET /api/users/{id}` pattern (method
  matching + wildcard extraction), not the legacy trailing-slash pattern.
  Against the modern stdlib, FGOTHS's dispatch advantage is ~2-8%, not the
  ~20% previously published against the legacy pattern.
- FGOTHS matches and extracts path parameters (`{id}` and `:id` styles) via
  a lazy context-injected `PathValue` — no per-request allocations for the
  extracted values. stdlib, chi, gin and go-zero all parse and extract
  `{id}` as well, so the param column is now apples-to-apples dispatch
  work. Remaining differences are implementation details, not capability
  gaps.
- On static routes (the health-endpoint case), FGOTHS's exact-match map hit
  is genuinely the fastest of the group.

## End-to-end over real loopback TCP (httptest.NewServer + http.Client)

| Framework | Param route ns/op | Static route ns/op |
|---|---|---|
| FGOTHS | ~28.2µs | ~27.5µs |
| stdlib | ~28.0µs | ~27.6µs |
| gin | ~28.0µs | ~27.6µs |
| go-zero | ~28.3µs | ~27.4µs |
| chi | ~28.3µs | ~27.7µs |

All frameworks are within noise of each other. **Any claim that a Go router
makes your real service meaningfully faster over the network is marketing.**
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

## Dependency surface (the batteries-included tax)

`go list -deps`, packages pulled in by each framework's entry point:

| Framework | Total packages | Non-stdlib |
|---|---|---|
| stdlib `net/http` (baseline) | 187 | **0** |
| chi | 190 | 1 |
| **FGOTHS runtime** | 235 | **26** |
| gin | 309 | 90 |
| go-zero rest (full server) | 507 | 280 |

go-zero pulls **~12x more external packages** than the FGOTHS runtime. That
is the real cost of batteries-included: audit surface, CVE exposure and
upgrade churn — not dispatch speed.

## What each stack actually is

| | FGOTHS | go-zero | gin / chi + stdlib |
|---|---|---|---|
| Model | Generator + embedded runtime | Full framework + codegen (goctl) | Compose-it-yourself |
| Routing | stdlib-band speed, path params via lazy `PathValue` | Full-featured, slower dispatch | Full-featured |
| Batteries | Health, metrics, OpenAPI, proxy, governance (opt-in) | RPC, JWT, monitoring, service framework | None — you assemble |
| Footprint | ~8.8 MB static, 0 CGO, scratch images | Larger; more deps to audit | Small, but you build the ops layer |
| Best for | Regulated internal services, minimal audit surface | Teams standardizing on one service framework | Teams that want zero framework lock-in |

