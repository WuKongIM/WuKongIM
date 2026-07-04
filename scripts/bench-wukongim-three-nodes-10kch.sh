#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_ACTIVATE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="${WK_BENCH_ACTIVATE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-activate-10kch}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-activate-channels/wkbench}"
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-three-nodes.sh}"
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
RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"
METRICS_HEALTH_EXIT_STATUS=7
METRICS_HEALTH_STATUS="not_run"

CLUSTER_PID=""
RESOURCE_SAMPLER_PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-three-nodes-10kch.sh [options]

Starts a local cmd/wukongim three-node cluster through
scripts/start-wukongim-three-nodes.sh, then runs:

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
  --resource-interval SECS    Server process CPU/memory sample interval. 0 disables periodic sampling. Default: 1.
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
require_positive_int '--activation-concurrency' "$ACTIVATION_CONCURRENCY"
require_positive_int '--probe-batch-size' "$PROBE_BATCH_SIZE"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_nonnegative_number '--prepare-rate' "$PREPARE_RATE"
require_nonnegative_number '--connect-rate' "$CONNECT_RATE"
require_nonnegative_number '--resource-interval' "$RESOURCE_SAMPLE_INTERVAL"

declare -a API_VALUES GATEWAY_VALUES METRICS_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

cleanup() {
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
  WK_CLUSTER_INITIAL_SLOT_COUNT="${WK_CLUSTER_INITIAL_SLOT_COUNT:-3}" \
  WK_CLUSTER_HASH_SLOT_COUNT="${WK_CLUSTER_HASH_SLOT_COUNT:-96}" \
  WK_CLUSTER_CHANNEL_REACTOR_COUNT="${WK_CLUSTER_CHANNEL_REACTOR_COUNT:-32}" \
  WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS="${WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS:-256}" \
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

metric_classify_value() {
  local file="$1"
  local key="$2"
  local fallback_key="${3:-}"
  awk -F':' -v key="$key" '
    $1 == key {
      value = $2
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      print value
      found = 1
      exit
    }
    END {
      if (!found) exit 1
    }
  ' "$file" && return 0
  [[ -n "$fallback_key" ]] || return 1
  awk -F':' -v key="$fallback_key" '
    $1 == key {
      value = $2
      sub(/^[[:space:]]+/, "", value)
      sub(/[[:space:]]+$/, "", value)
      print value
      found = 1
      exit
    }
    END {
      if (!found) exit 1
    }
  ' "$file"
}

legacy_channel_metric_key() {
  local key="$1"
  if [[ "$key" == channel_* ]]; then
    printf 'channelv2_%s\n' "${key#channel_}"
    return
  fi
  printf '%s\n' "$key"
}

metric_number_equals() {
  local left="$1"
  local right="$2"
  [[ "$left" =~ ^-?[0-9]+([.][0-9]+)?$ ]] || return 2
  [[ "$right" =~ ^-?[0-9]+([.][0-9]+)?$ ]] || return 2
  awk -v left="$left" -v right="$right" 'BEGIN { exit !(left + 0 == right + 0) }'
}

record_zero_metric_gate() {
  local body="$1"
  local file="$2"
  local key="$3"
  local legacy_key
  local value
  legacy_key="$(legacy_channel_metric_key "$key")"
  if ! value="$(metric_classify_value "$file" "$key" "$legacy_key")"; then
    printf 'FAIL\t%s\t%s\tmissing\texpected=0\n' "$(basename "$file")" "$key" >>"$body"
    return 1
  fi
  if ! metric_number_equals "$value" "0"; then
    printf 'FAIL\t%s\t%s\t%s\texpected=0\n' "$(basename "$file")" "$key" "$value" >>"$body"
    return 1
  fi
  printf 'OK\t%s\t%s\t%s\texpected=0\n' "$(basename "$file")" "$key" "$value" >>"$body"
}

record_equal_metric_gate() {
  local body="$1"
  local file="$2"
  local left_key="$3"
  local right_key="$4"
  local left_legacy_key right_legacy_key
  local left right
  left_legacy_key="$(legacy_channel_metric_key "$left_key")"
  right_legacy_key="$(legacy_channel_metric_key "$right_key")"
  if ! left="$(metric_classify_value "$file" "$left_key" "$left_legacy_key")"; then
    printf 'FAIL\t%s\t%s\tmissing\texpected=%s\n' "$(basename "$file")" "$left_key" "$right_key" >>"$body"
    return 1
  fi
  if ! right="$(metric_classify_value "$file" "$right_key" "$right_legacy_key")"; then
    printf 'FAIL\t%s\t%s\tmissing\texpected=%s\n' "$(basename "$file")" "$right_key" "$left_key" >>"$body"
    return 1
  fi
  if ! metric_number_equals "$left" "$right"; then
    printf 'FAIL\t%s\t%s=%s\t%s=%s\texpected_equal\n' "$(basename "$file")" "$left_key" "$left" "$right_key" "$right" >>"$body"
    return 1
  fi
  printf 'OK\t%s\t%s=%s\t%s=%s\texpected_equal\n' "$(basename "$file")" "$left_key" "$left" "$right_key" "$right" >>"$body"
}

evaluate_metrics_health_gates() {
  local metrics_dir="$OUT_DIR/metrics"
  local out="$metrics_dir/health-gates.txt"
  local body="$metrics_dir/health-gates.body.tmp"
  local failures=0
  local checks=0
  local files=0
  mkdir -p "$metrics_dir"
  : >"$body"

  local classify_files=("$metrics_dir"/*-classify.txt)
  if [[ ! -e "${classify_files[0]}" ]]; then
    printf 'FAIL\tmetrics_classify_outputs\tmissing\texpected>=1\n' >>"$body"
    failures=$((failures + 1))
  else
    local file key
    for file in "${classify_files[@]}"; do
      files=$((files + 1))
      if grep -q '^metrics classify skipped:' "$file"; then
        printf 'FAIL\t%s\tmetrics_classify_missing\texpected_before_after_snapshots\n' "$(basename "$file")" >>"$body"
        failures=$((failures + 1))
        continue
      fi
      for key in \
        channel_pending_meta_current_max \
        channel_pending_meta_released_count \
        channel_need_meta_pull_retry_count \
        channel_need_meta_pull_err_count \
        channel_pull_hint_err_count \
        channel_pull_hint_receive_err_count; do
        checks=$((checks + 1))
        if ! record_zero_metric_gate "$body" "$file" "$key"; then
          failures=$((failures + 1))
        fi
      done
      checks=$((checks + 1))
      if ! record_equal_metric_gate "$body" "$file" channel_need_meta_pull_submitted_count channel_need_meta_pull_ok_count; then
        failures=$((failures + 1))
      fi
    done
  fi

  if [[ "$failures" -eq 0 ]]; then
    METRICS_HEALTH_STATUS="passed"
  else
    METRICS_HEALTH_STATUS="failed"
  fi
  {
    echo "status: $METRICS_HEALTH_STATUS"
    echo "files: $files"
    echo "checks: $checks"
    echo "failures: $failures"
    echo
    cat "$body"
  } >"$out"
  rm -f "$body"

  if [[ "$failures" -ne 0 ]]; then
    return "$METRICS_HEALTH_EXIT_STATUS"
  fi
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
  local config="$ROOT_DIR/scripts/wukongim/wukongim-node${node}.conf"
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
RESOURCE_SAMPLE_INTERVAL=$RESOURCE_SAMPLE_INTERVAL
START_CLUSTER=$START_CLUSTER
CLEAN_CLUSTER=$CLEAN_CLUSTER
START_SCRIPT=$START_SCRIPT
READY_TIMEOUT=$READY_TIMEOUT
EOF
  cp "$ROOT_DIR"/scripts/wukongim/wukongim-node*.conf "$OUT_DIR/config/" 2>/dev/null || true
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
- workload: local wukongim three-node cluster wkbench activate-channels
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
- metrics_classification: metrics/*-classify.txt
- metrics_health: metrics/health-gates.txt
- pprof: pprof/
- resources: resources/
- logs: logs/
- report: report/
- console: wkbench-console.txt

## Result
- exit_status: $status
- metrics_health_status: $METRICS_HEALTH_STATUS
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
  start_server_resource_sampler

  local status=0
  run_activation || status=$?

  stop_server_resource_sampler
  sample_server_resources after
  write_server_resource_summary
  scrape_metrics after
  classify_metrics
  local metrics_status=0
  evaluate_metrics_health_gates || metrics_status=$?
  if [[ "$status" -eq 0 && "$metrics_status" -ne 0 ]]; then
    status="$metrics_status"
  fi
  capture_pprof after
  collect_node_logs after
  write_summary "$status"
  cat "$OUT_DIR/wkbench-console.txt" || true
  log "evidence: $OUT_DIR"
  exit "$status"
}

main "$@"
