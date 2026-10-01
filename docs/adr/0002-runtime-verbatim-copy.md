# ADR 0002: Embed Selected Runtime Sources in Generated Projects

* **Status:** Accepted
* **Date:** 2026-09-22
* **Context:** Generated projects need FGOTHS runtime behavior. The two
  options were (a) depending on the framework as a Go module, or (b) embedding
  the source needed by the selected preset and features.

---

## Decision
Generated projects include the selected runtime sources under their own
`pkg/runtime/` directory. They do not receive every file in the framework's
runtime package and do not depend on the framework module. The repository
maintains an explicit set of managed source/template pairs and verifies them
with `make check-templates` and `TestRuntimeTemplatesInSync`. This repository
does not currently commit a CI workflow; external CI can run the same check.

---

## Rationale
1. **No automatic version skew:** framework releases do not silently change an existing generated project. The project keeps the selected source until its owner updates it.
2. **No framework module dependency:** generated projects do not add this repository as a Go module dependency. Optional features may add their own external packages.
3. **Auditability:** teams in regulated industries can read, diff and pin the exact runtime code shipped in their binary without resolving a module graph.
4. **Verifiable source:** managed source/template pairs are checked for drift, while generated-project integration tests compile representative outputs. The integration matrix is not exhaustive.

---

## Consequences
* **Positive:** deterministic builds, no module resolution at generation time, trivially auditable runtime, no breaking-change propagation to existing projects.
* **Negative:** runtime fixes do not automatically reach generated projects. There is no conflict-aware upgrade command; owners must regenerate or manually port desired changes.
* **Mitigation:** the framework-side `sync-templates --check` reports drift in managed pairs. It is a repository maintenance command, not a downstream-project upgrader.
