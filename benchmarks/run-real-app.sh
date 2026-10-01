#!/usr/bin/env bash
#
# Compare a generated FGOTHS SQLite CRUD application with the same handlers
# mounted on Go's standard-library ServeMux.
#
# Usage:
#   ./benchmarks/run-real-app.sh
#   SATURATION_RUNS=5 SATURATION_DURATION=15s SATURATION_WORKERS=150 ./benchmarks/run-real-app.sh
#
# Requires Go 1.26+, curl and vegeta.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="$ROOT_DIR/benchmarks/real-app"
RESULTS_DIR="${RESULTS_DIR:-$(mktemp -d "${TMPDIR:-/tmp}/fgoths-real-app.XXXXXX")}"
RUNS="${SATURATION_RUNS:-3}"
DURATION="${SATURATION_DURATION:-10s}"
WARMUP_DURATION="${SATURATION_WARMUP_DURATION:-2s}"
WORKERS="${SATURATION_WORKERS:-150}"
SEED_COUNT="${BENCH_SEED_COUNT:-100}"
PORT="${SATURATION_PORT:-18280}"
PROFILE_PORT="${SATURATION_PROFILE_PORT:-18281}"
APP_BIN="$RESULTS_DIR/real-app"
SEED_BIN="$RESULTS_DIR/seed"
SERVER_PID=""
PROFILE_PID=""
ATTACK_PID=""

if [[ -e "$RESULTS_DIR" && -n "$(find "$RESULTS_DIR" -mindepth 1 -print -quit 2>/dev/null)" ]]; then
  echo "Results directory must be empty to avoid overwriting data: $RESULTS_DIR" >&2
  exit 1
fi
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

if [[ ! "$RUNS" =~ ^[1-9][0-9]*$ || ! "$WORKERS" =~ ^[1-9][0-9]*$ || ! "$SEED_COUNT" =~ ^[0-9]+$ ]]; then
  echo "SATURATION_RUNS and SATURATION_WORKERS must be positive integers; BENCH_SEED_COUNT must be non-negative." >&2
  exit 1
fi
if [[ ! "$DURATION" =~ ^[1-9][0-9]*s$ || ! "$WARMUP_DURATION" =~ ^[1-9][0-9]*s$ ]]; then
  echo "SATURATION_DURATION and SATURATION_WARMUP_DURATION must be integer seconds followed by 's'." >&2
  exit 1
fi
PROFILE_SECONDS="${DURATION%s}"

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
  echo "logical_cpus=$(sysctl -n hw.ncpu 2>/dev/null || getconf _NPROCESSORS_ONLN)"
  echo "gomaxprocs_setting=${GOMAXPROCS:-default}"
  echo "duration=$DURATION"
  echo "warmup_duration=$WARMUP_DURATION"
  echo "workers=$WORKERS"
  echo "seed_products=$SEED_COUNT"
  echo "runs_per_case=$RUNS"
} | tee "$RESULTS_DIR/metadata.txt"

echo "results_dir=$RESULTS_DIR"
(cd "$APP_DIR" && go mod tidy && go run ./cmd/assetmanifest && go tool templ generate)
(cd "$APP_DIR" && go build -o "$APP_BIN" . && go build -o "$SEED_BIN" ./cmd/seed)

BODY_FILE="$RESULTS_DIR/product.json"
printf '{"name":"Benchmark Product","category":"electronics","price_cents":1299}\n' >"$BODY_FILE"

make_target() {
  local scenario="$1"
  local target_file="$2"
  if [[ "$scenario" == "get" ]]; then
    printf 'GET http://127.0.0.1:%s/api/products\n\n' "$PORT" >"$target_file"
  else
    printf 'POST http://127.0.0.1:%s/api/products\nContent-Type: application/json\n@%s\n' "$PORT" "$BODY_FILE" >"$target_file"
  fi
}

start_server() {
  local router="$1"
  local database_url="$2"
  local profiling="$3"
  local log_file="$4"

  (
    cd "$APP_DIR"
    export PORT
    export DATABASE_URL="$database_url"
    export FGOTHS_BENCH_ROUTER="$router"
    export FGOTHS_BENCH_PROFILE="$profiling"
    export FGOTHS_BENCH_PROFILE_PORT="$PROFILE_PORT"
    exec "$APP_BIN"
  ) >"$log_file" 2>&1 &
  SERVER_PID=$!

  local attempt status
  for attempt in $(seq 1 100); do
    status="$(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/health/live" 2>/dev/null || true)"
    if [[ "$status" == "200" ]]; then
      if [[ "$profiling" != "1" ]] || curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/pprof/" >/dev/null 2>&1; then
        return 0
      fi
    fi
    if ! kill -0 "$SERVER_PID" 2>/dev/null; then
      cat "$log_file" >&2
      echo "Benchmark application exited before becoming healthy." >&2
      return 1
    fi
    sleep 0.1
  done
  echo "Timed out waiting for $router application on port $PORT." >&2
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
  local target_file="$2"
  local result_file="$3"
  vegeta attack -targets="$target_file" -duration="$duration" -rate=0 -max-workers="$WORKERS" >"$result_file"
}

require_status() {
  local report="$1"
  local expected="$2"
  local failures
  failures="$(awk -v expected="$expected" '
    /^Status Codes/ {
      found = 1
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^[0-9]+:[0-9]+$/) {
          split($i, code, ":")
          if (code[1] != expected) failures += code[2]
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
  if [[ "$failures" -ne 0 ]]; then
    echo "Expected only HTTP $expected responses, but $report contains $failures other responses." >&2
    return 1
  fi
}

run_case() {
  local router="$1"
  local scenario="$2"
  local run="$3"
  local profiling="$4"
  local suffix="$run"
  local database_file="$RESULTS_DIR/$router-$scenario-$suffix.sqlite"
  local database_url="file:$database_file?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
  local target_file="$RESULTS_DIR/$router-$scenario.targets"
  local expected=200

  if [[ "$scenario" == "post" ]]; then
    expected=201
  fi
  make_target "$scenario" "$target_file"

  (cd "$APP_DIR" && DATABASE_URL="$database_url" BENCH_SEED_COUNT="$SEED_COUNT" "$SEED_BIN" \
    >"$RESULTS_DIR/$router-$scenario-$suffix-seed.log" 2>&1) || {
    cat "$RESULTS_DIR/$router-$scenario-$suffix-seed.log" >&2
    return 1
  }

  local profile_flag=0
  if [[ "$profiling" == "1" ]]; then
    profile_flag=1
  fi
  start_server "$router" "$database_url" "$profile_flag" "$RESULTS_DIR/$router-$scenario-$suffix-server.log"

  local warmup="$RESULTS_DIR/$router-$scenario-warmup-$suffix.bin"
  local warmup_report="$RESULTS_DIR/$router-$scenario-warmup-$suffix.txt"
  run_attack "$WARMUP_DURATION" "$target_file" "$warmup"
  vegeta report -type=text <"$warmup" >"$warmup_report"
  require_status "$warmup_report" "$expected"

  if [[ "$profiling" == "1" ]]; then
    curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/pprof/profile?seconds=$PROFILE_SECONDS" \
      -o "$RESULTS_DIR/$router-$scenario-cpu.pprof" &
    PROFILE_PID=$!
  fi

  local raw="$RESULTS_DIR/$router-$scenario-$suffix.bin"
  local report="$RESULTS_DIR/$router-$scenario-$suffix.txt"
  run_attack "$DURATION" "$target_file" "$raw"
  vegeta report -type=text <"$raw" | tee "$report"
  require_status "$report" "$expected"

  if [[ "$profiling" == "1" ]]; then
    wait "$PROFILE_PID"
    PROFILE_PID=""
    for profile in allocs block goroutine heap mutex threadcreate; do
      curl -fsS "http://127.0.0.1:$PROFILE_PORT/debug/pprof/$profile" \
        -o "$RESULTS_DIR/$router-$scenario-$profile.pprof"
    done
  fi
  stop_server
}

summarize_case() {
  local router="$1"
  local scenario="$2"
  local throughputs="$RESULTS_DIR/$router-$scenario-throughputs.txt"
  local p99s="$RESULTS_DIR/$router-$scenario-p99-ns.txt"
  local sorted="$RESULTS_DIR/$router-$scenario-sorted.txt"
  local count min median max p99_median

  : >"$throughputs"
  : >"$p99s"
  for report in "$RESULTS_DIR"/"$router-$scenario"-[0-9]*.txt; do
    [[ -f "$report" ]] || continue
    awk '/^Requests[[:space:]]/ { gsub(/,/, "", $7); print $7; exit }' "$report" >>"$throughputs"
    awk '
      /^Latencies[[:space:]]/ {
        value = $14
        sub(/,$/, "", value)
        if (value ~ /ms$/) {
          sub(/ms$/, "", value)
          value *= 1000000
        } else if (value ~ /[u]s$/) {
          sub(/us$/, "", value)
          value *= 1000
        } else if (value ~ /µs$/) {
          sub(/µs$/, "", value)
          value *= 1000
        } else if (value ~ /ns$/) {
          sub(/ns$/, "", value)
        } else if (value ~ /s$/) {
          sub(/s$/, "", value)
          value *= 1000000000
        } else {
          exit 2
        }
        print value
      }
    ' "$report" >>"$p99s"
  done

  sort -n "$throughputs" >"$sorted"
  count="$(wc -l <"$sorted" | tr -d ' ')"
  if [[ "$count" -ne "$RUNS" ]]; then
    echo "Expected $RUNS measurements for $router/$scenario, found $count." >&2
    return 1
  fi
  min="$(sed -n '1p' "$sorted")"
  max="$(sed -n "${count}p" "$sorted")"
  if (( count % 2 == 1 )); then
    median="$(sed -n "$(((count + 1) / 2))p" "$sorted")"
  else
    median="$(awk -v lo="$((count / 2))" -v hi="$((count / 2 + 1))" 'NR == lo || NR == hi { sum += $1 } END { printf "%.2f", sum / 2 }' "$sorted")"
  fi

  sort -n "$p99s" >"$RESULTS_DIR/$router-$scenario-p99-sorted.txt"
  if (( count % 2 == 1 )); then
    p99_median="$(sed -n "$(((count + 1) / 2))p" "$RESULTS_DIR/$router-$scenario-p99-sorted.txt")"
  else
    p99_median="$(awk -v lo="$((count / 2))" -v hi="$((count / 2 + 1))" 'NR == lo || NR == hi { sum += $1 } END { printf "%.2f", sum / 2 }' "$RESULTS_DIR/$router-$scenario-p99-sorted.txt")"
  fi
  printf '%s %s: throughput min=%s median=%s max=%s req/s; p99 median=%.3fms (%s runs)\n' \
    "$router" "$scenario" "$min" "$median" "$max" "$(awk -v value="$p99_median" 'BEGIN { printf "%.3f", value / 1000000 }')" "$count"
}

for router in fgoths stdlib; do
  for scenario in get post; do
    echo
    echo "=== $router router / products $scenario, $RUNS runs, $WORKERS workers ==="
    for run in $(seq 1 "$RUNS"); do
      printf -v run_id '%02d' "$run"
      run_case "$router" "$scenario" "$run_id" 0
    done
  done
done

{
  summarize_case fgoths get
  summarize_case stdlib get
  summarize_case fgoths post
  summarize_case stdlib post
} | tee "$RESULTS_DIR/summary.txt"

echo
echo "Collecting separate FGOTHS CPU and allocation profiles for each CRUD path."
run_case fgoths get profile 1
run_case fgoths post profile 1
for profile in cpu allocs block goroutine heap mutex threadcreate; do
  for scenario in get post; do
    go tool pprof -top -nodecount=20 "$APP_BIN" "$RESULTS_DIR/fgoths-$scenario-$profile.pprof" \
      >"$RESULTS_DIR/fgoths-$scenario-$profile-top.txt" 2>&1 || {
      echo "Warning: could not render $scenario/$profile profile." >&2
    }
  done
done

echo
echo "Results:"
echo "  $RESULTS_DIR/metadata.txt       - environment and load parameters"
echo "  $RESULTS_DIR/summary.txt        - throughput and p99 medians by router and workload"
echo "  $RESULTS_DIR/fgoths-get-cpu-top.txt - FGOTHS GET collection CPU profile"
echo "  $RESULTS_DIR/fgoths-post-cpu-top.txt - FGOTHS POST create CPU profile"
echo "  $RESULTS_DIR/fgoths-*-allocs-top.txt - FGOTHS allocation profiles"
echo "  $RESULTS_DIR/fgoths-*.bin        - raw Vegeta results"
echo "  $RESULTS_DIR/*-server.log        - application request logs"
