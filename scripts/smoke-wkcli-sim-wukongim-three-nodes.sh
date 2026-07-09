#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${WK_WKCLI_SIM_THREE_SMOKE_OUT_DIR:-$ROOT_DIR/data/wkcli-sim-three-node-smoke}"
START_SCRIPT="${WK_WKCLI_SIM_THREE_SMOKE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongim-three-nodes.sh}"
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
AUTO_JOIN_CLUSTER_ID="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_CLUSTER_ID:-wukongim-dev-three}"
AUTO_JOIN_CONFIG_PATH="${WK_WKCLI_SIM_THREE_SMOKE_AUTO_JOIN_CONFIG:-$OUT_DIR/wukongim-node4.toml}"
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
FAULT_KILL_NODE="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE:-false}"
FAULT_NODE_ID="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_NODE_ID:-2}"
FAULT_AFTER="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_AFTER:-2}"
FAULT_SIGNAL="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_SIGNAL:-TERM}"
FAULT_PID_DIR="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_PID_DIR:-$OUT_DIR/node-pids}"
FAULT_HEALTH_REPORT_INTERVAL="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_HEALTH_REPORT_INTERVAL:-}"
FAULT_HEALTH_REPORT_TTL="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_HEALTH_REPORT_TTL:-}"
FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL:-}"
FAULT_CHANNEL_MIGRATION_SCAN_LIMIT="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_CHANNEL_MIGRATION_SCAN_LIMIT:-}"
FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK:-}"
FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK:-}"
FAULT_CHANNEL_MIGRATION_TASK_LIMIT="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_CHANNEL_MIGRATION_TASK_LIMIT:-}"
FAULT_GATEWAY_SEND_TIMEOUT="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_GATEWAY_SEND_TIMEOUT:-}"
FAULT_SIM_ACK_TIMEOUT="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_SIM_ACK_TIMEOUT:-}"
FAULT_MAX_SEND_ERRORS="${WK_WKCLI_SIM_THREE_SMOKE_FAULT_MAX_SEND_ERRORS:-0}"
FAULT_PID_DIR_SET=0
[[ -n "${WK_WKCLI_SIM_THREE_SMOKE_FAULT_PID_DIR-}" ]] && FAULT_PID_DIR_SET=1

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
FAULT_TIMER_PID=""
AUTO_JOIN_STARTED=0
API_VALUES=()
GATEWAY_VALUES=()
AUTO_JOIN_SEED_VALUES=()

usage() {
  cat <<'USAGE'
Usage: scripts/smoke-wkcli-sim-wukongim-three-nodes.sh [options]

Starts a local cmd/wukongim three-node cluster, runs a small real wkcli sim
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
  --gateway LIST            Comma-separated WKProto gateway addresses used by wkcli sim. Fault drills may use a survivor subset.
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
  --auto-join-cluster-id ID Cluster ID expected by the joining node. Default: wukongim-dev-three.
  --auto-join-config PATH   Generated joining node config path. Default: OUT_DIR/wukongim-node4.toml.
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
  --fault-kill-node        Kill one static three-node-cluster node while wkcli sim is sending.
  --fault-node-id N        Static node ID to kill. Must be 1, 2, or 3. Default: 2.
  --fault-after SECS       Seconds after the send phase starts before killing the node. Default: 2.
  --fault-signal SIGNAL    Signal used for the kill. Supported: TERM, KILL. Default: TERM.
  --fault-pid-dir DIR      Directory containing node PID files. Default: OUT_DIR/node-pids.
  --fault-health-report-interval DURATION
                           Optional WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL override for the started cluster during fault drills.
  --fault-health-report-ttl DURATION
                           Optional WK_CLUSTER_NODE_HEALTH_REPORT_TTL override for the started cluster during fault drills.
  --fault-channel-migration-scan-interval DURATION
                           Optional WK_CHANNEL_MIGRATION_SCAN_INTERVAL override for the started cluster during fault drills.
  --fault-channel-migration-scan-limit N
                           Optional WK_CHANNEL_MIGRATION_SCAN_LIMIT override for the started cluster during fault drills.
  --fault-channel-migration-max-pages-per-tick N
                           Optional WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK override for the started cluster during fault drills.
  --fault-channel-migration-max-tasks-per-tick N
                           Optional WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK override for the started cluster during fault drills.
  --fault-channel-migration-task-limit N
                           Optional WK_CHANNEL_MIGRATION_TASK_LIMIT override for the started cluster during fault drills.
  --fault-gateway-send-timeout DURATION
                           Optional WK_GATEWAY_SEND_TIMEOUT override for the started cluster during fault drills.
  --fault-sim-ack-timeout DURATION
                           Optional wkcli sim --ack-timeout override for fault drill runs.
  --fault-max-send-errors N
                           Allowed final wkcli sim send_errors. Default: 0.
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
  printf '%s/wukongim' "$OUT_DIR"
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

fault_pid_file() {
  printf '%s/node%s.pid' "$FAULT_PID_DIR" "$FAULT_NODE_ID"
}

fault_event_file() {
  printf '%s/fault-node%s-kill.env' "$OUT_DIR" "$FAULT_NODE_ID"
}

sim_done_file() {
  printf '%s/sim.done' "$OUT_DIR"
}

prepare_fault_state() {
  rm -f "$(fault_event_file)"
  if [[ "$FAULT_KILL_NODE" != "true" ]]; then
    return
  fi
  mkdir -p "$FAULT_PID_DIR"
  if [[ "$START_CLUSTER" -eq 1 && "$CLEAN_CLUSTER" -eq 1 ]]; then
    rm -f "$FAULT_PID_DIR"/node*.pid
  fi
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

toml_string() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

cluster_start_env_preview() {
  local envs=("WK_DEBUG_API_ENABLE=$DEBUG_API_ENABLE")
  if [[ "$FAULT_KILL_NODE" == "true" ]]; then
    [[ -n "$FAULT_HEALTH_REPORT_INTERVAL" ]] && envs+=("WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=$FAULT_HEALTH_REPORT_INTERVAL")
    [[ -n "$FAULT_HEALTH_REPORT_TTL" ]] && envs+=("WK_CLUSTER_NODE_HEALTH_REPORT_TTL=$FAULT_HEALTH_REPORT_TTL")
    [[ -n "$FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL" ]] && envs+=("WK_CHANNEL_MIGRATION_SCAN_INTERVAL=$FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL")
    [[ -n "$FAULT_CHANNEL_MIGRATION_SCAN_LIMIT" ]] && envs+=("WK_CHANNEL_MIGRATION_SCAN_LIMIT=$FAULT_CHANNEL_MIGRATION_SCAN_LIMIT")
    [[ -n "$FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK" ]] && envs+=("WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK=$FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK")
    [[ -n "$FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK" ]] && envs+=("WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK=$FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK")
    [[ -n "$FAULT_CHANNEL_MIGRATION_TASK_LIMIT" ]] && envs+=("WK_CHANNEL_MIGRATION_TASK_LIMIT=$FAULT_CHANNEL_MIGRATION_TASK_LIMIT")
    [[ -n "$FAULT_GATEWAY_SEND_TIMEOUT" ]] && envs+=("WK_GATEWAY_SEND_TIMEOUT=$FAULT_GATEWAY_SEND_TIMEOUT")
  fi
  printf '%s' "${envs[*]}"
}

print_start_cmd() {
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    printf 'start_cmd=<disabled>\n'
    return
  fi
  printf 'start_cmd=env %s %s' "$(cluster_start_env_preview)" "$START_SCRIPT"
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    printf ' --clean'
  fi
  printf ' --ready-timeout %s --bin %s --log-dir %s' "$READY_TIMEOUT" "$(cluster_bin)" "$(node_log_dir)"
  if [[ "$BUILD_CLUSTER" -eq 0 ]]; then
    printf ' --no-build'
  fi
  if [[ "$FAULT_KILL_NODE" == "true" ]]; then
    printf ' --pid-dir %s --allow-node-exit %s' "$FAULT_PID_DIR" "$FAULT_NODE_ID"
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
  printf ' --users %s --groups %s --group-members %s --rate %s --max-runtime %s --payload-size %s --status-listen %s --status-interval %s' \
    "$USERS" "$GROUP_COUNT" "$GROUP_MEMBERS" "$RATE" "$DURATION" "$PAYLOAD_SIZE" "$STATUS_LISTEN" "$STATUS_INTERVAL"
  if [[ -n "$FAULT_SIM_ACK_TIMEOUT" ]]; then
    printf ' --ack-timeout %s' "$FAULT_SIM_ACK_TIMEOUT"
  fi
  printf ' --json\n'
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

print_fault_cmd() {
  if [[ "$FAULT_KILL_NODE" != "true" ]]; then
    printf 'fault_cmd=<disabled>\n'
    return
  fi
  printf 'fault_cmd=kill -s %s $(cat %s)\n' "$FAULT_SIGNAL" "$(fault_pid_file)"
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
  printf 'fault_kill_node=%s\n' "$FAULT_KILL_NODE"
  printf 'fault_node_id=%s\n' "$FAULT_NODE_ID"
  printf 'fault_after_secs=%s\n' "$FAULT_AFTER"
  printf 'fault_signal=%s\n' "$FAULT_SIGNAL"
  printf 'fault_pid_dir=%s\n' "$FAULT_PID_DIR"
  printf 'fault_event_file=%s\n' "$(fault_event_file)"
  printf 'fault_health_report_interval=%s\n' "${FAULT_HEALTH_REPORT_INTERVAL:-<default>}"
  printf 'fault_health_report_ttl=%s\n' "${FAULT_HEALTH_REPORT_TTL:-<default>}"
  printf 'fault_channel_migration_scan_interval=%s\n' "${FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL:-<default>}"
  printf 'fault_channel_migration_scan_limit=%s\n' "${FAULT_CHANNEL_MIGRATION_SCAN_LIMIT:-<default>}"
  printf 'fault_channel_migration_max_pages_per_tick=%s\n' "${FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK:-<default>}"
  printf 'fault_channel_migration_max_tasks_per_tick=%s\n' "${FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK:-<default>}"
  printf 'fault_channel_migration_task_limit=%s\n' "${FAULT_CHANNEL_MIGRATION_TASK_LIMIT:-<default>}"
  printf 'fault_gateway_send_timeout=%s\n' "${FAULT_GATEWAY_SEND_TIMEOUT:-<default>}"
  printf 'fault_sim_ack_timeout=%s\n' "${FAULT_SIM_ACK_TIMEOUT:-<default>}"
  printf 'fault_max_send_errors=%s\n' "$FAULT_MAX_SEND_ERRORS"
  print_start_cmd
  print_sim_cmd
  print_auto_join_cmd
  print_auto_promote_cmd
  print_fault_cmd
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

stop_fault_timer() {
  if [[ -z "$FAULT_TIMER_PID" ]]; then
    return
  fi
  if kill -0 "$FAULT_TIMER_PID" 2>/dev/null; then
    kill "$FAULT_TIMER_PID" 2>/dev/null || true
  fi
  wait "$FAULT_TIMER_PID" 2>/dev/null || true
  FAULT_TIMER_PID=""
}

cleanup() {
  stop_fault_timer
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
    --fault-kill-node)
      FAULT_KILL_NODE=true
      shift
      ;;
    --no-fault-kill-node)
      FAULT_KILL_NODE=false
      shift
      ;;
    --fault-node-id)
      [[ $# -ge 2 ]] || die '--fault-node-id requires a value'
      FAULT_NODE_ID="$2"
      shift 2
      ;;
    --fault-after)
      [[ $# -ge 2 ]] || die '--fault-after requires a value'
      FAULT_AFTER="$2"
      shift 2
      ;;
    --fault-signal)
      [[ $# -ge 2 ]] || die '--fault-signal requires a value'
      FAULT_SIGNAL="$2"
      shift 2
      ;;
    --fault-pid-dir)
      [[ $# -ge 2 ]] || die '--fault-pid-dir requires a value'
      FAULT_PID_DIR="$2"
      FAULT_PID_DIR_SET=1
      shift 2
      ;;
    --fault-health-report-interval)
      [[ $# -ge 2 ]] || die '--fault-health-report-interval requires a value'
      require_nonempty '--fault-health-report-interval' "$2"
      FAULT_HEALTH_REPORT_INTERVAL="$2"
      shift 2
      ;;
    --fault-health-report-ttl)
      [[ $# -ge 2 ]] || die '--fault-health-report-ttl requires a value'
      require_nonempty '--fault-health-report-ttl' "$2"
      FAULT_HEALTH_REPORT_TTL="$2"
      shift 2
      ;;
    --fault-channel-migration-scan-interval)
      [[ $# -ge 2 ]] || die '--fault-channel-migration-scan-interval requires a value'
      require_nonempty '--fault-channel-migration-scan-interval' "$2"
      FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL="$2"
      shift 2
      ;;
    --fault-channel-migration-scan-limit)
      [[ $# -ge 2 ]] || die '--fault-channel-migration-scan-limit requires a value'
      FAULT_CHANNEL_MIGRATION_SCAN_LIMIT="$2"
      shift 2
      ;;
    --fault-channel-migration-max-pages-per-tick)
      [[ $# -ge 2 ]] || die '--fault-channel-migration-max-pages-per-tick requires a value'
      FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK="$2"
      shift 2
      ;;
    --fault-channel-migration-max-tasks-per-tick)
      [[ $# -ge 2 ]] || die '--fault-channel-migration-max-tasks-per-tick requires a value'
      FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK="$2"
      shift 2
      ;;
    --fault-channel-migration-task-limit)
      [[ $# -ge 2 ]] || die '--fault-channel-migration-task-limit requires a value'
      FAULT_CHANNEL_MIGRATION_TASK_LIMIT="$2"
      shift 2
      ;;
    --fault-gateway-send-timeout)
      [[ $# -ge 2 ]] || die '--fault-gateway-send-timeout requires a value'
      require_nonempty '--fault-gateway-send-timeout' "$2"
      FAULT_GATEWAY_SEND_TIMEOUT="$2"
      shift 2
      ;;
    --fault-sim-ack-timeout)
      [[ $# -ge 2 ]] || die '--fault-sim-ack-timeout requires a value'
      require_nonempty '--fault-sim-ack-timeout' "$2"
      FAULT_SIM_ACK_TIMEOUT="$2"
      shift 2
      ;;
    --fault-max-send-errors)
      [[ $# -ge 2 ]] || die '--fault-max-send-errors requires a value'
      FAULT_MAX_SEND_ERRORS="$2"
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
  AUTO_JOIN_CONFIG_PATH="$OUT_DIR/wukongim-node${AUTO_JOIN_NODE_ID}.toml"
fi
if [[ "$AUTO_JOIN_DATA_DIR_SET" -eq 0 ]]; then
  AUTO_JOIN_DATA_DIR="$OUT_DIR/node${AUTO_JOIN_NODE_ID}-data"
fi
if [[ "$FAULT_PID_DIR_SET" -eq 0 ]]; then
  FAULT_PID_DIR="$OUT_DIR/node-pids"
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
require_bool 'WK_WKCLI_SIM_THREE_SMOKE_FAULT_KILL_NODE' "$FAULT_KILL_NODE"
require_uint '--auto-join-after' "$AUTO_JOIN_AFTER"
require_positive_uint '--auto-join-node-id' "$AUTO_JOIN_NODE_ID"
require_uint '--fault-after' "$FAULT_AFTER"
require_uint '--fault-max-send-errors' "$FAULT_MAX_SEND_ERRORS"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_FLUSH_ERROR_SELECTED_ROWS' "$MAX_FLUSH_ERROR_SELECTED_ROWS"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_HANDOFF_ERROR_TOTAL' "$MAX_HANDOFF_ERROR_TOTAL"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_HANDOFF_TIMEOUT_TOTAL' "$MAX_HANDOFF_TIMEOUT_TOTAL"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_GOROUTINES' "$MAX_GOROUTINES"
require_uint 'WK_WKCLI_SIM_THREE_SMOKE_MAX_HEAP_ALLOC_BYTES' "$MAX_HEAP_ALLOC_BYTES"
[[ -z "$FAULT_CHANNEL_MIGRATION_SCAN_LIMIT" ]] || require_positive_uint '--fault-channel-migration-scan-limit' "$FAULT_CHANNEL_MIGRATION_SCAN_LIMIT"
[[ -z "$FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK" ]] || require_positive_uint '--fault-channel-migration-max-pages-per-tick' "$FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK"
[[ -z "$FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK" ]] || require_positive_uint '--fault-channel-migration-max-tasks-per-tick' "$FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK"
[[ -z "$FAULT_CHANNEL_MIGRATION_TASK_LIMIT" ]] || require_positive_uint '--fault-channel-migration-task-limit' "$FAULT_CHANNEL_MIGRATION_TASK_LIMIT"

if [[ "${#API_VALUES[@]}" -eq 0 ]]; then
  die 'at least one --api value is required'
fi
if [[ "${#GATEWAY_VALUES[@]}" -eq 0 ]]; then
  die 'at least one --gateway value is required'
fi
if [[ "${#GATEWAY_VALUES[@]}" -gt "${#API_VALUES[@]}" ]]; then
  die "--gateway must not contain more items than --api: ${#GATEWAY_VALUES[@]} > ${#API_VALUES[@]}"
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
if [[ "$FAULT_KILL_NODE" == "true" ]]; then
  case "$FAULT_NODE_ID" in
    1|2|3) ;;
    *) die "--fault-node-id must be 1, 2, or 3: $FAULT_NODE_ID" ;;
  esac
  (( FAULT_NODE_ID <= ${#API_VALUES[@]} )) || die "--fault-node-id ${FAULT_NODE_ID} exceeds configured --api node count ${#API_VALUES[@]}"
  case "$FAULT_SIGNAL" in
    TERM|KILL) ;;
    *) die "--fault-signal must be TERM or KILL: $FAULT_SIGNAL" ;;
  esac
  require_nonempty '--fault-pid-dir' "$FAULT_PID_DIR"
fi

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

mkdir -p "$OUT_DIR" "$(node_log_dir)" "$(capabilities_dir)" "$(capacity_dir)" "$(snapshot_dir)" "$(metrics_dir)"
: >"$(cluster_log)"
: >"$(sim_output)"
prepare_auto_join_state
prepare_fault_state

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
  local env_args=("WK_DEBUG_API_ENABLE=$DEBUG_API_ENABLE")
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    args+=(--clean)
  fi
  args+=(--ready-timeout "$READY_TIMEOUT" --bin "$(cluster_bin)" --log-dir "$(node_log_dir)")
  if [[ "$BUILD_CLUSTER" -eq 0 ]]; then
    args+=(--no-build)
  fi
  if [[ "$FAULT_KILL_NODE" == "true" ]]; then
    args+=(--pid-dir "$FAULT_PID_DIR" --allow-node-exit "$FAULT_NODE_ID")
    [[ -n "$FAULT_HEALTH_REPORT_INTERVAL" ]] && env_args+=("WK_CLUSTER_NODE_HEALTH_REPORT_INTERVAL=$FAULT_HEALTH_REPORT_INTERVAL")
    [[ -n "$FAULT_HEALTH_REPORT_TTL" ]] && env_args+=("WK_CLUSTER_NODE_HEALTH_REPORT_TTL=$FAULT_HEALTH_REPORT_TTL")
    [[ -n "$FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL" ]] && env_args+=("WK_CHANNEL_MIGRATION_SCAN_INTERVAL=$FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL")
    [[ -n "$FAULT_CHANNEL_MIGRATION_SCAN_LIMIT" ]] && env_args+=("WK_CHANNEL_MIGRATION_SCAN_LIMIT=$FAULT_CHANNEL_MIGRATION_SCAN_LIMIT")
    [[ -n "$FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK" ]] && env_args+=("WK_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK=$FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK")
    [[ -n "$FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK" ]] && env_args+=("WK_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK=$FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK")
    [[ -n "$FAULT_CHANNEL_MIGRATION_TASK_LIMIT" ]] && env_args+=("WK_CHANNEL_MIGRATION_TASK_LIMIT=$FAULT_CHANNEL_MIGRATION_TASK_LIMIT")
    [[ -n "$FAULT_GATEWAY_SEND_TIMEOUT" ]] && env_args+=("WK_GATEWAY_SEND_TIMEOUT=$FAULT_GATEWAY_SEND_TIMEOUT")
  fi
  log "starting three-node cluster: $START_SCRIPT"
  (
    cd "$ROOT_DIR"
    exec env "${env_args[@]}" "${args[@]}"
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
# WuKongIM v2 seed-join node config generated by smoke-wkcli-sim-wukongim-three-nodes.sh.
[node]
id = ${AUTO_JOIN_NODE_ID}
data_dir = $(toml_string "$AUTO_JOIN_DATA_DIR")

[cluster]
listen_addr = $(toml_string "$AUTO_JOIN_CLUSTER_ADDR")
advertise_addr = $(toml_string "$AUTO_JOIN_CLUSTER_ADDR")
id = $(toml_string "$AUTO_JOIN_CLUSTER_ID")
seeds = ${seed_json}
join_token = $(toml_string "$AUTO_JOIN_TOKEN")
initial_slot_count = 10
hash_slot_count = 256
slot_replica_n = 3

[api]
listen_addr = $(toml_string "$api_listen")
external_tcp_addr = $(toml_string "$AUTO_JOIN_GATEWAY_ADDR")

[bench]
api_enable = true
api_max_batch_size = 10000
api_max_payload_bytes = 10485760

[observability]
metrics_enable = true
debug_api_enable = ${DEBUG_API_ENABLE}

[prometheus]
enable = false

[top]
api_enable = true

[log]
level = "info"
dir = $(toml_string "$AUTO_JOIN_DATA_DIR/logs")
console = true
format = "console"

[gateway]
listeners = [{ name = "tcp-wkproto", network = "tcp", address = $(toml_string "$AUTO_JOIN_GATEWAY_ADDR"), transport = "gnet", protocol = "wkproto" }]
send_timeout = "5s"

[delivery]
enable = true
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

node_faulted() {
  local node="$1"
  [[ "$FAULT_KILL_NODE" == "true" ]] || return 1
  [[ "$node" == "$FAULT_NODE_ID" ]] || return 1
  [[ -f "$(fault_event_file)" ]]
}

wait_fault_survivors_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local ready=()
  local idx
  local api
  for idx in "${!API_VALUES[@]}"; do
    ready+=(0)
  done
  while (( SECONDS <= deadline )); do
    check_cluster_process
    local all_ready=1
    idx=0
    for api in "${API_VALUES[@]}"; do
      local node=$((idx + 1))
      if [[ "$node" == "$FAULT_NODE_ID" ]]; then
        idx=$((idx + 1))
        continue
      fi
      if [[ "${ready[$idx]}" -eq 1 ]]; then
        idx=$((idx + 1))
        continue
      fi
      if curl -fsS --max-time 2 "$api/readyz" >/dev/null 2>&1; then
        ready[$idx]=1
        log "fault survivor node${node} ready: $api/readyz"
      else
        all_ready=0
      fi
      idx=$((idx + 1))
    done
    if [[ "$all_ready" -eq 1 ]]; then
      return
    fi
    sleep "$POLL_INTERVAL"
  done
  die "fault survivor nodes did not become ready within ${READY_TIMEOUT}s after killing node${FAULT_NODE_ID}"
}

kill_fault_node() {
  local file
  local pid
  local deadline
  file="$(fault_pid_file)"
  deadline=$((SECONDS + READY_TIMEOUT))
  while [[ ! -f "$file" && SECONDS -le deadline ]]; do
    check_cluster_process
    sleep "$POLL_INTERVAL"
  done
  [[ -f "$file" ]] || die "fault pid file missing for node${FAULT_NODE_ID}: $file"
  pid="$(tr -d '[:space:]' <"$file")"
  [[ -n "$pid" ]] || die "fault pid file is empty for node${FAULT_NODE_ID}: $file"
  if ! kill -0 "$pid" 2>/dev/null; then
    die "fault target node${FAULT_NODE_ID} pid is not running: $pid"
  fi
  log "fault kill node${FAULT_NODE_ID}: pid=${pid} signal=${FAULT_SIGNAL}"
  kill -s "$FAULT_SIGNAL" "$pid"
  {
    printf 'node_id=%s\n' "$FAULT_NODE_ID"
    printf 'pid=%s\n' "$pid"
    printf 'signal=%s\n' "$FAULT_SIGNAL"
    printf 'killed_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'pid_file=%s\n' "$file"
  } >"$(fault_event_file)"
}

schedule_fault_kill_node() {
  if [[ "$FAULT_KILL_NODE" != "true" ]]; then
    return
  fi
  rm -f "$(fault_event_file)"
  (
    sleep "$FAULT_AFTER"
    if [[ -f "$(sim_done_file)" ]]; then
      exit 0
    fi
    kill_fault_node
    wait_fault_survivors_ready
  ) &
  FAULT_TIMER_PID="$!"
  log "fault kill node${FAULT_NODE_ID} scheduled after ${FAULT_AFTER}s"
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
  if [[ -n "$gateway" ]]; then
    grep -q "\"tcp_addr\":\"${gateway}\"" "$capacity" || die "node${node} capacity target did not publish expected gateway ${gateway}"
  fi
}

capture_target_evidence() {
  local idx=0
  local api
  for api in "${API_VALUES[@]}"; do
    local gateway=""
    if (( idx < ${#GATEWAY_VALUES[@]} )); then
      gateway="${GATEWAY_VALUES[$idx]}"
    fi
    capture_one_target_evidence "$((idx + 1))" "$api" "$gateway"
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
  if [[ -n "$FAULT_SIM_ACK_TIMEOUT" ]]; then
    cmd+=(--ack-timeout "$FAULT_SIM_ACK_TIMEOUT")
  fi
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
  schedule_fault_kill_node
  wait "$sim_pid" || status=$?
  touch "$(sim_done_file)"
  if [[ "$status" -ne 0 ]]; then
    stop_fault_timer
    capture_failure_metrics after-failure
    stop_auto_join_node
    die "wkcli sim failed with status ${status}"
  fi
  if [[ "$FAULT_KILL_NODE" == "true" ]]; then
    if [[ ! -f "$(fault_event_file)" ]]; then
      stop_fault_timer
      die "fault kill node${FAULT_NODE_ID} did not run before wkcli sim completed; increase --duration or lower --fault-after"
    fi
    local fault_status=0
    if [[ -n "$FAULT_TIMER_PID" ]]; then
      wait "$FAULT_TIMER_PID" || fault_status=$?
      FAULT_TIMER_PID=""
    fi
    if [[ "$fault_status" -ne 0 ]]; then
      die "fault kill node${FAULT_NODE_ID} failed with status ${fault_status}"
    fi
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

die_after_sim_verification_failure() {
  capture_failure_metrics after-failure
  die "$@"
}

verify_sim_output() {
  local final state sent errors
  final="$(grep '"state"' "$(sim_output)" | tail -n 1 || true)"
  [[ -n "$final" ]] || die_after_sim_verification_failure 'wkcli sim produced no status snapshots'
  state="$(printf '%s\n' "$final" | json_string state)"
  sent="$(printf '%s\n' "$final" | json_int messages_sent)"
  errors="$(printf '%s\n' "$final" | json_int send_errors)"
  [[ "$state" == "stopped" ]] || die_after_sim_verification_failure "final sim state is ${state:-<empty>}, want stopped"
  [[ -n "$sent" && "$sent" =~ ^[0-9]+$ && "$sent" -gt 0 ]] || die_after_sim_verification_failure "messages_sent=${sent:-<empty>}, want >0"
  [[ -n "${errors:-}" && "$errors" =~ ^[0-9]+$ ]] || die_after_sim_verification_failure "send_errors=${errors:-<empty>}, want integer"
  if (( errors > FAULT_MAX_SEND_ERRORS )); then
    if (( FAULT_MAX_SEND_ERRORS == 0 )); then
      die_after_sim_verification_failure "send_errors=${errors}, want 0"
    fi
    die_after_sim_verification_failure "send_errors=${errors}, want <= ${FAULT_MAX_SEND_ERRORS}"
  fi
  log "wkcli sim passed: messages_sent=${sent} send_errors=${errors}"
}

capture_snapshots() {
  local idx=0
  local api
  local snapshots_with_counts=0
  for api in "${API_VALUES[@]}"; do
    local node=$((idx + 1))
    if node_faulted "$node"; then
      idx=$((idx + 1))
      continue
    fi
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
    if [[ "$phase" == "after" ]] && node_faulted "$node"; then
      idx=$((idx + 1))
      continue
    fi
    curl -fsS "$api/metrics" >"$(metric_file "$phase" "$node")"
    idx=$((idx + 1))
  done
  log "metrics ${phase}: $(metrics_dir)"
}

capture_failure_metrics() {
  local phase="$1"
  local idx=0
  local api
  for api in "${API_VALUES[@]}"; do
    local node=$((idx + 1))
    local output
    if node_faulted "$node"; then
      idx=$((idx + 1))
      continue
    fi
    output="$(metric_file "$phase" "$node")"
    if ! curl -fsS --max-time 2 "$api/metrics" >"$output"; then
      rm -f "$output"
      log "failure metrics node${node} unavailable: $api/metrics"
    fi
    idx=$((idx + 1))
  done
  log "failure metrics ${phase}: $(metrics_dir)"
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
    if node_faulted "$node"; then
      idx=$((idx + 1))
      continue
    fi
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
    printf '%s\n' "- fault_kill_node: ${FAULT_KILL_NODE}"
    printf '%s\n' "- fault_node_id: ${FAULT_NODE_ID}"
    printf '%s\n' "- fault_after_secs: ${FAULT_AFTER}"
    printf '%s\n' "- fault_signal: ${FAULT_SIGNAL}"
    printf '%s\n' "- fault_pid_dir: ${FAULT_PID_DIR}"
    printf '%s\n' "- fault_health_report_interval: ${FAULT_HEALTH_REPORT_INTERVAL:-<default>}"
    printf '%s\n' "- fault_health_report_ttl: ${FAULT_HEALTH_REPORT_TTL:-<default>}"
    printf '%s\n' "- fault_channel_migration_scan_interval: ${FAULT_CHANNEL_MIGRATION_SCAN_INTERVAL:-<default>}"
    printf '%s\n' "- fault_channel_migration_scan_limit: ${FAULT_CHANNEL_MIGRATION_SCAN_LIMIT:-<default>}"
    printf '%s\n' "- fault_channel_migration_max_pages_per_tick: ${FAULT_CHANNEL_MIGRATION_MAX_PAGES_PER_TICK:-<default>}"
    printf '%s\n' "- fault_channel_migration_max_tasks_per_tick: ${FAULT_CHANNEL_MIGRATION_MAX_TASKS_PER_TICK:-<default>}"
    printf '%s\n' "- fault_channel_migration_task_limit: ${FAULT_CHANNEL_MIGRATION_TASK_LIMIT:-<default>}"
    printf '%s\n' "- fault_gateway_send_timeout: ${FAULT_GATEWAY_SEND_TIMEOUT:-<default>}"
    printf '%s\n' "- fault_sim_ack_timeout: ${FAULT_SIM_ACK_TIMEOUT:-<default>}"
    printf '%s\n' "- fault_max_send_errors: ${FAULT_MAX_SEND_ERRORS}"
    if [[ "$FAULT_KILL_NODE" == "true" ]]; then
      printf '%s\n' "- fault_event_file: $(basename "$(fault_event_file)")"
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
