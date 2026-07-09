#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_MESSAGE_EVENT_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="${WK_BENCH_MESSAGE_EVENT_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-message-event}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-message-event/wkbench}"
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-three-nodes.sh}"
READY_TIMEOUT="${WK_BENCH_THREE_NODE_READY_TIMEOUT:-90}"
START_CLUSTER=1
CLEAN_CLUSTER=1

PROFILE="${WK_BENCH_MESSAGE_EVENT_PROFILE:-smoke}"
RUN_ID="${WK_BENCH_MESSAGE_EVENT_RUN_ID:-message-event-${TIMESTAMP}}"
CHANNELS="${WK_BENCH_MESSAGE_EVENT_CHANNELS:-}"
STREAMS_PER_CHANNEL="${WK_BENCH_MESSAGE_EVENT_STREAMS_PER_CHANNEL:-}"
LANES_PER_STREAM="${WK_BENCH_MESSAGE_EVENT_LANES_PER_STREAM:-}"
DELTAS_PER_LANE="${WK_BENCH_MESSAGE_EVENT_DELTAS_PER_LANE:-}"
PAYLOAD_BYTES="${WK_BENCH_MESSAGE_EVENT_PAYLOAD_BYTES:-}"
CONCURRENCY="${WK_BENCH_MESSAGE_EVENT_CONCURRENCY:-}"
REQUEST_TIMEOUT="${WK_BENCH_MESSAGE_EVENT_REQUEST_TIMEOUT:-}"
WARM_CHANNELS="${WK_BENCH_MESSAGE_EVENT_WARM_CHANNELS:-0}"
WARM_RUNTIME="${WK_BENCH_MESSAGE_EVENT_WARM_RUNTIME:-0}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"
RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"
LIVE_METRICS_INTERVAL="${WK_BENCH_MESSAGE_EVENT_LIVE_METRICS_INTERVAL:-1}"
CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-10}"
CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-256}"
CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}"

CLUSTER_PID=""
RESOURCE_SAMPLER_PID=""
LIVE_METRICS_SAMPLER_PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-three-nodes-message-event.sh [options]

Starts a local cmd/wukongim three-node cluster through
scripts/start-wukongim-three-nodes.sh, then runs:

  wkbench capacity message-event

Options:
  --out-dir DIR              Evidence output directory.
  --wkbench-bin PATH         wkbench binary path. Default: data/wkbench-message-event/wkbench.
  --no-start                 Use an already-running cluster.
  --no-clean                 Keep existing node data when starting the cluster.
  --start-script PATH        Three-node startup script.
  --ready-timeout SECS       Cluster ready wait timeout. Default: 90.
  --profile NAME             Baseline profile: smoke, medium, pressure, or custom. Default: smoke.
  --run-id ID                Stable generated data identifier.
  --warm-channels            Create generated channels before measured message-event snapshots.
  --warm-runtime             Send one normal message per channel before measured message-event snapshots.
  --channels N               Generated group channel count. Default: 32.
  --streams-per-channel N    Stream base messages per channel. Default: 2.
  --lanes-per-stream N       Event keys updated before each finish. Default: 2.
  --deltas-per-lane N        stream.delta updates per event key. Default: 4.
  --payload-bytes N          Approximate stream.delta payload bytes. Default: 128.
  --concurrency N            Maximum in-flight stream workflows. Default: 64.
  --request-timeout D        Per-request HTTP timeout. Default: 10s.
  --api LIST                 Comma-separated API base URLs.
  --metrics LIST             Comma-separated metrics base URLs. Default: same as --api.
  --resource-interval SECS   Server process CPU/memory sample interval. 0 disables periodic sampling. Default: 1.
  --live-metrics-interval S  During-run /metrics sample interval. 0 disables live samples. Default: 1.
  -h, --help                 Show this help.
USAGE
}

log() {
  printf '[bench-three-message-event] %s\n' "$*"
}

die() {
  printf '[bench-three-message-event] ERROR: %s\n' "$*" >&2
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

require_bool01() {
  local name="$1"
  local value="$2"
  [[ "$value" == "0" || "$value" == "1" ]] || die "$name must be 0 or 1: $value"
}

apply_profile_defaults() {
  local profile_channels profile_streams profile_lanes profile_deltas profile_payload profile_concurrency profile_timeout
  case "$PROFILE" in
    smoke)
      profile_channels=32
      profile_streams=2
      profile_lanes=2
      profile_deltas=4
      profile_payload=128
      profile_concurrency=64
      profile_timeout=10s
      ;;
    medium)
      profile_channels=1000
      profile_streams=2
      profile_lanes=2
      profile_deltas=4
      profile_payload=128
      profile_concurrency=512
      profile_timeout=15s
      ;;
    pressure)
      profile_channels=10000
      profile_streams=2
      profile_lanes=2
      profile_deltas=4
      profile_payload=128
      profile_concurrency=2048
      profile_timeout=20s
      ;;
    custom)
      profile_channels=""
      profile_streams=""
      profile_lanes=""
      profile_deltas=""
      profile_payload=""
      profile_concurrency=""
      profile_timeout=""
      ;;
    *)
      die "unknown profile: $PROFILE"
      ;;
  esac
  CHANNELS="${CHANNELS:-$profile_channels}"
  STREAMS_PER_CHANNEL="${STREAMS_PER_CHANNEL:-$profile_streams}"
  LANES_PER_STREAM="${LANES_PER_STREAM:-$profile_lanes}"
  DELTAS_PER_LANE="${DELTAS_PER_LANE:-$profile_deltas}"
  PAYLOAD_BYTES="${PAYLOAD_BYTES:-$profile_payload}"
  CONCURRENCY="${CONCURRENCY:-$profile_concurrency}"
  REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-$profile_timeout}"
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
    --profile)
      [[ $# -ge 2 ]] || die '--profile requires a value'
      PROFILE="$2"
      shift 2
      ;;
    --run-id)
      [[ $# -ge 2 ]] || die '--run-id requires a value'
      RUN_ID="$2"
      shift 2
      ;;
    --warm-channels)
      WARM_CHANNELS=1
      shift
      ;;
    --warm-runtime)
      WARM_RUNTIME=1
      shift
      ;;
    --channels)
      [[ $# -ge 2 ]] || die '--channels requires a value'
      CHANNELS="$2"
      shift 2
      ;;
    --streams-per-channel)
      [[ $# -ge 2 ]] || die '--streams-per-channel requires a value'
      STREAMS_PER_CHANNEL="$2"
      shift 2
      ;;
    --lanes-per-stream)
      [[ $# -ge 2 ]] || die '--lanes-per-stream requires a value'
      LANES_PER_STREAM="$2"
      shift 2
      ;;
    --deltas-per-lane)
      [[ $# -ge 2 ]] || die '--deltas-per-lane requires a value'
      DELTAS_PER_LANE="$2"
      shift 2
      ;;
    --payload-bytes)
      [[ $# -ge 2 ]] || die '--payload-bytes requires a value'
      PAYLOAD_BYTES="$2"
      shift 2
      ;;
    --concurrency)
      [[ $# -ge 2 ]] || die '--concurrency requires a value'
      CONCURRENCY="$2"
      shift 2
      ;;
    --request-timeout)
      [[ $# -ge 2 ]] || die '--request-timeout requires a value'
      REQUEST_TIMEOUT="$2"
      shift 2
      ;;
    --api)
      [[ $# -ge 2 ]] || die '--api requires a value'
      API_ADDRS="$2"
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
    --live-metrics-interval)
      [[ $# -ge 2 ]] || die '--live-metrics-interval requires a value'
      LIVE_METRICS_INTERVAL="$2"
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

apply_profile_defaults
require_positive_int '--channels' "$CHANNELS"
require_positive_int '--streams-per-channel' "$STREAMS_PER_CHANNEL"
require_positive_int '--lanes-per-stream' "$LANES_PER_STREAM"
require_positive_int '--deltas-per-lane' "$DELTAS_PER_LANE"
require_positive_int '--payload-bytes' "$PAYLOAD_BYTES"
require_positive_int '--concurrency' "$CONCURRENCY"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_nonnegative_number '--resource-interval' "$RESOURCE_SAMPLE_INTERVAL"
require_nonnegative_number '--live-metrics-interval' "$LIVE_METRICS_INTERVAL"
require_bool01 'WK_BENCH_MESSAGE_EVENT_WARM_CHANNELS' "$WARM_CHANNELS"
require_bool01 'WK_BENCH_MESSAGE_EVENT_WARM_RUNTIME' "$WARM_RUNTIME"

declare -a API_VALUES METRICS_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

cleanup() {
  stop_live_metrics_sampler
  stop_server_resource_sampler
  if [[ -n "$CLUSTER_PID" ]]; then
    log "stopping three-node cluster pid=$CLUSTER_PID"
    kill "$CLUSTER_PID" >/dev/null 2>&1 || true
    wait "$CLUSTER_PID" 2>/dev/null || true
  fi
}

trap cleanup EXIT

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
  WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}" \
  WK_CLUSTER_INITIAL_SLOT_COUNT="$CLUSTER_INITIAL_SLOT_COUNT" \
  WK_CLUSTER_HASH_SLOT_COUNT="$CLUSTER_HASH_SLOT_COUNT" \
  WK_CLUSTER_CHANNEL_REACTOR_COUNT="$CLUSTER_CHANNEL_REACTOR_COUNT" \
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

capture_debug_snapshots() {
  local config_dir="$OUT_DIR/config"
  mkdir -p "$config_dir"
  local api id
  for api in "${API_VALUES[@]}"; do
    id="$(metric_file_id "$api")"
    curl -fsS --max-time 5 "${api%/}/debug/config" >"$config_dir/${id}-debug-config.json" 2>/dev/null || true
    curl -fsS --max-time 5 "${api%/}/debug/cluster" >"$config_dir/${id}-debug-cluster.json" 2>/dev/null || true
  done
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

live_metrics_sampling_enabled() {
  awk -v interval="$LIVE_METRICS_INTERVAL" 'BEGIN { exit !(interval > 0) }'
}

sample_live_metrics_once() {
  local index="$1"
  local metrics_dir="$OUT_DIR/metrics/live"
  mkdir -p "$metrics_dir"
  local ts addr id sample_id
  ts="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  sample_id="$(printf '%06d' "$index")"
  printf '%s\t%s\n' "$sample_id" "$ts" >>"$metrics_dir/samples.tsv"
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    curl -fsS "${addr%/}/metrics" >"$metrics_dir/${sample_id}-${id}.prom" || true
  done
}

start_live_metrics_sampler() {
  local metrics_dir="$OUT_DIR/metrics/live"
  mkdir -p "$metrics_dir"
  printf 'sample\ttimestamp\n' >"$metrics_dir/samples.tsv"
  if ! live_metrics_sampling_enabled; then
    return
  fi
  (
    local i=0
    while true; do
      sample_live_metrics_once "$i"
      i=$((i + 1))
      sleep "$LIVE_METRICS_INTERVAL"
    done
  ) &
  LIVE_METRICS_SAMPLER_PID="$!"
}

stop_live_metrics_sampler() {
  if [[ -n "$LIVE_METRICS_SAMPLER_PID" ]]; then
    kill "$LIVE_METRICS_SAMPLER_PID" >/dev/null 2>&1 || true
    wait "$LIVE_METRICS_SAMPLER_PID" 2>/dev/null || true
    LIVE_METRICS_SAMPLER_PID=""
  fi
}

write_live_metrics_summary() {
  local summary="$OUT_DIR/metrics/live-summary.tsv"
  mkdir -p "$OUT_DIR/metrics"
  if ! compgen -G "$OUT_DIR/metrics/live/*.prom" >/dev/null; then
    {
      echo -e "metric\tmax"
      echo -e "message_event_stream_cache_sessions\t0"
      echo -e "message_event_stream_cache_open_lanes\t0"
      echo -e "message_event_stream_cache_payload_bytes\t0"
    } >"$summary"
    return
  fi
  awk '
    BEGIN {
      sessions = 0
      lanes = 0
      payload = 0
    }
    /^wukongim_message_event_stream_cache_sessions([{ ]|$)/ {
      value = $NF + 0
      if (value > sessions) sessions = value
    }
    /^wukongim_message_event_stream_cache_open_lanes([{ ]|$)/ {
      value = $NF + 0
      if (value > lanes) lanes = value
    }
    /^wukongim_message_event_stream_cache_payload_bytes([{ ]|$)/ {
      value = $NF + 0
      if (value > payload) payload = value
    }
    END {
      print "metric\tmax"
      printf "message_event_stream_cache_sessions\t%.0f\n", sessions
      printf "message_event_stream_cache_open_lanes\t%.0f\n", lanes
      printf "message_event_stream_cache_payload_bytes\t%.0f\n", payload
    }
  ' "$OUT_DIR"/metrics/live/*.prom >"$summary"
}

classify_metrics() {
  local metrics_dir="$OUT_DIR/metrics"
  mkdir -p "$metrics_dir"
  local addr id before after out
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    before="$metrics_dir/${id}-before.prom"
    after="$metrics_dir/${id}-after.prom"
    out="$metrics_dir/${id}-classify.txt"
    if [[ ! -f "$before" || ! -f "$after" ]]; then
      {
        echo "metrics classify skipped: missing before/after snapshots"
        echo "before: $before"
        echo "after: $after"
      } >"$out"
      continue
    fi
    "$WK_BENCH_BIN" metrics classify --before "$before" --after "$after" >"$out" 2>&1 || true
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
  cp "$ROOT_DIR"/data/wukongim-three-node-logs/node*.log "$dest/" 2>/dev/null || true
  if [[ -f "$OUT_DIR/cluster-start.log" ]]; then
    cp "$OUT_DIR/cluster-start.log" "$dest/cluster-start.log" 2>/dev/null || true
  fi
}

json_escape() {
  printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g'
}

server_pid_from_log() {
  local node="$1"
  local log_file="$OUT_DIR/cluster-start.log"
  [[ -f "$log_file" ]] || return 1
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

write_resource_error_sample() {
  local phase="$1"
  local node_name="$2"
  local reason="$3"
  local ts
  ts="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  mkdir -p "$OUT_DIR/resources"
  printf '{"timestamp":"%s","phase":"%s","node":"%s","pid":null,"error":"%s"}\n' \
    "$ts" "$phase" "$node_name" "$(json_escape "$reason")" >>"$OUT_DIR/resources/server-process.jsonl"
}

sample_server_resources() {
  local phase="$1"
  local ts node node_name pid line cpu mem rss vsz elapsed command
  ts="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  mkdir -p "$OUT_DIR/resources"
  for node in 1 2 3; do
    node_name="node${node}"
    pid="$(server_pid_for_node "$node")"
    if [[ -z "$pid" ]]; then
      write_resource_error_sample "$phase" "$node_name" "pid_not_found"
      continue
    fi
    line="$(ps -p "$pid" -o pcpu= -o pmem= -o rss= -o vsz= -o etime= -o comm= 2>/dev/null || true)"
    if [[ -z "${line//[[:space:]]/}" ]]; then
      write_resource_error_sample "$phase" "$node_name" "ps_sample_unavailable"
      continue
    fi
    read -r cpu mem rss vsz elapsed command <<<"$line"
    printf '{"timestamp":"%s","phase":"%s","node":"%s","pid":%s,"cpu_percent":%.3f,"mem_percent":%.3f,"rss_kb":%s,"vsz_kb":%s,"elapsed":"%s","command":"%s"}\n' \
      "$ts" "$phase" "$node_name" "$pid" "$cpu" "$mem" "$rss" "$vsz" "$elapsed" "$(json_escape "$command")" \
      >>"$OUT_DIR/resources/server-process.jsonl"
  done
}

resource_periodic_sampling_enabled() {
  awk -v interval="$RESOURCE_SAMPLE_INTERVAL" 'BEGIN { exit !(interval > 0) }'
}

start_server_resource_sampler() {
  sample_server_resources before
  if ! resource_periodic_sampling_enabled; then
    return
  fi
  (
    while true; do
      sleep "$RESOURCE_SAMPLE_INTERVAL"
      sample_server_resources interval
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
      print "node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb"
    }
    {
      node = json_string("node", $0)
      pid = json_number("pid", $0)
      if (node == "" || pid == "" || pid == "null") next
      cpu = json_number("cpu_percent", $0) + 0
      mem = json_number("mem_percent", $0) + 0
      rss = json_number("rss_kb", $0) + 0
      vsz = json_number("vsz_kb", $0) + 0
      samples[node]++
      last_pid[node] = pid
      cpu_sum[node] += cpu
      mem_sum[node] += mem
      if (samples[node] == 1 || cpu > cpu_max[node]) cpu_max[node] = cpu
      if (samples[node] == 1 || mem > mem_max[node]) mem_max[node] = mem
      if (samples[node] == 1 || rss > rss_max[node]) rss_max[node] = rss
      if (samples[node] == 1 || vsz > vsz_max[node]) vsz_max[node] = vsz
    }
    END {
      for (i = 1; i <= 3; i++) {
        node = "node" i
        if (samples[node] == 0) continue
        printf "%s\t%s\t%d\t%.3f\t%.3f\t%.3f\t%.3f\t%.0f\t%.0f\n",
          node,
          last_pid[node],
          samples[node],
          cpu_sum[node] / samples[node],
          cpu_max[node],
          mem_sum[node] / samples[node],
          mem_max[node],
          rss_max[node],
          vsz_max[node]
      }
    }
  ' "$samples" >"$summary"
}

write_metadata() {
  mkdir -p "$OUT_DIR/config"
  {
    echo "head=$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || true)"
    echo "short=$(git -C "$ROOT_DIR" rev-parse --short HEAD 2>/dev/null || true)"
    git -C "$ROOT_DIR" status --short 2>/dev/null || true
  } >"$OUT_DIR/git.txt"
cat >"$OUT_DIR/env.txt" <<EOF
PROFILE=$PROFILE
RUN_ID=$RUN_ID
CHANNELS=$CHANNELS
STREAMS_PER_CHANNEL=$STREAMS_PER_CHANNEL
LANES_PER_STREAM=$LANES_PER_STREAM
DELTAS_PER_LANE=$DELTAS_PER_LANE
PAYLOAD_BYTES=$PAYLOAD_BYTES
CONCURRENCY=$CONCURRENCY
REQUEST_TIMEOUT=$REQUEST_TIMEOUT
WARM_CHANNELS=$WARM_CHANNELS
WARM_RUNTIME=$WARM_RUNTIME
API_ADDRS=$API_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
RESOURCE_SAMPLE_INTERVAL=$RESOURCE_SAMPLE_INTERVAL
LIVE_METRICS_INTERVAL=$LIVE_METRICS_INTERVAL
CLUSTER_INITIAL_SLOT_COUNT=$CLUSTER_INITIAL_SLOT_COUNT
CLUSTER_HASH_SLOT_COUNT=$CLUSTER_HASH_SLOT_COUNT
CLUSTER_CHANNEL_REACTOR_COUNT=$CLUSTER_CHANNEL_REACTOR_COUNT
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
EOF
  cp "$ROOT_DIR"/scripts/wukongim/wukongim-node*.toml "$OUT_DIR/config/" 2>/dev/null || true
  if [[ -x "$START_SCRIPT" ]]; then
    WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}" \
    WK_CLUSTER_INITIAL_SLOT_COUNT="$CLUSTER_INITIAL_SLOT_COUNT" \
    WK_CLUSTER_HASH_SLOT_COUNT="$CLUSTER_HASH_SLOT_COUNT" \
    WK_CLUSTER_CHANNEL_REACTOR_COUNT="$CLUSTER_CHANNEL_REACTOR_COUNT" \
      "$START_SCRIPT" --dry-run >"$OUT_DIR/start-plan.txt" 2>&1 || true
  fi
}

write_summary() {
  local status="$1"
  cat >"$OUT_DIR/summary.md" <<EOF
# Three-Node Message Event Evidence

## Scenario
- workload: local wukongim three-node cluster wkbench message-event
- profile: $PROFILE
- run_id: $RUN_ID
- channels: $CHANNELS
- streams_per_channel: $STREAMS_PER_CHANNEL
- lanes_per_stream: $LANES_PER_STREAM
- deltas_per_lane: $DELTAS_PER_LANE
- payload_bytes: $PAYLOAD_BYTES
- concurrency: $CONCURRENCY
- request_timeout: $REQUEST_TIMEOUT
- warm_channels: $WARM_CHANNELS
- warm_runtime: $WARM_RUNTIME
- live_metrics_interval: $LIVE_METRICS_INTERVAL
- clean_cluster: $CLEAN_CLUSTER

## Evidence
- git: git.txt
- env: env.txt
- start_plan: start-plan.txt
- config: config/
- debug_config: config/*-debug-config.json
- debug_cluster: config/*-debug-cluster.json
- metrics: metrics/
- metrics_classification: metrics/*-classify.txt
- live_metrics_summary: metrics/live-summary.tsv
- pprof: pprof/
- resources: resources/
- logs: logs/
- report: report/
- console: wkbench-console.txt

## Result
- exit_status: $status
- message_event_gates: report/summary.md
- message_event_report: report/message_event_report.json
- message_event_summary: report/summary.md
EOF
}

run_message_event() {
  local report_dir="$OUT_DIR/report"
  local exit_status=0
  mkdir -p "$report_dir"
  local cmd=(
    "$WK_BENCH_BIN" capacity message-event
    --api "$API_ADDRS"
    --run-id "$RUN_ID"
    --channels "$CHANNELS"
    --streams-per-channel "$STREAMS_PER_CHANNEL"
    --lanes-per-stream "$LANES_PER_STREAM"
    --deltas-per-lane "$DELTAS_PER_LANE"
    --payload-bytes "$PAYLOAD_BYTES"
    --concurrency "$CONCURRENCY"
    --request-timeout "$REQUEST_TIMEOUT"
    --report-dir "$report_dir"
  )
  if [[ "$WARM_CHANNELS" -eq 1 ]]; then
    cmd+=(--warm-channels)
  fi
  if [[ "$WARM_RUNTIME" -eq 1 ]]; then
    cmd+=(--warm-runtime)
  fi
  log "running message-event channels=$CHANNELS streams_per_channel=$STREAMS_PER_CHANNEL lanes=$LANES_PER_STREAM deltas=$DELTAS_PER_LANE"
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
  capture_debug_snapshots
  collect_node_logs before
  scrape_metrics before
  capture_pprof before
  start_server_resource_sampler

  local status=0
  start_live_metrics_sampler
  run_message_event || status=$?
  stop_live_metrics_sampler

  stop_server_resource_sampler
  sample_server_resources after
  write_server_resource_summary
  write_live_metrics_summary
  scrape_metrics after
  classify_metrics
  capture_pprof after
  collect_node_logs after
  write_summary "$status"
  cat "$OUT_DIR/wkbench-console.txt" || true
  log "evidence: $OUT_DIR"
  exit "$status"
}

main "$@"
