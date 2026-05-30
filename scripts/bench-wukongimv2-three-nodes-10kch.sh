#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_ACTIVATE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="${WK_BENCH_ACTIVATE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-activate-10kch}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-activate-channels/wkbench}"
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongimv2-three-nodes.sh}"
READY_TIMEOUT="${WK_BENCH_THREE_NODE_READY_TIMEOUT:-90}"
START_CLUSTER=1
CLEAN_CLUSTER=1
EVICT_AFTER=0

CHANNELS="${WK_BENCH_ACTIVATE_CHANNELS:-10000}"
USERS="${WK_BENCH_ACTIVATE_USERS:-1000}"
GROUP_MEMBERS="${WK_BENCH_ACTIVATE_GROUP_MEMBERS:-10}"
PREPARE_RATE="${WK_BENCH_ACTIVATE_PREPARE_RATE:-1000}"
CONNECT_RATE="${WK_BENCH_ACTIVATE_CONNECT_RATE:-500}"
ACTIVATION_CONCURRENCY="${WK_BENCH_ACTIVATE_CONCURRENCY:-512}"
ACTIVATION_WINDOW="${WK_BENCH_ACTIVATE_WINDOW:-120s}"
HOLD="${WK_BENCH_ACTIVATE_HOLD:-60s}"
STABLE_P99="${WK_BENCH_ACTIVATE_STABLE_P99:-2s}"
PROBE_BATCH_SIZE="${WK_BENCH_ACTIVATE_PROBE_BATCH_SIZE:-1000}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"

CLUSTER_PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongimv2-three-nodes-10kch.sh [options]

Starts a local cmd/wukongimv2 three-node cluster through
scripts/start-wukongimv2-three-nodes.sh, then runs:

  wkbench capacity activate-channels

Options:
  --out-dir DIR               Evidence output directory.
  --wkbench-bin PATH          wkbench binary path. Default: data/wkbench-activate-channels/wkbench.
  --no-start                  Use an already-running cluster.
  --no-clean                  Keep existing node data when starting the cluster.
  --start-script PATH         Three-node startup script.
  --ready-timeout SECS        Cluster ready wait timeout. Default: 90.
  --channels N                Group channel count. Default: 10000.
  --users N                   Online user pool. Default: 1000.
  --members N                 Members per group channel. Default: 10.
  --prepare-rate N            Bench API preparation rate per second. Default: 1000.
  --connect-rate N            Gateway connect attempts per second. Default: 500.
  --activation-concurrency N  Maximum in-flight SEND operations. Default: 512.
  --activation-window D       Window that schedules exactly one SEND/channel. Default: 120s.
  --hold D                    Post-activation observation duration. Default: 60s.
  --stable-p99 D              Sendack p99 gate. Default: 2s.
  --probe-batch-size N        Runtime probe batch size. Default: 1000.
  --evict-after               Evict generated channel runtime after probing.
  --api LIST                  Comma-separated API base URLs.
  --gateway LIST              Comma-separated WKProto gateway addresses.
  --metrics LIST              Comma-separated metrics base URLs. Default: same as --api.
  -h, --help                  Show this help.
USAGE
}

log() {
  printf '[bench-three-activate-10kch] %s\n' "$*"
}

die() {
  printf '[bench-three-activate-10kch] ERROR: %s\n' "$*" >&2
  exit 1
}

require_positive_int() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a positive integer: $value"
  (( value > 0 )) || die "$name must be a positive integer: $value"
}

require_nonnegative_number() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "$name must be a non-negative number: $value"
}

split_csv() {
  local raw="$1"
  local var_name="$2"
  eval "$var_name=()"
  local values=()
  local item
  IFS=',' read -ra values <<<"$raw"
  for item in "${values[@]}"; do
    [[ -n "${item//[[:space:]]/}" ]] || die "comma-separated list contains an empty item: $raw"
    eval "$var_name+=(\"\$item\")"
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      [[ $# -ge 2 ]] || die '--out-dir requires a value'
      OUT_DIR="$2"
      shift 2
      ;;
    --wkbench-bin)
      [[ $# -ge 2 ]] || die '--wkbench-bin requires a value'
      WK_BENCH_BIN="$2"
      shift 2
      ;;
    --no-start)
      START_CLUSTER=0
      shift
      ;;
    --no-clean)
      CLEAN_CLUSTER=0
      shift
      ;;
    --start-script)
      [[ $# -ge 2 ]] || die '--start-script requires a value'
      START_SCRIPT="$2"
      shift 2
      ;;
    --ready-timeout)
      [[ $# -ge 2 ]] || die '--ready-timeout requires a value'
      READY_TIMEOUT="$2"
      shift 2
      ;;
    --channels)
      [[ $# -ge 2 ]] || die '--channels requires a value'
      CHANNELS="$2"
      shift 2
      ;;
    --users)
      [[ $# -ge 2 ]] || die '--users requires a value'
      USERS="$2"
      shift 2
      ;;
    --members)
      [[ $# -ge 2 ]] || die '--members requires a value'
      GROUP_MEMBERS="$2"
      shift 2
      ;;
    --prepare-rate)
      [[ $# -ge 2 ]] || die '--prepare-rate requires a value'
      PREPARE_RATE="$2"
      shift 2
      ;;
    --connect-rate)
      [[ $# -ge 2 ]] || die '--connect-rate requires a value'
      CONNECT_RATE="$2"
      shift 2
      ;;
    --activation-concurrency)
      [[ $# -ge 2 ]] || die '--activation-concurrency requires a value'
      ACTIVATION_CONCURRENCY="$2"
      shift 2
      ;;
    --activation-window)
      [[ $# -ge 2 ]] || die '--activation-window requires a value'
      ACTIVATION_WINDOW="$2"
      shift 2
      ;;
    --hold)
      [[ $# -ge 2 ]] || die '--hold requires a value'
      HOLD="$2"
      shift 2
      ;;
    --stable-p99)
      [[ $# -ge 2 ]] || die '--stable-p99 requires a value'
      STABLE_P99="$2"
      shift 2
      ;;
    --probe-batch-size)
      [[ $# -ge 2 ]] || die '--probe-batch-size requires a value'
      PROBE_BATCH_SIZE="$2"
      shift 2
      ;;
    --evict-after)
      EVICT_AFTER=1
      shift
      ;;
    --api)
      [[ $# -ge 2 ]] || die '--api requires a value'
      API_ADDRS="$2"
      shift 2
      ;;
    --gateway)
      [[ $# -ge 2 ]] || die '--gateway requires a value'
      GATEWAY_ADDRS="$2"
      shift 2
      ;;
    --metrics)
      [[ $# -ge 2 ]] || die '--metrics requires a value'
      METRICS_ADDRS="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

require_positive_int '--channels' "$CHANNELS"
require_positive_int '--users' "$USERS"
require_positive_int '--members' "$GROUP_MEMBERS"
require_positive_int '--activation-concurrency' "$ACTIVATION_CONCURRENCY"
require_positive_int '--probe-batch-size' "$PROBE_BATCH_SIZE"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_nonnegative_number '--prepare-rate' "$PREPARE_RATE"
require_nonnegative_number '--connect-rate' "$CONNECT_RATE"

declare -a API_VALUES GATEWAY_VALUES METRICS_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

cleanup() {
  if [[ -n "$CLUSTER_PID" ]]; then
    log "stopping three-node cluster pid=$CLUSTER_PID"
    kill "$CLUSTER_PID" >/dev/null 2>&1 || true
    wait "$CLUSTER_PID" 2>/dev/null || true
  fi
}

trap cleanup EXIT

ensure_wkbench_binary() {
  if [[ -x "$WK_BENCH_BIN" ]]; then
    return
  fi
  log "building wkbench: $WK_BENCH_BIN"
  mkdir -p "$(dirname "$WK_BENCH_BIN")"
  (
    cd "$ROOT_DIR"
    go build -o "$WK_BENCH_BIN" ./cmd/wkbench
  )
}

start_cluster() {
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    log "cluster startup disabled; using existing cluster"
    return
  fi
  [[ -x "$START_SCRIPT" ]] || die "start script is not executable: $START_SCRIPT"
  mkdir -p "$OUT_DIR/logs"
  local clean_arg=()
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    clean_arg=(--clean)
  fi
  log "starting three-node cluster with $START_SCRIPT"
  WK_PPROF_ENABLE="${WK_PPROF_ENABLE:-true}" \
  WK_CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-3}" \
  WK_CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-96}" \
  WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}" \
  WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS:-256}" \
  WK_CLUSTER_COMMIT_COORDINATOR_SYNC="${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}" \
    "$START_SCRIPT" "${clean_arg[@]}" --ready-timeout "$READY_TIMEOUT" \
      >"$OUT_DIR/cluster-start.log" 2>&1 &
  CLUSTER_PID="$!"
}

check_cluster_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local api all_ready
  while (( SECONDS <= deadline )); do
    if [[ -n "$CLUSTER_PID" ]] && ! kill -0 "$CLUSTER_PID" 2>/dev/null; then
      tail -n 120 "$OUT_DIR/cluster-start.log" >&2 || true
      die "three-node cluster exited before becoming ready"
    fi
    all_ready=1
    for api in "${API_VALUES[@]}"; do
      if ! curl -fsS --max-time 3 "${api%/}/readyz" >/dev/null 2>&1; then
        all_ready=0
        break
      fi
    done
    if [[ "$all_ready" -eq 1 ]]; then
      log "cluster ready"
      return
    fi
    sleep 1
  done
  tail -n 120 "$OUT_DIR/cluster-start.log" >&2 || true
  die "timed out waiting for cluster readyz"
}

metric_file_id() {
  local raw="$1"
  raw="${raw#http://}"
  raw="${raw#https://}"
  printf '%s' "$raw" | tr -c 'A-Za-z0-9' '_'
}

scrape_metrics() {
  local phase="$1"
  local metrics_dir="$OUT_DIR/metrics"
  mkdir -p "$metrics_dir"
  local addr id
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    curl -fsS "${addr%/}/metrics" >"$metrics_dir/${id}-${phase}.prom" || true
  done
}

capture_pprof() {
  local phase="$1"
  local pprof_dir="$OUT_DIR/pprof/$phase"
  mkdir -p "$pprof_dir"
  local addr id
  for addr in "${API_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    curl -fsS "${addr%/}/debug/pprof/goroutine?debug=2" >"$pprof_dir/${id}-goroutine.txt" || true
    curl -fsS "${addr%/}/debug/pprof/heap" >"$pprof_dir/${id}-heap.pb.gz" || true
  done
}

collect_node_logs() {
  local phase="$1"
  local dest="$OUT_DIR/logs/$phase"
  mkdir -p "$dest"
  cp "$ROOT_DIR"/data/wukongimv2-three-node-logs/node*.log "$dest/" 2>/dev/null || true
  if [[ -f "$OUT_DIR/cluster-start.log" ]]; then
    cp "$OUT_DIR/cluster-start.log" "$dest/cluster-start.log" 2>/dev/null || true
  fi
}

write_metadata() {
  mkdir -p "$OUT_DIR/config"
  {
    echo "head=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
    echo "short=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
    git -C "$ROOT_DIR" status --short 2>/dev/null || true
  } >"$OUT_DIR/git.txt"
  cat >"$OUT_DIR/env.txt" <<EOF
CHANNELS=$CHANNELS
USERS=$USERS
GROUP_MEMBERS=$GROUP_MEMBERS
PREPARE_RATE=$PREPARE_RATE
CONNECT_RATE=$CONNECT_RATE
ACTIVATION_CONCURRENCY=$ACTIVATION_CONCURRENCY
ACTIVATION_WINDOW=$ACTIVATION_WINDOW
HOLD=$HOLD
STABLE_P99=$STABLE_P99
PROBE_BATCH_SIZE=$PROBE_BATCH_SIZE
EVICT_AFTER=$EVICT_AFTER
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
EOF
  cp "$ROOT_DIR"/scripts/wukongimv2/wukongimv2-node*.conf "$OUT_DIR/config/" 2>/dev/null || true
  if [[ -x "$START_SCRIPT" ]]; then
    "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
}

write_summary() {
  local status="$1"
  local report_dir="$OUT_DIR/report"
  cat >"$OUT_DIR/summary.md" <<EOF
# Three-Node Activate Channels Evidence

## Scenario
- workload: local wukongimv2 three-node wkbench activate-channels
- channels: $CHANNELS
- users: $USERS
- group_members: $GROUP_MEMBERS
- activation_window: $ACTIVATION_WINDOW
- activation_concurrency: $ACTIVATION_CONCURRENCY
- clean_cluster: $CLEAN_CLUSTER

## Evidence
- git: git.txt
- env: env.txt
- start_plan: start-plan.txt
- config: config/
- metrics: metrics/
- pprof: pprof/
- logs: logs/
- report: report/
- console: wkbench-console.txt

## Result
- exit_status: $status
- activation_report: report/activation_report.json
- activation_summary: report/summary.md
EOF
}

run_activation() {
  local report_dir="$OUT_DIR/report"
  local exit_status=0
  mkdir -p "$report_dir"
  local cmd=(
    "$WK_BENCH_BIN" capacity activate-channels
    --api "$API_ADDRS"
    --gateway "$GATEWAY_ADDRS"
    --channels "$CHANNELS"
    --users "$USERS"
    --group-members "$GROUP_MEMBERS"
    --prepare-rate "$PREPARE_RATE"
    --connect-rate "$CONNECT_RATE"
    --activation-concurrency "$ACTIVATION_CONCURRENCY"
    --activation-window "$ACTIVATION_WINDOW"
    --hold "$HOLD"
    --stable-p99 "$STABLE_P99"
    --probe-batch-size "$PROBE_BATCH_SIZE"
    --report-dir "$report_dir"
  )
  if [[ "$EVICT_AFTER" -eq 1 ]]; then
    cmd+=(--evict-after)
  fi
  log "running activate-channels channels=$CHANNELS users=$USERS window=$ACTIVATION_WINDOW"
  "${cmd[@]}" >"$OUT_DIR/wkbench-console.txt" 2>&1 || exit_status=$?
  return "$exit_status"
}

main() {
  cd "$ROOT_DIR"
  mkdir -p "$OUT_DIR"
  ensure_wkbench_binary
  start_cluster
  check_cluster_ready
  write_metadata
  collect_node_logs before
  scrape_metrics before
  capture_pprof before

  local status=0
  run_activation || status=$?

  scrape_metrics after
  capture_pprof after
  collect_node_logs after
  write_summary "$status"
  cat "$OUT_DIR/wkbench-console.txt" || true
  log "evidence: $OUT_DIR"
  exit "$status"
}

main "$@"
