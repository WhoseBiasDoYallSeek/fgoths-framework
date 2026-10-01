#!/usr/bin/env bash
#
# Repeatable saturation benchmark with process diagnostics.
#
# Measurement runs keep profiling disabled. Separate diagnostics runs record
# CPU, heap, mutex, block, goroutine and runtime-memory data so profiling
# overhead does not contaminate throughput measurements.
#
# Usage:
#   ./benchmarks/run-saturation.sh
#   SATURATION_RUNS=7 SATURATION_DURATION=20s SATURATION_WORKERS=200 ./benchmarks/run-saturation.sh
#
# Requires Go 1.26+, curl and vegeta.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RESULTS_DIR="${RESULTS_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/fgoths-saturation.XXXXXX")}"
RUNS="${SATURATION_RUNS:-5}"
DURATION="${SATURATION_DURATION:-15s}"
WARMUP_DURATION="${SATURATION_WARMUP_DURATION:-3s}"
WORKERS="${SATURATION_WORKERS:-150}"
PORT="${SATURATION_PORT:-18180}"
PROFILE_PORT="${SATURATION_PROFILE_PORT:-18181}"
BIN="$RESULTS_DIR/fgoths-bench-server"
SERVER_PID=""
PROFILE_PID=""
ATTACK_PID=""

mkdir -p "$RESULTS_DIR"

cleanup() {
  if [[ -n "$ATTACK_PID" ]] && kill -0 "$ATTACK_PID" 2>/dev/null; then
    kill "$ATTACK_PID" 2>/dev/null || true
    wait "$ATTACK_PID" 2>/dev/null || true
  fi
  if [[ -n "$PROFILE_PID" ]] && kill -0 "$PROFILE_PID" 2>/dev/null; then
    kill "$PROFILE_PID" 2>/dev/null || true
    wait "$PROFILE_PID" 2>/dev/null || true
  fi
  if [[ -n "$SERVER_PID" ]] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

require() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required command '$1' was not found." >&2
    exit 1
  fi
}

require go
require curl
require vegeta

if [[ ! "$RUNS" =~ ^[1-9][0-9]*$ || ! "$WORKERS" =~ ^[1-9][0-9]*$ ]]; then
  echo "SATURATION_RUNS and SATURATION_WORKERS must be positive integers." >&2
  exit 1
fi
if [[ ! "$DURATION" =~ ^[1-9][0-9]*s$ || ! "$WARMUP_DURATION" =~ ^[1-9][0-9]*s$ ]]; then
  echo "SATURATION_DURATION and SATURATION_WARMUP_DURATION must be integer seconds followed by 's'." >&2
  exit 1
fi
PROFILE_SECONDS="${DURATION%s}"

cpu_count="$(sysctl -n hw.ncpu 2>/dev/null || getconf _NPROCESSORS_ONLN)"
if [[ -n "$(git -C "$ROOT_DIR" status --porcelain 2>/dev/null)" ]]; then
  git_dirty=true
else
  git_dirty=false
fi
{
  echo "timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  echo "git_commit=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)"
  echo "git_dirty=$git_dirty"
  echo "go_version=$(go version)"
  echo "goos=$(go env GOOS)"
  echo "goarch=$(go env GOARCH)"
  echo "machine=$(uname -sm)"
  echo "logical_cpus=$cpu_count"
  echo "gomaxprocs_setting=${GOMAXPROCS:-default}"
  echo "duration=$DURATION"
  echo "warmup_duration=$WARMUP_DURATION"
  echo "workers=$WORKERS"
  echo "runs=$RUNS"
} | tee "$RESULTS_DIR/metadata.txt"

echo "results_dir=$RESULTS_DIR"
(cd "$ROOT_DIR/benchmarks/fgoths" && go build -o "$BIN" .)

start_server() {
  local profile_mode="$1"
  if [[ "$profile_mode" == "1" ]]; then
    PORT="$PORT" FGOTHS_BENCH_PROFILE=1 FGOTHS_BENCH_PROFILE_PORT="$PROFILE_PORT" "$BIN" \
      >"$RESULTS_DIR/profile-server.log" 2>&1 &
  else
    PORT="$PORT" "$BIN" >"$RESULTS_DIR/server.log" 2>&1 &
  fi
  SERVER_PID=$!

  local attempt
  for attempt in $(seq 1 100); do
    if [[ "$(curl -fsS "http://127.0.0.1:$PORT/health" 2>/dev/null || true)" == '{"status":"UP"}' ]]; then
      if [[ "$profile_mode" != "1" ]] || curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/bench/runtime" >/dev/null 2>&1; then
        return 0
      fi
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      cat "$RESULTS_DIR/profile-server.log" "$RESULTS_DIR/server.log" 2>/dev/null || true
      echo "Benchmark server exited before becoming healthy." >&2
      return 1
    fi
    sleep 0.1
  done
  echo "Timed out waiting for benchmark server on port $PORT." >&2
  return 1
}

stop_server() {
  if [[ -n "$SERVER_PID" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
    SERVER_PID=""
  fi
}

run_attack() {
  local duration="$1"
  local workers="$2"
  local raw="$3"
  echo "GET http://127.0.0.1:$PORT/health" \
    | vegeta attack -duration="$duration" -rate=0 -max-workers="$workers" >"$raw"
}

require_successful_run() {
  local report="$1"
  local failed
  failed="$(awk '
    /^Status Codes/ {
      found = 1
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^[0-9]+:[0-9]+$/) {
          split($i, result, ":")
          if (result[1] != 200) failures += result[2]
        }
      }
    }
    END {
      if (!found) exit 2
      print failures + 0
    }
  ' "$report")" || {
    echo "Could not validate status codes in $report." >&2
    return 1
  }
  if [[ "$failed" -ne 0 ]]; then
    echo "Benchmark run had $failed non-200 responses; refusing to include it in the throughput summary." >&2
    return 1
  fi
}

summarize_runs() {
  local values="$RESULTS_DIR/throughputs.txt"
  local sorted="$RESULTS_DIR/throughputs.sorted.txt"
  local count low high middle median

  : >"$values"
  for report in "$RESULTS_DIR"/measurement-*.txt; do
    [[ -f "$report" ]] || continue
    awk '/^Requests[[:space:]]/ { gsub(/,/, "", $7); print $7; exit }' "$report" >>"$values"
  done
  sort -n "$values" >"$sorted"
  count="$(wc -l <"$sorted" | tr -d ' ')"
  if [[ "$count" -eq 0 ]]; then
    echo "Could not extract throughput values from Vegeta reports." >&2
    return 1
  fi
  low="$(sed -n '1p' "$sorted")"
  high="$(sed -n "${count}p" "$sorted")"
  if (( count % 2 == 1 )); then
    middle=$(((count + 1) / 2))
    median="$(sed -n "${middle}p" "$sorted")"
  else
    middle=$((count / 2))
    median="$(awk -v lo="$middle" -v hi="$((middle + 1))" 'NR == lo || NR == hi { sum += $1 } END { printf "%.2f", sum / 2 }' "$sorted")"
  fi
  printf 'Saturation throughput (requests/s): min=%s median=%s max=%s across %s runs\n' "$low" "$median" "$high" "$count" \
    | tee "$RESULTS_DIR/summary.txt"
}

echo
echo "Starting unprofiled server for clean throughput measurements."
start_server 0
echo "Warm-up: $WARMUP_DURATION at max-workers=$WORKERS"
run_attack "$WARMUP_DURATION" "$WORKERS" "$RESULTS_DIR/warmup.bin"

for run in $(seq 1 "$RUNS"); do
  printf -v run_id '%02d' "$run"
  echo
  echo "--- Measurement $run/$RUNS: duration=$DURATION max-workers=$WORKERS ---"
  run_attack "$DURATION" "$WORKERS" "$RESULTS_DIR/measurement-$run_id.bin"
  vegeta report -type=text <"$RESULTS_DIR/measurement-$run_id.bin" \
    | tee "$RESULTS_DIR/measurement-$run_id.txt"
  require_successful_run "$RESULTS_DIR/measurement-$run_id.txt"
done
stop_server
summarize_runs

echo
echo "Starting a separate block/mutex/heap diagnostics run; its throughput is not included in the summary."
start_server 1
run_attack "$WARMUP_DURATION" "$WORKERS" "$RESULTS_DIR/profile-warmup.bin"
curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/bench/runtime" >"$RESULTS_DIR/runtime-before.json"
actual_gomaxprocs="$(sed -nE 's/.*"gomaxprocs":([0-9]+).*/\1/p' "$RESULTS_DIR/runtime-before.json")"
if [[ ! "$actual_gomaxprocs" =~ ^[1-9][0-9]*$ ]]; then
  echo "Could not read the running server's GOMAXPROCS value." >&2
  exit 1
fi
echo "gomaxprocs_runtime=$actual_gomaxprocs" | tee -a "$RESULTS_DIR/metadata.txt"
run_attack "$DURATION" "$WORKERS" "$RESULTS_DIR/diagnostics-run.bin" &
ATTACK_PID=$!
snapshot_delay=$((PROFILE_SECONDS / 2))
if (( snapshot_delay > 0 )); then
  sleep "$snapshot_delay"
fi
curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/bench/runtime" >"$RESULTS_DIR/runtime-during.json"
wait "$ATTACK_PID"
ATTACK_PID=""
vegeta report -type=text <"$RESULTS_DIR/diagnostics-run.bin" | tee "$RESULTS_DIR/diagnostics-run.txt"
curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/bench/runtime" >"$RESULTS_DIR/runtime-after.json"

for profile in allocs block goroutine heap mutex threadcreate; do
  curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/pprof/$profile" \
    -o "$RESULTS_DIR/$profile.pprof"
done
stop_server

echo
echo "Starting an isolated CPU-profile run; its throughput is not included in the summary."
start_server 1
run_attack "$WARMUP_DURATION" "$WORKERS" "$RESULTS_DIR/cpu-profile-warmup.bin"
curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/bench/runtime" >"$RESULTS_DIR/cpu-runtime-before.json"
curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/pprof/profile?seconds=$PROFILE_SECONDS" \
  -o "$RESULTS_DIR/cpu.pprof" &
PROFILE_PID=$!
run_attack "$DURATION" "$WORKERS" "$RESULTS_DIR/cpu-profile-run.bin" &
ATTACK_PID=$!
wait "$ATTACK_PID"
ATTACK_PID=""
wait "$PROFILE_PID"
PROFILE_PID=""
vegeta report -type=text <"$RESULTS_DIR/cpu-profile-run.bin" | tee "$RESULTS_DIR/cpu-profile-run.txt"
curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/bench/runtime" >"$RESULTS_DIR/cpu-runtime-after.json"
stop_server

for profile in cpu allocs block goroutine heap mutex threadcreate; do
  go tool pprof -top -nodecount=20 "$BIN" "$RESULTS_DIR/$profile.pprof" \
    >"$RESULTS_DIR/$profile-top.txt" 2>&1 || {
      echo "Warning: could not render $profile profile; see $profile-top.txt." >&2
    }
done

echo
echo "Diagnostics:"
echo "  $RESULTS_DIR/metadata.txt       - machine, Go, and load settings"
echo "  $RESULTS_DIR/measurement-*.bin - raw unprofiled Vegeta results"
echo "  $RESULTS_DIR/measurement-*.txt - per-run throughput and latency percentiles"
echo "  $RESULTS_DIR/summary.txt        - min/median/max throughput"
echo "  $RESULTS_DIR/cpu-top.txt        - CPU profile top"
echo "  $RESULTS_DIR/mutex-top.txt      - mutex contention profile top"
echo "  $RESULTS_DIR/block-top.txt      - blocking profile top"
echo "  $RESULTS_DIR/heap-top.txt       - heap profile top"
echo "  $RESULTS_DIR/runtime-*.json    - Go runtime and memory snapshots"
