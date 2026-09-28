# ADR 0002: Ship the Runtime as a Verbatim Copy in Generated Projects

* **Status:** Accepted
* **Date:** 2026-09-22
* **Context:** Generated projects need the FGOTHS runtime (`pkg/runtime`). The two obvious options were (a) depending on the framework as a Go module, or (b) copying the runtime source into each generated project.

---

## Decision
Every generated project receives a **verbatim copy** of `pkg/runtime` under its own `pkg/runtime/` directory. The framework repo enforces fidelity with `fgoths sync-templates --check`, which runs in CI and fails if the embedded templates drift from the tested source.

---

## Rationale
1. **No version skew:** a generated project never breaks because the framework published a new release. The code you test today is the code the project runs forever, until the owner explicitly re-syncs.
2. **Zero dependency surface:** generated projects do not add the framework module to `go.mod`. The runtime's 5 non-stdlib packages (jwt, otel, go-logr — only when features are selected) are the *only* external surface.
3. **Auditability:** teams in regulated industries can read, diff and pin the exact runtime code shipped in their binary without resolving a module graph.
4. **Single test suite:** the framework's `go test -race -cover ./...` validates the same bytes that land in generated projects — there is no "tested here, runs differently there" gap.

---

## Consequences
* **Positive:** deterministic builds, no module resolution at generation time, trivially auditable runtime, no breaking-change propagation to existing projects.
* **Negative:** runtime fixes do not automatically reach generated projects; users must re-run `fgoths sync-templates` (or regenerate) to pick up fixes. The `--check` mode in CI keeps the framework side honest, but downstream projects are on the user to refresh.
* **Mitigation:** the CLI prints the runtime version (`fgoths version`) and `sync-templates --check` reports drift, making staleness visible.
