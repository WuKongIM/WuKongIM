#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_DELIVERY_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"

SCENARIO="${WK_BENCH_DELIVERY_SCENARIO:-group}"
QPS_LIST="${WK_BENCH_DELIVERY_QPS:-100,200,500,800,1000}"
OUT_DIR="${WK_BENCH_DELIVERY_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-delivery-${SCENARIO}}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-delivery/wkbench}"
WORKER_ADDR="${WK_BENCH_WORKER_ADDR:-http://127.0.0.1:19140}"
WORKER_LISTEN="${WK_BENCH_WORKER_LISTEN:-127.0.0.1:19140}"
START_WORKER=1
START_CLUSTER=1
CLEAN_CLUSTER=1
START_SCRIPT="$ROOT_DIR/scripts/start-wukongim-three-nodes.sh"
READY_TIMEOUT="${WK_BENCH_THREE_NODE_READY_TIMEOUT:-90}"

CHANNELS="${WK_BENCH_DELIVERY_CHANNELS:-1000}"
USERS="${WK_BENCH_DELIVERY_USERS:-10000}"
GROUP_MEMBERS="${WK_BENCH_DELIVERY_GROUP_MEMBERS:-50}"
CONCURRENCY="${WK_BENCH_DELIVERY_CONCURRENCY:-1000}"
PAYLOAD_BYTES="${WK_BENCH_DELIVERY_PAYLOAD_BYTES:-128}"
DURATION="${WK_BENCH_DELIVERY_DURATION:-60s}"
WARMUP="${WK_BENCH_DELIVERY_WARMUP:-10s}"
COOLDOWN="${WK_BENCH_DELIVERY_COOLDOWN:-5s}"
STABLE_P99="${WK_BENCH_DELIVERY_STABLE_P99:-800ms}"
PROFILE_SECONDS="${WK_BENCH_DELIVERY_PROFILE_SECONDS:-0}"
PHASE_POLL_TIMEOUT="${WK_BENCH_DELIVERY_PHASE_POLL_TIMEOUT:-120s}"
SENDER_PICK="${WK_BENCH_DELIVERY_SENDER_PICK:-round_robin}"
VERIFY_RECV="${WK_BENCH_DELIVERY_VERIFY_RECV:-none}"
RECV_ACK="${WK_BENCH_DELIVERY_RECV_ACK:-true}"

DELIVERY_ENABLE="${WK_DELIVERY_ENABLE:-true}"
DELIVERY_EVENT_QUEUE_SIZE="${WK_DELIVERY_EVENT_QUEUE_SIZE:-1024}"
DELIVERY_FANOUT_PAGE_SIZE="${WK_DELIVERY_FANOUT_PAGE_SIZE:-512}"
DELIVERY_PUSH_BATCH_SIZE="${WK_DELIVERY_PUSH_BATCH_SIZE:-512}"
DELIVERY_PENDING_ACK_MAX_PER_SESSION="${WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION:-1024}"
DELIVERY_PENDING_ACK_TTL="${WK_DELIVERY_PENDING_ACK_TTL:-30s}"

API_ADDRS="http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013"
GATEWAY_ADDRS="127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113"
METRICS_ADDRS="$API_ADDRS"
QPS_SET=0
OUT_DIR_SET=0
CHANNELS_SET=0
USERS_SET=0
MEMBERS_SET=0
VERIFY_RECV_SET=0
EVENT_QUEUE_SET=0

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-delivery.sh [options]

Runs delivery-focused wkbench traffic against a local cmd/wukongim three-node cluster.

Options:
  --scenario NAME          smoke, person, group, group-large, or saturation.
  --qps LIST               Comma-separated offered QPS list.
  --out-dir DIR            Evidence output directory.
  --wkbench-bin PATH       wkbench binary path.
  --worker-addr URL        Worker control URL.
  --worker-listen ADDR     Temporary worker listen address.
  --no-worker              Do not start a temporary worker.
  --no-start               Use an already-running local three-node cluster.
  --no-clean               Keep existing node data when starting the cluster.
  --ready-timeout SECS     Cluster ready wait timeout.
  --channels N             Channel count.
  --users N                Online user pool size.
  --members N              Group members per channel.
  --concurrency N          wkbench traffic concurrency.
  --payload-bytes N        Message payload size.
  --duration DURATION      Measured duration.
  --warmup DURATION        Warmup duration.
  --cooldown DURATION      Cooldown duration.
  --stable-p99 DURATION    Soft p99 gate written into wkbench scenario.
  --profile-seconds N      Capture CPU pprof when N > 0.
  --phase-poll-timeout D   Base wkbench worker phase poll timeout.
  --sender-pick MODE       first_online or round_robin.
  --verify-recv MODE       none, sampled, or full.
  --recv-ack BOOL          true or false.
  --event-queue-size N     WK_DELIVERY_EVENT_QUEUE_SIZE.
  --fanout-page-size N     WK_DELIVERY_FANOUT_PAGE_SIZE.
  --push-batch-size N      WK_DELIVERY_PUSH_BATCH_SIZE.
  --pending-ack-max N      WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION.
  -h, --help               Show this help.
USAGE
}

log() {
  printf '[bench-delivery] %s\n' "$*"
}

die() {
  printf '[bench-delivery] ERROR: %s\n' "$*" >&2
  exit 1
}

require_nonnegative_int() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer: $value"
}

require_positive_int() {
  local name="$1"
  local value="$2"
  require_nonnegative_int "$name" "$value"
  (( value > 0 )) || die "$name must be greater than zero: $value"
}

split_csv() {
  local raw="$1"
  local var_name="$2"
  eval "$var_name=()"
  local values=()
  local item
  IFS=',' read -ra values <<<"$raw"
  for item in "${values[@]}"; do
    item="${item//[[:space:]]/}"
    [[ -n "$item" ]] || die "comma-separated list contains an empty item: $raw"
    eval "$var_name+=(\"\$item\")"
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --scenario) SCENARIO="${2:?}"; shift 2 ;;
    --qps) QPS_LIST="${2:?}"; QPS_SET=1; shift 2 ;;
    --out-dir) OUT_DIR="${2:?}"; OUT_DIR_SET=1; shift 2 ;;
    --wkbench-bin) WK_BENCH_BIN="${2:?}"; shift 2 ;;
    --worker-addr) WORKER_ADDR="${2:?}"; shift 2 ;;
    --worker-listen) WORKER_LISTEN="${2:?}"; shift 2 ;;
    --no-worker) START_WORKER=0; shift ;;
    --no-start) START_CLUSTER=0; shift ;;
    --no-clean) CLEAN_CLUSTER=0; shift ;;
    --ready-timeout) READY_TIMEOUT="${2:?}"; shift 2 ;;
    --channels) CHANNELS="${2:?}"; CHANNELS_SET=1; shift 2 ;;
    --users) USERS="${2:?}"; USERS_SET=1; shift 2 ;;
    --members) GROUP_MEMBERS="${2:?}"; MEMBERS_SET=1; shift 2 ;;
    --concurrency) CONCURRENCY="${2:?}"; shift 2 ;;
    --payload-bytes) PAYLOAD_BYTES="${2:?}"; shift 2 ;;
    --duration) DURATION="${2:?}"; shift 2 ;;
    --warmup) WARMUP="${2:?}"; shift 2 ;;
    --cooldown) COOLDOWN="${2:?}"; shift 2 ;;
    --stable-p99) STABLE_P99="${2:?}"; shift 2 ;;
    --profile-seconds) PROFILE_SECONDS="${2:?}"; shift 2 ;;
    --phase-poll-timeout) PHASE_POLL_TIMEOUT="${2:?}"; shift 2 ;;
    --sender-pick) SENDER_PICK="${2:?}"; shift 2 ;;
    --verify-recv) VERIFY_RECV="${2:?}"; VERIFY_RECV_SET=1; shift 2 ;;
    --recv-ack) RECV_ACK="${2:?}"; shift 2 ;;
    --event-queue-size) DELIVERY_EVENT_QUEUE_SIZE="${2:?}"; EVENT_QUEUE_SET=1; shift 2 ;;
    --fanout-page-size) DELIVERY_FANOUT_PAGE_SIZE="${2:?}"; shift 2 ;;
    --push-batch-size) DELIVERY_PUSH_BATCH_SIZE="${2:?}"; shift 2 ;;
    --pending-ack-max) DELIVERY_PENDING_ACK_MAX_PER_SESSION="${2:?}"; shift 2 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

case "$SCENARIO" in
  smoke)
    if [[ "$VERIFY_RECV_SET" -eq 0 && "${WK_BENCH_DELIVERY_VERIFY_RECV:-}" == "" ]]; then VERIFY_RECV="sampled"; fi
    if [[ "$QPS_SET" -eq 0 && "${WK_BENCH_DELIVERY_QPS:-}" == "" ]]; then QPS_LIST="10"; fi
    if [[ "$CHANNELS_SET" -eq 0 && "${WK_BENCH_DELIVERY_CHANNELS:-}" == "" ]]; then CHANNELS="10"; fi
    if [[ "$USERS_SET" -eq 0 && "${WK_BENCH_DELIVERY_USERS:-}" == "" ]]; then USERS="100"; fi
    if [[ "$MEMBERS_SET" -eq 0 && "${WK_BENCH_DELIVERY_GROUP_MEMBERS:-}" == "" ]]; then GROUP_MEMBERS="10"; fi
    ;;
  person|group|group-large|saturation)
    ;;
  *)
    die "--scenario must be smoke, person, group, group-large, or saturation: $SCENARIO"
    ;;
esac

if [[ "$SCENARIO" == "group-large" && "$MEMBERS_SET" -eq 0 && "${WK_BENCH_DELIVERY_GROUP_MEMBERS:-}" == "" ]]; then
  GROUP_MEMBERS=200
fi
if [[ "$SCENARIO" == "saturation" && "$EVENT_QUEUE_SET" -eq 0 && "${WK_DELIVERY_EVENT_QUEUE_SIZE:-}" == "" ]]; then
  DELIVERY_EVENT_QUEUE_SIZE=64
fi
if [[ "$OUT_DIR_SET" -eq 0 && "${WK_BENCH_DELIVERY_OUT_DIR:-}" == "" ]]; then
  OUT_DIR="$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-delivery-${SCENARIO}"
fi

require_positive_int '--channels' "$CHANNELS"
require_positive_int '--users' "$USERS"
require_positive_int '--members' "$GROUP_MEMBERS"
require_positive_int '--concurrency' "$CONCURRENCY"
require_nonnegative_int '--profile-seconds' "$PROFILE_SECONDS"
require_nonnegative_int '--event-queue-size' "$DELIVERY_EVENT_QUEUE_SIZE"
require_nonnegative_int '--fanout-page-size' "$DELIVERY_FANOUT_PAGE_SIZE"
require_nonnegative_int '--push-batch-size' "$DELIVERY_PUSH_BATCH_SIZE"
require_nonnegative_int '--pending-ack-max' "$DELIVERY_PENDING_ACK_MAX_PER_SESSION"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"

case "$SENDER_PICK" in
  first_online|round_robin) ;;
  *) die "--sender-pick must be first_online or round_robin: $SENDER_PICK" ;;
esac
case "$VERIFY_RECV" in
  none|sampled|full) ;;
  *) die "--verify-recv must be none, sampled, or full: $VERIFY_RECV" ;;
esac
case "$RECV_ACK" in
  true|false) ;;
  *) die "--recv-ack must be true or false: $RECV_ACK" ;;
esac

declare -a QPS_VALUES API_VALUES GATEWAY_VALUES METRICS_VALUES
split_csv "$QPS_LIST" QPS_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

WORKER_PID=""
CLUSTER_PID=""

cleanup() {
  if [[ -n "$WORKER_PID" ]]; then
    log "stopping temporary worker pid=$WORKER_PID"
    curl -fsS -X POST "${WORKER_ADDR%/}/v1/stop" >/dev/null 2>&1 || true
    kill "$WORKER_PID" >/dev/null 2>&1 || true
    wait "$WORKER_PID" 2>/dev/null || true
  fi
  if [[ -n "$CLUSTER_PID" ]]; then
    log "stopping local three-node cluster pid=$CLUSTER_PID"
    kill "$CLUSTER_PID" >/dev/null 2>&1 || true
    wait "$CLUSTER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

metric_file_id() {
  local raw="$1"
  raw="${raw#http://}"
  raw="${raw#https://}"
  printf '%s' "$raw" | tr -c 'A-Za-z0-9' '_'
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

ensure_wkbench_binary() {
  if [[ -x "$WK_BENCH_BIN" ]]; then
    return
  fi
  log "building wkbench: $WK_BENCH_BIN"
  mkdir -p "$(dirname "$WK_BENCH_BIN")"
  (cd "$ROOT_DIR" && go build -o "$WK_BENCH_BIN" ./cmd/wkbench)
}

worker_ready() {
  curl -fsS --max-time 2 "${WORKER_ADDR%/}/healthz" >/dev/null 2>&1
}

ensure_worker() {
  if [[ "$START_WORKER" -eq 0 ]]; then
    log "worker startup disabled; scenario will reference $WORKER_ADDR"
    return
  fi
  if worker_ready; then
    log "using existing worker: $WORKER_ADDR"
    return
  fi
  ensure_wkbench_binary
  mkdir -p "$OUT_DIR/worker-state"
  log "starting temporary worker: $WORKER_LISTEN"
  "$WK_BENCH_BIN" worker --listen "$WORKER_LISTEN" --work-dir "$OUT_DIR/worker-state" --insecure-control >/dev/null 2>&1 &
  WORKER_PID="$!"
  local deadline=$((SECONDS + 15))
  while (( SECONDS <= deadline )); do
    if worker_ready; then
      return
    fi
    sleep 1
  done
  die "timed out waiting for wkbench worker at $WORKER_ADDR"
}

start_cluster() {
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    log "cluster startup disabled; using existing local three-node cluster"
    return
  fi
  [[ -x "$START_SCRIPT" ]] || die "start script is not executable: $START_SCRIPT"
  mkdir -p "$OUT_DIR/logs"
  local clean_arg=()
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    clean_arg=(--clean)
  fi
  log "starting local three-node cluster with delivery enabled"
  WK_DELIVERY_ENABLE="$DELIVERY_ENABLE" \
  WK_DELIVERY_EVENT_QUEUE_SIZE="$DELIVERY_EVENT_QUEUE_SIZE" \
  WK_DELIVERY_FANOUT_PAGE_SIZE="$DELIVERY_FANOUT_PAGE_SIZE" \
  WK_DELIVERY_PUSH_BATCH_SIZE="$DELIVERY_PUSH_BATCH_SIZE" \
  WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION="$DELIVERY_PENDING_ACK_MAX_PER_SESSION" \
  WK_DELIVERY_PENDING_ACK_TTL="$DELIVERY_PENDING_ACK_TTL" \
  WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}" \
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
      die "local three-node cluster exited before ready"
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
name: local-three-node-delivery
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
  - id: delivery-worker-a
    addr: $WORKER_ADDR
    weight: 1
    control_token: ""
    insecure_control: true
YAML
}

scenario_profile_name() {
  case "$SCENARIO" in
    person) printf 'delivery-person' ;;
    *) printf 'delivery-group' ;;
  esac
}

scenario_channel_type() {
  case "$SCENARIO" in
    person) printf 'person' ;;
    *) printf 'group' ;;
  esac
}

write_scenario() {
  local qps="$1"
  local tag="$2"
  local report_dir="$3"
  local rate profile channel_type traffic
  rate="$(rate_per_channel "$qps")"
  profile="$(scenario_profile_name)"
  channel_type="$(scenario_channel_type)"
  traffic="${profile}-send"

  mkdir -p "$OUT_DIR/scenarios" "$report_dir"
  cat >"$OUT_DIR/scenarios/scenario-${tag}.yaml" <<YAML
version: wkbench/v1
run:
  id: delivery-${SCENARIO}-${tag}
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
  soft:
    max_sendack_p99: $STABLE_P99
    max_recv_p99: 0s
identity:
  uid_prefix: delivery${tag}-u
  device_prefix: delivery${tag}-d
  client_msg_prefix: delivery${tag}-msg
  token:
    mode: bench_api
online:
  total_users: $USERS
  connect_rate: 1000/s
  gateway_balance: round_robin
  heartbeat:
    enabled: true
    interval: 30s
    timeout: 5s
channels:
  profiles:
    - name: $profile
      channel_type: $channel_type
      count: $CHANNELS
YAML
  if [[ "$channel_type" == "group" ]]; then
    cat >>"$OUT_DIR/scenarios/scenario-${tag}.yaml" <<YAML
      members:
        count: $GROUP_MEMBERS
        overlap: allowed
      online:
        member_ratio: 1
      shard:
        mode: hash
      prepare:
        subscribers_batch_size: 1000
YAML
  fi
  cat >>"$OUT_DIR/scenarios/scenario-${tag}.yaml" <<YAML
cleanup:
  enabled: false
messages:
  payload:
    size_bytes: $PAYLOAD_BYTES
    mode: deterministic
  traffic:
    - name: $traffic
      channel_ref: $profile
      rate_per_channel: ${rate}/s
      concurrency: $CONCURRENCY
      sender_pick: $SENDER_PICK
      recv_ack: $RECV_ACK
      verify:
        recv:
          mode: $VERIFY_RECV
YAML
  if [[ "$VERIFY_RECV" == "sampled" ]]; then
    cat >>"$OUT_DIR/scenarios/scenario-${tag}.yaml" <<'YAML'
          sample_size_per_message: 1
YAML
  fi
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
  local pprof_dir="$OUT_DIR/pprof/$phase"
  mkdir -p "$pprof_dir"
  local addr id
  for addr in "${API_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    curl -fsS "${addr%/}/debug/pprof/goroutine?debug=2" >"$pprof_dir/${id}-goroutine.txt" || true
    curl -fsS "${addr%/}/debug/pprof/heap" >"$pprof_dir/${id}-heap.pb.gz" || true
    if (( PROFILE_SECONDS > 0 )); then
      curl -fsS "${addr%/}/debug/pprof/profile?seconds=${PROFILE_SECONDS}" >"$pprof_dir/${id}-cpu.pb.gz" || true
    fi
  done
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
  die "duration currently supports ms, s, or m: $value"
}

write_delivery_summary() {
  local tag="$1"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/delivery-summary.tsv"
  if [[ ! -f "$out" ]]; then
    printf 'tag\tnode\tadmission_ok\tadmission_overflow\tadmission_error\tdelivery_errors\tretry_drop\tretry_overflow\tretry_queue_depth\tack_bindings\tresolve_routes\tpush_routes\n' >"$out"
  fi
  local addr id before after
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    awk -v tag="$tag" -v node="$id" '
      function labels(line, key, value) {
        return line ~ key "=\"" value "\""
      }
      FNR == NR {
        if ($1 ~ /^wukongim_delivery_event_queue_total/ && labels($1, "result", "ok")) b_admission_ok += $2
        if ($1 ~ /^wukongim_delivery_event_queue_total/ && labels($1, "result", "overflow")) b_admission_overflow += $2
        if ($1 ~ /^wukongim_delivery_event_queue_total/ && labels($1, "result", "error")) b_admission_error += $2
        if ($1 ~ /^wukongim_delivery_errors_total/) b_errors += $2
        if ($1 ~ /^wukongim_delivery_retry_total/ && labels($1, "event", "drop")) b_retry_drop += $2
        if ($1 ~ /^wukongim_delivery_retry_total/ && labels($1, "result", "overflow")) b_retry_overflow += $2
        if ($1 ~ /^wukongim_delivery_resolve_routes_total/) b_resolve_routes += $2
        if ($1 ~ /^wukongim_delivery_push_rpc_routes_total/) b_push_routes += $2
        next
      }
      {
        if ($1 ~ /^wukongim_delivery_event_queue_total/ && labels($1, "result", "ok")) admission_ok += $2
        if ($1 ~ /^wukongim_delivery_event_queue_total/ && labels($1, "result", "overflow")) admission_overflow += $2
        if ($1 ~ /^wukongim_delivery_event_queue_total/ && labels($1, "result", "error")) admission_error += $2
        if ($1 ~ /^wukongim_delivery_errors_total/) errors += $2
        if ($1 ~ /^wukongim_delivery_retry_total/ && labels($1, "event", "drop")) retry_drop += $2
        if ($1 ~ /^wukongim_delivery_retry_total/ && labels($1, "result", "overflow")) retry_overflow += $2
        if ($1 ~ /^wukongim_delivery_retry_queue_depth/) retry_queue_depth = $2
        if ($1 ~ /^wukongim_delivery_ack_bindings/) ack_bindings = $2
        if ($1 ~ /^wukongim_delivery_resolve_routes_total/) resolve_routes += $2
        if ($1 ~ /^wukongim_delivery_push_rpc_routes_total/) push_routes += $2
      }
      END {
        printf "%s\t%s\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\n",
          tag, node,
          admission_ok - b_admission_ok,
          admission_overflow - b_admission_overflow,
          admission_error - b_admission_error,
          errors - b_errors,
          retry_drop - b_retry_drop,
          retry_overflow - b_retry_overflow,
          retry_queue_depth,
          ack_bindings,
          resolve_routes - b_resolve_routes,
          push_routes - b_push_routes
      }
    ' "$before" "$after" >>"$out"
  done
}

write_run_metadata() {
  mkdir -p "$OUT_DIR/logs" "$OUT_DIR/reports" "$OUT_DIR/metrics" "$OUT_DIR/pprof"
  {
    echo "head=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
    echo "short=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
    git -C "$ROOT_DIR" status --short 2>/dev/null || true
  } >"$OUT_DIR/git.txt"
  cat >"$OUT_DIR/env.txt" <<EOF
SCENARIO=$SCENARIO
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
VERIFY_RECV=$VERIFY_RECV
RECV_ACK=$RECV_ACK
DELIVERY_ENABLE=$DELIVERY_ENABLE
DELIVERY_EVENT_QUEUE_SIZE=$DELIVERY_EVENT_QUEUE_SIZE
DELIVERY_FANOUT_PAGE_SIZE=$DELIVERY_FANOUT_PAGE_SIZE
DELIVERY_PUSH_BATCH_SIZE=$DELIVERY_PUSH_BATCH_SIZE
DELIVERY_PENDING_ACK_MAX_PER_SESSION=$DELIVERY_PENDING_ACK_MAX_PER_SESSION
DELIVERY_PENDING_ACK_TTL=$DELIVERY_PENDING_ACK_TTL
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
EOF
  if [[ -x "$START_SCRIPT" ]]; then
    WK_DELIVERY_ENABLE="$DELIVERY_ENABLE" \
    WK_DELIVERY_EVENT_QUEUE_SIZE="$DELIVERY_EVENT_QUEUE_SIZE" \
    WK_DELIVERY_FANOUT_PAGE_SIZE="$DELIVERY_FANOUT_PAGE_SIZE" \
    WK_DELIVERY_PUSH_BATCH_SIZE="$DELIVERY_PUSH_BATCH_SIZE" \
    WK_DELIVERY_PENDING_ACK_MAX_PER_SESSION="$DELIVERY_PENDING_ACK_MAX_PER_SESSION" \
    WK_DELIVERY_PENDING_ACK_TTL="$DELIVERY_PENDING_ACK_TTL" \
    WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}" \
      "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
}

write_attempt_report_summary() {
  local tag="$1"
  local qps="$2"
  local report_dir="$3"
  local exit_status="$4"
  local duration
  duration="$(duration_seconds "$DURATION")"
  if [[ ! -f "$OUT_DIR/report-summary.tsv" ]]; then
    printf 'tag\tqps\tstatus\texit_status\tactual_qps\tsend_success\tsend_errors\tconnect_error_rate\tsendack_error_rate\trecv_verify_error_rate\tp99_seconds\n' >"$OUT_DIR/report-summary.tsv"
  fi
  if [[ ! -f "$report_dir/report.json" ]]; then
    printf '%s\t%s\tmissing_report\t%s\t0\t0\t0\t0\t0\t0\t0\n' "$tag" "$qps" "$exit_status" >>"$OUT_DIR/report-summary.tsv"
    return
  fi
  local profile traffic metric_prefix
  profile="$(scenario_profile_name)"
  traffic="${profile}-send"
  if [[ "$(scenario_channel_type)" == "person" ]]; then
    metric_prefix="person_send"
  else
    metric_prefix="group_send"
  fi
  jq -r --arg tag "$tag" --arg qps "$qps" --arg exit_status "$exit_status" --arg duration "$duration" --arg metric_prefix "$metric_prefix" --arg profile "$profile" --arg traffic "$traffic" '
    ($metric_prefix + "_success_total{channel_type=" + (if $metric_prefix == "person_send" then "person" else "group" end) + ",phase=run,profile=" + $profile + ",traffic=" + $traffic + "}") as $success_key
    | ($metric_prefix + "_error_total{channel_type=" + (if $metric_prefix == "person_send" then "person" else "group" end) + ",phase=run,profile=" + $profile + ",traffic=" + $traffic + "}") as $error_key
    | ($metric_prefix + "_latency_seconds{channel_type=" + (if $metric_prefix == "person_send" then "person" else "group" end) + ",phase=run,profile=" + $profile + ",traffic=" + $traffic + "}") as $hist_key
    | ($duration | tonumber) as $duration_seconds
    | (.metrics.counters[$success_key] // 0) as $success
    | (.metrics.counters[$error_key] // 0) as $errors
    | (.metrics.histograms[$hist_key] // {}) as $h
    | [
        $tag,
        $qps,
        .status,
        $exit_status,
        (if $duration_seconds > 0 then ($success / $duration_seconds) else 0 end),
        $success,
        $errors,
        (.summary.connect_error_rate // 0),
        (.summary.sendack_error_rate // 0),
        (.summary.recv_verify_error_rate // 0),
        ($h.p99_seconds // 0)
      ] | @tsv
  ' "$report_dir/report.json" >>"$OUT_DIR/report-summary.tsv"
}

run_attempt() {
  local qps="$1"
  local tag report_dir exit_status
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}"
  mkdir -p "$report_dir"
  write_scenario "$qps" "$tag" "$report_dir"

  log "running delivery scenario=$SCENARIO qps=$qps tag=$tag"
  scrape_metrics "$tag" before
  exit_status=0
  "$WK_BENCH_BIN" run \
    --target "$OUT_DIR/target.yaml" \
    --scenario "$OUT_DIR/scenarios/scenario-${tag}.yaml" \
    --workers "$OUT_DIR/workers.yaml" \
    --phase-poll-timeout "$PHASE_POLL_TIMEOUT" \
    >"$report_dir/wkbench-console.txt" 2>&1 || exit_status=$?
  scrape_metrics "$tag" after
  write_delivery_summary "$tag"
  write_attempt_report_summary "$tag" "$qps" "$report_dir" "$exit_status"
}

delivery_gate_status() {
  awk '
    NR == 1 { next }
    {
      if ($4 + 0 > 0) failed = 1
      if ($5 + 0 > 0) failed = 1
      if ($6 + 0 > 0) failed = 1
      if ($7 + 0 > 0) failed = 1
      if ($8 + 0 > 0) failed = 1
      if ($9 + 0 > 0) failed = 1
      if ($10 + 0 > 0) failed = 1
    }
    END { if (failed) print "failed"; else print "passed" }
  ' "$OUT_DIR/delivery-summary.tsv"
}

report_gate_status() {
  awk '
    NR == 1 { next }
    {
      if ($3 != "passed") failed = 1
      if ($4 + 0 != 0) failed = 1
      if ($7 + 0 != 0) failed = 1
      if ($8 + 0 != 0) failed = 1
      if ($9 + 0 != 0) failed = 1
      if ($10 + 0 != 0) failed = 1
    }
    END { if (failed) print "failed"; else print "passed" }
  ' "$OUT_DIR/report-summary.tsv"
}

write_evidence_summary() {
  local delivery_status report_status status
  delivery_status="$(delivery_gate_status)"
  report_status="$(report_gate_status)"
  status="passed"
  if [[ "$delivery_status" != "passed" || "$report_status" != "passed" ]]; then
    status="failed"
  fi
  cat >"$OUT_DIR/summary.md" <<EOF
# Delivery Bench Summary

- scenario: $SCENARIO
- status: $status
- delivery_status: $delivery_status
- report_status: $report_status
- delivery_summary: delivery-summary.tsv
- report_summary: report-summary.tsv
- reports: reports/
- metrics: metrics/
- pprof: pprof/
- logs: logs/
- git: git.txt
- env: env.txt
EOF
  [[ "$status" == "passed" ]]
}

main() {
  mkdir -p "$OUT_DIR"
  ensure_wkbench_binary
  start_cluster
  check_cluster_ready
  ensure_worker
  write_target_and_workers
  write_run_metadata
  collect_node_logs before
  capture_node_pprof before

  local qps
  for qps in "${QPS_VALUES[@]}"; do
    run_attempt "$qps"
  done

  collect_node_logs after
  capture_node_pprof after
  if write_evidence_summary; then
    log "delivery bench passed: $OUT_DIR"
  else
    log "delivery bench failed: $OUT_DIR"
    exit 7
  fi
}

main "$@"
