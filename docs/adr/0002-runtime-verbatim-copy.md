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
* **Negative:** runtime fixes do not automatically reach generated projects.
* **Mitigation:** release-generated projects record their FGOTHS version and
  selected configuration in `.fgoths/upgrade.json`. `fgoths upgrade` applies
  supported runtime source updates in place, backs up changed files, and
  three-way merges local edits. When changes overlap, the original is
  preserved and a merge candidate is written for review. A dedicated
  `.fgoths/.gitignore` keeps backups and conflict candidates out of Git while
  leaving the upgrade metadata commit-ready. Projects created before metadata
  was added must provide their source version explicitly. Upgrade support is
  versioned and deliberately refuses unknown transitions.
  The framework-side `sync-templates --check` remains a repository
  maintenance command, not a downstream-project upgrader.
