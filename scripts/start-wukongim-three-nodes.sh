#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

CONFIG_DIR="$ROOT_DIR/scripts/wukongim"
BIN_PATH="${WK_WUKONGIM_THREE_NODES_BIN:-$ROOT_DIR/data/wukongim-three-nodes/wukongim}"
LOG_DIR="${WK_WUKONGIM_THREE_NODES_LOG_DIR:-$ROOT_DIR/data/wukongim-three-node-logs}"
READY_TIMEOUT="${WK_WUKONGIM_THREE_NODES_READY_TIMEOUT:-60}"
POLL_INTERVAL="${WK_WUKONGIM_THREE_NODES_POLL_INTERVAL:-1}"
PROMETHEUS_ENABLE="${WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE:-${WK_PROMETHEUS_ENABLE:-true}}"
PROMETHEUS_LISTEN_ADDR="${WK_WUKONGIM_THREE_NODES_PROMETHEUS_LISTEN_ADDR:-${WK_PROMETHEUS_LISTEN_ADDR:-127.0.0.1:9091}}"
PROMETHEUS_DATA_DIR="${WK_WUKONGIM_THREE_NODES_PROMETHEUS_DATA_DIR:-$ROOT_DIR/data/wukongim-three-nodes/prometheus}"
PROMETHEUS_RETENTION_TIME="${WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_TIME:-360h}"
PROMETHEUS_RETENTION_SIZE="${WK_WUKONGIM_THREE_NODES_PROMETHEUS_RETENTION_SIZE:-}"
PROMETHEUS_SCRAPE_INTERVAL="${WK_WUKONGIM_THREE_NODES_PROMETHEUS_SCRAPE_INTERVAL:-15s}"
PROMETHEUS_SOURCE_REF="${WK_PROMETHEUS_SOURCE_REF:-${WK_PROMETHEUS_EMBED_VERSION:-v3.12.0}}"
PROMETHEUS_REPO="${WK_PROMETHEUS_REPO:-https://github.com/prometheus/prometheus.git}"
PROMETHEUS_EMBED_DIR="${WK_PROMETHEUS_EMBED_DIR:-$ROOT_DIR/internal/app/prometheus_embedded}"
PID_DIR="${WK_WUKONGIM_THREE_NODES_PID_DIR:-}"
ALLOW_NODE_EXIT="${WK_WUKONGIM_THREE_NODES_ALLOW_NODE_EXIT:-}"
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
METRICS_TARGETS=(
  "127.0.0.1:5011"
  "127.0.0.1:5012"
  "127.0.0.1:5013"
)
PIDS=()
ALLOW_NODE_EXIT_VALUES=()

usage() {
  cat <<'USAGE'
Usage: scripts/start-wukongim-three-nodes.sh [options]

Builds cmd/wukongim once, starts the three static local nodes, waits for
all /readyz endpoints, and keeps the cluster in the foreground until Ctrl+C.

Prometheus is enabled by default for this helper script. Node1 starts the
app-managed Prometheus process and scrapes all three local node /metrics
endpoints. Set --no-prometheus or WK_WUKONGIM_THREE_NODES_PROMETHEUS_ENABLE=false
to keep only the node metrics endpoints.

Options:
  --clean                Remove the node data directories and log dir before start.
  --no-build             Reuse --bin instead of running go build.
  --bin PATH             Binary path. Default: WK_WUKONGIM_THREE_NODES_BIN or data/wukongim-three-nodes/wukongim.
  --log-dir DIR          Per-node log directory. Default: WK_WUKONGIM_THREE_NODES_LOG_DIR or data/wukongim-three-node-logs.
  --ready-timeout SECS   Ready wait timeout. Default: WK_WUKONGIM_THREE_NODES_READY_TIMEOUT or 60.
  --poll SECS            Ready polling interval. Default: WK_WUKONGIM_THREE_NODES_POLL_INTERVAL or 1.
  --no-prometheus        Do not start the node1 app-managed Prometheus process.
  --prometheus-listen-addr ADDR
                         Prometheus web listen address. Default: 127.0.0.1:9091.
  --prometheus-data-dir DIR
                         Prometheus data dir. Default: data/wukongim-three-nodes/prometheus.
  --prometheus-scrape-interval DURATION
                         Prometheus scrape interval. Default: 15s.
  --pid-dir DIR          Write node PID files as node1.pid, node2.pid, node3.pid.
  --allow-node-exit LIST Comma-separated node IDs allowed to exit without failing this supervisor.
  --dry-run              Print resolved commands without starting nodes.
  --exit-after-ready     Stop nodes and exit after readiness passes. Useful for smoke tests.
  -h, --help             Show this help.
USAGE
}

log() {
  printf '[wukongim-three] %s\n' "$*"
}

die() {
  printf '[wukongim-three] ERROR: %s\n' "$*" >&2
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

parse_allow_node_exit() {
  local item
  local values=()
  ALLOW_NODE_EXIT_VALUES=()
  if [[ -z "$ALLOW_NODE_EXIT" ]]; then
    return 0
  fi
  IFS=',' read -ra values <<<"$ALLOW_NODE_EXIT"
  for item in "${values[@]}"; do
    item="${item//[[:space:]]/}"
    [[ -n "$item" ]] || die "allow-node-exit contains an empty item: $ALLOW_NODE_EXIT"
    case "$item" in
      1|2|3) ;;
      *) die "allow-node-exit only supports node IDs 1, 2, or 3: $item" ;;
    esac
    ALLOW_NODE_EXIT_VALUES+=("$item")
  done
}

node_exit_allowed() {
  local node="$1"
  local allowed
  for allowed in "${ALLOW_NODE_EXIT_VALUES[@]}"; do
    [[ "$allowed" == "$node" ]] && return 0
  done
  return 1
}

pid_file() {
  local node="$1"
  printf '%s/node%s.pid' "$PID_DIR" "$node"
}

config_path() {
  local node="$1"
  printf '%s/wukongim-node%s.toml' "$CONFIG_DIR" "$node"
}

log_path() {
  local node="$1"
  printf '%s/node%s.log' "$LOG_DIR" "$node"
}

data_path() {
  local node="$1"
  printf '%s/data/wukongim-node-%s' "$ROOT_DIR" "$node"
}

prometheus_scrape_targets_json() {
  local out="["
  local sep=""
  for target in "${METRICS_TARGETS[@]}"; do
    out+="${sep}\"${target}\""
    sep=","
  done
  out+="]"
  printf '%s' "$out"
}

prometheus_ready_url() {
  printf 'http://%s/-/ready' "$PROMETHEUS_LISTEN_ADDR"
}

prometheus_node_env_preview() {
  local node="$1"
  if [[ "$PROMETHEUS_ENABLE" != "true" ]]; then
    printf 'WK_PROMETHEUS_ENABLE=false'
    return
  fi
  if [[ "$node" == "1" ]]; then
    printf 'WK_METRICS_ENABLE=true WK_PROMETHEUS_ENABLE=true WK_PROMETHEUS_LISTEN_ADDR=%s WK_PROMETHEUS_SCRAPE_TARGETS=%s' \
      "$PROMETHEUS_LISTEN_ADDR" "$(prometheus_scrape_targets_json)"
    return
  fi
  printf 'WK_METRICS_ENABLE=true WK_PROMETHEUS_ENABLE=false'
}

print_plan() {
  printf 'repo_root=%s\n' "$ROOT_DIR"
  if [[ "$BUILD" -eq 1 ]]; then
    printf 'build_cmd=go build -o %s ./cmd/wukongim\n' "$BIN_PATH"
  else
    printf 'build_cmd=<disabled>\n'
  fi
  printf 'bin=%s\n' "$BIN_PATH"
  printf 'log_dir=%s\n' "$LOG_DIR"
  if [[ -n "$PID_DIR" ]]; then
    printf 'pid_dir=%s\n' "$PID_DIR"
    printf 'allow_node_exit=%s\n' "${ALLOW_NODE_EXIT:-<none>}"
  else
    printf 'pid_dir=<disabled>\n'
    printf 'allow_node_exit=%s\n' "${ALLOW_NODE_EXIT:-<none>}"
  fi
  printf 'prometheus_enable=%s\n' "$PROMETHEUS_ENABLE"
  if [[ "$PROMETHEUS_ENABLE" == "true" ]]; then
    printf 'prometheus_listen_addr=%s\n' "$PROMETHEUS_LISTEN_ADDR"
    printf 'prometheus_data_dir=%s\n' "$PROMETHEUS_DATA_DIR"
    printf 'prometheus_scrape_interval=%s\n' "$PROMETHEUS_SCRAPE_INTERVAL"
    printf 'prometheus_scrape_targets=%s\n' "$(prometheus_scrape_targets_json)"
    printf 'prometheus_ready=%s\n' "$(prometheus_ready_url)"
  fi
  for i in "${!NODES[@]}"; do
    local node="${NODES[$i]}"
    printf 'node%s_config=%s\n' "$node" "$(config_path "$node")"
    printf 'node%s_log=%s\n' "$node" "$(log_path "$node")"
    if [[ -n "$PID_DIR" ]]; then
      printf 'node%s_pid_file=%s\n' "$node" "$(pid_file "$node")"
    fi
    printf 'node%s_ready=%s\n' "$node" "${READY_URLS[$i]}"
    printf 'node%s_env=%s\n' "$node" "$(prometheus_node_env_preview "$node")"
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
    [[ -n "$pid" ]] || continue
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
    fi
  done
  for pid in "${PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
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
    --no-prometheus)
      PROMETHEUS_ENABLE=false
      shift
      ;;
    --prometheus-listen-addr)
      [[ $# -ge 2 ]] || die '--prometheus-listen-addr requires a value'
      PROMETHEUS_LISTEN_ADDR="$2"
      shift 2
      ;;
    --prometheus-data-dir)
      [[ $# -ge 2 ]] || die '--prometheus-data-dir requires a value'
      PROMETHEUS_DATA_DIR="$2"
      shift 2
      ;;
    --prometheus-scrape-interval)
      [[ $# -ge 2 ]] || die '--prometheus-scrape-interval requires a value'
      PROMETHEUS_SCRAPE_INTERVAL="$2"
      shift 2
      ;;
    --pid-dir)
      [[ $# -ge 2 ]] || die '--pid-dir requires a value'
      PID_DIR="$2"
      shift 2
      ;;
    --allow-node-exit)
      [[ $# -ge 2 ]] || die '--allow-node-exit requires a value'
      ALLOW_NODE_EXIT="$2"
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
require_bool 'prometheus enable' "$PROMETHEUS_ENABLE"
parse_allow_node_exit

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
  rm -rf "$LOG_DIR" "$PROMETHEUS_DATA_DIR"
fi

mkdir -p "$(dirname "$BIN_PATH")" "$LOG_DIR"
if [[ -n "$PID_DIR" ]]; then
  mkdir -p "$PID_DIR"
  rm -f "$PID_DIR"/node*.pid
fi

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
  command -v git >/dev/null 2>&1 || die "git is required to build embedded prometheus from source; set WK_PROMETHEUS_BINARY_PATH or use --no-prometheus"
  log "building embedded prometheus $PROMETHEUS_SOURCE_REF for ${goos}/${goarch}"
  mkdir -p "$PROMETHEUS_EMBED_DIR"
  tmp="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-prometheus.XXXXXX")"
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
}

if [[ "$BUILD" -eq 1 ]]; then
  if [[ "$PROMETHEUS_ENABLE" == "true" && -z "${WK_PROMETHEUS_BINARY_PATH-}" ]]; then
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

check_processes() {
  for i in "${!PIDS[@]}"; do
    local pid="${PIDS[$i]}"
    local node="${NODES[$i]}"
    [[ -n "$pid" ]] || continue
    if ! kill -0 "$pid" 2>/dev/null; then
      local status=0
      wait "$pid" 2>/dev/null || status=$?
      if node_exit_allowed "$node"; then
        log "node${node} exited as allowed with status ${status}"
        PIDS[$i]=""
        continue
      fi
      tail_logs
      die "node${node} exited early with status ${status}"
    fi
  done
}

start_node() {
  local node="$1"
  local config
  local log_file
  local env_args=()
  config="$(config_path "$node")"
  log_file="$(log_path "$node")"
  : > "$log_file"
  log "starting node${node}: $config"
  if [[ "$PROMETHEUS_ENABLE" == "true" ]]; then
    env_args+=("WK_METRICS_ENABLE=true")
    if [[ "$node" == "1" ]]; then
      env_args+=(
        "WK_PROMETHEUS_ENABLE=true"
        "WK_PROMETHEUS_LISTEN_ADDR=$PROMETHEUS_LISTEN_ADDR"
        "WK_PROMETHEUS_DATA_DIR=$PROMETHEUS_DATA_DIR"
        "WK_PROMETHEUS_RETENTION_TIME=$PROMETHEUS_RETENTION_TIME"
        "WK_PROMETHEUS_SCRAPE_INTERVAL=$PROMETHEUS_SCRAPE_INTERVAL"
        "WK_PROMETHEUS_SCRAPE_TARGETS=$(prometheus_scrape_targets_json)"
      )
      if [[ -n "$PROMETHEUS_RETENTION_SIZE" ]]; then
        env_args+=("WK_PROMETHEUS_RETENTION_SIZE=$PROMETHEUS_RETENTION_SIZE")
      fi
      if [[ -n "${WK_PROMETHEUS_BINARY_PATH-}" ]]; then
        env_args+=("WK_PROMETHEUS_BINARY_PATH=$WK_PROMETHEUS_BINARY_PATH")
      fi
    else
      env_args+=("WK_PROMETHEUS_ENABLE=false")
    fi
  else
    env_args+=("WK_PROMETHEUS_ENABLE=false")
  fi
  env "${env_args[@]}" "$BIN_PATH" -config "$config" >"$log_file" 2>&1 &
  local pid="$!"
  PIDS+=("$pid")
  if [[ -n "$PID_DIR" ]]; then
    printf '%s\n' "$pid" >"$(pid_file "$node")"
  fi
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

wait_prometheus_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local url
  url="$(prometheus_ready_url)"
  while (( SECONDS <= deadline )); do
    check_processes
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      log "prometheus ready: $url"
      return 0
    fi
    sleep "$POLL_INTERVAL"
  done
  tail_logs
  die "timed out waiting for prometheus to become ready: $url"
}

monitor_nodes() {
  if [[ "$PROMETHEUS_ENABLE" == "true" ]]; then
    log "cluster is running; Prometheus: http://$PROMETHEUS_LISTEN_ADDR; press Ctrl+C to stop"
  else
    log 'cluster is running; press Ctrl+C to stop'
  fi
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
if [[ "$PROMETHEUS_ENABLE" == "true" ]]; then
  wait_prometheus_ready
fi
if [[ "$EXIT_AFTER_READY" -eq 1 ]]; then
  exit 0
fi
monitor_nodes
