# Contributing to FGOTHS

Thank you for your interest in contributing to FGOTHS! We welcome pull
requests, bug reports, feature proposals, and documentation improvements.

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

# Run tests
go test ./...

# Build CLI locally
make build
./bin/fgoths version
```

---

## Validation

```bash
go test ./...                      # full functional and generated-project tests
make test                          # full suite under the race detector
make cover                         # short-mode statement coverage report
go build ./...
go vet ./...
make check-templates
```

`make cover` uses `-short` because the CLI suite has subprocess helpers that
cannot reliably participate in Go's coverage collection. The full suite,
including those helpers, still runs under the race detector with `make test`.
The short-mode report must reach 100% repository-wide statement coverage;
`make cover` fails if the target is missed. This measures statement coverage,
not branch coverage.

`golangci-lint run ./...` is recommended when golangci-lint is installed.
Run `make check-perf` when changing routing, proxying, or allocation-sensitive
runtime code. Docker builds are not part of the default test suite; validate
them separately when changing container generation.

There is no GitHub Actions workflow in this repository. These commands are
local validation guidance; any external CI must invoke the relevant checks
explicitly.

---

## Project-specific rules

### Runtime template synchronization (ADR 0002)

Generated projects contain selected runtime files, not a copy of every file
in `pkg/runtime`. `sync-templates` maintains the explicitly managed core and
feature file pairs; generated-project integration tests compile representative
outputs. If you change a managed runtime source or template:

```bash
go run ./cmd/fgoths sync-templates
make check-templates
go test ./pkg/runtime ./internal/cli ./internal/generator
```

Feature-scoped runtime files are kept in their corresponding feature
templates. Update the committed example copies when their generated behavior
changes, then run the example test suites:

```bash
(cd examples/api-demo && go test ./...)
(cd examples/webapp-demo && go test ./...)
```

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
2. Run the applicable validation commands above.
3. Update `CHANGELOG.md` under `[Unreleased]` (Added/Changed/Fixed/Removed).
4. Open the PR describing the *why*, not just the *what*.

---

## Versioning & releases

FGOTHS follows [Semantic Versioning](https://semver.org). The version is
stamped into the CLI at build time (`VERSION ?= x.y.z` in the `Makefile`;
check it with `./bin/fgoths version`).

Every release must keep `fgoths upgrade` working for projects created with
the previous release. To cut `vX.Y.Z`:

1. **Bump the version** in the `Makefile` (`VERSION`) and in
   `internal/cli/upgrade_baseline.go` (`latestRuntimeUpgradeVersion`).
2. **Freeze the previous runtime as an upgrade baseline**, so its projects
   can be three-way merged:
   - Copy the runtime templates as they were at the previous tag, for
     example `git show vPREV:internal/generator/templates/base/pkg/runtime/server.go.tpl`,
     into `internal/cli/upgrade/baselines/vPREV/pkg/runtime/<file>.go.txt`
     (same file set as the existing baselines).
   - Add `vPREV` to the `//go:embed` line and to
     `supportedRuntimeUpgradeSources`, and update the supported-sources
     wording in the error message and in [docs/upgrading.md](./docs/upgrading.md).
3. **Validate:** `make test`, `make cover`, `make check-templates`,
   `make check-perf`, then try upgrading a project generated with the
   previous release.
4. **Release:** move `[Unreleased]` to `[X.Y.Z] - YYYY-MM-DD` in the
   CHANGELOG, update [SECURITY.md](./SECURITY.md) supported versions,
   commit, then:

   ```bash
   git tag -a vX.Y.Z -m "vX.Y.Z"
   git push origin main vX.Y.Z
   gh release create vX.Y.Z --latest --title "vX.Y.Z" --notes-file <notes>
   ```

---

## Reporting bugs

Open an issue with: Go version (`go version`), OS, minimal reproduction, and
expected vs actual behavior. For security-sensitive reports, see
[SECURITY.md](./SECURITY.md).
