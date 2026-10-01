# Getting Started

This guide takes you from zero to a deployed service in about ten minutes.
You'll build a small JSON API, then a web app with a database, and finally a
production binary.

**You need:** Go 1.26+, `make`, and Git. Docker is optional and only needed
for container images.

---

## 1. Install the CLI

```bash
git clone https://github.com/WhoseBiasDoYallSeek/fgoths-framework.git
cd fgoths-framework
make build
./bin/fgoths version        # → 1.3.0
```

To call `fgoths` from anywhere, copy it onto your `PATH`, for example
`cp bin/fgoths /usr/local/bin/`. The rest of this guide assumes you did.

---

## 2. Your first API

```bash
fgoths init --name=orders --preset=api
cd orders
make dev
```

`make dev` starts the server on `:8080` and restarts it every time you save a
`.go` file. In another terminal:

```bash
curl localhost:8080/health          # {"status":"ok"}
curl localhost:8080/status          # {"service":"orders","status":"up"}
```

### What was generated

```
orders/
├── main.go          ← routes live here
├── handlers/        ← your code goes here
├── pkg/runtime/     ← the embedded FGOTHS runtime (you rarely touch it)
├── cmd/dev/         ← the hot-reload watcher used by `make dev`
├── Makefile
└── Dockerfile
```

It is a plain Go module. There is no hidden framework import, and everything
the service does is in this folder.

### Add an endpoint

Create `handlers/orders.go`:

```go
package handlers

import (
	"net/http"

	"orders/pkg/runtime"
)

func GetOrder(w http.ResponseWriter, r *http.Request) {
	id := runtime.PathValue(r, "id")
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "status": "pending"})
}
```

Register it in `main.go`, next to the existing routes:

```go
server.Get("/orders/{id}", handlers.GetOrder)
```

Save, and the server restarts on its own:

```bash
curl localhost:8080/orders/42       # {"id":"42","status":"pending"}
```

The server also has `Post`, `Put`, `Patch`, `Delete`, `Any`, and `Use` (for
middleware). Use `runtime.PathValue` to read `{params}`.

### Test it

```bash
make test                # go test ./...
go test -race ./...      # recommended before shipping
```

The generated `handlers/handlers_test.go` shows the pattern: an
`httptest.Recorder` and a direct handler call, with no server to start.

---

## 3. A web app with a database

```bash
fgoths init --name=shop --preset=webapp
cd shop
make dev                            # → http://localhost:8080
```

The `webapp` preset adds:

- `views/`: [Templ](https://templ.guide) components, compiled to Go, so
  templates are type-checked;
- `models/` and `internal/database/`: SQLite with embedded migrations;
- `static/`: CSS and JS. In development, the browser reloads automatically
  when you save a view, a stylesheet, or a script.

### Scaffold a resource

```bash
fgoths generate crud Product name:string price:float64 in_stock:bool
```

This creates the model, repository, handler, SQL migration, and tests, and
registers `GET` and `POST /api/products` for you:

```bash
curl -X POST localhost:8080/api/products \
  -d '{"name":"Coffee","price":12.5,"in_stock":true}'
curl localhost:8080/api/products
```

Field types: `string`, `bool`, `int`, `int64`, `float64`. Write entity names
in PascalCase and field names in `lower_snake`.

---

## 4. Add features

Pick features when you create a project:

```bash
fgoths init --name=billing --preset=api --db=postgres --features=metrics,openapi,jwt-auth
```

Each feature adds code, and dependencies only when it truly needs them. Run
`fgoths presets` or see the [CLI reference](./cli.md#features) for the full
list.

---

## 5. Ship it

### As a single binary

```bash
make build                          # → bin/app (static Linux binary, ~6 MB)
scp bin/app server:/opt/orders/
ssh server systemctl restart orders
```

The binary has no runtime dependencies: no Go, no libc, and no container
engine on the server.

### As a container

```bash
make docker-build                   # FROM scratch image, under 10 MB
make docker-run
```

### With an SBOM

```bash
fgoths build --scratch --sbom       # binary + Dockerfile + software bill of materials
```

---

## 6. Keep it up to date

New FGOTHS releases never change your project on their own. When you want the
latest runtime fixes, run:

```bash
fgoths upgrade --dir=.              # preview
fgoths upgrade --dir=. --apply      # apply
```

See [Upgrading](./upgrading.md) for the details.

---

## Where next

- [CLI reference](./cli.md): every command and flag
- [Project layout](../BOILERPLATE.md): what each generated file does
- [Architecture](../ARCHITECTURE.md): how the runtime works and which
  contracts it guarantees
- Working examples: [examples/api-demo](../examples/api-demo) and
  [examples/webapp-demo](../examples/webapp-demo)
