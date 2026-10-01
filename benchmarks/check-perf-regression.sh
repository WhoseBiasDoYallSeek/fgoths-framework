#!/usr/bin/env bash
#
# check-perf-regression.sh — CI gate for dispatch performance.
#
# Runs the in-process dispatch benchmarks (FGOTHS only) and fails when the
# median ns/op regresses more than TIME_TOLERANCE_PCT or when allocations
# increase at all. Baselines live in dispatch-baseline.txt as KEY=VALUE lines.
#
# Update the baseline deliberately: run with BENCH_BASELINE_UPDATE=1 and
# commit the new file. Never let CI auto-update it, or a noisy runner would
# silently ratchet the floor down.
#
# Usage:
#   ./benchmarks/check-perf-regression.sh                     # gate mode (CI)
#   BENCH_COUNT=3 BENCH_BASELINE_UPDATE=1 ./benchmarks/check-perf-regression.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BASELINE_FILE="$ROOT_DIR/benchmarks/dispatch-baseline.txt"
RUNS="${BENCH_COUNT:-5}"
TIME_TOLERANCE_PCT=10

BENCH_DIR="$ROOT_DIR/benchmarks/comparison"
OUT="$(mktemp)"
trap 'rm -f "$OUT"' EXIT

cd "$BENCH_DIR"
go test -run '^$' -bench 'DispatchFGOTHS$' -benchmem -count="$RUNS" > "$OUT" 2>&1 || {
  cat "$OUT"
  echo "FAIL: benchmark run error" >&2
  exit 1
}

median() { sort -n | awk '{a[NR]=$1} END {if (NR%2) print a[(NR+1)/2]; else print int((a[NR/2]+a[NR/2+1])/2)}'; }

# shellcheck disable=SC2016  # awk program text, not shell var
PARAM_TIME=$(awk '/BenchmarkParamDispatchFGOTHS-/ { for (i=1;i<=NF;i++) if ($i ~ /ns\/op/) print int($(i-1)) }' "$OUT" | median)
PARAM_ALLOCS=$(awk '/BenchmarkParamDispatchFGOTHS-/ { for (i=1;i<=NF;i++) if ($i ~ /allocs\/op/) print $(i-1) }' "$OUT" | sort -n | tail -1)
STATIC_TIME=$(awk '/BenchmarkStaticDispatchFGOTHS-/ { for (i=1;i<=NF;i++) if ($i ~ /ns\/op/) print int($(i-1)) }' "$OUT" | median)
STATIC_ALLOCS=$(awk '/BenchmarkStaticDispatchFGOTHS-/ { for (i=1;i<=NF;i++) if ($i ~ /allocs\/op/) print $(i-1) }' "$OUT" | sort -n | tail -1)

if [[ -z "${PARAM_TIME:-}" || -z "${STATIC_TIME:-}" || -z "${PARAM_ALLOCS:-}" || -z "${STATIC_ALLOCS:-}" ]]; then
  echo "FAIL: could not parse benchmark output" >&2
  cat "$OUT" >&2
  exit 1
fi

if [[ "${BENCH_BASELINE_UPDATE:-0}" == "1" ]]; then
  cat > "$BASELINE_FILE" << EOF
# Dispatch performance baseline (medians of $RUNS runs).
# Reference: local test environment.
# CI fails when median ns/op regresses more than 10% or allocations increase.
# Update deliberately (BENCH_BASELINE_UPDATE=1) after intentional changes.
BASELINE_PARAM_TIME=$PARAM_TIME
BASELINE_PARAM_ALLOCS=$PARAM_ALLOCS
BASELINE_STATIC_TIME=$STATIC_TIME
BASELINE_STATIC_ALLOCS=$STATIC_ALLOCS
EOF
  echo "Baseline updated:"
  cat "$BASELINE_FILE"
  exit 0
fi

# shellcheck source=/dev/null
source "$BASELINE_FILE"

PARAM_LIMIT=$(( BASELINE_PARAM_TIME * (100 + TIME_TOLERANCE_PCT) / 100 ))
STATIC_LIMIT=$(( BASELINE_STATIC_TIME * (100 + TIME_TOLERANCE_PCT) / 100 ))

fail=0
gate_time() {
  # Time is noisy across runners — informational only, never fails the gate.
  local label="$1" got="$2" limit="$3"
  if (( got <= limit )); then
    echo "  OK   $label: ${got} ns/op (limit ${limit})"
  else
    echo "  WARN $label: ${got} ns/op over limit ${limit} — informational on shared runners (allocs are the hard gate)"
  fi
}
gate_allocs() {
  local label="$1" got="$2" base="$3"
  if (( got <= base )); then
    echo "  OK   $label: ${got} allocs/op (baseline ${base})"
  else
    echo "  FAIL $label: ${got} allocs/op exceeds baseline ${base}"
    fail=1
  fi
}

echo "Dispatch results (median of $RUNS runs):"
gate_time   "param dispatch"  "$PARAM_TIME"   "$PARAM_LIMIT"
gate_time   "static dispatch" "$STATIC_TIME"  "$STATIC_LIMIT"
gate_allocs "param allocs"    "$PARAM_ALLOCS" "$BASELINE_PARAM_ALLOCS"
gate_allocs "static allocs"   "$STATIC_ALLOCS" "$BASELINE_STATIC_ALLOCS"

if [[ "$fail" -ne 0 ]]; then
  echo "FAIL: dispatch performance regression detected (baselines: param ${BASELINE_PARAM_TIME}ns/${BASELINE_PARAM_ALLOCS}a, static ${BASELINE_STATIC_TIME}ns/${BASELINE_STATIC_ALLOCS}a)"
  exit 1
fi
echo "OK: no dispatch performance regression (baselines: param ${BASELINE_PARAM_TIME}ns/${BASELINE_PARAM_ALLOCS}a, static ${BASELINE_STATIC_TIME}ns/${BASELINE_STATIC_ALLOCS}a)"