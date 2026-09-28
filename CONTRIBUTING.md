# Contributing to FGOTHS

Thank you for your interest in contributing to FGOTHS! We welcome pull requests, bug reports, feature proposals, and documentation improvements.

---

## Development Setup

### Prerequisites
* **Go** 1.26 or higher
* **Git**
* **golangci-lint** (optional but recommended: `brew install golangci-lint`)
* **vegeta** (optional, for stress benchmarks: `brew install vegeta`)

### Building from Source
```bash
# Clone repository
git clone https://github.com/WhoseBiasDoYallSeek/fgoths-framework.git
cd fgoths-framework

# Run unit tests
go test ./...

# Build CLI locally
make build
./bin/fgoths version
```

---

## Validation checklist (run before opening a PR)

```bash
go build ./...                          # compiles
go vet ./...                            # static analysis
golangci-lint run ./...                 # lint (config in .golangci.yml)
go test ./...                           # full suite
go test -race ./pkg/runtime/ ./internal/cli/   # race detector on hot packages
go run ./cmd/fgoths sync-templates --check     # template drift guard
./benchmarks/check-perf-regression.sh   # dispatch allocation gate
```

All of these must pass. The template drift guard and the allocation gate are
the two most commonly missed — see below.

---

## Project-specific rules

### Runtime is a verbatim copy (ADR 0002)

`pkg/runtime/*.go` must stay byte-identical to
`internal/generator/templates/base/pkg/runtime/*.go.tpl` (and to the copies in
`examples/*/pkg/runtime/`). If you edit a runtime file:

```bash
cp pkg/runtime/router.go internal/generator/templates/base/pkg/runtime/router.go.tpl
cp pkg/runtime/router.go examples/api-demo/pkg/runtime/router.go
cp pkg/runtime/router.go examples/webapp-demo/pkg/runtime/router.go
go run ./cmd/fgoths sync-templates --check   # must pass
```

Feature-scoped files (`auth.go`, `otel.go`, `tls.go`, `identity.go`) sync to
`internal/generator/templates/features/<feature>/pkg/runtime/`.

### Dispatch allocations are gated

`benchmarks/check-perf-regression.sh` fails when the dispatch benchmark's
allocations/op increase against `benchmarks/dispatch-baseline.txt`. If your
change intentionally adds allocations, re-measure and update the baseline
deliberately:

```bash
BENCH_COUNT=7 BENCH_BASELINE_UPDATE=1 ./benchmarks/check-perf-regression.sh
# then commit the updated dispatch-baseline.txt with a justification
```

Time medians are informational only (runner noise); allocations are the hard
gate because they are deterministic.

### Lint exceptions are inline, not config-wide

`pkg/runtime` is linted in full (gosec + staticcheck). Intentional findings
need a `//nolint:<linter> // justification` on the line — never add a
package-level exclusion to `.golangci.yml`.

---

## Pull requests

1. Fork, create a branch (`feature/amazing-feature`).
2. Run the full validation checklist above.
3. Update `CHANGELOG.md` under `[Unreleased]` (Added/Changed/Fixed/Removed).
4. Open the PR describing the *why*, not just the *what*.

## Reporting bugs

Open an issue with: Go version (`go version`), OS, minimal reproduction, and
expected vs actual behavior. For security-sensitive reports, see
[SECURITY.md](./SECURITY.md).
