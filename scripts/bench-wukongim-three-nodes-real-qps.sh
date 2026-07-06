#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_REAL_QPS_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"

QPS_LIST="${WK_BENCH_REAL_QPS_LIST:-2000,2400,2600,2800,3000}"
OUT_DIR="${WK_BENCH_REAL_QPS_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-real-qps}"
BASE_SCRIPT="${WK_BENCH_REAL_QPS_BASE_SCRIPT:-$ROOT_DIR/scripts/bench-wukongim-three-nodes-1000ch.sh}"
MIN_ACTUAL_RATIO="${WK_BENCH_MIN_ACTUAL_RATIO:-0.95}"
SENDER_PICK="${WK_BENCH_SENDER_PICK:-round_robin}"

WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-test}"
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-three-nodes.sh}"
READY_TIMEOUT="${WK_BENCH_THREE_NODE_READY_TIMEOUT:-90}"

CHANNELS="${WK_BENCH_CHANNELS:-1000}"
USERS="${WK_BENCH_USERS:-4096}"
GROUP_MEMBERS="${WK_BENCH_GROUP_MEMBERS:-10}"
CONCURRENCY="${WK_BENCH_CONCURRENCY:-2800}"
PAYLOAD_BYTES="${WK_BENCH_PAYLOAD_BYTES:-128}"
DURATION="${WK_BENCH_DURATION:-30s}"
WARMUP="${WK_BENCH_WARMUP:-10s}"
COOLDOWN="${WK_BENCH_COOLDOWN:-3s}"
STABLE_P99="${WK_BENCH_STABLE_P99:-400ms}"
ACK_TIMEOUT="${WK_BENCH_ACK_TIMEOUT:-15s}"
RECV_ACK="${WK_BENCH_RECV_ACK:-true}"
HEARTBEAT_ENABLED="${WK_BENCH_HEARTBEAT_ENABLED:-true}"
PROFILE_SECONDS="${WK_BENCH_PROFILE_SECONDS:-0}"
PHASE_POLL_TIMEOUT="${WK_BENCH_PHASE_POLL_TIMEOUT:-30s}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-three-nodes-real-qps.sh [options]

Runs a true QPS search shape for local cmd/wukongim three-node group traffic.
Each offered QPS value is measured as an independent clean single-attempt run,
then summarized into one parent evidence directory.

Options:
  --qps LIST                 Comma-separated offered QPS list. Default: 2000,2400,2600,2800,3000.
  --out-dir DIR              Parent evidence output directory.
  --sender-pick MODE         Group sender selection: round_robin or first_online. Default: round_robin.
  --min-actual-ratio FLOAT   Required actual/offered ratio for PASS. Default: 0.95.
  --channels N               Fixed group channel count. Default: 1000.
  --channel-count N          Alias for --channels.
  --users N                  Online user pool. Default: 4096.
  --members N                Members per group channel. Default: 10.
  --concurrency N            wkbench send concurrency. Default: 2800.
  --payload-bytes N          Message payload bytes. Default: 128.
  --duration DURATION        Measured run duration. Default: 30s.
  --warmup DURATION          Warmup duration. Default: 10s.
  --cooldown DURATION        Cooldown duration. Default: 3s.
  --stable-p99 DURATION      p99 latency gate. Default: 400ms.
  --ack-timeout DURATION     Per-SEND sendack wait timeout. Default: 15s.
  --phase-poll-timeout DURATION
                             Base wkbench worker phase poll timeout. Default: 30s.
  --recv-ack BOOL            Whether group recv frames are acknowledged. Default: true.
  --heartbeat BOOL           Whether benchmark clients send heartbeat pings. Default: true.
  --profile-seconds N        Capture final CPU pprof for each node when N > 0. Default: 0.
  --wkbench-bin PATH         wkbench binary path. Default: data/wkbench-test.
  --start-script PATH        Three-node startup script. Default: scripts/start-wukongim-three-nodes.sh.
  --ready-timeout SECS       Cluster ready wait timeout. Default: 90.
  --api LIST                 Comma-separated API base URLs. Default: node 5011/5012/5013.
  --gateway LIST             Comma-separated WKProto gateway addresses. Default: 5111/5112/5113.
  --metrics LIST             Comma-separated metrics base URLs. Default: same as --api.
  -h, --help                 Show this help.

Example:
  GOWORK=off scripts/bench-wukongim-three-nodes-real-qps.sh --qps 2400,2600,2800

Notes:
  - The child runner cleans and restarts the cluster for every QPS value.
  - The script defaults to GOWORK=off when GOWORK is unset, which avoids a
    broken parent go.work from invalidating local-only benchmark runs.
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
  printf '%s[bench-real-qps]%s %s\n' "$C_CYAN" "$C_RESET" "$*"
}

die() {
  printf '%s[bench-real-qps] ERROR:%s %s\n' "$C_RED" "$C_RESET" "$*" >&2
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
    item="${item//[[:space:]]/}"
    [[ -n "$item" ]] || die "comma-separated list contains an empty item: $raw"
    eval "$var_name+=(\"\$item\")"
  done
}

qps_tag() {
  local qps="$1"
  if [[ "$qps" =~ ^[0-9]+$ ]]; then
    printf '%06d' "$qps"
    return
  fi
  printf '%s' "$qps" | tr '.' 'p'
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
  die "duration currently supports milliseconds, seconds, or minutes only: $value"
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
    --sender-pick)
      [[ $# -ge 2 ]] || die '--sender-pick requires a value'
      SENDER_PICK="$2"
      shift 2
      ;;
    --min-actual-ratio)
      [[ $# -ge 2 ]] || die '--min-actual-ratio requires a value'
      MIN_ACTUAL_RATIO="$2"
      shift 2
      ;;
    --channels)
      [[ $# -ge 2 ]] || die '--channels requires a value'
      CHANNELS="$2"
      shift 2
      ;;
    --channel-count)
      [[ $# -ge 2 ]] || die '--channel-count requires a value'
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
    --payload-bytes)
      [[ $# -ge 2 ]] || die '--payload-bytes requires a value'
      PAYLOAD_BYTES="$2"
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
    --wkbench-bin)
      [[ $# -ge 2 ]] || die '--wkbench-bin requires a value'
      WK_BENCH_BIN="$2"
      shift 2
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
require_positive_int '--payload-bytes' "$PAYLOAD_BYTES"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
[[ "$PROFILE_SECONDS" =~ ^[0-9]+$ ]] || die "--profile-seconds must be a non-negative integer: $PROFILE_SECONDS"
[[ "$MIN_ACTUAL_RATIO" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "--min-actual-ratio must be a non-negative number: $MIN_ACTUAL_RATIO"
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
[[ -x "$BASE_SCRIPT" ]] || die "base script is not executable: $BASE_SCRIPT"

declare -a QPS_VALUES
split_csv "$QPS_LIST" QPS_VALUES

write_metadata() {
  mkdir -p "$OUT_DIR"
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
HEARTBEAT_ENABLED=$HEARTBEAT_ENABLED
PHASE_POLL_TIMEOUT=$PHASE_POLL_TIMEOUT
MIN_ACTUAL_RATIO=$MIN_ACTUAL_RATIO
SENDER_PICK=$SENDER_PICK
CHANNEL_APPEND_SHARD_COUNT=${WK_CHANNEL_APPEND_SHARD_COUNT:-0}
CHANNEL_APPEND_ADVANCE_POOL_SIZE=${WK_CHANNEL_APPEND_ADVANCE_POOL_SIZE:-0}
CHANNEL_APPEND_EFFECT_POOL_SIZE=${WK_CHANNEL_APPEND_EFFECT_POOL_SIZE:-0}
CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY=${WK_CHANNEL_APPEND_RECIPIENT_AUTHORITY_DISPATCH_CONCURRENCY:-0}
TOP_API_ENABLE=${WK_TOP_API_ENABLE:-false}
CLUSTER_CHANNEL_REACTOR_COUNT=${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-128}
CLUSTER_CHANNEL_STORE_APPEND_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}
CLUSTER_CHANNEL_STORE_APPLY_WORKERS=${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}
CLUSTER_CHANNEL_RPC_WORKERS=${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}
CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_RECORDS:-128}
CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT=${WK_CLUSTER_CHANNEL_APPEND_BATCH_MAX_WAIT:-250us}
CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}
CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_REQUESTS:-0}
CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_RECORDS:-0}
CLUSTER_COMMIT_COORDINATOR_MAX_BYTES=${WK_CLUSTER_COMMIT_COORDINATOR_MAX_BYTES:-131072}
CLUSTER_COMMIT_COORDINATOR_SHARDS=${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-8}
CLUSTER_COMMIT_COORDINATOR_SYNC=${WK_CLUSTER_COMMIT_COORDINATOR_SYNC:-true}
GATEWAY_ASYNC_SEND_WORKERS=${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-2048}
GATEWAY_ASYNC_SEND_BATCH_MAX_WAIT=${WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_WAIT:-500us}
GATEWAY_SEND_TIMEOUT=${WK_GATEWAY_SEND_TIMEOUT:-14s}
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
WK_BENCH_BIN=$WK_BENCH_BIN
BASE_SCRIPT=$BASE_SCRIPT
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
PROFILE_SECONDS=$PROFILE_SECONDS
GOWORK=${GOWORK:-off}
EOF
}

runtime_pool_attempt_summary() {
  local metrics="$1"
  if [[ ! -f "$metrics" ]]; then
    printf '0\t0.000\t0\t0.000\t0\t0.000\t0\t0\t0\t0\n'
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
    function max_value(current, value) {
      if (value > current) {
        return value
      }
      return current
    }
    NR == 1 {
      for (i = 1; i <= NF; i++) {
        idx[$i] = i
      }
      next
    }
    {
      queue_depth = max_value(queue_depth, metric_value("runtime_pool_queue_depth_max"))
      queue_fill = max_value(queue_fill, metric_value("runtime_pool_queue_fill_max"))
      queue_bytes = max_value(queue_bytes, metric_value("runtime_pool_queue_bytes_max"))
      queue_bytes_fill = max_value(queue_bytes_fill, metric_value("runtime_pool_queue_bytes_fill_max"))
      inflight = max_value(inflight, metric_value("runtime_pool_inflight_max"))
      inflight_util = max_value(inflight_util, metric_value("runtime_pool_inflight_util_max"))
      admission_full += metric_value("runtime_pool_admission_full_delta")
      admission_busy += metric_value("runtime_pool_admission_busy_delta")
      admission_dirty += metric_value("runtime_pool_admission_dirty_delta")
      admission_requeued += metric_value("runtime_pool_admission_requeued_delta")
    }
    END {
      printf "%.0f\t%.3f\t%.0f\t%.3f\t%.0f\t%.3f\t%.0f\t%.0f\t%.0f\t%.0f\n",
        queue_depth,
        queue_fill,
        queue_bytes,
        queue_bytes_fill,
        inflight,
        inflight_util,
        admission_full,
        admission_busy,
        admission_dirty,
        admission_requeued
    }
  ' "$metrics"
}

channelappend_attempt_summary() {
  local metrics="$1"
  if [[ ! -f "$metrics" ]]; then
    printf '0\t0\t0\t0\t0\t0\t0\t0.000\t0.000\t0\t0\t0.000\t0.000\n'
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
    function max_value(current, value) {
      if (value > current) {
        return value
      }
      return current
    }
    NR == 1 {
      for (i = 1; i <= NF; i++) {
        idx[$i] = i
      }
      next
    }
    {
      router_total += metric_value("router_total_delta")
      router_error += metric_value("router_error_delta")
      router_backpressured += metric_value("router_backpressured_delta")
      router_channel_busy += metric_value("router_channel_busy_delta")
      router_route_not_ready += metric_value("router_route_not_ready_delta")
      router_timeout += metric_value("router_timeout_delta")
      local_rejected += metric_value("local_admission_rejected_delta")
      mailbox_fill = max_value(mailbox_fill, metric_value("mailbox_fill_max"))
      effect_slots_capacity = metric_value("effect_slots_capacity_max")
      if (effect_slots_capacity > 0) {
        effect_slots_fill = max_value(effect_slots_fill, metric_value("effect_slots_max") / effect_slots_capacity)
      }
      post_commit_backlog = max_value(post_commit_backlog, metric_value("post_commit_backlog_max"))
      effect_error += metric_value("effect_error_delta")
      effect_worker_util = max_value(effect_worker_util, metric_value("effect_worker_util_max"))
      effect_queue_fill = max_value(effect_queue_fill, metric_value("effect_queue_fill_max"))
    }
    END {
      printf "%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.0f\t%.3f\t%.3f\t%.0f\t%.0f\t%.3f\t%.3f\n",
        router_total,
        router_error,
        router_backpressured,
        router_channel_busy,
        router_route_not_ready,
        router_timeout,
        local_rejected,
        mailbox_fill,
        effect_slots_fill,
        post_commit_backlog,
        effect_error,
        effect_worker_util,
        effect_queue_fill
    }
  ' "$metrics"
}

filter_child_bench_result_display() {
  awk '
    /^BENCH RESULT$/ {
      skip = 1
      next
    }
    skip && /^SERVER PROCESS PEAKS$/ {
      skip = 0
    }
    !skip {
      print
    }
  '
}

write_runtime_pool_pressure_summary() {
  local out="$OUT_DIR/runtime_pool_pressure_summary.tsv"
  local qps tag attempt_dir file line
  cat >"$out" <<'EOF'
offered_qps	attempt_dir	tag	node	component	pool	queue	priority	queue_depth_max	queue_capacity	queue_fill_max	queue_bytes_max	queue_bytes_capacity	queue_bytes_fill_max	inflight_max	workers	inflight_util_max	admission_full_delta	admission_busy_delta	admission_dirty_delta	admission_requeued_delta	reason
EOF
  for qps in "${QPS_VALUES[@]}"; do
    tag="$(qps_tag "$qps")"
    attempt_dir="$OUT_DIR/${tag}-qps"
    file="$attempt_dir/runtime_pool_pressure_summary.tsv"
    [[ -f "$file" ]] || continue
    while IFS= read -r line; do
      [[ "$line" == tag$'\t'* ]] && continue
      [[ -n "$line" ]] || continue
      printf '%s\t%s\t%s\n' "$qps" "$attempt_dir" "$line" >>"$out"
    done <"$file"
  done
}

write_ants_pool_usage_summary() {
  local out="$OUT_DIR/ants_pool_usage_summary.tsv"
  local qps tag attempt_dir file line
  cat >"$out" <<'EOF'
offered_qps	attempt_dir	tag	node	component	pool	running	capacity	waiting	utilization_max
EOF
  for qps in "${QPS_VALUES[@]}"; do
    tag="$(qps_tag "$qps")"
    attempt_dir="$OUT_DIR/${tag}-qps"
    file="$attempt_dir/ants_pool_usage_summary.tsv"
    [[ -f "$file" ]] || continue
    while IFS= read -r line; do
      [[ "$line" == tag$'\t'* ]] && continue
      [[ -n "$line" ]] || continue
      printf '%s\t%s\t%s\n' "$qps" "$attempt_dir" "$line" >>"$out"
    done <"$file"
  done
}

append_ants_pool_usage_display() {
  local file="$1"
  printf '\n%s# ants pool usage%s\n' "$C_BOLD" "$C_RESET"
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
      offered = $1 + 0
      node = $4
      remember_node(node)
      pool = display_pool($5, $6)
      running = $7 + 0
      capacity = $8 + 0
      waiting = $9 + 0
      util = $10 + 0

      row_offered[entries] = offered
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
        max_offered[node] = offered
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
        printf "  %s%-9s %-28s %12s %10s %8s%s\n", c_dim, "offered", "pool", "used/cap", "util", "waiting", c_reset
        for (i = 1; i <= entries; i++) {
          if (row_node[i] != node) {
            continue
          }
          util_color = (row_util[i] >= 0.8) ? c_yellow : ""
          printf "  %-9.0f %-28s %12s %s%10.3f%s %8.0f\n",
            row_offered[i], row_pool[i],
            sprintf("%.0f/%.0f", row_running[i], row_capacity[i]),
            util_color, row_util[i], (util_color != "") ? c_reset : "",
            row_waiting[i]
        }
      }
    }
  ' "$file"
}

append_real_qps_result_display() {
  local file="$1"
  local p99_limit
  p99_limit="$(duration_seconds "$STABLE_P99")"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'BENCH RESULT\n'
    printf '%s\n' '────────────'
    printf 'none\n'
    return
  fi
  awk -F'\t' -v p99_limit="$p99_limit" -v actual_min_ratio="$MIN_ACTUAL_RATIO" \
    -v c_bold="$C_BOLD" -v c_reset="$C_RESET" -v c_green="$C_GREEN" \
    -v c_red="$C_RED" -v c_dim="$C_DIM" -v c_yellow="$C_YELLOW" '
    function attempt_rpc_qps(attempt_dir, file, line, parts, total) {
      file = attempt_dir "/rpc_pull_qps.tsv"
      total = 0
      while ((getline line < file) > 0) {
        split(line, parts, "\t")
        if (parts[1] == "tag") {
          continue
        }
        total += parts[4] + 0
      }
      close(file)
      return total
    }
    BEGIN {
      printf "%sBENCH RESULT%s\n", c_bold, c_reset
      print "────────────"
      printf "p99 gate: <= %.0f ms │ send_errors: 0\n", p99_limit * 1000
      printf "actual/offered gate: >= %.2f\n\n", actual_min_ratio
      printf "%s%9s %10s %7s %8s %8s %8s %8s %8s %12s %s%s\n", c_dim, "offered", "actual", "ratio", "result", "errors", "p99ms", "p95ms", "maxms", "rpc_pull/s", "note", c_reset
    }
    NR == 1 { next }
    {
      offered = $2 + 0
      attempt_dir = $3
      actual = $7 + 0
      ratio = $6 + 0
      errors = $9 + 0
      p95 = $13 + 0
      p99 = $14 + 0
      max = $15 + 0
      result = $39
      note = $40
      rpc_qps = attempt_rpc_qps(attempt_dir)
      if (result == "PASS" && actual > best_actual) {
        best_actual = actual
        best_offered = offered
        best_p99 = p99
        best_rpc = rpc_qps
      }
      if (result == "PASS") {
        result_str = c_green "    PASS" c_reset
      } else {
        result_str = c_red "    FAIL" c_reset
      }
      printf "%9.0f %10.1f %7.3f %s %8.0f %8.1f %8.1f %8.1f %12.1f %s%s%s\n", offered, actual, ratio, result_str, errors, p99 * 1000, p95 * 1000, max * 1000, rpc_qps, c_dim, note, c_reset
    }
    END {
      print ""
      if (best_actual > 0) {
        printf "%s★ best pass:%s offered=%.0f actual=%.1f qps p99=%.1fms rpc_pull/s=%.1f\n", c_green, c_reset, best_offered, best_actual, best_p99 * 1000, best_rpc
      } else {
        printf "%s✗ best pass: none%s\n", c_yellow, c_reset
      }
    }
  ' "$file"
}

append_runtime_pool_pressure_display() {
  local file="$1"
  printf '\n%s# runtime pool pressure%s\n' "$C_BOLD" "$C_RESET"
  if [[ ! -f "$file" ]] || [[ "$(wc -l <"$file")" -le 1 ]]; then
    printf 'none\n'
    return
  fi
  awk -F'\t' -v c_bold="$C_BOLD" -v c_reset="$C_RESET" -v c_yellow="$C_YELLOW" -v c_dim="$C_DIM" '
    NR == 1 { next }
    {
      entries++
      fill = $11 + 0
      inflight_util = $17 + 0
      full += $18 + 0
      busy += $19 + 0
      dirty += $20 + 0
      requeued += $21 + 0
      if (fill > max_fill) {
        max_fill = fill
      }
      if (inflight_util > max_inflight_util) {
        max_inflight_util = inflight_util
      }
      score = fill + inflight_util
      if ($22 != "") {
        score += 1
      }
      if (($18 + $19 + $20 + $21) > 0) {
        score += 1
      }
      if (score > worst_score) {
        worst_score = score
        worst = sprintf("offered=%.0f node=%s pool=%s/%s queue=%s priority=%s reason=%s", $1 + 0, $4, $5, $6, $7, $8, $22)
      }
    }
    END {
      if (entries == 0) {
        print "none"
        exit
      }
      fill_color = (max_fill >= 0.8) ? c_yellow : ""
      util_color = (max_inflight_util >= 0.8) ? c_yellow : ""
      printf "entries=%.0f max_fill=%s%.3f%s max_inflight_util=%s%.3f%s full=%.0f busy=%.0f dirty=%.0f requeued=%.0f\n",
        entries,
        fill_color, max_fill, (fill_color != "") ? c_reset : "",
        util_color, max_inflight_util, (util_color != "") ? c_reset : "",
        full, busy, dirty, requeued
      printf "%sworst=%s%s\n%sdetails=runtime_pool_pressure_summary.tsv%s\n",
        c_dim, worst, c_reset, c_dim, c_reset
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
      offered = $1 + 0
      node = $4
      remember_node(node)
      pool = display_pool($5, $6)
      running = $7 + 0
      capacity = $8 + 0
      waiting = $9 + 0
      util = $10 + 0

      row_offered[entries] = offered
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
        max_offered[node] = offered
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
        printf "- node=%s pools=%.0f max_util=%.3f offered=%.0f pool=%s used/cap=%.0f/%.0f waiting=%.0f\n",
          node, pools_by_node[node], max_util[node], max_offered[node],
          max_pool[node], max_running[node], max_capacity[node], max_waiting[node]
        for (i = 1; i <= entries; i++) {
          if (row_node[i] != node) {
            continue
          }
          printf "- offered=%.0f node=%s pool=%s used/cap=%s util=%.3f waiting=%.0f\n",
            row_offered[i], row_node[i], row_pool[i],
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
    /^ANTS POOL USAGE$/ || /^# ants pool usage$/ { exit }
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
    NR == 1 { next }
    {
      entries++
      fill = $11 + 0
      inflight_util = $17 + 0
      full += $18 + 0
      busy += $19 + 0
      dirty += $20 + 0
      requeued += $21 + 0
      if (fill > max_fill) {
        max_fill = fill
      }
      if (inflight_util > max_inflight_util) {
        max_inflight_util = inflight_util
      }
      score = fill + inflight_util
      if ($22 != "") {
        score += 1
      }
      if (($18 + $19 + $20 + $21) > 0) {
        score += 1
      }
      if (score > worst_score) {
        worst_score = score
        worst = sprintf("offered=%.0f node=%s pool=%s/%s queue=%s priority=%s reason=%s", $1 + 0, $4, $5, $6, $7, $8, $22)
      }
    }
    END {
      if (entries == 0) {
        print "- none"
        exit
      }
      printf "- entries=%.0f max_fill=%.3f max_inflight_util=%.3f full=%.0f busy=%.0f dirty=%.0f requeued=%.0f worst=%s details=runtime_pool_pressure_summary.tsv\n",
        entries, max_fill, max_inflight_util, full, busy, dirty, requeued, worst
    }
  ' "$file"
}

append_attempt_summary() {
  local qps="$1"
  local tag="$2"
  local attempt_dir="$3"
  local child_exit="$4"
  local summary="$attempt_dir/summary.tsv"
  local p99_limit
  local runtime_pool
  local runtime_pool_file
  local channelappend
  p99_limit="$(duration_seconds "$STABLE_P99")"
  runtime_pool_file="$attempt_dir/channel_metrics_summary.tsv"
  if [[ ! -f "$runtime_pool_file" ]]; then
    runtime_pool_file="$attempt_dir/channelv2_metrics_summary.tsv"
  fi
  runtime_pool="$(runtime_pool_attempt_summary "$runtime_pool_file")"
  channelappend="$(channelappend_attempt_summary "$attempt_dir/channelappend_metrics_summary.tsv")"
  if [[ ! -f "$summary" ]]; then
    printf '%s\t%s\t%s\t%s\tmissing_summary\t0\t0\t0\t0\t0\t0\t0\t0\t0\t0\t%s\t%s\tFAIL\tmissing_summary\t%s\n' \
      "$tag" "$qps" "$attempt_dir" "$child_exit" "$runtime_pool" "$channelappend" "$attempt_dir" >>"$OUT_DIR/summary.tsv"
    return
  fi
  awk -F'\t' \
    -v attempt_dir="$attempt_dir" \
    -v child_exit="$child_exit" \
    -v min_ratio="$MIN_ACTUAL_RATIO" \
    -v p99_limit="$p99_limit" \
    -v runtime_pool="$runtime_pool" \
    -v channelappend="$channelappend" '
    BEGIN {
      split(runtime_pool, pool, "\t")
      split(channelappend, cw, "\t")
    }
    NR == 1 { next }
    {
      tag = $1
      offered = $2 + 0
      status = $3
      worker_exit = $4 + 0
      actual = $5 + 0
      send_success = $6 + 0
      send_errors = $7 + 0
      connect_error_rate = $8 + 0
      sendack_error_rate = $9 + 0
      p50 = $10 + 0
      p95 = $11 + 0
      p99 = $12 + 0
      max = $13 + 0
      ratio = offered > 0 ? actual / offered : 0
      result = "PASS"
      note = "ok"
      if (child_exit != 0) {
        result = "FAIL"
        note = "child_exit=" child_exit
      } else if (status != "passed") {
        result = "FAIL"
        note = status
      } else if (worker_exit != 0) {
        result = "FAIL"
        note = "worker_exit=" worker_exit
      } else if (send_errors > 0) {
        result = "FAIL"
        note = "send_errors"
      } else if (connect_error_rate > 0) {
        result = "FAIL"
        note = "connect_errors"
      } else if (sendack_error_rate > 0) {
        result = "FAIL"
        note = "sendack_errors"
      } else if (p99 > p99_limit) {
        result = "FAIL"
        note = "p99"
      } else if (ratio + 0.0000001 < min_ratio) {
        result = "FAIL"
        note = "actual_ratio"
      }
      printf "%s\t%.6g\t%s\t%s\t%s\t%.3f\t%.1f\t%.0f\t%.0f\t%.6g\t%.6g\t%.6g\t%.6g\t%.6g\t%.6g\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
        tag, offered, attempt_dir, child_exit, status, ratio, actual, send_success, send_errors,
        connect_error_rate, sendack_error_rate, p50, p95, p99, max,
        pool[1], pool[2], pool[3], pool[4], pool[5],
        pool[6], pool[7], pool[8], pool[9], pool[10],
        cw[1], cw[2], cw[3], cw[4], cw[5], cw[6], cw[7], cw[8], cw[9], cw[10], cw[11], cw[12], cw[13],
        result, note, attempt_dir
    }
  ' "$summary" >>"$OUT_DIR/summary.tsv"
}

write_display_summary() {
  write_ants_pool_usage_summary
  write_runtime_pool_pressure_summary
  # The summary.txt file is plain (no ANSI) for archival; the live terminal
  # output uses the colorized functions directly in main().
  (
    C_RESET='' C_BOLD='' C_DIM='' C_GREEN='' C_RED='' C_YELLOW='' C_CYAN='' C_MAGENTA='' C_WHITE=''
    append_real_qps_result_display "$OUT_DIR/summary.tsv" >"$OUT_DIR/summary.txt"
    printf '\n' >>"$OUT_DIR/summary.txt"
    append_ants_pool_usage_display "$OUT_DIR/ants_pool_usage_summary.tsv" >>"$OUT_DIR/summary.txt"
  )
}

write_markdown_summary() {
  local ants_pool_usage
  local result_summary
  result_summary="$(result_display_markdown "$OUT_DIR/summary.txt")"
  ants_pool_usage="$(ants_pool_usage_markdown "$OUT_DIR/ants_pool_usage_summary.tsv")"
  cat >"$OUT_DIR/summary.md" <<EOF
# Three-Node Real QPS Evidence

## Scenario
- workload: clean single-attempt local wukongim three-node cluster group-channel QPS runs
- qps_list: $QPS_LIST
- channels: $CHANNELS
- users: $USERS
- group_members: $GROUP_MEMBERS
- sender_pick: $SENDER_PICK
- recv_ack: $RECV_ACK
- heartbeat: $HEARTBEAT_ENABLED
- min_actual_ratio: $MIN_ACTUAL_RATIO
- duration: $DURATION
- warmup: $WARMUP
- cooldown: $COOLDOWN

## Evidence
- aggregate_summary: summary.tsv
- display_summary: summary.txt
- env: env.txt
- git: git.txt
- attempt_dirs: one child directory per offered QPS value
- ants_pool_usage: ants_pool_usage_summary.tsv

## Result
\`\`\`text
${result_summary}
\`\`\`

## Ants Pool Usage
${ants_pool_usage}
EOF
}

run_attempt() {
  local qps="$1"
  local tag attempt_dir console child_exit
  tag="$(qps_tag "$qps")"
  attempt_dir="$OUT_DIR/${tag}-qps"
  console="$attempt_dir/real-qps-console.txt"
  mkdir -p "$attempt_dir"
  printf '\n%s── qps=%s tag=%s ──%s\n' "$C_MAGENTA" "$qps" "$tag" "$C_RESET"
  log "running clean qps=$qps tag=$tag out=$attempt_dir"
  set +e
  GOWORK="${GOWORK:-off}" \
  WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS:-500}" \
  WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS="${WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS:-500}" \
  WK_CLUSTER_CHANNEL_RPC_WORKERS="${WK_CLUSTER_CHANNEL_RPC_WORKERS:-500}" \
  WK_TOP_API_ENABLE="${WK_TOP_API_ENABLE:-false}" \
  WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW="${WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW:-1ms}" \
  WK_CLUSTER_COMMIT_COORDINATOR_SHARDS="${WK_CLUSTER_COMMIT_COORDINATOR_SHARDS:-8}" \
  WK_BENCH_PAYLOAD_BYTES="$PAYLOAD_BYTES" \
    "$BASE_SCRIPT" \
      --qps "$qps" \
      --out-dir "$attempt_dir" \
      --wkbench-bin "$WK_BENCH_BIN" \
      --start-script "$START_SCRIPT" \
      --ready-timeout "$READY_TIMEOUT" \
      --channels "$CHANNELS" \
      --users "$USERS" \
      --members "$GROUP_MEMBERS" \
      --concurrency "$CONCURRENCY" \
      --duration "$DURATION" \
      --warmup "$WARMUP" \
      --cooldown "$COOLDOWN" \
      --stable-p99 "$STABLE_P99" \
      --ack-timeout "$ACK_TIMEOUT" \
      --phase-poll-timeout "$PHASE_POLL_TIMEOUT" \
      --recv-ack "$RECV_ACK" \
      --heartbeat "$HEARTBEAT_ENABLED" \
      --profile-seconds "$PROFILE_SECONDS" \
      --sender-pick "$SENDER_PICK" \
      --api "$API_ADDRS" \
      --gateway "$GATEWAY_ADDRS" \
      --metrics "$METRICS_ADDRS" \
      > >(tee "$console" | filter_child_bench_result_display) 2>&1
  child_exit=$?
  set -e
  append_attempt_summary "$qps" "$tag" "$attempt_dir" "$child_exit"
}

main() {
  cd "$ROOT_DIR"
  mkdir -p "$OUT_DIR"
  write_metadata
  cat >"$OUT_DIR/summary.tsv" <<'EOF'
tag	offered_qps	child_dir	child_exit	status	actual_ratio	actual_qps	send_success	send_errors	connect_error_rate	sendack_error_rate	p50_seconds	p95_seconds	p99_seconds	max_seconds	runtime_pool_queue_depth_max	runtime_pool_queue_fill_max	runtime_pool_queue_bytes_max	runtime_pool_queue_bytes_fill_max	runtime_pool_inflight_max	runtime_pool_inflight_util_max	runtime_pool_admission_full_delta	runtime_pool_admission_busy_delta	runtime_pool_admission_dirty_delta	runtime_pool_admission_requeued_delta	channelappend_router_total_delta	channelappend_router_error_delta	channelappend_router_backpressured_delta	channelappend_router_channel_busy_delta	channelappend_router_route_not_ready_delta	channelappend_router_timeout_delta	channelappend_local_admission_rejected_delta	channelappend_mailbox_fill_max	channelappend_effect_slots_fill_max	channelappend_post_commit_backlog_max	channelappend_effect_error_delta	channelappend_effect_worker_util_max	channelappend_effect_queue_fill_max	result	note	attempt_dir
EOF
  local qps
  for qps in "${QPS_VALUES[@]}"; do
    [[ "$qps" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "invalid qps value: $qps"
    run_attempt "$qps"
  done
  write_display_summary
  write_markdown_summary
  printf '\n%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n' "$C_DIM" "$C_RESET"
  append_real_qps_result_display "$OUT_DIR/summary.tsv"
  append_ants_pool_usage_display "$OUT_DIR/ants_pool_usage_summary.tsv"
  append_runtime_pool_pressure_display "$OUT_DIR/runtime_pool_pressure_summary.tsv"
  printf '\n%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n' "$C_DIM" "$C_RESET"
  log "details:"
  printf '  %ssummary:%s        %s\n' "$C_DIM" "$C_RESET" "$OUT_DIR/summary.tsv"
  printf '  %sants_pool_usage:%s %s\n' "$C_DIM" "$C_RESET" "$OUT_DIR/ants_pool_usage_summary.tsv"
  printf '  %sattempts:%s       %s\n' "$C_DIM" "$C_RESET" "$OUT_DIR"
}

main "$@"
