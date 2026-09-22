#!/usr/bin/env bash
#
# run-comparison.sh — end-to-end HTTP comparison against batteries-included
# Go frameworks. Primary matchup: FGOTHS (real runtime router) vs go-zero
# (full rest.Server, middleware chain included).
#
# Both servers expose the same API (/health, /users, /users/<id>, POST /users)
# with identical JSON responses, so throughput differences are attributable to
# the frameworks, not the workload.
#
# Optional: a Node.js baseline runs only if node is installed (legacy baseline,
# kept for continuity — see docs/archive/COMPARISON-nodejs-2026-09.md).
#
# Usage:
#   ./benchmarks/run-comparison.sh [--quick]
#
# Requirements: go 1.26+, vegeta (brew install vegeta). Optional: node.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

QUICK=false
[[ "${1:-}" == "--quick" ]] && QUICK=true

FGOTHS_PORT="18080"
GOZERO_PORT="18081"
NODE_PORT="13000"

VEGETA_DURATION="10s"
SATURATION_DURATION="15s"
RATES=(500 2000 5000)
if $QUICK; then
  VEGETA_DURATION="5s"
  SATURATION_DURATION="5s"
  RATES=(500 2000)
fi

FGOTHS_BIN="$(mktemp -d)/fgoths-bench-server"
GOZERO_BIN="$(mktemp -d)/gozero-bench-server"
FGOTHS_LOG="$(mktemp)"
GOZERO_LOG="$(mktemp)"
NODE_LOG="$(mktemp)"
RESULTS_DIR="$(mktemp -d)"
echo "📁 Results directory: $RESULTS_DIR"

cleanup() {
  for pid in "${FGOTHS_PID:-}" "${GOZERO_PID:-}" "${NODE_PID:-}"; do
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  rm -f "$FGOTHS_LOG" "$GOZERO_LOG" "$NODE_LOG"
}
trap cleanup EXIT

section() { echo; echo "==================================================================="; echo "  $1"; echo "==================================================================="; }

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "❌ '$1' is required but not installed." >&2
    case "$1" in
      vegeta) echo "   Install: brew install vegeta  |  go install github.com/tsenart/vegeta/v12@latest" >&2 ;;
    esac
    exit 1
  fi
}

require go
require vegeta

echo "🧪 FGOTHS vs go-zero — end-to-end comparison ($(date '+%Y-%m-%d %H:%M'))"
echo "   Machine: $(uname -sm), $(sysctl -n hw.ncpu 2>/dev/null || nproc) cores"

# ---------------------------------------------------------------------------
section "Build & start servers"
# ---------------------------------------------------------------------------
(cd "$ROOT_DIR/benchmarks/fgoths" && go build -o "$FGOTHS_BIN" .)
(cd "$ROOT_DIR/benchmarks/comparison" && go build -o "$GOZERO_BIN" ./gozero-server)

PORT="$FGOTHS_PORT" "$FGOTHS_BIN" >"$FGOTHS_LOG" 2>&1 &
FGOTHS_PID=$!
PORT="$GOZERO_PORT" "$GOZERO_BIN" >"$GOZERO_LOG" 2>&1 &
GOZERO_PID=$!

NODE_PID=""
if command -v node >/dev/null 2>&1; then
  (cd "$ROOT_DIR/benchmarks/nodejs" && PORT="$NODE_PORT" node server.js >"$NODE_LOG" 2>&1) &
  NODE_PID=$!
else
  echo "ℹ️  node not found — skipping the optional Node.js baseline"
fi

wait_healthy() {
  local name="$1" port="$2" body
  for _ in $(seq 1 30); do
    # Identity check, not just connectivity: another process (e.g. a dev
    # server) may already own the port and answer /health with a 200.
    body=$(curl -fsS "http://127.0.0.1:${port}/health" 2>/dev/null || true)
    if [[ "$body" == '{"status":"UP"}' ]]; then
      echo "✅ $name ready on :$port"
      return 0
    fi
    sleep 1
  done
  echo "❌ $name failed to start on :$port (last body: ${body:-<none>})" >&2
  echo "   Tip: is another process already listening on :$port?" >&2
  return 1
}

wait_healthy "FGOTHS" "$FGOTHS_PORT"
wait_healthy "go-zero" "$GOZERO_PORT"
if [[ -n "$NODE_PID" ]]; then
  wait_healthy "Node.js" "$NODE_PORT" || { echo "--- NODE LOG ---" >&2; cat "$NODE_LOG" >&2; exit 1; }
fi

# ---------------------------------------------------------------------------
section "Dependency surface (go list -deps, packages pulled in)"
# ---------------------------------------------------------------------------
dep_surface() {
  local label="$1" pkg="$2" dir="$3" total ext
  total=$(cd "$dir" && go list -deps "$pkg" | wc -l | tr -d ' ')
  ext=$(cd "$dir" && go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "$pkg" | grep -c . || true)
  printf '  %-34s %5s packages total (%s non-stdlib)\n' "$label" "$total" "$ext"
}
dep_surface "FGOTHS runtime" "github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime" "$ROOT_DIR"
dep_surface "go-zero rest (full server)" "github.com/zeromicro/go-zero/rest" "$ROOT_DIR/benchmarks/comparison"

# ---------------------------------------------------------------------------
section "Fixed-rate throughput: GET /users/42 (JSON, param route)"
# ---------------------------------------------------------------------------
for rate in "${RATES[@]}"; do
  for name in FGOTHS go-zero; do
    [[ "$name" == "FGOTHS" ]] && port="$FGOTHS_PORT" || port="$GOZERO_PORT"
    echo
    echo "--- $name: ${rate} req/s for ${VEGETA_DURATION} ---"
    echo "GET http://127.0.0.1:${port}/users/42" \
      | vegeta attack -duration="$VEGETA_DURATION" -rate="$rate" \
      | tee "$RESULTS_DIR/users-${name}-${rate}.bin" | vegeta report -type=text
  done
done

# ---------------------------------------------------------------------------
section "Saturation: GET /health (max workers)"
# ---------------------------------------------------------------------------
for name in FGOTHS go-zero; do
  [[ "$name" == "FGOTHS" ]] && port="$FGOTHS_PORT" || port="$GOZERO_PORT"
  echo
  echo "--- $name: saturation for ${SATURATION_DURATION} ---"
  echo "GET http://127.0.0.1:${port}/health" \
    | vegeta attack -duration="$SATURATION_DURATION" -rate=0 -max-workers=100 \
    | tee "$RESULTS_DIR/saturation-${name}.bin" | vegeta report -type=text
done

# ---------------------------------------------------------------------------
section "Summary"
# ---------------------------------------------------------------------------
echo "All raw outputs saved in: $RESULTS_DIR"
echo
echo "Key files:"
echo "  users-<framework>-<rate>.bin   — fixed-rate raw results (vegeta report -from <file>)"
echo "  saturation-<framework>.bin     — saturation raw results"
echo
echo "Reading the results:"
echo "  - In-process dispatch numbers live in benchmarks/run-benchmarks.sh (section 1)."
echo "  - Over real TCP, expect the frameworks to converge: the network stack"
echo "    dominates. The durable differences are dependency surface, footprint"
echo "    and operational model — not ns/op."
echo
echo "✅ Comparison complete."
