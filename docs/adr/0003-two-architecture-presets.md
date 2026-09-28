# ADR 0003: Exactly Two Architecture Presets (Flat API and MVC)

* **Status:** Accepted
* **Date:** 2026-09-22
* **Context:** Scaffolding tools often offer many layout options (clean architecture, hexagonal, DDD layers, micro-kernel...). Each additional preset multiplies the template matrix that must be tested, documented and kept working across every database and feature combination.

---

## Decision
FGOTHS generates projects in exactly **two** structural patterns:

1. **Flat API** (default for `--preset=api`): `main.go` + `handlers/` + optional `internal/` feature packages. Zero ceremony; readable in a minute.
2. **MVC** (the `--preset=webapp`): `handlers/` (controllers), `models/`, `views/` (Templ + HTMX SSR), `middleware/`, `internal/database/`.

Everything else is an **opt-in feature** (metrics, openapi, grpc, jwt-auth, mtls, otel, flatbuffers, database), not a new layout.

---

## Rationale
1. **Depth over breadth:** two layouts can be tuned end-to-end — scaffolding, tests, `FROM scratch` container, docs. Five layouts would each be half-polished.
2. **Test matrix control:** the CI validates every preset × database × feature combination that exists. Adding a third architecture would square the maintenance cost.
3. **The two patterns cover the real split:** JSON APIs/services (flat) and server-rendered apps (MVC). Hybrid needs are served by MVC, whose handlers also serve JSON.
4. **Escape hatch:** users who need a different layout can restructure after generation — the generated code is plain Go with no framework magic binding it to the scaffold.

---

## Consequences
* **Positive:** every generated project is polished and fully tested; smaller template surface; simpler docs and onboarding.
* **Negative:** users wanting clean/hexagonal scaffolding out of the box must restructure manually. This is a deliberate trade: FGOTHS optimizes for the 80% case and stays out of the way for the rest.
