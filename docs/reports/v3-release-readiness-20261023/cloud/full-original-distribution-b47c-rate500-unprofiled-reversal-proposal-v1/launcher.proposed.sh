#!/bin/bash
set -euo pipefail
umask 077
ROOT_DIR=/Users/tt/Desktop/work/go/WuKongIM-v2/WuKongIM/.worktrees/v3-rc-exact-source-b47c-queue-snapshot
RUN_DIR='/tmp/wukongim-v3-readiness-20261023/cloud/full-original-distribution-b47c-rate500-unprofiled-reversal-v1'
BASE_PORT=25000
READY_TIMEOUT=60
CLEANUP_TIMEOUT=15
RUNTIME_DEFAULTS=1
PIDS=()
NAMES=()
WAIT_CHILD_STATUS=0
WUKONGIM_BIN=/tmp/wukongim-v3-readiness-20261023/cloud/exact-source-build-b47c-native-v1/darwin-arm64/wukongim
WK_CLI_BIN=/tmp/wukongim-v3-readiness-20261023/cloud/exact-source-build-b47c-native-v1/darwin-arm64/wkcli
CONFIG_DIR="$RUN_DIR/config"
DATA_DIR="$RUN_DIR/data"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
WORKER_DIR="$RUN_DIR/workers"
REPORT_DIR="$RUN_DIR/report"
EVIDENCE_DIR="$RUN_DIR/evidence"
LIFECYCLE_CONFIG="$RUN_DIR/original-distribution-local.yaml"

log() { printf '[chat-lifecycle-shakeout] %s\n' "$*"; }

die() { printf '[chat-lifecycle-shakeout] ERROR: %s\n' "$*" >&2; exit 1; }

port() { printf '%d' "$((BASE_PORT + $1))"; }

api_port() { port "$1"; }

cluster_port() { port "$((10 + $1))"; }

gateway_port() { port "$((20 + $1))"; }

ws_port() { port "$((30 + $1))"; }

manager_port() { port "$((40 + $1))"; }

worker_port() { port "$((50 + $1))"; }

host_metrics_port() { port "$((60 + $1))"; }

load_host_metrics_port() { port 60; }

record_pid() {
  local name="$1" pid="$2"
  NAMES+=("$name")
  PIDS+=("$pid")
  printf '%s\n' "$pid" >"$PID_DIR/$name.pid"
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

stop_recorded_processes() {
  local pid deadline alive
  if [[ "${#PIDS[@]}" -eq 0 ]]; then
    return 0
  fi
  for pid in "${PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
    if kill -0 "$pid" 2>/dev/null; then
      kill -TERM "$pid" 2>/dev/null || true
    fi
  done
  deadline=$((SECONDS + CLEANUP_TIMEOUT))
  while (( SECONDS < deadline )); do
    alive=0
    for pid in "${PIDS[@]}"; do
      [[ -n "$pid" ]] || continue
      kill -0 "$pid" 2>/dev/null && alive=1
    done
    [[ "$alive" -eq 0 ]] && break
    sleep 1
  done
  for pid in "${PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
    if kill -0 "$pid" 2>/dev/null; then
      kill -KILL "$pid" 2>/dev/null || true
    fi
    wait_child_uninterrupted "$pid"
  done
}

terminate_recorded() {
  local original_status=$?
  stop_recorded_processes
  return "$original_status"
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

start_service() {
  local node="$1" gateway_listeners pid
  # Bash 3.2 treats an empty-array expansion as unbound under set -u. Keep the
  # common node identity in the array so product-default mode remains portable.
  local -a service_environment=("WK_NODE_ID=$node" "GOMAXPROCS=2")
  gateway_listeners="[{\"name\":\"tcp-wkproto\",\"network\":\"tcp\",\"address\":\"127.0.0.1:$(gateway_port "$node")\",\"transport\":\"gnet\",\"protocol\":\"wkproto\"},{\"name\":\"ws-gateway\",\"network\":\"websocket\",\"address\":\"127.0.0.1:$(ws_port "$node")\",\"transport\":\"gnet\",\"protocol\":\"wsmux\"}]"
  if (( RUNTIME_DEFAULTS == 0 )); then
    service_environment+=(
      "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500"
      "WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=1000"
    )
  fi
  env -u WK_BENCH_WORKER_TOKEN -u WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE \
    "${service_environment[@]}" \
    WK_NODE_DATA_DIR="$DATA_DIR/node$node" \
    WK_CLUSTER_LISTEN_ADDR="127.0.0.1:$(cluster_port "$node")" \
    WK_CLUSTER_NODES="$CLUSTER_NODES" \
    WK_CLUSTER_INITIAL_SLOT_COUNT=12 \
    WK_CLUSTER_HASH_SLOT_COUNT=256 \
    WK_CLUSTER_SLOT_REPLICA_N=3 \
    WK_CLUSTER_CHANNEL_REPLICA_N=3 \
    WK_CLUSTER_MAX_CHANNELS=50000 \
    WK_GATEWAY_DEFAULT_SESSION_ASYNC_SEND_BATCH_MAX_RECORDS=1 \
    WK_CLUSTER_COMMIT_COORDINATOR_FLUSH_WINDOW=500us \
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
  "$WK_CLI_BIN" bench worker --mode chat-lifecycle --listen "127.0.0.1:$(worker_port "$node")" \
    --work-dir "$WORKER_DIR/node$node" >"$LOG_DIR/worker-$node.log" 2>&1 &
  pid=$!
  record_pid "worker-$node" "$pid"
}

start_host_metrics() {
  local node="$1" pid
  local -a physical_io_args=(--physical-io=true)
  if [[ "$node" -ne 1 ]]; then
    physical_io_args=(--physical-io=false)
  fi
  write_process_metrics_for_host "$node" "$EVIDENCE_DIR/processes-node-$node.prom" || die "node $node process metrics initialization failed"
  "$WK_CLI_BIN" bench host-metrics --listen "127.0.0.1:$(host_metrics_port "$node")" \
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
  "$WK_CLI_BIN" bench host-metrics --listen "127.0.0.1:$(load_host_metrics_port)" \
    --path "$DATA_DIR/load" --mountpoint "/var/lib/wukongim-load" --device "/dev/local-load-data" \
    --watch-path "$REPORT_DIR" --process-metrics-path "$EVIDENCE_DIR/processes-load.prom" \
    --physical-io=false \
    >"$LOG_DIR/host-metrics-load.log" 2>&1 &
  pid=$!
  record_pid host-metrics-load "$pid"
  write_process_metrics_for_host load "$EVIDENCE_DIR/processes-load.prom" || die 'load process metrics refresh failed'
}

wait_url() {
  local name="$1" url="$2" token="${3:-}" required_successes="${4:-1}" deadline pid ready consecutive=0
  [[ "$required_successes" =~ ^[0-9]+$ ]] && (( required_successes > 0 )) || die 'readiness success count must be greater than zero'
  pid="$(<"$PID_DIR/$name.pid")"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      tail -n 80 "$LOG_DIR/$name.log" >&2 || true
      die "$name exited before readiness"
    fi
    ready=0
    if [[ -n "$token" ]]; then
      curl -fsS --max-time 2 -H "Authorization: Bearer $token" "$url" >/dev/null 2>&1 && ready=1
    elif curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      ready=1
    fi
    if (( ready == 1 )); then
      consecutive=$((consecutive + 1))
      if (( consecutive >= required_successes )); then
        log "$name ready: $url"
        return
      fi
    else
      consecutive=0
    fi
    sleep 1
  done
  die "$name readiness timed out: $url"
}

strip_promoted_runtime_overrides() {
  local config_path="$1" temporary
  temporary="$config_path.runtime-defaults.next.$$"
  awk '
    /^[[:space:]]*(channel_store_append_workers|channel_store_apply_workers|channel_rpc_workers|channel_rpc_batch_max_items|gnet_multicore|gnet_num_event_loop|runtime_async_send_workers|runtime_async_send_queue_capacity|recipient_worker_concurrency)[[:space:]]*=/ { next }
    { print }
  ' "$config_path" >"$temporary"
  mv "$temporary" "$config_path"
}

trap terminate_recorded EXIT
mkdir -p "$CONFIG_DIR" "$DATA_DIR/load" "$LOG_DIR" "$PID_DIR" "$WORKER_DIR" "$REPORT_DIR" "$EVIDENCE_DIR"
printf '%s\n' "$$" >"$PID_DIR/launcher.pid"
python3 "$RUN_DIR/watchdog.py" "$$" "$LOCAL_REPRO_DEADLINE_MONO" "$RUN_DIR/evidence/watchdog.json" &
WATCHDOG_PID=$!
record_pid watchdog "$WATCHDOG_PID"
CLUSTER_NODES="[{\"id\":1,\"addr\":\"127.0.0.1:$(cluster_port 1)\"},{\"id\":2,\"addr\":\"127.0.0.1:$(cluster_port 2)\"},{\"id\":3,\"addr\":\"127.0.0.1:$(cluster_port 3)\"}]"
for node in 1 2 3; do
  mkdir -p "$DATA_DIR/node$node" "$LOG_DIR/node$node" "$WORKER_DIR/node$node"
  cp "$ROOT_DIR/scripts/wukongim/wukongim-node$node.toml" "$CONFIG_DIR/node$node.toml"
  strip_promoted_runtime_overrides "$CONFIG_DIR/node$node.toml"
done
for node in 1 2 3; do start_service "$node"; done
for node in 1 2 3; do wait_url "service-$node" "http://127.0.0.1:$(api_port "$node")/readyz" "" 3; done
python3 "$RUN_DIR/verify-slots.py" "$RUN_DIR" "$BASE_PORT"
for node in 1 2 3; do start_worker "$node"; start_host_metrics "$node"; done
start_load_host_metrics
for node in 1 2 3; do
  wait_url "worker-$node" "http://127.0.0.1:$(worker_port "$node")/healthz" "$WK_BENCH_WORKER_TOKEN"
  wait_url "host-metrics-$node" "http://127.0.0.1:$(host_metrics_port "$node")/healthz"
done
wait_url host-metrics-load "http://127.0.0.1:$(load_host_metrics_port)/healthz"
"$WK_CLI_BIN" bench soak chat-lifecycle --config "$LIFECYCLE_CONFIG" --output-dir "$REPORT_DIR" >"$LOG_DIR/coordinator.log" 2>&1 &
COORDINATOR_PID=$!
record_pid coordinator "$COORDINATOR_PID"

start_process_metrics_collector
wait_child_uninterrupted "$COORDINATOR_PID"
COORDINATOR_EXIT_STATUS="$WAIT_CHILD_STATUS"
printf '%s\n' "$COORDINATOR_EXIT_STATUS" >"$EVIDENCE_DIR/coordinator-exit-status"
WAIT_CHILD_STATUS="$COORDINATOR_EXIT_STATUS"
if [[ "$WAIT_CHILD_STATUS" -eq 7 ]]; then
  diagnosis_hold_end=$((SECONDS + 90))
  while [[ ! -f "$EVIDENCE_DIR/diagnosis-hold-release" ]] && (( SECONDS < diagnosis_hold_end )); do
    sleep 0.5
  done
fi
kill -USR1 "$WATCHDOG_PID" 2>/dev/null || true
exit "$WAIT_CHILD_STATUS"
