#!/usr/bin/env bash
#
# run-benchmarks.sh — full performance validation suite for FGOTHS.
#
# Runs, in order:
#   1. Go benchmark comparison: FGOTHS vs stdlib (1.22+ patterns) vs chi vs
#      gin vs go-zero (route dispatch, in-process + real TCP) + dependency surface
#   2. Runtime percentile benchmarks: router + proxy latency distributions
#   3. Vegeta stress tests: fixed-rate steps + saturation against a built server
#   4. Network tail-latency guard (p99/p99.9 over real TCP)
#   5. Edge case suite (path traversal, CRLF, large bodies, concurrency)
#
# Usage:
#   ./benchmarks/run-benchmarks.sh [--quick]
#
#   --quick  shorter durations, fewer scenarios (for CI or a fast sanity pass)
#
# Requirements: go 1.26+, vegeta (brew install vegeta). Optional: node (skipped if absent).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

QUICK=false
[[ "${1:-}" == "--quick" ]] && QUICK=true

BENCH_TIME="3s"
VEGETA_DURATION="10s"
SATURATION_DURATION="15s"
if $QUICK; then
  BENCH_TIME="1s"
  VEGETA_DURATION="5s"
  SATURATION_DURATION="5s"
fi

BENCH_SERVER_BIN="$(mktemp -d)/fgoths-bench-server"
BENCH_PORT="18080"
RESULTS_DIR="$(mktemp -d)"
echo "📁 Results directory: $RESULTS_DIR"

cleanup() {
  if [[ -n "${SERVER_PID:-}" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
  fi
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

echo "🧪 FGOTHS Benchmark Suite ($(date '+%Y-%m-%d %H:%M'))"
echo "   Machine: $(uname -sm), $(sysctl -n hw.ncpu 2>/dev/null || nproc) cores"

# ---------------------------------------------------------------------------
section "1/5  Route dispatch comparison: FGOTHS vs stdlib (1.22+ patterns) vs chi vs gin vs go-zero"
# ---------------------------------------------------------------------------
cd "$ROOT_DIR/benchmarks/comparison"
go test -bench . -benchmem -benchtime="$BENCH_TIME" -run '^$' | tee "$RESULTS_DIR/1-comparison.txt"

# Dependency surface: total packages pulled in per framework (the
# batteries-included tax). stdlib is the zero-dependency baseline.
dep_surface() {
  local label="$1" pkg="$2" total ext
  total=$(go list -deps "$pkg" | wc -l | tr -d ' ')
  ext=$(go list -deps -f '{{if not .Standard}}{{.ImportPath}}{{end}}' "$pkg" | grep -c . || true)
  printf '  %-34s %5s packages total (%s non-stdlib)\n' "$label" "$total" "$ext"
}
echo
echo "Dependency surface (go list -deps, packages pulled in):"
dep_surface "stdlib net/http (baseline)" "net/http"
dep_surface "FGOTHS pkg/runtime" "github.com/WhoseBiasDoYallSeek/fgoths-framework/pkg/runtime"
dep_surface "chi" "github.com/go-chi/chi/v5"
dep_surface "gin" "github.com/gin-gonic/gin"
dep_surface "go-zero rest" "github.com/zeromicro/go-zero/rest"

# ---------------------------------------------------------------------------
section "2/5  Runtime percentile benchmarks (router + proxy)"
# ---------------------------------------------------------------------------
cd "$ROOT_DIR"
go test ./pkg/runtime/ -bench 'Percentiles' -benchtime="$BENCH_TIME" -run '^$' | tee "$RESULTS_DIR/2-percentiles.txt"

# ---------------------------------------------------------------------------
section "3/5  Vegeta stress tests"
# ---------------------------------------------------------------------------
(cd "$ROOT_DIR/benchmarks/fgoths" && go build -o "$BENCH_SERVER_BIN" .)
PORT="$BENCH_PORT" "$BENCH_SERVER_BIN" &
SERVER_PID=$!
sleep 1

# Identity check, not just connectivity: another process (e.g. a dev server)
# may already own the port and answer /health with a 200.
BENCH_HEALTH="$(curl -sf "http://localhost:$BENCH_PORT/health" 2>/dev/null || true)"
if [[ "$BENCH_HEALTH" != '{"status":"UP"}' ]]; then
  echo "❌ benchmark server failed to start on :$BENCH_PORT (last body: ${BENCH_HEALTH:-<none>})" >&2
  echo "   Tip: is another process already listening on :$BENCH_PORT?" >&2
  exit 1
fi
echo "✅ benchmark server running on :$BENCH_PORT (pid $SERVER_PID)"

STEPS=(500 2000 5000)
$QUICK && STEPS=(500 2000)

for rate in "${STEPS[@]}"; do
  echo
  echo "--- Fixed rate: ${rate} req/s for ${VEGETA_DURATION} ---"
  echo "GET http://localhost:$BENCH_PORT/health" \
    | vegeta attack -duration="$VEGETA_DURATION" -rate="$rate" \
    | tee "$RESULTS_DIR/3-rate-${rate}.bin" | vegeta report -type=text
done

echo
echo "--- Saturation: max workers for ${SATURATION_DURATION} ---"
echo "GET http://localhost:$BENCH_PORT/health" \
  | vegeta attack -duration="$SATURATION_DURATION" -rate=0 -max-workers=100 \
  | tee "$RESULTS_DIR/3-saturation.bin" | vegeta report -type=text

kill "$SERVER_PID" 2>/dev/null || true
wait "$SERVER_PID" 2>/dev/null || true
SERVER_PID=""

# ---------------------------------------------------------------------------
section "4/5  Network tail-latency guard (p99 / p99.9 over real TCP)"
# ---------------------------------------------------------------------------
go test ./pkg/runtime/ -run 'NetworkLatencyPercentilesGuard' -count=1 -v 2>&1 \
  | grep -E 'samples=|PASS|FAIL|exceeds' | tee "$RESULTS_DIR/4-tail-latency.txt"

# ---------------------------------------------------------------------------
section "5/5  Edge case suite"
# ---------------------------------------------------------------------------
go test -race ./pkg/runtime/ -run 'EdgeCases' -count=1 -v 2>&1 \
  | grep -E '^(--- |ok|FAIL)' | tee "$RESULTS_DIR/5-edge-cases.txt"

# ---------------------------------------------------------------------------
section "Summary"
# ---------------------------------------------------------------------------
echo "All raw outputs saved in: $RESULTS_DIR"
echo
echo "Key files:"
echo "  1-comparison.txt      — framework comparison table + dependency surface"
echo "  2-percentiles.txt     — router/proxy latency distributions"
echo "  3-rate-*.bin          — vegeta raw results (vegeta report -from 3-rate-500.bin)"
echo "  4-tail-latency.txt    — p99/p99.9 over real TCP"
echo "  5-edge-cases.txt      — security/robustness suite"
echo
echo "✅ Benchmark suite complete."
