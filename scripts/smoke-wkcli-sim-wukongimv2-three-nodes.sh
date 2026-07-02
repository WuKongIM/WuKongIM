#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${WK_WKCLI_SIM_THREE_SMOKE_OUT_DIR:-$ROOT_DIR/data/wkcli-sim-three-node-smoke}"
START_SCRIPT="${WK_WKCLI_SIM_THREE_SMOKE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongimv2-three-nodes.sh}"
API_ADDRS="${WK_WKCLI_SIM_THREE_SMOKE_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_WKCLI_SIM_THREE_SMOKE_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
STATUS_LISTEN="${WK_WKCLI_SIM_THREE_SMOKE_STATUS_LISTEN:-127.0.0.1:19109}"
READY_TIMEOUT="${WK_WKCLI_SIM_THREE_SMOKE_READY_TIMEOUT:-90}"
POLL_INTERVAL="${WK_WKCLI_SIM_THREE_SMOKE_POLL_INTERVAL:-1}"
DEBUG_API_ENABLE="${WK_DEBUG_API_ENABLE:-true}"
MAX_FLUSH_ERROR_SELECTED_ROWS="${WK_WKCLI_SIM_THREE_SMOKE_MAX_FLUSH_ERROR_SELECTED_ROWS:-0}"
MAX_HANDOFF_ERROR_TOTAL="${WK_WKCLI_SIM_THREE_SMOKE_MAX_HANDOFF_ERROR_TOTAL:-0}"
MAX_HANDOFF_TIMEOUT_TOTAL="${WK_WKCLI_SIM_THREE_SMOKE_MAX_HANDOFF_TIMEOUT_TOTAL:-0}"
MAX_GOROUTINES="${WK_WKCLI_SIM_THREE_SMOKE_MAX_GOROUTINES:-2000}"
MAX_HEAP_ALLOC_BYTES="${WK_WKCLI_SIM_THREE_SMOKE_MAX_HEAP_ALLOC_BYTES:-4294967296}"
AUTO_JOIN_NODE="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE:-false}"
AUTO_JOIN_AFTER="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_AFTER:-2}"
AUTO_JOIN_NODE_ID="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE_ID:-4}"
AUTO_JOIN_API_ADDR="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_API_ADDR:-http://127.0.0.1:5014}"
AUTO_JOIN_GATEWAY_ADDR="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_GATEWAY_ADDR:-127.0.0.1:5114}"
AUTO_JOIN_CLUSTER_ADDR="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_CLUSTER_ADDR:-127.0.0.1:7014}"
AUTO_JOIN_SEEDS="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_SEEDS:-127.0.0.1:7011,127.0.0.1:7012,127.0.0.1:7013}"
AUTO_JOIN_TOKEN="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_TOKEN:-change-me}"
AUTO_JOIN_CLUSTER_ID="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_CLUSTER_ID:-wukongimv2-dev-three}"
AUTO_JOIN_CONFIG_PATH="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_CONFIG:-$OUT_DIR/wukongimv2-node4.conf}"
AUTO_JOIN_DATA_DIR="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_DATA_DIR:-$OUT_DIR/node4-data}"
AUTO_JOIN_CONFIG_PATH_SET=0
AUTO_JOIN_DATA_DIR_SET=0
[[ -n "${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_CONFIG-}" ]] && AUTO_JOIN_CONFIG_PATH_SET=1
[[ -n "${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_DATA_DIR-}" ]] && AUTO_JOIN_DATA_DIR_SET=1
AUTO_PROMOTE_CONTROLLER_VOTER="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_CONTROLLER_VOTER:-false}"
AUTO_PROMOTE_MANAGER_API="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_MANAGER_API:-http://127.0.0.1:5311}"
AUTO_PROMOTE_MANAGER_AUTH="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_MANAGER_AUTH:-true}"
AUTO_PROMOTE_MANAGER_USERNAME="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_MANAGER_USERNAME:-admin}"
AUTO_PROMOTE_MANAGER_PASSWORD="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_MANAGER_PASSWORD:-a1234567}"

USERS="${WK_WKCLI_SIM_THREE_SMOKE_USERS:-30}"
GROUP_COUNT="${WK_WKCLI_SIM_THREE_SMOKE_GROUPS:-6}"
GROUP_MEMBERS="${WK_WKCLI_SIM_THREE_SMOKE_GROUP_MEMBERS:-10}"
RATE="${WK_WKCLI_SIM_THREE_SMOKE_RATE:-10/s}"
DURATION="${WK_WKCLI_SIM_THREE_SMOKE_DURATION:-10s}"
PAYLOAD_SIZE="${WK_WKCLI_SIM_THREE_SMOKE_PAYLOAD_SIZE:-128B}"
STATUS_INTERVAL="${WK_WKCLI_SIM_THREE_SMOKE_STATUS_INTERVAL:-1s}"

START_CLUSTER=1
CLEAN_CLUSTER=1
BUILD_CLUSTER=1
DRY_RUN=0
CLUSTER_PID=""
AUTO_JOIN_PID=""
AUTO_JOIN_TIMER_PID=""
AUTO_JOIN_STARTED=0
API_VALUES=()
GATEWAY_VALUES=()
AUTO_JOIN_SEED_VALUES=()

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-wkcli-sim-wukongimv2-three-nodes.sh [options]

Starts a local cmd/wukongimv2 three-node cluster, runs a small real wkcli sim
workload through all three WKProto gateways, captures v2 bench API evidence
from every node, then stops the cluster.

The started cluster enables WK_DEBUG_API_ENABLE by default so pprof/debug
evidence is available during failures. Set WK_DEBUG_API_ENABLE=false to opt out.

Options:
  --out-dir DIR             Evidence directory. Default: data/wkcli-sim-three-node-smoke.
  --start-script PATH       Three-node cluster startup script.
  --no-start                Use an already-running three-node cluster.
  --no-clean                Keep existing node data when starting the cluster.
  --no-build                Reuse the evidence-dir cluster binary when starting.
  --api LIST                Comma-separated HTTP API base URLs.
  --gateway LIST            Comma-separated WKProto gateway addresses.
  --status-listen HOST:PORT Local wkcli sim status address. Default: 127.0.0.1:19109.
  --users N                 Simulated users. Default: 30.
  --groups N                Simulated group channels. Default: 6.
  --members N               Members per group. Default: 10.
  --rate RATE               Per-group send rate. Default: 10/s.
  --duration DURATION       Sim max runtime. Default: 10s.
  --payload-size SIZE       Sim payload size. Default: 128B.
  --ready-timeout SECS      Cluster ready wait timeout. Default: 90.
  --poll SECS               Ready polling interval. Default: 1.
  --auto-join-node          Start one seed-join data node while wkcli sim is sending.
  --auto-join-after SECS    Seconds after the send phase starts before starting the new node. Default: 2.
  --auto-join-node-id N     Joining node ID. Default: 4.
  --auto-join-api URL       Joining node HTTP API base URL. Default: http://127.0.0.1:5014.
  --auto-join-gateway ADDR  Joining node WKProto gateway address. Default: 127.0.0.1:5114.
  --auto-join-cluster ADDR  Joining node cluster RPC address. Default: 127.0.0.1:7014.
  --auto-join-seeds LIST    Comma-separated seed cluster RPC addresses. Default: node1-node3 local RPC addresses.
  --auto-join-token TOKEN   Join token accepted by the seed nodes. Default: change-me.
  --auto-join-cluster-id ID Cluster ID expected by the joining node. Default: wukongimv2-dev-three.
  --auto-join-config PATH   Generated joining node config path. Default: OUT_DIR/wukongimv2-node4.conf.
  --auto-join-data-dir DIR  Joining node data directory. Default: OUT_DIR/node4-data.
  --auto-promote-controller-voter
                           After the auto-join node is ready, activate it and promote it to a Controller voter while sim is running.
  --auto-promote-manager-api URL
                           Manager API base URL used for activation and promotion. Default: http://127.0.0.1:5311.
  --no-auto-promote-manager-auth
                           Skip manager login and Authorization header for clusters with manager auth disabled.
  --auto-promote-manager-user USER
                           Manager username used when auth is enabled. Default: admin.
  --auto-promote-manager-password PASSWORD
                           Manager password used when auth is enabled. Default: a1234567.
  --dry-run                 Print resolved commands without starting anything.
  -h, --help                Show this help.
USAGE
}

log() {
  printf '[wkcli-sim-three-smoke] %s\n' "$*"
}

die() {
  printf '[wkcli-sim-three-smoke] ERROR: %s\n' "$*" >&2
  tail_evidence
  exit 1
}

require_uint() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer: $value"
}

require_positive_uint() {
  local name="$1"
  local value="$2"
  require_uint "$name" "$value"
  (( value > 0 )) || die "$name must be greater than zero: $value"
}

require_bool() {
  local name="$1"
  local value="$2"
  case "$value" in
    true|false) ;;
    *) die "$name must be true or false: $value" ;;
  esac
}

require_nonempty() {
  local name="$1"
  local value="$2"
  [[ -n "$value" ]] || die "$name must not be empty"
}

split_csv() {
  local raw="$1"
  local var_name="$2"
  local item
  local values=()
  eval "$var_name=()"
  IFS=',' read -ra values <<<"$raw"
  for item in "${values[@]}"; do
    item="${item//[[:space:]]/}"
    [[ -n "$item" ]] || die "comma-separated list contains an empty item: $raw"
    eval "$var_name+=(\"\$item\")"
  done
}

cluster_bin() {
  printf '%s/wukongimv2' "$OUT_DIR"
}

cluster_log() {
  printf '%s/cluster.log' "$OUT_DIR"
}

node_log_dir() {
  printf '%s/node-logs' "$OUT_DIR"
}

sim_output() {
  printf '%s/sim.jsonl' "$OUT_DIR"
}

capabilities_dir() {
  printf '%s/bench-capabilities' "$OUT_DIR"
}

capacity_dir() {
  printf '%s/bench-capacity-targets' "$OUT_DIR"
}

snapshot_dir() {
  printf '%s/bench-snapshots' "$OUT_DIR"
}

metrics_dir() {
  printf '%s/metrics' "$OUT_DIR"
}

summary_output() {
  printf '%s/summary.md' "$OUT_DIR"
}

node_file() {
  local dir="$1"
  local index="$2"
  printf '%s/node%s.json' "$dir" "$index"
}

auto_join_log() {
  printf '%s/node%s.log' "$(node_log_dir)" "$AUTO_JOIN_NODE_ID"
}

auto_join_pid_file() {
  printf '%s/auto-join-node.pid' "$OUT_DIR"
}

auto_join_ready_file() {
  printf '%s/auto-join-node.ready' "$OUT_DIR"
}

auto_promote_ready_file() {
  printf '%s/controller-voter-promotion-node%s.ready' "$OUT_DIR" "$AUTO_JOIN_NODE_ID"
}

auto_promote_login_response_file() {
  printf '%s/manager-login.json' "$OUT_DIR"
}

auto_promote_token_file() {
  printf '%s/manager-token' "$OUT_DIR"
}

auto_promote_activate_response_file() {
  printf '%s/node%s-activate.json' "$OUT_DIR" "$AUTO_JOIN_NODE_ID"
}

auto_promote_nodes_response_file() {
  printf '%s/nodes-after-node%s-activate.json' "$OUT_DIR" "$AUTO_JOIN_NODE_ID"
}

auto_promote_response_file() {
  printf '%s/controller-voter-promotion-node%s.json' "$OUT_DIR" "$AUTO_JOIN_NODE_ID"
}

auto_promote_controller_raft_status_file() {
  printf '%s/controller-raft-node%s.json' "$OUT_DIR" "$AUTO_JOIN_NODE_ID"
}

sim_done_file() {
  printf '%s/sim.done' "$OUT_DIR"
}

prepare_auto_join_state() {
  if [[ "$AUTO_JOIN_NODE" != "true" ]]; then
    return
  fi
  rm -f "$(auto_join_pid_file)" "$(auto_join_ready_file)" "$(auto_promote_ready_file)" "$(sim_done_file)"
  rm -f "$(auto_promote_login_response_file)" "$(auto_promote_token_file)"
  rm -f "$(auto_promote_activate_response_file)" "$(auto_promote_nodes_response_file)" "$(auto_promote_response_file)" "$(auto_promote_controller_raft_status_file)"
  if [[ "$CLEAN_CLUSTER" -eq 0 ]]; then
    return
  fi
  case "$AUTO_JOIN_DATA_DIR" in
    ""|"/"|".") die "refusing to clean unsafe auto-join data dir: ${AUTO_JOIN_DATA_DIR:-<empty>}" ;;
  esac
  rm -rf "$AUTO_JOIN_DATA_DIR"
  rm -f "$AUTO_JOIN_CONFIG_PATH" "$(auto_join_log)"
}

auto_join_api_listen_addr() {
  local raw="$AUTO_JOIN_API_ADDR"
  raw="${raw#http://}"
  raw="${raw#https://}"
  raw="${raw%%/*}"
  printf '%s' "$raw"
}

json_string_list() {
  local item
  local sep=""
  printf '['
  for item in "$@"; do
    case "$item" in
      *\"*|*\\*) die "JSON list item must not contain quote or backslash: $item" ;;
    esac
    printf '%s"%s"' "$sep" "$item"
    sep=","
  done
  printf ']'
}

print_start_cmd() {
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    printf 'start_cmd=<disabled>\n'
    return
  fi
  printf 'start_cmd=env WK_DEBUG_API_ENABLE=%s %s' "$DEBUG_API_ENABLE" "$START_SCRIPT"
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    printf ' --clean'
  fi
  printf ' --ready-timeout %s --bin %s --log-dir %s' "$READY_TIMEOUT" "$(cluster_bin)" "$(node_log_dir)"
  if [[ "$BUILD_CLUSTER" -eq 0 ]]; then
    printf ' --no-build'
  fi
  printf '\n'
}

print_sim_cmd() {
  local api
  local gateway
  printf 'sim_cmd=go run ./cmd/wkcli sim'
  for api in "${API_VALUES[@]}"; do
    printf ' --server %s' "$api"
  done
  for gateway in "${GATEWAY_VALUES[@]}"; do
    printf ' --gateway %s' "$gateway"
  done
  printf ' --users %s --groups %s --group-members %s --rate %s --max-runtime %s --payload-size %s --status-listen %s --status-interval %s --json\n' \
    "$USERS" "$GROUP_COUNT" "$GROUP_MEMBERS" "$RATE" "$DURATION" "$PAYLOAD_SIZE" "$STATUS_LISTEN" "$STATUS_INTERVAL"
}

print_auto_join_cmd() {
  if [[ "$AUTO_JOIN_NODE" != "true" ]]; then
    printf 'auto_join_cmd=<disabled>\n'
    return
  fi
  printf 'auto_join_cmd=env WK_DEBUG_API_ENABLE=%s %s -config %s\n' "$DEBUG_API_ENABLE" "$(cluster_bin)" "$AUTO_JOIN_CONFIG_PATH"
}

print_auto_promote_cmd() {
  if [[ "$AUTO_PROMOTE_CONTROLLER_VOTER" != "true" ]]; then
    printf 'auto_promote_login_cmd=<disabled>\n'
    printf 'auto_promote_activate_cmd=<disabled>\n'
    printf 'auto_promote_cmd=<disabled>\n'
    return
  fi
  if [[ "$AUTO_PROMOTE_MANAGER_AUTH" == "true" ]]; then
    printf 'auto_promote_login_cmd=curl -fsS -H Content-Type: application/json -d {"username":"%s","password":"%s"} %s/manager/login\n' \
      "$AUTO_PROMOTE_MANAGER_USERNAME" "$AUTO_PROMOTE_MANAGER_PASSWORD" "$AUTO_PROMOTE_MANAGER_API"
    printf 'auto_promote_activate_cmd=curl -fsS -H Authorization: Bearer <token> -X POST %s/manager/nodes/%s/activate\n' "$AUTO_PROMOTE_MANAGER_API" "$AUTO_JOIN_NODE_ID"
    printf 'auto_promote_cmd=curl -fsS -H Authorization: Bearer <token> -X POST %s/manager/nodes/%s/controller-voter/promote\n' "$AUTO_PROMOTE_MANAGER_API" "$AUTO_JOIN_NODE_ID"
    return
  fi
  printf 'auto_promote_login_cmd=<disabled>\n'
  printf 'auto_promote_activate_cmd=curl -fsS -X POST %s/manager/nodes/%s/activate\n' "$AUTO_PROMOTE_MANAGER_API" "$AUTO_JOIN_NODE_ID"
  printf 'auto_promote_cmd=curl -fsS -X POST %s/manager/nodes/%s/controller-voter/promote\n' "$AUTO_PROMOTE_MANAGER_API" "$AUTO_JOIN_NODE_ID"
}

print_plan() {
  printf 'repo_root=%s\n' "$ROOT_DIR"
  printf 'out_dir=%s\n' "$OUT_DIR"
  printf 'start_script=%s\n' "$START_SCRIPT"
  printf 'api_addrs=%s\n' "$(IFS=','; printf '%s' "${API_VALUES[*]}")"
  printf 'gateway_addrs=%s\n' "$(IFS=','; printf '%s' "${GATEWAY_VALUES[*]}")"
  printf 'cluster_log=%s\n' "$(cluster_log)"
  printf 'node_log_dir=%s\n' "$(node_log_dir)"
  printf 'sim_output=%s\n' "$(sim_output)"
  printf 'snapshot_output_dir=%s\n' "$(snapshot_dir)"
  printf 'metrics_output_dir=%s\n' "$(metrics_dir)"
  printf 'max_flush_error_selected_rows=%s\n' "$MAX_FLUSH_ERROR_SELECTED_ROWS"
  printf 'max_handoff_error_total=%s\n' "$MAX_HANDOFF_ERROR_TOTAL"
  printf 'max_handoff_timeout_total=%s\n' "$MAX_HANDOFF_TIMEOUT_TOTAL"
  printf 'max_goroutines=%s\n' "$MAX_GOROUTINES"
  printf 'max_heap_alloc_bytes=%s\n' "$MAX_HEAP_ALLOC_BYTES"
  printf 'auto_join_node=%s\n' "$AUTO_JOIN_NODE"
  printf 'auto_join_after_secs=%s\n' "$AUTO_JOIN_AFTER"
  printf 'auto_join_node_id=%s\n' "$AUTO_JOIN_NODE_ID"
  printf 'auto_join_api=%s\n' "$AUTO_JOIN_API_ADDR"
  printf 'auto_join_gateway=%s\n' "$AUTO_JOIN_GATEWAY_ADDR"
  printf 'auto_join_cluster=%s\n' "$AUTO_JOIN_CLUSTER_ADDR"
  printf 'auto_join_seeds=%s\n' "$(IFS=','; printf '%s' "${AUTO_JOIN_SEED_VALUES[*]}")"
  printf 'auto_join_config=%s\n' "$AUTO_JOIN_CONFIG_PATH"
  printf 'auto_join_data_dir=%s\n' "$AUTO_JOIN_DATA_DIR"
  printf 'auto_join_log=%s\n' "$(auto_join_log)"
  printf 'auto_promote_controller_voter=%s\n' "$AUTO_PROMOTE_CONTROLLER_VOTER"
  printf 'auto_promote_node_id=%s\n' "$AUTO_JOIN_NODE_ID"
  printf 'auto_promote_manager_api=%s\n' "$AUTO_PROMOTE_MANAGER_API"
  printf 'auto_promote_manager_auth=%s\n' "$AUTO_PROMOTE_MANAGER_AUTH"
  printf 'auto_promote_login_response=%s\n' "$(auto_promote_login_response_file)"
  printf 'auto_promote_activate_response=%s\n' "$(auto_promote_activate_response_file)"
  printf 'auto_promote_nodes_response=%s\n' "$(auto_promote_nodes_response_file)"
  printf 'auto_promote_response=%s\n' "$(auto_promote_response_file)"
  printf 'auto_promote_controller_raft_status=%s\n' "$(auto_promote_controller_raft_status_file)"
  print_start_cmd
  print_sim_cmd
  print_auto_join_cmd
  print_auto_promote_cmd
}

tail_evidence() {
  local path
  path="$(cluster_log)"
  if [[ -f "$path" ]]; then
    printf '\n--- cluster log: %s ---\n' "$path" >&2
    tail -n 120 "$path" >&2 || true
  fi
  path="$(sim_output)"
  if [[ -f "$path" ]]; then
    printf '\n--- sim output: %s ---\n' "$path" >&2
    tail -n 80 "$path" >&2 || true
  fi
  local log_dir
  log_dir="$(node_log_dir)"
  if [[ -d "$log_dir" ]]; then
    local node_log
    for node_log in "$log_dir"/node*.log; do
      [[ -f "$node_log" ]] || continue
      printf '\n--- node log: %s ---\n' "$node_log" >&2
      tail -n 80 "$node_log" >&2 || true
    done
  fi
}

stop_cluster() {
  if [[ -z "$CLUSTER_PID" ]]; then
    return
  fi
  if kill -0 "$CLUSTER_PID" 2>/dev/null; then
    log 'stopping cluster'
    kill "$CLUSTER_PID" 2>/dev/null || true
  fi
  wait "$CLUSTER_PID" 2>/dev/null || true
  CLUSTER_PID=""
}

stop_auto_join_node() {
  if [[ -n "$AUTO_JOIN_TIMER_PID" ]]; then
    if kill -0 "$AUTO_JOIN_TIMER_PID" 2>/dev/null; then
      kill "$AUTO_JOIN_TIMER_PID" 2>/dev/null || true
    fi
    wait "$AUTO_JOIN_TIMER_PID" 2>/dev/null || true
    AUTO_JOIN_TIMER_PID=""
  fi
  if [[ -z "$AUTO_JOIN_PID" && -f "$(auto_join_pid_file)" ]]; then
    AUTO_JOIN_PID="$(tr -d '[:space:]' <"$(auto_join_pid_file)")"
  fi
  if [[ -z "$AUTO_JOIN_PID" ]]; then
    return
  fi
  if kill -0 "$AUTO_JOIN_PID" 2>/dev/null; then
    log "stopping auto-join node${AUTO_JOIN_NODE_ID}"
    kill "$AUTO_JOIN_PID" 2>/dev/null || true
  fi
  wait "$AUTO_JOIN_PID" 2>/dev/null || true
  AUTO_JOIN_PID=""
}

cleanup() {
  stop_auto_join_node
  stop_cluster
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
    --start-script)
      [[ $# -ge 2 ]] || die '--start-script requires a value'
      START_SCRIPT="$2"
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
    --no-build)
      BUILD_CLUSTER=0
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
    --auto-join-node)
      AUTO_JOIN_NODE=true
      shift
      ;;
    --no-auto-join-node)
      AUTO_JOIN_NODE=false
      shift
      ;;
    --auto-join-after)
      [[ $# -ge 2 ]] || die '--auto-join-after requires a value'
      AUTO_JOIN_AFTER="$2"
      shift 2
      ;;
    --auto-join-node-id)
      [[ $# -ge 2 ]] || die '--auto-join-node-id requires a value'
      AUTO_JOIN_NODE_ID="$2"
      shift 2
      ;;
    --auto-join-api)
      [[ $# -ge 2 ]] || die '--auto-join-api requires a value'
      AUTO_JOIN_API_ADDR="$2"
      shift 2
      ;;
    --auto-join-gateway)
      [[ $# -ge 2 ]] || die '--auto-join-gateway requires a value'
      AUTO_JOIN_GATEWAY_ADDR="$2"
      shift 2
      ;;
    --auto-join-cluster)
      [[ $# -ge 2 ]] || die '--auto-join-cluster requires a value'
      AUTO_JOIN_CLUSTER_ADDR="$2"
      shift 2
      ;;
    --auto-join-seeds)
      [[ $# -ge 2 ]] || die '--auto-join-seeds requires a value'
      AUTO_JOIN_SEEDS="$2"
      shift 2
      ;;
    --auto-join-token)
      [[ $# -ge 2 ]] || die '--auto-join-token requires a value'
      AUTO_JOIN_TOKEN="$2"
      shift 2
      ;;
    --auto-join-cluster-id)
      [[ $# -ge 2 ]] || die '--auto-join-cluster-id requires a value'
      AUTO_JOIN_CLUSTER_ID="$2"
      shift 2
      ;;
    --auto-join-config)
      [[ $# -ge 2 ]] || die '--auto-join-config requires a value'
      AUTO_JOIN_CONFIG_PATH="$2"
      AUTO_JOIN_CONFIG_PATH_SET=1
      shift 2
      ;;
    --auto-join-data-dir)
      [[ $# -ge 2 ]] || die '--auto-join-data-dir requires a value'
      AUTO_JOIN_DATA_DIR="$2"
      AUTO_JOIN_DATA_DIR_SET=1
      shift 2
      ;;
    --auto-promote-controller-voter)
      AUTO_PROMOTE_CONTROLLER_VOTER=true
      shift
      ;;
    --no-auto-promote-controller-voter)
      AUTO_PROMOTE_CONTROLLER_VOTER=false
      shift
      ;;
    --auto-promote-manager-api)
      [[ $# -ge 2 ]] || die '--auto-promote-manager-api requires a value'
      AUTO_PROMOTE_MANAGER_API="$2"
      shift 2
      ;;
    --auto-promote-manager-auth)
      AUTO_PROMOTE_MANAGER_AUTH=true
      shift
      ;;
    --no-auto-promote-manager-auth)
      AUTO_PROMOTE_MANAGER_AUTH=false
      shift
      ;;
    --auto-promote-manager-user)
      [[ $# -ge 2 ]] || die '--auto-promote-manager-user requires a value'
      AUTO_PROMOTE_MANAGER_USERNAME="$2"
      shift 2
      ;;
    --auto-promote-manager-password)
      [[ $# -ge 2 ]] || die '--auto-promote-manager-password requires a value'
      AUTO_PROMOTE_MANAGER_PASSWORD="$2"
      shift 2
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

if [[ "$AUTO_JOIN_CONFIG_PATH_SET" -eq 0 ]]; then
  AUTO_JOIN_CONFIG_PATH="$OUT_DIR/wukongimv2-node${AUTO_JOIN_NODE_ID}.conf"
fi
if [[ "$AUTO_JOIN_DATA_DIR_SET" -eq 0 ]]; then
  AUTO_JOIN_DATA_DIR="$OUT_DIR/node${AUTO_JOIN_NODE_ID}-data"
fi

split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
split_csv "$AUTO_JOIN_SEEDS" AUTO_JOIN_SEED_VALUES
require_positive_uint '--ready-timeout' "$READY_TIMEOUT"
require_uint '--poll' "$POLL_INTERVAL"
require_positive_uint '--users' "$USERS"
require_positive_uint '--groups' "$GROUP_COUNT"
require_positive_uint '--members' "$GROUP_MEMBERS"
require_bool 'WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_NODE' "$AUTO_JOIN_NODE"
require_bool 'WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_CONTROLLER_VOTER' "$AUTO_PROMOTE_CONTROLLER_VOTER"
require_bool 'WK_WKCLI_SIM_THREE_SMOKE_AUTO_PROMOTE_MANAGER_AUTH' "$AUTO_PROMOTE_MANAGER_AUTH"
require_uint '--auto-join-after' "$AUTO_JOIN_AFTER"
require_positive_uint '--auto-join-node-id' "$AUTO_JOIN_NODE_ID"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_FLUSH_ERROR_SELECTED_ROWS' "$MAX_FLUSH_ERROR_SELECTED_ROWS"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_HANDOFF_ERROR_TOTAL' "$MAX_HANDOFF_ERROR_TOTAL"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_HANDOFF_TIMEOUT_TOTAL' "$MAX_HANDOFF_TIMEOUT_TOTAL"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_GOROUTINES' "$MAX_GOROUTINES"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_HEAP_ALLOC_BYTES' "$MAX_HEAP_ALLOC_BYTES"

if [[ "${#API_VALUES[@]}" -eq 0 ]]; then
  die 'at least one --api value is required'
fi
if [[ "${#GATEWAY_VALUES[@]}" -eq 0 ]]; then
  die 'at least one --gateway value is required'
fi
if [[ "${#API_VALUES[@]}" -ne "${#GATEWAY_VALUES[@]}" ]]; then
  die "--api and --gateway must contain the same number of items: ${#API_VALUES[@]} != ${#GATEWAY_VALUES[@]}"
fi
AUTO_PROMOTE_MANAGER_API="${AUTO_PROMOTE_MANAGER_API%/}"
if [[ "$AUTO_JOIN_NODE" == "true" ]]; then
  require_nonempty '--auto-join-api' "$AUTO_JOIN_API_ADDR"
  require_nonempty '--auto-join-gateway' "$AUTO_JOIN_GATEWAY_ADDR"
  require_nonempty '--auto-join-cluster' "$AUTO_JOIN_CLUSTER_ADDR"
  require_nonempty '--auto-join-token' "$AUTO_JOIN_TOKEN"
  require_nonempty '--auto-join-cluster-id' "$AUTO_JOIN_CLUSTER_ID"
  require_nonempty '--auto-join-config' "$AUTO_JOIN_CONFIG_PATH"
  require_nonempty '--auto-join-data-dir' "$AUTO_JOIN_DATA_DIR"
  [[ "${#AUTO_JOIN_SEED_VALUES[@]}" -gt 0 ]] || die 'at least one --auto-join-seeds value is required'
fi
if [[ "$AUTO_PROMOTE_CONTROLLER_VOTER" == "true" ]]; then
  [[ "$AUTO_JOIN_NODE" == "true" ]] || die '--auto-promote-controller-voter requires --auto-join-node'
  require_nonempty '--auto-promote-manager-api' "$AUTO_PROMOTE_MANAGER_API"
  if [[ "$AUTO_PROMOTE_MANAGER_AUTH" == "true" ]]; then
    require_nonempty '--auto-promote-manager-user' "$AUTO_PROMOTE_MANAGER_USERNAME"
    require_nonempty '--auto-promote-manager-password' "$AUTO_PROMOTE_MANAGER_PASSWORD"
  fi
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

mkdir -p "$OUT_DIR" "$(node_log_dir)" "$(capabilities_dir)" "$(capacity_dir)" "$(snapshot_dir)" "$(metrics_dir)"
: >"$(cluster_log)"
: >"$(sim_output)"
prepare_auto_join_state

check_cluster_process() {
  if [[ "$START_CLUSTER" -eq 0 || -z "$CLUSTER_PID" ]]; then
    return
  fi
  if ! kill -0 "$CLUSTER_PID" 2>/dev/null; then
    local status=0
    wait "$CLUSTER_PID" 2>/dev/null || status=$?
    CLUSTER_PID=""
    die "cluster start script exited before smoke completed with status ${status}"
  fi
}

check_auto_join_process() {
  if [[ -z "$AUTO_JOIN_PID" && -f "$(auto_join_pid_file)" ]]; then
    AUTO_JOIN_PID="$(tr -d '[:space:]' <"$(auto_join_pid_file)")"
    AUTO_JOIN_STARTED=1
  fi
  if [[ "$AUTO_JOIN_STARTED" -eq 0 || -z "$AUTO_JOIN_PID" ]]; then
    return
  fi
  if ! kill -0 "$AUTO_JOIN_PID" 2>/dev/null; then
    local status=0
    wait "$AUTO_JOIN_PID" 2>/dev/null || status=$?
    AUTO_JOIN_PID=""
    die "auto-join node${AUTO_JOIN_NODE_ID} exited before smoke completed with status ${status}"
  fi
}

start_cluster() {
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    log 'using already-running cluster'
    return
  fi
  local args=("$START_SCRIPT")
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    args+=(--clean)
  fi
  args+=(--ready-timeout "$READY_TIMEOUT" --bin "$(cluster_bin)" --log-dir "$(node_log_dir)")
  if [[ "$BUILD_CLUSTER" -eq 0 ]]; then
    args+=(--no-build)
  fi
  log "starting three-node cluster: $START_SCRIPT"
  (
    cd "$ROOT_DIR"
    exec env "WK_DEBUG_API_ENABLE=$DEBUG_API_ENABLE" "${args[@]}"
  ) >"$(cluster_log)" 2>&1 &
  CLUSTER_PID="$!"
  log "cluster pid=${CLUSTER_PID} log=$(cluster_log)"
}

render_auto_join_config() {
  local api_listen
  local seed_json
  api_listen="$(auto_join_api_listen_addr)"
  seed_json="$(json_string_list "${AUTO_JOIN_SEED_VALUES[@]}")"
  mkdir -p "$(dirname "$AUTO_JOIN_CONFIG_PATH")" "$AUTO_JOIN_DATA_DIR" "$(node_log_dir)"
  cat >"$AUTO_JOIN_CONFIG_PATH" <<CONFIG
# WuKongIM v2 seed-join node config generated by smoke-wkcli-sim-wukongimv2-three-nodes.sh.
WK_NODE_ID=${AUTO_JOIN_NODE_ID}
WK_NODE_DATA_DIR=${AUTO_JOIN_DATA_DIR}
WK_CLUSTER_LISTEN_ADDR=${AUTO_JOIN_CLUSTER_ADDR}
WK_CLUSTER_ADVERTISE_ADDR=${AUTO_JOIN_CLUSTER_ADDR}
WK_CLUSTER_ID=${AUTO_JOIN_CLUSTER_ID}
WK_CLUSTER_SEEDS=${seed_json}
WK_CLUSTER_JOIN_TOKEN=${AUTO_JOIN_TOKEN}
WK_CLUSTER_INITIAL_SLOT_COUNT=10
WK_CLUSTER_HASH_SLOT_COUNT=256
WK_CLUSTER_SLOT_REPLICA_N=3
WK_API_LISTEN_ADDR=${api_listen}
WK_BENCH_API_ENABLE=true
WK_BENCH_API_MAX_BATCH_SIZE=10000
WK_BENCH_API_MAX_PAYLOAD_BYTES=10485760
WK_METRICS_ENABLE=true
WK_PROMETHEUS_ENABLE=false
WK_DEBUG_API_ENABLE=${DEBUG_API_ENABLE}
WK_TOP_API_ENABLE=true
WK_LOG_LEVEL=info
WK_LOG_DIR=${AUTO_JOIN_DATA_DIR}/logs
WK_LOG_CONSOLE=true
WK_LOG_FORMAT=console
WK_EXTERNAL_TCPADDR=${AUTO_JOIN_GATEWAY_ADDR}
WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"${AUTO_JOIN_GATEWAY_ADDR}","transport":"gnet","protocol":"wkproto"}]
WK_GATEWAY_SEND_TIMEOUT=5s
WK_DELIVERY_ENABLE=true
CONFIG
}

start_auto_join_node() {
  if [[ "$AUTO_JOIN_NODE" != "true" || "$AUTO_JOIN_STARTED" -eq 1 ]]; then
    return
  fi
  local bin
  bin="$(cluster_bin)"
  [[ -x "$bin" ]] || die "auto-join requires executable cluster binary: $bin"
  render_auto_join_config
  : >"$(auto_join_log)"
  log "starting auto-join node${AUTO_JOIN_NODE_ID}: delay=${AUTO_JOIN_AFTER}s config=${AUTO_JOIN_CONFIG_PATH}"
  (
    cd "$ROOT_DIR"
    exec env "WK_DEBUG_API_ENABLE=$DEBUG_API_ENABLE" "$bin" -config "$AUTO_JOIN_CONFIG_PATH"
  ) >"$(auto_join_log)" 2>&1 &
  AUTO_JOIN_PID="$!"
  printf '%s\n' "$AUTO_JOIN_PID" >"$(auto_join_pid_file)"
  AUTO_JOIN_STARTED=1
  log "auto-join node${AUTO_JOIN_NODE_ID} pid=${AUTO_JOIN_PID} log=$(auto_join_log)"
}

wait_auto_join_ready() {
  if [[ "$AUTO_JOIN_STARTED" -eq 0 ]]; then
    return
  fi
  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_cluster_process
    check_auto_join_process
    if curl -fsS --max-time 2 "$AUTO_JOIN_API_ADDR/readyz" >/dev/null 2>&1; then
      log "auto join node${AUTO_JOIN_NODE_ID} ready: $AUTO_JOIN_API_ADDR/readyz"
      return
    fi
    sleep "$POLL_INTERVAL"
  done
  die "auto-join node${AUTO_JOIN_NODE_ID} did not become ready within ${READY_TIMEOUT}s"
}

compact_json_file() {
  tr -d '[:space:]' <"$1"
}

json_array_file_contains_int() {
  local file="$1"
  local key="$2"
  local value="$3"
  local compact
  compact="$(compact_json_file "$file")"
  printf '%s\n' "$compact" | grep -Eq "\"${key}\":\\[[^]]*(^|[^0-9])${value}([^0-9]|\\])"
}

nodes_response_has_promotable_auto_join_node() {
  local file="$1"
  local compact
  local tail
  local node
  compact="$(compact_json_file "$file")"
  tail="${compact#*\"node_id\":${AUTO_JOIN_NODE_ID}}"
  [[ "$tail" != "$compact" ]] || return 1
  node="${tail%%},{\"node_id\":*}"
  printf '%s\n' "$node" | grep -q '"join_state":"active"' || return 1
  printf '%s\n' "$node" | grep -q '"can_promote_controller_voter":true'
}

auto_promote_manager_login() {
  if [[ "$AUTO_PROMOTE_MANAGER_AUTH" != "true" ]]; then
    rm -f "$(auto_promote_token_file)"
    return
  fi
  local url
  local file
  local tmp
  local body
  local token
  local deadline
  url="${AUTO_PROMOTE_MANAGER_API}/manager/login"
  file="$(auto_promote_login_response_file)"
  tmp="${file}.tmp"
  body="{\"username\":\"${AUTO_PROMOTE_MANAGER_USERNAME}\",\"password\":\"${AUTO_PROMOTE_MANAGER_PASSWORD}\"}"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_cluster_process
    check_auto_join_process
    if curl -fsS -H 'Content-Type: application/json' -d "$body" "$url" >"$tmp"; then
      mv "$tmp" "$file"
      token="$(json_string access_token <"$file" | head -n 1)"
      [[ -n "$token" ]] || die 'auto-promote manager login response missing access_token'
      printf '%s\n' "$token" >"$(auto_promote_token_file)"
      log 'auto-promote manager login ok'
      return
    fi
    rm -f "$tmp"
    sleep "$POLL_INTERVAL"
  done
  die "auto-promote manager login did not succeed within ${READY_TIMEOUT}s"
}

manager_curl() {
  local method="$1"
  local url="$2"
  local output="$3"
  local args=(-fsS)
  if [[ "$AUTO_PROMOTE_MANAGER_AUTH" == "true" ]]; then
    local token
    [[ -f "$(auto_promote_token_file)" ]] || die 'auto-promote manager token is missing'
    token="$(tr -d '[:space:]' <"$(auto_promote_token_file)")"
    [[ -n "$token" ]] || die 'auto-promote manager token is empty'
    args+=(-H "Authorization: Bearer ${token}")
  fi
  if [[ "$method" != "GET" ]]; then
    args+=(-X "$method")
  fi
  curl "${args[@]}" "$url" >"$output"
}

wait_auto_promote_activation() {
  local url
  local file
  local tmp
  local deadline
  url="${AUTO_PROMOTE_MANAGER_API}/manager/nodes/${AUTO_JOIN_NODE_ID}/activate"
  file="$(auto_promote_activate_response_file)"
  tmp="${file}.tmp"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_cluster_process
    check_auto_join_process
    if manager_curl POST "$url" "$tmp"; then
      mv "$tmp" "$file"
      grep -q "\"node_id\":${AUTO_JOIN_NODE_ID}" "$file" || die "auto-promote activation response missing node_id=${AUTO_JOIN_NODE_ID}"
      grep -q '"join_state":"active"' "$file" || die "auto-promote activation response missing join_state=active"
      log "auto-promote controller voter node${AUTO_JOIN_NODE_ID} activated"
      return
    fi
    rm -f "$tmp"
    sleep "$POLL_INTERVAL"
  done
  die "auto-promote controller voter node${AUTO_JOIN_NODE_ID} activation did not succeed within ${READY_TIMEOUT}s"
}

wait_auto_promote_promotable() {
  local url
  local file
  local tmp
  local deadline
  url="${AUTO_PROMOTE_MANAGER_API}/manager/nodes"
  file="$(auto_promote_nodes_response_file)"
  tmp="${file}.tmp"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_cluster_process
    check_auto_join_process
    if manager_curl GET "$url" "$tmp"; then
      mv "$tmp" "$file"
      if nodes_response_has_promotable_auto_join_node "$file"; then
        log "auto-promote controller voter node${AUTO_JOIN_NODE_ID} promotable"
        return
      fi
    else
      rm -f "$tmp"
    fi
    sleep "$POLL_INTERVAL"
  done
  die "auto-promote controller voter node${AUTO_JOIN_NODE_ID} did not become promotable within ${READY_TIMEOUT}s"
}

wait_auto_promote_write() {
  local url
  local file
  local tmp
  local deadline
  url="${AUTO_PROMOTE_MANAGER_API}/manager/nodes/${AUTO_JOIN_NODE_ID}/controller-voter/promote"
  file="$(auto_promote_response_file)"
  tmp="${file}.tmp"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_cluster_process
    check_auto_join_process
    if manager_curl POST "$url" "$tmp"; then
      mv "$tmp" "$file"
      grep -q "\"node_id\":${AUTO_JOIN_NODE_ID}" "$file" || die "auto-promote response missing node_id=${AUTO_JOIN_NODE_ID}"
      json_array_file_contains_int "$file" next_voters "$AUTO_JOIN_NODE_ID" || die "auto-promote response next_voters missing node${AUTO_JOIN_NODE_ID}"
      log "auto-promote controller voter node${AUTO_JOIN_NODE_ID} accepted"
      return
    fi
    rm -f "$tmp"
    sleep "$POLL_INTERVAL"
  done
  die "auto-promote controller voter node${AUTO_JOIN_NODE_ID} write did not succeed within ${READY_TIMEOUT}s"
}

wait_auto_promote_controller_raft() {
  local url
  local file
  local tmp
  local deadline
  url="${AUTO_PROMOTE_MANAGER_API}/manager/nodes/${AUTO_JOIN_NODE_ID}/controller-raft"
  file="$(auto_promote_controller_raft_status_file)"
  tmp="${file}.tmp"
  deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_cluster_process
    check_auto_join_process
    if manager_curl GET "$url" "$tmp"; then
      mv "$tmp" "$file"
      if json_array_file_contains_int "$file" voters "$AUTO_JOIN_NODE_ID"; then
        log "auto-promote controller raft node${AUTO_JOIN_NODE_ID} voters include node${AUTO_JOIN_NODE_ID}"
        return
      fi
    else
      rm -f "$tmp"
    fi
    sleep "$POLL_INTERVAL"
  done
  die "auto-promote controller raft node${AUTO_JOIN_NODE_ID} voters did not include node${AUTO_JOIN_NODE_ID} within ${READY_TIMEOUT}s"
}

promote_auto_join_controller_voter() {
  if [[ "$AUTO_PROMOTE_CONTROLLER_VOTER" != "true" ]]; then
    return
  fi
  auto_promote_manager_login
  wait_auto_promote_activation
  wait_auto_promote_promotable
  wait_auto_promote_write
  wait_auto_promote_controller_raft
  touch "$(auto_promote_ready_file)"
}

schedule_auto_join_node() {
  if [[ "$AUTO_JOIN_NODE" != "true" ]]; then
    return
  fi
  rm -f "$(auto_join_pid_file)" "$(auto_join_ready_file)" "$(auto_promote_ready_file)"
  (
    sleep "$AUTO_JOIN_AFTER"
    if [[ -f "$(sim_done_file)" ]]; then
      exit 0
    fi
    start_auto_join_node
    wait_auto_join_ready
    touch "$(auto_join_ready_file)"
    promote_auto_join_controller_voter
  ) &
  AUTO_JOIN_TIMER_PID="$!"
  log "auto-join node${AUTO_JOIN_NODE_ID} scheduled after ${AUTO_JOIN_AFTER}s"
}

wait_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local ready=()
  local api
  for _ in "${API_VALUES[@]}"; do
    ready+=(0)
  done
  while (( SECONDS <= deadline )); do
    check_cluster_process
    local all_ready=1
    local idx=0
    for api in "${API_VALUES[@]}"; do
      if [[ "${ready[$idx]}" -eq 1 ]]; then
        idx=$((idx + 1))
        continue
      fi
      if curl -fsS --max-time 2 "$api/readyz" >/dev/null 2>&1; then
        ready[$idx]=1
        log "node$((idx + 1)) ready: $api/readyz"
      else
        all_ready=0
      fi
      idx=$((idx + 1))
    done
    if [[ "$all_ready" -eq 1 ]]; then
      log 'all nodes ready'
      return
    fi
    sleep "$POLL_INTERVAL"
  done
  die "cluster did not become ready within ${READY_TIMEOUT}s"
}

capture_one_target_evidence() {
  local node="$1"
  local api="$2"
  local gateway="$3"
  local caps
  local capacity
  caps="$(node_file "$(capabilities_dir)" "$node")"
  capacity="$(node_file "$(capacity_dir)" "$node")"
  curl -fsS "$api/bench/v1/capabilities" >"$caps"
  curl -fsS "$api/bench/v1/capacity-target" >"$capacity"
  grep -q '"channels_batch":true' "$caps" || die "node${node} bench capabilities missing channels_batch=true"
  grep -q '"channel_subscribers_batch":true' "$caps" || die "node${node} bench capabilities missing channel_subscribers_batch=true"
  grep -q '"snapshot":true' "$caps" || die "node${node} bench capabilities missing snapshot=true"
  grep -q '"group"' "$caps" || die "node${node} bench capabilities missing group channel type"
  grep -q "\"tcp_addr\":\"${gateway}\"" "$capacity" || die "node${node} capacity target did not publish expected gateway ${gateway}"
}

capture_target_evidence() {
  local idx=0
  local api
  for api in "${API_VALUES[@]}"; do
    capture_one_target_evidence "$((idx + 1))" "$api" "${GATEWAY_VALUES[$idx]}"
    idx=$((idx + 1))
  done
}

run_sim() {
  local cmd=(go run ./cmd/wkcli sim)
  local api
  local gateway
  for api in "${API_VALUES[@]}"; do
    cmd+=(--server "$api")
  done
  for gateway in "${GATEWAY_VALUES[@]}"; do
    cmd+=(--gateway "$gateway")
  done
  cmd+=(--users "$USERS")
  cmd+=(--groups "$GROUP_COUNT")
  cmd+=(--group-members "$GROUP_MEMBERS")
  cmd+=(--rate "$RATE")
  cmd+=(--payload-size "$PAYLOAD_SIZE")
  cmd+=(--max-runtime "$DURATION")
  cmd+=(--status-listen "$STATUS_LISTEN")
  cmd+=(--status-interval "$STATUS_INTERVAL")
  cmd+=(--json)
  log "running wkcli sim: users=${USERS} groups=${GROUP_COUNT} members=${GROUP_MEMBERS} gateways=${#GATEWAY_VALUES[@]}"
  local status=0
  rm -f "$(sim_done_file)"
  (
    set -o pipefail
    cd "$ROOT_DIR"
    "${cmd[@]}" | tee "$(sim_output)"
  ) &
  local sim_pid="$!"
  schedule_auto_join_node
  wait "$sim_pid" || status=$?
  touch "$(sim_done_file)"
  if [[ "$status" -ne 0 ]]; then
    stop_auto_join_node
    die "wkcli sim failed with status ${status}"
  fi
  if [[ "$AUTO_JOIN_NODE" == "true" ]]; then
    if [[ ! -f "$(auto_join_pid_file)" ]]; then
      stop_auto_join_node
      die "auto-join node${AUTO_JOIN_NODE_ID} did not start before wkcli sim completed; increase --duration or lower --auto-join-after"
    fi
    local timer_status=0
    if [[ -n "$AUTO_JOIN_TIMER_PID" ]]; then
      wait "$AUTO_JOIN_TIMER_PID" || timer_status=$?
      AUTO_JOIN_TIMER_PID=""
    fi
    if [[ "$timer_status" -ne 0 ]]; then
      die "auto-join node${AUTO_JOIN_NODE_ID} failed with status ${timer_status}"
    fi
    [[ -f "$(auto_join_ready_file)" ]] || die "auto-join node${AUTO_JOIN_NODE_ID} did not report ready"
    if [[ "$AUTO_PROMOTE_CONTROLLER_VOTER" == "true" ]]; then
      [[ -f "$(auto_promote_ready_file)" ]] || die "auto-promote controller voter node${AUTO_JOIN_NODE_ID} did not complete"
    fi
    check_auto_join_process
  fi
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

capture_snapshots() {
  local idx=0
  local api
  local snapshots_with_counts=0
  for api in "${API_VALUES[@]}"; do
    local node=$((idx + 1))
    local snapshot
    snapshot="$(node_file "$(snapshot_dir)" "$node")"
    curl -fsS "$api/bench/v1/snapshot" >"$snapshot"
    grep -q '"version":"bench/v1"' "$snapshot" || die "node${node} bench snapshot missing version=bench/v1"
    if grep -q '"accepted_channels"' "$snapshot"; then
      snapshots_with_counts=$((snapshots_with_counts + 1))
    fi
    idx=$((idx + 1))
  done
  (( snapshots_with_counts > 0 )) || die 'bench snapshots missing accepted_channels on every node'
  log "bench snapshots: $(snapshot_dir)"
}

capture_auto_join_evidence() {
  if [[ "$AUTO_JOIN_NODE" != "true" || "$AUTO_JOIN_STARTED" -eq 0 ]]; then
    return
  fi
  check_auto_join_process
  capture_one_target_evidence "$AUTO_JOIN_NODE_ID" "$AUTO_JOIN_API_ADDR" "$AUTO_JOIN_GATEWAY_ADDR"
  local snapshot
  snapshot="$(node_file "$(snapshot_dir)" "$AUTO_JOIN_NODE_ID")"
  curl -fsS "$AUTO_JOIN_API_ADDR/bench/v1/snapshot" >"$snapshot"
  grep -q '"version":"bench/v1"' "$snapshot" || die "node${AUTO_JOIN_NODE_ID} bench snapshot missing version=bench/v1"
  curl -fsS "$AUTO_JOIN_API_ADDR/metrics" >"$(metric_file after "$AUTO_JOIN_NODE_ID")"
  log "auto-join node${AUTO_JOIN_NODE_ID} evidence captured"
}

metric_file() {
  local phase="$1"
  local index="$2"
  printf '%s/node%s-%s.prom' "$(metrics_dir)" "$index" "$phase"
}

capture_metrics() {
  local phase="$1"
  local idx=0
  local api
  for api in "${API_VALUES[@]}"; do
    local node=$((idx + 1))
    curl -fsS "$api/metrics" >"$(metric_file "$phase" "$node")"
    idx=$((idx + 1))
  done
  log "metrics ${phase}: $(metrics_dir)"
}

metric_sum() {
  local file="$1"
  local metric="$2"
  local label_a="${3:-}"
  local label_b="${4:-}"
  awk -v metric="$metric" -v label_a="$label_a" -v label_b="$label_b" '
    $1 ~ ("^" metric "(\\{| )") {
      if (label_a != "" && index($0, label_a) == 0) next
      if (label_b != "" && index($0, label_b) == 0) next
      sum += $NF
    }
    END { printf "%.0f\n", sum }
  ' "$file"
}

metric_delta() {
  local before="$1"
  local after="$2"
  local metric="$3"
  local label_a="${4:-}"
  local label_b="${5:-}"
  local before_value
  local after_value
  before_value="$(metric_sum "$before" "$metric" "$label_a" "$label_b")"
  after_value="$(metric_sum "$after" "$metric" "$label_a" "$label_b")"
  awk -v before="$before_value" -v after="$after_value" 'BEGIN {
    delta = after - before
    if (delta < 0) delta = after
    printf "%.0f\n", delta
  }'
}

verify_metric_limit() {
  local name="$1"
  local value="$2"
  local limit="$3"
  if (( value > limit )); then
    die "${name}=${value} exceeds limit ${limit}"
  fi
}

verify_metrics_health() {
  local idx=0
  while (( idx < ${#API_VALUES[@]} )); do
    local node=$((idx + 1))
    local before
    local after
    before="$(metric_file before "$node")"
    after="$(metric_file after "$node")"
    local selected_error
    local handoff_error
    local handoff_timeout
    local goroutines
    local heap_alloc
    selected_error="$(metric_delta "$before" "$after" 'wukongim_conversation_active_flush_rows_sum' 'kind="selected"' 'result="error"')"
    handoff_error="$(metric_delta "$before" "$after" 'wukongim_conversation_authority_handoff_total' 'result="error"')"
    handoff_timeout="$(metric_delta "$before" "$after" 'wukongim_conversation_authority_handoff_total' 'result="timeout"')"
    goroutines="$(metric_sum "$after" 'go_goroutines')"
    heap_alloc="$(metric_sum "$after" 'go_memstats_heap_alloc_bytes')"
    verify_metric_limit "node${node} conversation_active selected_error_rows" "$selected_error" "$MAX_FLUSH_ERROR_SELECTED_ROWS"
    verify_metric_limit "node${node} conversation_authority handoff_error" "$handoff_error" "$MAX_HANDOFF_ERROR_TOTAL"
    verify_metric_limit "node${node} conversation_authority handoff_timeout" "$handoff_timeout" "$MAX_HANDOFF_TIMEOUT_TOTAL"
    verify_metric_limit "node${node} go_goroutines" "$goroutines" "$MAX_GOROUTINES"
    verify_metric_limit "node${node} heap_alloc_bytes" "$heap_alloc" "$MAX_HEAP_ALLOC_BYTES"
    idx=$((idx + 1))
  done
  log 'metrics health gates passed'
}

write_summary() {
  local final state sent errors
  final="$(grep '"state"' "$(sim_output)" | tail -n 1 || true)"
  state="$(printf '%s\n' "$final" | json_string state)"
  sent="$(printf '%s\n' "$final" | json_int messages_sent)"
  errors="$(printf '%s\n' "$final" | json_int send_errors)"
  {
    printf '# wkcli sim three-node smoke\n\n'
    printf '%s\n' "- state: ${state:-unknown}"
    printf '%s\n' "- messages_sent: ${sent:-0}"
    printf '%s\n' "- send_errors: ${errors:-unknown}"
    printf '%s\n' "- api_addrs: $(IFS=','; printf '%s' "${API_VALUES[*]}")"
    printf '%s\n' "- gateway_addrs: $(IFS=','; printf '%s' "${GATEWAY_VALUES[*]}")"
    printf '%s\n' '- sim_output: sim.jsonl'
    printf '%s\n' '- snapshots: bench-snapshots/'
    printf '%s\n' '- metrics: metrics/'
    printf '%s\n' "- max_flush_error_selected_rows: ${MAX_FLUSH_ERROR_SELECTED_ROWS}"
    printf '%s\n' "- max_handoff_error_total: ${MAX_HANDOFF_ERROR_TOTAL}"
    printf '%s\n' "- max_handoff_timeout_total: ${MAX_HANDOFF_TIMEOUT_TOTAL}"
    printf '%s\n' "- max_goroutines: ${MAX_GOROUTINES}"
    printf '%s\n' "- max_heap_alloc_bytes: ${MAX_HEAP_ALLOC_BYTES}"
    printf '%s\n' "- auto_join_node: ${AUTO_JOIN_NODE}"
    printf '%s\n' "- auto_join_after_secs: ${AUTO_JOIN_AFTER}"
    if [[ "$AUTO_JOIN_NODE" == "true" ]]; then
      printf '%s\n' "- auto_join_node_id: ${AUTO_JOIN_NODE_ID}"
      printf '%s\n' "- auto_join_api: ${AUTO_JOIN_API_ADDR}"
      printf '%s\n' "- auto_join_gateway: ${AUTO_JOIN_GATEWAY_ADDR}"
      printf '%s\n' "- auto_join_config: ${AUTO_JOIN_CONFIG_PATH}"
    fi
    printf '%s\n' "- auto_promote_controller_voter: ${AUTO_PROMOTE_CONTROLLER_VOTER}"
    if [[ "$AUTO_PROMOTE_CONTROLLER_VOTER" == "true" ]]; then
      printf '%s\n' "- auto_promote_node_id: ${AUTO_JOIN_NODE_ID}"
      printf '%s\n' "- auto_promote_manager_api: ${AUTO_PROMOTE_MANAGER_API}"
      printf '%s\n' "- auto_promote_manager_auth: ${AUTO_PROMOTE_MANAGER_AUTH}"
      if [[ "$AUTO_PROMOTE_MANAGER_AUTH" == "true" ]]; then
        printf '%s\n' "- auto_promote_login_response: $(basename "$(auto_promote_login_response_file)")"
      fi
      printf '%s\n' "- auto_promote_activate_response: $(basename "$(auto_promote_activate_response_file)")"
      printf '%s\n' "- auto_promote_nodes_response: $(basename "$(auto_promote_nodes_response_file)")"
      printf '%s\n' "- auto_promote_response: $(basename "$(auto_promote_response_file)")"
      printf '%s\n' "- auto_promote_controller_raft_status: $(basename "$(auto_promote_controller_raft_status_file)")"
    fi
  } >"$(summary_output)"
}

start_cluster
wait_ready
capture_target_evidence
capture_metrics before
run_sim
verify_sim_output
capture_metrics after
verify_metrics_health
capture_snapshots
capture_auto_join_evidence
write_summary
log "smoke passed; evidence_dir=$OUT_DIR"
