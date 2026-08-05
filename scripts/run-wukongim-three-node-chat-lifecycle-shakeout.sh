#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
RUN_DIR=""
BASE_PORT=15000
READY_TIMEOUT=120
STOP_AFTER=0
DRY_RUN=0
CLEANUP_TIMEOUT=15
GRACEFUL_STOP_TIMEOUT=120

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
# nodes, three authenticated lifecycle workers, three local filesystem metrics
# endpoints, and one coordinator. Every artifact stays below the fresh run dir.
#
# Options:
#   --run-dir DIR       Required fresh artifact, data, PID, log, and report root.
#   --base-port PORT    First port in a reserved 64-port range (default 15000).
#   --ready-timeout S   Readiness deadline in seconds (default 120).
#   --stop-after S      Request graceful coordinator stop after S seconds; 0 waits.
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

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-dir) [[ $# -ge 2 ]] || die '--run-dir requires a value'; RUN_DIR="$2"; shift 2 ;;
    --base-port) [[ $# -ge 2 ]] || die '--base-port requires a value'; BASE_PORT="$2"; shift 2 ;;
    --ready-timeout) [[ $# -ge 2 ]] || die '--ready-timeout requires a value'; READY_TIMEOUT="$2"; shift 2 ;;
    --stop-after) [[ $# -ge 2 ]] || die '--stop-after requires a value'; STOP_AFTER="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

require_uint '--base-port' "$BASE_PORT"
require_uint '--ready-timeout' "$READY_TIMEOUT"
require_uint '--stop-after' "$STOP_AFTER"
(( BASE_PORT >= 1024 && BASE_PORT <= 65472 )) || die '--base-port must reserve 64 ports within 1024..65535'
(( READY_TIMEOUT > 0 )) || die '--ready-timeout must be greater than zero'
validate_run_dir

WUKONGIM_BIN="$RUN_DIR/bin/wukongim"
WKBENCH_BIN="$RUN_DIR/bin/wkbench"
CONFIG_DIR="$RUN_DIR/config"
DATA_DIR="$RUN_DIR/data"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
WORKER_DIR="$RUN_DIR/workers"
REPORT_DIR="$RUN_DIR/report"
LIFECYCLE_CONFIG="$RUN_DIR/chat-lifecycle.yaml"
RUN_ID="local-chat-lifecycle-$(date -u +%Y%m%dT%H%M%SZ)-$$"

print_plan() {
  printf 'run_dir=%s\n' "$RUN_DIR"
  printf 'build_wukongim=GOWORK=off go build -o %s ./cmd/wukongim\n' "$WUKONGIM_BIN"
  printf 'build_wkbench=GOWORK=off go build -o %s ./cmd/wkbench\n' "$WKBENCH_BIN"
  printf 'logical_slot_groups=12\n'
  printf 'hash_slots=256\n'
  printf 'replicas=3/3\n'
  printf 'coordinator_config=%s\n' "$LIFECYCLE_CONFIG"
  printf 'report_dir=%s\n' "$REPORT_DIR"
  local node
  for node in 1 2 3; do
    printf 'service_%s=http://127.0.0.1:%s\n' "$node" "$(api_port "$node")"
    printf 'gateway_%s=127.0.0.1:%s\n' "$node" "$(gateway_port "$node")"
    printf 'worker_%s=http://127.0.0.1:%s\n' "$node" "$(worker_port "$node")"
    printf 'host_metrics_%s=http://127.0.0.1:%s\n' "$node" "$(host_metrics_port "$node")"
  done
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

command -v go >/dev/null 2>&1 || die 'go is required'
command -v curl >/dev/null 2>&1 || die 'curl is required'
[[ -n "${WK_BENCH_API_TOKEN:-}" ]] || die 'WK_BENCH_API_TOKEN is required'
[[ -n "${WK_BENCH_WORKER_TOKEN:-}" ]] || die 'WK_BENCH_WORKER_TOKEN is required'
if [[ -e "$RUN_DIR" ]] && [[ -n "$(find "$RUN_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  die "--run-dir must be absent or empty: $RUN_DIR"
fi

mkdir -p "$RUN_DIR/bin" "$CONFIG_DIR" "$DATA_DIR" "$LOG_DIR" "$PID_DIR" "$WORKER_DIR" "$REPORT_DIR"
for node in 1 2 3; do
  mkdir -p "$DATA_DIR/node$node" "$LOG_DIR/node$node" "$WORKER_DIR/node$node"
  cp "$ROOT_DIR/scripts/wukongim/wukongim-node$node.toml" "$CONFIG_DIR/node$node.toml"
done

log 'building service and benchmark binaries'
(cd "$ROOT_DIR" && GOWORK=off go build -o "$WUKONGIM_BIN" ./cmd/wukongim)
(cd "$ROOT_DIR" && GOWORK=off go build -o "$WKBENCH_BIN" ./cmd/wkbench)

sed \
  -e "s/local-chat-lifecycle-shakeout/$RUN_ID/g" \
  -e "s/15001/$(api_port 1)/g" -e "s/15002/$(api_port 2)/g" -e "s/15003/$(api_port 3)/g" \
  -e "s/15011/$(api_port 1)/g" -e "s/15012/$(api_port 2)/g" -e "s/15013/$(api_port 3)/g" \
  -e "s/15101/$(gateway_port 1)/g" -e "s/15102/$(gateway_port 2)/g" -e "s/15103/$(gateway_port 3)/g" \
  -e "s/19091/$(worker_port 1)/g" -e "s/19092/$(worker_port 2)/g" -e "s/19093/$(worker_port 3)/g" \
  -e "s/19101/$(host_metrics_port 1)/g" -e "s/19102/$(host_metrics_port 2)/g" -e "s/19103/$(host_metrics_port 3)/g" \
  "$ROOT_DIR/configs/wkbench/chat-lifecycle/local-shakeout.yaml" >"$LIFECYCLE_CONFIG"

record_pid() {
  local name="$1" pid="$2"
  NAMES+=("$name")
  PIDS+=("$pid")
  printf '%s\n' "$pid" >"$PID_DIR/$name.pid"
}

terminate_recorded() {
  local pid deadline alive
  if [[ "${#PIDS[@]}" -eq 0 ]]; then
    return
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
    WK_CLUSTER_MAX_CHANNELS=500 \
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

start_host_metrics() {
  local node="$1" pid
  "$WKBENCH_BIN" host-metrics --listen "127.0.0.1:$(host_metrics_port "$node")" \
    --path "$DATA_DIR/node$node" --mountpoint "/var/lib/wukongim-$node" --device "/dev/local-data-$node" \
    >"$LOG_DIR/host-metrics-$node.log" 2>&1 &
  pid=$!
  record_pid "host-metrics-$node" "$pid"
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
for node in 1 2 3; do
  wait_url "worker-$node" "http://127.0.0.1:$(worker_port "$node")/healthz" "$WK_BENCH_WORKER_TOKEN"
  wait_url "host-metrics-$node" "http://127.0.0.1:$(host_metrics_port "$node")/healthz"
done

log 'starting coordinator'
"$WKBENCH_BIN" soak chat-lifecycle --config "$LIFECYCLE_CONFIG" --output-dir "$REPORT_DIR" \
  >"$LOG_DIR/coordinator.log" 2>&1 &
COORDINATOR_PID=$!
record_pid coordinator "$COORDINATOR_PID"

started_at=$SECONDS
while kill -0 "$COORDINATOR_PID" 2>/dev/null; do
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
