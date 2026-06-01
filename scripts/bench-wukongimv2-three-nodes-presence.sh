#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_PRESENCE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="${WK_BENCH_PRESENCE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-presence}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-presence/wkbench}"
WORKER_ADDR="${WK_BENCH_WORKER_ADDR:-http://127.0.0.1:19131}"
WORKER_LISTEN="${WK_BENCH_WORKER_LISTEN:-127.0.0.1:19131}"
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongimv2-three-nodes.sh}"
READY_TIMEOUT="${WK_BENCH_THREE_NODE_READY_TIMEOUT:-90}"
START_CLUSTER=1
CLEAN_CLUSTER=1
START_WORKER=1

USERS="${WK_BENCH_PRESENCE_USERS:-1000}"
CONNECT_RATE="${WK_BENCH_PRESENCE_CONNECT_RATE:-500}"
DURATION="${WK_BENCH_PRESENCE_DURATION:-5s}"
WARMUP="${WK_BENCH_PRESENCE_WARMUP:-1s}"
COOLDOWN="${WK_BENCH_PRESENCE_COOLDOWN:-2s}"
HEARTBEAT_INTERVAL="${WK_BENCH_PRESENCE_HEARTBEAT_INTERVAL:-1s}"
HEARTBEAT_TIMEOUT="${WK_BENCH_PRESENCE_HEARTBEAT_TIMEOUT:-5s}"
SAMPLE_INTERVAL="${WK_BENCH_PRESENCE_SAMPLE_INTERVAL:-1}"
STABLE_SAMPLES="${WK_BENCH_PRESENCE_STABLE_SAMPLES:-2}"
REQUIRE_TOUCH="${WK_BENCH_PRESENCE_REQUIRE_TOUCH:-1}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"

WORKER_PID=""
CLUSTER_PID=""
PRESENCE_SAMPLE_SEQ=0

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongimv2-three-nodes-presence.sh [options]

Starts a local cmd/wukongimv2 three-node cluster, runs a connection-only
wkbench scenario with heartbeat pings, polls live presence snapshots while
the run is active, then validates the live peak against report.json status.

Options:
  --out-dir DIR             Evidence output directory.
  --wkbench-bin PATH        wkbench binary path. Default: data/wkbench-presence/wkbench.
  --worker-addr URL         Worker control URL. Default: http://127.0.0.1:19131.
  --worker-listen ADDR      Temporary worker listen address. Default: 127.0.0.1:19131.
  --no-worker               Do not start a temporary worker; require --worker-addr to be reachable.
  --no-start                Do not start or stop the three-node cluster; use an already-running cluster.
  --no-clean                When starting the cluster, keep existing node data.
  --start-script PATH       Three-node startup script. Default: scripts/start-wukongimv2-three-nodes.sh.
  --ready-timeout SECS      Cluster ready wait timeout. Default: 90.
  --users N                 Online user count. Default: 1000.
  --connect-rate N          Gateway connect attempts per second. Default: 500.
  --duration D              Measured run duration. Default: 5s.
  --warmup D                Warmup duration. Default: 1s.
  --cooldown D              Cooldown duration. Default: 2s.
  --heartbeat-interval D    Client ping interval. Default: 1s.
  --heartbeat-timeout D     Client ping timeout. Default: 5s.
  --sample-interval SECS    Live presence sample interval. Default: 1.
  --stable-samples N        Required consecutive full live samples. Default: 2.
  --no-require-touch        Do not fail when touch_routes_total is zero.
  --api LIST                Comma-separated API base URLs.
  --gateway LIST            Comma-separated WKProto gateway addresses.
  -h, --help                Show this help.
USAGE
}

log() {
  printf '[bench-three-presence] %s\n' "$*"
}

die() {
  printf '[bench-three-presence] ERROR: %s\n' "$*" >&2
  exit 1
}

require_positive_int() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a positive integer: $value"
  (( value > 0 )) || die "$name must be a positive integer: $value"
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
    --worker-addr)
      [[ $# -ge 2 ]] || die '--worker-addr requires a value'
      WORKER_ADDR="$2"
      shift 2
      ;;
    --worker-listen)
      [[ $# -ge 2 ]] || die '--worker-listen requires a value'
      WORKER_LISTEN="$2"
      shift 2
      ;;
    --no-worker)
      START_WORKER=0
      shift
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
    --users)
      [[ $# -ge 2 ]] || die '--users requires a value'
      USERS="$2"
      shift 2
      ;;
    --connect-rate)
      [[ $# -ge 2 ]] || die '--connect-rate requires a value'
      CONNECT_RATE="$2"
      shift 2
      ;;
    --duration)
      [[ $# -ge 2 ]] || die '--duration requires a value'
      DURATION="$2"
      shift 2
      ;;
    --warmup)
      [[ $# -ge 2 ]] || die '--warmup requires a value'
      WARMUP="$2"
      shift 2
      ;;
    --cooldown)
      [[ $# -ge 2 ]] || die '--cooldown requires a value'
      COOLDOWN="$2"
      shift 2
      ;;
    --heartbeat-interval)
      [[ $# -ge 2 ]] || die '--heartbeat-interval requires a value'
      HEARTBEAT_INTERVAL="$2"
      shift 2
      ;;
    --heartbeat-timeout)
      [[ $# -ge 2 ]] || die '--heartbeat-timeout requires a value'
      HEARTBEAT_TIMEOUT="$2"
      shift 2
      ;;
    --sample-interval)
      [[ $# -ge 2 ]] || die '--sample-interval requires a value'
      SAMPLE_INTERVAL="$2"
      shift 2
      ;;
    --stable-samples)
      [[ $# -ge 2 ]] || die '--stable-samples requires a value'
      STABLE_SAMPLES="$2"
      shift 2
      ;;
    --no-require-touch)
      REQUIRE_TOUCH=0
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
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

require_positive_int '--users' "$USERS"
require_positive_int '--connect-rate' "$CONNECT_RATE"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_positive_int '--stable-samples' "$STABLE_SAMPLES"
[[ "$REQUIRE_TOUCH" == "0" || "$REQUIRE_TOUCH" == "1" ]] || die "WK_BENCH_PRESENCE_REQUIRE_TOUCH must be 0 or 1"

declare -a API_VALUES GATEWAY_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES

cleanup() {
  if [[ -n "$WORKER_PID" ]]; then
    log "stopping temporary worker pid=$WORKER_PID"
    curl -fsS -X POST "${WORKER_ADDR%/}/v1/stop" >/dev/null 2>&1 || true
    kill "$WORKER_PID" >/dev/null 2>&1 || true
    wait "$WORKER_PID" 2>/dev/null || true
  fi
  if [[ -n "$CLUSTER_PID" ]]; then
    log "stopping three-node cluster pid=$CLUSTER_PID"
    kill "$CLUSTER_PID" >/dev/null 2>&1 || true
    wait "$CLUSTER_PID" 2>/dev/null || true
  fi
}

trap cleanup EXIT

ensure_tools() {
  command -v jq >/dev/null 2>&1 || die "jq is required to validate presence snapshots"
}

ensure_wkbench_binary() {
  if [[ -x "$WK_BENCH_BIN" ]]; then
    local newer_source
    newer_source="$(find "$ROOT_DIR/cmd/wkbench" "$ROOT_DIR/internal/bench" "$ROOT_DIR/pkg/protocol" -type f -newer "$WK_BENCH_BIN" -print -quit)"
    if [[ -z "$newer_source" ]]; then
      return
    fi
    log "rebuilding stale wkbench: $WK_BENCH_BIN"
  else
    log "building wkbench: $WK_BENCH_BIN"
  fi
  mkdir -p "$(dirname "$WK_BENCH_BIN")"
  (
    cd "$ROOT_DIR"
    GOWORK="${GOWORK:-off}" go build -o "$WK_BENCH_BIN" ./cmd/wkbench
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
  WK_PRESENCE_TOUCH_FLUSH_INTERVAL="${WK_PRESENCE_TOUCH_FLUSH_INTERVAL:-1s}" \
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

worker_ready() {
  curl -fsS --max-time 2 "${WORKER_ADDR%/}/healthz" >/dev/null 2>&1
}

ensure_worker() {
  if worker_ready; then
    log "using existing worker: $WORKER_ADDR"
    return
  fi
  if [[ "$START_WORKER" -eq 0 ]]; then
    die "worker is not reachable at $WORKER_ADDR"
  fi
  ensure_wkbench_binary
  local worker_dir="$OUT_DIR/worker-state"
  mkdir -p "$worker_dir"
  log "starting temporary worker: $WORKER_LISTEN"
  "$WK_BENCH_BIN" worker --listen "$WORKER_LISTEN" --work-dir "$worker_dir" --insecure-control >/dev/null 2>&1 &
  WORKER_PID="$!"
  local deadline=$((SECONDS + 15))
  while (( SECONDS <= deadline )); do
    if worker_ready; then
      log "worker ready: $WORKER_ADDR"
      return
    fi
    sleep 1
  done
  die "timed out waiting for worker at $WORKER_ADDR"
}

yaml_list() {
  local var_name="$1"
  eval "local values=(\"\${${var_name}[@]}\")"
  local value
  for value in "${values[@]}"; do
    printf '    - %s\n' "$value"
  done
}

write_target_and_workers() {
  mkdir -p "$OUT_DIR"
  {
    cat <<'YAML'
name: local-three-node-cluster
api:
  addrs:
YAML
    yaml_list API_VALUES
    cat <<'YAML'
gateway:
  tcp:
    addrs:
YAML
    yaml_list GATEWAY_VALUES
    cat <<'YAML'
bench_api:
  enabled: true
  addrs:
YAML
    yaml_list API_VALUES
  } >"$OUT_DIR/target.yaml"

  cat >"$OUT_DIR/workers.yaml" <<YAML
workers:
  - id: worker-a
    addr: $WORKER_ADDR
    weight: 1
    control_token: ""
    insecure_control: true
YAML
}

write_scenario() {
  local report_dir="$OUT_DIR/report"
  cat >"$OUT_DIR/scenario.yaml" <<YAML
version: wkbench/v1
run:
  id: three-node-presence-${USERS}u
  duration: $DURATION
  warmup: $WARMUP
  cooldown: $COOLDOWN
  random_seed: 0
  fail_fast: true
  report_dir: $report_dir
limits:
  fail_on_soft: false
  hard:
    max_worker_failed: 0
    max_connect_error_rate: 0
    max_sendack_error_rate: 0
    max_recv_verify_error_rate: 0
identity:
  uid_prefix: presence-${TIMESTAMP}-u
  device_prefix: presence-${TIMESTAMP}-d
  client_msg_prefix: presence-${TIMESTAMP}-msg
  token:
    mode: bench_api
online:
  total_users: $USERS
  connect_rate: ${CONNECT_RATE}/s
  gateway_balance: round_robin
  heartbeat:
    enabled: true
    interval: $HEARTBEAT_INTERVAL
    timeout: $HEARTBEAT_TIMEOUT
channels:
  profiles: []
messages:
  payload:
    size_bytes: 32
    mode: fixed
  traffic: []
YAML
}

write_run_metadata() {
  mkdir -p "$OUT_DIR/logs"
  {
    echo "head=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
    echo "short=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
    git -C "$ROOT_DIR" status --short 2>/dev/null || true
  } >"$OUT_DIR/git.txt"
  cat >"$OUT_DIR/env.txt" <<EOF
USERS=$USERS
CONNECT_RATE=$CONNECT_RATE
DURATION=$DURATION
WARMUP=$WARMUP
COOLDOWN=$COOLDOWN
HEARTBEAT_INTERVAL=$HEARTBEAT_INTERVAL
HEARTBEAT_TIMEOUT=$HEARTBEAT_TIMEOUT
SAMPLE_INTERVAL=$SAMPLE_INTERVAL
STABLE_SAMPLES=$STABLE_SAMPLES
REQUIRE_TOUCH=$REQUIRE_TOUCH
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
WORKER_ADDR=$WORKER_ADDR
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
EOF
  if [[ -x "$START_SCRIPT" ]]; then
    "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
}

run_bench() {
  local report_dir="$OUT_DIR/report"
  mkdir -p "$report_dir"
  log "running presence scenario users=$USERS"
  "$WK_BENCH_BIN" run \
    --target "$OUT_DIR/target.yaml" \
    --scenario "$OUT_DIR/scenario.yaml" \
    --workers "$OUT_DIR/workers.yaml" \
    >"$OUT_DIR/wkbench-console.txt" 2>&1 &
  local bench_pid="$!"

  collect_presence_sample "run" || true
  while jobs -pr | grep -qx "$bench_pid"; do
    sleep "$SAMPLE_INTERVAL"
    collect_presence_sample "run" || true
  done

  local status
  set +e
  wait "$bench_pid"
  status=$?
  set -e
  collect_presence_sample "after" || true
  return "$status"
}

collect_presence_sample() {
  local phase="$1"
  local samples="$OUT_DIR/presence-samples.jsonl"
  local tmp="$OUT_DIR/.presence-sample.tmp"
  local now api payload
  PRESENCE_SAMPLE_SEQ=$((PRESENCE_SAMPLE_SEQ + 1))
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  : >"$tmp"
  for api in "${API_VALUES[@]}"; do
    if payload="$(curl -fsS --max-time 3 "${api%/}/bench/v1/presence/snapshot" 2>/dev/null)"; then
      jq -c \
        --arg at "$now" \
        --arg phase "$phase" \
        --arg api "$api" \
        --argjson sample_seq "$PRESENCE_SAMPLE_SEQ" \
        '. + {sample_at: $at, sample_phase: $phase, api_addr: $api, sample_seq: $sample_seq}' \
        <<<"$payload" >>"$tmp"
    else
      jq -cn \
        --arg at "$now" \
        --arg phase "$phase" \
        --arg api "$api" \
        --argjson sample_seq "$PRESENCE_SAMPLE_SEQ" \
        '{sample_at: $at, sample_phase: $phase, api_addr: $api, sample_seq: $sample_seq, sample_error: "presence snapshot request failed"}' \
        >>"$tmp"
    fi
  done
  if [[ -s "$tmp" ]]; then
    cat "$tmp" >>"$samples"
  fi
  rm -f "$tmp"
}

validate_presence_report() {
  local report="$OUT_DIR/report/report.json"
  local samples="$OUT_DIR/presence-samples.jsonl"
  local out="$OUT_DIR/presence-summary.tsv"
  [[ -f "$report" ]] || die "missing report: $report"
  [[ -s "$samples" ]] || die "missing live presence samples: $samples"

  local status
  status="$(jq -r '.status // ""' "$report")"
  local heartbeat_success_total heartbeat_error_total report_error_sample_count
  heartbeat_success_total="$(jq -r '.metrics.counters.heartbeat_success_total // 0' "$report")"
  heartbeat_error_total="$(jq -r '.metrics.counters.heartbeat_error_total // 0' "$report")"
  report_error_sample_count="$(jq -r '(.error_samples // []) | length' "$report")"

  local final_stats
  final_stats="$(jq -r '
    def sumfield($name): ([.presence_snapshots[]? | .[$name] // 0] | add // 0);
    def hashslotsum: ([.presence_snapshots[]? | (.authority_routes_by_hash_slot // {}) | to_entries[]? | .value // 0] | add // 0);
    [
      ((.presence_snapshots // []) | length),
      sumfield("owner_routes_active"),
      sumfield("owner_routes_pending"),
      sumfield("owner_touched_dirty"),
      sumfield("authority_routes_active"),
      hashslotsum,
      sumfield("touch_routes_total"),
      sumfield("expired_routes_total")
    ] | @tsv
  ' "$report")"

  local final_snapshots final_owner_active final_owner_pending final_owner_dirty final_authority_active final_hash_slot_total final_touch_total final_expired_total
  IFS=$'\t' read -r final_snapshots final_owner_active final_owner_pending final_owner_dirty final_authority_active final_hash_slot_total final_touch_total final_expired_total <<<"$final_stats"

  local live_stats
  live_stats="$(jq -rs \
    --argjson expected_users "$USERS" \
    --argjson expected_nodes "${#API_VALUES[@]}" \
    '
    def sumfield($name): (map(.[$name] // 0) | add // 0);
    def hashslotsum: (map((.authority_routes_by_hash_slot // {}) | to_entries[]? | .value // 0) | add // 0);
    def sample_ok:
      .sample_phase == "run"
      and .snapshot_nodes >= $expected_nodes
      and .owner_routes_active == $expected_users
      and .authority_routes_active == $expected_users
      and .owner_routes_pending == 0
      and .authority_routes_by_hash_slot_total == .authority_routes_active
      and .expired_routes_total == 0;
    (group_by(.sample_seq) | map({
      sample_seq: (.[0].sample_seq // 0),
      sample_phase: (.[0].sample_phase // ""),
      snapshot_nodes: (map(select(has("sample_error") | not)) | length),
      sample_error_count: (map(select(has("sample_error"))) | length),
      owner_routes_active: sumfield("owner_routes_active"),
      owner_routes_pending: sumfield("owner_routes_pending"),
      owner_touched_dirty: sumfield("owner_touched_dirty"),
      authority_routes_active: sumfield("authority_routes_active"),
      authority_routes_by_hash_slot_total: hashslotsum,
      touch_routes_total: sumfield("touch_routes_total"),
      expired_routes_total: sumfield("expired_routes_total")
    })) as $samples
    | ($samples | max_by(([.owner_routes_active // 0, .authority_routes_active // 0] | min)) // {}) as $peak
    | (reduce $samples[] as $sample ({current: 0, max: 0};
        if ($sample | sample_ok) then
          .current += 1 | .max = ([.max, .current] | max)
        else
          .current = 0
        end
      ) | .max) as $stable_count
    | [
      ($samples | length),
      ($peak.sample_seq // 0),
      ($peak.sample_phase // ""),
      ($peak.snapshot_nodes // 0),
      ($peak.owner_routes_active // 0),
      ($peak.owner_routes_pending // 0),
      ($peak.owner_touched_dirty // 0),
      ($peak.authority_routes_active // 0),
      ($peak.authority_routes_by_hash_slot_total // 0),
      ($peak.touch_routes_total // 0),
      ($peak.expired_routes_total // 0),
      ($samples | map(.touch_routes_total // 0) | max // 0),
      ($samples | map(.expired_routes_total // 0) | max // 0),
      $stable_count,
      ($samples | map(.sample_error_count // 0) | add // 0)
    ] | @tsv
  ' "$samples")"

  local live_samples peak_seq peak_phase snapshots owner_active owner_pending owner_dirty authority_active hash_slot_total touch_total expired_total max_touch_total max_expired_total stable_sample_count sample_error_count
  IFS=$'\t' read -r live_samples peak_seq peak_phase snapshots owner_active owner_pending owner_dirty authority_active hash_slot_total touch_total expired_total max_touch_total max_expired_total stable_sample_count sample_error_count <<<"$live_stats"

  local presence_status="passed"
  local failures=0
  if [[ "$status" != "passed" ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$heartbeat_error_total" -ne 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$report_error_sample_count" -ne 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$sample_error_count" -ne 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$snapshots" -le 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$snapshots" -lt "${#API_VALUES[@]}" ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$owner_active" -ne "$USERS" ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$authority_active" -ne "$USERS" ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$owner_pending" -ne 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$hash_slot_total" -ne "$authority_active" ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$max_expired_total" -ne 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$REQUIRE_TOUCH" -eq 1 && "$max_touch_total" -le 0 ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi
  if [[ "$stable_sample_count" -lt "$STABLE_SAMPLES" ]]; then
    presence_status="failed"
    failures=$((failures + 1))
  fi

  {
    printf 'metric\tvalue\n'
    printf 'report_status\t%s\n' "$status"
    printf 'presence_status\t%s\n' "$presence_status"
    printf 'expected_users\t%s\n' "$USERS"
    printf 'required_stable_samples\t%s\n' "$STABLE_SAMPLES"
    printf 'stable_sample_count\t%s\n' "$stable_sample_count"
    printf 'heartbeat_success_total\t%s\n' "$heartbeat_success_total"
    printf 'heartbeat_error_total\t%s\n' "$heartbeat_error_total"
    printf 'report_error_sample_count\t%s\n' "$report_error_sample_count"
    printf 'sample_error_count\t%s\n' "$sample_error_count"
    printf 'live_sample_count\t%s\n' "$live_samples"
    printf 'live_peak_sample_seq\t%s\n' "$peak_seq"
    printf 'live_peak_sample_phase\t%s\n' "$peak_phase"
    printf 'snapshot_nodes\t%s\n' "$snapshots"
    printf 'owner_routes_active\t%s\n' "$owner_active"
    printf 'owner_routes_pending\t%s\n' "$owner_pending"
    printf 'owner_touched_dirty\t%s\n' "$owner_dirty"
    printf 'authority_routes_active\t%s\n' "$authority_active"
    printf 'authority_routes_by_hash_slot_total\t%s\n' "$hash_slot_total"
    printf 'touch_routes_total\t%s\n' "$touch_total"
    printf 'expired_routes_total\t%s\n' "$expired_total"
    printf 'max_touch_routes_total\t%s\n' "$max_touch_total"
    printf 'max_expired_routes_total\t%s\n' "$max_expired_total"
    printf 'final_snapshot_nodes\t%s\n' "$final_snapshots"
    printf 'final_owner_routes_active\t%s\n' "$final_owner_active"
    printf 'final_owner_routes_pending\t%s\n' "$final_owner_pending"
    printf 'final_owner_touched_dirty\t%s\n' "$final_owner_dirty"
    printf 'final_authority_routes_active\t%s\n' "$final_authority_active"
    printf 'final_authority_routes_by_hash_slot_total\t%s\n' "$final_hash_slot_total"
    printf 'final_touch_routes_total\t%s\n' "$final_touch_total"
    printf 'final_expired_routes_total\t%s\n' "$final_expired_total"
    printf 'failures\t%s\n' "$failures"
  } >"$out"

  if [[ "$presence_status" != "passed" ]]; then
    cat "$out" >&2
    die "presence snapshot validation failed"
  fi
}

write_evidence_summary() {
  local presence_status
  presence_status="$(awk -F'\t' '$1 == "presence_status" { print $2 }' "$OUT_DIR/presence-summary.tsv" 2>/dev/null || true)"
  cat >"$OUT_DIR/summary.md" <<EOF
# Three-Node Presence Bench Evidence

## Scenario
- workload: local wukongimv2 three-node wkbench connection presence
- users: $USERS
- connect_rate: ${CONNECT_RATE}/s
- duration: $DURATION
- heartbeat_interval: $HEARTBEAT_INTERVAL
- stable_samples: $STABLE_SAMPLES
- clean_cluster: $CLEAN_CLUSTER

## Evidence
- git: git.txt
- env: env.txt
- start_plan: start-plan.txt
- target: target.yaml
- workers: workers.yaml
- scenario: scenario.yaml
- console: wkbench-console.txt
- report: report/report.json
- live_presence_samples: presence-samples.jsonl
- presence_summary: presence-summary.tsv

## Result
- presence_status: ${presence_status:-unknown}
EOF
}

print_summary() {
  write_evidence_summary
  cat "$OUT_DIR/presence-summary.tsv"
  log "details:"
  printf '  report: %s\n' "$OUT_DIR/report/report.json"
  printf '  presence: %s\n' "$OUT_DIR/presence-summary.tsv"
  printf '  summary: %s\n' "$OUT_DIR/summary.md"
}

main() {
  cd "$ROOT_DIR"
  mkdir -p "$OUT_DIR"
  ensure_tools
  ensure_wkbench_binary
  start_cluster
  check_cluster_ready
  ensure_worker
  write_target_and_workers
  write_scenario
  write_run_metadata
  run_bench
  validate_presence_report
  print_summary
}

main "$@"
