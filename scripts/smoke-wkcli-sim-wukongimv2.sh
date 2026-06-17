#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${WK_WKCLI_SIM_SMOKE_OUT_DIR:-$ROOT_DIR/data/wkcli-sim-smoke}"
API_ADDR="${WK_WKCLI_SIM_SMOKE_API_ADDR:-http://127.0.0.1:15001}"
GATEWAY_ADDR="${WK_WKCLI_SIM_SMOKE_GATEWAY_ADDR:-127.0.0.1:15100}"
CLUSTER_ADDR="${WK_WKCLI_SIM_SMOKE_CLUSTER_ADDR:-127.0.0.1:17001}"
WS_ADDR="${WK_WKCLI_SIM_SMOKE_WS_ADDR:-127.0.0.1:15200}"
STATUS_LISTEN="${WK_WKCLI_SIM_SMOKE_STATUS_LISTEN:-127.0.0.1:19099}"
READY_TIMEOUT="${WK_WKCLI_SIM_SMOKE_READY_TIMEOUT:-60}"
POLL_INTERVAL="${WK_WKCLI_SIM_SMOKE_POLL_INTERVAL:-1}"

USERS="${WK_WKCLI_SIM_SMOKE_USERS:-10}"
GROUP_COUNT="${WK_WKCLI_SIM_SMOKE_GROUPS:-2}"
GROUP_MEMBERS="${WK_WKCLI_SIM_SMOKE_GROUP_MEMBERS:-5}"
RATE="${WK_WKCLI_SIM_SMOKE_RATE:-5/s}"
DURATION="${WK_WKCLI_SIM_SMOKE_DURATION:-5s}"
PAYLOAD_SIZE="${WK_WKCLI_SIM_SMOKE_PAYLOAD_SIZE:-64B}"
STATUS_INTERVAL="${WK_WKCLI_SIM_SMOKE_STATUS_INTERVAL:-1s}"

CLEAN=1
DRY_RUN=0
PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-wkcli-sim-wukongimv2.sh [options]

Starts a temporary cmd/wukongimv2 single-node cluster with the v2 bench API
enabled, runs a small real wkcli sim workload through WKProto, captures bench
snapshot evidence, then stops the node.

Options:
  --out-dir DIR             Evidence directory. Default: data/wkcli-sim-smoke.
  --api-addr URL            HTTP API base URL. Default: http://127.0.0.1:15001.
  --gateway-addr HOST:PORT  WKProto TCP gateway address. Default: 127.0.0.1:15100.
  --cluster-addr HOST:PORT  clusterv2 listen address. Default: 127.0.0.1:17001.
  --ws-addr HOST:PORT       Published websocket address. Default: 127.0.0.1:15200.
  --status-listen HOST:PORT Local wkcli sim status address. Default: 127.0.0.1:19099.
  --users N                 Simulated users. Default: 10.
  --groups N                Simulated group channels. Default: 2.
  --members N               Members per group. Default: 5.
  --rate RATE               Per-group send rate. Default: 5/s.
  --duration DURATION       Sim max runtime. Default: 5s.
  --payload-size SIZE       Sim payload size. Default: 64B.
  --ready-timeout SECS      Node ready wait timeout. Default: 60.
  --poll SECS               Ready polling interval. Default: 1.
  --keep-data               Do not remove the output directory before start.
  --dry-run                 Print resolved commands without starting anything.
  -h, --help                Show this help.
USAGE
}

log() {
  printf '[wkcli-sim-smoke] %s\n' "$*"
}

die() {
  printf '[wkcli-sim-smoke] ERROR: %s\n' "$*" >&2
  tail_evidence
  exit 1
}

require_positive_uint() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a positive integer: $value"
  (( value > 0 )) || die "$name must be a positive integer: $value"
}

node_data_dir() {
  printf '%s/node' "$OUT_DIR"
}

node_log() {
  printf '%s/node.log' "$OUT_DIR"
}

sim_output() {
  printf '%s/sim.jsonl' "$OUT_DIR"
}

snapshot_output() {
  printf '%s/bench-snapshot.json' "$OUT_DIR"
}

capabilities_output() {
  printf '%s/bench-capabilities.json' "$OUT_DIR"
}

capacity_output() {
  printf '%s/bench-capacity-target.json' "$OUT_DIR"
}

api_listen_addr() {
  local raw="$API_ADDR"
  raw="${raw#http://}"
  raw="${raw#https://}"
  raw="${raw%%/*}"
  printf '%s' "$raw"
}

print_plan() {
  local listen
  listen="$(api_listen_addr)"
  printf 'repo_root=%s\n' "$ROOT_DIR"
  printf 'out_dir=%s\n' "$OUT_DIR"
  printf 'api_addr=%s\n' "$API_ADDR"
  printf 'gateway_addr=%s\n' "$GATEWAY_ADDR"
  printf 'cluster_addr=%s\n' "$CLUSTER_ADDR"
  printf 'node_data_dir=%s\n' "$(node_data_dir)"
  printf 'node_log=%s\n' "$(node_log)"
  printf 'sim_output=%s\n' "$(sim_output)"
  printf 'snapshot_output=%s\n' "$(snapshot_output)"
  printf 'node_cmd=env WK_NODE_ID=1 WK_NODE_DATA_DIR=%s WK_CLUSTER_LISTEN_ADDR=%s WK_CLUSTER_NODES=[{\"id\":1,\"addr\":\"%s\"}] WK_API_LISTEN_ADDR=%s WK_BENCH_API_ENABLE=true WK_EXTERNAL_TCPADDR=%s go run ./cmd/wukongimv2\n' \
    "$(node_data_dir)" "$CLUSTER_ADDR" "$CLUSTER_ADDR" "$listen" "$GATEWAY_ADDR"
  printf 'sim_cmd=go run ./cmd/wkcli sim --server %s --users %s --groups %s --group-members %s --rate %s --max-runtime %s --payload-size %s --status-listen %s --status-interval %s --json\n' \
    "$API_ADDR" "$USERS" "$GROUP_COUNT" "$GROUP_MEMBERS" "$RATE" "$DURATION" "$PAYLOAD_SIZE" "$STATUS_LISTEN" "$STATUS_INTERVAL"
}

tail_evidence() {
  local path
  path="$(node_log)"
  if [[ -f "$path" ]]; then
    printf '\n--- node log: %s ---\n' "$path" >&2
    tail -n 80 "$path" >&2 || true
  fi
  path="$(sim_output)"
  if [[ -f "$path" ]]; then
    printf '\n--- sim output: %s ---\n' "$path" >&2
    tail -n 80 "$path" >&2 || true
  fi
}

stop_node() {
  if [[ -z "$PID" ]]; then
    return
  fi
  if kill -0 "$PID" 2>/dev/null; then
    log 'stopping node'
    kill "$PID" 2>/dev/null || true
  fi
  wait "$PID" 2>/dev/null || true
  PID=""
}

cleanup() {
  stop_node
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      [[ $# -ge 2 ]] || die '--out-dir requires a value'
      OUT_DIR="$2"
      shift 2
      ;;
    --api-addr)
      [[ $# -ge 2 ]] || die '--api-addr requires a value'
      API_ADDR="$2"
      shift 2
      ;;
    --gateway-addr)
      [[ $# -ge 2 ]] || die '--gateway-addr requires a value'
      GATEWAY_ADDR="$2"
      shift 2
      ;;
    --cluster-addr)
      [[ $# -ge 2 ]] || die '--cluster-addr requires a value'
      CLUSTER_ADDR="$2"
      shift 2
      ;;
    --ws-addr)
      [[ $# -ge 2 ]] || die '--ws-addr requires a value'
      WS_ADDR="$2"
      shift 2
      ;;
    --status-listen)
      [[ $# -ge 2 ]] || die '--status-listen requires a value'
      STATUS_LISTEN="$2"
      shift 2
      ;;
    --users)
      [[ $# -ge 2 ]] || die '--users requires a value'
      USERS="$2"
      shift 2
      ;;
    --groups)
      [[ $# -ge 2 ]] || die '--groups requires a value'
      GROUP_COUNT="$2"
      shift 2
      ;;
    --members)
      [[ $# -ge 2 ]] || die '--members requires a value'
      GROUP_MEMBERS="$2"
      shift 2
      ;;
    --rate)
      [[ $# -ge 2 ]] || die '--rate requires a value'
      RATE="$2"
      shift 2
      ;;
    --duration)
      [[ $# -ge 2 ]] || die '--duration requires a value'
      DURATION="$2"
      shift 2
      ;;
    --payload-size)
      [[ $# -ge 2 ]] || die '--payload-size requires a value'
      PAYLOAD_SIZE="$2"
      shift 2
      ;;
    --ready-timeout)
      [[ $# -ge 2 ]] || die '--ready-timeout requires a value'
      READY_TIMEOUT="$2"
      shift 2
      ;;
    --poll)
      [[ $# -ge 2 ]] || die '--poll requires a value'
      POLL_INTERVAL="$2"
      shift 2
      ;;
    --keep-data)
      CLEAN=0
      shift
      ;;
    --dry-run)
      DRY_RUN=1
      shift
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

require_positive_uint '--ready-timeout' "$READY_TIMEOUT"
require_positive_uint '--users' "$USERS"
require_positive_uint '--groups' "$GROUP_COUNT"
require_positive_uint '--members' "$GROUP_MEMBERS"

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

if [[ "$CLEAN" -eq 1 ]]; then
  rm -rf "$OUT_DIR"
fi
mkdir -p "$OUT_DIR"
: >"$(node_log)"
: >"$(sim_output)"

start_node() {
  local listen
  listen="$(api_listen_addr)"
  log "starting cmd/wukongimv2: api=${API_ADDR} gateway=${GATEWAY_ADDR} cluster=${CLUSTER_ADDR}"
  (
    cd "$ROOT_DIR"
    env \
      WK_NODE_ID=1 \
      WK_NODE_DATA_DIR="$(node_data_dir)" \
      WK_CLUSTER_LISTEN_ADDR="$CLUSTER_ADDR" \
      WK_CLUSTER_ID=wkcli-sim-smoke \
      WK_CLUSTER_NODES="[{\"id\":1,\"addr\":\"$CLUSTER_ADDR\"}]" \
      WK_CLUSTER_INITIAL_SLOT_COUNT=1 \
      WK_CLUSTER_HASH_SLOT_COUNT=16 \
      WK_CLUSTER_SLOT_REPLICA_N=1 \
      WK_CLUSTER_CHANNEL_REACTOR_COUNT=1 \
      WK_API_LISTEN_ADDR="$listen" \
      WK_BENCH_API_ENABLE=true \
      WK_BENCH_API_MAX_BATCH_SIZE=100 \
      WK_BENCH_API_MAX_PAYLOAD_BYTES=1048576 \
      WK_METRICS_ENABLE=false \
      WK_PROMETHEUS_ENABLE=false \
      WK_TOP_API_ENABLE=false \
      WK_DIAGNOSTICS_ENABLE=false \
      WK_LOG_LEVEL=warn \
      WK_LOG_DIR="$OUT_DIR/logs" \
      WK_LOG_CONSOLE=true \
      WK_LOG_FORMAT=console \
      WK_EXTERNAL_TCPADDR="$GATEWAY_ADDR" \
      WK_EXTERNAL_WSADDR="ws://$WS_ADDR" \
      WK_GATEWAY_LISTENERS="[{\"name\":\"tcp-wkproto\",\"network\":\"tcp\",\"address\":\"$GATEWAY_ADDR\",\"transport\":\"gnet\",\"protocol\":\"wkproto\"}]" \
      WK_GATEWAY_GNET_MULTICORE=false \
      WK_GATEWAY_GNET_NUM_EVENT_LOOP=1 \
      WK_GATEWAY_SEND_TIMEOUT=5s \
      WK_PRESENCE_ACTIVATION_TIMEOUT=3s \
      WK_PRESENCE_TOUCH_FLUSH_INTERVAL=500ms \
      WK_PRESENCE_TOUCH_BATCH_SIZE=64 \
      WK_PRESENCE_ROUTE_TTL=30s \
      WK_DELIVERY_ENABLE=true \
      go run ./cmd/wukongimv2
  ) >"$(node_log)" 2>&1 &
  PID="$!"
  log "node pid=${PID} log=$(node_log)"
}

check_node() {
  if [[ -n "$PID" ]] && ! kill -0 "$PID" 2>/dev/null; then
    local status=0
    wait "$PID" 2>/dev/null || status=$?
    PID=""
    die "node exited before smoke completed with status ${status}"
  fi
}

wait_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_node
    if curl -fsS --max-time 2 "$API_ADDR/readyz" >/dev/null 2>&1; then
      log "node ready: $API_ADDR/readyz"
      return
    fi
    sleep "$POLL_INTERVAL"
  done
  die "node did not become ready within ${READY_TIMEOUT}s"
}

capture_target_evidence() {
  curl -fsS "$API_ADDR/bench/v1/capabilities" >"$(capabilities_output)"
  curl -fsS "$API_ADDR/bench/v1/capacity-target" >"$(capacity_output)"
  grep -q '"channels_batch":true' "$(capabilities_output)" || die 'bench capabilities missing channels_batch=true'
  grep -q '"channel_subscribers_batch":true' "$(capabilities_output)" || die 'bench capabilities missing channel_subscribers_batch=true'
  grep -q '"group"' "$(capabilities_output)" || die 'bench capabilities missing group channel type'
  grep -q "\"tcp_addr\":\"$GATEWAY_ADDR\"" "$(capacity_output)" || die 'capacity target did not publish expected gateway tcp address'
}

run_sim() {
  log "running wkcli sim: users=${USERS} groups=${GROUP_COUNT} members=${GROUP_MEMBERS} rate=${RATE} duration=${DURATION}"
  (
    cd "$ROOT_DIR"
    go run ./cmd/wkcli sim \
      --server "$API_ADDR" \
      --users "$USERS" \
      --groups "$GROUP_COUNT" \
      --group-members "$GROUP_MEMBERS" \
      --rate "$RATE" \
      --payload-size "$PAYLOAD_SIZE" \
      --max-runtime "$DURATION" \
      --status-listen "$STATUS_LISTEN" \
      --status-interval "$STATUS_INTERVAL" \
      --json
  ) | tee "$(sim_output)"
}

json_int() {
  local key="$1"
  sed -n "s/.*\"${key}\":\\([0-9][0-9]*\\).*/\\1/p"
}

json_string() {
  local key="$1"
  sed -n "s/.*\"${key}\":\"\\([^\"]*\\)\".*/\\1/p"
}

verify_sim_output() {
  local final state sent errors
  final="$(grep '"state"' "$(sim_output)" | tail -n 1 || true)"
  [[ -n "$final" ]] || die 'wkcli sim produced no status snapshots'
  state="$(printf '%s\n' "$final" | json_string state)"
  sent="$(printf '%s\n' "$final" | json_int messages_sent)"
  errors="$(printf '%s\n' "$final" | json_int send_errors)"
  [[ "$state" == "stopped" ]] || die "final sim state is ${state:-<empty>}, want stopped"
  [[ -n "$sent" && "$sent" =~ ^[0-9]+$ && "$sent" -gt 0 ]] || die "messages_sent=${sent:-<empty>}, want >0"
  [[ "${errors:-}" == "0" ]] || die "send_errors=${errors:-<empty>}, want 0"
  log "wkcli sim passed: messages_sent=${sent} send_errors=${errors}"
}

capture_snapshot() {
  curl -fsS "$API_ADDR/bench/v1/snapshot" >"$(snapshot_output)"
  grep -q '"accepted_channels"' "$(snapshot_output)" || die 'bench snapshot missing accepted_channels'
  log "bench snapshot: $(snapshot_output)"
}

start_node
wait_ready
capture_target_evidence
run_sim
verify_sim_output
capture_snapshot
log "smoke passed; evidence_dir=$OUT_DIR"
