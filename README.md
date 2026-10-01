# FGOTHS

**Generate Go services you can read, audit, and ship as a single binary.**

`v1.3.0` · Apache 2.0 · Go 1.26+

FGOTHS is a small CLI that creates ready-to-run Go services: JSON APIs or
server-rendered web apps. Every generated project compiles on day one. It comes
with health checks, graceful shutdown, hot reload, and a container build, and
it carries only the dependencies you ask for.

It is the foundation of our internal application platform: one consistent,
auditable way to start, build, run, and upgrade services. It is deliberately
not a general-purpose "Rails for Go".

```bash
fgoths init --name=orders --preset=api
cd orders && make run          # → http://localhost:8080/health
```

---

## Why FGOTHS

|  |  |
|---|---|
| 🔒 **Small supply chain** | The runtime is built on the Go standard library. A full-featured project pulls in **5** external packages; gin pulls in 90 and go-zero 280. |
| 📦 **One binary** | Static, no CGO, about **6 MB**. Copy it to a server or ship a `FROM scratch` image under 10 MB. |
| 🛡️ **Resilient by default** | Graceful shutdown, request IDs, and a built-in reverse proxy with retries, circuit breaking, and failover. |
| 🧩 **You own the code** | Projects are plain Go modules with no framework import to fight. Upgrade the embedded runtime when you choose, without losing your edits. |
| ⚡ **Fast enough to forget** | About 100k req/s on a laptop. Over a real network FGOTHS performs on par with gin, chi, and the standard library. [See the numbers →](./docs/performance.md) |
| ✅ **Tested** | 100% statement coverage, race-detector clean, plus tests that compile generated projects. |

---

## Get started

**1. Install the CLI**

```bash
git clone https://github.com/WhoseBiasDoYallSeek/fgoths-framework.git
cd fgoths-framework
make build                  # → ./bin/fgoths
./bin/fgoths version
```

Optionally, put `bin/fgoths` on your `PATH`. The examples below assume you did.

**2. Create a service**

```bash
fgoths init --name=orders --preset=api
cd orders
make dev                    # runs and reloads on every save
```

**3. Try it**

```bash
curl localhost:8080/health
```

That's it. The [Getting Started guide](./docs/getting-started.md) walks you
through adding endpoints, a database, pages, and a production build.

---

## Pick a preset

| Preset | Use it when you need… | You get |
|---|---|---|
| **`api`** | A JSON API or service-to-service endpoint | `main.go`, `handlers/`, health checks, runtime |
| **`webapp`** | Server-rendered pages, with JSON too if you want | MVC layout, [Templ](https://templ.guide) views, HTMX, SQLite |

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

---

## Everyday commands

Run these inside a generated project:

| Command | What it does |
|---|---|
| `make dev` | Run with hot reload. The browser refreshes on save. |
| `make test` | Run the project's tests |
| `make build` | Build the static binary into `bin/app` |
| `make docker-build` | Build a `FROM scratch` container image |
| `fgoths generate crud Product name:string price:float64` | Scaffold model, handler, repository, migration, and tests (webapp) |
| `fgoths routes` | List the project's HTTP routes |

Shipping can be as simple as:

```bash
make build && scp bin/app server:/opt/orders/ && ssh server systemctl restart orders
```

---

## Keep projects up to date

Each project carries its own copy of the runtime, so a new FGOTHS version
never changes a project silently. When you want the update, run:

```bash
fgoths upgrade --dir=../orders            # preview (changes nothing)
fgoths upgrade --dir=../orders --apply    # apply, with backups
```

Your local edits are preserved. If a change can't be merged safely, your file
is left untouched and FGOTHS writes a proposal for you to review. Projects
created with v1.1.0 or v1.2.0 add `--from=1.1.0` or `--from=1.2.0`.
→ [Upgrade guide](./docs/upgrading.md)

---

## What's inside

- **Runtime**: router with path parameters, middleware, graceful shutdown,
  request IDs, socket activation, and hot reload in development.
- **Reverse proxy**: connection pooling, retries, circuit breaker, rate
  limiting, and health-checked failover. No Envoy, no CGO.
- **Governance** (optional): a control plane with versioned policies, release
  approvals, a deployment ledger, and canary or staged rollouts. Run it with
  `fgoths controlplane`.

---

## Documentation

| Start here | Go deeper |
|---|---|
| [Getting Started](./docs/getting-started.md): your first API and web app | [Architecture](./ARCHITECTURE.md): runtime contracts and design |
| [CLI Reference](./docs/cli.md): every command, flag, and feature | [Project Layout](./BOILERPLATE.md): generated structure and data flow |
| [Upgrading](./docs/upgrading.md): updating existing projects | [Performance](./docs/performance.md): benchmarks and how to reproduce them |
| [Changelog](./CHANGELOG.md): what changed in each release | [Comparison](./COMPARISON.md): FGOTHS vs gin, chi, go-zero, stdlib |
| | [Decision records](./docs/adr/): why things are the way they are |

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md) and the
[Code of Conduct](./CODE_OF_CONDUCT.md). Report security issues privately as
described in [SECURITY.md](./SECURITY.md).

## License

Apache 2.0. See [LICENSE](./LICENSE).
