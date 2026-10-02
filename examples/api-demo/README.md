# api-demo

api service generated with FGOTHS.

- Architecture: flat
- Database: none

## Run

```bash
make run
```

`make run` (and `make dev` / `make build`) handles first-run setup
automatically: it refreshes `go.sum` (`go mod tidy`) and compiles Templ
templates before starting. **No manual steps needed** — this is the only
command you need after `fgoths init`.

Static asset URLs in Templ layouts use content fingerprints generated from
`static/`. Production responses with a matching fingerprint are cacheable;
development responses disable browser caching and asset changes trigger a
browser reload.

If you prefer raw Go commands over the Makefile, run the setup once first:

```bash
go mod tidy          # populate go.sum
make generate        # compile .templ views (requires the templ toolchain)
go build ./...       # now works
go test ./...
```

For development with hot reload:

```bash
make dev
```

> **Best practice:** prefer `make run` / `make dev` over `go run .` directly.
> `go run` compiles a child binary and does not forward termination signals
> to it — stopping it leaves an orphan process holding port 8080
> (`bind: address already in use`). The Makefile targets manage the whole
> process lifecycle (and `make dev` kills the entire process group on
> restart), so the port is always released cleanly. If it ever happens
> anyway: `lsof -ti :8080 | xargs kill`.



## Docker & Scratch Containers

This project ships with a production-ready multi-stage `Dockerfile` that builds
a static Linux binary and runs it from `scratch` (no shell, package manager or
base OS in the final image). The builder stage uses Docker BuildKit cache mounts
for Go modules and build artifacts; `make docker-build` enables BuildKit for you.

### Install Docker

Install Docker Engine (or any compatible runtime with BuildKit) for your
platform by following the official guide: <https://docs.docker.com/get-docker/>.

After installing, verify the daemon is available:

```bash
docker version
docker info
```

If `make docker-build` prints an error like
`failed to connect to the docker API at unix:///var/run/docker.sock`, the Docker
CLI is installed but the daemon is not running. Start the Docker daemon (or
your container runtime), then retry `make docker-build`.

If Docker reports `BuildKit is enabled but the buildx component is missing or
broken`, install or repair the `docker-buildx` plugin:
<https://docs.docker.com/build/install-buildx/>.

### Build And Run

```bash
make docker-build
make docker-run
```

`make docker-build` runs `make generate` first so `go.sum`, generated Templ code
and module metadata are ready before Docker copies `go.mod`/`go.sum` into the
isolated build layer. The final image is always `FROM scratch`.

The image name defaults to the current directory name:

```bash
make docker-build APP_IMAGE=api-demo:dev
docker run --rm -p 8080:8080 api-demo:dev
```

The final container only needs
the compiled app binary. No writable volume
is required unless your own code writes files.

### Container Endpoints

API: http://localhost:8080

Metrics: http://localhost:8080/metrics


OpenAPI: http://localhost:8080/docs


Health: http://localhost:8080/health
Readiness: http://localhost:8080/health/ready
