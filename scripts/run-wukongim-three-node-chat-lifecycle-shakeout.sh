#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
RUN_DIR=""
BASE_PORT=15000
READY_TIMEOUT=120
STOP_AFTER=0
SEND_RATE=100
MEASURE_SECONDS=0
WARMUP_SECONDS=60
MINIMUM_THROUGHPUT_PERCENT=90
METRICS_SAMPLE_SECONDS=5
DRY_RUN=0
CLEANUP_TIMEOUT=15
GRACEFUL_STOP_TIMEOUT=90

PIDS=()
NAMES=()
COORDINATOR_PID=""
STOP_SENT=0
GRACEFUL_STOP_DEADLINE=0

usage() {
  sed -n '/^# Usage:/,/^#   -h, --help/p' "$0" | sed 's/^# \{0,1\}//'
}

# Usage: run-wukongim-three-node-chat-lifecycle-shakeout.sh --run-dir DIR [options]
#
# Builds one WuKongIM binary and one wkbench binary, then starts three service
# nodes, three authenticated lifecycle workers, four local filesystem metrics
# endpoints, and one coordinator. Every artifact stays below the fresh run dir.
#
# Options:
#   --run-dir DIR       Required fresh artifact, data, PID, log, and report root.
#   --base-port PORT    First port in a reserved 64-port range (default 15000).
#   --ready-timeout S   Readiness deadline in seconds (default 120).
#   --stop-after S      Request graceful coordinator stop after S seconds; 0 waits.
#   --send-rate N       Offered SEND rate per second (default 100).
#   --measure-seconds S After warmup, measure for S seconds; 0 keeps legacy mode.
#   --warmup-seconds S  Traffic warmup before a measured step (default 60).
#   --drain-timeout S   Maximum graceful drain in seconds (default 90).
#   --dry-run           Print the resolved topology without building or writing.
#   -h, --help          Show this help.

log() { printf '[chat-lifecycle-shakeout] %s\n' "$*"; }
die() { printf '[chat-lifecycle-shakeout] ERROR: %s\n' "$*" >&2; exit 1; }

require_uint() {
  local name="$1" value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer"
}

absolute_path() {
  local value="$1" parent base
  if [[ "$value" != /* ]]; then
    value="$PWD/$value"
  fi
  while [[ "$value" != / && "$value" == */ ]]; do
    value="${value%/}"
  done
  if [[ "$value" == / ]]; then
    printf '/'
    return
  fi
  parent="$(dirname "$value")"
  base="$(basename "$value")"
  [[ -d "$parent" ]] || die "--run-dir parent does not exist: $parent"
  parent="$(cd "$parent" && pwd -P)"
  printf '%s/%s' "$parent" "$base"
}

validate_run_dir() {
  local home_dir
  [[ -n "$RUN_DIR" ]] || die '--run-dir is required'
  RUN_DIR="$(absolute_path "$RUN_DIR")"
  home_dir="$(cd "$HOME" && pwd -P)"
  case "$RUN_DIR" in
    /|"$ROOT_DIR"|"$home_dir") die "unsafe --run-dir: $RUN_DIR" ;;
  esac
}

port() { printf '%d' "$((BASE_PORT + $1))"; }
api_port() { port "$1"; }
cluster_port() { port "$((10 + $1))"; }
gateway_port() { port "$((20 + $1))"; }
ws_port() { port "$((30 + $1))"; }
manager_port() { port "$((40 + $1))"; }
worker_port() { port "$((50 + $1))"; }
host_metrics_port() { port "$((60 + $1))"; }
load_host_metrics_port() { port 60; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-dir) [[ $# -ge 2 ]] || die '--run-dir requires a value'; RUN_DIR="$2"; shift 2 ;;
    --base-port) [[ $# -ge 2 ]] || die '--base-port requires a value'; BASE_PORT="$2"; shift 2 ;;
    --ready-timeout) [[ $# -ge 2 ]] || die '--ready-timeout requires a value'; READY_TIMEOUT="$2"; shift 2 ;;
    --stop-after) [[ $# -ge 2 ]] || die '--stop-after requires a value'; STOP_AFTER="$2"; shift 2 ;;
    --send-rate) [[ $# -ge 2 ]] || die '--send-rate requires a value'; SEND_RATE="$2"; shift 2 ;;
    --measure-seconds) [[ $# -ge 2 ]] || die '--measure-seconds requires a value'; MEASURE_SECONDS="$2"; shift 2 ;;
    --warmup-seconds) [[ $# -ge 2 ]] || die '--warmup-seconds requires a value'; WARMUP_SECONDS="$2"; shift 2 ;;
    --drain-timeout) [[ $# -ge 2 ]] || die '--drain-timeout requires a value'; GRACEFUL_STOP_TIMEOUT="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

require_uint '--base-port' "$BASE_PORT"
require_uint '--ready-timeout' "$READY_TIMEOUT"
require_uint '--stop-after' "$STOP_AFTER"
require_uint '--send-rate' "$SEND_RATE"
require_uint '--measure-seconds' "$MEASURE_SECONDS"
require_uint '--warmup-seconds' "$WARMUP_SECONDS"
require_uint '--drain-timeout' "$GRACEFUL_STOP_TIMEOUT"
(( BASE_PORT >= 1024 && BASE_PORT <= 65472 )) || die '--base-port must reserve 64 ports within 1024..65535'
(( READY_TIMEOUT > 0 )) || die '--ready-timeout must be greater than zero'
(( SEND_RATE > 0 )) || die '--send-rate must be greater than zero'
(( SEND_RATE <= 1000000 )) || die '--send-rate must not exceed 1000000'
(( WARMUP_SECONDS > 0 )) || die '--warmup-seconds must be greater than zero'
(( GRACEFUL_STOP_TIMEOUT > 0 )) || die '--drain-timeout must be greater than zero'
(( STOP_AFTER == 0 || MEASURE_SECONDS == 0 )) || die '--stop-after and --measure-seconds are mutually exclusive'
validate_run_dir

WUKONGIM_BIN="$RUN_DIR/bin/wukongim"
WKBENCH_BIN="$RUN_DIR/bin/wkbench"
CONFIG_DIR="$RUN_DIR/config"
DATA_DIR="$RUN_DIR/data"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
WORKER_DIR="$RUN_DIR/workers"
REPORT_DIR="$RUN_DIR/report"
EVIDENCE_DIR="$RUN_DIR/evidence"
METRICS_DIR="$RUN_DIR/metrics"
LIFECYCLE_CONFIG="$RUN_DIR/chat-lifecycle.yaml"

print_plan() {
  printf 'run_dir=%s\n' "$RUN_DIR"
  printf 'build_wukongim=GOWORK=off go build -o %s ./cmd/wukongim\n' "$WUKONGIM_BIN"
  printf 'build_wkbench=GOWORK=off go build -o %s ./cmd/wkbench\n' "$WKBENCH_BIN"
  printf 'logical_slot_groups=12\n'
  printf 'hash_slots=256\n'
  printf 'replicas=3/3\n'
  printf 'online_connections=2500\n'
  printf 'offered_send_rate_per_second=%s\n' "$SEND_RATE"
  printf 'measured_duration_seconds=%s\n' "$MEASURE_SECONDS"
  printf 'warmup_seconds=%s\n' "$WARMUP_SECONDS"
  printf 'drain_timeout_seconds=%s\n' "$GRACEFUL_STOP_TIMEOUT"
  printf 'commit_coordinator_flush_window=200us\n'
  printf 'commit_coordinator_shards=1\n'
  printf 'sync_commit=true\n'
  printf 'coordinator_config=%s\n' "$LIFECYCLE_CONFIG"
  printf 'report_dir=%s\n' "$REPORT_DIR"
  local node
  for node in 1 2 3; do
    printf 'service_%s=http://127.0.0.1:%s\n' "$node" "$(api_port "$node")"
    printf 'gateway_%s=127.0.0.1:%s\n' "$node" "$(gateway_port "$node")"
    printf 'worker_%s=http://127.0.0.1:%s\n' "$node" "$(worker_port "$node")"
    printf 'host_metrics_%s=http://127.0.0.1:%s\n' "$node" "$(host_metrics_port "$node")"
  done
  printf 'host_metrics_load=http://127.0.0.1:%s\n' "$(load_host_metrics_port)"
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

command -v go >/dev/null 2>&1 || die 'go is required'
command -v curl >/dev/null 2>&1 || die 'curl is required'
command -v awk >/dev/null 2>&1 || die 'awk is required'
command -v ps >/dev/null 2>&1 || die 'ps is required'
[[ -n "${WK_BENCH_API_TOKEN:-}" ]] || die 'WK_BENCH_API_TOKEN is required'
[[ -n "${WK_BENCH_WORKER_TOKEN:-}" ]] || die 'WK_BENCH_WORKER_TOKEN is required'
if [[ -e "$RUN_DIR" ]] && [[ -n "$(find "$RUN_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  die "--run-dir must be absent or empty: $RUN_DIR"
fi

overlap="$(ps -axo pid=,comm= | awk -v self="$$" '
  {
    command = $2
    sub(/^.*\//, "", command)
    if ($1 != self && (command == "wukongim" || command == "wkbench")) print $0
  }
')"
[[ -z "$overlap" ]] || die "host_confounded: another wukongim or wkbench process is active: $overlap"

mkdir -p "$RUN_DIR/bin" "$CONFIG_DIR" "$DATA_DIR/load" "$LOG_DIR" "$PID_DIR" "$WORKER_DIR" "$REPORT_DIR" "$EVIDENCE_DIR" "$METRICS_DIR"
for node in 1 2 3; do
  mkdir -p "$DATA_DIR/node$node" "$LOG_DIR/node$node" "$WORKER_DIR/node$node"
  cp "$ROOT_DIR/scripts/wukongim/wukongim-node$node.toml" "$CONFIG_DIR/node$node.toml"
done

log 'building service and benchmark binaries'
(cd "$ROOT_DIR" && GOWORK=off go build -o "$WUKONGIM_BIN" ./cmd/wukongim)
(cd "$ROOT_DIR" && GOWORK=off go build -o "$WKBENCH_BIN" ./cmd/wkbench)

sed \
  -e "s/15001/$(api_port 1)/g" -e "s/15002/$(api_port 2)/g" -e "s/15003/$(api_port 3)/g" \
  -e "s/15011/$(api_port 1)/g" -e "s/15012/$(api_port 2)/g" -e "s/15013/$(api_port 3)/g" \
  -e "s/15101/$(gateway_port 1)/g" -e "s/15102/$(gateway_port 2)/g" -e "s/15103/$(gateway_port 3)/g" \
  -e "s/19091/$(worker_port 1)/g" -e "s/19092/$(worker_port 2)/g" -e "s/19093/$(worker_port 3)/g" \
  -e "s/19101/$(host_metrics_port 1)/g" -e "s/19102/$(host_metrics_port 2)/g" -e "s/19103/$(host_metrics_port 3)/g" \
  -e "s/19104/$(load_host_metrics_port)/g" \
	-e "s/send_rate_per_second: 100/send_rate_per_second: $SEND_RATE/" \
	-e "s/max_global_burst: 200/max_global_burst: $((SEND_RATE * 2))/" \
  "$ROOT_DIR/configs/wkbench/chat-lifecycle/local-shakeout.yaml" >"$LIFECYCLE_CONFIG"

if (( MEASURE_SECONDS > 0 )); then
  checkpoint_milliseconds=$((WARMUP_SECONDS * 1000 + 1))
  final_seconds=$((WARMUP_SECONDS + MEASURE_SECONDS + GRACEFUL_STOP_TIMEOUT + 60))
  sed -e "s/timeline: {warmup: 10m, checkpoint: 20m, final: 30m}/timeline: {warmup: ${WARMUP_SECONDS}s, checkpoint: ${checkpoint_milliseconds}ms, final: ${final_seconds}s}/" \
    "$LIFECYCLE_CONFIG" >"$LIFECYCLE_CONFIG.next"
  mv "$LIFECYCLE_CONFIG.next" "$LIFECYCLE_CONFIG"
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

record_evidence_identity() {
  local revision dirty config_sha
  revision="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)"
  if git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD -- 2>/dev/null &&
    [[ -z "$(git -C "$ROOT_DIR" ls-files --others --exclude-standard 2>/dev/null)" ]]; then
    dirty=false
  else
    dirty=true
  fi
  config_sha="$(sha256_file "$LIFECYCLE_CONFIG")"
  {
    printf 'schema\twukongim/chat-lifecycle-local-evidence/v1\n'
    printf 'source_revision\t%s\n' "$revision"
    printf 'source_dirty\t%s\n' "$dirty"
    printf 'config_sha256\t%s\n' "$config_sha"
    printf 'online_connections\t2500\n'
    printf 'offered_send_rate_per_second\t%s\n' "$SEND_RATE"
    printf 'measured_duration_seconds\t%s\n' "$MEASURE_SECONDS"
    printf 'logical_slot_groups\t12\n'
    printf 'hash_slots\t256\n'
    printf 'slot_replicas\t3\n'
    printf 'channel_replicas\t3\n'
    printf 'commit_coordinator_flush_window\t200us\n'
    printf 'commit_coordinator_shards\t1\n'
    printf 'sync_commit\ttrue\n'
    printf 'physical_io_source\thost-node-1\n'
  } >"$EVIDENCE_DIR/identity.tsv"
  df -Pk "$RUN_DIR" >"$EVIDENCE_DIR/filesystem-preflight.txt"
}

record_evidence_identity

record_pid() {
  local name="$1" pid="$2"
  NAMES+=("$name")
  PIDS+=("$pid")
  printf '%s\n' "$pid" >"$PID_DIR/$name.pid"
}

terminate_recorded() {
  local original_status=$? pid deadline alive
  if [[ "${#PIDS[@]}" -eq 0 ]]; then
    return "$original_status"
  fi
  for pid in "${PIDS[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  deadline=$((SECONDS + CLEANUP_TIMEOUT))
  while (( SECONDS < deadline )); do
    alive=0
    for pid in "${PIDS[@]}"; do
      kill -0 "$pid" 2>/dev/null && alive=1
    done
    [[ "$alive" -eq 0 ]] && break
    sleep 1
  done
  for pid in "${PIDS[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
    wait "$pid" 2>/dev/null || true
  done
  return "$original_status"
}

request_coordinator_stop() {
  local reason="$1"
  [[ -n "$COORDINATOR_PID" ]] && kill -0 "$COORDINATOR_PID" 2>/dev/null || return 1
  log "$reason: forwarding one TERM to the coordinator"
  kill -TERM "$COORDINATOR_PID" 2>/dev/null || return 1
  STOP_SENT=1
  GRACEFUL_STOP_DEADLINE=$((SECONDS + GRACEFUL_STOP_TIMEOUT))
}

handle_signal() {
  local signal_name="$1" exit_status="$2"
  if [[ "$STOP_SENT" -eq 0 ]] && request_coordinator_stop "received $signal_name"; then
    return
  fi
  log "received $signal_name after graceful stop was unavailable or already requested; forcing cleanup"
  trap - INT TERM
  exit "$exit_status"
}

trap terminate_recorded EXIT
trap 'handle_signal INT 130' INT
trap 'handle_signal TERM 143' TERM

CLUSTER_NODES="[{\"id\":1,\"addr\":\"127.0.0.1:$(cluster_port 1)\"},{\"id\":2,\"addr\":\"127.0.0.1:$(cluster_port 2)\"},{\"id\":3,\"addr\":\"127.0.0.1:$(cluster_port 3)\"}]"

start_service() {
  local node="$1" gateway_listeners pid
  gateway_listeners="[{\"name\":\"tcp-wkproto\",\"network\":\"tcp\",\"address\":\"127.0.0.1:$(gateway_port "$node")\",\"transport\":\"gnet\",\"protocol\":\"wkproto\"},{\"name\":\"ws-gateway\",\"network\":\"websocket\",\"address\":\"127.0.0.1:$(ws_port "$node")\",\"transport\":\"gnet\",\"protocol\":\"wsmux\"}]"
  env -u WK_BENCH_WORKER_TOKEN -u WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE \
    WK_NODE_ID="$node" \
    WK_NODE_DATA_DIR="$DATA_DIR/node$node" \
    WK_CLUSTER_LISTEN_ADDR="127.0.0.1:$(cluster_port "$node")" \
    WK_CLUSTER_NODES="$CLUSTER_NODES" \
    WK_CLUSTER_INITIAL_SLOT_COUNT=12 \
    WK_CLUSTER_HASH_SLOT_COUNT=256 \
    WK_CLUSTER_SLOT_REPLICA_N=3 \
    WK_CLUSTER_CHANNEL_REPLICA_N=3 \
    WK_CLUSTER_MAX_CHANNELS=50000 \
    WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=200us \
    WK_CLUSTER_COMMIT_COORDINATOR_SHARDS=1 \
    WK_CLUSTER_COMMIT_COORDINATOR_SYNC=true \
    WK_API_LISTEN_ADDR="127.0.0.1:$(api_port "$node")" \
    WK_EXTERNAL_TCPADDR="127.0.0.1:$(gateway_port "$node")" \
    WK_MANAGER_LISTEN_ADDR="127.0.0.1:$(manager_port "$node")" \
    WK_GATEWAY_LISTENERS="$gateway_listeners" \
    WK_PLUGIN_SOCKET_PATH="${TMPDIR:-/tmp}/wkcl-$$-$node.sock" \
    WK_METRICS_ENABLE=true \
    WK_DEBUG_API_ENABLE=true \
    WK_BENCH_API_ENABLE=true \
    WK_PROMETHEUS_ENABLE=false \
    WK_LOG_DIR="$LOG_DIR/node$node" \
    "$WUKONGIM_BIN" -config "$CONFIG_DIR/node$node.toml" >"$LOG_DIR/service-$node.log" 2>&1 &
  pid=$!
  record_pid "service-$node" "$pid"
}

start_worker() {
  local node="$1" pid
  "$WKBENCH_BIN" worker --mode chat-lifecycle --listen "127.0.0.1:$(worker_port "$node")" \
    --work-dir "$WORKER_DIR/node$node" >"$LOG_DIR/worker-$node.log" 2>&1 &
  pid=$!
  record_pid "worker-$node" "$pid"
}

process_pid() {
  local wanted="$1" index
  for index in "${!NAMES[@]}"; do
    if [[ "${NAMES[$index]}" == "$wanted" ]]; then
      printf '%s' "${PIDS[$index]}"
      return
    fi
  done
}

process_cpu_jiffies_and_rss() {
  local pid="$1" sample clock_ticks
  sample="$(ps -p "$pid" -o time= -o rss= 2>/dev/null)" || return 1
  clock_ticks="$(getconf CLK_TCK 2>/dev/null || printf 100)"
  [[ "$clock_ticks" =~ ^[0-9]+$ && "$clock_ticks" -gt 0 ]] || clock_ticks=100
  awk -v clock_ticks="$clock_ticks" '
    NF >= 2 {
      rss = $NF + 0
      cpu = $1
      days = 0
      if (index(cpu, "-") > 0) {
        split(cpu, day_parts, "-")
        days = day_parts[1] + 0
        cpu = day_parts[2]
      }
      count = split(cpu, parts, ":")
      seconds = parts[count] + 0
      if (count >= 2) seconds += (parts[count - 1] + 0) * 60
      if (count >= 3) seconds += (parts[count - 2] + 0) * 3600
      seconds += days * 86400
      if (rss <= 0 || seconds < 0) exit 1
      printf "%.0f %.0f\n", seconds * clock_ticks, rss * 1024
      found = 1
    }
    END { if (!found) exit 1 }
  ' <<<"$sample"
}

write_process_metrics_for_host() {
  local host="$1" output="$2" temporary unit name pid values cpu rss
  temporary="$output.next"
  local -a units=(
    wukongim.service wkbench-host-metrics.service wkbench-worker@1.service wkbench-worker@2.service
    wkbench-worker@3.service wkbench-coordinator.service wkbench-formal.service wkbench-rehearsal.service
    prometheus.service caddy.service wkanalysis.service wukongim-process-metrics.service node-exporter.service
  )
  : >"$temporary"
  for unit in "${units[@]}"; do
    name=""
    case "$host:$unit" in
      1:wukongim.service) name=service-1 ;;
      2:wukongim.service) name=service-2 ;;
      3:wukongim.service) name=service-3 ;;
      1:wkbench-host-metrics.service) name=host-metrics-1 ;;
      2:wkbench-host-metrics.service) name=host-metrics-2 ;;
      3:wkbench-host-metrics.service) name=host-metrics-3 ;;
      load:wkbench-host-metrics.service) name=host-metrics-load ;;
      load:wkbench-worker@1.service) name=worker-1 ;;
      load:wkbench-worker@2.service) name=worker-2 ;;
      load:wkbench-worker@3.service) name=worker-3 ;;
      load:wkbench-coordinator.service) name=coordinator ;;
    esac
    pid=""
    [[ -z "$name" ]] || pid="$(process_pid "$name")"
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
      printf 'wukongim_process_up{unit="%s"} 0\n' "$unit" >>"$temporary"
      continue
    fi
    values="$(process_cpu_jiffies_and_rss "$pid")" || return 1
    read -r cpu rss <<<"$values"
    printf 'wukongim_process_up{unit="%s"} 1\n' "$unit" >>"$temporary"
    printf 'wukongim_process_cpu_jiffies_total{unit="%s"} %s\n' "$unit" "$cpu" >>"$temporary"
    printf 'wukongim_process_resident_memory_bytes{unit="%s"} %s\n' "$unit" "$rss" >>"$temporary"
  done
  printf 'wukongim_process_collector_last_success_unixtime_seconds %s\n' "$(date +%s)" >>"$temporary"
  mv "$temporary" "$output"
}

refresh_process_metrics() {
  local node
  for node in 1 2 3; do
    write_process_metrics_for_host "$node" "$EVIDENCE_DIR/processes-node-$node.prom" || return 1
  done
  write_process_metrics_for_host load "$EVIDENCE_DIR/processes-load.prom"
}

start_process_metrics_collector() {
  local pid
  refresh_process_metrics || die 'process metrics collector initial refresh failed'
  (
    while true; do
      refresh_process_metrics || exit 1
      sleep 5
    done
  ) >"$LOG_DIR/process-metrics.log" 2>&1 &
  pid=$!
  record_pid process-metrics-collector "$pid"
}

start_host_metrics() {
  local node="$1" pid
  local -a physical_io_args=(--physical-io=true)
  if [[ "$node" -ne 1 ]]; then
    physical_io_args=(--physical-io=false)
  fi
  write_process_metrics_for_host "$node" "$EVIDENCE_DIR/processes-node-$node.prom" || die "node $node process metrics initialization failed"
  "$WKBENCH_BIN" host-metrics --listen "127.0.0.1:$(host_metrics_port "$node")" \
    --path "$DATA_DIR/node$node" --mountpoint "/var/lib/wukongim-$node" --device "/dev/local-data-$node" \
    --process-metrics-path "$EVIDENCE_DIR/processes-node-$node.prom" \
    "${physical_io_args[@]}" \
    >"$LOG_DIR/host-metrics-$node.log" 2>&1 &
  pid=$!
  record_pid "host-metrics-$node" "$pid"
  write_process_metrics_for_host "$node" "$EVIDENCE_DIR/processes-node-$node.prom" || die "node $node process metrics refresh failed"
}

start_load_host_metrics() {
  local pid
  write_process_metrics_for_host load "$EVIDENCE_DIR/processes-load.prom" || die 'load process metrics initialization failed'
  "$WKBENCH_BIN" host-metrics --listen "127.0.0.1:$(load_host_metrics_port)" \
    --path "$DATA_DIR/load" --mountpoint "/var/lib/wukongim-load" --device "/dev/local-load-data" \
    --watch-path "$REPORT_DIR" --process-metrics-path "$EVIDENCE_DIR/processes-load.prom" \
    --physical-io=false \
    >"$LOG_DIR/host-metrics-load.log" 2>&1 &
  pid=$!
  record_pid host-metrics-load "$pid"
  write_process_metrics_for_host load "$EVIDENCE_DIR/processes-load.prom" || die 'load process metrics refresh failed'
}

wait_url() {
  local name="$1" url="$2" token="${3:-}" deadline pid
  pid="$(<"$PID_DIR/$name.pid")"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      tail -n 80 "$LOG_DIR/$name.log" >&2 || true
      die "$name exited before readiness"
    fi
    if [[ -n "$token" ]]; then
      curl -fsS --max-time 2 -H "Authorization: Bearer $token" "$url" >/dev/null 2>&1 && {
        log "$name ready: $url"
        return
      }
    elif curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      log "$name ready: $url"
      return
    fi
    sleep 1
  done
  die "$name readiness timed out: $url"
}

for node in 1 2 3; do start_service "$node"; done
for node in 1 2 3; do wait_url "service-$node" "http://127.0.0.1:$(api_port "$node")/readyz"; done
for node in 1 2 3; do start_worker "$node"; start_host_metrics "$node"; done
start_load_host_metrics
for node in 1 2 3; do
  wait_url "worker-$node" "http://127.0.0.1:$(worker_port "$node")/healthz" "$WK_BENCH_WORKER_TOKEN"
  wait_url "host-metrics-$node" "http://127.0.0.1:$(host_metrics_port "$node")/healthz"
done
wait_url host-metrics-load "http://127.0.0.1:$(load_host_metrics_port)/healthz"

printf 'observed_at_utc\tphase\tnode\tstatus\n' >"$EVIDENCE_DIR/timeline.tsv"

record_timeline_boundary() {
  printf '%s\t%s\tboundary\tcomplete\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" >>"$EVIDENCE_DIR/timeline.tsv"
}

capture_metric_target() {
  local url="$1" timeout="$2" destination="$3" temporary="$3.next"
  if curl -fsS --max-time "$timeout" "$url" >"$temporary"; then
    mv "$temporary" "$destination"
    return 0
  fi
  rm -f "$temporary"
  : >"$destination"
  return 1
}

capture_service_metrics() {
  local phase="$1" node destination observed status host_name index
  local -a capture_pids=() capture_names=()
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for node in 1 2 3; do
    destination="$METRICS_DIR/node-$node-$phase.prom"
    capture_metric_target "http://127.0.0.1:$(api_port "$node")/metrics" 3 "$destination" &
    capture_pids+=("$!")
    capture_names+=("node-$node")
  done
  for node in 1 2 3; do
    host_name="host-node-$node"
    destination="$METRICS_DIR/$host_name-$phase.prom"
    capture_metric_target "http://127.0.0.1:$(host_metrics_port "$node")/metrics" 6 "$destination" &
    capture_pids+=("$!")
    capture_names+=("$host_name")
  done
  destination="$METRICS_DIR/host-load-$phase.prom"
  capture_metric_target "http://127.0.0.1:$(load_host_metrics_port)/metrics" 6 "$destination" &
  capture_pids+=("$!")
  capture_names+=(host-load)
  for index in "${!capture_pids[@]}"; do
    status=complete
    wait "${capture_pids[$index]}" || status=missing
    printf '%s\t%s\t%s\t%s\n' "$observed" "$phase" "${capture_names[$index]}" "$status" >>"$EVIDENCE_DIR/timeline.tsv"
  done
}

summarize_storage_metrics() {
  local output="$RUN_DIR/storage_metrics_summary.tsv" node before after
  local -a samples files
  awk -v header=1 -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" /dev/null >"$output"
  for node in 1 2 3; do
    before="$METRICS_DIR/node-$node-before.prom"
    after="$METRICS_DIR/node-$node-after.prom"
    samples=("$METRICS_DIR/node-$node-sample-"*.prom)
    files=()
    [[ -f "$before" ]] && files+=("$before")
    if [[ -e "${samples[0]}" ]]; then
      files+=("${samples[@]}")
    fi
    [[ -f "$after" ]] && files+=("$after")
    if [[ "${#files[@]}" -ge 2 ]]; then
      awk -v tag="rate-$SEND_RATE" -v node="node-$node" -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" "${files[@]}" >>"$output"
    else
      awk -v tag="rate-$SEND_RATE" -v node="node-$node" -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" /dev/null /dev/null >>"$output"
    fi
  done
}

summarize_host_io() {
  local output="$RUN_DIR/host_io_summary.tsv" host node
  local -a files
  awk -v header=1 -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >"$output"
  for node in 1 2 3; do
    host="host-node-$node"
    files=("$METRICS_DIR/$host-"*.prom)
    if [[ -e "${files[0]}" ]]; then
      awk -v tag="rate-$SEND_RATE" -v host="$host" -f "$ROOT_DIR/scripts/host-io-summary.awk" "${files[@]}" >>"$output"
    else
      awk -v tag="rate-$SEND_RATE" -v host="$host" -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >>"$output"
    fi
  done
  files=("$METRICS_DIR/host-load-"*.prom)
  if [[ -e "${files[0]}" ]]; then
    awk -v tag="rate-$SEND_RATE" -v host=host-load -f "$ROOT_DIR/scripts/host-io-summary.awk" "${files[@]}" >>"$output"
  else
    awk -v tag="rate-$SEND_RATE" -v host=host-load -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >>"$output"
  fi
}

record_process_continuity() {
  local output="$EVIDENCE_DIR/process-continuity.tsv" name pid alive index
  printf 'name\talive\n' >"$output"
  for name in service-1 service-2 service-3 worker-1 worker-2 worker-3 host-metrics-1 host-metrics-2 host-metrics-3 host-metrics-load process-metrics-collector; do
    alive=false
    for index in "${!NAMES[@]}"; do
      if [[ "${NAMES[$index]}" == "$name" ]]; then
        pid="${PIDS[$index]}"
        kill -0 "$pid" 2>/dev/null && alive=true
        break
      fi
    done
    printf '%s\t%s\n' "$name" "$alive" >>"$output"
  done
}

write_artifact_checksums() {
  local output="$EVIDENCE_DIR/checksums.sha256" path digest
  : >"$output"
  while IFS= read -r path; do
    [[ "$path" == "$output" ]] && continue
    digest="$(sha256_file "$path")"
    printf '%s  %s\n' "$digest" "${path#"$RUN_DIR"/}" >>"$output"
  done < <(find "$CONFIG_DIR" "$REPORT_DIR" "$EVIDENCE_DIR" "$METRICS_DIR" \
    "$RUN_DIR/storage_metrics_summary.tsv" "$RUN_DIR/host_io_summary.tsv" "$RUN_DIR/local-step.json" \
    -type f -print | LC_ALL=C sort)
}

log 'starting coordinator'
record_timeline_boundary warmup_start
"$WKBENCH_BIN" soak chat-lifecycle --config "$LIFECYCLE_CONFIG" --output-dir "$REPORT_DIR" \
  >"$LOG_DIR/coordinator.log" 2>&1 &
COORDINATOR_PID=$!
record_pid coordinator "$COORDINATOR_PID"
start_process_metrics_collector

started_at=$SECONDS
qualification_seen=0
measurement_deadline=0
next_metrics_at=0
metrics_sequence=0
while kill -0 "$COORDINATOR_PID" 2>/dev/null; do
  if (( MEASURE_SECONDS > 0 && qualification_seen == 0 )) && [[ -s "$REPORT_DIR/qualification.json" ]]; then
    measurement_deadline=$((SECONDS + MEASURE_SECONDS))
    record_timeline_boundary warmup_end
    record_timeline_boundary measurement_start
    capture_service_metrics before
    qualification_seen=1
    next_metrics_at=$((SECONDS + METRICS_SAMPLE_SECONDS))
    log "warmup evidence complete; measuring ${MEASURE_SECONDS}s at ${SEND_RATE} offered SEND/s"
  fi
  if (( qualification_seen == 1 && STOP_SENT == 0 && SECONDS >= measurement_deadline )); then
    record_timeline_boundary measurement_end
    record_timeline_boundary drain_start
    request_coordinator_stop 'measured interval elapsed' || die 'coordinator exited before measured stop request'
    metrics_sequence=$((metrics_sequence + 1))
    capture_service_metrics "sample-$metrics_sequence"
  elif (( qualification_seen == 1 && STOP_SENT == 0 && SECONDS >= next_metrics_at &&
    SECONDS + METRICS_SAMPLE_SECONDS * 2 < measurement_deadline )); then
    metrics_sequence=$((metrics_sequence + 1))
    capture_service_metrics "sample-$metrics_sequence"
    next_metrics_at=$((SECONDS + METRICS_SAMPLE_SECONDS))
  fi
  if (( STOP_AFTER > 0 && STOP_SENT == 0 && SECONDS - started_at >= STOP_AFTER )); then
    request_coordinator_stop '--stop-after elapsed' || die 'coordinator exited before graceful stop request'
  fi
  if (( GRACEFUL_STOP_DEADLINE > 0 && SECONDS >= GRACEFUL_STOP_DEADLINE )); then
    die "coordinator did not finish graceful stop within ${GRACEFUL_STOP_TIMEOUT}s"
  fi
  sleep 1 || true
done

coordinator_status=0
wait "$COORDINATOR_PID" || coordinator_status=$?
if (( MEASURE_SECONDS > 0 )); then
  capture_service_metrics after
  record_timeline_boundary drain_end
  record_timeline_boundary shutdown_start
  summarize_storage_metrics
  summarize_host_io
  record_process_continuity
  classifier_status=0
  "$WKBENCH_BIN" report local-chat-lifecycle-step \
    --before "$REPORT_DIR/qualification.json" \
    --after "$REPORT_DIR/final.json" \
    --storage-summary "$RUN_DIR/storage_metrics_summary.tsv" \
    --host-io-summary "$RUN_DIR/host_io_summary.tsv" \
    --process-continuity "$EVIDENCE_DIR/process-continuity.tsv" \
    --output "$RUN_DIR/local-step.json" \
    --offered-rate "$SEND_RATE" \
    --measured-duration "${MEASURE_SECONDS}s" \
    --minimum-throughput-percent "$MINIMUM_THROUGHPUT_PERCENT" || classifier_status=$?
  write_artifact_checksums
  if [[ -s "$RUN_DIR/local-step.json" ]]; then
    log "local diagnostic result: $RUN_DIR/local-step.json (status $classifier_status)"
    exit "$classifier_status"
  fi
  die "local diagnostic classifier did not write local-step.json; coordinator status $coordinator_status"
fi
if [[ "$STOP_SENT" -eq 1 && "$coordinator_status" -eq 130 ]]; then
  [[ -f "$REPORT_DIR/final.json" ]] || die 'coordinator stopped without final.json'
  log "bounded shakeout stopped cleanly; final report: $REPORT_DIR/final.json"
  exit 130
fi
if [[ "$coordinator_status" -ne 0 ]]; then
  log "coordinator exited with status $coordinator_status; logs preserved in $RUN_DIR"
  exit "$coordinator_status"
fi
[[ -f "$REPORT_DIR/final.json" ]] || die 'coordinator completed without final.json'
log "shakeout completed; final report: $REPORT_DIR/final.json"
