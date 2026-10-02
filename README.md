# FGOTHS

**The auditable foundation for resilient Go services.**

<sub>**F**latBuffers · **G**o · **O**rchestration · **T**empl · **H**TMX · **S**QL & scratch containers</sub>

`v1.4.1` · Apache 2.0 · Go 1.26+

FGOTHS generates Go services, JSON APIs or server-rendered web apps, that you
own line by line and can still upgrade. Each one ships as a single static
binary with health checks, graceful shutdown, a resilient reverse proxy, and
hot reload built in, while linking exactly **one** external module.

It is the engine of our internal application platform: every service starts,
builds, ships, and gets audited the same way.

```bash
go install github.com/WhoseBiasDoYallSeek/fgoths-framework/cmd/fgoths@latest

fgoths init --name=orders --preset=api
cd orders && make dev               # reloads on every save

curl localhost:8080/health          # {"status":"ok"}
```

---

## It's just Go

There is no framework to import and nothing hidden. This is the generated
`main.go`, trimmed, with one endpoint added:

```go
func main() {
	server := runtime.NewServer(":8080")

	server.Get("/health", health.Live)
	server.Get("/orders/{id}", handlers.GetOrder)

	log.Fatal(server.ListenAndServe())
}
```

```go
func GetOrder(w http.ResponseWriter, r *http.Request) {
	id := runtime.PathValue(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "pending"})
}
```

Handlers are plain `http.HandlerFunc`s. The runtime behind `runtime.NewServer`
is a few readable files in your project's `pkg/runtime/`, not a dependency.

---

## Why FGOTHS

### 1. You own the code, and you still get upgrades

Generators hand you code and walk away. Frameworks keep updating, but you
can't change them. FGOTHS does both: the runtime lives in your repository,
and `fgoths upgrade` brings in new versions with a three-way merge that keeps
your edits.

```text
$ fgoths upgrade --dir=orders
FGOTHS runtime upgrade plan: v1.3.0 -> v1.4.1
  Current pkg/runtime/server.go
  Merge local changes in pkg/runtime/router.go
  Current pkg/runtime/proxy.go
  Current pkg/runtime/metrics.go
  Current pkg/runtime/hmr/hmr.go
Dry run only. Re-run with --apply to write safe updates.
```

Nothing changes until you pass `--apply`, every file is backed up first, and
a change that can't be merged safely is written as a proposal instead of
touching your code. → [Upgrade guide](./docs/upgrading.md)

### 2. The smallest supply chain you can audit

A default project links **1** external module (`flatbuffers`, used by hot
reload). Bare chi also links 1, gin links **18**, and go-zero links **44**,
and those last two counts are for the framework alone. Every extra module is
opt-in and visible in `go.mod`. [Measured →](./docs/performance.md#dependency-surface)

The tooling is built to be verified too. `fgoths build --sbom` writes a
CycloneDX SBOM of exactly what was linked into your binary.

### 3. Resilience without a sidecar

A built-in reverse proxy handles connection pooling, retries, circuit
breaking, rate limiting, and health-checked failover, without Envoy, a
service mesh, or CGO. When you need governance, `fgoths controlplane` adds
versioned policies, release approvals, a deployment ledger, and canary or
staged rollouts from the same toolchain.

### And it doesn't cost you speed

FGOTHS has the fastest static-route dispatch among stdlib, gin, chi, and
go-zero. It sustains about 100k req/s in a local test environment, matches go-zero under the
same load, and the binary is a ~6 MB static file. The test suite has 100%
statement coverage and runs clean under the race detector.
[See the numbers →](./docs/performance.md)

---

## Install

**With Go** (you need Go 1.26+ to build projects anyway):

```bash
go install github.com/WhoseBiasDoYallSeek/fgoths-framework/cmd/fgoths@latest
fgoths version
```

**From source:** `git clone` this repository and run `make build`. The binary
is written to `./bin/fgoths`.

---

## Pick a preset

| Preset | Use it when you need… | You get |
|---|---|---|
| **`api`** | A JSON API or service-to-service endpoint | `main.go`, `handlers/`, health checks, runtime |
| **`webapp`** | Server-rendered pages, with JSON too if you want | MVC layout, [Templ](https://templ.guide) views, HTMX, SQLite, live view updates |

Not sure? Choose `api` for machines and `webapp` for people.

## Add only what you need

Features are opt-in, so nothing ships unless you ask for it:

```bash
fgoths init --name=billing --preset=api --db=sqlite --features=metrics,openapi,jwt-auth
```

| Feature | Adds |
|---|---|
| `metrics` | Prometheus `/metrics`, zero dependencies |
| `openapi` | `/docs` and `/openapi.yaml` |
| `jwt-auth` | JWT middleware with roles and scopes |
| `mtls` | Mutual TLS and client-identity routing |
| `otel` | OpenTelemetry tracing |
| `grpc` | gRPC-style JSON API on `:9090` |
| `ci-cd` | GitHub Actions and GitLab CI pipelines |

Databases: `sqlite`, `postgres`, `mysql`. Run `fgoths presets` to see
everything.

## Everyday commands

| Command | What it does |
|---|---|
| `make dev` | Run with hot reload. Templ views update in the browser on save, no Node toolchain. |
| `make test` | Run the project's tests |
| `make build` | Build the static binary into `bin/app` |
| `make docker-build` | Build a `FROM scratch` image under 10 MB |
| `fgoths generate crud Product name:string price:float64` | Scaffold model, handler, repository, migration, and tests (webapp) |
| `fgoths routes` | List the project's HTTP routes |
| `fgoths upgrade --dir=. --apply` | Upgrade the embedded runtime and keep your edits |

Shipping can be as simple as:

```bash
make build && scp bin/app server:/opt/orders/ && ssh server systemctl restart orders
```

---

## Documentation

| Start here | Go deeper |
|---|---|
| [Getting Started](./docs/getting-started.md): your first API and web app in ten minutes | [Architecture](./ARCHITECTURE.md): runtime contracts and design |
| [CLI Reference](./docs/cli.md): every command, flag, and feature | [Project Layout](./BOILERPLATE.md): generated structure and data flow |
| [Upgrading](./docs/upgrading.md): updating existing projects | [Performance](./docs/performance.md): benchmarks and how to reproduce them |
| [Changelog](./CHANGELOG.md): what changed in each release | [Comparison](./COMPARISON.md): FGOTHS vs gin, chi, go-zero, stdlib |
| | [Decision records](./docs/adr/): why things are the way they are |

## About the name

**FGOTHS** stands for the stack it is built on: **F**latBuffers, **Go**,
**O**rchestration, **T**empl, **H**TMX, and **S**QL on **S**cratch, a static
binary that runs even in an empty container.

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) and the
[Code of Conduct](./CODE_OF_CONDUCT.md). Report security issues privately as
described in [SECURITY.md](./SECURITY.md).

## License

Apache 2.0. See [LICENSE](./LICENSE).
