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
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongimv2-three-nodes.sh}"
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
ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"
RECV_ACK="${WK_BENCH_RECV_ACK:-true}"
PROFILE_SECONDS="${WK_BENCH_PROFILE_SECONDS:-0}"
SENDER_PICK="${WK_BENCH_SENDER_PICK:-first_online}"
PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongimv2-three-nodes-1000ch.sh [options]

Starts a local cmd/wukongimv2 three-node cluster, then runs fixed multi-channel
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
  --start-script PATH    Three-node startup script. Default: scripts/start-wukongimv2-three-nodes.sh.
  --ready-timeout SECS   Cluster ready wait timeout. Default: 90.
  --channels N           Fixed group channel count. Default: 1000.
  --users N              Online user pool. Default: 4096.
  --members N            Members per group channel. Default: 10.
  --concurrency N        wkbench send concurrency. Default: 2800.
  --duration DURATION    Measured run duration. Default: 15s.
  --warmup DURATION      Warmup duration. Default: 5s.
  --cooldown DURATION    Cooldown duration. Default: 2s.
  --stable-p99 DURATION  Soft p99 gate written into scenarios. Default: 400ms.
  --ack-timeout DURATION Per-SEND sendack wait timeout in generated traffic. Default: 15s.
  --phase-poll-timeout DURATION
                         Base wkbench worker phase poll timeout. Default: 30s.
  --profile-seconds N    Capture final CPU pprof for each node when N > 0. Default: 0.
  --recv-ack BOOL        Whether drained group recv frames are acknowledged. Default: true.
  --sender-pick MODE     Group sender selection: first_online or round_robin. Default: first_online.
  --api LIST             Comma-separated API base URLs. Default: node 5011/5012/5013.
  --gateway LIST         Comma-separated WKProto gateway addresses. Default: 5111/5112/5113.
  --metrics LIST         Comma-separated metrics base URLs. Default: same as --api.
  -h, --help             Show this help.

Example:
  scripts/bench-wukongimv2-three-nodes-1000ch.sh --qps 2000,2400,2500

  # Reuse an already-running cluster:
  scripts/bench-wukongimv2-three-nodes-1000ch.sh --no-start --qps 2000,2500
USAGE
}

log() {
  printf '[bench-three-1000ch] %s\n' "$*"
}

die() {
  printf '[bench-three-1000ch] ERROR: %s\n' "$*" >&2
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
[[ "$PROFILE_SECONDS" =~ ^[0-9]+$ ]] || die "--profile-seconds must be a non-negative integer: $PROFILE_SECONDS"
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
  # Preserve synchronous commits; the wider window only improves durable group-commit batching.
  # Keep server send timeout below the 15s client ACK wait so recovery can still write SENDACK.
  WK_PPROF_ENABLE="${WK_PPROF_ENABLE:-true}" \
  WK_CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-3}" \
  WK_CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-96}" \
  WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-128}" \
  WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}" \
  WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT="${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}" \
  WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1100us}" \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}" \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}" \
  WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES="${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-0}" \
  WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS="${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS:-1024}" \
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
  local rate
  rate="$(rate_per_channel "$qps")"
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
    enabled: true
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

collect_node_logs() {
  local phase="$1"
  local dest="$OUT_DIR/logs/$phase"
  mkdir -p "$dest"
  cp "$ROOT_DIR"/data/wukongimv2-three-node-logs/node*.log "$dest/" 2>/dev/null || true
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

channelv2_metrics_summary() {
  local tag="$1"
  local duration="$2"
  local metrics_dir="$OUT_DIR/metrics/$tag"
  local out="$OUT_DIR/channelv2_metrics_summary.tsv"
  local summarizer="$ROOT_DIR/scripts/channelv2-metrics-summary.awk"
  local addr id before after
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    [[ -f "$before" && -f "$after" ]] || continue
    awk -v tag="$tag" -v node="$id" -v duration="$duration" -f "$summarizer" "$before" "$after" >>"$out" || true
  done
}

run_attempt() {
  local qps="$1"
  local tag report_dir exit_status duration
  tag="$(qps_tag "$qps")"
  report_dir="$OUT_DIR/reports/${tag}-qps"
  duration="$(duration_seconds "$DURATION")"
  mkdir -p "$report_dir"

  write_scenario "$qps" "$tag" "$report_dir"
  curl -fsS -X POST "${WORKER_ADDR%/}/v1/stop" >/dev/null 2>&1 || true

  log "running qps=$qps tag=$tag"
  scrape_metrics "$tag" before
  exit_status=0
  "$WK_BENCH_BIN" run \
    --target "$OUT_DIR/target.yaml" \
    --scenario "$OUT_DIR/scenario-${tag}.yaml" \
    --workers "$OUT_DIR/workers.yaml" \
    --phase-poll-timeout "$PHASE_POLL_TIMEOUT" \
    >"$report_dir/wkbench-console.txt" 2>&1 || exit_status=$?
  scrape_metrics "$tag" after
  classify_metrics "$tag"
  rpc_pull_qps_summary "$tag" "$duration"
  channelv2_metrics_summary "$tag" "$duration"

  if [[ ! -f "$report_dir/report.json" ]]; then
    printf '%s\t%s\tmissing_report\t%s\t0\t0\t0\t0\t0\t0\t0\t0\t0\n' "$tag" "$qps" "$exit_status" >>"$OUT_DIR/summary.tsv"
    return
  fi
  jq -r --arg tag "$tag" --arg qps "$qps" --arg exit_status "$exit_status" --arg duration "$duration" '
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
        ($h.max_seconds // 0)
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
CHANNELS=$CHANNELS
USERS=$USERS
GROUP_MEMBERS=$GROUP_MEMBERS
CONCURRENCY=$CONCURRENCY
PAYLOAD_BYTES=$PAYLOAD_BYTES
DURATION=$DURATION
WARMUP=$WARMUP
COOLDOWN=$COOLDOWN
STABLE_P99=$STABLE_P99
ACK_TIMEOUT=$ACK_TIMEOUT
RECV_ACK=$RECV_ACK
PHASE_POLL_TIMEOUT=$PHASE_POLL_TIMEOUT
SENDER_PICK=$SENDER_PICK
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
WORKER_ADDR=$WORKER_ADDR
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
CLUSTER_CHANNEL_REACTOR_COUNT=${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-128}
CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}
CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}
CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1100us}
CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}
CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}
CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-0}
CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}
GATEWAY_ASYNC_SEND_DISPATCH_WORKERS=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_DISPATCH_WORKERS:-1024}
GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}
GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
PROFILE_SECONDS=$PROFILE_SECONDS
EOF
  mkdir -p "$OUT_DIR/config"
  cp "$ROOT_DIR"/scripts/wukongimv2/wukongimv2-node*.conf "$OUT_DIR/config/" 2>/dev/null || true
  if [[ -x "$START_SCRIPT" ]]; then
    "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
  collect_node_logs before
  scrape_metrics_snapshot before
  capture_node_pprof before
}

write_display_summary() {
  local p99_limit
  p99_limit="$(duration_seconds "$STABLE_P99")"
  awk -v rpc_file="$OUT_DIR/rpc_pull_qps.tsv" -v p99_limit="$p99_limit" '
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

      print "# bench result"
      printf "p99 gate: <= %.0f ms, send_errors: 0\n\n", p99_limit * 1000
      printf "%-9s %10s %10s %8s %8s %9s %9s %12s %s\n", "offered", "actual", "result", "errors", "p99ms", "p95ms", "maxms", "rpc_pull/s", "note"
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
      if (result == "PASS" && actual > best_actual) {
        best_actual = actual
        best_offered = offered
        best_p99 = p99
        best_rpc = rpc_qps[tag]
      }
      printf "%-9.0f %10.1f %10s %8.0f %8.1f %9.1f %9.1f %12.1f %s\n", offered, actual, result, errors, p99 * 1000, p95 * 1000, max * 1000, rpc_qps[tag], note
    }
    END {
      print ""
      if (best_actual > 0) {
        printf "best pass: offered=%.0f actual=%.1f qps p99=%.1fms rpc_pull/s=%.1f\n", best_offered, best_actual, best_p99 * 1000, best_rpc
      } else {
        print "best pass: none"
      }
    }
  ' "$OUT_DIR/summary.tsv" >"$OUT_DIR/summary.txt"
}

print_summary() {
  write_display_summary
  write_evidence_summary
  cat "$OUT_DIR/summary.txt"
  log "details:"
  printf '  summary: %s\n' "$OUT_DIR/summary.tsv"
  printf '  rpc_pull: %s\n' "$OUT_DIR/rpc_pull_qps.tsv"
  printf '  channelv2: %s\n' "$OUT_DIR/channelv2_metrics_summary.tsv"
  printf '  reports: %s\n' "$OUT_DIR/reports"
  printf '  metrics: %s\n' "$OUT_DIR/metrics"
}

write_evidence_summary() {
  local best_line
  best_line="$(tail -n 1 "$OUT_DIR/summary.txt" 2>/dev/null || true)"
  cat >"$OUT_DIR/summary.md" <<EOF
# Three-Node Bench Evidence

## Scenario
- workload: local wukongimv2 three-node wkbench group channels
- channels: $CHANNELS
- users: $USERS
- group_members: $GROUP_MEMBERS
- qps_list: $QPS_LIST
- duration: $DURATION
- clean_cluster: $CLEAN_CLUSTER

## Evidence
- git: git.txt
- env: env.txt
- start_plan: start-plan.txt
- config: config/
- metrics: metrics/
- pprof: pprof/
- logs: logs/
- reports: reports/
- rpc_pull: rpc_pull_qps.tsv
- channelv2_metrics: channelv2_metrics_summary.tsv
- summary_tsv: summary.tsv

## Result
${best_line:-best pass: none}
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

  cat >"$OUT_DIR/summary.tsv" <<'EOF'
tag	offered_qps	status	exit_status	actual_qps	send_success	send_errors	connect_error_rate	sendack_error_rate	p50_seconds	p95_seconds	p99_seconds	max_seconds
EOF
  cat >"$OUT_DIR/rpc_pull_qps.tsv" <<'EOF'
tag	node	rpc_pull_delta	rpc_pull_qps
EOF
  cat >"$OUT_DIR/channelv2_metrics_summary.tsv" <<'EOF'
tag	node	active_total	active_leader	active_follower	follower_parked	mailbox_depth_max	worker_queue_depth_max	activation_rejected_delta	recovery_probe_submitted_delta	recovery_probe_ok_delta	recovery_probe_err_delta	pull_ok_nonempty_delta	pull_ok_empty_delta	pull_err_delta	rpc_pull_ok_delta	rpc_pull_err_delta	rpc_pull_qps	meta_cache_hit_delta	meta_cache_miss_delta	meta_cache_invalidate_delta	append_count_delta	append_avg_ms	append_batch_count_delta	append_batch_avg_records	append_batch_avg_bytes	append_batch_wait_avg_ms	worker_task_count_delta	worker_task_avg_ms
EOF

  local qps
  for qps in "${QPS_VALUES[@]}"; do
    [[ "$qps" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "invalid qps value: $qps"
    run_attempt "$qps"
  done

  collect_node_logs after
  scrape_metrics_snapshot after
  capture_node_pprof after
  print_summary
}

main "$@"
