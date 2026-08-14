#!/usr/bin/env bash
set -euo pipefail
umask 077
# Do not allow an inherited xtrace setting to expose the benchmark API token
# when the threshold helper receives it through its environment.
set +x

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_SINGLE_NODE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"

QPS_LIST="${WK_BENCH_SINGLE_NODE_QPS:-250,500,750,1000}"
OUT_DIR="${WK_BENCH_SINGLE_NODE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-single-node-1000ch}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-test}"
WORKER_ADDR="${WK_BENCH_WORKER_ADDR:-http://127.0.0.1:19130}"
WORKER_LISTEN="${WK_BENCH_WORKER_LISTEN:-127.0.0.1:19130}"
START_WORKER=1
START_CLUSTER=1
CLEAN_CLUSTER=1
START_SCRIPT="${WK_BENCH_SINGLE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-single-node.sh}"
READY_TIMEOUT="${WK_BENCH_SINGLE_NODE_READY_TIMEOUT:-90}"
WUKONGIM_CONFIG_SOURCE="${WK_WUKONGIM_SINGLE_NODE_CONFIG:-$ROOT_DIR/scripts/wukongim/wukongim.toml}"
WUKONGIM_CONFIG="$WUKONGIM_CONFIG_SOURCE"
WUKONGIM_CONFIG_SOURCE_CANONICAL=""
WUKONGIM_CONFIG_SOURCE_REVIEWED=false
RUNTIME_CONFIG_DIR=""
RUNTIME_CONFIG_SNAPSHOT=""
RUNTIME_CONFIG_SHA256=""
WUKONGIM_BIN="${WK_WUKONGIM_SINGLE_NODE_BIN:-$ROOT_DIR/data/wukongim-single-node/wukongim}"
WUKONGIM_LOG_ROOT="${WK_WUKONGIM_SINGLE_NODE_LOG_DIR:-$ROOT_DIR/data/wukongim-single-node-logs}"
WUKONGIM_LOG_DIR="$WUKONGIM_LOG_ROOT"
ACTIVE_CLUSTER_TAG=""
ACTIVE_CLUSTER_START_LOG=""
CLUSTER_GENERATION_INDEX=0
SEALED_WUKONGIM_RELATIVE=bin/wukongim
SEALED_WKBENCH_RELATIVE=bin/wkbench

CHANNELS="${WK_BENCH_CHANNELS:-1000}"
USERS="${WK_BENCH_USERS:-2500}"
GROUP_MEMBERS="${WK_BENCH_GROUP_MEMBERS:-10}"
CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"
PAYLOAD_BYTES="${WK_BENCH_PAYLOAD_BYTES:-128}"
DURATION="${WK_BENCH_DURATION:-5m}"
WARMUP="${WK_BENCH_WARMUP:-60s}"
COOLDOWN="${WK_BENCH_COOLDOWN:-90s}"
STABLE_P99="${WK_BENCH_STABLE_P99:-400ms}"
ACTUAL_QPS_MIN_RATIO="${WK_BENCH_ACTUAL_QPS_MIN_RATIO:-0.90}"
ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"
RECV_ACK="${WK_BENCH_RECV_ACK:-true}"
HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"
PROFILE_SECONDS="${WK_BENCH_PROFILE_SECONDS:-10}"
SENDER_PICK="${WK_BENCH_SENDER_PICK:-round_robin}"
PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"
RUNTIME_POOL_SAMPLE_INTERVAL="${WK_BENCH_RUNTIME_POOL_SAMPLE_INTERVAL:-1}"
RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"
LIFECYCLE_SAMPLE_INTERVAL="${WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL:-1}"
TERMINAL_CUT_POLL_INTERVAL="${WK_BENCH_TERMINAL_CUT_POLL_INTERVAL:-1}"
# Reserve the final 15 seconds of the reviewed 90-second cooldown for the
# post-ACK receive reproof, reader join, and 2,500-session stop sequence.
TERMINAL_CUT_ACK_SAFETY_SECONDS="${WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS:-15}"
STORAGE_OVERLAP_SAMPLE_INTERVAL="${WK_BENCH_STORAGE_OVERLAP_SAMPLE_INTERVAL:-20}"
HOST_OVERLAP_DETECTOR="$ROOT_DIR/scripts/chat-lifecycle/detect-local-workload-overlap.sh"
STORAGE_OVERLAP_CAPTURE="$ROOT_DIR/scripts/chat-lifecycle/capture-local-storage-overlap.sh"
THRESHOLD_PROFILE_HELPER="$ROOT_DIR/scripts/capture-wukongim-local-threshold-pprof.sh"
MAIN_SHELL_PID="$$"
MINIMUM_FREE_PERCENT="${WK_BENCH_MINIMUM_FREE_PERCENT:-10}"
HOST_METRICS_LISTEN="${WK_BENCH_SINGLE_NODE_HOST_METRICS_LISTEN:-127.0.0.1:19131}"
HOST_METRICS_ADDR="${WK_BENCH_SINGLE_NODE_HOST_METRICS_ADDR:-http://$HOST_METRICS_LISTEN}"
SINGLE_NODE_DATA_DIR="${WK_WUKONGIM_SINGLE_NODE_DATA_DIR:-$ROOT_DIR/data/wukongim-single-node-data}"
CANONICAL_SINGLE_NODE_DATA_DIR=""
DATA_FILESYSTEM_DEVICE=unavailable
DATA_FILESYSTEM_TOTAL_BLOCKS=0
DATA_FILESYSTEM_BLOCK_SIZE=0

WKBENCH_BUILT_FROM_CURRENT_SOURCE=false
WUKONGIM_BUILT_FROM_CURRENT_SOURCE=false
SOURCE_INITIAL_VALID=false
SOURCE_INITIAL_REVISION=unknown
SOURCE_INITIAL_CLEAN=false
SOURCE_POST_BUILD_VALID=false
SOURCE_POST_BUILD_REVISION=unknown
SOURCE_POST_BUILD_CLEAN=false
SOURCE_FINAL_VALID=false
SOURCE_FINAL_REVISION=unknown
SOURCE_FINAL_CLEAN=false
WK_BENCH_BUILD_DIR=""
SOURCE_STATE_DIR=""
BASELINE_INVOCATION_ID=""
SEALED_WUKONGIM_SHA256=""
ACTIVE_CLUSTER_GENERATION_NUMBER=0
ACTIVE_CLUSTER_PRESPAWN_SHA256=""
ACTIVE_CLUSTER_PRESPAWN_STAGE=""

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5001}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5100}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-single-node-1000ch.sh [options]

Starts a local cmd/wukongim single-node cluster, then runs fixed multi-channel
wkbench traffic against it.

Options:
  --qps LIST             Comma-separated offered SEND/s list. Default: 250,500,750,1000.
  --out-dir DIR          Evidence output directory.
  --wkbench-bin PATH     wkbench binary path. Default: data/wkbench-test.
  --worker-addr URL      Worker control URL. Default: http://127.0.0.1:19130.
  --worker-listen ADDR   Temporary worker listen address. Default: 127.0.0.1:19130.
  --no-worker            Do not start a temporary worker; require --worker-addr to be reachable.
  --no-start             Use an already-running single-node cluster for exactly one --qps value.
  --no-clean             When starting the cluster, keep existing node data.
  --start-script PATH    Single-node startup script. Default: scripts/start-wukongim-single-node.sh.
  --ready-timeout SECS   Cluster ready wait timeout. Default: 90.
  --channels N           Fixed group channel count. Default: 1000.
  --users N              Online connection population, range 1..2500. Default: 2500.
  --members N            Members per group channel. Default: 10.
  --concurrency N        wkbench send concurrency. Default: 2800.
  --duration DURATION    Measured run duration. Default: 5m.
  --warmup DURATION      Warmup duration. Default: 60s.
  --cooldown DURATION    Post-generation drain budget. Default: 90s.
  --stable-p99 DURATION  Soft p99 gate written into scenarios. Default: 400ms.
                         Summary PASS also requires actual/offered >= WK_BENCH_ACTUAL_QPS_MIN_RATIO, default 0.90.
  --ack-timeout DURATION Per-SEND sendack wait timeout in generated traffic. Default: 15s.
  --phase-poll-timeout DURATION
                         Base wkbench worker phase poll timeout. Default: 30s.
  --profile-seconds N    Bounded CPU profile duration after the first typed measured threshold. Default: 10; range: 1..30.
  --recv-ack BOOL        Must be true: the reviewed external terminal cut requires
                         delivery ACK convergence. Default: true.
  --heartbeat BOOL       Whether benchmark clients send heartbeat pings. Default: true.
  --sender-pick MODE     Group sender selection: round_robin or first_online. Default: round_robin.
  --api LIST             Comma-separated API base URLs. Default: node 5001.
  --gateway LIST         Comma-separated WKProto gateway addresses. Default: 5100.
  --metrics LIST         Comma-separated metrics base URLs. Default: same as --api.
  --resource-interval SECS
                         Server process CPU/memory sample interval. 0 disables periodic sampling. Default: 1.
  -h, --help             Show this help.

Example:
  scripts/bench-wukongim-single-node-1000ch.sh --qps 2000,2400,2500

  # Reuse an already-running cluster for one diagnostic rate:
  scripts/bench-wukongim-single-node-1000ch.sh --no-start --qps 2000
USAGE
}

# ─── ANSI colors (disabled when not a tty) ───────────────────────────────────
if [[ -t 1 ]]; then
  C_RESET=$'\033[0m'
  C_BOLD=$'\033[1m'
  C_DIM=$'\033[2m'
  C_GREEN=$'\033[32m'
  C_RED=$'\033[31m'
  C_YELLOW=$'\033[33m'
  C_CYAN=$'\033[36m'
  C_MAGENTA=$'\033[35m'
  C_WHITE=$'\033[97m'
else
  C_RESET='' C_BOLD='' C_DIM='' C_GREEN='' C_RED=''
  C_YELLOW='' C_CYAN='' C_MAGENTA='' C_WHITE=''
fi

log() {
  printf '%s[bench-single-%sch]%s %s\n' "$C_CYAN" "$CHANNELS" "$C_RESET" "$*"
}

die() {
  printf '%s[bench-single-%sch] ERROR:%s %s\n' "$C_RED" "$CHANNELS" "$C_RESET" "$*" >&2
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

initialize_baseline_invocation_id() {
  local generated
  command -v od >/dev/null 2>&1 || die 'od is required to create the baseline invocation identity'
  generated="$(LC_ALL=C od -An -N16 -tx1 /dev/urandom | tr -d ' \n')" || \
    die 'failed to create the baseline invocation identity'
  [[ "$generated" =~ ^[0-9a-f]{32}$ ]] || die 'baseline invocation identity is invalid'
  BASELINE_INVOCATION_ID="$generated"
}

reject_topology_environment_overrides() {
  local name
  for name in \
    WK_NODE_ID \
    WK_CLUSTER_ID \
    WK_CLUSTER_LISTEN_ADDR \
    WK_CLUSTER_ADVERTISE_ADDR \
    WK_CLUSTER_SEEDS \
    WK_CLUSTER_JOIN_TOKEN \
    WK_CLUSTER_NODES \
    WK_API_LISTEN_ADDR \
    WK_EXTERNAL_TCPADDR \
    WK_GATEWAY_LISTENERS \
    WK_METRICS_ENABLE \
    WK_BENCH_API_ENABLE \
    WK_WUKONGIM_SINGLE_NODE_READY_URL \
    WK_DEBUG_API_ENABLE \
    WK_CLUSTER_INITIAL_SLOT_COUNT \
    WK_CLUSTER_HASH_SLOT_COUNT \
    WK_CLUSTER_SLOT_REPLICA_N \
    WK_CLUSTER_CHANNEL_REPLICA_N \
    WK_CLUSTER_CHANNEL_REACTOR_COUNT \
    WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS \
    WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS \
    WK_CLUSTER_CHANNEL_RPC_WORKERS \
    WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS \
    WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT \
    WK_CHANNEL_APPEND_SHARD_COUNT \
    WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE \
    WK_CHANNEL_APPEND_EFFECT_POOL_SIZE \
    WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY \
    WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW \
    WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS \
    WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS \
    WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES \
    WK_CLUSTER_COMMIT_COORDINATOR_SHARDS \
    WK_CLUSTER_COMMIT_COORDINATOR_SYNC \
    WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS \
    WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT \
    WK_GATEWAY_SEND_TIMEOUT
  do
    if declare -p "$name" >/dev/null 2>&1; then
      die "the reviewed single-node cluster rejects inherited topology override or endpoint override: $name"
    fi
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --qps)
      [[ $# -ge 2 ]] || die '--qps requires a value'
      QPS_LIST="$2"
      shift 2
      ;;
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
    --concurrency)
      [[ $# -ge 2 ]] || die '--concurrency requires a value'
      CONCURRENCY="$2"
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
    --stable-p99)
      [[ $# -ge 2 ]] || die '--stable-p99 requires a value'
      STABLE_P99="$2"
      shift 2
      ;;
    --ack-timeout)
      [[ $# -ge 2 ]] || die '--ack-timeout requires a value'
      ACK_TIMEOUT="$2"
      shift 2
      ;;
    --phase-poll-timeout)
      [[ $# -ge 2 ]] || die '--phase-poll-timeout requires a value'
      PHASE_POLL_TIMEOUT="$2"
      shift 2
      ;;
    --recv-ack)
      [[ $# -ge 2 ]] || die '--recv-ack requires a value'
      RECV_ACK="$2"
      shift 2
      ;;
    --heartbeat)
      [[ $# -ge 2 ]] || die '--heartbeat requires a value'
      HEARTBEAT_ENABLED="$2"
      shift 2
      ;;
    --profile-seconds)
      [[ $# -ge 2 ]] || die '--profile-seconds requires a value'
      PROFILE_SECONDS="$2"
      shift 2
      ;;
    --sender-pick)
      [[ $# -ge 2 ]] || die '--sender-pick requires a value'
      SENDER_PICK="$2"
      shift 2
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
    --resource-interval)
      [[ $# -ge 2 ]] || die '--resource-interval requires a value'
      RESOURCE_SAMPLE_INTERVAL="$2"
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

# Reject product/runtime overrides before the dedicated evidence directory is
# created. Harness-only WK_BENCH_* inputs remain governed by their own bounds.
reject_topology_environment_overrides

require_positive_int '--channels' "$CHANNELS"
require_positive_int '--users' "$USERS"
(( USERS <= 2500 )) || die "--users must not exceed terminal fence capacity 2500: $USERS"
require_positive_int '--members' "$GROUP_MEMBERS"
(( GROUP_MEMBERS > 1 )) || die '--members must be greater than one for the reviewed fanout objective'
require_positive_int '--concurrency' "$CONCURRENCY"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_nonnegative_number '--resource-interval' "$RESOURCE_SAMPLE_INTERVAL"
require_nonnegative_number 'WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL' "$LIFECYCLE_SAMPLE_INTERVAL"
awk -v interval="$LIFECYCLE_SAMPLE_INTERVAL" 'BEGIN {exit !(interval > 0 && interval <= 30)}' || \
  die 'WK_BENCH_LIFECYCLE_SAMPLE_INTERVAL must be within 0..30 seconds'
require_nonnegative_number 'WK_BENCH_TERMINAL_CUT_POLL_INTERVAL' "$TERMINAL_CUT_POLL_INTERVAL"
awk -v interval="$TERMINAL_CUT_POLL_INTERVAL" 'BEGIN {exit !(interval > 0 && interval <= 5)}' || \
  die 'WK_BENCH_TERMINAL_CUT_POLL_INTERVAL must be within 0..5 seconds'
require_positive_int 'WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS' "$TERMINAL_CUT_ACK_SAFETY_SECONDS"
(( TERMINAL_CUT_ACK_SAFETY_SECONDS >= 15 )) || \
  die 'WK_BENCH_TERMINAL_CUT_ACK_SAFETY_SECONDS must be at least 15 seconds'
require_positive_int 'WK_BENCH_STORAGE_OVERLAP_SAMPLE_INTERVAL' "$STORAGE_OVERLAP_SAMPLE_INTERVAL"
(( STORAGE_OVERLAP_SAMPLE_INTERVAL <= 20 )) || \
  die 'WK_BENCH_STORAGE_OVERLAP_SAMPLE_INTERVAL must be within 1..20 seconds'
require_nonnegative_number 'WK_BENCH_ACTUAL_QPS_MIN_RATIO' "$ACTUAL_QPS_MIN_RATIO"
awk -v ratio="$ACTUAL_QPS_MIN_RATIO" 'BEGIN {exit !(ratio >= 0.90 && ratio <= 1)}' || \
  die 'WK_BENCH_ACTUAL_QPS_MIN_RATIO must be within 0.90..1.00 for the reviewed local baseline'
require_positive_int 'WK_BENCH_MINIMUM_FREE_PERCENT' "$MINIMUM_FREE_PERCENT"
(( MINIMUM_FREE_PERCENT <= 100 )) || die "WK_BENCH_MINIMUM_FREE_PERCENT must not exceed 100: $MINIMUM_FREE_PERCENT"
[[ "$PROFILE_SECONDS" =~ ^[0-9]+$ ]] && (( PROFILE_SECONDS >= 1 && PROFILE_SECONDS <= 30 )) || \
  die "--profile-seconds must be an integer from 1 through 30: $PROFILE_SECONDS"
case "$SENDER_PICK" in
  first_online|round_robin)
    ;;
  *)
    die "--sender-pick must be first_online or round_robin: $SENDER_PICK"
    ;;
esac
case "$RECV_ACK" in
  true)
    ;;
  false)
    die '--recv-ack must be true for the reviewed external terminal cut'
    ;;
  *)
    die "--recv-ack must be true or false: $RECV_ACK"
    ;;
esac
case "$HEARTBEAT_ENABLED" in
  true|false)
    ;;
  *)
    die "--heartbeat must be true or false: $HEARTBEAT_ENABLED"
    ;;
esac

declare -a QPS_VALUES API_VALUES GATEWAY_VALUES METRICS_VALUES
split_csv "$QPS_LIST" QPS_VALUES
if [[ "$START_CLUSTER" -eq 0 && "${#QPS_VALUES[@]}" -ne 1 ]]; then
  die '--no-start cannot be combined with multiple --qps values; use one rate per invocation'
fi
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

WORKER_PID=""
CLUSTER_PID=""
RESOURCE_SAMPLER_PID=""
RUNTIME_POOL_SAMPLER_PID=""
RUNTIME_POOL_SAMPLER_STOP_FILE=""
LIFECYCLE_SAMPLER_PID=""
LIFECYCLE_SAMPLER_STOP_FILE=""
LIFECYCLE_SAMPLER_START_FILE=""
LIFECYCLE_SAMPLER_STATUS_FILE=""
LIFECYCLE_SAMPLER_LOG_FILE=""
LIFECYCLE_SAMPLER_START_TOKEN=""
THRESHOLD_PROFILE_WATCHER_PID=""
THRESHOLD_PROFILE_WATCHER_STOP_FILE=""
TERMINAL_CUT_OBSERVER_PID=""
TERMINAL_CUT_OBSERVER_STOP_FILE=""
WAIT_CHILD_STATUS=0
OPERATOR_SIGNAL_STATUS=0
HOST_METRICS_PID=""
WORKER_WRITER_STOPPED=0
OWNED_WORKER_STARTED=0
OWNED_CLUSTER_STARTED=0

stop_worker_exact_from_status() {
  local reason="${1:-cleanup}"
  local status run_id assignment_id phase payload response
  if ! command -v jq >/dev/null 2>&1; then
    log "cannot stop worker exactly during $reason: jq is unavailable"
    return 1
  fi
  if ! status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status")"; then
    log "cannot read worker status during $reason"
    return 1
  fi
  if ! jq -e 'type == "object" and (.phase | type == "string") and (.assignment | type == "object")' >/dev/null <<<"$status"; then
    log "worker returned invalid status during $reason"
    return 1
  fi
  phase="$(jq -r '.phase // ""' <<<"$status")"
  run_id="$(jq -r '.assignment.run_id // ""' <<<"$status")"
  assignment_id="$(jq -r '.assignment.assignment_id // ""' <<<"$status")"
  if [[ "$phase" == "idle" && -z "$run_id" && -z "$assignment_id" ]]; then
    return 0
  fi
  if [[ -z "$run_id" || -z "$assignment_id" ]]; then
    log "refusing non-exact worker stop during $reason: phase=$phase run_id=${run_id:-missing} assignment_id=${assignment_id:-missing}"
    return 1
  fi
  payload="$(jq -cn --arg run_id "$run_id" --arg assignment_id "$assignment_id" '{run_id:$run_id,assignment_id:$assignment_id}')"
  if ! response="$(curl -fsS --connect-timeout 1 --max-time 15 -X POST -H 'Content-Type: application/json' --data "$payload" "${WORKER_ADDR%/}/v1/stop")"; then
    log "exact worker stop failed during $reason: run_id=$run_id assignment_id=$assignment_id"
    return 1
  fi
  if ! jq -e --arg run_id "$run_id" --arg assignment_id "$assignment_id" '
    .phase == "stopped" and
    ((.active_phase // "") == "") and
    (.assignment.run_id == $run_id) and
    (.assignment.assignment_id == $assignment_id)
  ' >/dev/null <<<"$response"; then
    log "worker stop was not terminal or changed identity during $reason: run_id=$run_id assignment_id=$assignment_id"
    return 1
  fi
}

wait_child_uninterrupted() {
  local pid="$1" status
  WAIT_CHILD_STATUS=0
  while true; do
    status=0
    wait "$pid" 2>/dev/null || status=$?
    if ! kill -0 "$pid" 2>/dev/null; then
      WAIT_CHILD_STATUS="$status"
      return 0
    fi
  done
}

operator_signal_exit() {
  local status="$1"
  OPERATOR_SIGNAL_STATUS="$status"
  exit "$status"
}

cleanup() {
  local original_status=$?
  # A further operator signal may interrupt wait(1), but must not abandon a
  # child owned by this wrapper while EXIT cleanup is joining it.
  trap 'OPERATOR_SIGNAL_STATUS=130' INT
  trap 'OPERATOR_SIGNAL_STATUS=143' TERM
  if declare -F terminate_terminal_cut_observer >/dev/null 2>&1; then
    terminate_terminal_cut_observer || true
  fi
  if declare -F terminate_threshold_profile_watcher >/dev/null 2>&1; then
    terminate_threshold_profile_watcher || true
  fi
  if declare -F stop_runtime_pool_sampler >/dev/null 2>&1; then
    stop_runtime_pool_sampler || true
  fi
  if declare -F stop_lifecycle_sampler >/dev/null 2>&1; then
    stop_lifecycle_sampler || true
  fi
  stop_server_resource_sampler || true
  stop_worker_writer "script cleanup" || true
  if declare -F discard_owned_worker_runtime_state >/dev/null 2>&1; then
    discard_owned_worker_runtime_state || true
  fi
  stop_host_metrics_writer || true
  stop_cluster_writer || true
  if declare -F discard_owned_wkbench_build >/dev/null 2>&1; then
    discard_owned_wkbench_build || true
  fi
  if declare -F discard_runtime_config_snapshot >/dev/null 2>&1; then
    discard_runtime_config_snapshot || true
  fi
  return "$original_status"
}

stop_host_metrics_writer() {
  if [[ -n "$HOST_METRICS_PID" ]]; then
    log "stopping host metrics pid=$HOST_METRICS_PID"
    kill "$HOST_METRICS_PID" >/dev/null 2>&1 || true
    wait "$HOST_METRICS_PID" 2>/dev/null || true
    HOST_METRICS_PID=""
  fi
}

stop_worker_writer() {
  local reason="${1:-cleanup}" stop_status=0
  [[ "$WORKER_WRITER_STOPPED" -eq 0 ]] || return 0
  if worker_ready; then
    stop_worker_exact_from_status "$reason" || stop_status=1
  elif [[ -n "$WORKER_PID" ]]; then
    stop_status=1
  fi
  if [[ -n "$WORKER_PID" ]]; then
    log "stopping temporary worker pid=$WORKER_PID"
    kill "$WORKER_PID" >/dev/null 2>&1 || true
    wait "$WORKER_PID" 2>/dev/null || true
    WORKER_PID=""
  fi
  if [[ "$stop_status" -eq 0 ]]; then
    WORKER_WRITER_STOPPED=1
  fi
  return "$stop_status"
}

discard_owned_worker_runtime_state() {
  local state_dir="$OUT_DIR/worker-state" state_file="$OUT_DIR/worker-state/current-run.json"
  [[ "$OWNED_WORKER_STARTED" -eq 1 ]] || return 0
  [[ ! -L "$state_dir" ]] || return 1
  [[ -d "$state_dir" ]] || return 0
  if [[ -e "$state_file" || -L "$state_file" ]]; then
    [[ -f "$state_file" && ! -L "$state_file" ]] || return 1
    rm -f -- "$state_file" || return 1
  fi
  rmdir "$state_dir"
}

stop_cluster_writer() {
  if [[ -n "$CLUSTER_PID" ]]; then
    log "stopping single-node cluster pid=$CLUSTER_PID"
    kill "$CLUSTER_PID" >/dev/null 2>&1 || true
    wait "$CLUSTER_PID" 2>/dev/null || true
    CLUSTER_PID=""
  fi
}

trap 'operator_signal_exit 130' INT
trap 'operator_signal_exit 143' TERM
trap cleanup EXIT

start_cluster_generation() {
  local qps="$1" tag generation_dir current_binary_sha
  tag="$(qps_tag "$qps")"
  verify_runtime_config_snapshot || die "private runtime config snapshot changed before qps=$qps"
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    log "cluster startup disabled; using existing cluster"
    ACTIVE_CLUSTER_TAG="$tag"
    ACTIVE_CLUSTER_START_LOG="$OUT_DIR/cluster-start.log"
    return
  fi
  [[ -x "$START_SCRIPT" ]] || die "start script is not executable: $START_SCRIPT"
  [[ -z "$CLUSTER_PID" ]] || die "previous product generation is still running before qps=$qps"
  ACTIVE_CLUSTER_GENERATION_NUMBER=$((CLUSTER_GENERATION_INDEX + 1))
  ACTIVE_CLUSTER_PRESPAWN_SHA256=""
  ACTIVE_CLUSTER_PRESPAWN_STAGE=pre_spawn
  if [[ "$CLUSTER_GENERATION_INDEX" -gt 0 ]]; then
    [[ -n "$SEALED_WUKONGIM_SHA256" && -f "$WUKONGIM_BIN" && ! -L "$WUKONGIM_BIN" && -x "$WUKONGIM_BIN" ]] || \
      die "sealed product executable is unavailable before generation=$tag"
    current_binary_sha="$(sha256_file "$WUKONGIM_BIN")" || \
      die "cannot hash product executable before generation=$tag"
    [[ "$current_binary_sha" == "$SEALED_WUKONGIM_SHA256" ]] || \
      die "product executable changed before generation=$tag"
    ACTIVE_CLUSTER_PRESPAWN_SHA256="$current_binary_sha"
  fi
  generation_dir="$OUT_DIR/cluster-generations/${tag}"
  WUKONGIM_LOG_DIR="$generation_dir/logs"
  ACTIVE_CLUSTER_TAG="$tag"
  ACTIVE_CLUSTER_START_LOG="$OUT_DIR/cluster-start.log"
  mkdir -p "$OUT_DIR/logs" "$generation_dir"
  set -- --ready-timeout "$READY_TIMEOUT"
  if [[ "$CLUSTER_GENERATION_INDEX" -eq 0 && "$CLEAN_CLUSTER" -eq 1 ]]; then
    set -- --clean "$@"
  fi
  if [[ "$CLUSTER_GENERATION_INDEX" -gt 0 ]]; then
    set -- --no-build "$@"
  fi
  local canonical_start default_start
  canonical_start="$(realpath -q "$START_SCRIPT" 2>/dev/null || true)"
  default_start="$(realpath -q "$ROOT_DIR/scripts/start-wukongim-single-node.sh" 2>/dev/null || true)"
  if [[ "$CLUSTER_GENERATION_INDEX" -eq 0 && -n "$canonical_start" && "$canonical_start" == "$default_start" ]]; then
    WUKONGIM_BUILT_FROM_CURRENT_SOURCE=true
  fi
  log "starting single-node cluster generation=$tag with $START_SCRIPT"
  # Preserve synchronous commits; the wider window only improves durable group-commit batching.
  # Keep server send timeout below the 15s client ACK wait so recovery can still write SENDACK.
  env -i \
  PATH="$PATH" \
  HOME="${HOME:-/tmp}" \
  TMPDIR="${TMPDIR:-/tmp}" \
  GOWORK="${GOWORK:-off}" \
  LC_ALL=C \
  WK_DEBUG_API_ENABLE=true \
  WK_CLUSTER_INITIAL_SLOT_COUNT=12 \
  WK_CLUSTER_HASH_SLOT_COUNT=256 \
  WK_CLUSTER_SLOT_REPLICA_N=1 \
  WK_CLUSTER_CHANNEL_REPLICA_N=1 \
  WK_CLUSTER_CHANNEL_REACTOR_COUNT=128 \
  WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500 \
  WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500 \
  WK_CLUSTER_CHANNEL_RPC_WORKERS=500 \
  WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=128 \
  WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=250us \
  WK_CHANNEL_APPEND_SHARD_COUNT=0 \
  WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE=0 \
  WK_CHANNEL_APPEND_EFFECT_POOL_SIZE=0 \
  WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=0 \
  WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=200us \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=0 \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=0 \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=131072 \
  WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=1 \
  WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=2048 \
  WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT=500us \
  WK_GATEWAY_SEND_TIMEOUT=14s \
  WK_CLUSTER_COMMIT_COORDINATOR_SYNC=true \
  WK_PROMETHEUS_ENABLE=true \
  WK_BENCH_API_TOKEN="$WK_BENCH_API_TOKEN" \
  WK_WUKONGIM_SINGLE_NODE_BIN="$WUKONGIM_BIN" \
  WK_WUKONGIM_SINGLE_NODE_CONFIG="$WUKONGIM_CONFIG" \
  WK_WUKONGIM_SINGLE_NODE_LOG_DIR="$WUKONGIM_LOG_DIR" \
  WK_WUKONGIM_SINGLE_NODE_DATA_DIR="$SINGLE_NODE_DATA_DIR" \
  WK_NODE_DATA_DIR="$SINGLE_NODE_DATA_DIR" \
    "$START_SCRIPT" "$@" \
      >"$ACTIVE_CLUSTER_START_LOG" 2>&1 &
  CLUSTER_PID="$!"
  OWNED_CLUSTER_STARTED=1
  CLUSTER_GENERATION_INDEX=$((CLUSTER_GENERATION_INDEX + 1))
}

attest_cluster_generation_ready() {
  local qps="$1" tag current_binary_sha deadline
  [[ "$START_CLUSTER" -eq 1 ]] || return 0
  tag="$(qps_tag "$qps")"
  deadline=$((SECONDS + READY_TIMEOUT))
  while [[ ! -f "$WUKONGIM_BIN" || -L "$WUKONGIM_BIN" || ! -x "$WUKONGIM_BIN" ]]; do
    if [[ -n "$CLUSTER_PID" ]] && ! kill -0 "$CLUSTER_PID" 2>/dev/null; then
      break
    fi
    (( SECONDS <= deadline )) || break
    sleep 0.05
  done
  if [[ ! -f "$WUKONGIM_BIN" || -L "$WUKONGIM_BIN" || ! -x "$WUKONGIM_BIN" ]]; then
    tail -n 120 "$ACTIVE_CLUSTER_START_LOG" >&2 || true
    die "product executable is unavailable after readiness for generation=$tag"
  fi
  current_binary_sha="$(sha256_file "$WUKONGIM_BIN")" || \
    die "cannot hash product executable after readiness for generation=$tag"
  if [[ -z "$SEALED_WUKONGIM_SHA256" ]]; then
    SEALED_WUKONGIM_SHA256="$current_binary_sha"
    ACTIVE_CLUSTER_PRESPAWN_SHA256="$current_binary_sha"
    ACTIVE_CLUSTER_PRESPAWN_STAGE=post_ready_first_generation
  fi
  [[ "$current_binary_sha" == "$SEALED_WUKONGIM_SHA256" &&
    "$ACTIVE_CLUSTER_PRESPAWN_SHA256" == "$SEALED_WUKONGIM_SHA256" ]] || \
    die "product executable changed at readiness for generation=$tag"
}

write_cluster_generation_executable_attestation() {
  local qps="$1" tag report_dir output temporary post_stop_sha
  [[ "$START_CLUSTER" -eq 1 ]] || return 0
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps/evidence"
  output="$report_dir/product-executable.tsv"
  temporary="$report_dir/.product-executable.tsv.next.$$"
  [[ -n "$SEALED_WUKONGIM_SHA256" && -n "$ACTIVE_CLUSTER_PRESPAWN_SHA256" &&
    -f "$WUKONGIM_BIN" && ! -L "$WUKONGIM_BIN" && -x "$WUKONGIM_BIN" ]] || return 1
  post_stop_sha="$(sha256_file "$WUKONGIM_BIN")" || return 1
  [[ "$post_stop_sha" == "$SEALED_WUKONGIM_SHA256" &&
    "$ACTIVE_CLUSTER_PRESPAWN_SHA256" == "$SEALED_WUKONGIM_SHA256" ]] || return 1
  mkdir -p "$report_dir" || return 1
  {
    printf 'schema\twukongim/chat-lifecycle-local-single-node-product-executable/v1\n'
    printf 'baseline_invocation_id\t%s\n' "$BASELINE_INVOCATION_ID"
    printf 'rate_tag\t%s\n' "$tag"
    printf 'generation\t%s\n' "$ACTIVE_CLUSTER_GENERATION_NUMBER"
    printf 'binary\t%s\n' "$SEALED_WUKONGIM_RELATIVE"
    printf 'source_config_sha256\t%s\n' "$RUNTIME_CONFIG_SHA256"
    printf 'pre_spawn_stage\t%s\n' "$ACTIVE_CLUSTER_PRESPAWN_STAGE"
    printf 'pre_spawn_sha256\t%s\n' "$ACTIVE_CLUSTER_PRESPAWN_SHA256"
    printf 'post_stop_sha256\t%s\n' "$post_stop_sha"
    printf 'sealed_binary_sha256\t%s\n' "$SEALED_WUKONGIM_SHA256"
  } >"$temporary" || { rm -f "$temporary"; return 1; }
  mv "$temporary" "$output"
}

stop_cluster_generation() {
  local qps="$1" tag report_dir
  tag="$(qps_tag "$qps")"
  [[ -z "$ACTIVE_CLUSTER_TAG" || "$ACTIVE_CLUSTER_TAG" == "$tag" ]] || \
    die "active product generation $ACTIVE_CLUSTER_TAG does not match qps=$qps"
  if [[ "$START_CLUSTER" -eq 1 ]]; then
    stop_cluster_writer
    write_cluster_generation_executable_attestation "$qps" || \
      die "product executable attestation failed for qps=$qps"
  fi
  report_dir="$OUT_DIR/reports/${tag}-qps/logs/product"
  mkdir -p "$report_dir"
  if ! cp "$WUKONGIM_LOG_DIR/node1.log" "$report_dir/node1.log" 2>/dev/null; then
    printf 'log_unavailable source=%s\n' "$WUKONGIM_LOG_DIR/node1.log" >"$report_dir/node1.log"
  fi
  if [[ -f "$ACTIVE_CLUSTER_START_LOG" ]]; then
    cp "$ACTIVE_CLUSTER_START_LOG" "$report_dir/cluster-start.log" 2>/dev/null || true
  fi
  ACTIVE_CLUSTER_TAG=""
  ACTIVE_CLUSTER_START_LOG=""
}

start_host_metrics() {
  [[ -d "$SINGLE_NODE_DATA_DIR" ]] || die "single-node data directory is missing: $SINGLE_NODE_DATA_DIR"
  mkdir -p "$OUT_DIR/logs"
  "$WK_BENCH_BIN" host-metrics --listen "$HOST_METRICS_LISTEN" \
    --path "$SINGLE_NODE_DATA_DIR" --mountpoint /var/lib/wukongim-local \
    --device /dev/wukongim-local --physical-io=true \
    >"$OUT_DIR/logs/host-metrics.log" 2>&1 &
  HOST_METRICS_PID="$!"
  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    kill -0 "$HOST_METRICS_PID" 2>/dev/null || die 'single-node host metrics exited before readiness'
    if curl -fsS --max-time 2 "${HOST_METRICS_ADDR%/}/healthz" >/dev/null 2>&1; then
      return
    fi
    sleep 1
  done
  die 'single-node host metrics readiness timed out'
}

ensure_wkbench_binary() {
  if [[ "$START_WORKER" -eq 1 ]]; then
    WK_BENCH_BUILD_DIR="$(mktemp -d "$OUT_DIR/.wkbench-build.XXXXXX")" || \
      die 'cannot create dedicated wkbench build directory'
    WK_BENCH_BIN="$WK_BENCH_BUILD_DIR/wkbench"
    log "building owned wkbench from current source into dedicated OUT_DIR"
    (
      cd "$ROOT_DIR"
      GOWORK="${GOWORK:-off}" go build -o "$WK_BENCH_BIN" ./cmd/wkbench
    )
    WKBENCH_BUILT_FROM_CURRENT_SOURCE=true
    return
  fi
  [[ -f "$WK_BENCH_BIN" && ! -L "$WK_BENCH_BIN" && -x "$WK_BENCH_BIN" ]] || \
    die "external wkbench binary is not a regular executable: $WK_BENCH_BIN"
}

worker_ready() {
  curl -fsS --max-time 2 "${WORKER_ADDR%/}/healthz" >/dev/null 2>&1
}

gateway_ready() {
  local addr="$1"
  local host="${addr%:*}"
  local port="${addr##*:}"
  if [[ -z "$host" || -z "$port" || "$host" == "$addr" ]]; then
    return 1
  fi
  ( : >/dev/tcp/"$host"/"$port" ) >/dev/null 2>&1
}

ensure_worker() {
  if worker_ready; then
    # The reachable worker was not started from the binary sealed by this run.
    # Preserve its usefulness for explicitly external diagnostics, but never
    # claim that the complete measured process set is rebuildable from source.
    WKBENCH_BUILT_FROM_CURRENT_SOURCE=false
    log "using existing worker: $WORKER_ADDR"
    return
  fi
  if [[ "$START_WORKER" -eq 0 ]]; then
    die "worker is not reachable at $WORKER_ADDR"
  fi
  local worker_dir="$OUT_DIR/worker-state"
  mkdir -p "$worker_dir"
  log "starting temporary worker: $WORKER_LISTEN"
  "$WK_BENCH_BIN" worker --listen "$WORKER_LISTEN" --work-dir "$worker_dir" --insecure-control \
    >"$OUT_DIR/logs/worker.log" 2>&1 &
  WORKER_PID="$!"
  OWNED_WORKER_STARTED=1
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

ensure_local_bench_api_token() {
  local generated
  if [[ -n "${WK_BENCH_API_TOKEN:-}" ]]; then
    [[ "$WK_BENCH_API_TOKEN" != *$'\n'* && "$WK_BENCH_API_TOKEN" != *$'\r'* ]] || return 1
    [[ "$WK_BENCH_API_TOKEN" == "${WK_BENCH_API_TOKEN#${WK_BENCH_API_TOKEN%%[![:space:]]*}}" ]] || return 1
    [[ "$WK_BENCH_API_TOKEN" == "${WK_BENCH_API_TOKEN%${WK_BENCH_API_TOKEN##*[![:space:]]}}" ]] || return 1
    export WK_BENCH_API_TOKEN
    return 0
  fi
  command -v od >/dev/null 2>&1 || return 1
  generated="$(LC_ALL=C od -An -N32 -tx1 /dev/urandom | tr -d ' \n')" || return 1
  [[ "$generated" =~ ^[0-9a-f]{64}$ ]] || return 1
  WK_BENCH_API_TOKEN="$generated"
  export WK_BENCH_API_TOKEN
}

check_cluster_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local api gateway all_ready
  while (( SECONDS <= deadline )); do
    if [[ -n "$CLUSTER_PID" ]] && ! kill -0 "$CLUSTER_PID" 2>/dev/null; then
      tail -n 120 "$OUT_DIR/cluster-start.log" >&2 || true
      die "single-node cluster exited before becoming ready"
    fi
    all_ready=1
    for api in "${API_VALUES[@]}"; do
      if ! curl -fsS --max-time 3 "${api%/}/readyz" >/dev/null 2>&1; then
        all_ready=0
        break
      fi
    done
    if [[ "$all_ready" -eq 1 ]]; then
      for gateway in "${GATEWAY_VALUES[@]}"; do
        if ! gateway_ready "$gateway"; then
          all_ready=0
          break
        fi
      done
    fi
    if [[ "$all_ready" -eq 1 ]]; then
      log "cluster ready"
      return
    fi
    sleep 1
  done
  tail -n 120 "$OUT_DIR/cluster-start.log" >&2 || true
  die "timed out waiting for cluster readyz"
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
name: local-single-node-cluster
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
    cat <<'YAML'
  # Expanded in-memory by wkbench; the evidence file never contains the token.
  token: "${WK_BENCH_API_TOKEN}"
metrics:
  enabled: true
  addrs:
YAML
    yaml_list METRICS_VALUES
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

qps_tag() {
  local qps="$1"
  if [[ "$qps" =~ ^[0-9]+$ ]]; then
    printf '%06d' "$qps"
    return
  fi
  printf '%s' "$qps" | tr '.' 'p'
}

rate_per_channel() {
  local qps="$1"
  awk -v qps="$qps" -v channels="$CHANNELS" 'BEGIN { printf "%.6g", qps / channels }'
}

online_fanout_qps() {
  local qps="$1"
  awk -v qps="$qps" -v members="$GROUP_MEMBERS" 'BEGIN { printf "%.6g", qps * (members - 1) }'
}

write_scenario() {
  local qps="$1"
  local tag="$2"
  local report_dir="$3"
  local rate fanout
  rate="$(rate_per_channel "$qps")"
  fanout="$(online_fanout_qps "$qps")"
  cat >"$OUT_DIR/scenario-${tag}.yaml" <<YAML
version: wkbench/v1
run:
  id: single-node-${BASELINE_INVOCATION_ID}-fixed-${CHANNELS}ch-${tag}-qps
  duration: $DURATION
  warmup: $WARMUP
  cooldown: $COOLDOWN
  external_terminal_cut: true
  random_seed: 0
  fail_fast: true
  report_dir: $report_dir
objectives:
  scale: small
  standard: false
  ingress_qps: ${qps}/s
  online_fanout_qps: ${fanout}/s
  tolerance_ratio: 0.1
limits:
  fail_on_soft: false
  hard:
    max_worker_failed: 0
    max_connect_error_rate: 0
    max_sendack_error_rate: 0
    max_recv_verify_error_rate: 0
  soft:
    max_sendack_p99: $STABLE_P99
    max_recv_p99: 0s
identity:
  uid_prefix: bench${tag}-u
  device_prefix: bench${tag}-d
  client_msg_prefix: bench${tag}-msg
  token:
    mode: bench_api
online:
  total_users: $USERS
  connect_rate: 1000/s
  gateway_balance: round_robin
  heartbeat:
    enabled: $HEARTBEAT_ENABLED
    interval: 30s
    timeout: 5s
channels:
  profiles:
    - name: thousand-groups
      channel_type: group
      count: $CHANNELS
      members:
        count: $GROUP_MEMBERS
        overlap: allowed
      online:
        member_ratio: 1
      shard:
        mode: hash
      prepare:
        subscribers_batch_size: 1000
cleanup:
  enabled: false
messages:
  payload:
    size_bytes: $PAYLOAD_BYTES
    mode: deterministic
  traffic:
    - name: group-send
      channel_ref: thousand-groups
      rate_per_channel: ${rate}/s
      concurrency: $CONCURRENCY
      ack_timeout: $ACK_TIMEOUT
      retry:
        enabled: true
      sender_pick: $SENDER_PICK
      recv_ack: $RECV_ACK
      verify:
        recv:
          mode: none
YAML
}

duration_seconds() {
  local value="$1"
  if [[ "$value" =~ ^([0-9]+([.][0-9]+)?)ms$ ]]; then
    awk -v ms="${value%ms}" 'BEGIN { printf "%.6g\n", ms / 1000 }'
    return
  fi
  if [[ "$value" =~ ^([0-9]+([.][0-9]+)?)s$ ]]; then
    printf '%s\n' "${value%s}"
    return
  fi
  if [[ "$value" =~ ^([0-9]+([.][0-9]+)?)m$ ]]; then
    awk -v minutes="${value%m}" 'BEGIN { printf "%.6g\n", minutes * 60 }'
    return
  fi
  die "duration currently supports seconds or minutes only: $value"
}

metric_file_id() {
  local raw="$1"
  raw="${raw#http://}"
  raw="${raw#https://}"
  printf '%s' "$raw" | tr -c 'A-Za-z0-9' '_'
}

product_queue_cut_metadata() {
  local status
  status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status")" || return 1
  jq -ce '
    if type == "object" and
      ((.observed_at | type) == "string") and ((.observed_at | length) > 0) and
      ((.phase | type) == "string") and (((.active_phase // "") | type) == "string") and
      ((.assignment | type) == "object") and
      ((.assignment.run_id | type) == "string") and ((.assignment.run_id | length) > 0) and
      ((.assignment.assignment_id | type) == "string") and ((.assignment.assignment_id | length) > 0) and
      ((.lifecycle.receive_drain_sha256 | type) == "string") and
      (.lifecycle.receive_drain_sha256 | test("^[0-9a-f]{64}$"))
    then {
      schema:"wukongim/chat-lifecycle-local-single-node-product-queue-cut/v1",
      observed_at:.observed_at,
      run_id:.assignment.run_id,
      assignment_id:.assignment.assignment_id,
      phase:.phase,
      active_phase:(.active_phase // ""),
      receive_drain_sha256:.lifecycle.receive_drain_sha256
    } else error("worker status is not an exact lifecycle cut") end
  ' <<<"$status"
}

product_queue_cut_from_metrics() {
  local metrics_file="$1"
  awk '
    index($0, "# wkbench_local_single_node_cut ") == 1 {
      print substr($0, length("# wkbench_local_single_node_cut ") + 1)
      exit
    }
  ' "$metrics_file"
}

scrape_metrics() {
  local tag="$1"
  local phase="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  mkdir -p "$metrics_dir"
  local addr id cut temporary
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    cut="$(product_queue_cut_metadata 2>/dev/null || true)"
    temporary="$metrics_dir/.${id}-${phase}.next.$$"
    {
      if [[ -n "$cut" ]]; then
        printf '# wkbench_local_single_node_cut %s\n' "$cut"
      fi
      curl -fsS "${addr%/}/metrics"
    } >"$temporary" && mv "$temporary" "$metrics_dir/${id}-${phase}.prom" || {
      rm -f "$temporary"
      return 1
    }
  done
  curl -fsS --max-time 6 "${HOST_METRICS_ADDR%/}/metrics" >"$metrics_dir/host-$phase.prom"
}

scrape_metrics_snapshot() {
  local phase="$1"
  local metrics_dir="$OUT_DIR/metrics/cluster"
  mkdir -p "$metrics_dir"
  local addr id
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    curl -fsS "${addr%/}/metrics" >"$metrics_dir/${id}-${phase}.prom" || true
  done
}

collect_node_logs() {
  local phase="$1"
  local dest="$OUT_DIR/logs/$phase"
  mkdir -p "$dest"
  if ! cp "$WUKONGIM_LOG_DIR/node1.log" "$dest/node1.log" 2>/dev/null; then
    printf 'log_unavailable source=%s\n' "$WUKONGIM_LOG_DIR/node1.log" >"$dest/node1.log"
  fi
  if [[ -f "$OUT_DIR/cluster-start.log" ]]; then
    cp "$OUT_DIR/cluster-start.log" "$dest/cluster-start.log" 2>/dev/null || true
  fi
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

is_nonnegative_number() {
  [[ "$1" =~ ^[0-9]+([.][0-9]+)?$ ]]
}

is_nonnegative_int() {
  [[ "$1" =~ ^[0-9]+$ ]]
}

server_pid_from_log() {
  local node="$1"
  local log_file="$OUT_DIR/cluster-start.log"
  [[ -f "$log_file" ]] || return 0
  awk -v node="node${node}" '
    index($0, node " pid=") {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^pid=/) {
          sub(/^pid=/, "", $i)
          pid = $i
        }
      }
    }
    index($0, "node pid=") {
      for (i = 1; i <= NF; i++) {
        if ($i ~ /^pid=/) {
          sub(/^pid=/, "", $i)
          pid = $i
        }
      }
    }
    END {
      if (pid != "") {
        print pid
      }
    }
  ' "$log_file"
}

server_pid_from_process_table() {
  local node="$1"
  local config="$ROOT_DIR/scripts/wukongim/wukongim.toml"
  pgrep -f "$config" 2>/dev/null | head -n 1 || true
}

server_pid_for_node() {
  local node="$1"
  local pid
  pid="$(server_pid_from_log "$node" || true)"
  if [[ -z "$pid" ]]; then
    pid="$(server_pid_from_process_table "$node")"
  fi
  printf '%s' "$pid"
}

process_start_token() {
  local pid="$1" stat tail token
  [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  if [[ -r "/proc/$pid/stat" ]]; then
    stat="$(LC_ALL=C command cat "/proc/$pid/stat" 2>/dev/null)" || return 1
    [[ "$stat" == *') '* ]] || return 1
    tail="${stat##*) }"
    token="$(awk '{print $20}' <<<"$tail")"
  else
    token="$(LC_ALL=C ps -p "$pid" -o lstart= 2>/dev/null | awk '{$1=$1; print}')"
  fi
  [[ -n "$token" ]] || return 1
  printf '%s\n' "$token"
}

process_evidence_json() {
  local pid="$1" token=""
  if [[ "$pid" =~ ^[0-9]+$ ]] && kill -0 "$pid" 2>/dev/null; then
    token="$(process_start_token "$pid" 2>/dev/null || true)"
  fi
  if [[ -n "$token" ]]; then
    jq -cn --argjson pid "$pid" --arg token "$token" '{pid:$pid,start_token:$token,alive:true}'
    return
  fi
  jq -cn '{pid:0,start_token:"",alive:false}'
}

write_lifecycle_capture_error() {
  local tag="$1" reason="$2" output sampled_at
  output="$OUT_DIR/reports/${tag}-qps/lifecycle-status.jsonl"
  sampled_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  jq -cn --arg sampled_at "$sampled_at" --arg reason "$reason" '
    {schema:"wukongim/chat-lifecycle-local-single-node-lifecycle-sample/v1",sampled_at:$sampled_at,error:$reason,
     server:{pid:0,start_token:"",alive:false},worker:{pid:0,start_token:"",alive:false}}
  ' >>"$output" || true
}

capture_lifecycle_sample() {
  local tag="$1" status sampled_at server_pid server_json worker_json output temporary lifecycle_line projected_line phase active_phase overlap pid
  local lifecycle_line_count=0
  local status_run_id status_assignment_id expected_run_id
  local -a owned_pids=()
  output="$OUT_DIR/reports/${tag}-qps/lifecycle-status.jsonl"
  sampled_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  if ! status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status")"; then
    write_lifecycle_capture_error "$tag" worker_status_unavailable
    return 1
  fi
  server_pid="$(server_pid_for_node 1)"
  active_phase="$(jq -r '.active_phase // ""' <<<"$status" 2>/dev/null || true)"
  if [[ "$active_phase" == run ]]; then
    for pid in "$MAIN_SHELL_PID" "$server_pid" "$WORKER_PID" "$HOST_METRICS_PID"; do
      [[ "$pid" =~ ^[1-9][0-9]*$ ]] && owned_pids+=("$pid")
    done
    if command -v pgrep >/dev/null 2>&1; then
      while IFS= read -r pid; do
        [[ "$pid" =~ ^[1-9][0-9]*$ ]] && owned_pids+=("$pid")
      done < <(pgrep -P "$MAIN_SHELL_PID" 2>/dev/null || true)
    fi
    if [[ ! -x "$HOST_OVERLAP_DETECTOR" ]] || ! overlap="$("$HOST_OVERLAP_DETECTOR" "${owned_pids[@]}")"; then
      : >"$OUT_DIR/reports/${tag}-qps/host-overlap.detected"
      write_lifecycle_capture_error "$tag" host_overlap_observer_error
      return 1
    fi
    if [[ -n "$overlap" ]]; then
      : >"$OUT_DIR/reports/${tag}-qps/host-overlap.detected"
      write_lifecycle_capture_error "$tag" host_overlap_detected
      return 1
    fi
  fi
  server_json="$(process_evidence_json "$server_pid")"
  worker_json="$(process_evidence_json "$WORKER_PID")"
  temporary="$OUT_DIR/reports/${tag}-qps/.lifecycle-status.next.$$"
  if ! jq -c \
    --arg sampled_at "$sampled_at" \
    --argjson server "$server_json" \
    --argjson worker "$worker_json" '
      {
        schema:"wukongim/chat-lifecycle-local-single-node-lifecycle-sample/v1",
        sampled_at:$sampled_at,
        status:{
          phase:(.phase // ""), active_phase:(.active_phase // ""),
          completed_phase:(.completed_phase // ""), last_error:(.last_error // ""),
          observed_at:(.observed_at // ""),
          lifecycle:(if (.lifecycle | type) == "object" then ({
            active_connections:(.lifecycle.active_connections // 0),
            terminal_pre_close:(.lifecycle.terminal_pre_close // false),
            terminal_cut_required:(.lifecycle.terminal_cut_required // false),
            terminal_cut_ready:(.lifecycle.terminal_cut_ready // false),
            receive_drain_sha256:(.lifecycle.receive_drain_sha256 // ""),
            terminal_cut:(if (.lifecycle.terminal_cut | type) == "object" then {
              run_id:(.lifecycle.terminal_cut.run_id // ""),
              assignment_id:(.lifecycle.terminal_cut.assignment_id // ""),
              ready_at:.lifecycle.terminal_cut.ready_at,
              deadline_at:.lifecycle.terminal_cut.deadline_at,
              observed_at:.lifecycle.terminal_cut.observed_at,
              receive_drain_sha256:(.lifecycle.terminal_cut.receive_drain_sha256 // ""),
              product_metrics_sha256:(.lifecycle.terminal_cut.product_metrics_sha256 // ""),
              storage_overlap_sha256:(.lifecycle.terminal_cut.storage_overlap_sha256 // ""),
              acknowledged_at:.lifecycle.terminal_cut.acknowledged_at
            } else null end),
            traffic:(.lifecycle.traffic // {}),
            receive_drain:(if (.lifecycle.receive_drain | type) == "object" then {
              required:(.lifecycle.receive_drain.required // false),
              evidence_complete:(.lifecycle.receive_drain.evidence_complete // false),
              drain_complete:(.lifecycle.receive_drain.drain_complete // false),
              client_count:(.lifecycle.receive_drain.client_count // 0),
              active_drains:(.lifecycle.receive_drain.active_drains // 0),
              queue_snapshot_clients:(.lifecycle.receive_drain.queue_snapshot_clients // 0),
              inner_recv_depth:(.lifecycle.receive_drain.inner_recv_depth // 0),
              inner_recv_handoffs:(.lifecycle.receive_drain.inner_recv_handoffs // 0),
              adapter_queue_depth:(.lifecycle.receive_drain.adapter_queue_depth // 0),
              adapter_handoffs:(.lifecycle.receive_drain.adapter_handoffs // 0),
              matching_buffer_depth:(.lifecycle.receive_drain.matching_buffer_depth // 0),
              foreground_matchers:(.lifecycle.receive_drain.foreground_matchers // 0),
              read_frames_inflight:(.lifecycle.receive_drain.read_frames_inflight // 0),
              recvacks_inflight:(.lifecycle.receive_drain.recvacks_inflight // 0),
              publications_inflight:(.lifecycle.receive_drain.publications_inflight // 0),
              publication_waiters:(.lifecycle.receive_drain.publication_waiters // 0),
              recvack_failures:(.lifecycle.receive_drain.recvack_failures // 0),
              recvack_successes:(.lifecycle.receive_drain.recvack_successes // 0),
	              read_failures:(.lifecycle.receive_drain.read_failures // 0),
	              receive_frames_observed:(.lifecycle.receive_drain.receive_frames_observed // 0),
	              buffered_frames_drained:(.lifecycle.receive_drain.buffered_frames_drained // 0),
	              fanout_proof:(if (.lifecycle.receive_drain.fanout_proof | type) == "object" then {
	                version:(.lifecycle.receive_drain.fanout_proof.version // ""),
	                required:(.lifecycle.receive_drain.fanout_proof.required // false),
	                evidence_complete:(.lifecycle.receive_drain.fanout_proof.evidence_complete // false),
	                logical_sendacks:(.lifecycle.receive_drain.fanout_proof.logical_sendacks // 0),
	                expected:(.lifecycle.receive_drain.fanout_proof.expected // {}),
	                received:(.lifecycle.receive_drain.fanout_proof.received // {}),
	                recvacked:(.lifecycle.receive_drain.fanout_proof.recvacked // {})
	              } else {} end),
	              stable_zero_observations:(.lifecycle.receive_drain.stable_zero_observations // 0)
            } else {} end)
          }
          + (if ((.lifecycle.terminal_cut_ready_at // "") | type) == "string" and
                    ((.lifecycle.terminal_cut_ready_at // "") | length) > 0
             then {terminal_cut_ready_at:.lifecycle.terminal_cut_ready_at} else {} end)
          + (if ((.lifecycle.terminal_cut_deadline_at // "") | type) == "string" and
                    ((.lifecycle.terminal_cut_deadline_at // "") | length) > 0
             then {terminal_cut_deadline_at:.lifecycle.terminal_cut_deadline_at} else {} end))
          else null end),
          assignment:{run_id:(.assignment.run_id // ""),assignment_id:(.assignment.assignment_id // "")}
        },
        server:$server,
        worker:$worker
      }
    ' <<<"$status" >"$temporary"; then
    rm -f "$temporary"
    write_lifecycle_capture_error "$tag" worker_status_invalid
    return 1
  fi
  while IFS= read -r projected_line; do
    lifecycle_line_count=$((lifecycle_line_count + 1))
    lifecycle_line="$projected_line"
    [[ "$lifecycle_line_count" -eq 1 ]] || break
  done <"$temporary"
  if [[ "$lifecycle_line_count" -ne 1 || -z "$lifecycle_line" ]]; then
    rm -f "$temporary"
    write_lifecycle_capture_error "$tag" worker_status_invalid
    return 1
  fi
  if ! printf '%s\n' "$lifecycle_line" >>"$output"; then
    rm -f "$temporary"
    write_lifecycle_capture_error "$tag" lifecycle_capture_write_failed
    return 1
  fi
  rm -f "$temporary"
  status_run_id="$(jq -r '.assignment.run_id // ""' <<<"$status" 2>/dev/null || true)"
  status_assignment_id="$(jq -r '.assignment.assignment_id // ""' <<<"$status" 2>/dev/null || true)"
  expected_run_id="single-node-${BASELINE_INVOCATION_ID}-fixed-${CHANNELS}ch-${tag}-qps"
  if [[ "$status_run_id" != "$expected_run_id" || -z "$status_assignment_id" ]]; then
    return 0
  fi
  phase="$(jq -r '.phase // ""' <<<"$status" 2>/dev/null || true)"
  case "$active_phase" in
    run) write_threshold_profile_phase "$tag" measurement || true ;;
    cooldown) write_threshold_profile_phase "$tag" drain || true ;;
    *)
      if [[ "$phase" == stopped ]]; then
        write_threshold_profile_phase "$tag" shutdown || true
      fi
      ;;
  esac
}

lifecycle_sampler_stop_file() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/lifecycle-sampler.stop"
}

lifecycle_sampler_start_file() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/.lifecycle-sampler.start"
}

lifecycle_sampler_status_file() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/lifecycle-sampler-status.json"
}

lifecycle_sampler_log_file() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/lifecycle-sampler.log"
}

write_lifecycle_sampler_status() {
  local status_file="$1" pid="$2" start_token="$3" attempts="$4" completions="$5" exit_status="$6" reason="$7" temporary
  [[ "$pid" =~ ^[1-9][0-9]*$ && -n "$start_token" ]] || return 1
  [[ "$attempts" =~ ^[0-9]+$ && "$completions" =~ ^[0-9]+$ && "$exit_status" =~ ^[0-9]+$ ]] || return 1
  (( completions <= attempts && exit_status <= 255 )) || return 1
  case "$reason" in
    starting|running|capturing|capture_failed|stopping|stopped|unexpected_exit) ;;
    *) return 1 ;;
  esac
  temporary="$(mktemp "${status_file}.next.XXXXXX")" || return 1
  if ! jq -cn \
    --arg schema 'wukongim/chat-lifecycle-local-single-node-sampler-status/v1' \
    --argjson pid "$pid" \
    --arg start_token "$start_token" \
    --argjson attempts "$attempts" \
    --argjson completions "$completions" \
    --argjson exit_status "$exit_status" \
    --arg reason "$reason" \
    '{schema:$schema,pid:$pid,start_token:$start_token,attempts:$attempts,completions:$completions,exit_status:$exit_status,reason:$reason}' \
    >"$temporary" || ! mv "$temporary" "$status_file"; then
    rm -f "$temporary"
    return 1
  fi
}

lifecycle_sampler_loop() {
  local tag="$1" stop_file="$2"
  local capture_status reason
  write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_CHILD_STATUS_FILE" \
    "$LIFECYCLE_SAMPLER_CHILD_PID" "$LIFECYCLE_SAMPLER_CHILD_START_TOKEN" 0 0 0 running || return 70
  while [[ ! -f "$stop_file" ]]; do
    LIFECYCLE_SAMPLER_CHILD_ATTEMPTS=$((LIFECYCLE_SAMPLER_CHILD_ATTEMPTS + 1))
    write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_CHILD_STATUS_FILE" \
      "$LIFECYCLE_SAMPLER_CHILD_PID" "$LIFECYCLE_SAMPLER_CHILD_START_TOKEN" \
      "$LIFECYCLE_SAMPLER_CHILD_ATTEMPTS" "$LIFECYCLE_SAMPLER_CHILD_COMPLETIONS" 0 capturing || return 70
    capture_status=0
    capture_lifecycle_sample "$tag" || capture_status=$?
    LIFECYCLE_SAMPLER_CHILD_COMPLETIONS=$((LIFECYCLE_SAMPLER_CHILD_COMPLETIONS + 1))
    reason=running
    [[ "$capture_status" -eq 0 ]] || reason=capture_failed
    write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_CHILD_STATUS_FILE" \
      "$LIFECYCLE_SAMPLER_CHILD_PID" "$LIFECYCLE_SAMPLER_CHILD_START_TOKEN" \
      "$LIFECYCLE_SAMPLER_CHILD_ATTEMPTS" "$LIFECYCLE_SAMPLER_CHILD_COMPLETIONS" 0 "$reason" || return 70
    sleep "$LIFECYCLE_SAMPLE_INTERVAL" || true
  done
  write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_CHILD_STATUS_FILE" \
    "$LIFECYCLE_SAMPLER_CHILD_PID" "$LIFECYCLE_SAMPLER_CHILD_START_TOKEN" \
    "$LIFECYCLE_SAMPLER_CHILD_ATTEMPTS" "$LIFECYCLE_SAMPLER_CHILD_COMPLETIONS" 0 stopping || return 70
}

lifecycle_sampler_process() {
  local tag="$1" stop_file="$2" start_file="$3" status_file="$4"
  local identity loop_status=0 reason=unexpected_exit
  while [[ ! -f "$start_file" ]]; do
    if [[ -f "$stop_file" ]] || ! kill -0 "$MAIN_SHELL_PID" 2>/dev/null; then
      return 70
    fi
    sleep 0.01 || true
  done
  rm -f "$start_file"
  if ! identity="$(jq -er \
    'select(.schema == "wukongim/chat-lifecycle-local-single-node-sampler-status/v1" and (.pid | type) == "number" and .pid > 0 and (.start_token | type) == "string" and (.start_token | length) > 0) | [.pid,.start_token] | @tsv' \
    "$status_file")"; then
    printf 'lifecycle sampler start status is unavailable\n' >&2
    return 70
  fi
  IFS=$'\t' read -r LIFECYCLE_SAMPLER_CHILD_PID LIFECYCLE_SAMPLER_CHILD_START_TOKEN <<<"$identity"
  LIFECYCLE_SAMPLER_CHILD_STATUS_FILE="$status_file"
  LIFECYCLE_SAMPLER_CHILD_ATTEMPTS=0
  LIFECYCLE_SAMPLER_CHILD_COMPLETIONS=0
  lifecycle_sampler_loop "$tag" "$stop_file" || loop_status=$?
  if [[ "$LIFECYCLE_SAMPLER_CHILD_ATTEMPTS" -ne "$LIFECYCLE_SAMPLER_CHILD_COMPLETIONS" ]]; then
    loop_status=70
  elif [[ "$loop_status" -eq 0 && -f "$stop_file" ]]; then
    reason=stopped
  elif [[ "$loop_status" -eq 0 ]]; then
    loop_status=70
  fi
  if ! write_lifecycle_sampler_status "$status_file" \
    "$LIFECYCLE_SAMPLER_CHILD_PID" "$LIFECYCLE_SAMPLER_CHILD_START_TOKEN" \
    "$LIFECYCLE_SAMPLER_CHILD_ATTEMPTS" "$LIFECYCLE_SAMPLER_CHILD_COMPLETIONS" "$loop_status" "$reason"; then
    printf 'lifecycle sampler final status write failed\n' >&2
    [[ "$loop_status" -ne 0 ]] || loop_status=70
  fi
  return "$loop_status"
}

start_lifecycle_sampler() {
  local tag="$1" start_token start_status=0
  LIFECYCLE_SAMPLER_STOP_FILE="$(lifecycle_sampler_stop_file "$tag")"
  LIFECYCLE_SAMPLER_START_FILE="$(lifecycle_sampler_start_file "$tag")"
  LIFECYCLE_SAMPLER_STATUS_FILE="$(lifecycle_sampler_status_file "$tag")"
  LIFECYCLE_SAMPLER_LOG_FILE="$(lifecycle_sampler_log_file "$tag")"
  rm -f "$LIFECYCLE_SAMPLER_STOP_FILE" "$LIFECYCLE_SAMPLER_START_FILE"
  : >"$OUT_DIR/reports/${tag}-qps/lifecycle-status.jsonl"
  : >"$LIFECYCLE_SAMPLER_LOG_FILE"
  lifecycle_sampler_process "$tag" "$LIFECYCLE_SAMPLER_STOP_FILE" \
    "$LIFECYCLE_SAMPLER_START_FILE" "$LIFECYCLE_SAMPLER_STATUS_FILE" \
    >>"$LIFECYCLE_SAMPLER_LOG_FILE" 2>&1 &
  LIFECYCLE_SAMPLER_PID="$!"
  if ! start_token="$(process_start_token "$LIFECYCLE_SAMPLER_PID")"; then
    start_status=70
  elif ! write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_STATUS_FILE" \
    "$LIFECYCLE_SAMPLER_PID" "$start_token" 0 0 0 starting; then
    start_status=70
  elif ! touch "$LIFECYCLE_SAMPLER_START_FILE"; then
    start_status=70
  fi
  LIFECYCLE_SAMPLER_START_TOKEN="$start_token"
  if [[ "$start_status" -ne 0 ]]; then
    touch "$LIFECYCLE_SAMPLER_STOP_FILE" "$LIFECYCLE_SAMPLER_START_FILE" 2>/dev/null || true
    wait_child_uninterrupted "$LIFECYCLE_SAMPLER_PID"
    rm -f "$LIFECYCLE_SAMPLER_STOP_FILE" "$LIFECYCLE_SAMPLER_START_FILE"
    LIFECYCLE_SAMPLER_PID=""
    LIFECYCLE_SAMPLER_STOP_FILE=""
    LIFECYCLE_SAMPLER_START_FILE=""
    LIFECYCLE_SAMPLER_START_TOKEN=""
    return "$start_status"
  fi
}

stop_lifecycle_sampler() {
  local child_status=0 status_values attempts=0 completions=0 recorded_exit_status=0 recorded_reason="" reason=stopped
  [[ -n "$LIFECYCLE_SAMPLER_PID" ]] || return 0
  touch "$LIFECYCLE_SAMPLER_STOP_FILE"
  wait_child_uninterrupted "$LIFECYCLE_SAMPLER_PID"
  child_status="$WAIT_CHILD_STATUS"
  if status_values="$(jq -er \
    --argjson pid "$LIFECYCLE_SAMPLER_PID" \
    --arg start_token "$LIFECYCLE_SAMPLER_START_TOKEN" '
      select(.schema == "wukongim/chat-lifecycle-local-single-node-sampler-status/v1" and
             .pid == $pid and .start_token == $start_token and
             (.attempts | type) == "number" and .attempts >= 0 and .attempts == (.attempts | floor) and
             (.completions | type) == "number" and .completions >= 0 and .completions == (.completions | floor) and
             .completions <= .attempts and
             (.exit_status | type) == "number" and .exit_status >= 0 and .exit_status <= 255 and
             .exit_status == (.exit_status | floor) and (.reason | type) == "string") |
      [.attempts,.completions,.exit_status,.reason] | @tsv
    ' "$LIFECYCLE_SAMPLER_STATUS_FILE")"; then
    IFS=$'\t' read -r attempts completions recorded_exit_status recorded_reason <<<"$status_values"
  elif [[ "$child_status" -eq 0 ]]; then
    child_status=70
  fi
  if [[ "$child_status" -eq 0 ]] && \
    [[ "$attempts" -ne "$completions" || "$recorded_exit_status" -ne 0 || "$recorded_reason" != stopped ]]; then
    child_status=70
  fi
  if [[ "$child_status" -ne 0 ]]; then
    reason=unexpected_exit
  fi
  if ! write_lifecycle_sampler_status "$LIFECYCLE_SAMPLER_STATUS_FILE" \
    "$LIFECYCLE_SAMPLER_PID" "$LIFECYCLE_SAMPLER_START_TOKEN" \
    "$attempts" "$completions" "$child_status" "$reason"; then
    [[ "$child_status" -ne 0 ]] || child_status=70
  fi
  rm -f "$LIFECYCLE_SAMPLER_STOP_FILE" "$LIFECYCLE_SAMPLER_START_FILE"
  LIFECYCLE_SAMPLER_PID=""
  LIFECYCLE_SAMPLER_STOP_FILE=""
  LIFECYCLE_SAMPLER_START_FILE=""
  LIFECYCLE_SAMPLER_START_TOKEN=""
  return "$child_status"
}

threshold_profile_evidence_dir() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/evidence"
}

threshold_profile_phase_file() {
  printf '%s\n' "$(threshold_profile_evidence_dir "$1")/threshold-pprof-phase"
}

write_threshold_profile_phase() {
  local tag="$1" phase="$2" phase_file temporary current=""
  case "$phase" in
    warmup|measurement|drain|shutdown) ;;
    *) return 1 ;;
  esac
  phase_file="$(threshold_profile_phase_file "$tag")"
  mkdir -p "$(dirname "$phase_file")" || return 1
  if [[ -f "$phase_file" && ! -L "$phase_file" ]]; then
    current="$(LC_ALL=C head -c 65 "$phase_file" 2>/dev/null || true)"
  fi
  # A stale worker cut must never move a parent-closed admission phase back to
  # measurement while a bounded profile is still completing.
  if [[ "$current" == drain || "$current" == shutdown ]]; then
    if [[ "$phase" == warmup || "$phase" == measurement ]]; then
      return 0
    fi
  fi
  temporary="$(mktemp "${phase_file}.next.XXXXXX")" || return 1
  if ! printf '%s' "$phase" >"$temporary" || ! mv "$temporary" "$phase_file"; then
    rm -f "$temporary"
    return 1
  fi
}

write_threshold_profile_operational_status() {
  local status_file="$1" query_file="$2" reason="$3" helper_exit="${4:-}" temporary
  temporary="$(mktemp "${status_file}.next.XXXXXX")" || return 1
  if [[ -f "$query_file" && ! -L "$query_file" ]] && jq -e '
    .schema == "wukongim/chat-lifecycle-local-single-node-profile-threshold/v1" and
    (.triggered | type == "boolean")
  ' "$query_file" >/dev/null 2>&1; then
    jq -n --slurpfile query "$query_file" --arg reason "$reason" --arg helper_exit "$helper_exit" '
      ($query[0]) as $q |
      {
        schema:"wukongim/chat-lifecycle-local-single-node-threshold-pprof/v1",
        status:"operational_error", evidence_complete:false, capture_valid:false,
        reason:$reason, triggered:$q.triggered,
        trigger:(if $q.triggered then $q.trigger else null end), metadata:""
      }
      + (if ($helper_exit | length) > 0 then {helper_exit_status:($helper_exit | tonumber)} else {} end)
    ' >"$temporary" || { rm -f "$temporary"; return 1; }
  else
    jq -n --arg reason "$reason" --arg helper_exit "$helper_exit" '
      {
        schema:"wukongim/chat-lifecycle-local-single-node-threshold-pprof/v1",
        status:"operational_error", evidence_complete:false, capture_valid:false,
        reason:$reason, triggered:false, metadata:""
      }
      + (if ($helper_exit | length) > 0 then {helper_exit_status:($helper_exit | tonumber)} else {} end)
    ' >"$temporary" || { rm -f "$temporary"; return 1; }
  fi
  mv "$temporary" "$status_file"
}

write_threshold_profile_status() {
  local tag="$1" helper_exit="${2:-}" evidence_dir query_file status_file profile_dir metadata_file temporary
  evidence_dir="$(threshold_profile_evidence_dir "$tag")"
  query_file="$evidence_dir/threshold-pprof-query.json"
  status_file="$evidence_dir/threshold-pprof-status.json"
  profile_dir="$evidence_dir/threshold-pprof"
  metadata_file="$profile_dir/metadata.json"
  if ! jq -e '
    .schema == "wukongim/chat-lifecycle-local-single-node-profile-threshold/v1" and
    .evidence_complete == true and (.triggered | type == "boolean") and
    (.reason | type == "string" and length > 0)
  ' "$query_file" >/dev/null 2>&1; then
    write_threshold_profile_operational_status "$status_file" "$query_file" threshold_query_incomplete "$helper_exit"
    return
  fi
  if jq -e '.triggered == false' "$query_file" >/dev/null 2>&1; then
    if [[ -e "$profile_dir" || -L "$profile_dir" ]]; then
      write_threshold_profile_operational_status "$status_file" "$query_file" untriggered_capture_artifacts_present "$helper_exit"
      return
    fi
    temporary="$(mktemp "${status_file}.next.XXXXXX")" || return 1
    jq -n '{
      schema:"wukongim/chat-lifecycle-local-single-node-threshold-pprof/v1",
      status:"not_triggered", evidence_complete:true, capture_valid:true,
      reason:"no_measured_threshold", triggered:false, metadata:""
    }' >"$temporary" || { rm -f "$temporary"; return 1; }
    mv "$temporary" "$status_file"
    return
  fi
  if [[ ! "$helper_exit" =~ ^[0-9]+$ || "$helper_exit" -ne 0 ||
    ! -f "$metadata_file" || -L "$metadata_file" ]]; then
    write_threshold_profile_operational_status "$status_file" "$query_file" missing_or_invalid_helper_metadata "${helper_exit:-70}"
    return
  fi
  if ! jq -e '
    .schema == "wukongim.local_threshold_pprof/v1" and
    (.capture.status == "complete" or .capture.status == "partial") and
    (.capture.valid | type == "boolean") and
    (.capture.reason | type == "string" and length > 0)
  ' "$metadata_file" >/dev/null 2>&1; then
    write_threshold_profile_operational_status "$status_file" "$query_file" missing_or_invalid_helper_metadata "$helper_exit"
    return
  fi
  temporary="$(mktemp "${status_file}.next.XXXXXX")" || return 1
  if ! jq -n --slurpfile query "$query_file" --slurpfile metadata "$metadata_file" --argjson helper_exit "$helper_exit" '
    ($query[0]) as $q | ($metadata[0]) as $m |
    {
      schema:"wukongim/chat-lifecycle-local-single-node-threshold-pprof/v1",
      status:$m.capture.status,
      evidence_complete:($m.capture.status == "complete" and $m.capture.valid == true),
      capture_valid:$m.capture.valid, reason:$m.capture.reason,
      triggered:true, trigger:$q.trigger,
      metadata:"threshold-pprof/metadata.json", helper_exit_status:$helper_exit
    }
  ' >"$temporary"; then
    rm -f "$temporary"
    return 1
  fi
  mv "$temporary" "$status_file"
}

threshold_profile_watcher_loop() {
  local tag="$1" offered_qps="$2" run_id="$3" stop_file="$4"
  local evidence_dir query_file log_file phase_file watcher_child_pid="" watcher_child_kind="" helper_exit="" child_status=0
  local live_phase trigger trigger_kind previous_utc current_utc
  evidence_dir="$(threshold_profile_evidence_dir "$tag")"
  query_file="$evidence_dir/threshold-pprof-query.json"
  log_file="$evidence_dir/threshold-pprof.log"
  phase_file="$(threshold_profile_phase_file "$tag")"

  threshold_profile_watcher_signal() {
    trap - HUP INT TERM
    if [[ -n "$watcher_child_pid" ]]; then
      kill "$watcher_child_pid" >/dev/null 2>&1 || true
      child_status=0
      wait "$watcher_child_pid" 2>/dev/null || child_status=$?
      if [[ "$watcher_child_kind" == helper ]]; then
        helper_exit="$child_status"
      fi
      watcher_child_pid=""
      watcher_child_kind=""
    fi
    write_threshold_profile_status "$tag" "${helper_exit:-}" || true
    exit 0
  }
  trap threshold_profile_watcher_signal HUP INT TERM

  while true; do
    child_status=0
    "$WK_BENCH_BIN" report local-single-node-profile-threshold \
      --lifecycle "$OUT_DIR/reports/${tag}-qps/lifecycle-status.jsonl" \
      --run-id "$run_id" \
      --offered-qps "$offered_qps" \
      --minimum-throughput-percent 90 \
      --output "$query_file" >>"$log_file" 2>&1 &
    watcher_child_pid="$!"
    watcher_child_kind=query
    wait "$watcher_child_pid" || child_status=$?
    watcher_child_pid=""
    watcher_child_kind=""
    if (( child_status == 0 )) && jq -e '
      .schema == "wukongim/chat-lifecycle-local-single-node-profile-threshold/v1" and
      .evidence_complete == true and
      (.live_phase == "warmup" or .live_phase == "measurement" or
       .live_phase == "drain" or .live_phase == "shutdown" or .live_phase == "unknown")
    ' "$query_file" >/dev/null 2>&1; then
      live_phase="$(jq -r '.live_phase' "$query_file")"
      if jq -e '(.assignment_id | type == "string" and length > 0)' "$query_file" >/dev/null 2>&1 &&
        [[ "$live_phase" != unknown ]]; then
        write_threshold_profile_phase "$tag" "$live_phase" || true
      fi
      if jq -e '
        .triggered == true and
        (.trigger.kind == "actual_offered_ratio" or .trigger.kind == "terminal_product_failure") and
        (.trigger.previous_at | type == "string" and length > 0) and
        (.trigger.current_at | type == "string" and length > 0)
      ' "$query_file" >/dev/null 2>&1; then
        trigger="$(jq -r '[.trigger.kind,.trigger.previous_at,.trigger.current_at] | @tsv' "$query_file")"
        IFS=$'\t' read -r trigger_kind previous_utc current_utc <<<"$trigger"
        helper_exit=0
        WK_BENCH_API_TOKEN="${WK_BENCH_API_TOKEN:-}" "$THRESHOLD_PROFILE_HELPER" \
          --out-dir "$evidence_dir/threshold-pprof" \
          --phase-state-file "$phase_file" \
          --trigger-kind "$trigger_kind" \
          --trigger-observed-phase measurement \
          --previous-utc "$previous_utc" \
          --current-utc "$current_utc" \
          --node "${API_VALUES[0]}" \
          --cpu-seconds "$PROFILE_SECONDS" >>"$log_file" 2>&1 &
        watcher_child_pid="$!"
        watcher_child_kind=helper
        wait "$watcher_child_pid" || helper_exit=$?
        watcher_child_pid=""
        watcher_child_kind=""
        write_threshold_profile_status "$tag" "$helper_exit" || true
        trap - HUP INT TERM
        return 0
      fi
    fi
    [[ ! -f "$stop_file" ]] || break
    sleep "$LIFECYCLE_SAMPLE_INTERVAL" &
    watcher_child_pid="$!"
    watcher_child_kind=sleep
    wait "$watcher_child_pid" 2>/dev/null || true
    watcher_child_pid=""
    watcher_child_kind=""
  done
  write_threshold_profile_status "$tag" || true
  trap - HUP INT TERM
}

start_threshold_profile_watcher() {
  local tag="$1" offered_qps="$2" run_id="$3" evidence_dir
  evidence_dir="$(threshold_profile_evidence_dir "$tag")"
  mkdir -p "$evidence_dir" || return 1
  [[ ! -e "$evidence_dir/threshold-pprof-status.json" && ! -e "$evidence_dir/threshold-pprof" ]] || return 1
  THRESHOLD_PROFILE_WATCHER_STOP_FILE="$evidence_dir/threshold-pprof-watcher.stop"
  rm -f "$THRESHOLD_PROFILE_WATCHER_STOP_FILE"
  : >"$evidence_dir/threshold-pprof.log"
  write_threshold_profile_phase "$tag" warmup || return 1
  threshold_profile_watcher_loop "$tag" "$offered_qps" "$run_id" "$THRESHOLD_PROFILE_WATCHER_STOP_FILE" &
  THRESHOLD_PROFILE_WATCHER_PID="$!"
}

stop_threshold_profile_watcher() {
  local tag="$1" status_file
  [[ -n "$THRESHOLD_PROFILE_WATCHER_PID" ]] || return 0
  touch "$THRESHOLD_PROFILE_WATCHER_STOP_FILE"
  wait_child_uninterrupted "$THRESHOLD_PROFILE_WATCHER_PID"
  rm -f "$THRESHOLD_PROFILE_WATCHER_STOP_FILE"
  THRESHOLD_PROFILE_WATCHER_PID=""
  THRESHOLD_PROFILE_WATCHER_STOP_FILE=""
  status_file="$(threshold_profile_evidence_dir "$tag")/threshold-pprof-status.json"
  [[ -f "$status_file" && ! -L "$status_file" ]]
}

terminate_threshold_profile_watcher() {
  [[ -n "$THRESHOLD_PROFILE_WATCHER_PID" ]] || return 0
  kill "$THRESHOLD_PROFILE_WATCHER_PID" >/dev/null 2>&1 || true
  wait_child_uninterrupted "$THRESHOLD_PROFILE_WATCHER_PID"
  [[ -z "$THRESHOLD_PROFILE_WATCHER_STOP_FILE" ]] || rm -f "$THRESHOLD_PROFILE_WATCHER_STOP_FILE"
  THRESHOLD_PROFILE_WATCHER_PID=""
  THRESHOLD_PROFILE_WATCHER_STOP_FILE=""
}

sample_node_goroutines() {
  local node="$1"
  local idx=$((node - 1))
  local addr metrics
  addr="${API_VALUES[$idx]:-}"
  [[ -n "$addr" ]] || return 0
  metrics="$(curl -fsS --max-time 2 "${addr%/}/metrics" 2>/dev/null || true)"
  [[ -n "$metrics" ]] || return 0
  awk '
    $0 ~ /^#/ { next }
    $1 == "go_goroutines" || $1 ~ /^go_goroutines[{]/ {
      print int($NF)
      exit
    }
  ' <<<"$metrics"
}

write_resource_error_sample() {
  local phase="$1"
  local node_name="$2"
  local reason="$3"
  local ts
  ts="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  mkdir -p "$OUT_DIR/resources"
  printf '{"timestamp":"%s","phase":"%s","node":"%s","pid":null,"error":"%s"}\n' \
    "$ts" "$phase" "$node_name" "$(json_escape "$reason")" >>"$OUT_DIR/resources/server-process.jsonl" || true
  return 0
}

sample_server_resources() {
  local phase="$1"
  local ts node node_name pid line cpu mem rss vsz elapsed command goroutines
  ts="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  mkdir -p "$OUT_DIR/resources"
  for node in 1; do
    node_name="node${node}"
    pid="$(server_pid_for_node "$node")"
    if [[ -z "$pid" ]]; then
      write_resource_error_sample "$phase" "$node_name" "pid_not_found"
      continue
    fi
    line="$(LC_ALL=C ps -p "$pid" -o pcpu= -o pmem= -o rss= -o vsz= -o etime= -o comm= 2>/dev/null || true)"
    if [[ -z "${line//[[:space:]]/}" ]]; then
      write_resource_error_sample "$phase" "$node_name" "ps_sample_unavailable"
      continue
    fi
    read -r cpu mem rss vsz elapsed command <<<"$line"
    if ! is_nonnegative_number "$cpu" || ! is_nonnegative_number "$mem" || ! is_nonnegative_int "$rss" || ! is_nonnegative_int "$vsz"; then
      write_resource_error_sample "$phase" "$node_name" "invalid_ps_sample"
      continue
    fi
    goroutines="$(sample_node_goroutines "$node")"
    if ! is_nonnegative_int "$goroutines"; then
      goroutines="null"
    fi
    printf '{"timestamp":"%s","phase":"%s","node":"%s","pid":%s,"cpu_percent":%.3f,"mem_percent":%.3f,"rss_kb":%s,"vsz_kb":%s,"elapsed":"%s","command":"%s","goroutines":%s}\n' \
      "$ts" "$phase" "$node_name" "$pid" "$cpu" "$mem" "$rss" "$vsz" "$elapsed" "$(json_escape "$command")" "$goroutines" \
      >>"$OUT_DIR/resources/server-process.jsonl" || true
  done
  return 0
}

resource_periodic_sampling_enabled() {
  awk -v interval="$RESOURCE_SAMPLE_INTERVAL" 'BEGIN { exit !(interval > 0) }'
}

start_server_resource_sampler() {
  sample_server_resources before || true
  if ! resource_periodic_sampling_enabled; then
    return
  fi
  (
    while true; do
      sleep "$RESOURCE_SAMPLE_INTERVAL"
      sample_server_resources interval || true
    done
  ) &
  RESOURCE_SAMPLER_PID="$!"
}

stop_server_resource_sampler() {
  if [[ -n "$RESOURCE_SAMPLER_PID" ]]; then
    kill "$RESOURCE_SAMPLER_PID" >/dev/null 2>&1 || true
    wait "$RESOURCE_SAMPLER_PID" 2>/dev/null || true
    RESOURCE_SAMPLER_PID=""
  fi
}

write_server_resource_summary() {
  local samples="$OUT_DIR/resources/server-process.jsonl"
  local summary="$OUT_DIR/resources/server-process-summary.tsv"
  mkdir -p "$OUT_DIR/resources"
  if [[ ! -f "$samples" ]]; then
    printf 'node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb\tmax_goroutines\n' >"$summary" || true
    return 0
  fi
  awk '
    function json_number(key, line, pattern, rest) {
      pattern = "\"" key "\":"
      pos = index(line, pattern)
      if (pos == 0) return ""
      rest = substr(line, pos + length(pattern))
      sub(/[,}].*/, "", rest)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", rest)
      return rest
    }
    function json_string(key, line, pattern, rest) {
      pattern = "\"" key "\":\""
      pos = index(line, pattern)
      if (pos == 0) return ""
      rest = substr(line, pos + length(pattern))
      sub(/".*/, "", rest)
      return rest
    }
    BEGIN {
      print "node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb\tmax_goroutines"
    }
    {
      node = json_string("node", $0)
      pid = json_number("pid", $0)
      if (node == "" || pid == "" || pid == "null") next
      cpu = json_number("cpu_percent", $0) + 0
      mem = json_number("mem_percent", $0) + 0
      rss = json_number("rss_kb", $0) + 0
      vsz = json_number("vsz_kb", $0) + 0
      goroutines_raw = json_number("goroutines", $0)
      goroutines = -1
      if (goroutines_raw != "" && goroutines_raw != "null") {
        goroutines = goroutines_raw + 0
      }
      samples[node]++
      last_pid[node] = pid
      cpu_sum[node] += cpu
      mem_sum[node] += mem
      if (samples[node] == 1 || cpu > cpu_max[node]) cpu_max[node] = cpu
      if (samples[node] == 1 || mem > mem_max[node]) mem_max[node] = mem
      if (samples[node] == 1 || rss > rss_max[node]) rss_max[node] = rss
      if (samples[node] == 1 || vsz > vsz_max[node]) vsz_max[node] = vsz
      if (goroutines >= 0 && (!has_goroutines[node] || goroutines > goroutines_max[node])) {
        has_goroutines[node] = 1
        goroutines_max[node] = goroutines
      }
    }
    END {
      for (i = 1; i <= 1; i++) {
        node = "node" i
        if (samples[node] == 0) continue
        printf "%s\t%s\t%d\t%.3f\t%.3f\t%.3f\t%.3f\t%.0f\t%.0f\t%.0f\n",
          node,
          last_pid[node],
          samples[node],
          cpu_sum[node] / samples[node],
          cpu_max[node],
          mem_sum[node] / samples[node],
          mem_max[node],
          rss_max[node],
          vsz_max[node],
          has_goroutines[node] ? goroutines_max[node] : 0
      }
    }
  ' "$samples" >"$summary" || {
    printf 'node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb\tmax_goroutines\n' >"$summary" || true
    return 0
  }
  return 0
}

classify_metrics() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local addr id
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    "$WK_BENCH_BIN" metrics classify \
      --before "$metrics_dir/${id}-before.prom" \
      --after "$metrics_dir/${id}-after.prom" \
      >"$metrics_dir/${id}-classify.txt" 2>&1 || true
  done
}

runtime_pool_sampler_stop_file() {
  printf '%s\n' "$OUT_DIR/metrics/$1/runtime-pool-sampler.stop"
}

storage_overlap_evidence_path() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/evidence/storage-overlap.tsv"
}

storage_overlap_lock_dir() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/evidence/storage-overlap.lock"
}

storage_overlap_closed_file() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/evidence/storage-overlap.closed"
}

acquire_storage_overlap_lock() {
  local tag="$1" deadline_epoch="${2:-0}" lock_dir now_epoch
  lock_dir="$(storage_overlap_lock_dir "$tag")"
  while ! mkdir "$lock_dir" 2>/dev/null; do
    if [[ "$deadline_epoch" =~ ^[0-9]+$ && "$deadline_epoch" -gt 0 ]]; then
      now_epoch="$(date -u '+%s')"
      (( now_epoch < deadline_epoch - TERMINAL_CUT_ACK_SAFETY_SECONDS )) || return 1
    fi
    sleep 0.05 || true
  done
}

release_storage_overlap_lock() {
  rmdir "$(storage_overlap_lock_dir "$1")" 2>/dev/null || true
}

initialize_storage_overlap_evidence() {
  local tag="$1" evidence_dir output
  evidence_dir="$OUT_DIR/reports/${tag}-qps/evidence"
  output="$(storage_overlap_evidence_path "$tag")"
  mkdir -p "$evidence_dir/snapshot-inventory"
  rm -f "$(storage_overlap_closed_file "$tag")"
  rmdir "$(storage_overlap_lock_dir "$tag")" 2>/dev/null || true
  printf 'observed_at_utc\trun_id\tsample\tnode\tstatus\tcompaction_count\tcompactions_in_progress\tsnapshot_files\tsnapshot_bytes\tsnapshot_identity\tsnapshot_inventory\n' >"$output"
}

capture_storage_overlap_cut() {
  local tag="$1" metrics_file="$2" cut="$3" sample="$4" deadline_epoch="${5:-0}"
  local observed_at run_id inventory output row closed_file terminal=false capture_status=0
  observed_at="$(jq -er '.observed_at' <<<"$cut" 2>/dev/null || true)"
  run_id="$(jq -er '.run_id' <<<"$cut" 2>/dev/null || true)"
  [[ -n "$observed_at" && "$run_id" =~ ^[A-Za-z0-9_-]{1,128}$ && "$sample" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || return 1
  output="$(storage_overlap_evidence_path "$tag")"
  closed_file="$(storage_overlap_closed_file "$tag")"
  [[ "$sample" == terminal ]] && terminal=true
  acquire_storage_overlap_lock "$tag" "$deadline_epoch" || return 1
  if [[ -f "$closed_file" ]]; then
    release_storage_overlap_lock "$tag"
    if [[ "$terminal" == false ]]; then
      return 0
    fi
    return 1
  fi
  inventory="$(dirname "$output")/snapshot-inventory/${sample}-node-1.tsv"
  if [[ -x "$STORAGE_OVERLAP_CAPTURE" ]] && row="$(
    "$STORAGE_OVERLAP_CAPTURE" \
      --metrics "$metrics_file" \
      --snapshot-root "$SINGLE_NODE_DATA_DIR/slotraft-snapshots" \
      --inventory "$inventory" \
      --observed-at "$observed_at" \
      --run-id "$run_id" \
      --sample "$sample" \
      --node node-1
  )"; then
    printf '%s\n' "$row" >>"$output"
  else
    printf '%s\t%s\t%s\tnode-1\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n' \
      "$observed_at" "$run_id" "$sample" >>"$output"
    capture_status=1
  fi
  if [[ "$terminal" == true ]]; then
    : >"$closed_file"
  fi
  release_storage_overlap_lock "$tag"
  return "$capture_status"
}

runtime_pool_sampler_loop() {
  local tag="$1"
  local stop_file="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local seq=0
  local storage_seq=0 last_storage_epoch=0
  local addr id observed_epoch temporary cut sample_name first_metrics_id first_metrics_file
  mkdir -p "$metrics_dir"
  first_metrics_id="$(metric_file_id "${METRICS_VALUES[0]:-missing}")"
  while [[ ! -f "$stop_file" ]]; do
    cut="$(product_queue_cut_metadata 2>/dev/null || true)"
    for addr in "${METRICS_VALUES[@]}"; do
      id="$(metric_file_id "$addr")"
      observed_epoch="$(date -u '+%s')"
      temporary="$metrics_dir/.${id}-sample-${seq}.next.$$"
      {
        printf '# wkbench_sampled_at_unix %s\n' "$observed_epoch"
        if [[ -n "$cut" ]]; then
          printf '# wkbench_local_single_node_cut %s\n' "$cut"
        fi
        curl -fsS --max-time 2 "${addr%/}/metrics"
      } >"$temporary" 2>/dev/null && mv "$temporary" "$metrics_dir/${id}-sample-${seq}.prom" || rm -f "$temporary"
    done
    first_metrics_file="$metrics_dir/${first_metrics_id}-sample-${seq}.prom"
    if [[ -f "$first_metrics_file" && -n "$cut" ]] && jq -e '
      (.phase == "warmup") and (.active_phase == "run") and
      ((.run_id | type) == "string") and ((.run_id | length) > 0) and
      ((.assignment_id | type) == "string") and ((.assignment_id | length) > 0)
    ' >/dev/null <<<"$cut"; then
      observed_epoch="$(date -u '+%s')"
      if (( last_storage_epoch == 0 || observed_epoch - last_storage_epoch >= STORAGE_OVERLAP_SAMPLE_INTERVAL )); then
        sample_name=post-warmup
        if (( storage_seq > 0 )); then
          printf -v sample_name 'periodic-%06d' "$storage_seq"
        fi
        capture_storage_overlap_cut "$tag" "$first_metrics_file" "$cut" "$sample_name" || true
        last_storage_epoch="$observed_epoch"
        storage_seq=$((storage_seq + 1))
      fi
    fi
    curl -fsS --max-time 6 "${HOST_METRICS_ADDR%/}/metrics" >"$metrics_dir/host-sample-${seq}.prom" 2>/dev/null || true
    seq=$((seq + 1))
    sleep "$RUNTIME_POOL_SAMPLE_INTERVAL" || true
  done
}

select_post_warmup_metrics_cut() {
  local tag="$1" run_id="$2" assignment_id="$3" metrics_dir addr id sample sample_seq cut best_seq best_sample temporary
  metrics_dir="$OUT_DIR/metrics/$tag"
  [[ "$run_id" =~ ^[A-Za-z0-9_-]{1,128}$ && "$assignment_id" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || return 1
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    best_seq=9223372036854775807
    best_sample=""
    for sample in "$metrics_dir/${id}-sample-"*.prom; do
      [[ -f "$sample" ]] || continue
      sample_seq="${sample##*-sample-}"
      sample_seq="${sample_seq%.prom}"
      [[ "$sample_seq" =~ ^[0-9]+$ ]] || continue
      cut="$(product_queue_cut_from_metrics "$sample")"
      if jq -e --arg run_id "$run_id" --arg assignment_id "$assignment_id" '
        (.schema == "wukongim/chat-lifecycle-local-single-node-product-queue-cut/v1") and
        (.run_id == $run_id) and
        (.assignment_id == $assignment_id) and
        (.phase == "warmup") and (.active_phase == "run")
      ' >/dev/null <<<"$cut" && (( sample_seq < best_seq )); then
        best_seq="$sample_seq"
        best_sample="$sample"
      fi
    done
    [[ -n "$best_sample" ]] || return 1
    temporary="$metrics_dir/.${id}-post-warmup.next.$$"
    cp "$best_sample" "$temporary" && mv "$temporary" "$metrics_dir/${id}-post-warmup.prom" || {
      rm -f "$temporary"
      return 1
    }
  done
}

terminal_cut_observer_stop_file() {
  printf '%s\n' "$OUT_DIR/reports/$1-qps/terminal-cut-observer.stop"
}

write_terminal_cut_observer_result() {
  local tag="$1" status="$2" reason="$3" run_id="${4:-}" assignment_id="${5:-}"
  local output temporary observed_at
  output="$OUT_DIR/reports/${tag}-qps/evidence/terminal-cut-observer.json"
  temporary="${output}.next.$$"
  observed_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  mkdir -p "$(dirname "$output")"
  if jq -n \
    --arg status "$status" --arg reason "$reason" --arg observed_at "$observed_at" \
    --arg run_id "$run_id" --arg assignment_id "$assignment_id" '
      {
        schema:"wukongim/chat-lifecycle-local-single-node-terminal-cut-observer/v1",
        status:$status,reason:$reason,observed_at:$observed_at,
        run_id:$run_id,assignment_id:$assignment_id
      }
    ' >"$temporary"; then
    mv "$temporary" "$output"
  else
    rm -f "$temporary"
    return 1
  fi
}

terminal_cut_deadline_epoch() {
  local deadline="$1"
  jq -ner --arg deadline "$deadline" '
    $deadline
    | select(type == "string" and test("^[-0-9]{10}T[0-9:.]+Z$"))
    | sub("\\.[0-9]+Z$"; "Z")
    | fromdateiso8601
  '
}

capture_terminal_queue_candidate() {
  local tag="$1" run_id="$2" assignment_id="$3" destination="$4"
  local status cut addr temporary
  addr="${METRICS_VALUES[0]:-}"
  [[ -n "$addr" ]] || return 1
  status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status")" || return 1
  if ! jq -e --arg run_id "$run_id" --arg assignment_id "$assignment_id" --argjson clients "$USERS" '
    (.phase == "run") and (.active_phase == "cooldown") and
    (.assignment.run_id == $run_id) and (.assignment.assignment_id == $assignment_id) and
    (.lifecycle.active_connections == $clients) and
    (.lifecycle.terminal_cut_required == true) and (.lifecycle.terminal_cut_ready == true) and
    ((.lifecycle.receive_drain_sha256 | type) == "string") and
    (.lifecycle.receive_drain_sha256 | test("^[0-9a-f]{64}$")) and
    ((.lifecycle.terminal_cut_ready_at | type) == "string") and
    ((.lifecycle.terminal_cut_deadline_at | type) == "string") and
    ((.lifecycle.terminal_cut // null) == null) and
	    (.lifecycle.receive_drain.required == true) and
	    (.lifecycle.receive_drain.evidence_complete == true) and
	    (.lifecycle.receive_drain.fanout_proof.version == "wukongim/group-fanout-proof/v1") and
	    (.lifecycle.receive_drain.fanout_proof.required == true) and
	    (.lifecycle.receive_drain.fanout_proof.evidence_complete == true) and
	    (.lifecycle.receive_drain.drain_complete == true) and
    (.lifecycle.receive_drain.client_count == $clients) and
    (.lifecycle.receive_drain.active_drains == $clients) and
    (.lifecycle.receive_drain.queue_snapshot_clients == $clients)
  ' >/dev/null <<<"$status"; then
    return 1
  fi
  cut="$(jq -ce '
    {
      schema:"wukongim/chat-lifecycle-local-single-node-product-queue-cut/v1",
      observed_at:.observed_at,
      run_id:.assignment.run_id,
      assignment_id:.assignment.assignment_id,
      phase:.phase,
      active_phase:.active_phase,
      receive_drain_sha256:.lifecycle.receive_drain_sha256
    }
  ' <<<"$status")" || return 1
  temporary="${destination}.next.$$"
  {
    printf '# wkbench_local_single_node_cut %s\n' "$cut"
    curl -fsS --connect-timeout 1 --max-time 3 "${addr%/}/metrics"
  } >"$temporary" && mv "$temporary" "$destination" || {
    rm -f "$temporary"
    return 1
  }
}

terminal_cut_observer_loop() {
  local tag="$1" run_id="$2" stop_file="$3"
  local report_dir metrics_dir metrics_id baseline candidate query_result terminal_metrics storage_evidence
  local status assignment_id ready_at deadline_at deadline_epoch now_epoch query_status=0
  local cut observed_at receive_drain_digest product_digest storage_digest payload response_tmp response
  report_dir="$OUT_DIR/reports/${tag}-qps"
  metrics_dir="$OUT_DIR/metrics/$tag"
  metrics_id="$(metric_file_id "${METRICS_VALUES[0]:-missing}")"
  baseline="$metrics_dir/${metrics_id}-post-warmup.prom"
  candidate="$metrics_dir/.${metrics_id}-terminal-candidate.prom"
  query_result="$report_dir/evidence/terminal-queue-convergence.json"
  terminal_metrics="$metrics_dir/${metrics_id}-terminal-pre-close.prom"
  storage_evidence="$(storage_overlap_evidence_path "$tag")"
  rm -f "$candidate" "$query_result" "$terminal_metrics"

  assignment_id=""
  ready_at=""
  deadline_at=""
  while [[ ! -f "$stop_file" ]]; do
    status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status" 2>/dev/null || true)"
    if jq -e --arg run_id "$run_id" --argjson clients "$USERS" '
      (.phase == "run") and (.active_phase == "cooldown") and
      (.assignment.run_id == $run_id) and
      ((.assignment.assignment_id | type) == "string") and ((.assignment.assignment_id | length) > 0) and
      (.lifecycle.active_connections == $clients) and
      (.lifecycle.terminal_cut_required == true) and (.lifecycle.terminal_cut_ready == true) and
      ((.lifecycle.receive_drain_sha256 | type) == "string") and
      (.lifecycle.receive_drain_sha256 | test("^[0-9a-f]{64}$")) and
      ((.lifecycle.terminal_cut_ready_at | type) == "string") and
      ((.lifecycle.terminal_cut_deadline_at | type) == "string") and
      ((.lifecycle.terminal_cut // null) == null) and
	      (.lifecycle.receive_drain.required == true) and
	      (.lifecycle.receive_drain.evidence_complete == true) and
	      (.lifecycle.receive_drain.fanout_proof.version == "wukongim/group-fanout-proof/v1") and
	      (.lifecycle.receive_drain.fanout_proof.required == true) and
	      (.lifecycle.receive_drain.fanout_proof.evidence_complete == true) and
	      (.lifecycle.receive_drain.drain_complete == true) and
      (.lifecycle.receive_drain.client_count == $clients) and
      (.lifecycle.receive_drain.active_drains == $clients) and
      (.lifecycle.receive_drain.queue_snapshot_clients == $clients)
    ' >/dev/null 2>&1 <<<"$status"; then
      assignment_id="$(jq -r '.assignment.assignment_id' <<<"$status")"
      ready_at="$(jq -r '.lifecycle.terminal_cut_ready_at' <<<"$status")"
      deadline_at="$(jq -r '.lifecycle.terminal_cut_deadline_at' <<<"$status")"
      break
    fi
    sleep "$TERMINAL_CUT_POLL_INTERVAL" || true
  done

  if [[ -f "$stop_file" ]]; then
    write_terminal_cut_observer_result "$tag" interrupted observer_stopped "$run_id" "$assignment_id" || true
    return 130
  fi
  if ! deadline_epoch="$(terminal_cut_deadline_epoch "$deadline_at")"; then
    write_terminal_cut_observer_result "$tag" failed terminal_cut_deadline_invalid "$run_id" "$assignment_id" || true
    return 6
  fi
  if ! select_post_warmup_metrics_cut "$tag" "$run_id" "$assignment_id"; then
    write_terminal_cut_observer_result "$tag" failed post_warmup_queue_cut_unavailable "$run_id" "$assignment_id" || true
    return 6
  fi

  while [[ ! -f "$stop_file" ]]; do
    now_epoch="$(date -u '+%s')"
    if (( now_epoch >= deadline_epoch - TERMINAL_CUT_ACK_SAFETY_SECONDS )); then
      write_terminal_cut_observer_result "$tag" failed terminal_cut_deadline_elapsed "$run_id" "$assignment_id" || true
      return 6
    fi
    if ! capture_terminal_queue_candidate "$tag" "$run_id" "$assignment_id" "$candidate"; then
      sleep "$TERMINAL_CUT_POLL_INTERVAL" || true
      continue
    fi
    query_status=0
    "$WK_BENCH_BIN" report local-single-node-queue-convergence \
      --post-warmup "$baseline" \
      --candidate "$candidate" \
      --run-id "$run_id" \
      --assignment-id "$assignment_id" \
      --output "$query_result" >/dev/null 2>&1 || query_status=$?
    if [[ "$query_status" -ne 0 ]]; then
      if [[ "$query_status" -eq 3 ]] && jq -e '
        (.schema == "wukongim/chat-lifecycle-local-single-node-queue-convergence/v1") and
        (.evidence_complete == true) and (.converged == false) and
        (.reason == "product_failure_counter_increased")
      ' "$query_result" >/dev/null 2>&1; then
        write_terminal_cut_observer_result "$tag" failed product_failure_counter_increased "$run_id" "$assignment_id" || true
        return 3
      fi
      sleep "$TERMINAL_CUT_POLL_INTERVAL" || true
      continue
    fi
    product_digest="$(sha256_file "$candidate" 2>/dev/null || true)"
    if ! jq -e --arg run_id "$run_id" --arg assignment_id "$assignment_id" --arg digest "$product_digest" '
      (.schema == "wukongim/chat-lifecycle-local-single-node-queue-convergence/v1") and
      (.run_id == $run_id) and (.assignment_id == $assignment_id) and
      (.evidence_complete == true) and (.converged == true) and (.reason == "ok") and
      (.candidate_sha256 == $digest) and
      (.candidate_cut.run_id == $run_id) and (.candidate_cut.assignment_id == $assignment_id) and
      (.candidate_cut.phase == "run") and (.candidate_cut.active_phase == "cooldown")
    ' "$query_result" >/dev/null; then
      write_terminal_cut_observer_result "$tag" failed typed_queue_convergence_invalid "$run_id" "$assignment_id" || true
      return 6
    fi
    cut="$(product_queue_cut_from_metrics "$candidate")"
    observed_at="$(jq -er '.observed_at' <<<"$cut" 2>/dev/null || true)"
    receive_drain_digest="$(jq -er '.receive_drain_sha256 | select(test("^[0-9a-f]{64}$"))' <<<"$cut" 2>/dev/null || true)"
    [[ -n "$observed_at" && "$receive_drain_digest" =~ ^[0-9a-f]{64}$ ]] || {
      write_terminal_cut_observer_result "$tag" failed terminal_cut_observed_at_missing "$run_id" "$assignment_id" || true
      return 6
    }
    if ! capture_storage_overlap_cut "$tag" "$candidate" "$cut" terminal "$deadline_epoch"; then
      write_terminal_cut_observer_result "$tag" failed terminal_storage_overlap_incomplete "$run_id" "$assignment_id" || true
      return 6
    fi
    storage_digest="$(sha256_file "$storage_evidence" 2>/dev/null || true)"
    if [[ ! "$product_digest" =~ ^[0-9a-f]{64}$ || ! "$storage_digest" =~ ^[0-9a-f]{64}$ ]]; then
      write_terminal_cut_observer_result "$tag" failed terminal_cut_digest_unavailable "$run_id" "$assignment_id" || true
      return 6
    fi
    mv "$candidate" "$terminal_metrics" || {
      write_terminal_cut_observer_result "$tag" failed terminal_cut_publication_failed "$run_id" "$assignment_id" || true
      return 6
    }
    payload="$(jq -cn \
      --arg run_id "$run_id" --arg assignment_id "$assignment_id" --arg observed_at "$observed_at" \
      --arg receive_drain_digest "$receive_drain_digest" \
      --arg product_digest "$product_digest" --arg storage_digest "$storage_digest" '
        {
          run_id:$run_id,assignment_id:$assignment_id,observed_at:$observed_at,
          receive_drain_sha256:$receive_drain_digest,
          product_metrics_sha256:$product_digest,storage_overlap_sha256:$storage_digest
        }
      ')" || return 6
    response_tmp="$report_dir/evidence/.terminal-cut-binding.next.$$"
    if ! curl -fsS --connect-timeout 1 --max-time 2 -X POST -H 'Content-Type: application/json' \
      --data "$payload" "${WORKER_ADDR%/}/v1/terminal-cut" >"$response_tmp"; then
      rm -f "$response_tmp"
      write_terminal_cut_observer_result "$tag" failed terminal_cut_ack_failed "$run_id" "$assignment_id" || true
      return 6
    fi
    response="$(command cat "$response_tmp")"
    if ! jq -e --arg run_id "$run_id" --arg assignment_id "$assignment_id" --arg ready_at "$ready_at" \
      --arg deadline_at "$deadline_at" --arg observed_at "$observed_at" \
      --arg receive_drain_digest "$receive_drain_digest" \
      --arg product_digest "$product_digest" --arg storage_digest "$storage_digest" '
      (.run_id == $run_id) and (.assignment_id == $assignment_id) and
      (.ready_at == $ready_at) and (.deadline_at == $deadline_at) and (.observed_at == $observed_at) and
      (.receive_drain_sha256 == $receive_drain_digest) and
      (.product_metrics_sha256 == $product_digest) and (.storage_overlap_sha256 == $storage_digest) and
      ((.acknowledged_at | type) == "string") and ((.acknowledged_at | length) > 0)
    ' >/dev/null <<<"$response"; then
      rm -f "$response_tmp"
      write_terminal_cut_observer_result "$tag" failed terminal_cut_ack_response_invalid "$run_id" "$assignment_id" || true
      return 6
    fi
    mv "$response_tmp" "$report_dir/evidence/terminal-cut-binding.json" || return 6
    write_terminal_cut_observer_result "$tag" complete acknowledged "$run_id" "$assignment_id" || true
    return 0
  done

  write_terminal_cut_observer_result "$tag" interrupted observer_stopped "$run_id" "$assignment_id" || true
  return 130
}

start_terminal_cut_observer() {
  local tag="$1" run_id="$2"
  TERMINAL_CUT_OBSERVER_STOP_FILE="$(terminal_cut_observer_stop_file "$tag")"
  rm -f "$TERMINAL_CUT_OBSERVER_STOP_FILE"
  terminal_cut_observer_loop "$tag" "$run_id" "$TERMINAL_CUT_OBSERVER_STOP_FILE" \
    >"$OUT_DIR/reports/${tag}-qps/terminal-cut-observer.log" 2>&1 &
  TERMINAL_CUT_OBSERVER_PID="$!"
}

stop_terminal_cut_observer() {
  local observer_status=0
  [[ -n "$TERMINAL_CUT_OBSERVER_PID" ]] || return 0
  touch "$TERMINAL_CUT_OBSERVER_STOP_FILE"
  wait_child_uninterrupted "$TERMINAL_CUT_OBSERVER_PID"
  observer_status="$WAIT_CHILD_STATUS"
  rm -f "$TERMINAL_CUT_OBSERVER_STOP_FILE"
  TERMINAL_CUT_OBSERVER_PID=""
  TERMINAL_CUT_OBSERVER_STOP_FILE=""
  return "$observer_status"
}

terminate_terminal_cut_observer() {
  local observer_status=0
  [[ -n "$TERMINAL_CUT_OBSERVER_PID" ]] || return 0
  touch "$TERMINAL_CUT_OBSERVER_STOP_FILE" 2>/dev/null || true
  wait_child_uninterrupted "$TERMINAL_CUT_OBSERVER_PID"
  observer_status="$WAIT_CHILD_STATUS"
  rm -f "$TERMINAL_CUT_OBSERVER_STOP_FILE" 2>/dev/null || true
  TERMINAL_CUT_OBSERVER_PID=""
  TERMINAL_CUT_OBSERVER_STOP_FILE=""
  return "$observer_status"
}

host_io_summary() {
  local tag="$1" metrics_dir out
  metrics_dir="$OUT_DIR/metrics/$tag"
  out="$OUT_DIR/host_io_summary.tsv"
  local -a samples files
  samples=("$metrics_dir/host-sample-"*.prom)
  files=("$metrics_dir/host-before.prom")
  if [[ -e "${samples[0]}" ]]; then files+=("${samples[@]}"); fi
  files+=("$metrics_dir/host-after.prom")
  awk -v tag="$tag" -v host=host-local -f "$ROOT_DIR/scripts/host-io-summary.awk" "${files[@]}" >>"$out"
}

start_runtime_pool_sampler() {
  local tag="$1"
  local stop_file
  initialize_storage_overlap_evidence "$tag"
  stop_file="$(runtime_pool_sampler_stop_file "$tag")"
  rm -f "$stop_file"
  runtime_pool_sampler_loop "$tag" "$stop_file" >/dev/null 2>&1 &
  RUNTIME_POOL_SAMPLER_PID="$!"
  RUNTIME_POOL_SAMPLER_STOP_FILE="$stop_file"
}

stop_runtime_pool_sampler() {
  [[ -n "$RUNTIME_POOL_SAMPLER_PID" ]] || return 0
  touch "$RUNTIME_POOL_SAMPLER_STOP_FILE"
  wait_child_uninterrupted "$RUNTIME_POOL_SAMPLER_PID"
  rm -f "$RUNTIME_POOL_SAMPLER_STOP_FILE"
  RUNTIME_POOL_SAMPLER_PID=""
  RUNTIME_POOL_SAMPLER_STOP_FILE=""
}

rpc_pull_qps_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local duration="$2"
  local out="$OUT_DIR/rpc_pull_qps.tsv"
  local addr id
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    awk -v tag="$tag" -v node="$id" -v duration="$duration" '
      FNR == NR {
        if ($1 ~ /^wukongim_channelv2_rpc_pull_total/) before += $2
        next
      }
      {
        if ($1 ~ /^wukongim_channelv2_rpc_pull_total/) after += $2
      }
      END {
        delta = after - before
        if (delta < 0) delta = 0
        printf "%s\t%s\t%.0f\t%.3f\n", tag, node, delta, delta / duration
      }
    ' "$metrics_dir/${id}-before.prom" "$metrics_dir/${id}-after.prom" >>"$out"
  done
}

channel_metrics_summary() {
  local tag="$1"
  local duration="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/channel_metrics_summary.tsv"
  local legacy_out="$OUT_DIR/channelv2_metrics_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/channel-metrics-summary.awk"
  local addr id before after
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    [[ -f "$before" && -f "$after" ]] || continue
    awk -v tag="$tag" -v node="$id" -v duration="$duration" -f "$summarizer" "$before" "$after" >>"$out" || true
  done
  if [[ -f "$out" ]]; then
    cp "$out" "$legacy_out"
  fi
}

channelappend_metrics_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/channelappend_metrics_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/channelappend-metrics-summary.awk"
  local addr id before after
  local samples=()
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    [[ -f "$before" && -f "$after" ]] || continue
    samples=("$metrics_dir/${id}-sample-"*.prom)
    if [[ ! -e "${samples[0]}" ]]; then
      samples=()
    fi
    awk -v tag="$tag" -v node="$id" -f "$summarizer" "$before" "$after" "${samples[@]}" >>"$out" || true
  done
}

storage_metrics_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/storage_metrics_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/storage-metrics-summary.awk"
  local addr id before after
  local samples=() files=()
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    if [[ ! -f "$before" || ! -f "$after" ]]; then
      awk -v tag="$tag" -v node="$id" -f "$summarizer" /dev/null /dev/null >>"$out" || true
      continue
    fi
    samples=("$metrics_dir/${id}-sample-"*.prom)
    if [[ ! -e "${samples[0]}" ]]; then
      samples=()
    fi
    files=("$before")
    files+=("${samples[@]}")
    files+=("$after")
    awk -v tag="$tag" -v node="$id" -f "$summarizer" "${files[@]}" >>"$out" || true
  done
}

# write_immutable_step_summaries copies the one closed row for a rate step out
# of the append-only run summaries before the step checksum manifest is made.
write_immutable_step_summaries() {
  local qps="$1" tag report_dir source destination temporary
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  [[ -d "$report_dir" && ! -L "$report_dir" ]] || return 1
  mkdir -p "$report_dir/evidence" || return 1
  for source in "$OUT_DIR/storage_metrics_summary.tsv" "$OUT_DIR/host_io_summary.tsv"; do
    [[ -f "$source" && ! -L "$source" ]] || return 1
    case "$source" in
      "$OUT_DIR/storage_metrics_summary.tsv") destination="$report_dir/evidence/storage-summary.tsv" ;;
      "$OUT_DIR/host_io_summary.tsv") destination="$report_dir/evidence/host-io-summary.tsv" ;;
      *) return 1 ;;
    esac
    [[ ! -e "$destination" && ! -L "$destination" ]] || return 1
    temporary="${destination}.next.$$"
    [[ ! -e "$temporary" && ! -L "$temporary" ]] || return 1
    awk -F '\t' -v tag="$tag" 'NR == 1 || $1 == tag { print }' "$source" >"$temporary" || return 1
    [[ -s "$temporary" && ! -L "$temporary" ]] || return 1
    mv "$temporary" "$destination" || return 1
  done
}

runtime_pool_pressure_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/runtime_pool_pressure_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/runtime-pool-pressure-summary.awk"
  local addr id before after
  local samples=()
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    [[ -f "$before" && -f "$after" ]] || continue
    samples=("$metrics_dir/${id}-sample-"*.prom)
    if [[ ! -e "${samples[0]}" ]]; then
      samples=()
    fi
    awk -v tag="$tag" -v node="$id" -f "$summarizer" "$before" "$after" "${samples[@]}" >>"$out" || true
  done
}

ants_pool_usage_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/ants_pool_usage_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/ants-pool-usage-summary.awk"
  local addr id before after
  local samples=()
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    [[ -f "$before" && -f "$after" ]] || continue
    samples=("$metrics_dir/${id}-sample-"*.prom)
    if [[ ! -e "${samples[0]}" ]]; then
      samples=()
    fi
    awk -v tag="$tag" -v node="$id" -f "$summarizer" "$before" "$after" "${samples[@]}" >>"$out" || true
  done
}

cluster_transport_peak_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/cluster_transport_peak_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/cluster-transport-peak-summary.awk"
  local addr id
  local wrote=0
  local samples=()
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    samples=("$metrics_dir/${id}-sample-"*.prom)
    if [[ ! -e "${samples[0]}" ]]; then
      continue
    fi
    wrote=1
    awk -v tag="$tag" -v node="$id" -v interval="$RUNTIME_POOL_SAMPLE_INTERVAL" -f "$summarizer" "${samples[@]}" >>"$out" || true
  done
  if [[ "$wrote" -eq 0 ]]; then
    printf '%s\tunknown\t0\t0\t0.000\t0.000\t0.000\t0.000\t0\t0\n' "$tag" >>"$out"
    return
  fi
}

run_attempt() {
  local qps="$1"
  local tag report_dir run_id exit_status wkbench_status observer_status duration
  local lifecycle_sampler_status lifecycle_sampler_stop_status
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  run_id="single-node-${BASELINE_INVOCATION_ID}-fixed-${CHANNELS}ch-${tag}-qps"
  duration="$(duration_seconds "$DURATION")"
  mkdir -p "$report_dir"

  write_scenario "$qps" "$tag" "$report_dir"
  stop_worker_exact_from_status "before qps=$qps" || die "worker exact cleanup failed before qps=$qps"

  log "running qps=$qps tag=$tag"
  scrape_metrics "$tag" before
  lifecycle_sampler_status=0
  start_lifecycle_sampler "$tag" || lifecycle_sampler_status=$?
  start_threshold_profile_watcher "$tag" "$qps" "$run_id" || die "failed to start typed threshold profile watcher for qps=$qps"
  start_runtime_pool_sampler "$tag"
  start_terminal_cut_observer "$tag" "$run_id"
  wkbench_status=0
  "$WK_BENCH_BIN" run \
    --target "$OUT_DIR/target.yaml" \
    --scenario "$OUT_DIR/scenario-${tag}.yaml" \
    --workers "$OUT_DIR/workers.yaml" \
    --phase-poll-timeout "$PHASE_POLL_TIMEOUT" \
    >"$report_dir/wkbench-console.txt" 2>&1 || wkbench_status=$?
  exit_status="$wkbench_status"
  # SEND admission is now closed. Never let a stale measured worker cut move
  # the helper back into measurement while a bounded capture is completing.
  write_threshold_profile_phase "$tag" drain || true
  observer_status=0
  stop_terminal_cut_observer "$tag" || observer_status=$?
  if [[ "$exit_status" -eq 0 && "$observer_status" -ne 0 ]]; then
    exit_status="$observer_status"
  fi
  # Join the only lifecycle writer before the foreground terminal capture; a
  # subshell shares $$ and must never race the same temporary/JSONL append.
  lifecycle_sampler_stop_status=0
  stop_lifecycle_sampler || lifecycle_sampler_stop_status=$?
  if [[ "$lifecycle_sampler_status" -eq 0 && "$lifecycle_sampler_stop_status" -ne 0 ]]; then
    lifecycle_sampler_status="$lifecycle_sampler_stop_status"
  fi
  if [[ "$lifecycle_sampler_status" -ne 0 ]]; then
    log "lifecycle sampler failed closed for qps=$qps status=$lifecycle_sampler_status"
    exit_status=6
    write_lifecycle_capture_error "$tag" lifecycle_sampler_failed
  fi
  capture_lifecycle_sample "$tag" || true
  stop_threshold_profile_watcher "$tag" || true
  stop_runtime_pool_sampler
  scrape_metrics "$tag" after
  classify_metrics "$tag"
  rpc_pull_qps_summary "$tag" "$duration"
  channel_metrics_summary "$tag" "$duration"
  channelappend_metrics_summary "$tag"
  storage_metrics_summary "$tag"
  host_io_summary "$tag"
  runtime_pool_pressure_summary "$tag"
  ants_pool_usage_summary "$tag"
  cluster_transport_peak_summary "$tag"

  if [[ ! -f "$report_dir/report.json" ]]; then
    printf '%s\t%s\tmissing_report\t%s\t0\t0\t0\t0\t0\t0\t0\t0\t0\t0\t0\t0\t0\n' "$tag" "$qps" "$exit_status" >>"$OUT_DIR/summary.tsv"
    return
  fi
  jq -r --arg tag "$tag" --arg qps "$qps" --arg exit_status "$exit_status" --arg duration "$duration" '
    (.summary.send_success // 0) as $success
    | ([.metrics.counters | to_entries[] | select(.key | startswith("group_send_error_total{")) | select(.key | contains("phase=run")) | .value] | add // 0) as $errors
    | ([.metrics.counters | to_entries[] | select(.key | startswith("workload_scheduler_planned_total{")) | select(.key | contains("phase=run")) | .value] | add // 0) as $planned
    | ([.metrics.counters | to_entries[] | select(.key | startswith("workload_scheduler_dispatched_total{")) | select(.key | contains("phase=run")) | .value] | add // 0) as $dispatched
    | ([.metrics.counters | to_entries[] | select(.key | startswith("workload_scheduler_dropped_total{")) | select(.key | contains("phase=run")) | .value] | add // 0) as $dropped
    | (.metrics.histograms["group_send_latency_seconds{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}"] // {}) as $h
    | [
        $tag,
        $qps,
        .status,
        $exit_status,
        ($success / ($duration | tonumber)),
        $success,
        $errors,
        .summary.connect_error_rate,
        .summary.sendack_error_rate,
        ($h.p50_seconds // 0),
        ($h.p95_seconds // 0),
        ($h.p99_seconds // 0),
        ($h.max_seconds // 0),
        (.summary.connect_success // 0),
        $planned,
        $dispatched,
        $dropped
      ] | @tsv
  ' "$report_dir/report.json" >>"$OUT_DIR/summary.tsv"
}

write_run_metadata() {
  mkdir -p "$OUT_DIR/logs"
  {
    echo "head=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
    echo "short=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
    git -C "$ROOT_DIR" status --short 2>/dev/null || true
  } >"$OUT_DIR/git.txt"
  cat >"$OUT_DIR/env.txt" <<EOF
QPS_LIST=$QPS_LIST
BASELINE_INVOCATION_ID=$BASELINE_INVOCATION_ID
CHANNELS=$CHANNELS
USERS=$USERS
online_users=$USERS
GROUP_MEMBERS=$GROUP_MEMBERS
CONCURRENCY=$CONCURRENCY
send_concurrency=$CONCURRENCY
PAYLOAD_BYTES=$PAYLOAD_BYTES
DURATION=$DURATION
WARMUP=$WARMUP
COOLDOWN=$COOLDOWN
STABLE_P99=$STABLE_P99
ACTUAL_QPS_MIN_RATIO=$ACTUAL_QPS_MIN_RATIO
ACK_TIMEOUT=$ACK_TIMEOUT
RECV_ACK=$RECV_ACK
HEARTBEAT_ENABLED=$HEARTBEAT_ENABLED
PHASE_POLL_TIMEOUT=$PHASE_POLL_TIMEOUT
LIFECYCLE_SAMPLE_INTERVAL=$LIFECYCLE_SAMPLE_INTERVAL
SENDER_PICK=$SENDER_PICK
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
WORKER_ADDR=$WORKER_ADDR
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
CLUSTER_INITIAL_SLOT_COUNT=12
logical_slot_groups=12
CLUSTER_HASH_SLOT_COUNT=256
hash_slots=256
CHANNEL_APPEND_SHARD_COUNT=0
CHANNEL_APPEND_ADVANCE_POOL_SIZE=0
CHANNEL_APPEND_EFFECT_POOL_SIZE=0
CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=0
CLUSTER_CHANNEL_REACTOR_COUNT=128
CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500
CLUSTER_CHANNEL_STORE_APPLY_WORKERS=500
CLUSTER_CHANNEL_RPC_WORKERS=500
CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=128
CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=250us
CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=200us
CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=0
CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=0
CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=131072
CLUSTER_COMMIT_COORDINATOR_SHARDS=1
CLUSTER_COMMIT_COORDINATOR_SYNC=true
HOST_METRICS_ADDR=$HOST_METRICS_ADDR
SINGLE_NODE_DATA_DIR=$SINGLE_NODE_DATA_DIR
GATEWAY_ASYNC_SEND_WORKERS=2048
GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=500us
GATEWAY_SEND_TIMEOUT=14s
START_SCRIPT=$START_SCRIPT
WUKONGIM_CONFIG_SOURCE=$WUKONGIM_CONFIG_SOURCE_CANONICAL
WUKONGIM_CONFIG_SOURCE_REVIEWED=$WUKONGIM_CONFIG_SOURCE_REVIEWED
WUKONGIM_BIN=$WUKONGIM_BIN
WUKONGIM_LOG_DIR=$WUKONGIM_LOG_DIR
READY_TIMEOUT=$READY_TIMEOUT
PROFILE_SECONDS=$PROFILE_SECONDS
RUNTIME_POOL_SAMPLE_INTERVAL=$RUNTIME_POOL_SAMPLE_INTERVAL
RESOURCE_SAMPLE_INTERVAL=$RESOURCE_SAMPLE_INTERVAL
EOF
  write_redacted_effective_config
  if [[ -x "$START_SCRIPT" ]]; then
    verify_runtime_config_snapshot || die 'private runtime config snapshot changed before dry-run'
    WK_WUKONGIM_SINGLE_NODE_BIN="$WUKONGIM_BIN" \
    WK_WUKONGIM_SINGLE_NODE_CONFIG="$WUKONGIM_CONFIG" \
    WK_WUKONGIM_SINGLE_NODE_LOG_DIR="$WUKONGIM_LOG_DIR" \
      "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
  collect_node_logs before
  scrape_metrics_snapshot before
}

write_redacted_effective_config() {
  local source="$WUKONGIM_CONFIG"
  local destination="$OUT_DIR/config/effective-wukongim.toml"
  local temporary="$OUT_DIR/config/.effective-wukongim.toml.next.$$"
  verify_runtime_config_snapshot || return 1
  [[ -f "$source" && ! -L "$source" ]] || return 1
  mkdir -p "$OUT_DIR/config"
  rm -f "$temporary"
  # wkbench delegates to internal/config.RedactDiagnosticTOML, which parses
  # TOML and applies SchemaFields().DiagnosticSensitive fail-closed.
  "$WK_BENCH_BIN" report redact-config --input "$source" --output "$temporary" || {
    rm -f "$temporary"
    return 1
  }
  [[ -f "$temporary" && ! -L "$temporary" ]] || { rm -f "$temporary"; return 1; }
  cat >>"$temporary" <<EOF

[local_single_node_runtime]
topology_environment_overrides_rejected = true
endpoint_environment_overrides_rejected = true
product_environment_hermetic = true
initial_slot_count = 12
hash_slot_count = 256
slot_replica_n = 1
channel_replica_n = 1
commit_coordinator_flush_window = "200us"
commit_coordinator_shards = 1
commit_coordinator_sync = true
EOF
  mv "$temporary" "$destination"
}

discard_owned_wkbench_build() {
  [[ -n "$WK_BENCH_BUILD_DIR" ]] || return 0
  rm -f "$WK_BENCH_BUILD_DIR/wkbench" || return 1
  rmdir "$WK_BENCH_BUILD_DIR" || return 1
  WK_BENCH_BUILD_DIR=""
}

prepare_preflight_verifier_binary() {
  local destination="$OUT_DIR/bin" temporary
  [[ ! -e "$destination" && ! -L "$destination" ]] || return 1
  temporary="$(mktemp -d "$OUT_DIR/.preflight-bin.next.XXXXXX")" || return 1
  if ! copy_regular_binary "$WK_BENCH_BIN" "$temporary/wkbench"; then
    rm -rf "$temporary"
    return 1
  fi
  if ! mv "$temporary" "$destination"; then
    rm -rf "$temporary"
    return 1
  fi
  WK_BENCH_BIN="$OUT_DIR/$SEALED_WKBENCH_RELATIVE"
  discard_owned_wkbench_build
}

capture_source_state() {
  local label="$1" revision status_text valid=true clean=false output
  if ! revision="$(git -C "$ROOT_DIR" rev-parse --verify HEAD 2>/dev/null)" ||
    [[ ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
    revision=unknown
    valid=false
  fi
  if ! status_text="$(git -C "$ROOT_DIR" status --porcelain=v1 --untracked-files=normal --ignore-submodules=none 2>/dev/null)"; then
    valid=false
    status_text=source-observation-failed
  elif [[ -z "$status_text" ]]; then
    clean=true
  fi
  mkdir -p "$SOURCE_STATE_DIR" || return 1
  output="$SOURCE_STATE_DIR/$label.tsv"
  {
    printf 'schema\twukongim/chat-lifecycle-local-source-state/v1\n'
    printf 'checkpoint\t%s\n' "$label"
    printf 'observation_valid\t%s\n' "$valid"
    printf 'revision\t%s\n' "$revision"
    printf 'clean\t%s\n' "$clean"
  } >"$output" || return 1
  case "$label" in
    initial)
      SOURCE_INITIAL_VALID="$valid"
      SOURCE_INITIAL_REVISION="$revision"
      SOURCE_INITIAL_CLEAN="$clean"
      ;;
    post_build)
      SOURCE_POST_BUILD_VALID="$valid"
      SOURCE_POST_BUILD_REVISION="$revision"
      SOURCE_POST_BUILD_CLEAN="$clean"
      ;;
    final)
      SOURCE_FINAL_VALID="$valid"
      SOURCE_FINAL_REVISION="$revision"
      SOURCE_FINAL_CLEAN="$clean"
      ;;
    *)
      return 2
      ;;
  esac
  [[ "$valid" == true ]]
}

source_result_revision() {
  if [[ "$SOURCE_FINAL_VALID" == true ]]; then
    printf '%s' "$SOURCE_FINAL_REVISION"
  elif [[ "$SOURCE_INITIAL_VALID" == true ]]; then
    printf '%s' "$SOURCE_INITIAL_REVISION"
  else
    printf unknown
  fi
}

source_result_dirty() {
  if [[ "$SOURCE_FINAL_VALID" == true ]]; then
    [[ "$SOURCE_FINAL_CLEAN" == true ]] && printf false || printf true
  elif [[ "$SOURCE_INITIAL_VALID" == true ]]; then
    [[ "$SOURCE_INITIAL_CLEAN" == true ]] && printf false || printf true
  else
    printf true
  fi
}

PREFLIGHT_OUTCOME=insufficient_evidence
PREFLIGHT_REASON=preflight_not_completed
PREFLIGHT_FREE_PERCENT=0
PREFLIGHT_FILESYSTEM_OBSERVATION_COMPLETE=false

resolve_single_node_data_dir() {
  local requested="$SINGLE_NODE_DATA_DIR" parent base canonical_parent
  [[ "$requested" == /* && "$requested" != *$'\n'* && "$requested" != *$'\r'* ]] || \
    die "single-node data directory must be an absolute single-line path: $requested"
  [[ ! -L "$requested" ]] || die "single-node data directory must not be a symlink: $requested"
  if [[ -e "$requested" ]]; then
    [[ -d "$requested" ]] || die "single-node data directory must be a directory: $requested"
    CANONICAL_SINGLE_NODE_DATA_DIR="$(cd "$requested" && pwd -P)" || \
      die "cannot canonicalize single-node data directory: $requested"
  else
    parent="$(dirname "$requested")"
    base="$(basename "$requested")"
    [[ "$base" != . && "$base" != .. && -d "$parent" ]] || \
      die "single-node data directory parent must already exist: $parent"
    canonical_parent="$(cd "$parent" && pwd -P)" || \
      die "cannot canonicalize single-node data directory parent: $parent"
    CANONICAL_SINGLE_NODE_DATA_DIR="$canonical_parent/$base"
  fi
  case "$CANONICAL_SINGLE_NODE_DATA_DIR" in
    /|"$HOME"|"$ROOT_DIR"|"$ROOT_DIR/data")
      die "single-node data directory is too broad for managed cleanup: $CANONICAL_SINGLE_NODE_DATA_DIR"
      ;;
  esac
  SINGLE_NODE_DATA_DIR="$CANONICAL_SINGLE_NODE_DATA_DIR"
}

# capture_data_filesystem_observation samples the filesystem actually selected
# for WK_NODE_DATA_DIR. If a future data directory is not present yet, df is
# run against its existing parent; both paths resolve to the same target mount.
capture_data_filesystem_observation() {
  local output="$1" observed_path="$SINGLE_NODE_DATA_DIR" parent device blocks available
  DATA_FILESYSTEM_DEVICE=unavailable
  DATA_FILESYSTEM_TOTAL_BLOCKS=0
  DATA_FILESYSTEM_BLOCK_SIZE=0
  while [[ ! -e "$observed_path" && "$observed_path" != / ]]; do
    parent="$(dirname "$observed_path")"
    [[ "$parent" != "$observed_path" ]] || break
    observed_path="$parent"
  done
  [[ -e "$observed_path" ]] || return 1
  df -Pk "$observed_path" >"$output" || return 1
  read -r device blocks available < <(awk 'NR == 2 {print $1, $2, $4}' "$output")
  if [[ -z "${device:-}" || ! "${blocks:-}" =~ ^[0-9]+$ || ! "${available:-}" =~ ^[0-9]+$ ||
    "$blocks" -le 0 || "$available" -gt "$blocks" ]]; then
    return 1
  fi
  DATA_FILESYSTEM_DEVICE="$device"
  DATA_FILESYSTEM_TOTAL_BLOCKS="$blocks"
  # POSIX df -Pk reports total and available capacity in 1024-byte blocks.
  DATA_FILESYSTEM_BLOCK_SIZE=1024
}

write_local_preflight_result() {
  PREFLIGHT_OUTCOME="$1"
  PREFLIGHT_REASON="$2"
  PREFLIGHT_FREE_PERCENT="$3"
  printf 'schema\twukongim/chat-lifecycle-local-single-node-preflight/v1\noutcome\t%s\nreason\t%s\nobserved_filesystem_free_percent\t%s\n' \
    "$PREFLIGHT_OUTCOME" "$PREFLIGHT_REASON" "$PREFLIGHT_FREE_PERCENT" >"$OUT_DIR/preflight-result.tsv"
}

finalize_local_preflight_result() {
  local requested_status="$1" typed_status=0 published_status=0
  if ! write_redacted_effective_config; then
    PREFLIGHT_OUTCOME=insufficient_evidence
    PREFLIGHT_REASON=artifact_seal_verification_failed
    mkdir -p "$OUT_DIR/config" || return 6
    # Preserve a secret-free typed artifact even when structured source
    # redaction fails closed. It records unavailability, never source content.
    printf '# effective config unavailable: structured redaction failed\n' \
      >"$OUT_DIR/config/effective-wukongim.toml" || return 6
    write_local_preflight_result "$PREFLIGHT_OUTCOME" "$PREFLIGHT_REASON" "$PREFLIGHT_FREE_PERCENT" || return 6
  fi
  discard_runtime_config_snapshot || return 6
  # The verifier that derives and consumes the denial is itself sealed. The
  # server binary is deliberately absent because no server was started.
  prepare_preflight_verifier_binary || return 6
  write_local_artifact_identity preflight || return 6
  mkdir -p "$OUT_DIR/reports" || return 6
  write_typed_local_baseline_evidence "$PREFLIGHT_OUTCOME" false false "$PREFLIGHT_REASON" \
    "$PREFLIGHT_FREE_PERCENT" "$PREFLIGHT_FILESYSTEM_OBSERVATION_COMPLETE" || return 6
  derive_typed_local_baseline_authorization || typed_status=$?
  [[ "$typed_status" -eq "$requested_status" || "$typed_status" -eq 6 ]] || return 6
  if ! write_local_artifact_checksums || ! verify_local_artifact_checksums preflight; then
    return 6
  fi
  write_local_baseline_result "$PREFLIGHT_OUTCOME" "$PREFLIGHT_REASON" 0 0 false true || published_status=$?
  [[ -f "$OUT_DIR/local-baseline.json" && ! -L "$OUT_DIR/local-baseline.json" ]] || return 6
  "$WK_BENCH_BIN" report local-single-node-completion \
    --root "$OUT_DIR" --marker "$OUT_DIR/local-baseline.json" >/dev/null 2>&1 || published_status=$?
  [[ "$published_status" -eq "$requested_status" || "$published_status" -eq 6 ]] || return 6
  return "$published_status"
}

local_baseline_preflight() {
  local overlap available free_percent
  capture_data_filesystem_observation "$OUT_DIR/filesystem-preflight.txt" || {
    write_local_preflight_result insufficient_evidence filesystem_preflight_unavailable 0
    return 6
  }
  available="$(awk 'NR == 2 {print $4}' "$OUT_DIR/filesystem-preflight.txt")"
  free_percent=$((available * 100 / DATA_FILESYSTEM_TOTAL_BLOCKS))
  PREFLIGHT_FREE_PERCENT="$free_percent"
  PREFLIGHT_FILESYSTEM_OBSERVATION_COMPLETE=true
  if (( free_percent < MINIMUM_FREE_PERCENT )); then
    write_local_preflight_result storage_confounded filesystem_free_below_10_percent "$free_percent"
    return 2
  fi
  if [[ "$START_CLUSTER" -eq 1 ]]; then
    if [[ ! -x "$HOST_OVERLAP_DETECTOR" ]] || ! overlap="$("$HOST_OVERLAP_DETECTOR")"; then
      write_local_preflight_result insufficient_evidence host_overlap_observation_failed "$free_percent"
      return 6
    fi
    if [[ -n "$overlap" ]]; then
      write_local_preflight_result host_confounded overlapping_wukongim_workload "$free_percent"
      return 2
    fi
  fi
  return 0
}

LATEST_OUTCOME=""
LATEST_REASON=""
LATEST_EXIT_STATUS=0

classify_latest_local_step() {
  local qps="$1" tag row status exit_status actual success errors connected planned dispatched dropped
  local available free_percent addr
  tag="$(qps_tag "$qps")"
  row="$(awk -F '\t' -v tag="$tag" '$1 == tag {line=$0} END {print line}' "$OUT_DIR/summary.tsv")"
  if [[ -z "$row" ]]; then
    LATEST_OUTCOME=insufficient_evidence; LATEST_REASON=missing_summary_row; LATEST_EXIT_STATUS=6
    return
  fi
  IFS=$'\t' read -r _ _ status exit_status actual success errors _ _ _ _ _ _ connected planned dispatched dropped <<<"$row"
  if [[ -z "${dropped:-}" ]]; then
    LATEST_OUTCOME=insufficient_evidence; LATEST_REASON=incomplete_summary_row; LATEST_EXIT_STATUS=6
    return
  fi
  if [[ -f "$OUT_DIR/reports/${tag}-qps/host-overlap.detected" ]]; then
    LATEST_OUTCOME=host_confounded; LATEST_REASON=measured_host_overlap; LATEST_EXIT_STATUS=2
    return
  fi
  if ! awk -F '\t' -v tag="$tag" '$1 == tag {seen++; if ($3 != "complete") bad=1} END {exit !(seen == 1 && !bad)}' \
    "$OUT_DIR/storage_metrics_summary.tsv"; then
    LATEST_OUTCOME=insufficient_evidence; LATEST_REASON=storage_metrics_incomplete; LATEST_EXIT_STATUS=6
    return
  fi
  if ! awk -F '\t' -v tag="$tag" '$1 == tag {seen++; if ($3 != "complete" && $3 != "unavailable") bad=1} END {exit !(seen == 1 && !bad)}' \
    "$OUT_DIR/host_io_summary.tsv"; then
    LATEST_OUTCOME=insufficient_evidence; LATEST_REASON=host_io_evidence_incomplete; LATEST_EXIT_STATUS=6
    return
  fi
  if [[ -z "$HOST_METRICS_PID" ]] || ! kill -0 "$HOST_METRICS_PID" 2>/dev/null || ! worker_ready; then
    LATEST_OUTCOME=insufficient_evidence; LATEST_REASON=benchmark_process_exit; LATEST_EXIT_STATUS=6
    return
  fi
  if [[ "$START_CLUSTER" -eq 1 ]] && { [[ -z "$CLUSTER_PID" ]] || ! kill -0 "$CLUSTER_PID" 2>/dev/null; }; then
    LATEST_OUTCOME=product_failure; LATEST_REASON=service_process_exit; LATEST_EXIT_STATUS=3
    return
  fi
  for addr in "${API_VALUES[@]}"; do
    if ! curl -fsS --max-time 2 "${addr%/}/readyz" >/dev/null 2>&1; then
      LATEST_OUTCOME=product_failure; LATEST_REASON=service_readiness_lost; LATEST_EXIT_STATUS=3
      return
    fi
  done
  if ! capture_data_filesystem_observation "$OUT_DIR/metrics/$tag/filesystem-step.txt"; then
    LATEST_OUTCOME=insufficient_evidence; LATEST_REASON=filesystem_observation_missing; LATEST_EXIT_STATUS=6
    return
  fi
  available="$(awk 'NR == 2 {print $4}' "$OUT_DIR/metrics/$tag/filesystem-step.txt")"
  free_percent=$((available * 100 / DATA_FILESYSTEM_TOTAL_BLOCKS))
  if (( free_percent < MINIMUM_FREE_PERCENT )); then
    LATEST_OUTCOME=storage_confounded; LATEST_REASON=filesystem_free_below_10_percent; LATEST_EXIT_STATUS=2
    return
  fi
  if [[ "$status" != passed || "$exit_status" -ne 0 || "$errors" -ne 0 ]] ||
    ! awk -v actual="$actual" -v offered="$qps" -v minimum="$ACTUAL_QPS_MIN_RATIO" \
      'BEGIN {exit !(offered > 0 && actual / offered >= minimum)}' ||
    [[ "$connected" -lt "$USERS" || "$planned" -le 0 || "$dispatched" -ne "$planned" ||
      $((success + errors)) -ne "$dispatched" || "$dropped" -ne 0 ]]; then
    LATEST_OUTCOME=rate_failed; LATEST_REASON=underdelivery_or_incomplete_accounting; LATEST_EXIT_STATUS=3
    return
  fi
  LATEST_OUTCOME=clean; LATEST_REASON=complete; LATEST_EXIT_STATUS=0
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  else
    shasum -a 256 "$path" | awk '{print $1}'
  fi
}

sha256_text() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum | awk '{print $1}'
  else
    shasum -a 256 | awk '{print $1}'
  fi
}

freeze_runtime_config() {
  local source="$WUKONGIM_CONFIG_SOURCE" source_dir source_base canonical_source
  local default_source="$ROOT_DIR/scripts/wukongim/wukongim.toml" default_dir canonical_default
  local temp_parent before_sha after_sha snapshot_sha head_sha temporary
  [[ "$source" != *$'\n'* && "$source" != *$'\r'* && "$source" != *$'\t'* ]] || return 1
  [[ -f "$source" && ! -L "$source" ]] || return 1
  source_dir="$(cd "$(dirname "$source")" && pwd -P)" || return 1
  source_base="$(basename "$source")"
  canonical_source="$source_dir/$source_base"
  [[ -f "$canonical_source" && ! -L "$canonical_source" ]] || return 1
  default_dir="$(cd "$(dirname "$default_source")" && pwd -P)" || return 1
  canonical_default="$default_dir/$(basename "$default_source")"

  temp_parent="${TMPDIR:-/tmp}"
  [[ "$temp_parent" == /* && -d "$temp_parent" && ! -L "$temp_parent" ]] || return 1
  temp_parent="$(cd "$temp_parent" && pwd -P)" || return 1
  [[ "$temp_parent" != "$OUT_DIR" && "$temp_parent" != "$OUT_DIR/"* ]] || return 1
  RUNTIME_CONFIG_DIR="$(mktemp -d "$temp_parent/wukongim-single-node-config.XXXXXX")" || return 1
  [[ "$RUNTIME_CONFIG_DIR" == /* && ! -L "$RUNTIME_CONFIG_DIR" && -d "$RUNTIME_CONFIG_DIR" ]] || return 1
  chmod 0700 "$RUNTIME_CONFIG_DIR" || return 1
  temporary="$RUNTIME_CONFIG_DIR/config.toml.next"
  RUNTIME_CONFIG_SNAPSHOT="$RUNTIME_CONFIG_DIR/config.toml"

  before_sha="$(sha256_file "$canonical_source")" || return 1
  cp "$canonical_source" "$temporary" || return 1
  chmod 0600 "$temporary" || return 1
  [[ -f "$canonical_source" && ! -L "$canonical_source" ]] || return 1
  after_sha="$(sha256_file "$canonical_source")" || return 1
  snapshot_sha="$(sha256_file "$temporary")" || return 1
  [[ "$before_sha" =~ ^[0-9a-f]{64}$ && "$before_sha" == "$after_sha" && "$before_sha" == "$snapshot_sha" ]] || return 1
  mv "$temporary" "$RUNTIME_CONFIG_SNAPSHOT" || return 1
  [[ -f "$RUNTIME_CONFIG_SNAPSHOT" && ! -L "$RUNTIME_CONFIG_SNAPSHOT" ]] || return 1

  WUKONGIM_CONFIG_SOURCE_CANONICAL="$canonical_source"
  WUKONGIM_CONFIG_SOURCE_REVIEWED=false
  if [[ "$canonical_source" == "$canonical_default" ]]; then
    head_sha="$(git -C "$ROOT_DIR" show HEAD:scripts/wukongim/wukongim.toml 2>/dev/null | sha256_text)" || return 1
    [[ "$head_sha" =~ ^[0-9a-f]{64}$ ]] || return 1
    [[ "$snapshot_sha" == "$head_sha" ]] && WUKONGIM_CONFIG_SOURCE_REVIEWED=true
  fi
  RUNTIME_CONFIG_SHA256="$snapshot_sha"
  WUKONGIM_CONFIG="$RUNTIME_CONFIG_SNAPSHOT"
}

verify_runtime_config_snapshot() {
  local actual
  [[ -n "$RUNTIME_CONFIG_DIR" && -n "$RUNTIME_CONFIG_SNAPSHOT" &&
    -d "$RUNTIME_CONFIG_DIR" && ! -L "$RUNTIME_CONFIG_DIR" &&
    -f "$RUNTIME_CONFIG_SNAPSHOT" && ! -L "$RUNTIME_CONFIG_SNAPSHOT" &&
    "$RUNTIME_CONFIG_SHA256" =~ ^[0-9a-f]{64}$ ]] || return 1
  actual="$(sha256_file "$RUNTIME_CONFIG_SNAPSHOT")" || return 1
  [[ "$actual" == "$RUNTIME_CONFIG_SHA256" ]]
}

discard_runtime_config_snapshot() {
  local directory="$RUNTIME_CONFIG_DIR"
  [[ -n "$directory" ]] || return 0
  [[ "$directory" == /* && "$(basename "$directory")" == wukongim-single-node-config.* &&
    -d "$directory" && ! -L "$directory" ]] || return 1
  rm -f -- "$directory/config.toml.next" "$directory/config.toml" || return 1
  rmdir "$directory" || return 1
  RUNTIME_CONFIG_DIR=""
  RUNTIME_CONFIG_SNAPSHOT=""
  WUKONGIM_CONFIG=""
}

copy_regular_binary() {
  local source="$1" destination="$2"
  [[ -f "$source" && ! -L "$source" && -x "$source" ]] || return 1
  cp "$source" "$destination" || return 1
  chmod 0700 "$destination" || return 1
  [[ -f "$destination" && ! -L "$destination" && -x "$destination" ]]
}

prepare_sealed_test_binaries() {
  local destination="$OUT_DIR/bin" temporary
  [[ ! -e "$destination" && ! -L "$destination" ]] || return 1
  temporary="$(mktemp -d "$OUT_DIR/.bin.next.XXXXXX")" || return 1
  if ! copy_regular_binary "$WK_BENCH_BIN" "$temporary/wkbench"; then
    rm -rf "$temporary"
    return 1
  fi
  if ! mv "$temporary" "$destination"; then
    rm -rf "$temporary"
    return 1
  fi
  WK_BENCH_BIN="$OUT_DIR/$SEALED_WKBENCH_RELATIVE"
  discard_owned_wkbench_build || return 1
  if [[ "$START_CLUSTER" -eq 1 ]]; then
    WUKONGIM_BIN="$OUT_DIR/$SEALED_WUKONGIM_RELATIVE"
  fi
}

seal_local_binaries() {
  local path current_wukongim_sha
  for path in "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE" "$OUT_DIR/$SEALED_WKBENCH_RELATIVE"; do
    [[ -f "$path" && ! -L "$path" && -x "$path" ]] || return 1
    chmod 0700 "$path" || return 1
  done
  [[ "$SEALED_WUKONGIM_SHA256" =~ ^[0-9a-f]{64}$ ]] || return 1
  current_wukongim_sha="$(sha256_file "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE")" || return 1
  [[ "$current_wukongim_sha" == "$SEALED_WUKONGIM_SHA256" ]]
}

write_local_artifact_identity() {
  local seal_scope="${1:-measured}"
  local output="$OUT_DIR/artifact-identity.tsv"
  local revision dirty source_rebuildable source_capture original_config_sha config_sha wukongim_sha wkbench_sha
  revision="$(source_result_revision)"
  dirty="$(source_result_dirty)"
  source_rebuildable=false
  source_capture=binary_identity_only
  if [[ "$seal_scope" == measured ]] &&
    [[ "$WUKONGIM_CONFIG_SOURCE_REVIEWED" == true ]] &&
    [[ "$WKBENCH_BUILT_FROM_CURRENT_SOURCE" == true ]] &&
    [[ "$WUKONGIM_BUILT_FROM_CURRENT_SOURCE" == true ]] &&
    [[ "$SOURCE_INITIAL_VALID" == true && "$SOURCE_INITIAL_CLEAN" == true ]] &&
    [[ "$SOURCE_POST_BUILD_VALID" == true && "$SOURCE_POST_BUILD_CLEAN" == true ]] &&
    [[ "$SOURCE_FINAL_VALID" == true && "$SOURCE_FINAL_CLEAN" == true ]] &&
    [[ "$SOURCE_INITIAL_REVISION" == "$SOURCE_POST_BUILD_REVISION" ]] &&
    [[ "$SOURCE_INITIAL_REVISION" == "$SOURCE_FINAL_REVISION" ]] &&
    [[ "$revision" =~ ^[0-9a-f]{40}$ ]]; then
    source_rebuildable=true
    source_capture=revision_and_binary_identity
  fi
  original_config_sha=unavailable
  config_sha=unavailable
  wukongim_sha=unavailable
  wkbench_sha=unavailable
  if [[ "$RUNTIME_CONFIG_SHA256" =~ ^[0-9a-f]{64}$ ]]; then
    original_config_sha="$RUNTIME_CONFIG_SHA256"
  fi
  if [[ -f "$OUT_DIR/config/effective-wukongim.toml" && ! -L "$OUT_DIR/config/effective-wukongim.toml" ]]; then
    config_sha="$(sha256_file "$OUT_DIR/config/effective-wukongim.toml")"
  fi
  if [[ -f "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE" && ! -L "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE" ]]; then
    wukongim_sha="$(sha256_file "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE")"
  fi
  if [[ -f "$OUT_DIR/$SEALED_WKBENCH_RELATIVE" && ! -L "$OUT_DIR/$SEALED_WKBENCH_RELATIVE" ]]; then
    wkbench_sha="$(sha256_file "$OUT_DIR/$SEALED_WKBENCH_RELATIVE")"
  fi
  {
    printf 'schema\twukongim/chat-lifecycle-local-single-node-artifact-identity/v1\n'
    printf 'baseline_invocation_id\t%s\n' "$BASELINE_INVOCATION_ID"
    printf 'source_revision\t%s\n' "$revision"
    printf 'source_dirty\t%s\n' "$dirty"
    printf 'source_rebuildable_from_revision\t%s\n' "$source_rebuildable"
    printf 'source_capture\t%s\n' "$source_capture"
    printf 'seal_scope\t%s\n' "$seal_scope"
    printf 'canonical_data_dir\t%s\n' "$CANONICAL_SINGLE_NODE_DATA_DIR"
    printf 'data_filesystem_device\t%s\n' "$DATA_FILESYSTEM_DEVICE"
    printf 'data_filesystem_total_blocks\t%s\n' "$DATA_FILESYSTEM_TOTAL_BLOCKS"
    printf 'data_filesystem_block_size\t%s\n' "$DATA_FILESYSTEM_BLOCK_SIZE"
    printf 'original_config_sha256\t%s\n' "$original_config_sha"
    printf 'effective_config\tconfig/effective-wukongim.toml\n'
    printf 'effective_config_sha256\t%s\n' "$config_sha"
    printf 'wukongim_binary\t%s\n' "$SEALED_WUKONGIM_RELATIVE"
    printf 'wukongim_binary_sha256\t%s\n' "$wukongim_sha"
    printf 'wkbench_binary\t%s\n' "$SEALED_WKBENCH_RELATIVE"
    printf 'wkbench_binary_sha256\t%s\n' "$wkbench_sha"
  } >"$output"
}

local_artifact_payload_ready() {
  local identity="$OUT_DIR/artifact-identity.tsv"
  local expected actual relative
  for required in \
    "$OUT_DIR/config/effective-wukongim.toml" \
    "$OUT_DIR/logs/after/node1.log" \
    "$identity" \
    "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE" \
    "$OUT_DIR/$SEALED_WKBENCH_RELATIVE"; do
    [[ -f "$required" && ! -L "$required" ]] || return 1
  done
  expected="$(awk -F '\t' '$1 == "effective_config_sha256" { print $2 }' "$identity")"
  actual="$(sha256_file "$OUT_DIR/config/effective-wukongim.toml")" || return 1
  [[ "$expected" == "$actual" ]] || return 1
  expected="$(awk -F '\t' '$1 == "wukongim_binary_sha256" { print $2 }' "$identity")"
  relative="$(awk -F '\t' '$1 == "wukongim_binary" { print $2 }' "$identity")"
  [[ "$relative" == "$SEALED_WUKONGIM_RELATIVE" ]] || return 1
  actual="$(sha256_file "$OUT_DIR/$relative")" || return 1
  [[ "$expected" == "$actual" ]] || return 1
  expected="$(awk -F '\t' '$1 == "wkbench_binary_sha256" { print $2 }' "$identity")"
  relative="$(awk -F '\t' '$1 == "wkbench_binary" { print $2 }' "$identity")"
  [[ "$relative" == "$SEALED_WKBENCH_RELATIVE" ]] || return 1
  actual="$(sha256_file "$OUT_DIR/$relative")" || return 1
  [[ "$expected" == "$actual" ]] || return 1
}

reviewed_contract_defaults_satisfied() {
  [[ "$WUKONGIM_CONFIG_SOURCE_REVIEWED" == true ]] || return 1
  [[ "$QPS_LIST" == "250,500,750,1000" ]] || return 1
  [[ "$CHANNELS" -eq 1000 ]] || return 1
  [[ "$USERS" -eq 2500 ]] || return 1
  [[ "$GROUP_MEMBERS" -eq 10 ]] || return 1
  [[ "$CONCURRENCY" -eq 2800 ]] || return 1
  [[ "$PAYLOAD_BYTES" -eq 128 ]] || return 1
  [[ "$DURATION" == 5m ]] || return 1
  [[ "$WARMUP" == 60s ]] || return 1
	[[ "$COOLDOWN" == 90s ]] || return 1
	[[ "$TERMINAL_CUT_ACK_SAFETY_SECONDS" -eq 15 ]] || return 1
  [[ "$PROFILE_SECONDS" == 10 ]] || return 1
  [[ "$STORAGE_OVERLAP_SAMPLE_INTERVAL" == 20 ]] || return 1
  [[ "$ACK_TIMEOUT" == 15s ]] || return 1
  [[ "$RECV_ACK" == true ]] || return 1
  [[ "$HEARTBEAT_ENABLED" == true ]] || return 1
  [[ "$SENDER_PICK" == round_robin ]] || return 1
  [[ "$START_CLUSTER" -eq 1 ]] || return 1
  [[ "$START_WORKER" -eq 1 ]] || return 1
  [[ "$CLEAN_CLUSTER" -eq 1 ]] || return 1
  [[ "$MINIMUM_FREE_PERCENT" -eq 10 ]] || return 1
  [[ "$RUNTIME_CONFIG_SHA256" =~ ^[0-9a-f]{64}$ ]] || return 1
}

write_typed_local_step_evidence() {
  local qps="$1"
  local tag report_dir metrics_id warmup_seconds measured_seconds drain_seconds output result_output closure_output step_manifest exit_status=0
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  metrics_id="$(metric_file_id "${METRICS_VALUES[0]:-missing}")"
  warmup_seconds="$(duration_seconds "$WARMUP")"
  measured_seconds="$(duration_seconds "$DURATION")"
  drain_seconds="$(duration_seconds "$COOLDOWN")"
  output="$report_dir/typed-step-evidence.json"
  result_output="$report_dir/typed-step-result.json"
  closure_output="$report_dir/evidence/step-closure.json"
  step_manifest="$report_dir/evidence/step-checksums.sha256"
  [[ -d "$report_dir" ]] || return 1
  "$WK_BENCH_BIN" report local-single-node-step \
    --offered-qps "$qps" \
    --required-active-connections "$USERS" \
    --group-members "$GROUP_MEMBERS" \
    --warmup-seconds "$warmup_seconds" \
    --measured-seconds "$measured_seconds" \
    --drain-budget-seconds "$drain_seconds" \
    --maximum-sample-gap-seconds 30 \
    --scenario "$report_dir/scenario.yaml" \
    --plan "$report_dir/plan.json" \
    --run-report "$report_dir/report.json" \
    --diagnostic-summary "$report_dir/diagnostic-summary.json" \
    --lifecycle "$report_dir/lifecycle-status.jsonl" \
    --post-warmup-metrics "$OUT_DIR/metrics/$tag/${metrics_id}-post-warmup.prom" \
    --terminal-metrics "$OUT_DIR/metrics/$tag/${metrics_id}-terminal-pre-close.prom" \
    --storage-overlap "$report_dir/evidence/storage-overlap.tsv" \
    --storage-summary "$report_dir/evidence/storage-summary.tsv" \
    --host-io-summary "$report_dir/evidence/host-io-summary.tsv" \
    --profile-status "$report_dir/evidence/threshold-pprof-status.json" \
    --payload-root "$OUT_DIR" \
    --payload-manifest "$step_manifest" \
    --output "$output" \
    --result-output "$result_output" \
    --closure-output "$closure_output" >/dev/null 2>&1 || exit_status=$?
  [[ -f "$output" && ! -L "$output" && -f "$result_output" && ! -L "$result_output" &&
    -f "$closure_output" && ! -L "$closure_output" ]] || return 6
  return "$exit_status"
}

local_step_manifest_path() {
  printf '%s\n' "$OUT_DIR/reports/$(qps_tag "$1")-qps/evidence/step-checksums.sha256"
}

write_local_step_checksums() {
  local qps="$1" tag report_dir metrics_dir manifest temporary path digest relative
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  metrics_dir="$OUT_DIR/metrics/$tag"
  manifest="$(local_step_manifest_path "$qps")"
  temporary="${manifest}.tmp.$$"
  [[ -d "$report_dir" && -d "$metrics_dir" && ! -L "$report_dir" && ! -L "$metrics_dir" ]] || return 1
  mkdir -p "$(dirname "$manifest")" || return 1
  : >"$temporary" || return 1
  while IFS= read -r path; do
    [[ "$path" == "$manifest" || "$path" == "$temporary" ||
      "$path" == "$report_dir/typed-step-evidence.json" ||
      "$path" == "$report_dir/typed-step-result.json" ||
      "$path" == "$report_dir/evidence/step-closure.json" ||
      "$path" == "$report_dir/evidence/typed-step-consumer.json" ]] && continue
    digest="$(sha256_file "$path")" || { rm -f "$temporary"; return 1; }
    relative="${path#"$OUT_DIR"/}"
    printf '%s  %s\n' "$digest" "$relative" >>"$temporary" || { rm -f "$temporary"; return 1; }
  done < <(
    {
      find "$report_dir" "$metrics_dir" -type f -print
      printf '%s\n' \
        "$OUT_DIR/config/effective-wukongim.toml" \
        "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE" \
        "$OUT_DIR/$SEALED_WKBENCH_RELATIVE"
    } | LC_ALL=C sort -u
  )
  [[ -s "$temporary" ]] || { rm -f "$temporary"; return 1; }
  mv "$temporary" "$manifest"
}

verify_local_step_checksums() {
  local qps="$1" tag report_dir metrics_dir manifest digest relative extra path actual
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  metrics_dir="$OUT_DIR/metrics/$tag"
  manifest="$(local_step_manifest_path "$qps")"
  [[ -s "$manifest" && -f "$manifest" && ! -L "$manifest" ]] || return 1
  while read -r digest relative extra; do
    [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-f]{64}$ && -n "$relative" ]] || return 1
    [[ "$relative" != /* && "$relative" != ../* && "$relative" != */../* && "$relative" != */.. ]] || return 1
    path="$OUT_DIR/$relative"
    [[ -f "$path" && ! -L "$path" ]] || return 1
    actual="$(sha256_file "$path")" || return 1
    [[ "$actual" == "$digest" ]] || return 1
  done <"$manifest"
  while IFS= read -r path; do
    [[ "$path" == "$manifest" || "$path" == "$report_dir/typed-step-evidence.json" ||
      "$path" == "$report_dir/typed-step-result.json" ||
      "$path" == "$report_dir/evidence/step-closure.json" ||
      "$path" == "$report_dir/evidence/typed-step-consumer.json" ]] && continue
    relative="${path#"$OUT_DIR"/}"
    awk -v expected="$relative" '$2 == expected { found = 1 } END { exit !found }' "$manifest" || return 1
  done < <(find "$report_dir" "$metrics_dir" -type f -print | LC_ALL=C sort)
}

read_typed_local_step_result() {
  local qps="$1" tag result closure consumer outcome reason consumer_status=0 expected_status
  tag="$(qps_tag "$qps")"
  closure="$OUT_DIR/reports/${tag}-qps/evidence/step-closure.json"
  consumer="$OUT_DIR/reports/${tag}-qps/evidence/typed-step-consumer.json"
  [[ -f "$closure" && ! -L "$closure" ]] || return 1
  "$WK_BENCH_BIN" report local-single-node-step-closure \
    --root "$OUT_DIR" --closure "$closure" --output "$consumer" >/dev/null 2>&1 || consumer_status=$?
  [[ -f "$consumer" && ! -L "$consumer" ]] || return 1
  jq -e --argjson qps "$qps" '
    .schema == "wukongim/chat-lifecycle-local-single-node-step-result/v1" and
    .offered_send_qps == $qps and
    ((.reasons | type) == "array") and
    ((.outcome == "clean" and .clean == true and (.reasons | length) == 0) or
     (.outcome != "clean" and .clean == false and (.reasons | length) > 0))
  ' "$consumer" >/dev/null 2>&1 || return 1
  outcome="$(jq -r '.outcome' "$consumer")"
  reason="$(jq -r 'if (.reasons | length) > 0 then .reasons[0] else "complete" end' "$consumer")"
  case "$outcome" in
    clean)
      LATEST_OUTCOME=clean; LATEST_REASON=complete; LATEST_EXIT_STATUS=0
      ;;
    rate_failed|product_failure)
      LATEST_OUTCOME="$outcome"; LATEST_REASON="$reason"; LATEST_EXIT_STATUS=3
      ;;
    insufficient_evidence)
      LATEST_OUTCOME=insufficient_evidence; LATEST_REASON="$reason"; LATEST_EXIT_STATUS=6
      ;;
    *)
      return 1
      ;;
  esac
  expected_status="$LATEST_EXIT_STATUS"
  [[ "$consumer_status" -eq "$expected_status" ]] || return 1
}

write_typed_local_baseline_evidence() {
  local diagnostic_outcome="$1" payload_complete="$2" checksums_verified="$3"
  local revision dirty rebuildable warmup_seconds measured_seconds drain_seconds ack_timeout_seconds clean_cluster
  local diagnostic_reason="${4:-$diagnostic_outcome}"
  local observed_filesystem_free_percent="${5:-0}"
  local filesystem_observation_complete="${6:-false}"
  revision="$(source_result_revision)"
  dirty="$(source_result_dirty)"
  rebuildable="$(awk -F '\t' '$1 == "source_rebuildable_from_revision" {print $2}' "$OUT_DIR/artifact-identity.tsv")"
  warmup_seconds="$(duration_seconds "$WARMUP")"
  measured_seconds="$(duration_seconds "$DURATION")"
  drain_seconds="$(duration_seconds "$COOLDOWN")"
  ack_timeout_seconds="$(duration_seconds "$ACK_TIMEOUT")"
  clean_cluster=false
  [[ "$CLEAN_CLUSTER" -eq 1 ]] && clean_cluster=true
  jq -n \
    --arg outcome "$diagnostic_outcome" \
    --arg baseline_invocation_id "$BASELINE_INVOCATION_ID" \
    --arg diagnostic_reason "$diagnostic_reason" \
    --argjson observed_filesystem_free_percent "$observed_filesystem_free_percent" \
    --argjson filesystem_observation_complete "$filesystem_observation_complete" \
    --arg canonical_data_dir "$CANONICAL_SINGLE_NODE_DATA_DIR" \
    --arg data_filesystem_device "$DATA_FILESYSTEM_DEVICE" \
    --argjson data_filesystem_total_blocks "$DATA_FILESYSTEM_TOTAL_BLOCKS" \
    --argjson data_filesystem_block_size "$DATA_FILESYSTEM_BLOCK_SIZE" \
    --arg revision "$revision" \
    --argjson dirty "$dirty" \
    --argjson rebuildable "${rebuildable:-false}" \
    --argjson canonical_source_config "$WUKONGIM_CONFIG_SOURCE_REVIEWED" \
    --argjson active_connections "$USERS" \
    --argjson channels "$CHANNELS" \
    --argjson group_members "$GROUP_MEMBERS" \
    --argjson send_concurrency "$CONCURRENCY" \
    --argjson payload_bytes "$PAYLOAD_BYTES" \
    --argjson warmup_seconds "$warmup_seconds" \
    --argjson measured_seconds "$measured_seconds" \
    --argjson drain_seconds "$drain_seconds" \
    --argjson ack_timeout_seconds "$ack_timeout_seconds" \
    --argjson receive_ack "$RECV_ACK" \
    --argjson heartbeat_enabled "$HEARTBEAT_ENABLED" \
    --argjson sender_pick_round_robin "$([[ "$SENDER_PICK" == round_robin ]] && printf true || printf false)" \
    --argjson minimum_free_percent "$MINIMUM_FREE_PERCENT" \
    --argjson logical_slot_groups 12 \
    --argjson hash_slots 256 \
    --argjson commit_shards 1 \
    --argjson sync_commit true \
    --argjson clean_cluster "$clean_cluster" \
    --argjson owned_cluster "$([[ "$OWNED_CLUSTER_STARTED" -eq 1 ]] && printf true || printf false)" \
    --argjson owned_worker "$([[ "$OWNED_WORKER_STARTED" -eq 1 ]] && printf true || printf false)" \
    --argjson metrics_endpoint_count "${#METRICS_VALUES[@]}" \
    --argjson payload_complete "$payload_complete" \
    --argjson checksums_verified "$checksums_verified" '
      {
        schema:"wukongim/chat-lifecycle-local-single-node-baseline-evidence/v1",
        completion_generation:"",
        baseline_invocation_id:$baseline_invocation_id,
        diagnostic_outcome:$outcome,
        diagnostic_reason:$diagnostic_reason,
        filesystem_observation_complete:$filesystem_observation_complete,
        observed_filesystem_free_percent:$observed_filesystem_free_percent,
        canonical_data_dir:$canonical_data_dir,
        data_filesystem_device:$data_filesystem_device,
        data_filesystem_total_blocks:$data_filesystem_total_blocks,
        data_filesystem_block_size:$data_filesystem_block_size,
        settings:{
          canonical_source_config:$canonical_source_config,
          channels:$channels,active_connections:$active_connections,group_members:$group_members,
          send_concurrency:$send_concurrency,payload_bytes:$payload_bytes,warmup_seconds:$warmup_seconds,
          measured_seconds:$measured_seconds,drain_budget_seconds:$drain_seconds,
          ack_timeout_seconds:$ack_timeout_seconds,receive_ack:$receive_ack,
          heartbeat_enabled:$heartbeat_enabled,sender_pick_round_robin:$sender_pick_round_robin,
          minimum_filesystem_free_percent:$minimum_free_percent,
          logical_slot_groups:$logical_slot_groups,hash_slots:$hash_slots,
          slot_replicas:1,channel_replicas:1,commit_flush_window_micros:200,
          commit_coordinator_shards:$commit_shards,sync_commit:$sync_commit,
          clean_cluster:$clean_cluster,owned_cluster:$owned_cluster,owned_worker:$owned_worker,
          metrics_endpoint_count:$metrics_endpoint_count
        },
        source:{revision:$revision,dirty:$dirty,rebuildable_from_revision:$rebuildable},
        seal:{payload_complete:$payload_complete,checksums_verified:$checksums_verified},
        step_closures:[]
      }
    ' >"$OUT_DIR/reports/local-baseline-draft.json"
}

derive_typed_local_baseline_authorization() {
  local qps closure exit_status=0
  local -a args=(
    report local-single-node-baseline
    --root "$OUT_DIR"
    --evidence "$OUT_DIR/reports/local-baseline-draft.json"
    --sealed-evidence-output "$OUT_DIR/reports/local-baseline-evidence.json"
    --output "$OUT_DIR/reports/local-baseline-authorization.json"
  )
  for qps in "${QPS_VALUES[@]}"; do
    closure="$OUT_DIR/reports/$(qps_tag "$qps")-qps/evidence/step-closure.json"
    if [[ -f "$closure" && ! -L "$closure" ]]; then
      args+=(--step-closure "$closure")
    fi
  done
  "$WK_BENCH_BIN" "${args[@]}" >/dev/null 2>&1 || exit_status=$?
  [[ -f "$OUT_DIR/reports/local-baseline-evidence.json" && ! -L "$OUT_DIR/reports/local-baseline-evidence.json" &&
    -f "$OUT_DIR/reports/local-baseline-authorization.json" && ! -L "$OUT_DIR/reports/local-baseline-authorization.json" ]] || return 6
  return "$exit_status"
}

validate_and_prepare_out_dir() {
  local requested="$OUT_DIR" parent base canonical repo_canonical user_home_canonical existed=false first_entry
  [[ -n "$requested" ]] || die 'OUT_DIR must not be empty'
  while [[ "$requested" != / && "$requested" == */ ]]; do requested="${requested%/}"; done
  [[ ! -L "$requested" ]] || die "OUT_DIR must not be a symlink: $requested"

  if [[ -e "$requested" ]]; then
    [[ -d "$requested" ]] || die "OUT_DIR must be a directory: $requested"
    canonical="$(cd "$requested" && pwd -P)" || die "cannot canonicalize OUT_DIR: $requested"
    existed=true
  else
    parent="$(dirname "$requested")"
    base="$(basename "$requested")"
    [[ "$base" != . && "$base" != .. && -n "$base" ]] || die "invalid OUT_DIR basename: $requested"
    [[ -d "$parent" ]] || die "OUT_DIR parent must already exist: $parent"
    canonical="$(cd "$parent" && pwd -P)/$base" || die "cannot canonicalize OUT_DIR parent: $parent"
  fi

  repo_canonical="$(cd "$ROOT_DIR" && pwd -P)" || die 'cannot canonicalize repository root'
  user_home_canonical=""
  if [[ -n "${HOME:-}" && -d "$HOME" ]]; then
    user_home_canonical="$(cd "$HOME" && pwd -P)" || die 'cannot canonicalize user home'
  fi
  [[ "$canonical" != / ]] || die 'OUT_DIR must not be filesystem root'
  [[ "$canonical" != "$repo_canonical" ]] || die 'OUT_DIR must not be repository root'
  [[ -z "$user_home_canonical" || "$canonical" != "$user_home_canonical" ]] || die 'OUT_DIR must not be HOME'

  if [[ "$existed" == true ]]; then
    if ! first_entry="$(find "$canonical" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)"; then
      die "cannot inspect OUT_DIR contents: $canonical"
    fi
    [[ -z "$first_entry" ]] || die "OUT_DIR directory_not_empty: $canonical"
  fi

  if [[ ! -e "$canonical" ]]; then
    mkdir "$canonical" || die "cannot create dedicated OUT_DIR: $canonical"
  fi
  chmod 0700 "$canonical" || die "cannot protect OUT_DIR: $canonical"
  OUT_DIR="$canonical"
  SOURCE_STATE_DIR="$OUT_DIR/source-state"
}

write_local_artifact_checksums() {
  local output="$OUT_DIR/checksums.sha256" temporary="$OUT_DIR/.checksums.sha256.tmp.$$"
  local path digest
  : >"$temporary"
  while IFS= read -r path; do
    [[ "$path" == "$output" || "$path" == "$temporary" || "$path" == "$OUT_DIR/local-baseline.json" ]] && continue
    digest="$(sha256_file "$path")" || { rm -f "$temporary"; return 1; }
    printf '%s  %s\n' "$digest" "${path#"$OUT_DIR"/}" >>"$temporary" || {
      rm -f "$temporary"
      return 1
    }
  done < <(find "$OUT_DIR" -type f -print | LC_ALL=C sort)
  mv "$temporary" "$output"
}

verify_local_artifact_checksums() {
  local seal_scope="${1:-measured}"
  local manifest="$OUT_DIR/checksums.sha256" identity="$OUT_DIR/artifact-identity.tsv"
  local digest relative extra actual path expected
  [[ -s "$manifest" && -f "$manifest" && ! -L "$manifest" ]] || return 1
  while read -r digest relative extra; do
    [[ -z "${extra:-}" && "$digest" =~ ^[0-9a-f]{64}$ && -n "$relative" ]] || return 1
    [[ "$relative" != /* && "$relative" != ../* && "$relative" != */../* && "$relative" != */.. ]] || return 1
    path="$OUT_DIR/$relative"
    [[ -f "$path" && ! -L "$path" ]] || return 1
    actual="$(sha256_file "$path")" || return 1
    [[ "$actual" == "$digest" ]] || return 1
  done <"$manifest"
  for relative in \
    config/effective-wukongim.toml \
    artifact-identity.tsv; do
    awk -v expected="$relative" '$2 == expected { found = 1 } END { exit !found }' "$manifest" || return 1
  done
  [[ "$(awk -F '\t' '$1 == "seal_scope" { print $2 }' "$identity")" == "$seal_scope" ]] || return 1
  expected="$(awk -F '\t' '$1 == "original_config_sha256" { print $2 }' "$identity")"
  [[ "$expected" =~ ^[0-9a-f]{64}$ ]] || return 1
  expected="$(awk -F '\t' '$1 == "effective_config_sha256" { print $2 }' "$identity")"
  actual="$(sha256_file "$OUT_DIR/config/effective-wukongim.toml")" || return 1
  [[ "$expected" == "$actual" ]] || return 1
  if [[ "$seal_scope" == measured ]]; then
    for relative in logs/after/node1.log bin/wukongim bin/wkbench; do
      awk -v expected="$relative" '$2 == expected { found = 1 } END { exit !found }' "$manifest" || return 1
    done
    [[ "$(awk -F '\t' '$1 == "wukongim_binary" { print $2 }' "$identity")" == "$SEALED_WUKONGIM_RELATIVE" ]] || return 1
    [[ "$(awk -F '\t' '$1 == "wkbench_binary" { print $2 }' "$identity")" == "$SEALED_WKBENCH_RELATIVE" ]] || return 1
    expected="$(awk -F '\t' '$1 == "wukongim_binary_sha256" { print $2 }' "$identity")"
    actual="$(sha256_file "$OUT_DIR/$SEALED_WUKONGIM_RELATIVE")" || return 1
    [[ "$expected" == "$actual" ]] || return 1
    expected="$(awk -F '\t' '$1 == "wkbench_binary_sha256" { print $2 }' "$identity")"
    actual="$(sha256_file "$OUT_DIR/$SEALED_WKBENCH_RELATIVE")" || return 1
    [[ "$expected" == "$actual" ]] || return 1
  elif [[ "$seal_scope" != preflight ]]; then
    return 1
  fi
  while IFS= read -r path; do
    [[ "$path" == "$manifest" || "$path" == "$OUT_DIR/local-baseline.json" ]] && continue
    relative="${path#"$OUT_DIR"/}"
    awk -v expected="$relative" '$2 == expected { found = 1 } END { exit !found }' "$manifest" || return 1
  done < <(find "$OUT_DIR" -type f -print | LC_ALL=C sort)
}

FINAL_FILESYSTEM_FREE_PERCENT=0
FINAL_FILESYSTEM_OBSERVATION_COMPLETE=false

capture_final_filesystem_observation() {
  local available
  FINAL_FILESYSTEM_FREE_PERCENT=0
  FINAL_FILESYSTEM_OBSERVATION_COMPLETE=false
  if ! capture_data_filesystem_observation "$OUT_DIR/filesystem-final.txt"; then
    return 1
  fi
  available="$(awk 'NR == 2 {print $4}' "$OUT_DIR/filesystem-final.txt")"
  FINAL_FILESYSTEM_FREE_PERCENT=$((available * 100 / DATA_FILESYSTEM_TOTAL_BLOCKS))
  FINAL_FILESYSTEM_OBSERVATION_COMPLETE=true
}

write_local_baseline_result() {
  local outcome="$1" reason="$2" highest="$3" first_failing="$4"
  local source_seal_valid="$5" artifact_seal_valid="$6"
  local revision dirty free_percent filesystem_observation_complete authorizes reviewed_contract_satisfied typed_evidence_complete
  local completion_generation manifest_digest authorization_digest temporary publish_status=0
  local authorization="$OUT_DIR/reports/local-baseline-authorization.json"
  local evidence="$OUT_DIR/reports/local-baseline-evidence.json"
  revision="$(source_result_revision)"
  dirty="$(source_result_dirty)"
  [[ -f "$authorization" && ! -L "$authorization" && -f "$evidence" && ! -L "$evidence" ]] || return 6
  free_percent="$(jq -er '.observed_filesystem_free_percent | select(type == "number" and . >= 0 and . <= 100)' "$evidence" 2>/dev/null || true)"
  [[ -n "$free_percent" ]] || return 6
  filesystem_observation_complete="$(jq -er '.filesystem_observation_complete | select(type == "boolean")' "$evidence" 2>/dev/null || true)"
  [[ "$filesystem_observation_complete" == true || "$filesystem_observation_complete" == false ]] || return 6
  jq -e '
    .schema == "wukongim/chat-lifecycle-local-single-node-authorization/v1" and
    (.outcome | type == "string") and (.reason | type == "string") and
    (.exit_code | type == "number") and (.highest_clean_rate | type == "number") and
    (.first_failing_rate | type == "number") and (.steps | type == "array") and
    (.completion_generation | test("^[0-9a-f]{64}$"))
  ' "$authorization" >/dev/null 2>&1 || return 6
  outcome="$(jq -r '.outcome' "$authorization")"
  reason="$(jq -r '.reason' "$authorization")"
  highest="$(jq -r '.highest_clean_rate' "$authorization")"
  first_failing="$(jq -r '.first_failing_rate' "$authorization")"
  reviewed_contract_satisfied="$(jq -r '.reviewed_contract_satisfied == true' "$authorization")"
  authorizes="$(jq -r '.authorizes_three_node_diagnostic == true' "$authorization")"
  completion_generation="$(jq -r '.completion_generation' "$authorization")"
  typed_evidence_complete="$(jq -r '
    .seal.payload_complete == true and .seal.checksums_verified == true and (.step_closures | length) == 4
  ' "$evidence")"
  manifest_digest="$(sha256_file "$OUT_DIR/checksums.sha256")" || return 6
  authorization_digest="$(sha256_file "$authorization")" || return 6
  if [[ ! "$completion_generation" =~ ^[0-9a-f]{64}$ || ! "$manifest_digest" =~ ^[0-9a-f]{64}$ ||
    ! "$authorization_digest" =~ ^[0-9a-f]{64}$ ]]; then
    return 6
  fi
  temporary="$OUT_DIR/completion-draft.json"
  [[ ! -e "$temporary" && ! -e "$OUT_DIR/local-baseline.json" ]] || return 6
  {
    printf '{\n'
    printf '  "schema": "wukongim/chat-lifecycle-local-single-node-baseline/v1",\n'
		printf '  "completion_marker": true,\n'
    printf '  "completion_generation": "%s",\n' "$completion_generation"
		printf '  "baseline_invocation_id": "%s",\n' "$BASELINE_INVOCATION_ID"
		printf '  "artifact_manifest_sha256": "%s",\n' "$manifest_digest"
		printf '  "typed_authorization_sha256": "%s",\n' "$authorization_digest"
    printf '  "outcome": "%s",\n' "$outcome"
    printf '  "reason": "%s",\n' "$reason"
    printf '  "reviewed_contract": %s,\n' "$reviewed_contract_satisfied"
    printf '  "reviewed_contract_satisfied": %s,\n' "$reviewed_contract_satisfied"
    printf '  "reviewed_typed_lifecycle_evidence_complete": %s,\n' "$typed_evidence_complete"
    printf '  "online_connections": %s,\n' "$USERS"
    printf '  "highest_clean_rate": %s,\n' "$highest"
    printf '  "first_failing_rate": %s,\n' "$first_failing"
    printf '  "authorizes_three_node_diagnostic": %s,\n' "$authorizes"
    printf '  "qps_list": "%s",\n' "$QPS_LIST"
    printf '  "logical_slot_groups": 12,\n'
    printf '  "hash_slots": 256,\n'
    printf '  "slot_replicas": 1,\n'
    printf '  "channel_replicas": 1,\n'
    printf '  "commit_coordinator_flush_window": "200us",\n'
    printf '  "commit_coordinator_shards": 1,\n'
    printf '  "sync_commit": true,\n'
    printf '  "minimum_filesystem_free_percent": %s,\n' "$MINIMUM_FREE_PERCENT"
    printf '  "filesystem_observation_complete": %s,\n' "$filesystem_observation_complete"
    printf '  "observed_filesystem_free_percent": %s,\n' "$free_percent"
    printf '  "canonical_data_dir": %s,\n' "$(jq -n --arg value "$CANONICAL_SINGLE_NODE_DATA_DIR" '$value')"
    printf '  "data_filesystem_device": %s,\n' "$(jq -n --arg value "$DATA_FILESYSTEM_DEVICE" '$value')"
    printf '  "data_filesystem_total_blocks": %s,\n' "$DATA_FILESYSTEM_TOTAL_BLOCKS"
    printf '  "data_filesystem_block_size": %s,\n' "$DATA_FILESYSTEM_BLOCK_SIZE"
    printf '  "source_revision": "%s",\n' "$revision"
    printf '  "source_dirty": %s,\n' "$dirty"
    printf '  "source_seal_valid": %s,\n' "$source_seal_valid"
    printf '  "artifact_seal_valid": %s,\n' "$artifact_seal_valid"
    printf '  "artifact_identity": "artifact-identity.tsv",\n'
    printf '  "typed_evidence": "reports/local-baseline-evidence.json",\n'
    printf '  "typed_authorization": "reports/local-baseline-authorization.json",\n'
    printf '  "effective_config": "config/effective-wukongim.toml",\n'
    printf '  "summary": "summary.tsv",\n'
    printf '  "storage_summary": "storage_metrics_summary.tsv",\n'
    printf '  "host_io_summary": "host_io_summary.tsv",\n'
    printf '  "artifact_checksums": "checksums.sha256"\n'
    printf '}\n'
  } >"$temporary"
  "$WK_BENCH_BIN" report local-single-node-publish \
    --root "$OUT_DIR" --draft "$temporary" --output "$OUT_DIR/local-baseline.json" >/dev/null 2>&1 || publish_status=$?
  rm -f "$temporary"
  return "$publish_status"
}

write_display_summary() {
  local p99_limit
  p99_limit="$(duration_seconds "$STABLE_P99")"
  # Write archival summary.txt without ANSI escapes
  (
    C_RESET='' C_BOLD='' C_DIM='' C_GREEN='' C_RED='' C_YELLOW='' C_CYAN='' C_MAGENTA='' C_WHITE=''
    awk -v rpc_file="$OUT_DIR/rpc_pull_qps.tsv" -v p99_limit="$p99_limit" -v actual_min_ratio="$ACTUAL_QPS_MIN_RATIO" -v users="$USERS" \
      -v c_bold="" -v c_reset="" -v c_green="" \
      -v c_red="" -v c_dim="" -v c_yellow="" '
    BEGIN {
      FS = "\t"
      while ((getline line < rpc_file) > 0) {
        split(line, parts, "\t")
        if (parts[1] == "tag") {
          continue
        }
        rpc_qps[parts[1]] += parts[4] + 0
      }
      close(rpc_file)

      printf "%sBENCH RESULT%s\n", c_bold, c_reset
      print "────────────"
      printf "p99 diagnostic threshold: %.0f ms │ send_errors: 0\n", p99_limit * 1000
      printf "actual/offered gate: >= %.2f\n\n", actual_min_ratio
      printf "%s%9s %10s %7s %8s %8s %8s %8s %8s %12s %s%s\n", c_dim, "offered", "actual", "ratio", "result", "errors", "p99ms", "p95ms", "maxms", "rpc_pull/s", "note", c_reset
    }
    NR == 1 {
      next
    }
    {
      tag = $1
      offered = $2 + 0
      status = $3
      exit_status = $4 + 0
      actual = $5 + 0
      actual_ratio = 0
      if (offered > 0) {
        actual_ratio = actual / offered
      }
      success = $6 + 0
      errors = $7 + 0
      p95 = $11 + 0
      p99 = $12 + 0
      max = $13 + 0
      connected = $14 + 0
      planned = $15 + 0
      dispatched = $16 + 0
      dropped = $17 + 0
      note = "ok"
      result = "PASS"
      if (status != "passed") {
        result = "FAIL"
        note = status
      }
      if (exit_status != 0) {
        result = "FAIL"
        note = "exit=" exit_status
      }
      if (errors > 0) {
        result = "FAIL"
        note = "send_errors"
      }
      if (actual_ratio < actual_min_ratio) {
        result = "FAIL"
        note = sprintf("actual_ratio=%.3f", actual_ratio)
      }
      if (connected < users) { result = "FAIL"; note = "online_connections" }
      if (planned <= 0 || dispatched != planned || success + errors != dispatched || dropped > 0) {
        result = "FAIL"
        note = "scheduler_accounting"
      }
      if (result == "PASS" && actual > best_actual) {
        best_actual = actual
        best_offered = offered
        best_p99 = p99
        best_rpc = rpc_qps[tag]
      }
      if (result == "PASS") {
        result_str = c_green "    PASS" c_reset
      } else {
        result_str = c_red "    FAIL" c_reset
      }
      printf "%9.0f %10.1f %7.3f %s %8.0f %8.1f %8.1f %8.1f %12.1f %s%s%s\n", offered, actual, actual_ratio, result_str, errors, p99 * 1000, p95 * 1000, max * 1000, rpc_qps[tag], c_dim, note, c_reset
    }
    END {
      print ""
      if (best_actual > 0) {
        printf "%s★ best pass:%s offered=%.0f actual=%.1f qps p99=%.1fms rpc_pull/s=%.1f\n", c_green, c_reset, best_offered, best_actual, best_p99 * 1000, best_rpc
      } else {
        printf "%s✗ best pass: none%s\n", c_yellow, c_reset
      }
    }
  ' "$OUT_DIR/summary.tsv" >"$OUT_DIR/summary.txt"
    append_server_resource_peak_display "$OUT_DIR/resources/server-process-summary.tsv" >>"$OUT_DIR/summary.txt"
    append_cluster_transport_peak_display "$OUT_DIR/cluster_transport_peak_summary.tsv" >>"$OUT_DIR/summary.txt"
    append_ants_pool_usage_display "$OUT_DIR/ants_pool_usage_summary.tsv" >>"$OUT_DIR/summary.txt"
  )
}

append_server_resource_peak_display() {
  local file="$1"
  printf '\n%sSERVER PROCESS PEAKS%s\n' "$C_BOLD" "$C_RESET"
  printf '%s\n' '────────────────────'
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'none\n'
    return
  fi
  awk -F'\t' -v c_bold="$C_BOLD" -v c_reset="$C_RESET" -v c_dim="$C_DIM" '
    NR == 1 { next }
    {
      entries++
      node[entries] = $1
      pid[entries] = $2
      samples[entries] = $3 + 0
      avg_cpu[entries] = $4 + 0
      max_cpu[entries] = $5 + 0
      avg_mem[entries] = $6 + 0
      max_mem[entries] = $7 + 0
      max_rss_kb[entries] = $8 + 0
      max_goroutines[entries] = $10 + 0
      if (entries == 1 || max_cpu[entries] > peak_cpu) {
        peak_cpu = max_cpu[entries]
        peak_cpu_node = $1
      }
      if (entries == 1 || max_mem[entries] > peak_mem) {
        peak_mem = max_mem[entries]
        peak_mem_node = $1
      }
      if (entries == 1 || max_rss_kb[entries] > peak_rss_kb) {
        peak_rss_kb = max_rss_kb[entries]
        peak_rss_node = $1
      }
      if (max_goroutines[entries] > 0 && (peak_goroutines == 0 || max_goroutines[entries] > peak_goroutines)) {
        peak_goroutines = max_goroutines[entries]
        peak_goroutines_node = $1
      }
    }
    END {
      if (entries == 0) {
        print "none"
        exit
      }
      printf "peak_cpu=%s %.3f%% peak_rss=%s %.3fMiB peak_mem=%s %.3f%% peak_goroutines=%s %.0f %sdetails=resources/server-process-summary.tsv%s\n",
        peak_cpu_node, peak_cpu, peak_rss_node, peak_rss_kb / 1024, peak_mem_node, peak_mem, peak_goroutines_node, peak_goroutines, c_dim, c_reset
      printf "%s%-8s %7s %8s %9s %9s %12s %9s %14s%s\n", c_dim, "node", "samples", "pid", "avg_cpu%", "max_cpu%", "max_rssMiB", "max_mem%", "max_goroutines", c_reset
      for (i = 1; i <= entries; i++) {
        printf "%-8s %7.0f %8s %9.3f %9.3f %12.3f %9.3f %14.0f\n",
          node[i], samples[i], pid[i], avg_cpu[i], max_cpu[i], max_rss_kb[i] / 1024, max_mem[i], max_goroutines[i]
      }
    }
  ' "$file"
}

append_ants_pool_usage_display() {
  local file="$1"
  printf '\n%sANTS POOL USAGE%s\n' "$C_BOLD" "$C_RESET"
  printf '%s\n' '───────────────'
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'none\n'
    return
  fi
  awk -F'\t' -v c_bold="$C_BOLD" -v c_reset="$C_RESET" -v c_dim="$C_DIM" \
    -v c_yellow="$C_YELLOW" -v c_green="$C_GREEN" '
    function remember_node(node) {
      if (!(node in seen_node)) {
        seen_node[node] = 1
        node_order[++node_count] = node
      }
    }
    function display_component(component) {
      if (component == "channelv2") {
        return "channel"
      }
      if (component == "transportv2") {
        return "transport"
      }
      return component
    }
    function display_pool(component, pool) {
      return display_component(component) "/" pool
    }
    NR == 1 { next }
    {
      entries++
      node = $2
      remember_node(node)
      pool = display_pool($3, $4)
      running = $5 + 0
      capacity = $6 + 0
      waiting = $7 + 0
      util = $8 + 0

      row_node[entries] = node
      row_pool[entries] = pool
      row_running[entries] = running
      row_capacity[entries] = capacity
      row_waiting[entries] = waiting
      row_util[entries] = util

      pool_key = node "\034" pool
      if (!(pool_key in seen_pool)) {
        seen_pool[pool_key] = 1
        pools_by_node[node]++
      }
      if (!(node in has_max) || util > max_util[node]) {
        has_max[node] = 1
        max_util[node] = util
        max_pool[node] = pool
        max_running[node] = running
        max_capacity[node] = capacity
        max_waiting[node] = waiting
      }
    }
    END {
      if (entries == 0) {
        print "none"
        exit
      }
      printf "%sdetails=ants_pool_usage_summary.tsv%s\n", c_dim, c_reset
      for (n = 1; n <= node_count; n++) {
        node = node_order[n]
        util_color = (max_util[node] >= 0.8) ? c_yellow : c_green
        printf "\n%snode=%s%s pools=%.0f max_util=%s%.3f%s pool=%s used/cap=%.0f/%.0f waiting=%.0f\n",
          c_bold, node, c_reset, pools_by_node[node],
          util_color, max_util[node], c_reset,
          max_pool[node], max_running[node], max_capacity[node], max_waiting[node]
        printf "  %s%-28s %12s %10s %8s%s\n", c_dim, "pool", "used/cap", "util", "waiting", c_reset
        for (i = 1; i <= entries; i++) {
          if (row_node[i] != node) {
            continue
          }
          util_color = (row_util[i] >= 0.8) ? c_yellow : ""
          printf "  %-28s %12s %s%10.3f%s %8.0f\n",
            row_pool[i],
            sprintf("%.0f/%.0f", row_running[i], row_capacity[i]),
            util_color, row_util[i], (util_color != "") ? c_reset : "",
            row_waiting[i]
        }
      }
    }
  ' "$file"
}

append_runtime_pool_pressure_display() {
  local file="$1"
  printf '\nRUNTIME POOL PRESSURE\n'
  printf '%s\n' '---------------------'
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'none\n'
    return
  fi
  awk -F'\t' '
    function remember_node(node) {
      if (!(node in seen_node)) {
        seen_node[node] = 1
        node_order[++node_count] = node
      }
    }
    NR == 1 { next }
    {
      node = $2
      remember_node(node)
      fill = $9 + 0
      inflight_util = $15 + 0
      full = $16 + 0
      busy = $17 + 0
      dirty = $18 + 0
      requeued = $19 + 0
      reason = $20
      pool = $3 "/" $4 "/" $5

      pressure_pools[node]++
      pool_key = node "\034" $3 "\034" $4 "\034" $5 "\034" $6
      if ((fill >= 0.9 || inflight_util >= 0.9) && !(pool_key in over90_seen)) {
        over90_seen[pool_key] = 1
        hot_pools[node]++
      }
      full_sum[node] += full
      busy_sum[node] += busy
      dirty_sum[node] += dirty
      requeued_sum[node] += requeued
      if (fill > max_fill[node]) {
        max_fill[node] = fill
      }
      if (inflight_util > max_inflight_util[node]) {
        max_inflight_util[node] = inflight_util
      }
      score = fill + inflight_util
      if (reason != "") {
        score += 1
      }
      if ((full + busy + dirty + requeued) > 0) {
        score += 1
      }
      if (score > worst_score[node]) {
        worst_score[node] = score
        worst_pool[node] = pool
        worst_reason[node] = reason
      }
      if (score > global_worst_score) {
        global_worst_score = score
        global_worst_node = node
        global_worst_pool = pool
        global_worst_reason = reason
      }
    }
    END {
      if (node_count == 0) {
        print "none"
        exit
      }
      printf "worst_node=%s worst_pool=%s reason=%s details=runtime_pool_pressure_summary.tsv\n",
        global_worst_node, global_worst_pool, global_worst_reason
      printf "%-16s %14s %9s %10s %12s %7s %7s %7s %8s %-28s %s\n",
        "node", "pressure_pools", "hot_pools", "max_qfill", "max_inflight", "full", "busy", "dirty", "requeue", "worst_pool", "reason"
      for (i = 1; i <= node_count; i++) {
        node = node_order[i]
        printf "%-16s %14.0f %9.0f %10.3f %12.3f %7.0f %7.0f %7.0f %8.0f %-28s %s\n",
          node, pressure_pools[node], hot_pools[node], max_fill[node], max_inflight_util[node],
          full_sum[node], busy_sum[node], dirty_sum[node], requeued_sum[node], worst_pool[node], worst_reason[node]
      }
    }
  ' "$file"
}

append_channelappend_pool_pressure_display() {
  local file="$1"
  printf '\nCHANNELWRITE POOL PRESSURE\n'
  printf '%s\n' '--------------------------'
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'none\n'
    return
  fi
  awk -F'\t' '
    function metric_value(name, col) {
      col = idx[name]
      if (col <= 0) {
        return 0
      }
      return $col + 0
    }
    function remember_node(node) {
      if (!(node in seen_node)) {
        seen_node[node] = 1
        node_order[++node_count] = node
      }
    }
    function add_reason(reason, item) {
      if (reason == "") {
        return item
      }
      return reason "," item
    }
    NR == 1 {
      for (i = 1; i <= NF; i++) {
        idx[$i] = i
      }
      next
    }
    {
      node = $2
      remember_node(node)
      router_total[node] = metric_value("router_total_delta")
      route_block[node] = metric_value("router_backpressured_delta") + metric_value("router_channel_busy_delta") + metric_value("router_route_not_ready_delta") + metric_value("router_timeout_delta")
      router_errors[node] = metric_value("router_error_delta")
      local_reject[node] = metric_value("local_admission_rejected_delta")
      router_avg_ms[node] = metric_value("router_avg_ms")
      mailbox_fill[node] = metric_value("mailbox_fill_max")
      pending_append[node] = metric_value("pending_append_max")
      post_backlog[node] = metric_value("post_commit_backlog_max")
      effect_avg_ms[node] = metric_value("effect_avg_ms")
      effect_util[node] = metric_value("effect_pool_util_max")
      pool_full[node] = metric_value("effect_pool_full_delta")
      pool_error[node] = metric_value("effect_pool_error_delta")
      saturated[node] = metric_value("effect_pool_saturated_max")
      over90[node] = metric_value("effect_pool_over90_count")

      reason = ""
      if (router_errors[node] > 0) reason = add_reason(reason, "router_error")
      if (route_block[node] > 0) reason = add_reason(reason, "route_block")
      if (local_reject[node] > 0) reason = add_reason(reason, "local_reject")
      if (mailbox_fill[node] >= 0.5) reason = add_reason(reason, "mailbox_fill")
      if (pending_append[node] > 0) reason = add_reason(reason, "pending_append")
      if (post_backlog[node] > 0) reason = add_reason(reason, "post_commit_backlog")
      if (pool_full[node] > 0) reason = add_reason(reason, "effect_pool_full")
      if (pool_error[node] > 0) reason = add_reason(reason, "effect_pool_error")
      if (effect_util[node] >= 0.9 || saturated[node] > 0 || over90[node] > 0) reason = add_reason(reason, "effect_pool_hot")
      if (reason == "") reason = "ok"
      reason_by_node[node] = reason

      score = mailbox_fill[node] + effect_util[node]
      if (router_errors[node] > 0) score += 1
      if (route_block[node] > 0) score += 1
      if (local_reject[node] > 0) score += 1
      if (pending_append[node] > 0 || post_backlog[node] > 0) score += 1
      if (pool_full[node] > 0 || pool_error[node] > 0 || saturated[node] > 0 || over90[node] > 0) score += 1
      if (score > worst_score) {
        worst_score = score
        worst_node = node
        worst_reason = reason
      }
    }
    END {
      if (node_count == 0) {
        print "none"
        exit
      }
      printf "worst_node=%s reason=%s details=channelappend_metrics_summary.tsv\n", worst_node, worst_reason
      printf "%-16s %8s %11s %9s %9s %10s %8s %12s %9s %11s %9s %8s %9s %s\n",
        "node", "router", "route_block", "local_rej", "router_ms", "mailbox", "pending", "post_backlog", "effect_ms", "effect_util", "pool_full", "pool_err", "saturated", "reason"
      for (i = 1; i <= node_count; i++) {
        node = node_order[i]
        printf "%-16s %8.0f %11.0f %9.0f %9.3f %10.3f %8.0f %12.0f %9.3f %11.3f %9.0f %8.0f %9.0f %s\n",
          node, router_total[node], route_block[node], local_reject[node], router_avg_ms[node], mailbox_fill[node],
          pending_append[node], post_backlog[node], effect_avg_ms[node], effect_util[node],
          pool_full[node], pool_error[node], saturated[node], reason_by_node[node]
      }
    }
  ' "$file"
}

append_cluster_transport_peak_display() {
  local file="$1"
  printf '\n%sCLUSTER INTERNAL TRANSPORT PEAK%s\n' "$C_BOLD" "$C_RESET"
  printf '%s\n' '───────────────────────────────'
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'none\n'
    return
  fi
  awk -F'\t' -v c_bold="$C_BOLD" -v c_reset="$C_RESET" -v c_dim="$C_DIM" '
    NR == 1 { next }
    {
      entries++
      node = $2
      sample_pairs = $4 + 0
      peak = $5 + 0
      node_order[entries] = node
      sample_pairs_by_node[node] = sample_pairs
      peak_internal_by_node[node] = peak
      peak_out_by_node[node] = $6 + 0
      peak_in_by_node[node] = $7 + 0
      peak_duplex_by_node[node] = $8 + 0
      peak_interval_by_node[node] = $9 "-" $10
      if (entries == 1 || peak > peak_internal) {
        peak_internal = peak
        peak_node = node
        peak_duplex = $8 + 0
        peak_interval = $9 "-" $10
      }
    }
    END {
      if (entries == 0) {
        print "none"
        exit
      }
      printf "peak_node=%s peak_internal_mib_s=%.3f peak_duplex_mib_s=%.3f interval=%s %sdetails=cluster_transport_peak_summary.tsv%s\n",
        peak_node, peak_internal, peak_duplex, peak_interval, c_dim, c_reset
      printf "%s%-16s %12s %10s %10s %12s %9s %s%s\n", c_dim,
        "node", "peak_mib/s", "out_mib/s", "in_mib/s", "duplex_mib/s", "samples", "interval", c_reset
      for (i = 1; i <= entries; i++) {
        node = node_order[i]
        printf "%-16s %12.3f %10.3f %10.3f %12.3f %9.0f %s\n",
          node, peak_internal_by_node[node], peak_out_by_node[node], peak_in_by_node[node],
          peak_duplex_by_node[node], sample_pairs_by_node[node], peak_interval_by_node[node]
      }
    }
  ' "$file"
}

server_resource_peak_markdown() {
  local file="$1"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf '%s\n' '- none'
    return
  fi
  awk -F'\t' '
    NR == 1 { next }
    {
      entries++
      cpu = $5 + 0
      mem = $7 + 0
      rss = $8 + 0
      goroutines = $10 + 0
      if (entries == 1 || cpu > peak_cpu) {
        peak_cpu = cpu
        peak_cpu_node = $1
      }
      if (entries == 1 || mem > peak_mem) {
        peak_mem = mem
        peak_mem_node = $1
      }
      if (entries == 1 || rss > peak_rss) {
        peak_rss = rss
        peak_rss_node = $1
      }
      if (goroutines > 0 && (peak_goroutines == 0 || goroutines > peak_goroutines)) {
        peak_goroutines = goroutines
        peak_goroutines_node = $1
      }
    }
    END {
      if (entries == 0) {
        print "- none"
        exit
      }
      printf "- peak_cpu: %s %.3f%%\n", peak_cpu_node, peak_cpu
      printf "- peak_rss: %s %.3fMiB\n", peak_rss_node, peak_rss / 1024
      printf "- peak_mem: %s %.3f%%\n", peak_mem_node, peak_mem
      if (peak_goroutines > 0) {
        printf "- peak_goroutines: %s %.0f\n", peak_goroutines_node, peak_goroutines
      }
      printf "- details: resources/server-process-summary.tsv\n"
    }
  ' "$file"
}

ants_pool_usage_markdown() {
  local file="$1"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf '%s\n' '- none'
    return
  fi
  awk -F'\t' '
    function remember_node(node) {
      if (!(node in seen_node)) {
        seen_node[node] = 1
        node_order[++node_count] = node
      }
    }
    function display_component(component) {
      if (component == "channelv2") {
        return "channel"
      }
      if (component == "transportv2") {
        return "transport"
      }
      return component
    }
    function display_pool(component, pool) {
      return display_component(component) "/" pool
    }
    NR == 1 { next }
    {
      entries++
      node = $2
      remember_node(node)
      pool = display_pool($3, $4)
      running = $5 + 0
      capacity = $6 + 0
      waiting = $7 + 0
      util = $8 + 0

      row_node[entries] = node
      row_pool[entries] = pool
      row_running[entries] = running
      row_capacity[entries] = capacity
      row_waiting[entries] = waiting
      row_util[entries] = util

      pool_key = node "\034" pool
      if (!(pool_key in seen_pool)) {
        seen_pool[pool_key] = 1
        pools_by_node[node]++
      }
      if (!(node in has_max) || util > max_util[node]) {
        has_max[node] = 1
        max_util[node] = util
        max_pool[node] = pool
        max_running[node] = running
        max_capacity[node] = capacity
        max_waiting[node] = waiting
      }
    }
    END {
      if (entries == 0) {
        print "- none"
        exit
      }
      print "- details=ants_pool_usage_summary.tsv"
      for (n = 1; n <= node_count; n++) {
        node = node_order[n]
        printf "- node=%s pools=%.0f max_util=%.3f pool=%s used/cap=%.0f/%.0f waiting=%.0f\n",
          node, pools_by_node[node], max_util[node], max_pool[node],
          max_running[node], max_capacity[node], max_waiting[node]
        for (i = 1; i <= entries; i++) {
          if (row_node[i] != node) {
            continue
          }
          printf "- node=%s pool=%s used/cap=%s util=%.3f waiting=%.0f\n",
            row_node[i], row_pool[i],
            sprintf("%.0f/%.0f", row_running[i], row_capacity[i]), row_util[i],
            row_waiting[i]
        }
      }
    }
  ' "$file"
}

result_display_markdown() {
  local file="$1"
  if [[ ! -f "$file" ]]; then
    printf '%s\n' '- none'
    return
  fi
  awk '
    /^SERVER PROCESS PEAKS$/ || /^CLUSTER INTERNAL TRANSPORT PEAK$/ || /^ANTS POOL USAGE$/ || /^# ants pool usage$/ { exit }
    { print }
  ' "$file"
}

runtime_pool_pressure_markdown() {
  local file="$1"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf '%s\n' '- none'
    return
  fi
  awk -F'\t' '
    function remember_node(node) {
      if (!(node in seen_node)) {
        seen_node[node] = 1
        node_order[++node_count] = node
      }
    }
    NR == 1 { next }
    {
      node = $2
      remember_node(node)
      fill = $9 + 0
      inflight_util = $15 + 0
      full = $16 + 0
      busy = $17 + 0
      dirty = $18 + 0
      requeued = $19 + 0
      reason = $20
      pool = $3 "/" $4 "/" $5
      pressure_pools[node]++
      pool_key = node "\034" $3 "\034" $4 "\034" $5 "\034" $6
      if ((fill >= 0.9 || inflight_util >= 0.9) && !(pool_key in over90_seen)) {
        over90_seen[pool_key] = 1
        hot_pools[node]++
      }
      full_sum[node] += full
      busy_sum[node] += busy
      dirty_sum[node] += dirty
      requeued_sum[node] += requeued
      if (fill > max_fill[node]) {
        max_fill[node] = fill
      }
      if (inflight_util > max_inflight_util[node]) {
        max_inflight_util[node] = inflight_util
      }
      score = fill + inflight_util
      if (reason != "") {
        score += 1
      }
      if ((full + busy + dirty + requeued) > 0) {
        score += 1
      }
      if (score > worst_score[node]) {
        worst_score[node] = score
        worst_pool[node] = pool
        worst_reason[node] = reason
      }
      if (score > global_worst_score) {
        global_worst_score = score
        global_worst_node = node
        global_worst_pool = pool
        global_worst_reason = reason
      }
    }
    END {
      if (node_count == 0) {
        print "- none"
        exit
      }
      printf "- worst_node=%s worst_pool=%s reason=%s details=runtime_pool_pressure_summary.tsv\n",
        global_worst_node, global_worst_pool, global_worst_reason
      for (i = 1; i <= node_count; i++) {
        node = node_order[i]
        printf "- node=%s pressure_pools=%.0f hot_pools=%.0f max_qfill=%.3f max_inflight=%.3f full=%.0f busy=%.0f dirty=%.0f requeue=%.0f worst_pool=%s reason=%s\n",
          node, pressure_pools[node], hot_pools[node], max_fill[node], max_inflight_util[node],
          full_sum[node], busy_sum[node], dirty_sum[node], requeued_sum[node], worst_pool[node], worst_reason[node]
      }
    }
  ' "$file"
}

channelappend_pool_pressure_markdown() {
  local file="$1"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf '%s\n' '- none'
    return
  fi
  awk -F'\t' '
    function metric_value(name, col) {
      col = idx[name]
      if (col <= 0) {
        return 0
      }
      return $col + 0
    }
    function remember_node(node) {
      if (!(node in seen_node)) {
        seen_node[node] = 1
        node_order[++node_count] = node
      }
    }
    function add_reason(reason, item) {
      if (reason == "") {
        return item
      }
      return reason "," item
    }
    NR == 1 {
      for (i = 1; i <= NF; i++) {
        idx[$i] = i
      }
      next
    }
    {
      node = $2
      remember_node(node)
      router_total[node] = metric_value("router_total_delta")
      route_block[node] = metric_value("router_backpressured_delta") + metric_value("router_channel_busy_delta") + metric_value("router_route_not_ready_delta") + metric_value("router_timeout_delta")
      router_errors[node] = metric_value("router_error_delta")
      local_reject[node] = metric_value("local_admission_rejected_delta")
      mailbox_fill[node] = metric_value("mailbox_fill_max")
      pending_append[node] = metric_value("pending_append_max")
      post_backlog[node] = metric_value("post_commit_backlog_max")
      effect_util[node] = metric_value("effect_pool_util_max")
      pool_full[node] = metric_value("effect_pool_full_delta")
      pool_error[node] = metric_value("effect_pool_error_delta")
      saturated[node] = metric_value("effect_pool_saturated_max")
      over90[node] = metric_value("effect_pool_over90_count")

      reason = ""
      if (router_errors[node] > 0) reason = add_reason(reason, "router_error")
      if (route_block[node] > 0) reason = add_reason(reason, "route_block")
      if (local_reject[node] > 0) reason = add_reason(reason, "local_reject")
      if (mailbox_fill[node] >= 0.5) reason = add_reason(reason, "mailbox_fill")
      if (pending_append[node] > 0) reason = add_reason(reason, "pending_append")
      if (post_backlog[node] > 0) reason = add_reason(reason, "post_commit_backlog")
      if (pool_full[node] > 0) reason = add_reason(reason, "effect_pool_full")
      if (pool_error[node] > 0) reason = add_reason(reason, "effect_pool_error")
      if (effect_util[node] >= 0.9 || saturated[node] > 0 || over90[node] > 0) reason = add_reason(reason, "effect_pool_hot")
      if (reason == "") reason = "ok"
      reason_by_node[node] = reason

      score = mailbox_fill[node] + effect_util[node]
      if (router_errors[node] > 0) score += 1
      if (route_block[node] > 0) score += 1
      if (local_reject[node] > 0) score += 1
      if (pending_append[node] > 0 || post_backlog[node] > 0) score += 1
      if (pool_full[node] > 0 || pool_error[node] > 0 || saturated[node] > 0 || over90[node] > 0) score += 1
      if (score > worst_score) {
        worst_score = score
        worst_node = node
        worst_reason = reason
      }
    }
    END {
      if (node_count == 0) {
        print "- none"
        exit
      }
      printf "- worst_node=%s reason=%s details=channelappend_metrics_summary.tsv\n", worst_node, worst_reason
      for (i = 1; i <= node_count; i++) {
        node = node_order[i]
        printf "- node=%s router=%.0f route_block=%.0f local_rej=%.0f mailbox=%.3f pending=%.0f post_backlog=%.0f effect_util=%.3f pool_full=%.0f pool_err=%.0f saturated=%.0f reason=%s\n",
          node, router_total[node], route_block[node], local_reject[node], mailbox_fill[node],
          pending_append[node], post_backlog[node], effect_util[node], pool_full[node],
          pool_error[node], saturated[node], reason_by_node[node]
      }
    }
  ' "$file"
}

cluster_transport_peak_markdown() {
  local file="$1"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf '%s\n' '- none'
    return
  fi
  awk -F'\t' '
    NR == 1 { next }
    {
      entries++
      node = $2
      sample_pairs = $4 + 0
      peak = $5 + 0
      node_order[entries] = node
      sample_pairs_by_node[node] = sample_pairs
      peak_internal_by_node[node] = peak
      peak_out_by_node[node] = $6 + 0
      peak_in_by_node[node] = $7 + 0
      peak_duplex_by_node[node] = $8 + 0
      peak_interval_by_node[node] = $9 "-" $10
      if (entries == 1 || peak > peak_internal) {
        peak_internal = peak
        peak_node = node
        peak_duplex = $8 + 0
        peak_interval = $9 "-" $10
      }
    }
    END {
      if (entries == 0) {
        print "- none"
        exit
      }
      printf "- peak_node=%s peak_internal_mib_s=%.3f peak_duplex_mib_s=%.3f interval=%s details=cluster_transport_peak_summary.tsv\n",
        peak_node, peak_internal, peak_duplex, peak_interval
      for (i = 1; i <= entries; i++) {
        node = node_order[i]
        printf "- node=%s peak_internal_mib_s=%.3f out_mib_s=%.3f in_mib_s=%.3f duplex_mib_s=%.3f samples=%.0f interval=%s\n",
          node, peak_internal_by_node[node], peak_out_by_node[node], peak_in_by_node[node],
          peak_duplex_by_node[node], sample_pairs_by_node[node], peak_interval_by_node[node]
      }
    }
  ' "$file"
}

print_summary() {
  write_display_summary
  write_evidence_summary
  # Live terminal output uses colorized functions
  local p99_limit
  p99_limit="$(duration_seconds "$STABLE_P99")"
    awk -v rpc_file="$OUT_DIR/rpc_pull_qps.tsv" -v p99_limit="$p99_limit" -v actual_min_ratio="$ACTUAL_QPS_MIN_RATIO" -v users="$USERS" \
    -v c_bold="$C_BOLD" -v c_reset="$C_RESET" -v c_green="$C_GREEN" \
    -v c_red="$C_RED" -v c_dim="$C_DIM" -v c_yellow="$C_YELLOW" '
    BEGIN {
      FS = "\t"
      while ((getline line < rpc_file) > 0) {
        split(line, parts, "\t")
        if (parts[1] == "tag") { continue }
        rpc_qps[parts[1]] += parts[4] + 0
      }
      close(rpc_file)
      printf "%sBENCH RESULT%s\n", c_bold, c_reset
      print "────────────"
      printf "p99 diagnostic threshold: %.0f ms │ send_errors: 0\n", p99_limit * 1000
      printf "actual/offered gate: >= %.2f\n\n", actual_min_ratio
      printf "%s%9s %10s %7s %8s %8s %8s %8s %8s %12s %s%s\n", c_dim, "offered", "actual", "ratio", "result", "errors", "p99ms", "p95ms", "maxms", "rpc_pull/s", "note", c_reset
    }
    NR == 1 { next }
    {
      tag = $1; offered = $2 + 0; status = $3; exit_status = $4 + 0
      actual = $5 + 0; success = $6 + 0; errors = $7 + 0; p95 = $11 + 0; p99 = $12 + 0; max = $13 + 0
      connected = $14 + 0; planned = $15 + 0; dispatched = $16 + 0; dropped = $17 + 0
      actual_ratio = (offered > 0) ? actual / offered : 0
      note = "ok"; result = "PASS"
      if (status != "passed") { result = "FAIL"; note = status }
      if (exit_status != 0) { result = "FAIL"; note = "exit=" exit_status }
      if (errors > 0) { result = "FAIL"; note = "send_errors" }
      if (actual_ratio < actual_min_ratio) { result = "FAIL"; note = sprintf("actual_ratio=%.3f", actual_ratio) }
      if (connected < users) { result = "FAIL"; note = "online_connections" }
      if (planned <= 0 || dispatched != planned || success + errors != dispatched || dropped > 0) { result = "FAIL"; note = "scheduler_accounting" }
      if (result == "PASS" && actual > best_actual) { best_actual = actual; best_offered = offered; best_p99 = p99; best_rpc = rpc_qps[tag] }
      result_str = (result == "PASS") ? c_green "    PASS" c_reset : c_red "    FAIL" c_reset
      printf "%9.0f %10.1f %7.3f %s %8.0f %8.1f %8.1f %8.1f %12.1f %s%s%s\n", offered, actual, actual_ratio, result_str, errors, p99 * 1000, p95 * 1000, max * 1000, rpc_qps[tag], c_dim, note, c_reset
    }
    END {
      print ""
      if (best_actual > 0) { printf "%s★ best pass:%s offered=%.0f actual=%.1f qps p99=%.1fms rpc_pull/s=%.1f\n", c_green, c_reset, best_offered, best_actual, best_p99 * 1000, best_rpc }
      else { printf "%s✗ best pass: none%s\n", c_yellow, c_reset }
    }
  ' "$OUT_DIR/summary.tsv"
  append_server_resource_peak_display "$OUT_DIR/resources/server-process-summary.tsv"
  append_cluster_transport_peak_display "$OUT_DIR/cluster_transport_peak_summary.tsv"
  append_ants_pool_usage_display "$OUT_DIR/ants_pool_usage_summary.tsv"
  log "evidence:"
  printf '  %s%-23s%s %s\n' "$C_DIM" "summary" "$C_RESET" "summary.tsv"
  printf '  %s%-23s%s %s\n' "$C_DIM" "summary_md" "$C_RESET" "summary.md"
  printf '  %s%-23s%s %s\n' "$C_DIM" "server_process" "$C_RESET" "resources/server-process-summary.tsv"
  printf '  %s%-23s%s %s\n' "$C_DIM" "cluster_transport" "$C_RESET" "cluster_transport_peak_summary.tsv"
  printf '  %s%-23s%s %s\n' "$C_DIM" "ants_pool_usage" "$C_RESET" "ants_pool_usage_summary.tsv"
  printf '  %s%-23s%s %s\n' "$C_DIM" "storage_metrics" "$C_RESET" "storage_metrics_summary.tsv"
  printf '  %s%-23s%s %s\n' "$C_DIM" "host_io" "$C_RESET" "host_io_summary.tsv"
}

write_evidence_summary() {
  local ants_pool_usage
  local result_summary
  local server_resource_peaks
  local cluster_transport_peak
  result_summary="$(result_display_markdown "$OUT_DIR/summary.txt")"
  server_resource_peaks="$(server_resource_peak_markdown "$OUT_DIR/resources/server-process-summary.tsv")"
  cluster_transport_peak="$(cluster_transport_peak_markdown "$OUT_DIR/cluster_transport_peak_summary.tsv")"
  ants_pool_usage="$(ants_pool_usage_markdown "$OUT_DIR/ants_pool_usage_summary.tsv")"
  cat >"$OUT_DIR/summary.md" <<EOF
# Single-Node Bench Evidence

## Scenario
- workload: local wukongim single-node cluster wkbench group channels
- channels: $CHANNELS
- users: $USERS
- group_members: $GROUP_MEMBERS
- qps_list: $QPS_LIST
- duration: $DURATION
- clean_cluster: $CLEAN_CLUSTER

## Evidence
- summary_tsv: summary.tsv
- server_process: resources/server-process-summary.tsv
- cluster_transport: cluster_transport_peak_summary.tsv
- ants_pool_usage: ants_pool_usage_summary.tsv
- storage_metrics: storage_metrics_summary.tsv
- host_io: host_io_summary.tsv

## Result
\`\`\`text
${result_summary}
\`\`\`

## Server Process Peaks
${server_resource_peaks}

## Cluster Internal Transport Peak
${cluster_transport_peak}

## Ants Pool Usage
${ants_pool_usage}
EOF
}

main() {
  cd "$ROOT_DIR"
  validate_and_prepare_out_dir
  initialize_baseline_invocation_id
  freeze_runtime_config || die 'failed to create a stable private runtime config snapshot'
  resolve_single_node_data_dir
  capture_source_state initial || true
  ensure_wkbench_binary
  mkdir -p "$OUT_DIR/metrics" "$OUT_DIR/reports"

  local preflight_status=0
  local_baseline_preflight || preflight_status=$?
  if (( preflight_status != 0 )); then
    local finalized_preflight_status=0
    finalize_local_preflight_result "$preflight_status" || finalized_preflight_status=$?
    log "local baseline preflight result: $OUT_DIR/local-baseline.json"
    exit "$finalized_preflight_status"
  fi

  prepare_sealed_test_binaries || die "failed to prepare tested binaries under $OUT_DIR/bin"
	ensure_local_bench_api_token || die "failed to create a process-local benchmark API token"

  cat >"$OUT_DIR/summary.tsv" <<'EOF'
tag	offered_qps	status	exit_status	actual_qps	send_success	send_errors	connect_error_rate	sendack_error_rate	p50_seconds	p95_seconds	p99_seconds	max_seconds	connect_success	scheduler_planned	scheduler_dispatched	scheduler_dropped
EOF
  cat >"$OUT_DIR/rpc_pull_qps.tsv" <<'EOF'
tag	node	rpc_pull_delta	rpc_pull_qps
EOF
  cat >"$OUT_DIR/channel_metrics_summary.tsv" <<'EOF'
tag	node	active_total	active_leader	active_follower	follower_parked	mailbox_depth_max	worker_queue_depth_max	runtime_pool_queue_depth_max	runtime_pool_queue_fill_max	runtime_pool_queue_bytes_max	runtime_pool_queue_bytes_fill_max	runtime_pool_inflight_max	runtime_pool_inflight_util_max	runtime_pool_admission_full_delta	runtime_pool_admission_busy_delta	runtime_pool_admission_dirty_delta	runtime_pool_admission_requeued_delta	activation_rejected_delta	recovery_probe_submitted_delta	recovery_probe_ok_delta	recovery_probe_err_delta	pull_ok_nonempty_delta	pull_ok_empty_delta	pull_err_delta	rpc_pull_ok_delta	rpc_pull_err_delta	rpc_pull_qps	meta_cache_hit_delta	meta_cache_miss_delta	meta_cache_invalidate_delta	append_count_delta	append_avg_ms	append_batch_count_delta	append_batch_avg_records	append_batch_avg_bytes	append_batch_wait_avg_ms	worker_task_count_delta	worker_task_avg_ms	rpc_pull_batch_calls_delta	rpc_pull_batch_items_delta	rpc_pull_batch_avg_items	rpc_pull_hint_batch_calls_delta	rpc_pull_hint_batch_items_delta	rpc_pull_hint_batch_avg_items	store_append_batch_calls_delta	store_append_batch_items_delta	store_append_batch_avg_items	store_apply_batch_calls_delta	store_apply_batch_items_delta	store_apply_batch_avg_items
EOF
  cp "$OUT_DIR/channel_metrics_summary.tsv" "$OUT_DIR/channelv2_metrics_summary.tsv"
  cat >"$OUT_DIR/channelappend_metrics_summary.tsv" <<'EOF'
tag	node	router_total_delta	router_local_delta	router_remote_delta	router_error_delta	router_backpressured_delta	router_channel_busy_delta	router_route_not_ready_delta	router_timeout_delta	local_admission_total_delta	local_admission_rejected_delta	router_avg_ms	mailbox_depth_max	mailbox_capacity_max	mailbox_fill_max	effect_slots_max	effect_slots_capacity_max	pending_append_max	append_inflight_max	post_commit_backlog_max	effect_total_delta	effect_error_delta	append_effect_delta	post_commit_effect_delta	effect_avg_ms	effect_worker_inflight_max	effect_worker_capacity_max	effect_worker_util_max	effect_queue_depth_max	effect_queue_capacity_max	effect_queue_fill_max	effect_pool_submit_delta	effect_pool_full_delta	effect_pool_error_delta	effect_pool_inflight_max	effect_pool_capacity_max	effect_pool_util_max	effect_pool_saturated_max	effect_pool_over90_count
EOF
  awk -v header=1 -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" /dev/null >"$OUT_DIR/storage_metrics_summary.tsv"
  awk -v header=1 -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >"$OUT_DIR/host_io_summary.tsv"
  cat >"$OUT_DIR/runtime_pool_pressure_summary.tsv" <<'EOF'
tag	node	component	pool	queue	priority	queue_depth_max	queue_capacity	queue_fill_max	queue_bytes_max	queue_bytes_capacity	queue_bytes_fill_max	inflight_max	workers	inflight_util_max	admission_full_delta	admission_busy_delta	admission_dirty_delta	admission_requeued_delta	reason
EOF
  cat >"$OUT_DIR/ants_pool_usage_summary.tsv" <<'EOF'
tag	node	component	pool	running	capacity	waiting	utilization_max
EOF
  cat >"$OUT_DIR/cluster_transport_peak_summary.tsv" <<'EOF'
tag	node	sample_points	sample_pairs	peak_internal_mib_s	peak_out_mib_s	peak_in_mib_s	peak_duplex_mib_s	peak_from_seq	peak_to_seq
EOF

  local qps runtime_harness_started=false highest_clean_rate=0 first_failing_rate=0 final_outcome=clean final_reason=complete final_status=0
  for qps in "${QPS_VALUES[@]}"; do
    [[ "$qps" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "invalid qps value: $qps"
    start_cluster_generation "$qps"
    check_cluster_ready
    attest_cluster_generation_ready "$qps"
    if [[ "$runtime_harness_started" == false ]]; then
      capture_source_state post_build || true
      start_host_metrics
      ensure_worker
      write_target_and_workers
      write_run_metadata
      runtime_harness_started=true
    fi
    start_server_resource_sampler
    run_attempt "$qps"
    sample_server_resources after || true
    stop_server_resource_sampler
    stop_cluster_generation "$qps"
    write_immutable_step_summaries "$qps" || true
    write_local_step_checksums "$qps" || true
    verify_local_step_checksums "$qps" || true
    write_typed_local_step_evidence "$qps" || true
    if ! read_typed_local_step_result "$qps"; then
      LATEST_OUTCOME=insufficient_evidence
      LATEST_REASON=typed_step_result_unavailable
      LATEST_EXIT_STATUS=6
    fi
    if [[ "$LATEST_OUTCOME" == clean ]]; then
      highest_clean_rate="$qps"
      continue
    fi
    first_failing_rate="$qps"
    final_outcome="$LATEST_OUTCOME"
    final_reason="$LATEST_REASON"
    final_status="$LATEST_EXIT_STATUS"
    break
  done

  local artifact_writers_stopped=true binary_seal_valid=true source_seal_valid=false artifact_seal_valid=false artifact_seal_scope=measured
  stop_terminal_cut_observer || artifact_writers_stopped=false
  stop_server_resource_sampler || artifact_writers_stopped=false
  scrape_metrics_snapshot after
  stop_lifecycle_sampler || artifact_writers_stopped=false
  stop_runtime_pool_sampler || artifact_writers_stopped=false
  stop_worker_writer "artifact seal" || artifact_writers_stopped=false
  discard_owned_worker_runtime_state || artifact_writers_stopped=false
  stop_host_metrics_writer || artifact_writers_stopped=false
  stop_cluster_writer || artifact_writers_stopped=false
  discard_runtime_config_snapshot || artifact_writers_stopped=false
  collect_node_logs after
  write_server_resource_summary || true
  print_summary
  if [[ "$OWNED_CLUSTER_STARTED" -eq 1 ]]; then
    seal_local_binaries || binary_seal_valid=false
  else
    artifact_seal_scope=preflight
    binary_seal_valid=false
  fi
  capture_source_state final || true
  capture_final_filesystem_observation || true
  write_local_artifact_identity "$artifact_seal_scope"
  source_seal_valid="$(awk -F '\t' '$1 == "source_rebuildable_from_revision" { print $2 }' "$OUT_DIR/artifact-identity.tsv")"
  if [[ "$artifact_writers_stopped" == true && "$binary_seal_valid" == true ]] && local_artifact_payload_ready; then
    artifact_seal_valid=true
  fi
  write_typed_local_baseline_evidence "$final_outcome" "$artifact_seal_valid" true "$final_reason" \
    "$FINAL_FILESYSTEM_FREE_PERCENT" "$FINAL_FILESYSTEM_OBSERVATION_COMPLETE" || true
  derive_typed_local_baseline_authorization || true
  if ! write_local_artifact_checksums; then
    log 'failed to write final artifact checksum manifest'
    return 6
  fi
  if ! verify_local_artifact_checksums "$artifact_seal_scope"; then
    log 'failed to verify final artifact checksum manifest'
    return 6
  fi
  local publication_status=0 consumer_status=0
  write_local_baseline_result "$final_outcome" "$final_reason" "$highest_clean_rate" "$first_failing_rate" \
    "$source_seal_valid" "$artifact_seal_valid" || publication_status=$?
  [[ -f "$OUT_DIR/local-baseline.json" && ! -L "$OUT_DIR/local-baseline.json" ]] || return 6
  "$WK_BENCH_BIN" report local-single-node-completion \
    --root "$OUT_DIR" --marker "$OUT_DIR/local-baseline.json" >/dev/null 2>&1 || consumer_status=$?
  [[ "$consumer_status" -eq "$publication_status" ]] || return 6
  return "$consumer_status"
}

main "$@"
