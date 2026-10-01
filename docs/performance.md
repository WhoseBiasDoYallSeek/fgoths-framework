# Performance

**Short version:** FGOTHS is fast enough that the framework is never your
bottleneck. Under load, the cost goes to the operating system's network
stack, JSON, and your database, not to routing. The real gains are a small
dependency surface and a small binary.

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

Routing measured in-process (no network), in ns per request:

| | FGOTHS | stdlib | gin | chi | go-zero |
|---|---:|---:|---:|---:|---:|
| Path param | 366 | 377 | 359 | 494 | 543 |
| Static path | 314 | 341 | 357 | 411 | 363 |

Over real TCP, all of them converge to about **28 µs**: the network
dominates. Full methodology is in [COMPARISON.md](../COMPARISON.md).

## Dependency surface

What `go list -deps` pulls into a minimal service:

| | Packages | External modules |
|---|---:|---:|
| Go stdlib only | 187 | 0 |
| **FGOTHS** | **223** | **5** |
| chi | 190 | 1 |
| gin | 309 | 90 |
| go-zero | 507 | 280 |

Fewer modules means less code to audit and fewer supply-chain entry points.
This is FGOTHS's main advantage, more than raw speed.

---

## Run it yourself

From the framework repository:

| Command | What it measures | Needs |
|---|---|---|
| `make benchmark` | Quick latency and throughput suite | [vegeta](https://github.com/tsenart/vegeta) |
| `./benchmarks/run-comparison.sh` | FGOTHS vs stdlib, chi, gin, go-zero | Go |
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
- In-process routing figures were recorded before the v1.1.0 allocation
  pass. After it, param routes cost ~13 allocs (~500 ns) and static routes
  ~10 allocs (~360 ns). Allocations went down; timings vary slightly between
  runs.
