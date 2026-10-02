# real-app

ssr service generated with FGOTHS.

- Architecture: mvc
- Database: sqlite

## CRUD load-test application

This generated project includes `Product` CRUD routes backed by SQLite. The
benchmark compares the same generated handlers and middleware when registered
on the FGOTHS router and Go's standard-library `ServeMux`; only dispatch changes.
The GET case reads a seeded collection and the POST case creates rows in SQLite.
The generated request logger remains enabled in both cases.

From the repository root, run:

```bash
make benchmark-real-app
```

The runner builds this application and its seed helper, starts with a fresh
SQLite file for every measurement, validates expected HTTP status codes, and
stores raw Vegeta reports and separate FGOTHS CPU/allocation/block profiles in
a temporary directory. Set `RESULTS_DIR` to an empty directory to retain
results at a known location. Control run count, duration, warm-up, workers, and
initial dataset size with `SATURATION_RUNS`, `SATURATION_DURATION`,
`SATURATION_WARMUP_DURATION`, `SATURATION_WORKERS`, and `BENCH_SEED_COUNT`.

This is a local loopback comparison, so it includes client/server contention
and should not be read as production network capacity. For an external-load
test, run the load generator on a separate machine and keep the server and
workload configuration unchanged.

### Recorded comparison

One sustained run in a local test environment used
150 Vegeta workers, a 2-second warm-up, three 10-second measurements per case,
and 100 seeded products. Each measurement used a fresh SQLite database. Both
servers used a 10-second `ReadHeaderTimeout`, the same generated handlers and
logging middleware, and the same route set. All requests returned the expected
status (`200` for GET and `201` for POST).

| Workload | FGOTHS throughput (req/s) | `ServeMux` throughput (req/s) | FGOTHS p99 median | `ServeMux` p99 median |
|---|---:|---:|---:|---:|
| `GET /api/products` | 8,651 (8,567–8,727) | 8,585 (8,426–8,696) | 78.285 ms | 80.232 ms |
| `POST /api/products` | 19,522 (19,313–19,971) | 19,811 (19,682–19,938) | 35.400 ms | 34.921 ms |

The ranges overlap in both workloads, so these three runs do not establish a
material throughput difference between the routers. The slight GET/POST
differences should not be read as a router win or regression. Profiles point
instead to work in the complete HTTP, JSON, and SQLite paths: collection reads
spend allocations in row conversion and response construction, while inserts
include request decoding and SQLite work. This workload does not justify a
router or pooling optimization by itself.

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

MVC and hybrid projects use Templ for server-side rendering.
`make generate` downloads the pinned generator through Go when the `templ`
binary is not installed.

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
make docker-build APP_IMAGE=real-app:dev
docker run --rm -p 8080:8080 --user "$(id -u):$(id -g)" -v "$(pwd)/data:/data" real-app:dev
```

SQLite data is stored under `/data/app.db` inside the container.
`make docker-run` creates and mounts a Docker named volume (`real-app-data:/data`),
so local container restarts keep the database file and WAL sidecars without host
bind-mount permission issues. In production, mount a persistent volume at `/data`
or set `DATABASE_URL` explicitly.

If you intentionally want a local bind mount, prepare permissions first:

```bash
mkdir -p data
chmod 0777 data
docker run --rm -p 8080:8080 --user "$(id -u):$(id -g)" -v "$(pwd)/data:/data" real-app:dev
```


### Container Endpoints

API: http://localhost:8080



Health: http://localhost:8080/health
Readiness: http://localhost:8080/health/ready
