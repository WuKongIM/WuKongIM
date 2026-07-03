#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_BENCH_PRESENCE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="${WK_BENCH_PRESENCE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-three-node-presence}"
WK_BENCH_BIN="${WK_BENCH_BIN:-$ROOT_DIR/data/wkbench-presence/wkbench}"
WORKER_ADDR="${WK_BENCH_WORKER_ADDR:-http://127.0.0.1:19131}"
WORKER_LISTEN="${WK_BENCH_WORKER_LISTEN:-127.0.0.1:19131}"
START_SCRIPT="${WK_BENCH_THREE_NODE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-three-nodes.sh}"
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
CLEANUP_TIMEOUT="${WK_BENCH_PRESENCE_CLEANUP_TIMEOUT:-0}"
PHASE_POLL_TIMEOUT="${WK_BENCH_PRESENCE_PHASE_POLL_TIMEOUT:-30s}"
REQUIRE_TOUCH="${WK_BENCH_PRESENCE_REQUIRE_TOUCH:-1}"

API_ADDRS="${WK_BENCH_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_BENCH_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
METRICS_ADDRS="${WK_BENCH_METRICS_ADDRS:-$API_ADDRS}"
RESOURCE_SAMPLE_INTERVAL="${WK_BENCH_RESOURCE_SAMPLE_INTERVAL:-1}"
EVIDENCE_CONNECT_TIMEOUT="${WK_BENCH_EVIDENCE_CONNECT_TIMEOUT:-2}"
EVIDENCE_MAX_TIME="${WK_BENCH_EVIDENCE_MAX_TIME:-5}"

WORKER_PID=""
CLUSTER_PID=""
RESOURCE_SAMPLER_PID=""
PRESENCE_SAMPLE_SEQ=0
CLEANUP_ZERO_STATUS="skipped"
CLEANUP_ZERO_ELAPSED_SECONDS=0
CLEANUP_ZERO_SAMPLE_COUNT=0
CLEANUP_ZERO_LAST_SNAPSHOT_NODES=0
CLEANUP_ZERO_LAST_SAMPLE_ERROR_COUNT=0
CLEANUP_ZERO_LAST_OWNER_ROUTES_ACTIVE=0
CLEANUP_ZERO_LAST_OWNER_ROUTES_PENDING=0
CLEANUP_ZERO_LAST_OWNER_TOUCHED_DIRTY=0
CLEANUP_ZERO_LAST_AUTHORITY_ROUTES_ACTIVE=0
CLEANUP_ZERO_LAST_HASH_SLOT_TOTAL=0
CLEANUP_ZERO_LAST_TOUCH_ROUTES_TOTAL=0
CLEANUP_ZERO_LAST_EXPIRED_ROUTES_TOTAL=0

usage() {
  cat <<'USAGE'
Usage: scripts/bench-wukongim-three-nodes-presence.sh [options]

Starts a local cmd/wukongim three-node cluster, runs a connection-only
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
  --start-script PATH       Three-node startup script. Default: scripts/start-wukongim-three-nodes.sh.
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
  --cleanup-timeout SECS    Wait up to this many seconds for owner/authority routes to clear. Default: 0.
  --phase-poll-timeout D    Base wkbench worker phase poll timeout. Default: 30s.
  --no-require-touch        Do not fail when touch_routes_total is zero.
  --api LIST                Comma-separated API base URLs.
  --gateway LIST            Comma-separated WKProto gateway addresses.
  --metrics LIST            Comma-separated metrics base URLs. Default: same as --api.
  --resource-interval SECS  Server process CPU/memory sample interval. 0 disables periodic sampling. Default: 1.
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

require_nonnegative_number() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]] || die "$name must be a non-negative number: $value"
}

number_greater_than_zero() {
  local value="$1"
  awk -v value="$value" 'BEGIN { exit !(value > 0) }'
}

number_greater_or_equal() {
  local left="$1"
  local right="$2"
  awk -v left="$left" -v right="$right" 'BEGIN { exit !(left >= right) }'
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
    --cleanup-timeout)
      [[ $# -ge 2 ]] || die '--cleanup-timeout requires a value'
      CLEANUP_TIMEOUT="$2"
      shift 2
      ;;
    --phase-poll-timeout)
      [[ $# -ge 2 ]] || die '--phase-poll-timeout requires a value'
      PHASE_POLL_TIMEOUT="$2"
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

require_positive_int '--users' "$USERS"
require_positive_int '--connect-rate' "$CONNECT_RATE"
require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_positive_int '--stable-samples' "$STABLE_SAMPLES"
require_nonnegative_number '--resource-interval' "$RESOURCE_SAMPLE_INTERVAL"
require_nonnegative_number '--cleanup-timeout' "$CLEANUP_TIMEOUT"
[[ "$REQUIRE_TOUCH" == "0" || "$REQUIRE_TOUCH" == "1" ]] || die "WK_BENCH_PRESENCE_REQUIRE_TOUCH must be 0 or 1"

declare -a API_VALUES GATEWAY_VALUES METRICS_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$METRICS_ADDRS" METRICS_VALUES

cleanup() {
  stop_server_resource_sampler
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
  WK_DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}" \
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

metric_file_id() {
  local raw="$1"
  raw="${raw#http://}"
  raw="${raw#https://}"
  printf '%s' "$raw" | tr -c 'A-Za-z0-9' '_'
}

capture_http_evidence() {
  local url="$1"
  local out="$2"
  local tmp="${out}.tmp"
  local stderr_tmp="${out}.stderr.tmp"
  local err_file="${out}.error"
  local status
  rm -f "$out" "$tmp" "$stderr_tmp" "$err_file"
  if curl -fsS --connect-timeout "$EVIDENCE_CONNECT_TIMEOUT" --max-time "$EVIDENCE_MAX_TIME" "$url" >"$tmp" 2>"$stderr_tmp"; then
    mv "$tmp" "$out"
    rm -f "$stderr_tmp"
    return 0
  fi
  status=$?
  {
    printf 'capture_failed status=%s url=%s\n' "$status" "$url"
    if [[ -s "$stderr_tmp" ]]; then
      cat "$stderr_tmp"
    fi
  } >"$err_file" || true
  rm -f "$tmp" "$stderr_tmp"
  return 0
}

scrape_metrics_snapshot() {
  local phase="$1"
  local metrics_dir="$OUT_DIR/metrics/cluster"
  mkdir -p "$metrics_dir"
  local addr id
  for addr in "${METRICS_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    capture_http_evidence "${addr%/}/metrics" "$metrics_dir/${id}-${phase}.prom"
  done
}

capture_node_pprof() {
  local phase="$1"
  local pprof_dir="$OUT_DIR/pprof/$phase"
  mkdir -p "$pprof_dir"
  local addr id
  for addr in "${API_VALUES[@]}"; do
    id="$(metric_file_id "$addr")"
    capture_http_evidence "${addr%/}/debug/pprof/goroutine?debug=2" "$pprof_dir/${id}-goroutine.txt"
    capture_http_evidence "${addr%/}/debug/pprof/heap" "$pprof_dir/${id}-heap.pb.gz"
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

is_nonnegative_number() {
  [[ "$1" =~ ^[0-9]+([.][0-9]+)?$ ]]
}

is_nonnegative_int() {
  [[ "$1" =~ ^[0-9]+$ ]]
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
    "$ts" "$phase" "$node_name" "$(json_escape "$reason")" >>"$OUT_DIR/resources/server-process.jsonl" || true
  return 0
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
    printf '{"timestamp":"%s","phase":"%s","node":"%s","pid":%s,"cpu_percent":%.3f,"mem_percent":%.3f,"rss_kb":%s,"vsz_kb":%s,"elapsed":"%s","command":"%s"}\n' \
      "$ts" "$phase" "$node_name" "$pid" "$cpu" "$mem" "$rss" "$vsz" "$elapsed" "$(json_escape "$command")" \
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
    printf 'node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb\n' >"$summary" || true
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
  ' "$samples" >"$summary" || {
    printf 'node\tpid\tsamples\tavg_cpu_percent\tmax_cpu_percent\tavg_mem_percent\tmax_mem_percent\tmax_rss_kb\tmax_vsz_kb\n' >"$summary" || true
    return 0
  }
  return 0
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
  mkdir -p "$OUT_DIR/config" "$OUT_DIR/logs"
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
CLEANUP_TIMEOUT=$CLEANUP_TIMEOUT
PHASE_POLL_TIMEOUT=$PHASE_POLL_TIMEOUT
HEARTBEAT_INTERVAL=$HEARTBEAT_INTERVAL
HEARTBEAT_TIMEOUT=$HEARTBEAT_TIMEOUT
SAMPLE_INTERVAL=$SAMPLE_INTERVAL
STABLE_SAMPLES=$STABLE_SAMPLES
REQUIRE_TOUCH=$REQUIRE_TOUCH
API_ADDRS=$API_ADDRS
GATEWAY_ADDRS=$GATEWAY_ADDRS
METRICS_ADDRS=$METRICS_ADDRS
RESOURCE_SAMPLE_INTERVAL=$RESOURCE_SAMPLE_INTERVAL
EVIDENCE_CONNECT_TIMEOUT=$EVIDENCE_CONNECT_TIMEOUT
EVIDENCE_MAX_TIME=$EVIDENCE_MAX_TIME
WORKER_ADDR=$WORKER_ADDR
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

run_bench() {
  local report_dir="$OUT_DIR/report"
  mkdir -p "$report_dir"
  log "running presence scenario users=$USERS"
  "$WK_BENCH_BIN" run \
    --target "$OUT_DIR/target.yaml" \
    --scenario "$OUT_DIR/scenario.yaml" \
    --workers "$OUT_DIR/workers.yaml" \
    --phase-poll-timeout "$PHASE_POLL_TIMEOUT" \
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
    if payload="$(WK_BENCH_PRESENCE_SAMPLE_PHASE="$phase" curl -fsS --max-time 3 "${api%/}/bench/v1/presence/snapshot" 2>/dev/null)"; then
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

latest_cleanup_presence_stats() {
  local samples="$OUT_DIR/presence-samples.jsonl"
  if [[ ! -s "$samples" ]]; then
    printf '0\t0\t0\t0\t0\t0\t0\t0\t0\t0\n'
    return
  fi
  jq -rs '
    def sumfield($name): (map(.[$name] // 0) | add // 0);
    def hashslotsum: (map((.authority_routes_by_hash_slot // {}) | to_entries[]? | .value // 0) | add // 0);
    [
      group_by(.sample_seq)[]
      | select((.[0].sample_phase // "") == "cleanup")
      | {
          sample_seq: (.[0].sample_seq // 0),
          snapshot_nodes: (map(select(has("sample_error") | not)) | length),
          sample_error_count: (map(select(has("sample_error"))) | length),
          owner_routes_active: sumfield("owner_routes_active"),
          owner_routes_pending: sumfield("owner_routes_pending"),
          owner_touched_dirty: sumfield("owner_touched_dirty"),
          authority_routes_active: sumfield("authority_routes_active"),
          authority_routes_by_hash_slot_total: hashslotsum,
          touch_routes_total: sumfield("touch_routes_total"),
          expired_routes_total: sumfield("expired_routes_total")
        }
    ]
    | (last // {
        sample_seq: 0,
        snapshot_nodes: 0,
        sample_error_count: 0,
        owner_routes_active: 0,
        owner_routes_pending: 0,
        owner_touched_dirty: 0,
        authority_routes_active: 0,
        authority_routes_by_hash_slot_total: 0,
        touch_routes_total: 0,
        expired_routes_total: 0
      })
    | [
        .sample_seq,
        .snapshot_nodes,
        .sample_error_count,
        .owner_routes_active,
        .owner_routes_pending,
        .owner_touched_dirty,
        .authority_routes_active,
        .authority_routes_by_hash_slot_total,
        .touch_routes_total,
        .expired_routes_total
      ] | @tsv
  ' "$samples"
}

wait_for_presence_cleanup() {
  if ! number_greater_than_zero "$CLEANUP_TIMEOUT"; then
    return 0
  fi

  log "waiting up to ${CLEANUP_TIMEOUT}s for presence cleanup to reach zero"
  CLEANUP_ZERO_STATUS="timed_out"
  local start now elapsed sleep_interval
  start="$(date +%s)"
  sleep_interval="$SAMPLE_INTERVAL"
  if ! number_greater_than_zero "$sleep_interval"; then
    sleep_interval=1
  fi

  local seq snapshots sample_errors owner_active owner_pending owner_dirty authority_active hash_slot_total touch_total expired_total
  while true; do
    collect_presence_sample "cleanup" || true
    CLEANUP_ZERO_SAMPLE_COUNT=$((CLEANUP_ZERO_SAMPLE_COUNT + 1))

    IFS=$'\t' read -r seq snapshots sample_errors owner_active owner_pending owner_dirty authority_active hash_slot_total touch_total expired_total <<<"$(latest_cleanup_presence_stats)"
    CLEANUP_ZERO_LAST_SNAPSHOT_NODES="$snapshots"
    CLEANUP_ZERO_LAST_SAMPLE_ERROR_COUNT="$sample_errors"
    CLEANUP_ZERO_LAST_OWNER_ROUTES_ACTIVE="$owner_active"
    CLEANUP_ZERO_LAST_OWNER_ROUTES_PENDING="$owner_pending"
    CLEANUP_ZERO_LAST_OWNER_TOUCHED_DIRTY="$owner_dirty"
    CLEANUP_ZERO_LAST_AUTHORITY_ROUTES_ACTIVE="$authority_active"
    CLEANUP_ZERO_LAST_HASH_SLOT_TOTAL="$hash_slot_total"
    CLEANUP_ZERO_LAST_TOUCH_ROUTES_TOTAL="$touch_total"
    CLEANUP_ZERO_LAST_EXPIRED_ROUTES_TOTAL="$expired_total"

    now="$(date +%s)"
    elapsed=$((now - start))
    CLEANUP_ZERO_ELAPSED_SECONDS="$elapsed"

    if [[ "$snapshots" -ge "${#API_VALUES[@]}" &&
          "$sample_errors" -eq 0 &&
          "$owner_active" -eq 0 &&
          "$owner_pending" -eq 0 &&
          "$owner_dirty" -eq 0 &&
          "$authority_active" -eq 0 &&
          "$hash_slot_total" -eq 0 ]]; then
      CLEANUP_ZERO_STATUS="passed"
      return 0
    fi

    if number_greater_or_equal "$elapsed" "$CLEANUP_TIMEOUT"; then
      return 0
    fi
    sleep "$sleep_interval"
  done
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
    | ($samples | map(select(.sample_phase == "run"))) as $run_samples
    | ($run_samples | max_by(([.owner_routes_active // 0, .authority_routes_active // 0] | min)) // {}) as $peak
    | (reduce $run_samples[] as $sample ({current: 0, max: 0};
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
      ($run_samples | map(.touch_routes_total // 0) | max // 0),
      ($run_samples | map(.expired_routes_total // 0) | max // 0),
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
    printf 'cleanup_zero_status\t%s\n' "$CLEANUP_ZERO_STATUS"
    printf 'cleanup_zero_elapsed_seconds\t%s\n' "$CLEANUP_ZERO_ELAPSED_SECONDS"
    printf 'cleanup_zero_sample_count\t%s\n' "$CLEANUP_ZERO_SAMPLE_COUNT"
    printf 'cleanup_zero_last_snapshot_nodes\t%s\n' "$CLEANUP_ZERO_LAST_SNAPSHOT_NODES"
    printf 'cleanup_zero_last_sample_error_count\t%s\n' "$CLEANUP_ZERO_LAST_SAMPLE_ERROR_COUNT"
    printf 'cleanup_zero_last_owner_routes_active\t%s\n' "$CLEANUP_ZERO_LAST_OWNER_ROUTES_ACTIVE"
    printf 'cleanup_zero_last_owner_routes_pending\t%s\n' "$CLEANUP_ZERO_LAST_OWNER_ROUTES_PENDING"
    printf 'cleanup_zero_last_owner_touched_dirty\t%s\n' "$CLEANUP_ZERO_LAST_OWNER_TOUCHED_DIRTY"
    printf 'cleanup_zero_last_authority_routes_active\t%s\n' "$CLEANUP_ZERO_LAST_AUTHORITY_ROUTES_ACTIVE"
    printf 'cleanup_zero_last_authority_routes_by_hash_slot_total\t%s\n' "$CLEANUP_ZERO_LAST_HASH_SLOT_TOTAL"
    printf 'cleanup_zero_last_touch_routes_total\t%s\n' "$CLEANUP_ZERO_LAST_TOUCH_ROUTES_TOTAL"
    printf 'cleanup_zero_last_expired_routes_total\t%s\n' "$CLEANUP_ZERO_LAST_EXPIRED_ROUTES_TOTAL"
    printf 'failures\t%s\n' "$failures"
  } >"$out"

  if [[ "$presence_status" != "passed" ]]; then
    cat "$out" >&2
    return 1
  fi
}

write_evidence_summary() {
  local presence_status
  presence_status="$(awk -F'\t' '$1 == "presence_status" { print $2 }' "$OUT_DIR/presence-summary.tsv" 2>/dev/null || true)"
  local cleanup_zero_status
  cleanup_zero_status="$(awk -F'\t' '$1 == "cleanup_zero_status" { print $2 }' "$OUT_DIR/presence-summary.tsv" 2>/dev/null || true)"
  cat >"$OUT_DIR/summary.md" <<EOF
# Three-Node Presence Bench Evidence

## Scenario
- workload: local wukongim three-node cluster wkbench connection presence
- users: $USERS
- connect_rate: ${CONNECT_RATE}/s
- duration: $DURATION
- cooldown: $COOLDOWN
- cleanup_timeout: $CLEANUP_TIMEOUT
- heartbeat_interval: $HEARTBEAT_INTERVAL
- stable_samples: $STABLE_SAMPLES
- clean_cluster: $CLEAN_CLUSTER

## Evidence
- git: git.txt
- env: env.txt
- start_plan: start-plan.txt
- config: config/
- metrics: metrics/cluster/
- pprof: pprof/
- resources: resources/
- logs: logs/
- target: target.yaml
- workers: workers.yaml
- scenario: scenario.yaml
- console: wkbench-console.txt
- report: report/report.json
- live_presence_samples: presence-samples.jsonl
- presence_summary: presence-summary.tsv

## Result
- presence_status: ${presence_status:-unknown}
- cleanup_zero_status: ${cleanup_zero_status:-skipped}
EOF
}

print_summary() {
  write_evidence_summary
  cat "$OUT_DIR/presence-summary.tsv" 2>/dev/null || true
  log "details:"
  printf '  report: %s\n' "$OUT_DIR/report/report.json"
  printf '  presence: %s\n' "$OUT_DIR/presence-summary.tsv"
  printf '  metrics: %s\n' "$OUT_DIR/metrics/cluster"
  printf '  pprof: %s\n' "$OUT_DIR/pprof"
  printf '  resources: %s\n' "$OUT_DIR/resources"
  printf '  logs: %s\n' "$OUT_DIR/logs"
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
  collect_node_logs before
  scrape_metrics_snapshot before
  capture_node_pprof before
  start_server_resource_sampler

  local status=0
  run_bench || status=$?
  if [[ "$status" -eq 0 ]]; then
    wait_for_presence_cleanup || true
  fi

  stop_server_resource_sampler
  sample_server_resources after || true
  write_server_resource_summary || true
  scrape_metrics_snapshot after
  capture_node_pprof after
  collect_node_logs after

  if [[ "$status" -ne 0 ]]; then
    print_summary
    cat "$OUT_DIR/wkbench-console.txt" >&2 2>/dev/null || true
    exit "$status"
  fi

  validate_presence_report || status=$?
  print_summary
  exit "$status"
}

main "$@"
