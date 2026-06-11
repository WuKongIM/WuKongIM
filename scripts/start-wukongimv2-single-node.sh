#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONFIG_PATH="${WK_WUKONGIMV2_SINGLE_NODE_CONFIG:-$ROOT_DIR/scripts/wukongimv2/wukongimv2.conf}"
BIN_PATH="${WK_WUKONGIMV2_SINGLE_NODE_BIN:-$ROOT_DIR/data/wukongimv2-single-node/wukongimv2}"
LOG_DIR="${WK_WUKONGIMV2_SINGLE_NODE_LOG_DIR:-$ROOT_DIR/data/wukongimv2-single-node-logs}"
READY_URL="${WK_WUKONGIMV2_SINGLE_NODE_READY_URL:-http://127.0.0.1:5001/readyz}"
DATA_DIR="${WK_WUKONGIMV2_SINGLE_NODE_DATA_DIR:-$ROOT_DIR/data/wukongimv2-single-node-data}"
READY_TIMEOUT="${WK_WUKONGIMV2_SINGLE_NODE_READY_TIMEOUT:-60}"
POLL_INTERVAL="${WK_WUKONGIMV2_SINGLE_NODE_POLL_INTERVAL:-1}"
BUILD=1
CLEAN=0
DRY_RUN=0
EXIT_AFTER_READY=0
PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/start-wukongimv2-single-node.sh [options]

Builds cmd/wukongimv2, starts the single-node cluster config, waits for
/readyz, and keeps the node in the foreground until Ctrl+C.

Options:
  --clean                Remove the node data directory and log dir before start.
  --no-build             Reuse --bin instead of running go build.
  --bin PATH             Binary path. Default: WK_WUKONGIMV2_SINGLE_NODE_BIN or data/wukongimv2-single-node/wukongimv2.
  --config PATH          Config path. Default: WK_WUKONGIMV2_SINGLE_NODE_CONFIG or scripts/wukongimv2/wukongimv2.conf.
  --data-dir DIR         Data dir removed by --clean. Default: WK_WUKONGIMV2_SINGLE_NODE_DATA_DIR or data/wukongimv2-single-node-data.
  --log-dir DIR          Log directory. Default: WK_WUKONGIMV2_SINGLE_NODE_LOG_DIR or data/wukongimv2-single-node-logs.
  --ready-url URL        Ready probe URL. Default: WK_WUKONGIMV2_SINGLE_NODE_READY_URL or http://127.0.0.1:5001/readyz.
  --ready-timeout SECS   Ready wait timeout. Default: WK_WUKONGIMV2_SINGLE_NODE_READY_TIMEOUT or 60.
  --poll SECS            Ready polling interval. Default: WK_WUKONGIMV2_SINGLE_NODE_POLL_INTERVAL or 1.
  --dry-run              Print resolved commands without starting the node.
  --exit-after-ready     Stop the node and exit after readiness passes. Useful for smoke tests.
  -h, --help             Show this help.
USAGE
}

log() {
  printf '[wukongimv2-single] %s\n' "$*"
}

die() {
  printf '[wukongimv2-single] ERROR: %s\n' "$*" >&2
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

log_path() {
  printf '%s/node1.log' "$LOG_DIR"
}

print_plan() {
  printf 'repo_root=%s\n' "$ROOT_DIR"
  if [[ "$BUILD" -eq 1 ]]; then
    printf 'build_cmd=go build -o %s ./cmd/wukongimv2\n' "$BIN_PATH"
  else
    printf 'build_cmd=<disabled>\n'
  fi
  printf 'bin=%s\n' "$BIN_PATH"
  printf 'config=%s\n' "$CONFIG_PATH"
  printf 'data_dir=%s\n' "$DATA_DIR"
  printf 'log_dir=%s\n' "$LOG_DIR"
  printf 'log=%s\n' "$(log_path)"
  printf 'ready=%s\n' "$READY_URL"
  printf 'cmd=%s -config %s\n' "$BIN_PATH" "$CONFIG_PATH"
}

tail_log() {
  local path
  path="$(log_path)"
  if [[ -f "$path" ]]; then
    printf '\n--- node log: %s ---\n' "$path" >&2
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
    --config)
      [[ $# -ge 2 ]] || die '--config requires a value'
      CONFIG_PATH="$2"
      shift 2
      ;;
    --data-dir)
      [[ $# -ge 2 ]] || die '--data-dir requires a value'
      DATA_DIR="$2"
      shift 2
      ;;
    --log-dir)
      [[ $# -ge 2 ]] || die '--log-dir requires a value'
      LOG_DIR="$2"
      shift 2
      ;;
    --ready-url)
      [[ $# -ge 2 ]] || die '--ready-url requires a value'
      READY_URL="$2"
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

[[ -f "$CONFIG_PATH" ]] || die "missing config: $CONFIG_PATH"

if [[ "$CLEAN" -eq 1 ]]; then
  log 'cleaning node data and logs'
  rm -rf "$DATA_DIR" "$LOG_DIR"
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

check_process() {
  if [[ -z "$PID" ]]; then
    return
  fi
  if ! kill -0 "$PID" 2>/dev/null; then
    local status=0
    wait "$PID" 2>/dev/null || status=$?
    PID=""
    tail_log
    die "node exited early with status ${status}"
  fi
}

start_node() {
  local log_file
  log_file="$(log_path)"
  : > "$log_file"
  log "starting node: $CONFIG_PATH"
  "$BIN_PATH" -config "$CONFIG_PATH" >"$log_file" 2>&1 &
  PID="$!"
  log "node pid=${PID} log=$log_file"
}

wait_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  while (( SECONDS <= deadline )); do
    check_process
    if curl -fsS --max-time 2 "$READY_URL" >/dev/null 2>&1; then
      log "node ready: $READY_URL"
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  tail_log
  die "timed out waiting for node to become ready"
}

monitor_node() {
  log 'single-node cluster is running; press Ctrl+C to stop'
  while true; do
    check_process
    sleep 1
  done
}

cd "$ROOT_DIR"
start_node
wait_ready
if [[ "$EXIT_AFTER_READY" -eq 1 ]]; then
  exit 0
fi
monitor_node
