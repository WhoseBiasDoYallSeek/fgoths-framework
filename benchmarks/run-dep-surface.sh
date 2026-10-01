#!/usr/bin/env bash
#
# Measure the supply-chain surface of what actually ships: the main package
# of a freshly generated FGOTHS project, compared with the bare entry point of
# other Go frameworks. Counts come from `go list -deps` on the shipped binary,
# so dev-only tooling (the cmd/dev watcher) is excluded.
#
# Usage:
#   ./benchmarks/run-dep-surface.sh
#
# Requires Go 1.26+ and network access for `go mod tidy` on first run.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/fgoths-dep-surface.XXXXXX")"
trap 'rm -rf "$WORK_DIR"' EXIT

FGOTHS_BIN="$WORK_DIR/fgoths"
(cd "$ROOT_DIR" && go build -o "$FGOTHS_BIN" ./cmd/fgoths)

# surface <label> <dir> <package> [module-prefix-to-ignore]
surface() {
  local label="$1" dir="$2" pkg="$3" self="${4:-}" total pkgs mods
  total=$(cd "$dir" && go list -deps "$pkg" | wc -l | tr -d ' ')
  pkgs=$(cd "$dir" && go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "$pkg" \
    | { if [[ -n "$self" ]]; then grep -v "^$self" || true; else cat; fi; } | grep -c . || true)
  mods=$(cd "$dir" && go list -deps -f '{{if not .Standard}}{{with .Module}}{{.Path}}{{end}}{{end}}' "$pkg" \
    | { if [[ -n "$self" ]]; then grep -v "^$self\$" || true; else cat; fi; } | sort -u | grep -c . || true)
  printf '| %-40s | %8s | %13s | %16s |\n' "$label" "$mods" "$pkgs" "$total"
}

generate() {
  local name="$1"; shift
  (cd "$WORK_DIR" && "$FGOTHS_BIN" init --name="$name" "$@" >/dev/null)
  (cd "$WORK_DIR/$name" && go mod tidy >/dev/null 2>&1)
}

generate minapi --preset=api --db=none
generate fullapi --preset=api --db=none --features=metrics,openapi,jwt-auth,mtls,otel,grpc

printf '| %-40s | %8s | %13s | %16s |\n' "Shipped binary" "Modules" "External pkgs" "Total packages"
printf '|%s|%s|%s|%s|\n' "$(printf -- '-%.0s' {1..42})" "---------:" "--------------:" "-----------------:"
surface "stdlib net/http (baseline)" "$ROOT_DIR" "net/http"
surface "FGOTHS api project (default)" "$WORK_DIR/minapi" "." "minapi"
surface "chi" "$ROOT_DIR/benchmarks/comparison" "github.com/go-chi/chi/v5"
surface "FGOTHS api project (every feature)" "$WORK_DIR/fullapi" "." "fullapi"
surface "gin" "$ROOT_DIR/benchmarks/comparison" "github.com/gin-gonic/gin"
surface "go-zero rest" "$ROOT_DIR/benchmarks/comparison" "github.com/zeromicro/go-zero/rest"

echo
echo "External modules linked into the default FGOTHS api binary:"
(cd "$WORK_DIR/minapi" && go list -deps -f '{{if not .Standard}}{{with .Module}}{{.Path}}{{end}}{{end}}' . \
  | grep -v '^minapi$' | sort -u | sed 's/^/  - /')
