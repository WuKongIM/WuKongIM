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
API_VALUES=()
GATEWAY_VALUES=()

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
  print_start_cmd
  print_sim_cmd
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

cleanup() {
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

split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES
require_positive_uint '--ready-timeout' "$READY_TIMEOUT"
require_uint '--poll' "$POLL_INTERVAL"
require_positive_uint '--users' "$USERS"
require_positive_uint '--groups' "$GROUP_COUNT"
require_positive_uint '--members' "$GROUP_MEMBERS"
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

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

mkdir -p "$OUT_DIR" "$(node_log_dir)" "$(capabilities_dir)" "$(capacity_dir)" "$(snapshot_dir)" "$(metrics_dir)"
: >"$(cluster_log)"
: >"$(sim_output)"

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

capture_target_evidence() {
  local idx=0
  local api
  for api in "${API_VALUES[@]}"; do
    local node=$((idx + 1))
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
    grep -q "\"tcp_addr\":\"${GATEWAY_VALUES[$idx]}\"" "$capacity" || die "node${node} capacity target did not publish expected gateway ${GATEWAY_VALUES[$idx]}"
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
  (
    cd "$ROOT_DIR"
    "${cmd[@]}"
  ) | tee "$(sim_output)" || status=$?
  if [[ "$status" -ne 0 ]]; then
    die "wkcli sim failed with status ${status}"
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
write_summary
log "smoke passed; evidence_dir=$OUT_DIR"
