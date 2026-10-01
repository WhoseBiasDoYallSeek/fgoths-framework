# Performance

**Short version:** FGOTHS has the fastest static-route dispatch among the
stdlib, gin, chi, and go-zero, sustains ~100k req/s on a laptop, and does it
while linking a single external module. Under load the cost goes to the
operating system's network stack, JSON, and your database, not to the
framework.

> All numbers come from one machine: Apple M4, 10 CPUs, macOS arm64,
> Go 1.27.0. They are recorded results, not guarantees. Run the benchmarks
> on your own hardware before relying on them.

---

## At a glance

| What | Result |
|---|---|
| Sustained throughput | **~100k req/s** median at 150 workers (6 × 15 s runs, range 92k–104k, 100% success) |
| Tail latency under load | p99 **3.0 ms** at 150 workers |
| Single-request latency (TCP guard) | p50 100 µs · p99 512 µs · p99.9 706 µs |
| Against go-zero, same load | 109,877 vs 107,884 req/s |
| Real app (SQLite CRUD) | Same as plain `net/http`, within run-to-run noise |
| Binary | ~6 MB static, no CGO, `scratch` image under 10 MB |

---

## Throughput vs latency: choose your concurrency

More concurrent workers buy a little throughput and cost more tail latency:

| Workers | Throughput (median) | p99 | Runs |
|---:|---:|---:|---:|
| 100 | 92.1k req/s | 2.26 ms | 8 |
| **150** | **99.9k req/s** | **3.00 ms** | 6 |
| 200 | 96.8k req/s | 3.96 ms | 3 |

Going from 100 to 150 workers gives about **+8.5%** throughput for
**+33%** p99. Choose the level that meets your latency target, not the one
with the biggest number.

## Where the time goes

Profiling the saturation runs showed:

- **CPU** goes mostly to system calls and the local TCP path.
- **Allocations** come mostly from request and header parsing in `net/http`.
- **The router and handlers are not hotspots.**

In the real SQLite CRUD app, the time went to JSON and SQLite. Swapping the
FGOTHS router for the standard `ServeMux` didn't change the results beyond
noise. Details are in [benchmarks/real-app](../benchmarks/real-app/README.md).

So more router tuning won't make your service faster. Cutting database
round-trips and payload size will.

## Against other Go frameworks

Routing plus handler measured in-process (no network), median ns per
request over 6 runs:

| | FGOTHS | stdlib | gin | chi | go-zero |
|---|---:|---:|---:|---:|---:|
| Static path | **338** | 366 | 385 | 440 | 371 |
| Path param | 401 | 412 | **388** | 532 | 582 |

Static routes (health checks, fixed endpoints) are the fastest of the
group. Parameterized routes beat the stdlib, chi, and go-zero and are
within 3% of gin, while allocating the least of the group: route dispatch
itself adds zero allocations. Over real TCP, all of them converge to about
**28 µs**, so the network dominates. Full methodology is in
[COMPARISON.md](../COMPARISON.md).

## Dependency surface

What actually ships: the packages and modules linked into the binary
(`go list -deps`). FGOTHS rows are **generated projects**. The other rows
are the framework alone, before you add anything to it.

| Shipped binary | External modules | External packages | Total packages |
|---|---:|---:|---:|
| Go stdlib only | 0 | 0 | 187 |
| **FGOTHS api project, default** | **1** | **1** | **206** |
| chi | 1 | 1 | 190 |
| FGOTHS api project, every feature | 8 | 23 | 231 |
| gin | 18 | 90 | 309 |
| go-zero `rest` | 44 | 280 | 507 |

The default project already has the router, middleware, health checks,
request IDs, graceful shutdown, socket activation, the resilient proxy, and
hot reload. Its one module is `github.com/google/flatbuffers`, the hot-reload
wire format. Dev-only tooling (the file watcher) never reaches the binary.

Everything else is opt-in and shows up in `go.mod`:

| You add | Modules linked |
|---|---:|
| `webapp` preset (Templ views) | +1 |
| `--db=sqlite` (pure-Go driver, no CGO) | +10 |
| `metrics`, `openapi`, `mtls`, `grpc` | +0 |
| `jwt-auth` | +1 |
| `otel` | +6 |

Fewer modules means less code to audit, fewer CVE feeds to watch, and less
upgrade churn. Reproduce the table with `make benchmark-surface`.

---

## Run it yourself

From the framework repository:

| Command | What it measures | Needs |
|---|---|---|
| `make benchmark` | Quick latency and throughput suite | [vegeta](https://github.com/tsenart/vegeta) |
| `./benchmarks/run-comparison.sh` | FGOTHS vs stdlib, chi, gin, go-zero | Go |
| `make benchmark-surface` | Dependencies linked into a generated project vs other frameworks | Go |
| `make benchmark-saturation` | Repeated saturation runs, with CPU and heap profiles | vegeta |
| `make benchmark-real-app` | Generated SQLite CRUD app, FGOTHS vs `ServeMux` | vegeta |
| `make check-perf` | Regression guard used before releases | Go |

`make benchmark-saturation` options (environment variables; `make benchmark-real-app` accepts the same ones):

| Variable | Default | Meaning |
|---|---|---|
| `SATURATION_WORKERS` | `150` | Concurrent workers |
| `SATURATION_RUNS` | `5` | Number of measured runs |
| `SATURATION_DURATION` | `15s` | Length of each run |
| `SATURATION_WARMUP_DURATION` | `3s` | Warm-up before measuring |
| `RESULTS_DIR` | temp folder | Keep reports and profiles here |

Profiling runs separately from the clean measurement and listens on
loopback only, so it doesn't skew the numbers.

## Caveats

- Loopback benchmarks measure the local `net/http` and TCP path, not your
  production network, middleware, or database.
- Compare runs on the same machine, with the same Go version, on an idle
  system.
- In-process routing figures move by a few percent between runs. Compare
  medians of several runs (`-count=6`), not single results.
