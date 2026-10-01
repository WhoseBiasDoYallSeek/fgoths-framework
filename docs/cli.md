# CLI Reference

All commands are subcommands of the `fgoths` binary. Build it once from the
framework repository with `make build`. It is written to `bin/fgoths`.

```
fgoths <command> [options]
```

| Command | What it does | Where to run it |
|---|---|---|
| [`init`](#init) | Create a new project | Anywhere |
| [`presets`](#presets) | Show presets and opt-in features | Anywhere |
| [`generate`](#generate) | Compile `.templ` views | Inside a webapp project |
| [`generate crud`](#generate-crud) | Scaffold a resource (model, handlers, migration, routes) | Inside a webapp project |
| [`dev`](#dev) | Hot-reload loop | Inside a project |
| [`build`](#build) | Static production binary | Inside a project |
| [`routes`](#routes) | List registered HTTP routes | Inside a project |
| [`upgrade`](#upgrade) | Update a project's embedded runtime | Anywhere (`--dir`) |
| [`controlplane`](#controlplane) | Run the governance API | Anywhere |
| `version` | Print version, commit, build date, and Go version | Anywhere |

---

## init

```bash
fgoths init --name=orders --preset=api
fgoths init --name=shop --preset=webapp --features=metrics,openapi
fgoths init --name=billing --preset=api --db=postgres --features=jwt-auth,otel
```

| Flag | Meaning | Default |
|---|---|---|
| `--name` | Project name, which is also its folder name | required |
| `--preset` | `api` or `webapp` | required |
| `--db` | `none`, `sqlite`, `postgres`, or `mysql` | the preset's default |
| `--features` | Comma-separated extras, [listed below](#features) | the preset's defaults |
| `--dir` | Parent folder to create the project in | current folder |

`init` never overwrites an existing folder.

### Presets

| Preset | You get | Default database |
|---|---|---|
| `api` | A flat JSON service with health checks and the reverse proxy | none |
| `webapp` | MVC with server-rendered Templ views, HTMX, and the reverse proxy | SQLite |

**Picking one:** JSON or service-to-service traffic → `api`. HTML pages →
`webapp`. Both in one binary → `webapp`, because its handlers can serve JSON
too.

### Features

Add any of these with `--features=a,b,c`:

| Feature | Adds | Extra dependency |
|---|---|---|
| `health` | `/health`, `/health/live`, `/health/ready` | — |
| `metrics` | Prometheus-compatible `/metrics` | — |
| `openapi` | `/docs` and `/openapi.yaml` | — |
| `grpc` | gRPC-style JSON API on `:9090` (api preset) | — |
| `flatbuffers` | Zero-copy binary serialization | — |
| `htmx` | Server-driven interactivity, on by default in `webapp` | — |
| `jwt-auth` | `RequireJWT` middleware with roles, scopes, and claims. Fails closed without a secret. | `golang-jwt/jwt/v5` |
| `mtls` | Mutual TLS and client-identity routing (CN, SAN, SPIFFE) | — (stdlib) |
| `otel` | OpenTelemetry tracing for the server and proxy hops | `go.opentelemetry.io/otel` |
| `ci-cd` | GitHub Actions and GitLab CI pipelines | — |

### Databases

| Value | Driver | Configure with |
|---|---|---|
| `sqlite` | Pure-Go SQLite, no CGO | `DATABASE_URL` (defaults to `app.db`) |
| `postgres` | `jackc/pgx/v5` | `DATABASE_URL` |
| `mysql` | `go-sql-driver/mysql` | `DATABASE_URL` |

Migrations are embedded in the binary and run at startup.

---

## presets

```bash
fgoths presets
```

Prints both presets, their defaults, and every opt-in feature.

---

## generate

```bash
fgoths generate          # compile views/*.templ once
fgoths generate -watch   # recompile on change
```

Generated projects run this for you inside `make generate`, `make run`,
`make dev`, and `make build`.

## generate crud

```bash
fgoths generate crud Product name:string price:float64 in_stock:bool
```

Creates a model, handlers, a migration, and the
`RegisterCRUDRoutes(registrar, db)` wiring in `handlers/routes_gen.go`.
Restart the app and `GET`/`POST /api/products` are live.

Field types: `string`, `int`, `int64`, `float64`, `bool`. Every row also gets `id` and `created_at`.

---

## dev

```bash
fgoths dev    # same as: make dev
```

Rebuilds in the background and swaps processes without dropping
connections. When `.templ`, CSS, JS, or HTML files change, the browser
updates the page fragment in place (HMR) instead of doing a full reload.

> Dev mode is tested on macOS and Linux.

---

## build

```bash
fgoths build                     # → bin/app
fgoths build --out=dist/orders
fgoths build --scratch --sbom    # also writes a scratch Dockerfile and sbom.json
```

| Flag | Meaning |
|---|---|
| `--out` | Output path. Default: `bin/app` |
| `--scratch` | Also write a `FROM scratch` Dockerfile |
| `--sbom` | Also write a Software Bill of Materials (`sbom.json`) |

Binaries are static (`CGO_ENABLED=0`), stripped, and built with `-trimpath`.

---

## routes

```bash
fgoths routes
```

Scans the current folder and prints every registered route:

```
METHOD  PATH                SOURCE
GET     /api/products       handlers/product_handler.go:22
POST    /api/products       handlers/product_handler.go:30
```

---

## upgrade

```bash
fgoths upgrade --dir=../my-service             # preview
fgoths upgrade --dir=../my-service --apply     # write
fgoths upgrade --from=1.1.0 --dir=../old-site  # projects created before v1.3.0
```

| Flag | Meaning | Default |
|---|---|---|
| `--dir` | Project to upgrade | `.` |
| `--from` | Version the project was created with. Only needed for projects from v1.1.0 or v1.2.0. | read from `.fgoths/upgrade.json` |
| `--apply` | Actually write changes | off (preview only) |

Full guide: [Upgrading a project](./upgrading.md).

---

## controlplane

Runs the standalone governance API: routes, upstreams, a deployment ledger,
and release approvals.

```bash
FGOTHS_CP_TOKEN=change-me fgoths controlplane --addr=:9091 --store=ledger.json
```

| Flag | Meaning | Default |
|---|---|---|
| `--addr` | Listen address | `:9091` |
| `--store` | JSON file path, or `sqlite://file.db`\* | `controlplane-ledger.json` |
| `--token` | Admin bearer token (or `FGOTHS_CP_TOKEN`) | required |

\* The SQLite store needs a CLI built with `go build -tags sqlite ./cmd/fgoths`.

Without a token the API refuses to start: it **fails closed**. Every
endpoint lives under `/api/v1/` and needs `Authorization: Bearer <token>`.
The only public endpoint is `/healthz`.

What it governs:

- **Routes and policies**, versioned, with actor, reason, and diff, plus
  rollback.
- **Releases** with multi-approver gates and approve, rollback, and reject
  actions.
- **The deployment ledger**, which survives restarts.
- **Progressive delivery and failover**: canary weights, staged rollouts,
  and health-aware region selection.

---

## Generated project Makefile

Every project ships with the same targets:

| Target | What it does |
|---|---|
| `make generate` | Tidy modules and compile views and assets. **Run this first** in a fresh checkout. |
| `make run` | Generate, then run the app on `$PORT` (default `8080`) |
| `make dev` | Hot-reload loop with HMR |
| `make test` | `go test -v ./...` |
| `make build` | Static Linux binary at `bin/app` |
| `make docker-build` / `make docker-run` | Multi-stage build into a `scratch` image |
| `make clean` | Remove build output |

---

## Maintainers only

`fgoths sync-templates [--check]` copies `pkg/runtime/` into the generator
templates, so new projects receive the current runtime. `make check-templates` runs it with
`--check` to catch drift. See [CONTRIBUTING](../CONTRIBUTING.md).
