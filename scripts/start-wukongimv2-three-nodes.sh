#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_DIR="$ROOT_DIR/scripts/wukongimv2"
BIN_PATH="${WK_WUKONGIMV2_THREE_NODES_BIN:-$ROOT_DIR/data/wukongimv2-three-nodes/wukongimv2}"
LOG_DIR="${WK_WUKONGIMV2_THREE_NODES_LOG_DIR:-$ROOT_DIR/data/wukongimv2-three-node-logs}"
READY_TIMEOUT="${WK_WUKONGIMV2_THREE_NODES_READY_TIMEOUT:-60}"
POLL_INTERVAL="${WK_WUKONGIMV2_THREE_NODES_POLL_INTERVAL:-1}"
BUILD=1
CLEAN=0
DRY_RUN=0
EXIT_AFTER_READY=0
NODES=(1 2 3)
READY_URLS=(
  "http://127.0.0.1:5011/readyz"
  "http://127.0.0.1:5012/readyz"
  "http://127.0.0.1:5013/readyz"
)
PIDS=()

usage() {
  cat <<'USAGE'
Usage: scripts/start-wukongimv2-three-nodes.sh [options]

Builds cmd/wukongimv2 once, starts the three static local nodes, waits for
all /readyz endpoints, and keeps the cluster in the foreground until Ctrl+C.

Options:
  --clean                Remove the node data directories and log dir before start.
  --no-build             Reuse --bin instead of running go build.
  --bin PATH             Binary path. Default: WK_WUKONGIMV2_THREE_NODES_BIN or data/wukongimv2-three-nodes/wukongimv2.
  --log-dir DIR          Per-node log directory. Default: WK_WUKONGIMV2_THREE_NODES_LOG_DIR or data/wukongimv2-three-node-logs.
  --ready-timeout SECS   Ready wait timeout. Default: WK_WUKONGIMV2_THREE_NODES_READY_TIMEOUT or 60.
  --poll SECS            Ready polling interval. Default: WK_WUKONGIMV2_THREE_NODES_POLL_INTERVAL or 1.
  --dry-run              Print resolved commands without starting nodes.
  --exit-after-ready     Stop nodes and exit after readiness passes. Useful for smoke tests.
  -h, --help             Show this help.
USAGE
}

log() {
  printf '[wukongimv2-three] %s\n' "$*"
}

die() {
  printf '[wukongimv2-three] ERROR: %s\n' "$*" >&2
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

config_path() {
  local node="$1"
  printf '%s/wukongimv2-node%s.conf' "$CONFIG_DIR" "$node"
}

log_path() {
  local node="$1"
  printf '%s/node%s.log' "$LOG_DIR" "$node"
}

data_path() {
  local node="$1"
  printf '%s/data/wukongimv2-node-%s' "$ROOT_DIR" "$node"
}

print_plan() {
  printf 'repo_root=%s\n' "$ROOT_DIR"
  if [[ "$BUILD" -eq 1 ]]; then
    printf 'build_cmd=go build -o %s ./cmd/wukongimv2\n' "$BIN_PATH"
  else
    printf 'build_cmd=<disabled>\n'
  fi
  printf 'bin=%s\n' "$BIN_PATH"
  printf 'log_dir=%s\n' "$LOG_DIR"
  for i in "${!NODES[@]}"; do
    local node="${NODES[$i]}"
    printf 'node%s_config=%s\n' "$node" "$(config_path "$node")"
    printf 'node%s_log=%s\n' "$node" "$(log_path "$node")"
    printf 'node%s_ready=%s\n' "$node" "${READY_URLS[$i]}"
    printf 'node%s_cmd=%s -config %s\n' "$node" "$BIN_PATH" "$(config_path "$node")"
  done
}

tail_logs() {
  for node in "${NODES[@]}"; do
    local path
    path="$(log_path "$node")"
    if [[ -f "$path" ]]; then
      printf '\n--- node%s log: %s ---\n' "$node" "$path" >&2
      tail -n 80 "$path" >&2 || true
    fi
  done
}

stop_nodes() {
  if [[ "${#PIDS[@]}" -eq 0 ]]; then
    return
  fi
  log 'stopping nodes'
  for pid in "${PIDS[@]}"; do
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  for pid in "${PIDS[@]}"; do
    wait "$pid" 2>/dev/null || true
  done
  PIDS=()
}

cleanup() {
  stop_nodes
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

while [[ $# -gt 0 ]]; do
  case "$1" in
    --clean)
      CLEAN=1
      shift
      ;;
    --no-build)
      BUILD=0
      shift
      ;;
    --bin)
      [[ $# -ge 2 ]] || die '--bin requires a value'
      BIN_PATH="$2"
      shift 2
      ;;
    --log-dir)
      [[ $# -ge 2 ]] || die '--log-dir requires a value'
      LOG_DIR="$2"
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
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    --exit-after-ready)
      EXIT_AFTER_READY=1
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
require_uint '--poll' "$POLL_INTERVAL"

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

for node in "${NODES[@]}"; do
  [[ -f "$(config_path "$node")" ]] || die "missing config: $(config_path "$node")"
done

if [[ "$CLEAN" -eq 1 ]]; then
  log 'cleaning node data and logs'
  for node in "${NODES[@]}"; do
    rm -rf "$(data_path "$node")"
  done
  rm -rf "$LOG_DIR"
fi

mkdir -p "$(dirname "$BIN_PATH")" "$LOG_DIR"

if [[ "$BUILD" -eq 1 ]]; then
  log "building $BIN_PATH"
  (
    cd "$ROOT_DIR"
    go build -o "$BIN_PATH" ./cmd/wukongimv2
  )
elif [[ ! -x "$BIN_PATH" ]]; then
  die "--no-build requested but binary is not executable: $BIN_PATH"
fi

check_processes() {
  for i in "${!PIDS[@]}"; do
    local pid="${PIDS[$i]}"
    local node="${NODES[$i]}"
    if ! kill -0 "$pid" 2>/dev/null; then
      local status=0
      wait "$pid" 2>/dev/null || status=$?
      tail_logs
      die "node${node} exited early with status ${status}"
    fi
  done
}

start_node() {
  local node="$1"
  local config
  local log_file
  config="$(config_path "$node")"
  log_file="$(log_path "$node")"
  : > "$log_file"
  log "starting node${node}: $config"
  "$BIN_PATH" -config "$config" >"$log_file" 2>&1 &
  local pid="$!"
  PIDS+=("$pid")
  log "node${node} pid=${pid} log=$log_file"
}

wait_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local ready=()
  for _ in "${NODES[@]}"; do
    ready+=(0)
  done
  while (( SECONDS <= deadline )); do
    check_processes
    local all_ready=1
    for i in "${!NODES[@]}"; do
      local node="${NODES[$i]}"
      local url="${READY_URLS[$i]}"
      if [[ "${ready[$i]}" -eq 1 ]]; then
        continue
      fi
      if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
        ready[$i]=1
        log "node${node} ready: $url"
      else
        all_ready=0
      fi
    done
    if [[ "$all_ready" -eq 1 ]]; then
      log 'all nodes ready'
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  tail_logs
  die "timed out waiting for all nodes to become ready"
}

monitor_nodes() {
  log 'cluster is running; press Ctrl+C to stop'
  while true; do
    check_processes
    sleep 1
  done
}

cd "$ROOT_DIR"
for node in "${NODES[@]}"; do
  start_node "$node"
done
wait_ready
if [[ "$EXIT_AFTER_READY" -eq 1 ]]; then
  exit 0
fi
monitor_nodes
