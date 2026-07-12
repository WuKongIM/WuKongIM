#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_THREE_NODE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"

QPS_LIST="${WK_BENCH_THREE_NODE_QPS:-1000,2000,2400,2490,2500,2600,2800,3000}"
OUT_DIR="${WK_BENCH_THREE_NODE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-1000ch}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-test}"
WORKER_ADDR="${WK_BENCH_WORKER_ADDR:-http://127.0.0.1:19130}"
WORKER_LISTEN="${WK_BENCH_WORKER_LISTEN:-127.0.0.1:19130}"
START_WORKER=1
START_CLUSTER=1
CLEAN_CLUSTER=1
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-three-nodes.sh}"
READY_TIMEOUT="${WK_BENCH_THREE_NODE_READY_TIMEOUT:-90}"

CHANNELS="${WK_BENCH_CHANNELS:-1000}"
USERS="${WK_BENCH_USERS:-4096}"
GROUP_MEMBERS="${WK_BENCH_GROUP_MEMBERS:-10}"
CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"
PAYLOAD_BYTES="${WK_BENCH_PAYLOAD_BYTES:-128}"
DURATION="${WK_BENCH_DURATION:-15s}"
WARMUP="${WK_BENCH_WARMUP:-5s}"
COOLDOWN="${WK_BENCH_COOLDOWN:-2s}"
STABLE_P99="${WK_BENCH_STABLE_P99:-400ms}"
ACTUAL_QPS_MIN_RATIO="${WK_BENCH_ACTUAL_QPS_MIN_RATIO:-0.90}"
ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"
RECV_ACK="${WK_BENCH_RECV_ACK:-true}"
HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"
PROFILE_SECONDS="${WK_BENCH_PROFILE_SECONDS:-0}"
PROFILE_PHASE_TIMEOUT="${WK_BENCH_PROFILE_PHASE_TIMEOUT:-120}"
SENDER_PICK="${WK_BENCH_SENDER_PICK:-round_robin}"
PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"
RUNTIME_POOL_SAMPLE_INTERVAL="${WK_BENCH_RUNTIME_POOL_SAMPLE_INTERVAL:-1}"
RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"
CLIENT_MSG_PREFIX="${WK_BENCH_CLIENT_MSG_PREFIX:-}"
RUN_NONCE="${WK_BENCH_RUN_NONCE:-${TIMESTAMP}-$$-${RANDOM}}"
QUIESCENCE_TIMEOUT="${WK_BENCH_QUIESCENCE_TIMEOUT:-60}"
QUIESCENCE_POLL_INTERVAL="${WK_BENCH_QUIESCENCE_POLL_INTERVAL:-1}"
DELIVERY_RECIPIENT_WORKER_CONCURRENCY="${WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY:-100}"
THREE_NODE_DATA_ROOT_SOURCE="default"
if [[ -n "${WK_WUKONGIM_THREE_NODES_DATA_ROOT:-}" ]]; then
  THREE_NODE_DATA_ROOT_SOURCE="env"
fi
THREE_NODE_DATA_ROOT="${WK_WUKONGIM_THREE_NODES_DATA_ROOT:-$ROOT_DIR/data}"
STORAGE_FREE_WARN_PERCENT="${WK_BENCH_STORAGE_FREE_WARN_PERCENT:-5}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-three-nodes-1000ch.sh [options]

Starts a local cmd/wukongim three-node cluster, then runs fixed multi-channel
wkbench traffic against it.

Options:
  --qps LIST             Comma-separated offered QPS list. Default: 1000,2000,2400,2490,2500,2600,2800,3000.
  --out-dir DIR          Evidence output directory.
  --wkbench-bin PATH     wkbench binary path. Default: data/wkbench-test.
  --worker-addr URL      Worker control URL. Default: http://127.0.0.1:19130.
  --worker-listen ADDR   Temporary worker listen address. Default: 127.0.0.1:19130.
  --no-worker            Do not start a temporary worker; require --worker-addr to be reachable.
  --no-start             Do not start or stop the three-node cluster; use an already-running cluster.
  --no-clean             When starting the cluster, keep existing node data.
  --start-script PATH    Three-node startup script. Default: scripts/start-wukongim-three-nodes.sh.
  --ready-timeout SECS   Cluster ready wait timeout. Default: 90.
  --channels N           Fixed group channel count. Default: 1000.
  --users N              Online user pool. Default: 4096.
  --members N            Members per group channel. Default: 10.
  --concurrency N        wkbench send concurrency. Default: 2800.
  --duration DURATION    Measured run duration. Default: 15s.
  --warmup DURATION      Warmup duration. Default: 5s.
  --cooldown DURATION    Cooldown duration. Default: 2s.
  --stable-p99 DURATION  Soft p99 gate written into scenarios. Default: 400ms.
                         Summary PASS also requires actual/offered >= WK_BENCH_ACTUAL_QPS_MIN_RATIO, default 0.90.
  --ack-timeout DURATION Per-SEND sendack wait timeout in generated traffic. Default: 15s.
  --phase-poll-timeout DURATION
                         Base wkbench worker phase poll timeout. Default: 30s.
  --profile-seconds N    Capture run-phase CPU pprof for each node when N > 0. Default: 0.
  --recv-ack BOOL        Whether drained group recv frames are acknowledged. Default: true.
  --heartbeat BOOL       Whether benchmark clients send heartbeat pings. Default: true.
  --sender-pick MODE     Group sender selection: round_robin or first_online. Default: round_robin.
  --client-msg-prefix P  Explicit client message prefix for reproducible runs.
                         By default every script invocation generates a unique prefix.
  --quiescence-timeout SECS
                         Fail if runtime queues do not drain before a QPS attempt. Default: 60.
  --quiescence-poll-interval SECS
                         Runtime queue polling interval. Default: 1.
  --api LIST             Comma-separated API base URLs. Default: node 5011/5012/5013.
  --gateway LIST         Comma-separated WKProto gateway addresses. Default: 5111/5112/5113.
  --metrics LIST         Comma-separated metrics base URLs. Default: same as --api.
  --resource-interval SECS
                         Server process CPU/memory sample interval. 0 disables periodic sampling. Default: 1.
  -h, --help             Show this help.

Example:
  scripts/bench-wukongim-three-nodes-1000ch.sh --qps 2000,2400,2500

  # Reuse an already-running cluster:
  scripts/bench-wukongim-three-nodes-1000ch.sh --no-start --qps 2000,2500
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
  printf '%s[bench-three-%sch]%s %s\n' "$C_CYAN" "$CHANNELS" "$C_RESET" "$*"
}

die() {
  printf '%s[bench-three-%sch] ERROR:%s %s\n' "$C_RED" "$CHANNELS" "$C_RESET" "$*" >&2
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
    --client-msg-prefix)
      [[ $# -ge 2 ]] || die '--client-msg-prefix requires a value'
      CLIENT_MSG_PREFIX="$2"
      shift 2
      ;;
    --quiescence-timeout)
      [[ $# -ge 2 ]] || die '--quiescence-timeout requires a value'
      QUIESCENCE_TIMEOUT="$2"
      shift 2
      ;;
    --quiescence-poll-interval)
      [[ $# -ge 2 ]] || die '--quiescence-poll-interval requires a value'
      QUIESCENCE_POLL_INTERVAL="$2"
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

require_positive_int '--channels' "$CHANNELS"
require_positive_int '--users' "$USERS"
require_positive_int '--members' "$GROUP_MEMBERS"
require_positive_int '--concurrency' "$CONCURRENCY"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_positive_int '--quiescence-timeout' "$QUIESCENCE_TIMEOUT"
require_positive_int 'WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY' "$DELIVERY_RECIPIENT_WORKER_CONCURRENCY"
require_nonnegative_number 'WK_BENCH_STORAGE_FREE_WARN_PERCENT' "$STORAGE_FREE_WARN_PERCENT"
require_nonnegative_number '--resource-interval' "$RESOURCE_SAMPLE_INTERVAL"
require_nonnegative_number '--quiescence-poll-interval' "$QUIESCENCE_POLL_INTERVAL"
awk -v value="$QUIESCENCE_POLL_INTERVAL" 'BEGIN { exit !(value > 0) }' || \
  die "--quiescence-poll-interval must be greater than zero: $QUIESCENCE_POLL_INTERVAL"
[[ "$PROFILE_SECONDS" =~ ^[0-9]+$ ]] || die "--profile-seconds must be a non-negative integer: $PROFILE_SECONDS"
require_positive_int 'WK_BENCH_PROFILE_PHASE_TIMEOUT' "$PROFILE_PHASE_TIMEOUT"
case "$SENDER_PICK" in
  first_online|round_robin)
    ;;
  *)
    die "--sender-pick must be first_online or round_robin: $SENDER_PICK"
    ;;
esac
case "$RECV_ACK" in
  true|false)
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
if [[ -n "$CLIENT_MSG_PREFIX" && ! "$CLIENT_MSG_PREFIX" =~ ^[A-Za-z0-9._-]+$ ]]; then
  die "--client-msg-prefix must contain only letters, digits, dot, underscore, or dash: $CLIENT_MSG_PREFIX"
fi
if [[ ! "$RUN_NONCE" =~ ^[A-Za-z0-9._-]+$ ]]; then
  die "WK_BENCH_RUN_NONCE must contain only letters, digits, dot, underscore, or dash: $RUN_NONCE"
fi

declare -a QPS_VALUES API_VALUES GATEWAY_VALUES METRICS_VALUES
split_csv "$QPS_LIST" QPS_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

WORKER_PID=""
CLUSTER_PID=""
RESOURCE_SAMPLER_PID=""
RUNTIME_POOL_SAMPLER_PID=""
RUNTIME_POOL_SAMPLER_STOP_FILE=""
RUN_PPROF_PID=""
RUN_PPROF_STOP_FILE=""
SAMPLE_VALIDATION_FAILED=0
SOFT_P99_GATE_FAILED=0
SAMPLE_VALID="false"
SAMPLE_WORKER_SUCCESS="0"
SAMPLE_APPEND_EFFECT_DELTA="0"
SAMPLE_INVALID_REASON="not_checked"

cleanup() {
  stop_server_resource_sampler
  if [[ -n "$RUNTIME_POOL_SAMPLER_STOP_FILE" ]]; then
    touch "$RUNTIME_POOL_SAMPLER_STOP_FILE"
  fi
  if [[ -n "$RUNTIME_POOL_SAMPLER_PID" ]]; then
    kill "$RUNTIME_POOL_SAMPLER_PID" >/dev/null 2>&1 || true
    wait "$RUNTIME_POOL_SAMPLER_PID" 2>/dev/null || true
  fi
  if [[ -n "$RUN_PPROF_STOP_FILE" ]]; then
    touch "$RUN_PPROF_STOP_FILE"
  fi
  if [[ -n "$RUN_PPROF_PID" ]]; then
    kill "$RUN_PPROF_PID" >/dev/null 2>&1 || true
    wait "$RUN_PPROF_PID" 2>/dev/null || true
  fi
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
  # Preserve synchronous commits; one coordinator coalesces durable writes without concurrent fsync fragmentation.
  # Thirty-two reactors avoid preallocating about 411 MiB of mostly idle Event mailbox slots per node.
  # Keep server send timeout below the 15s client ACK wait so recovery can still write SENDACK.
  WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}" \
  WK_TOP_API_ENABLE="${WK_TOP_API_ENABLE:-false}" \
  WK_CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-3}" \
  WK_CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-96}" \
  WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}" \
  WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}" \
  WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}" \
  WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}" \
  WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}" \
  WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}" \
  WK_CHANNEL_APPEND_SHARD_COUNT="${WK_CHANNEL_APPEND_SHARD_COUNT:-0}" \
  WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE="${WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE:-0}" \
  WK_CHANNEL_APPEND_EFFECT_POOL_SIZE="${WK_CHANNEL_APPEND_EFFECT_POOL_SIZE:-0}" \
  WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY="${WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY:-0}" \
  WK_DELIVERY_RECIPIENT_WORKER_CONCURRENCY="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY" \
  WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}" \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}" \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}" \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}" \
  WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-1}" \
  WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS="${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}" \
  WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}" \
  WK_GATEWAY_SEND_TIMEOUT="${WK_GATEWAY_SEND_TIMEOUT:-14s}" \
  WK_CLUSTER_COMMIT_COORDINATOR_SYNC="${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}" \
    "$START_SCRIPT" "${clean_arg[@]}" --ready-timeout "$READY_TIMEOUT" \
      >"$OUT_DIR/cluster-start.log" 2>&1 &
  CLUSTER_PID="$!"
}

ensure_wkbench_binary() {
  if [[ -x "$WK_BENCH_BIN" ]]; then
    local newer_source
    newer_source="$(find "$ROOT_DIR/cmd/wkbench" "$ROOT_DIR/internal/bench" -type f -newer "$WK_BENCH_BIN" -print -quit)"
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

check_cluster_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local api gateway all_ready
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
    cat <<'YAML'
  token: ""
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

write_scenario() {
  local qps="$1"
  local tag="$2"
  local report_dir="$3"
  local rate client_msg_prefix
  rate="$(rate_per_channel "$qps")"
  client_msg_prefix="$CLIENT_MSG_PREFIX"
  if [[ -z "$client_msg_prefix" ]]; then
    client_msg_prefix="bench${tag}-${RUN_NONCE}-msg"
  fi
  cat >"$OUT_DIR/scenario-${tag}.yaml" <<YAML
version: wkbench/v1
run:
  id: three-node-fixed-${CHANNELS}ch-${tag}-qps
  duration: $DURATION
  warmup: $WARMUP
  cooldown: $COOLDOWN
  random_seed: 0
  fail_fast: true
  report_dir: $report_dir
limits:
  fail_on_soft: true
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
  client_msg_prefix: $client_msg_prefix
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

scrape_metrics() {
  local tag="$1"
  local phase="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  mkdir -p "$metrics_dir"
  local addr id
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    curl -fsS "${addr%/}/metrics" >"$metrics_dir/${id}-${phase}.prom"
  done
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

runtime_queue_depths() {
  local metrics_file="$1"
  awk '
    $1 ~ /^wukongim_delivery_recipient_worker_queue_depth([{]|$)/ {
      delivery += $2
      delivery_seen = 1
    }
    $1 ~ /^wukongim_delivery_recipient_worker_inflight([{]|$)/ {
      delivery_inflight += $2
      delivery_inflight_seen = 1
    }
    $1 ~ /^wukongim_delivery_recipient_worker_capacity([{]|$)/ {
      delivery_capacity += $2
      delivery_capacity_seen = 1
    }
    $1 ~ /^wukongim_channelappend_writer_state_items[{]/ && $1 ~ /kind="post_commit_backlog"/ {
      post_commit += $2
      post_commit_seen = 1
    }
    $1 ~ /^wukongim_gateway_async_send_queue_depth([{]|$)/ {
      gateway += $2
      gateway_seen = 1
    }
    END {
      if (!delivery_seen || !delivery_inflight_seen || !delivery_capacity_seen || !post_commit_seen || !gateway_seen) {
        exit 2
      }
      printf "%.6g\t%.6g\t%.6g\t%.6g\t%.6g\n", delivery, delivery_inflight, delivery_capacity, post_commit, gateway
    }
  ' "$metrics_file"
}

wait_for_runtime_quiescence() {
  local tag="$1"
  local evidence_dir="$OUT_DIR/quiescence"
  local evidence="$evidence_dir/${tag}.tsv"
  local started now elapsed observed_at sample all_drained capacity_mismatch
  local addr id snapshot values delivery delivery_inflight delivery_capacity post_commit gateway state
  mkdir -p "$evidence_dir"
  printf 'sample\tobserved_at\telapsed_seconds\tnode\tdelivery_queue_depth\tdelivery_worker_inflight\tdelivery_worker_capacity\tpost_commit_backlog\tgateway_queue_depth\tstate\n' >"$evidence"
  started="$(date +%s)"
  sample=0

  while true; do
    sample=$((sample + 1))
    now="$(date +%s)"
    elapsed=$((now - started))
    observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    all_drained=1
    capacity_mismatch=0
    for addr in "${METRICS_VALUES[@]}"; do
      id="$(metric_file_id "$addr")"
      snapshot="$evidence_dir/${tag}-${id}-latest.prom"
      delivery="NA"
      delivery_inflight="NA"
      delivery_capacity="NA"
      post_commit="NA"
      gateway="NA"
      state="scrape_error"
      if curl -fsS --max-time 5 "${addr%/}/metrics" >"$snapshot"; then
        if values="$(runtime_queue_depths "$snapshot")"; then
          IFS=$'\t' read -r delivery delivery_inflight delivery_capacity post_commit gateway <<<"$values"
          state="pending"
          if ! awk -v actual="$delivery_capacity" -v expected="$DELIVERY_RECIPIENT_WORKER_CONCURRENCY" \
            'BEGIN { exit !((actual + 0) == (expected + 0)) }'; then
            state="capacity_mismatch"
            capacity_mismatch=1
            all_drained=0
          elif awk -v delivery="$delivery" -v inflight="$delivery_inflight" -v post_commit="$post_commit" -v gateway="$gateway" \
            'BEGIN { exit !((delivery + 0) == 0 && (inflight + 0) == 0 && (post_commit + 0) == 0 && (gateway + 0) == 0) }'; then
            state="drained"
          else
            all_drained=0
          fi
        else
          state="missing_metrics"
          all_drained=0
        fi
      else
        all_drained=0
      fi
      printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
        "$sample" "$observed_at" "$elapsed" "$id" "$delivery" "$delivery_inflight" "$delivery_capacity" "$post_commit" "$gateway" "$state" >>"$evidence"
    done

    if [[ "$capacity_mismatch" -ne 0 ]]; then
      printf '# result=capacity_mismatch expected_delivery_worker_capacity=%s samples=%s\n' \
        "$DELIVERY_RECIPIENT_WORKER_CONCURRENCY" "$sample" >>"$evidence"
      die "recipient worker capacity mismatch before tag=$tag; expected=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY evidence=$evidence"
    fi
    if [[ "$all_drained" -eq 1 ]]; then
      printf '# result=passed elapsed_seconds=%s samples=%s\n' "$elapsed" "$sample" >>"$evidence"
      log "runtime queues drained tag=$tag elapsed=${elapsed}s evidence=$evidence"
      return
    fi
    if (( elapsed >= QUIESCENCE_TIMEOUT )); then
      printf '# result=timeout elapsed_seconds=%s samples=%s timeout_seconds=%s\n' \
        "$elapsed" "$sample" "$QUIESCENCE_TIMEOUT" >>"$evidence"
      die "runtime queues did not converge before tag=$tag within ${QUIESCENCE_TIMEOUT}s; evidence=$evidence"
    fi
    sleep "$QUIESCENCE_POLL_INTERVAL"
  done
}

scrape_append_effect_snapshot() {
  local tag="$1"
  local phase="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag/validity"
  local addr id out
  mkdir -p "$metrics_dir"
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    out="$metrics_dir/${id}-${phase}.prom"
    if ! curl -fsS --max-time 5 -H "X-WK-Bench-Evidence: append-effect-${phase}" \
      "${addr%/}/metrics" >"$out"; then
      : >"$out"
    fi
  done
}

append_effect_items_total() {
  local tag="$1"
  local phase="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag/validity"
  local files=("$metrics_dir"/*-"$phase".prom)
  local file value
  local total=0
  local seen_files=0
  [[ -e "${files[0]}" ]] || return 2
  [[ "${#files[@]}" -eq "${#METRICS_VALUES[@]}" ]] || return 2
  for file in "${files[@]}"; do
    if ! value="$(awk '
      $1 ~ /^wukongim_channelappend_effect_items_sum[{]/ &&
        $1 ~ /result="ok"/ && $1 ~ /stage="append"/ {
        total += $2
        matches++
      }
      END {
        if (matches != 1) {
          exit 2
        }
        printf "%.0f\n", total
      }
    ' "$file")"; then
      return 2
    fi
    total=$((total + value))
    seen_files=$((seen_files + 1))
  done
  [[ "$seen_files" -eq "${#METRICS_VALUES[@]}" ]] || return 2
  printf '%s\n' "$total"
}

validate_append_effect_sample() {
  local tag="$1"
  local report="$2"
  local before after
  SAMPLE_VALID="false"
  SAMPLE_WORKER_SUCCESS="0"
  SAMPLE_APPEND_EFFECT_DELTA="0"
  SAMPLE_INVALID_REASON="missing_report"

  if [[ -f "$report" ]]; then
    SAMPLE_INVALID_REASON="missing_worker_success"
    if SAMPLE_WORKER_SUCCESS="$(jq -r '
      [
        .metrics.counters
        | to_entries[]
        | select(.key | startswith("group_send_success_total{"))
        | select(.key | contains("profile=thousand-groups"))
        | select(.key | contains("traffic=group-send"))
        | select((.key | contains("phase=warmup")) or (.key | contains("phase=run")))
        | .value
      ]
      | add // 0
      | floor
    ' "$report" 2>/dev/null)"; then
      if [[ "$SAMPLE_WORKER_SUCCESS" -le 0 ]]; then
        SAMPLE_INVALID_REASON="no_successful_messages"
      else
        SAMPLE_INVALID_REASON="missing_append_effect_metrics"
        if before="$(append_effect_items_total "$tag" before)" && \
          after="$(append_effect_items_total "$tag" after)"; then
          SAMPLE_APPEND_EFFECT_DELTA=$((after - before))
          SAMPLE_INVALID_REASON="append_effect_mismatch"
          if [[ "$SAMPLE_APPEND_EFFECT_DELTA" -eq "$SAMPLE_WORKER_SUCCESS" ]]; then
            SAMPLE_VALID="true"
            SAMPLE_INVALID_REASON="ok"
          fi
        fi
      fi
    fi
  fi

  printf '%s\t%s\t%s\t%s\t%s\n' \
    "$tag" "$SAMPLE_WORKER_SUCCESS" "$SAMPLE_APPEND_EFFECT_DELTA" "$SAMPLE_VALID" "$SAMPLE_INVALID_REASON" \
    >>"$OUT_DIR/sample-validity.tsv"
  if [[ "$SAMPLE_VALID" != "true" ]]; then
    SAMPLE_VALIDATION_FAILED=1
    log "invalid sample tag=$tag worker_success_with_warmup=$SAMPLE_WORKER_SUCCESS append_effect_delta=$SAMPLE_APPEND_EFFECT_DELTA reason=$SAMPLE_INVALID_REASON"
  fi
}

collect_node_logs() {
  local phase="$1"
  local dest="$OUT_DIR/logs/$phase"
  mkdir -p "$dest"
  cp "$ROOT_DIR"/data/wukongim-three-node-logs/node*.log "$dest/" 2>/dev/null || true
  if [[ -f "$OUT_DIR/cluster-start.log" ]]; then
    cp "$OUT_DIR/cluster-start.log" "$dest/cluster-start.log" 2>/dev/null || true
  fi
}

capture_node_pprof() {
  local phase="$1"
  local attempt_tag="${2:-}"
  local pprof_dir="$OUT_DIR/pprof/$phase"
  if [[ -n "$attempt_tag" ]]; then
    pprof_dir="$pprof_dir/$attempt_tag"
  fi
  mkdir -p "$pprof_dir"
  local addr id pid
  local pids=()
  for addr in "${API_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    (
      cpu_pid=""
      if [[ "$phase" == "run" ]] && (( PROFILE_SECONDS > 0 )); then
        curl -fsS -H "X-WK-Bench-Evidence: pprof-${phase}" "${addr%/}/debug/pprof/profile?seconds=${PROFILE_SECONDS}" >"$pprof_dir/${id}-cpu.pb.gz" || true &
        cpu_pid="$!"
      fi
      curl -fsS -H "X-WK-Bench-Evidence: pprof-${phase}" "${addr%/}/debug/pprof/goroutine?debug=2" >"$pprof_dir/${id}-goroutine.txt" || true
      curl -fsS -H "X-WK-Bench-Evidence: pprof-${phase}" "${addr%/}/debug/pprof/heap" >"$pprof_dir/${id}-heap.pb.gz" || true
      if [[ -n "$cpu_pid" ]]; then
        wait "$cpu_pid" 2>/dev/null || true
      fi
    ) &
    pids+=("$!")
  done
  for pid in "${pids[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
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
    END {
      if (pid != "") {
        print pid
      }
    }
  ' "$log_file"
}

server_pid_from_process_table() {
  local node="$1"
  local config="$ROOT_DIR/scripts/wukongim/wukongim-node${node}.toml"
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
  for node in 1 2 3; do
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
      for (i = 1; i <= 3; i++) {
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

runtime_pool_sampler_loop() {
  local tag="$1"
  local stop_file="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local seq=0
  local addr id
  mkdir -p "$metrics_dir"
  while [[ ! -f "$stop_file" ]]; do
    for addr in "${METRICS_VALUES[@]}"; do
      id="$(metric_file_id "$addr")"
      curl -fsS --max-time 2 "${addr%/}/metrics" >"$metrics_dir/${id}-sample-${seq}.prom" 2>/dev/null || true
    done
    seq=$((seq + 1))
    sleep "$RUNTIME_POOL_SAMPLE_INTERVAL" || true
  done
}

start_runtime_pool_sampler() {
  local tag="$1"
  local stop_file
  stop_file="$(runtime_pool_sampler_stop_file "$tag")"
  rm -f "$stop_file"
  runtime_pool_sampler_loop "$tag" "$stop_file" >/dev/null 2>&1 &
  RUNTIME_POOL_SAMPLER_PID="$!"
  RUNTIME_POOL_SAMPLER_STOP_FILE="$stop_file"
}

stop_runtime_pool_sampler() {
  [[ -n "$RUNTIME_POOL_SAMPLER_PID" ]] || return 0
  touch "$RUNTIME_POOL_SAMPLER_STOP_FILE"
  wait "$RUNTIME_POOL_SAMPLER_PID" 2>/dev/null || true
  rm -f "$RUNTIME_POOL_SAMPLER_STOP_FILE"
  RUNTIME_POOL_SAMPLER_PID=""
  RUNTIME_POOL_SAMPLER_STOP_FILE=""
}

write_run_pprof_sampler_result() {
  local pprof_dir="$1"
  local expected_run_id="$2"
  local observed_run_id="$3"
  local observed_phase="$4"
  local observed_active_phase="$5"
  local end_observed_run_id="$6"
  local end_observed_phase="$7"
  local end_observed_active_phase="$8"
  local gate_at="$9"
  local capture_done_at="${10}"
  local valid="${11}"
  local reason="${12}"
  local out="$pprof_dir/sampler.tsv"
  {
    printf 'expected_run_id\tobserved_run_id\tobserved_phase\tobserved_active_phase\tend_observed_run_id\tend_observed_phase\tend_observed_active_phase\tgate_at\tcapture_done_at\tvalid\treason\n'
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$expected_run_id" "$observed_run_id" "$observed_phase" "$observed_active_phase" \
      "$end_observed_run_id" "$end_observed_phase" "$end_observed_active_phase" \
      "$gate_at" "$capture_done_at" "$valid" "$reason"
  } >"$out"
}

run_pprof_sampler_loop() {
  local expected_run_id="$1"
  local stop_file="$2"
  local tag="$3"
  local pprof_dir="$OUT_DIR/pprof/run/$tag"
  local deadline=$((SECONDS + PROFILE_PHASE_TIMEOUT))
  local status observed_run_id="" observed_phase="" observed_active_phase="" last_error=""
  local end_status="" end_observed_run_id="" end_observed_phase="" end_observed_active_phase="" end_last_error=""
  local gate_at="" capture_done_at="" valid reason="profile_phase_timeout"
  local addr id
  mkdir -p "$pprof_dir"

  while (( SECONDS < deadline )); do
    if [[ -f "$stop_file" ]]; then
      reason="run_completed_before_capture"
      break
    fi
    status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status" 2>/dev/null || true)"
    if jq -e . >/dev/null 2>&1 <<<"$status"; then
      observed_run_id="$(jq -r '.assignment.run_id // ""' <<<"$status")"
      observed_phase="$(jq -r '.phase // ""' <<<"$status")"
      observed_active_phase="$(jq -r '.active_phase // ""' <<<"$status")"
      last_error="$(jq -r '.last_error // ""' <<<"$status")"
      if [[ "$observed_run_id" == "$expected_run_id" && "$observed_active_phase" == "run" && -z "$last_error" ]]; then
        printf '%s\n' "$status" >"$pprof_dir/worker-status.json"
        gate_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        capture_node_pprof run "$tag"
        valid="true"
        reason="ok"
        end_status="$(curl -fsS --connect-timeout 1 --max-time 2 "${WORKER_ADDR%/}/v1/status" 2>/dev/null || true)"
        if jq -e . >/dev/null 2>&1 <<<"$end_status"; then
          printf '%s\n' "$end_status" >"$pprof_dir/worker-status-end.json"
          end_observed_run_id="$(jq -r '.assignment.run_id // ""' <<<"$end_status")"
          end_observed_phase="$(jq -r '.phase // ""' <<<"$end_status")"
          end_observed_active_phase="$(jq -r '.active_phase // ""' <<<"$end_status")"
          end_last_error="$(jq -r '.last_error // ""' <<<"$end_status")"
          if [[ "$end_observed_run_id" != "$expected_run_id" ]]; then
            valid="false"
            reason="run_id_changed_during_capture"
          elif [[ "$end_observed_active_phase" != "run" ]]; then
            valid="false"
            reason="active_run_ended_during_capture"
          elif [[ -n "$end_last_error" ]]; then
            valid="false"
            reason="worker_error_after_capture"
          fi
        else
          valid="false"
          reason="worker_status_unavailable_after_capture"
          printf '%s\n' "$end_status" >"$pprof_dir/worker-status-end.txt"
        fi
        capture_done_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
        if [[ "$valid" == "true" ]]; then
          for addr in "${API_VALUES[@]}"; do
            id="$(metric_file_id "$addr")"
            if [[ ! -s "$pprof_dir/${id}-cpu.pb.gz" ]]; then
              valid="false"
              reason="missing_cpu_profile"
            fi
          done
        fi
        write_run_pprof_sampler_result "$pprof_dir" "$expected_run_id" "$observed_run_id" "$observed_phase" "$observed_active_phase" \
          "$end_observed_run_id" "$end_observed_phase" "$end_observed_active_phase" \
          "$gate_at" "$capture_done_at" "$valid" "$reason"
        return 0
      fi
      if [[ "$observed_run_id" == "$expected_run_id" && -n "$last_error" ]]; then
        reason="worker_error"
        break
      fi
      if [[ "$observed_run_id" == "$expected_run_id" && -z "$observed_active_phase" && ( "$observed_phase" == "run" || "$observed_phase" == "cooldown" || "$observed_phase" == "stopped" ) ]]; then
        reason="missed_active_run_phase"
        break
      fi
    fi
    sleep 0.1
  done
  capture_done_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  write_run_pprof_sampler_result "$pprof_dir" "$expected_run_id" "$observed_run_id" "$observed_phase" "$observed_active_phase" \
    "" "" "" "$gate_at" "$capture_done_at" "false" "$reason"
}

start_run_pprof_sampler() {
  local expected_run_id="$1"
  local stop_file="$2"
  local tag="$3"
  if (( PROFILE_SECONDS <= 0 )); then
    return 0
  fi
  mkdir -p "$(dirname "$stop_file")"
  rm -f "$stop_file"
  run_pprof_sampler_loop "$expected_run_id" "$stop_file" "$tag" >/dev/null 2>&1 &
  RUN_PPROF_PID="$!"
  RUN_PPROF_STOP_FILE="$stop_file"
}

stop_run_pprof_sampler() {
  if [[ -n "$RUN_PPROF_STOP_FILE" ]]; then
    touch "$RUN_PPROF_STOP_FILE"
  fi
}

wait_run_pprof_sampler() {
  [[ -n "$RUN_PPROF_PID" ]] || return 0
  wait "$RUN_PPROF_PID" 2>/dev/null || true
  rm -f "$RUN_PPROF_STOP_FILE"
  RUN_PPROF_PID=""
  RUN_PPROF_STOP_FILE=""
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
  local tag report_dir exit_status duration expected_run_id pprof_stop_file
  tag="$(qps_tag "$qps")"
  expected_run_id="three-node-fixed-${CHANNELS}ch-${tag}-qps"
  pprof_stop_file="$OUT_DIR/pprof/run/${tag}/${tag}.stop"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  duration="$(duration_seconds "$DURATION")"
  mkdir -p "$report_dir"

  write_scenario "$qps" "$tag" "$report_dir"
  curl -fsS -X POST "${WORKER_ADDR%/}/v1/stop" >/dev/null 2>&1 || true
  wait_for_runtime_quiescence "$tag"
  scrape_append_effect_snapshot "$tag" before

  log "running qps=$qps tag=$tag"
  scrape_metrics "$tag" before
  start_runtime_pool_sampler "$tag"
  start_run_pprof_sampler "$expected_run_id" "$pprof_stop_file" "$tag"
  exit_status=0
  "$WK_BENCH_BIN" run \
    --target "$OUT_DIR/target.yaml" \
    --scenario "$OUT_DIR/scenario-${tag}.yaml" \
    --workers "$OUT_DIR/workers.yaml" \
    --phase-poll-timeout "$PHASE_POLL_TIMEOUT" \
    >"$report_dir/wkbench-console.txt" 2>&1 || exit_status=$?
  stop_run_pprof_sampler
  scrape_append_effect_snapshot "$tag" after
  stop_runtime_pool_sampler
  scrape_metrics "$tag" after
  wait_run_pprof_sampler
  classify_metrics "$tag"
  rpc_pull_qps_summary "$tag" "$duration"
  channel_metrics_summary "$tag" "$duration"
  channelappend_metrics_summary "$tag"
  runtime_pool_pressure_summary "$tag"
  ants_pool_usage_summary "$tag"
  cluster_transport_peak_summary "$tag"
  validate_append_effect_sample "$tag" "$report_dir/report.json"

  if [[ ! -f "$report_dir/report.json" ]]; then
    printf '%s\t%s\tmissing_report\t%s\t0\t0\t0\t0\t0\t0\t0\t0\t0\t%s\t%s\t%s\t%s\n' \
      "$tag" "$qps" "$exit_status" "$SAMPLE_VALID" "$SAMPLE_WORKER_SUCCESS" "$SAMPLE_APPEND_EFFECT_DELTA" "$SAMPLE_INVALID_REASON" \
      >>"$OUT_DIR/summary.tsv"
    return
  fi
  jq -r --arg tag "$tag" --arg qps "$qps" --arg exit_status "$exit_status" --arg duration "$duration" \
    --arg sample_valid "$SAMPLE_VALID" --arg worker_success "$SAMPLE_WORKER_SUCCESS" \
    --arg append_effect_delta "$SAMPLE_APPEND_EFFECT_DELTA" --arg invalid_reason "$SAMPLE_INVALID_REASON" '
    (.metrics.counters["group_send_success_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}"] // 0) as $success
    | (.metrics.counters["group_send_error_total{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}"] // 0) as $errors
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
        $sample_valid,
        $worker_success,
        $append_effect_delta,
        $invalid_reason
      ] | @tsv
  ' "$report_dir/report.json" >>"$OUT_DIR/summary.tsv"
  if jq -e --argjson p99_limit "$(duration_seconds "$STABLE_P99")" '
    (.metrics.histograms["group_send_latency_seconds{channel_type=group,phase=run,profile=thousand-groups,traffic=group-send}"].p99_seconds // 0) > $p99_limit
  ' "$report_dir/report.json" >/dev/null; then
    SOFT_P99_GATE_FAILED=1
  fi
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
CHANNELS=$CHANNELS
USERS=$USERS
GROUP_MEMBERS=$GROUP_MEMBERS
CONCURRENCY=$CONCURRENCY
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
SENDER_PICK=$SENDER_PICK
CLIENT_MSG_PREFIX=${CLIENT_MSG_PREFIX:-<auto-unique>}
RUN_NONCE=$RUN_NONCE
QUIESCENCE_TIMEOUT=$QUIESCENCE_TIMEOUT
QUIESCENCE_POLL_INTERVAL=$QUIESCENCE_POLL_INTERVAL
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
WORKER_ADDR=$WORKER_ADDR
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
CHANNEL_APPEND_SHARD_COUNT=${WK_CHANNEL_APPEND_SHARD_COUNT:-0}
CHANNEL_APPEND_ADVANCE_POOL_SIZE=${WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE:-0}
CHANNEL_APPEND_EFFECT_POOL_SIZE=${WK_CHANNEL_APPEND_EFFECT_POOL_SIZE:-0}
CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=${WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY:-0}
DELIVERY_RECIPIENT_WORKER_CONCURRENCY=$DELIVERY_RECIPIENT_WORKER_CONCURRENCY
THREE_NODE_DATA_ROOT=$THREE_NODE_DATA_ROOT
THREE_NODE_DATA_ROOT_SOURCE=$THREE_NODE_DATA_ROOT_SOURCE
CLUSTER_CHANNEL_REACTOR_COUNT=${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}
CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}
CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}
CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}
CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}
CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}
TOP_API_ENABLE=${WK_TOP_API_ENABLE:-false}
CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}
CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}
CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}
CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}
CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-1}
CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}
GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}
GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}
GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
PROFILE_SECONDS=$PROFILE_SECONDS
PROFILE_PHASE_TIMEOUT=$PROFILE_PHASE_TIMEOUT
RUNTIME_POOL_SAMPLE_INTERVAL=$RUNTIME_POOL_SAMPLE_INTERVAL
RESOURCE_SAMPLE_INTERVAL=$RESOURCE_SAMPLE_INTERVAL
EOF
  write_storage_preflight
  mkdir -p "$OUT_DIR/config"
  cp "$ROOT_DIR"/scripts/wukongim/wukongim-node*.toml "$OUT_DIR/config/" 2>/dev/null || true
  if [[ -x "$START_SCRIPT" ]]; then
    "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
  collect_node_logs before
  scrape_metrics_snapshot before
  capture_node_pprof before
}

write_storage_preflight() {
  local probe_path="$THREE_NODE_DATA_ROOT"
  local filesystem blocks_kib used_kib available_kib used_percent mount_point free_percent result
  while [[ ! -e "$probe_path" && "$probe_path" != "/" ]]; do
    probe_path="$(dirname "$probe_path")"
  done
  read -r filesystem blocks_kib used_kib available_kib used_percent mount_point < <(
    df -Pk "$probe_path" | awk 'NR == 2 { print; exit }'
  )
  used_percent="${used_percent%%%}"
  free_percent=$((100 - used_percent))
  result="ok"
  if [[ "$START_CLUSTER" -eq 0 && "$THREE_NODE_DATA_ROOT_SOURCE" != "env" ]]; then
    result="external_cluster_data_root_unverified"
    log "WARNING: --no-start data root is unknown; set WK_WUKONGIM_THREE_NODES_DATA_ROOT to make storage evidence attributable"
  elif awk -v free="$free_percent" -v warn="$STORAGE_FREE_WARN_PERCENT" 'BEGIN { exit !(free < warn) }'; then
    result="low_free_space"
    log "WARNING: data filesystem free space is ${free_percent}% at $mount_point; sync-commit latency evidence may be storage-bound"
  fi
  {
    printf 'data_root\tdata_root_source\tprobe_path\tfilesystem\tmount_point\tblocks_kib\tused_kib\tavailable_kib\tfree_percent\twarn_below_percent\tresult\n'
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$THREE_NODE_DATA_ROOT" "$THREE_NODE_DATA_ROOT_SOURCE" "$probe_path" "$filesystem" "$mount_point" "$blocks_kib" "$used_kib" "$available_kib" \
      "$free_percent" "$STORAGE_FREE_WARN_PERCENT" "$result"
  } >"$OUT_DIR/storage-preflight.tsv"
}

write_display_summary() {
  local p99_limit
  p99_limit="$(duration_seconds "$STABLE_P99")"
  # Write archival summary.txt without ANSI escapes
  (
    C_RESET='' C_BOLD='' C_DIM='' C_GREEN='' C_RED='' C_YELLOW='' C_CYAN='' C_MAGENTA='' C_WHITE=''
    awk -v rpc_file="$OUT_DIR/rpc_pull_qps.tsv" -v p99_limit="$p99_limit" -v actual_min_ratio="$ACTUAL_QPS_MIN_RATIO" \
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
      printf "p99 gate: <= %.0f ms │ send_errors: 0\n", p99_limit * 1000
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
      errors = $7 + 0
      p95 = $11 + 0
      p99 = $12 + 0
      max = $13 + 0
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
      if (p99 > p99_limit) {
        result = "FAIL"
        note = "p99"
      }
      if (actual_ratio < actual_min_ratio) {
        result = "FAIL"
        note = sprintf("actual_ratio=%.3f", actual_ratio)
      }
      if ($14 != "true") {
        result = "FAIL"
        note = "invalid_sample:" $17
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
  awk -v rpc_file="$OUT_DIR/rpc_pull_qps.tsv" -v p99_limit="$p99_limit" -v actual_min_ratio="$ACTUAL_QPS_MIN_RATIO" \
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
      printf "p99 gate: <= %.0f ms │ send_errors: 0\n", p99_limit * 1000
      printf "actual/offered gate: >= %.2f\n\n", actual_min_ratio
      printf "%s%9s %10s %7s %8s %8s %8s %8s %8s %12s %s%s\n", c_dim, "offered", "actual", "ratio", "result", "errors", "p99ms", "p95ms", "maxms", "rpc_pull/s", "note", c_reset
    }
    NR == 1 { next }
    {
      tag = $1; offered = $2 + 0; status = $3; exit_status = $4 + 0
      actual = $5 + 0; errors = $7 + 0; p95 = $11 + 0; p99 = $12 + 0; max = $13 + 0
      actual_ratio = (offered > 0) ? actual / offered : 0
      note = "ok"; result = "PASS"
      if (status != "passed") { result = "FAIL"; note = status }
      if (exit_status != 0) { result = "FAIL"; note = "exit=" exit_status }
      if (errors > 0) { result = "FAIL"; note = "send_errors" }
      if (p99 > p99_limit) { result = "FAIL"; note = "p99" }
      if (actual_ratio < actual_min_ratio) { result = "FAIL"; note = sprintf("actual_ratio=%.3f", actual_ratio) }
      if ($14 != "true") { result = "FAIL"; note = "invalid_sample:" $17 }
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
# Three-Node Bench Evidence

## Scenario
- workload: local wukongim three-node cluster wkbench group channels
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
- quiescence: quiescence/*.tsv
- sample_validity: sample-validity.tsv
- storage_preflight: storage-preflight.tsv

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
  mkdir -p "$OUT_DIR/metrics" "$OUT_DIR/reports"

  ensure_wkbench_binary
  start_cluster
  check_cluster_ready
  ensure_worker
  write_target_and_workers
  write_run_metadata
  start_server_resource_sampler

  cat >"$OUT_DIR/summary.tsv" <<'EOF'
tag	offered_qps	status	exit_status	actual_qps	send_success	send_errors	connect_error_rate	sendack_error_rate	p50_seconds	p95_seconds	p99_seconds	max_seconds	sample_valid	worker_success_with_warmup	append_effect_items_delta	invalid_reason
EOF
  cat >"$OUT_DIR/sample-validity.tsv" <<'EOF'
tag	worker_success_with_warmup	append_effect_items_delta	sample_valid	reason
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
  cat >"$OUT_DIR/runtime_pool_pressure_summary.tsv" <<'EOF'
tag	node	component	pool	queue	priority	queue_depth_max	queue_capacity	queue_fill_max	queue_bytes_max	queue_bytes_capacity	queue_bytes_fill_max	inflight_max	workers	inflight_util_max	admission_full_delta	admission_busy_delta	admission_dirty_delta	admission_requeued_delta	reason
EOF
  cat >"$OUT_DIR/ants_pool_usage_summary.tsv" <<'EOF'
tag	node	component	pool	running	capacity	waiting	utilization_max
EOF
  cat >"$OUT_DIR/cluster_transport_peak_summary.tsv" <<'EOF'
tag	node	sample_points	sample_pairs	peak_internal_mib_s	peak_out_mib_s	peak_in_mib_s	peak_duplex_mib_s	peak_from_seq	peak_to_seq
EOF

  local qps
  for qps in "${QPS_VALUES[@]}"; do
    [[ "$qps" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "invalid qps value: $qps"
    run_attempt "$qps"
  done

  stop_server_resource_sampler
  sample_server_resources after || true
  write_server_resource_summary || true
  collect_node_logs after
  scrape_metrics_snapshot after
  capture_node_pprof after
  print_summary
  if [[ "$SAMPLE_VALIDATION_FAILED" -ne 0 ]]; then
    return 8
  fi
  if [[ "$SOFT_P99_GATE_FAILED" -ne 0 ]]; then
    return 9
  fi
}

main "$@"
