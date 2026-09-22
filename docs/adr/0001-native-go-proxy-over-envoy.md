
# ADR 0001: Use Native Embedded Go Reverse Proxy Instead of C++ Envoy

* **Status:** Accepted
* **Date:** 2026-08-31
* **Context:** The original architectural vision proposed embedding C++ Envoy dynamic routing directly inside the Go runtime to eliminate external YAML configs.

---

## Decision
We decided to replace the embedded C++ Envoy dependency with an internal, native Go proxy implementation using the `net/http` standard library and dynamic route engines.

---

## Rationale
1. **CGO Constraints:** Embedding Envoy natively requires Cgo binding (`libenvoy`), which breaks `CGO_ENABLED=0` static linking. This prevents deploying the framework within a pure `scratch` minimal Docker container.
2. **Cross-Compilation Simplicity:** A pure Go proxy enables seamless multi-architecture binary creation (`GOOS=linux`, `GOARCH=amd64/arm64`) via the FGOTHS CLI without requiring C cross-compilers.
3. **Footprint Reduction:** Using Go native abstractions cuts the final binary size significantly while preserving sub-millisecond route dispatch times.

---

## Consequences
* **Positive:** Zero Cgo dependencies, ultra-fast compilation, reduced binary size (<30MB), and direct access to Go's standard networking stack.
* **Negative:** Loss of advanced enterprise Envoy features (e.g., complex gRPC filter chains), which can be implemented as native Go middleware if needed.
