#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CONFIG_PATH="${WK_WUKONGIM_SINGLE_NODE_CONFIG:-$ROOT_DIR/scripts/wukongim/wukongim.toml}"
BIN_PATH="${WK_WUKONGIM_SINGLE_NODE_BIN:-$ROOT_DIR/data/wukongim-single-node/wukongim}"
LOG_DIR="${WK_WUKONGIM_SINGLE_NODE_LOG_DIR:-$ROOT_DIR/data/wukongim-single-node-logs}"
READY_URL="${WK_WUKONGIM_SINGLE_NODE_READY_URL:-http://127.0.0.1:5001/readyz}"
DATA_DIR="${WK_WUKONGIM_SINGLE_NODE_DATA_DIR:-$ROOT_DIR/data/wukongim-single-node-data}"
READY_TIMEOUT="${WK_WUKONGIM_SINGLE_NODE_READY_TIMEOUT:-60}"
POLL_INTERVAL="${WK_WUKONGIM_SINGLE_NODE_POLL_INTERVAL:-1}"
PROMETHEUS_SOURCE_REF="${WK_PROMETHEUS_SOURCE_REF:-${WK_PROMETHEUS_EMBED_VERSION:-v3.12.0}}"
PROMETHEUS_REPO="${WK_PROMETHEUS_REPO:-https://github.com/prometheus/prometheus.git}"
PROMETHEUS_EMBED_DIR="${WK_PROMETHEUS_EMBED_DIR:-$ROOT_DIR/internal/app/prometheus_embedded}"
BUILD=1
CLEAN=0
DRY_RUN=0
EXIT_AFTER_READY=0
PID=""

usage() {
  cat <<'USAGE'
Usage: scripts/start-wukongim-single-node.sh [options]

Builds cmd/wukongim, starts the single-node cluster config, waits for
/readyz, and keeps the node in the foreground until Ctrl+C.

Prometheus is enabled by default for this helper script. Set
WK_PROMETHEUS_ENABLE=false to keep the node metrics endpoint without starting
the app-managed Prometheus process. When enabled and WK_PROMETHEUS_BINARY_PATH
is unset, the script builds a Prometheus binary into the wukongim embed
assets before compiling wukongim, so the final wukongim executable carries
Prometheus with it. Set WK_PROMETHEUS_SOURCE_REF to choose the Prometheus Git
ref used for that embedded build.

Options:
  --clean                Remove the node data directory and log dir before start.
  --no-build             Reuse --bin instead of running go build.
  --bin PATH             Binary path. Default: WK_WUKONGIM_SINGLE_NODE_BIN or data/wukongim-single-node/wukongim.
  --config PATH          Config path. Default: WK_WUKONGIM_SINGLE_NODE_CONFIG or scripts/wukongim/wukongim.toml.
  --data-dir DIR         Data dir removed by --clean. Default: WK_WUKONGIM_SINGLE_NODE_DATA_DIR or data/wukongim-single-node-data.
  --log-dir DIR          Log directory. Default: WK_WUKONGIM_SINGLE_NODE_LOG_DIR or data/wukongim-single-node-logs.
  --ready-url URL        Ready probe URL. Default: WK_WUKONGIM_SINGLE_NODE_READY_URL or http://127.0.0.1:5001/readyz.
  --ready-timeout SECS   Ready wait timeout. Default: WK_WUKONGIM_SINGLE_NODE_READY_TIMEOUT or 60.
  --poll SECS            Ready polling interval. Default: WK_WUKONGIM_SINGLE_NODE_POLL_INTERVAL or 1.
  --dry-run              Print resolved commands without starting the node.
  --exit-after-ready     Stop the node and exit after readiness passes. Useful for smoke tests.
  -h, --help             Show this help.
USAGE
}

log() {
  printf '[wukongim-single] %s\n' "$*"
}

die() {
  printf '[wukongim-single] ERROR: %s\n' "$*" >&2
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
    printf 'build_cmd=go build -o %s ./cmd/wukongim\n' "$BIN_PATH"
  else
    printf 'build_cmd=<disabled>\n'
  fi
  printf 'bin=%s\n' "$BIN_PATH"
  printf 'config=%s\n' "$CONFIG_PATH"
  printf 'data_dir=%s\n' "$DATA_DIR"
  printf 'log_dir=%s\n' "$LOG_DIR"
  printf 'log=%s\n' "$(log_path)"
  printf 'ready=%s\n' "$READY_URL"
  printf 'prometheus_enable=%s\n' "$WK_PROMETHEUS_ENABLE"
  if [[ -n "${WK_PROMETHEUS_BINARY_PATH-}" ]]; then
    printf 'prometheus_binary_path=%s\n' "$WK_PROMETHEUS_BINARY_PATH"
  else
    printf 'prometheus_binary_path=<embedded>\n'
  fi
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

WK_PROMETHEUS_ENABLE="${WK_PROMETHEUS_ENABLE:-true}"
export WK_PROMETHEUS_ENABLE
WK_PROMETHEUS_EXTERNAL_BINARY=0
if [[ -n "${WK_PROMETHEUS_BINARY_PATH-}" ]]; then
  WK_PROMETHEUS_EXTERNAL_BINARY=1
fi
if [[ "$WK_PROMETHEUS_ENABLE" == "true" && "$WK_PROMETHEUS_EXTERNAL_BINARY" -eq 0 ]]; then
  WK_PROMETHEUS_BINARY_PATH=""
  export WK_PROMETHEUS_BINARY_PATH
fi

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

ensure_embedded_prometheus() {
  local goos goarch suffix embed_name embed_path tmp src_dir gobin_path
  goos="${GOOS:-$(go env GOOS)}"
  goarch="${GOARCH:-$(go env GOARCH)}"
  suffix=""
  if [[ "$goos" == "windows" ]]; then
    suffix=".exe"
  fi
  embed_name="prometheus-${goos}-${goarch}${suffix}"
  embed_path="$PROMETHEUS_EMBED_DIR/$embed_name"
  if [[ -x "$embed_path" ]]; then
    log "using embedded prometheus asset: $embed_path"
    return
  fi
  command -v git >/dev/null 2>&1 || die "git is required to build embedded prometheus from source"
  log "building embedded prometheus $PROMETHEUS_SOURCE_REF for ${goos}/${goarch}"
  mkdir -p "$PROMETHEUS_EMBED_DIR"
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-prometheus.XXXXXX")"
  trap 'rm -rf "$tmp"; cleanup' EXIT
  gobin_path="$tmp/prometheus${suffix}"
  src_dir="$tmp/prometheus-src"
  git clone --depth 1 --branch "$PROMETHEUS_SOURCE_REF" "$PROMETHEUS_REPO" "$src_dir"
  (
    cd "$src_dir"
    GOOS="$goos" GOARCH="$goarch" go build -o "$gobin_path" ./cmd/prometheus
  )
  [[ -x "$gobin_path" ]] || die "prometheus build did not produce executable: $gobin_path"
  cp "$gobin_path" "$embed_path"
  chmod 0755 "$embed_path"
  rm -rf "$tmp"
  trap cleanup EXIT
}

if [[ "$BUILD" -eq 1 ]]; then
  if [[ "$WK_PROMETHEUS_ENABLE" == "true" && "$WK_PROMETHEUS_EXTERNAL_BINARY" -eq 0 ]]; then
    ensure_embedded_prometheus
  fi
  log "building $BIN_PATH"
  (
    cd "$ROOT_DIR"
    go build -o "$BIN_PATH" ./cmd/wukongim
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
