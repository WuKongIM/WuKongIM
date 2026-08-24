#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
RUN_DIR=""
BASE_PORT=15000
READY_TIMEOUT=120
STOP_AFTER=0
SEND_RATE=100
MEASURE_SECONDS=0
WARMUP_SECONDS=60
HOT_SENDACK_P99_MS=400
MINIMUM_THROUGHPUT_PERCENT=90
# Full Prometheus snapshots are deliberately less frequent than the product's
# own five-second observation loop. Each service snapshot is roughly 750 KiB
# in a realistic lifecycle run; scraping all three nodes every five seconds
# can make the diagnostic collector interfere with the evidence it measures.
METRICS_SAMPLE_SECONDS=30
DRY_RUN=0
RUNTIME_DEFAULTS=0
CLEANUP_TIMEOUT=15
GRACEFUL_STOP_TIMEOUT=90
RUN_ID=local-chat-lifecycle-shakeout
PPROF_CPU_SECONDS=10

PIDS=()
NAMES=()
COORDINATOR_PID=""
STOP_SENT=0
GRACEFUL_STOP_DEADLINE=0
PPROF_PID=""
PPROF_TRIGGERED=0
PPROF_EXIT_STATUS=0
PPROF_TRIGGER_KIND=""
PPROF_TRIGGER_PREVIOUS_UTC=""
PPROF_TRIGGER_CURRENT_UTC=""
CUT_CURSOR=0
CUT_QUERY_READY=0
OPERATOR_SIGNAL_STATUS=0
OPERATOR_STOP_PENDING=0
DRAIN_BOUNDARY_RECORDED=0
TERMINAL_BOUNDARY_AT=""
HARNESS_FAILURE_REASON=""
COORDINATOR_JOINED=0
COORDINATOR_STATUS=0
WAIT_CHILD_STATUS=0
SOURCE_REVISION=unknown
SOURCE_DIRTY=true
SOURCE_REBUILDABLE_FROM_REVISION=false
SOURCE_CAPTURE=binary_identity_only
SOURCE_BUILD_START_REVISION=unknown
SOURCE_BUILD_START_DIRTY=true
SOURCE_BUILD_END_REVISION=unknown
SOURCE_BUILD_END_DIRTY=true
HOST_OVERLAP_MONITOR_PID=""
HOST_OVERLAP_MONITOR_STARTED=0
HOST_OVERLAP_MONITOR_JOINED=0
HOST_CONFOUNDED=0

usage() {
  sed -n '/^# Usage:/,/^#   -h, --help/p' "$0" | sed 's/^# \{0,1\}//'
}

# Usage: run-wukongim-three-node-chat-lifecycle-shakeout.sh --run-dir DIR [options]
#
# Builds one WuKongIM binary and one wkbench binary, then starts three service
# nodes, three authenticated lifecycle workers, four local filesystem metrics
# endpoints, and one coordinator. Every artifact stays below the fresh run dir.
#
# Options:
#   --run-dir DIR       Required fresh artifact, data, PID, log, and report root.
#   --base-port PORT    First port in a reserved 64-port range (default 15000).
#   --ready-timeout S   Readiness deadline in seconds (default 120).
#   --stop-after S      Request graceful coordinator stop after S seconds; 0 waits.
#   --send-rate N       Offered SEND rate per second (default 100).
#   --measure-seconds S After warmup, measure for S seconds; 0 keeps legacy mode.
#   --warmup-seconds S  Traffic warmup before a measured step (default 60).
#   --drain-timeout S   Maximum graceful drain in seconds (default 90).
#   --hot-sendack-p99-ms N
#                       Sealed hot SENDACK p99 limit in milliseconds (default 400).
#   --runtime-defaults  Remove the promoted performance overrides from generated
#                       node configs and let the product supply its defaults.
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
load_host_metrics_port() { port 60; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-dir) [[ $# -ge 2 ]] || die '--run-dir requires a value'; RUN_DIR="$2"; shift 2 ;;
    --base-port) [[ $# -ge 2 ]] || die '--base-port requires a value'; BASE_PORT="$2"; shift 2 ;;
    --ready-timeout) [[ $# -ge 2 ]] || die '--ready-timeout requires a value'; READY_TIMEOUT="$2"; shift 2 ;;
    --stop-after) [[ $# -ge 2 ]] || die '--stop-after requires a value'; STOP_AFTER="$2"; shift 2 ;;
    --send-rate) [[ $# -ge 2 ]] || die '--send-rate requires a value'; SEND_RATE="$2"; shift 2 ;;
    --measure-seconds) [[ $# -ge 2 ]] || die '--measure-seconds requires a value'; MEASURE_SECONDS="$2"; shift 2 ;;
    --warmup-seconds) [[ $# -ge 2 ]] || die '--warmup-seconds requires a value'; WARMUP_SECONDS="$2"; shift 2 ;;
    --drain-timeout) [[ $# -ge 2 ]] || die '--drain-timeout requires a value'; GRACEFUL_STOP_TIMEOUT="$2"; shift 2 ;;
    --hot-sendack-p99-ms) [[ $# -ge 2 ]] || die '--hot-sendack-p99-ms requires a value'; HOT_SENDACK_P99_MS="$2"; shift 2 ;;
    --runtime-defaults) RUNTIME_DEFAULTS=1; shift ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

require_uint '--base-port' "$BASE_PORT"
require_uint '--ready-timeout' "$READY_TIMEOUT"
require_uint '--stop-after' "$STOP_AFTER"
require_uint '--send-rate' "$SEND_RATE"
require_uint '--measure-seconds' "$MEASURE_SECONDS"
require_uint '--warmup-seconds' "$WARMUP_SECONDS"
require_uint '--drain-timeout' "$GRACEFUL_STOP_TIMEOUT"
require_uint '--hot-sendack-p99-ms' "$HOT_SENDACK_P99_MS"
(( BASE_PORT >= 1024 && BASE_PORT <= 65472 )) || die '--base-port must reserve 64 ports within 1024..65535'
(( READY_TIMEOUT > 0 )) || die '--ready-timeout must be greater than zero'
(( SEND_RATE > 0 )) || die '--send-rate must be greater than zero'
(( SEND_RATE <= 1000000 )) || die '--send-rate must not exceed 1000000'
(( WARMUP_SECONDS > 0 )) || die '--warmup-seconds must be greater than zero'
(( GRACEFUL_STOP_TIMEOUT > 0 )) || die '--drain-timeout must be greater than zero'
(( HOT_SENDACK_P99_MS > 0 && HOT_SENDACK_P99_MS <= 1000 )) || die '--hot-sendack-p99-ms must be within 1..1000'
(( STOP_AFTER == 0 || MEASURE_SECONDS == 0 )) || die '--stop-after and --measure-seconds are mutually exclusive'
validate_run_dir

WUKONGIM_BIN="$RUN_DIR/bin/wukongim"
WKBENCH_BIN="$RUN_DIR/bin/wkbench"
CONFIG_DIR="$RUN_DIR/config"
DATA_DIR="$RUN_DIR/data"
LOG_DIR="$RUN_DIR/logs"
PID_DIR="$RUN_DIR/pids"
WORKER_DIR="$RUN_DIR/workers"
REPORT_DIR="$RUN_DIR/report"
EVIDENCE_DIR="$RUN_DIR/evidence"
METRICS_DIR="$RUN_DIR/metrics"
LIFECYCLE_CONFIG="$RUN_DIR/chat-lifecycle.yaml"
PHASE_STATE_FILE="$EVIDENCE_DIR/phase-state"
CUT_QUERY_FILE="$EVIDENCE_DIR/worker-cut-query.json"
CUT_QUERY_NEXT="$EVIDENCE_DIR/worker-cut-query.json.next"
FROZEN_WORKER_LOG="$EVIDENCE_DIR/coordinator-worker-cuts.log"
UNIFIED_TIMELINE_JSON="$EVIDENCE_DIR/unified-timeline.json"
UNIFIED_TIMELINE_TSV="$EVIDENCE_DIR/unified-timeline.tsv"
PPROF_DIR="$EVIDENCE_DIR/threshold-pprof"
PPROF_STATUS_FILE="$EVIDENCE_DIR/threshold-pprof-status.json"
GRACEFUL_STOP_STATUS_FILE="$EVIDENCE_DIR/graceful-stop-status.json"
GRACEFUL_STOP_SNAPSHOT_DIR="$EVIDENCE_DIR/graceful-stop-timeout"
HOST_OVERLAP_STATUS_FILE="$EVIDENCE_DIR/measured-host-overlap.tsv"
HOST_OVERLAP_DETECTOR="$ROOT_DIR/scripts/chat-lifecycle/detect-local-workload-overlap.sh"
STORAGE_OVERLAP_FILE="$EVIDENCE_DIR/storage-overlap.tsv"
STORAGE_OVERLAP_CAPTURE="$ROOT_DIR/scripts/chat-lifecycle/capture-local-storage-overlap.sh"
STORAGE_METRICS_CUT_VALIDATOR="$ROOT_DIR/scripts/storage-metrics-cut-consistent.awk"

print_plan() {
  printf 'run_dir=%s\n' "$RUN_DIR"
  printf 'build_wukongim=GOWORK=off go build -o %s ./cmd/wukongim\n' "$WUKONGIM_BIN"
  printf 'build_wkbench=GOWORK=off go build -o %s ./cmd/wkbench\n' "$WKBENCH_BIN"
  printf 'logical_slot_groups=12\n'
  printf 'hash_slots=256\n'
  printf 'replicas=3/3\n'
  printf 'online_connections=2500\n'
  printf 'offered_send_rate_per_second=%s\n' "$SEND_RATE"
  printf 'measured_duration_seconds=%s\n' "$MEASURE_SECONDS"
  printf 'warmup_seconds=%s\n' "$WARMUP_SECONDS"
  printf 'drain_timeout_seconds=%s\n' "$GRACEFUL_STOP_TIMEOUT"
  printf 'hot_sendack_p99_milliseconds=%s\n' "$HOT_SENDACK_P99_MS"
  printf 'raw_metrics_sample_seconds=%s\n' "$METRICS_SAMPLE_SECONDS"
  if (( RUNTIME_DEFAULTS == 1 )); then
    printf 'runtime_profile=product_defaults\n'
    printf 'channel_store_append_workers=product_default\n'
    printf 'channel_store_apply_workers=product_default\n'
    printf 'channel_rpc_workers=product_default\n'
    printf 'channel_rpc_batch_max_items=product_default\n'
    printf 'gateway_gnet_multicore=product_default\n'
    printf 'gateway_gnet_event_loops=product_default\n'
    printf 'gateway_async_send_workers=product_default\n'
    printf 'gateway_async_send_queue_capacity=product_default\n'
    printf 'delivery_recipient_worker_concurrency=product_default\n'
  else
    printf 'runtime_profile=explicit_rehearsal_tuning\n'
    printf 'channel_store_append_workers=500\n'
    printf 'gateway_async_send_workers=1000\n'
  fi
  printf 'gateway_async_send_batch_max_records=1\n'
  printf 'commit_coordinator_flush_window=500us\n'
  printf 'commit_coordinator_shards=1\n'
  printf 'sync_commit=true\n'
  printf 'coordinator_config=%s\n' "$LIFECYCLE_CONFIG"
  printf 'report_dir=%s\n' "$REPORT_DIR"
  local node
  for node in 1 2 3; do
    printf 'service_%s=http://127.0.0.1:%s\n' "$node" "$(api_port "$node")"
    printf 'gateway_%s=127.0.0.1:%s\n' "$node" "$(gateway_port "$node")"
    printf 'worker_%s=http://127.0.0.1:%s\n' "$node" "$(worker_port "$node")"
    printf 'host_metrics_%s=http://127.0.0.1:%s\n' "$node" "$(host_metrics_port "$node")"
  done
  printf 'host_metrics_load=http://127.0.0.1:%s\n' "$(load_host_metrics_port)"
}

if [[ "$DRY_RUN" -eq 1 ]]; then
  print_plan
  exit 0
fi

command -v go >/dev/null 2>&1 || die 'go is required'
command -v curl >/dev/null 2>&1 || die 'curl is required'
command -v awk >/dev/null 2>&1 || die 'awk is required'
command -v jq >/dev/null 2>&1 || die 'jq is required'
command -v ps >/dev/null 2>&1 || die 'ps is required'
[[ -x "$HOST_OVERLAP_DETECTOR" ]] || die "local workload overlap detector is unavailable: $HOST_OVERLAP_DETECTOR"
[[ -x "$STORAGE_OVERLAP_CAPTURE" ]] || die "local storage-overlap capture is unavailable: $STORAGE_OVERLAP_CAPTURE"
[[ -f "$STORAGE_METRICS_CUT_VALIDATOR" ]] || die "storage metrics cut validator is unavailable: $STORAGE_METRICS_CUT_VALIDATOR"
[[ -n "${WK_BENCH_API_TOKEN:-}" ]] || die 'WK_BENCH_API_TOKEN is required'
[[ -n "${WK_BENCH_WORKER_TOKEN:-}" ]] || die 'WK_BENCH_WORKER_TOKEN is required'
if [[ -e "$RUN_DIR" ]] && [[ -n "$(find "$RUN_DIR" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
  die "--run-dir must be absent or empty: $RUN_DIR"
fi

overlap="$("$HOST_OVERLAP_DETECTOR")" || die 'host_confounded: local workload overlap preflight was unavailable'
[[ -z "$overlap" ]] || die "host_confounded: another wukongim or wkbench process is active: $overlap"

SOURCE_BUILD_START_REVISION="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)"
SOURCE_REVISION="$SOURCE_BUILD_START_REVISION"
source_untracked=""
if [[ "$SOURCE_BUILD_START_REVISION" != unknown ]] &&
  git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD -- 2>/dev/null &&
  source_untracked="$(git -C "$ROOT_DIR" ls-files --others --exclude-standard 2>/dev/null)" &&
  [[ -z "$source_untracked" ]]; then
  SOURCE_BUILD_START_DIRTY=false
fi

finalize_source_rebuildability_after_builds() {
  local source_untracked=""
  SOURCE_BUILD_END_REVISION="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)"
  SOURCE_BUILD_END_DIRTY=true
  if [[ "$SOURCE_BUILD_END_REVISION" != unknown ]] &&
    git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD -- 2>/dev/null &&
    source_untracked="$(git -C "$ROOT_DIR" ls-files --others --exclude-standard 2>/dev/null)" &&
    [[ -z "$source_untracked" ]]; then
    SOURCE_BUILD_END_DIRTY=false
  fi

  # A binary can be reconstructed from source_revision only when the same
  # clean revision bounded both builds. Any dirty state, Git read failure, or
  # revision movement makes the sealed binaries the sole source identity.
  SOURCE_DIRTY=true
  SOURCE_REBUILDABLE_FROM_REVISION=false
  SOURCE_CAPTURE=binary_identity_only
  if [[ "$SOURCE_BUILD_START_REVISION" != unknown && "$SOURCE_BUILD_START_DIRTY" == false &&
    "$SOURCE_BUILD_END_REVISION" == "$SOURCE_BUILD_START_REVISION" && "$SOURCE_BUILD_END_DIRTY" == false ]]; then
    SOURCE_DIRTY=false
    SOURCE_REBUILDABLE_FROM_REVISION=true
    SOURCE_CAPTURE=git_revision
  fi
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

mkdir -p "$RUN_DIR/bin" "$CONFIG_DIR" "$DATA_DIR/load" "$LOG_DIR" "$PID_DIR" "$WORKER_DIR" "$REPORT_DIR" \
  "$EVIDENCE_DIR/snapshot-inventory" "$METRICS_DIR"
for node in 1 2 3; do
  mkdir -p "$DATA_DIR/node$node" "$LOG_DIR/node$node" "$WORKER_DIR/node$node"
  cp "$ROOT_DIR/scripts/wukongim/wukongim-node$node.toml" "$CONFIG_DIR/node$node.toml"
  if (( RUNTIME_DEFAULTS == 1 )); then
    strip_promoted_runtime_overrides "$CONFIG_DIR/node$node.toml"
  fi
done

log 'building service and benchmark binaries'
(cd "$ROOT_DIR" && GOWORK=off go build -o "$WUKONGIM_BIN" ./cmd/wukongim)
(cd "$ROOT_DIR" && GOWORK=off go build -o "$WKBENCH_BIN" ./cmd/wkbench)
finalize_source_rebuildability_after_builds

sed \
	-e 's/15001/__WK_API_1__/g' -e 's/15002/__WK_API_2__/g' -e 's/15003/__WK_API_3__/g' \
	-e 's/15011/__WK_METRICS_1__/g' -e 's/15012/__WK_METRICS_2__/g' -e 's/15013/__WK_METRICS_3__/g' \
	-e 's/15101/__WK_GATEWAY_1__/g' -e 's/15102/__WK_GATEWAY_2__/g' -e 's/15103/__WK_GATEWAY_3__/g' \
	-e 's/19091/__WK_WORKER_1__/g' -e 's/19092/__WK_WORKER_2__/g' -e 's/19093/__WK_WORKER_3__/g' \
	-e 's/19101/__WK_HOST_METRICS_1__/g' -e 's/19102/__WK_HOST_METRICS_2__/g' -e 's/19103/__WK_HOST_METRICS_3__/g' \
	-e 's/19104/__WK_LOAD_HOST_METRICS__/g' \
	-e "s/__WK_API_1__/$(api_port 1)/g" -e "s/__WK_API_2__/$(api_port 2)/g" -e "s/__WK_API_3__/$(api_port 3)/g" \
	-e "s/__WK_METRICS_1__/$(api_port 1)/g" -e "s/__WK_METRICS_2__/$(api_port 2)/g" -e "s/__WK_METRICS_3__/$(api_port 3)/g" \
	-e "s/__WK_GATEWAY_1__/$(gateway_port 1)/g" -e "s/__WK_GATEWAY_2__/$(gateway_port 2)/g" -e "s/__WK_GATEWAY_3__/$(gateway_port 3)/g" \
	-e "s/__WK_WORKER_1__/$(worker_port 1)/g" -e "s/__WK_WORKER_2__/$(worker_port 2)/g" -e "s/__WK_WORKER_3__/$(worker_port 3)/g" \
	-e "s/__WK_HOST_METRICS_1__/$(host_metrics_port 1)/g" -e "s/__WK_HOST_METRICS_2__/$(host_metrics_port 2)/g" -e "s/__WK_HOST_METRICS_3__/$(host_metrics_port 3)/g" \
	-e "s/__WK_LOAD_HOST_METRICS__/$(load_host_metrics_port)/g" \
	-e "s/send_rate_per_second: 100/send_rate_per_second: $SEND_RATE/" \
	-e "s/max_global_burst: 200/max_global_burst: $((SEND_RATE * 2))/" \
	-e "s/hot_sendack: {p99: 400ms, p999: 1s}/hot_sendack: {p99: ${HOT_SENDACK_P99_MS}ms, p999: 1s}/" \
  "$ROOT_DIR/configs/wkbench/chat-lifecycle/local-shakeout.yaml" >"$LIFECYCLE_CONFIG"

if (( MEASURE_SECONDS > 0 )); then
  checkpoint_milliseconds=$((WARMUP_SECONDS * 1000 + 1))
  final_seconds=$((WARMUP_SECONDS + MEASURE_SECONDS + GRACEFUL_STOP_TIMEOUT + 60))
  # The local staircase owns its post-drain rate verdict. Recovered first-attempt
  # failures remain in evidence but must not trigger the formal evaluator's
  # immediate terminal decision before the fixed measured interval completes.
  sed \
    -e "s/timeline: {warmup: 10m, checkpoint: 20m, final: 30m}/timeline: {warmup: ${WARMUP_SECONDS}s, checkpoint: ${checkpoint_milliseconds}ms, final: ${final_seconds}s}/" \
    -e 's/overall_first_attempt_failure: {max_failures: 1, per_attempts: 10000, operator: "<"}/overall_first_attempt_failure: {max_failures: 1, per_attempts: 1, operator: "<="}/' \
    -e 's/any_minute_first_attempt_failure: {max_failures: 1, per_attempts: 1000, operator: "<="}/any_minute_first_attempt_failure: {max_failures: 1, per_attempts: 1, operator: "<="}/' \
    "$LIFECYCLE_CONFIG" >"$LIFECYCLE_CONFIG.next"
  mv "$LIFECYCLE_CONFIG.next" "$LIFECYCLE_CONFIG"
fi

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

record_evidence_identity() {
  local config_sha wukongim_sha wkbench_sha
  config_sha="$(sha256_file "$LIFECYCLE_CONFIG")"
  wukongim_sha="$(sha256_file "$WUKONGIM_BIN")"
  wkbench_sha="$(sha256_file "$WKBENCH_BIN")"
  {
    printf 'schema\twukongim/chat-lifecycle-local-evidence/v1\n'
    printf 'source_revision\t%s\n' "$SOURCE_REVISION"
    printf 'source_dirty\t%s\n' "$SOURCE_DIRTY"
    printf 'source_rebuildable_from_revision\t%s\n' "$SOURCE_REBUILDABLE_FROM_REVISION"
    printf 'source_capture\t%s\n' "$SOURCE_CAPTURE"
    printf 'source_revision_before_build\t%s\n' "$SOURCE_BUILD_START_REVISION"
    printf 'source_dirty_before_build\t%s\n' "$SOURCE_BUILD_START_DIRTY"
    printf 'source_revision_after_build\t%s\n' "$SOURCE_BUILD_END_REVISION"
    printf 'source_dirty_after_build\t%s\n' "$SOURCE_BUILD_END_DIRTY"
    printf 'config_sha256\t%s\n' "$config_sha"
    printf 'wukongim_binary_sha256\t%s\n' "$wukongim_sha"
    printf 'wkbench_binary_sha256\t%s\n' "$wkbench_sha"
    printf 'online_connections\t2500\n'
    printf 'offered_send_rate_per_second\t%s\n' "$SEND_RATE"
    printf 'measured_duration_seconds\t%s\n' "$MEASURE_SECONDS"
    printf 'logical_slot_groups\t12\n'
    printf 'hash_slots\t256\n'
    printf 'slot_replicas\t3\n'
    printf 'channel_replicas\t3\n'
    if (( RUNTIME_DEFAULTS == 1 )); then
      printf 'runtime_profile\tproduct_defaults\n'
      printf 'channel_store_append_workers\tproduct_default\n'
      printf 'channel_store_apply_workers\tproduct_default\n'
      printf 'channel_rpc_workers\tproduct_default\n'
      printf 'channel_rpc_batch_max_items\tproduct_default\n'
      printf 'gateway_gnet_multicore\tproduct_default\n'
      printf 'gateway_gnet_event_loops\tproduct_default\n'
      printf 'gateway_async_send_workers\tproduct_default\n'
      printf 'gateway_async_send_queue_capacity\tproduct_default\n'
      printf 'delivery_recipient_worker_concurrency\tproduct_default\n'
    else
      printf 'runtime_profile\texplicit_rehearsal_tuning\n'
      printf 'channel_store_append_workers\t500\n'
      printf 'gateway_async_send_workers\t1000\n'
    fi
    printf 'gateway_async_send_batch_max_records\t1\n'
    printf 'commit_coordinator_flush_window\t500us\n'
    printf 'commit_coordinator_shards\t1\n'
    printf 'sync_commit\ttrue\n'
    printf 'physical_io_source\thost-node-1\n'
  } >"$EVIDENCE_DIR/identity.tsv"
  df -Pk "$RUN_DIR" >"$EVIDENCE_DIR/filesystem-preflight.txt"
}

record_evidence_identity

record_pid() {
  local name="$1" pid="$2"
  NAMES+=("$name")
  PIDS+=("$pid")
  printf '%s\n' "$pid" >"$PID_DIR/$name.pid"
}

# wait may return 128+signal while its child is still alive when the wrapper's
# first operator trap runs. Keep ownership until the exact child is reaped; the
# second operator signal remains the explicit force-exit escape hatch.
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
  if [[ -n "$PPROF_PID" ]]; then
    kill -TERM "$PPROF_PID" 2>/dev/null || true
    wait_child_uninterrupted "$PPROF_PID"
    PPROF_PID=""
  fi
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

mark_recorded_stopped() {
  local wanted="$1" index
  for index in "${!NAMES[@]}"; do
    if [[ "${NAMES[$index]}" == "$wanted" ]]; then
      PIDS[$index]=""
      return 0
    fi
  done
  return 1
}

write_measured_host_overlap_status() {
  local status="$1" started="$2" observed="$3" samples="$4" pid="$5" command="$6"
  local temporary="$HOST_OVERLAP_STATUS_FILE.next.$$"
  {
    printf 'schema\twukongim/chat-lifecycle-measured-host-overlap/v1\n'
    printf 'status\t%s\n' "$status"
    printf 'started_at_utc\t%s\n' "$started"
    printf 'completed_at_utc\t%s\n' "$observed"
    printf 'samples\t%s\n' "$samples"
    printf 'pid\t%s\n' "$pid"
    printf 'command\t%s\n' "$command"
    printf 'sample_seconds\t1\n'
  } >"$temporary"
  mv "$temporary" "$HOST_OVERLAP_STATUS_FILE"
}

start_measured_host_overlap_monitor() {
  local pid
  local -a owned_pids=("$$" "${PIDS[@]}")
  (( HOST_OVERLAP_MONITOR_STARTED == 0 )) || return 0
  HOST_OVERLAP_MONITOR_STARTED=1
  (
    local overlap="" first="" foreign_pid="" foreign_command="" observed=""
    local started samples=0 monitor_status=0
    started="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    trap 'monitor_status=143' INT TERM
    while [[ -s "$PHASE_STATE_FILE" ]] && [[ "$(<"$PHASE_STATE_FILE")" == measurement ]]; do
      observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      if ! overlap="$("$HOST_OVERLAP_DETECTOR" "${owned_pids[@]}")"; then
        write_measured_host_overlap_status observer_error "$started" "$observed" "$samples" 0 unavailable
        exit 0
      fi
      samples=$((samples + 1))
      if [[ -n "$overlap" ]]; then
        first="${overlap%%$'\n'*}"
        IFS=$'\t' read -r foreign_pid foreign_command <<<"$first"
        write_measured_host_overlap_status overlap "$started" "$observed" "$samples" "$foreign_pid" "$foreign_command"
        exit 0
      fi
      sleep 1 || true
    done
    if (( monitor_status != 0 )); then
      write_measured_host_overlap_status observer_error "$started" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        "$samples" 0 unavailable
    else
      write_measured_host_overlap_status clear "$started" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$samples" 0 none
    fi
  ) >"$LOG_DIR/measured-host-overlap.log" 2>&1 &
  pid=$!
  HOST_OVERLAP_MONITOR_PID="$pid"
  record_pid measured-host-overlap-monitor "$pid"
}

check_measured_host_overlap() {
  local monitor_status=0 status="" samples=""
  (( HOST_OVERLAP_MONITOR_STARTED != 0 )) || return 0
  (( HOST_OVERLAP_MONITOR_JOINED == 0 )) || return 0
  if [[ -n "$HOST_OVERLAP_MONITOR_PID" ]]; then
    wait_child_uninterrupted "$HOST_OVERLAP_MONITOR_PID"
    monitor_status="$WAIT_CHILD_STATUS"
    mark_recorded_stopped measured-host-overlap-monitor || true
    HOST_OVERLAP_MONITOR_PID=""
  fi
  HOST_OVERLAP_MONITOR_JOINED=1
  if (( monitor_status != 0 )) || [[ ! -s "$HOST_OVERLAP_STATUS_FILE" ]] ||
    ! status="$(awk -F '\t' '$1 == "status" { print $2 }' "$HOST_OVERLAP_STATUS_FILE" 2>/dev/null)"; then
    write_measured_host_overlap_status observer_error "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      "$(date -u +%Y-%m-%dT%H:%M:%SZ)" 0 0 unavailable
    status=observer_error
  fi
  samples="$(awk -F '\t' '$1 == "samples" { print $2 }' "$HOST_OVERLAP_STATUS_FILE" 2>/dev/null)"
  case "$status" in
    clear|overlap)
      if ! [[ "$samples" =~ ^[1-9][0-9]*$ ]]; then
        write_measured_host_overlap_status observer_error "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
          "$(date -u +%Y-%m-%dT%H:%M:%SZ)" 0 0 unavailable
        status=observer_error
      fi
      ;;
    observer_error)
      if ! [[ "$samples" =~ ^[0-9]+$ ]]; then
        write_measured_host_overlap_status observer_error "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
          "$(date -u +%Y-%m-%dT%H:%M:%SZ)" 0 0 unavailable
      fi
      ;;
    *)
      write_measured_host_overlap_status observer_error "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
        "$(date -u +%Y-%m-%dT%H:%M:%SZ)" 0 0 unavailable
      status=observer_error
      ;;
  esac
  case "$status" in
    clear) ;;
    overlap)
      HOST_CONFOUNDED=1
      log 'measured interval overlapped another wukongim or wkbench process; result will be host_confounded'
      ;;
    *)
      HOST_CONFOUNDED=1
      log 'measured host-overlap observation was incomplete; result will fail closed as host_confounded'
      ;;
  esac
}

stop_process_metrics_collector() {
  local index pid=""
  for index in "${!NAMES[@]}"; do
    if [[ "${NAMES[$index]}" == process-metrics-collector ]]; then
      pid="${PIDS[$index]}"
      break
    fi
  done
  [[ -n "$pid" ]] || return 0
  if kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null || true
  fi
  wait_child_uninterrupted "$pid"
  PIDS[$index]=""
}

request_coordinator_stop() {
  local reason="$1"
  (( STOP_SENT == 0 )) || return 0
  [[ -n "$COORDINATOR_PID" ]] && kill -0 "$COORDINATOR_PID" 2>/dev/null || return 1
  # Fence the request before signaling so a trap between shell commands cannot
  # send a second TERM to wkbench's force-stop handler.
  STOP_SENT=1
  GRACEFUL_STOP_DEADLINE=$((SECONDS + GRACEFUL_STOP_TIMEOUT))
  log "$reason: forwarding one TERM to the coordinator"
  kill -TERM "$COORDINATOR_PID" 2>/dev/null || return 1
}

handle_signal() {
  local signal_name="$1" exit_status="$2"
  if (( OPERATOR_SIGNAL_STATUS == 0 )); then
    OPERATOR_SIGNAL_STATUS="$exit_status"
    OPERATOR_STOP_PENDING=1
    if (( STOP_SENT != 0 )); then
      log "received $signal_name while the coordinator is already draining; preserving the first graceful stop"
    else
      log "received $signal_name; queued one graceful coordinator stop"
    fi
    return
  fi
  log "received a second operator signal ($signal_name); forcing cleanup"
  trap - INT TERM
  exit "$exit_status"
}

request_pending_operator_stop() {
  (( OPERATOR_STOP_PENDING != 0 && STOP_SENT == 0 )) || return 0
  [[ -n "$COORDINATOR_PID" ]] && kill -0 "$COORDINATOR_PID" 2>/dev/null || return 1
  close_operator_phase_if_needed
  request_coordinator_stop 'operator signal pending' || return 1
  OPERATOR_STOP_PENDING=0
}

trap terminate_recorded EXIT
trap 'handle_signal INT 130' INT
trap 'handle_signal TERM 143' TERM

CLUSTER_NODES="[{\"id\":1,\"addr\":\"127.0.0.1:$(cluster_port 1)\"},{\"id\":2,\"addr\":\"127.0.0.1:$(cluster_port 2)\"},{\"id\":3,\"addr\":\"127.0.0.1:$(cluster_port 3)\"}]"

start_service() {
  local node="$1" gateway_listeners pid
  local -a runtime_overrides=()
  gateway_listeners="[{\"name\":\"tcp-wkproto\",\"network\":\"tcp\",\"address\":\"127.0.0.1:$(gateway_port "$node")\",\"transport\":\"gnet\",\"protocol\":\"wkproto\"},{\"name\":\"ws-gateway\",\"network\":\"websocket\",\"address\":\"127.0.0.1:$(ws_port "$node")\",\"transport\":\"gnet\",\"protocol\":\"wsmux\"}]"
  if (( RUNTIME_DEFAULTS == 0 )); then
    runtime_overrides+=(
      "WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS=500"
      "WK_GATEWAY_RUNTIME_ASYNC_SEND_WORKERS=1000"
    )
  fi
  env -u WK_BENCH_WORKER_TOKEN -u WK_CHAT_LIFECYCLE_WORKER_TOKEN_FILE \
    "${runtime_overrides[@]}" \
    WK_NODE_ID="$node" \
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
  "$WKBENCH_BIN" worker --mode chat-lifecycle --listen "127.0.0.1:$(worker_port "$node")" \
    --work-dir "$WORKER_DIR/node$node" >"$LOG_DIR/worker-$node.log" 2>&1 &
  pid=$!
  record_pid "worker-$node" "$pid"
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

start_host_metrics() {
  local node="$1" pid
  local -a physical_io_args=(--physical-io=true)
  if [[ "$node" -ne 1 ]]; then
    physical_io_args=(--physical-io=false)
  fi
  write_process_metrics_for_host "$node" "$EVIDENCE_DIR/processes-node-$node.prom" || die "node $node process metrics initialization failed"
  "$WKBENCH_BIN" host-metrics --listen "127.0.0.1:$(host_metrics_port "$node")" \
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
  "$WKBENCH_BIN" host-metrics --listen "127.0.0.1:$(load_host_metrics_port)" \
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

for node in 1 2 3; do start_service "$node"; done
for node in 1 2 3; do wait_url "service-$node" "http://127.0.0.1:$(api_port "$node")/readyz" "" 3; done
for node in 1 2 3; do start_worker "$node"; start_host_metrics "$node"; done
start_load_host_metrics
for node in 1 2 3; do
  wait_url "worker-$node" "http://127.0.0.1:$(worker_port "$node")/healthz" "$WK_BENCH_WORKER_TOKEN"
  wait_url "host-metrics-$node" "http://127.0.0.1:$(host_metrics_port "$node")/healthz"
done
wait_url host-metrics-load "http://127.0.0.1:$(load_host_metrics_port)/healthz"

printf 'observed_at_utc\tphase\tnode\tstatus\n' >"$EVIDENCE_DIR/timeline.tsv"
printf 'observed_at_utc\trun_id\tsample\tnode\tstatus\tcompaction_count\tcompactions_in_progress\tsnapshot_files\tsnapshot_bytes\tsnapshot_identity\tsnapshot_inventory\n' >"$STORAGE_OVERLAP_FILE"

record_timeline_boundary() {
  printf '%s\t%s\tboundary\tcomplete\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$1" >>"$EVIDENCE_DIR/timeline.tsv"
}

record_timeline_boundary_at() {
  local observed_at_utc="$1" boundary="$2"
  [[ "$observed_at_utc" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$ ]] || return 1
  printf '%s\t%s\tboundary\tcomplete\n' "$observed_at_utc" "$boundary" >>"$EVIDENCE_DIR/timeline.tsv"
}

close_terminal_drain_boundary() {
  local terminal_at
  terminal_at="$(jq -er '
    select(.terminal_cut_present == true and .latest_cut.cut == "terminal") |
    .latest_cut.at
  ' "$CUT_QUERY_FILE" 2>/dev/null)" || return 1
  TERMINAL_BOUNDARY_AT="$terminal_at"
  if (( DRAIN_BOUNDARY_RECORDED == 0 )); then
    if (( qualification_seen == 1 )); then
      record_timeline_boundary_at "$terminal_at" measurement_end
    else
      record_timeline_boundary_at "$terminal_at" warmup_end
    fi
    record_timeline_boundary_at "$terminal_at" drain_start
    write_phase_state drain
    DRAIN_BOUNDARY_RECORDED=1
  fi
  record_timeline_boundary_at "$terminal_at" drain_end
  record_timeline_boundary_at "$terminal_at" shutdown_start
  write_phase_state shutdown
}

wait_for_service_sample_after_terminal_boundary() {
  local boundary_second current deadline LC_ALL=C
  [[ "$TERMINAL_BOUNDARY_AT" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$ ]] || return 1
  boundary_second="${TERMINAL_BOUNDARY_AT%Z}"
  boundary_second="${boundary_second%%.*}Z"
  deadline=$((SECONDS + 3))
  while true; do
    current="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    [[ "$current" > "$boundary_second" ]] && return 0
    (( SECONDS < deadline )) || return 1
    sleep 0.1
  done
}

write_phase_state() {
  local phase="$1" temporary="$PHASE_STATE_FILE.next.$$" current=""
  case "$phase" in warmup|measurement|drain|shutdown) ;; *) return 1 ;; esac
  if [[ -f "$PHASE_STATE_FILE" ]]; then
    current="$(<"$PHASE_STATE_FILE")"
    [[ "$current" == "$phase" ]] && return 0
  fi
  printf '%s\n' "$phase" >"$temporary"
  mv "$temporary" "$PHASE_STATE_FILE"
}

start_threshold_pprof_capture() {
  local trigger_kind="$1" previous_utc="$2" current_utc="$3"
  # Never expose the inherited bearer token if the wrapper itself was invoked
  # with shell xtrace enabled.
  set +x
  (( PPROF_TRIGGERED == 0 )) || return 0
  PPROF_TRIGGERED=1
  PPROF_TRIGGER_KIND="$trigger_kind"
  PPROF_TRIGGER_PREVIOUS_UTC="$previous_utc"
  PPROF_TRIGGER_CURRENT_UTC="$current_utc"
  WK_BENCH_API_TOKEN="$WK_BENCH_API_TOKEN" \
    bash "$ROOT_DIR/scripts/capture-wukongim-local-threshold-pprof.sh" \
    --out-dir "$PPROF_DIR" \
    --phase-state-file "$PHASE_STATE_FILE" \
    --trigger-kind "$trigger_kind" \
    --trigger-observed-phase measurement \
    --previous-utc "$previous_utc" \
    --current-utc "$current_utc" \
    --node "http://127.0.0.1:$(api_port 1)" \
    --node "http://127.0.0.1:$(api_port 2)" \
    --node "http://127.0.0.1:$(api_port 3)" \
    --cpu-seconds "$PPROF_CPU_SECONDS" >"$LOG_DIR/threshold-pprof.log" 2>&1 &
  PPROF_PID=$!
  log "measured $trigger_kind threshold crossed; started bounded three-node pprof capture"
}

refresh_live_cut_query() {
  local next_cursor phase trigger
  local -a query=(
    "$WKBENCH_BIN" report chat-lifecycle-cut-query
    --worker-log "$LOG_DIR/coordinator.log"
    --run-id "$RUN_ID"
    --cursor "$CUT_CURSOR"
    --offered-rate "$SEND_RATE"
    --minimum-throughput-percent "$MINIMUM_THROUGHPUT_PERCENT"
    --output "$CUT_QUERY_NEXT"
  )
  if (( CUT_QUERY_READY == 1 )); then
    query+=(--previous-query "$CUT_QUERY_FILE")
  fi
  if ! "${query[@]}"; then
    rm -f "$CUT_QUERY_NEXT"
    return 1
  fi
  if ! jq -e '.schema == "wukongim/chat-lifecycle-worker-cut-query/v1" and (.next_cursor | type == "number" and . >= 0 and . == floor)' \
    "$CUT_QUERY_NEXT" >/dev/null 2>&1; then
    rm -f "$CUT_QUERY_NEXT"
    return 1
  fi
  next_cursor="$(jq -er '.next_cursor' "$CUT_QUERY_NEXT")" || return 1
  mv "$CUT_QUERY_NEXT" "$CUT_QUERY_FILE"
  CUT_CURSOR="$next_cursor"
  CUT_QUERY_READY=1
  phase="$(<"$PHASE_STATE_FILE")"
  if [[ "$phase" == measurement && "$PPROF_TRIGGERED" -eq 0 ]]; then
    trigger="$(jq -er '
      first(.transitions[] | select(
        .measurement_eligible == true and
        (.trigger_kind == "actual_offered_ratio" or .trigger_kind == "terminal_product_failure")
      )) | [.trigger_kind, .previous_at, .current_at] | @tsv
    ' "$CUT_QUERY_FILE" 2>/dev/null)" || trigger=""
    if [[ -n "$trigger" ]]; then
      local trigger_kind previous_utc current_utc
      IFS=$'\t' read -r trigger_kind previous_utc current_utc <<<"$trigger"
      start_threshold_pprof_capture "$trigger_kind" "$previous_utc" "$current_utc"
    fi
  fi
}

close_operator_phase_if_needed() {
  local phase
  (( OPERATOR_SIGNAL_STATUS != 0 )) || return 0
  [[ -s "$PHASE_STATE_FILE" ]] || return 0
  phase="$(<"$PHASE_STATE_FILE")"
  case "$phase" in
    measurement)
      refresh_live_cut_query || log 'operator-stop measured worker-cut query was unavailable'
      record_timeline_boundary measurement_end
      record_timeline_boundary drain_start
      write_phase_state drain
      DRAIN_BOUNDARY_RECORDED=1
      ;;
    warmup)
      record_timeline_boundary warmup_end
      record_timeline_boundary drain_start
      write_phase_state drain
      DRAIN_BOUNDARY_RECORDED=1
      ;;
  esac
}

join_threshold_pprof_capture() {
  [[ -n "$PPROF_PID" ]] || return 0
  wait_child_uninterrupted "$PPROF_PID"
  PPROF_EXIT_STATUS="$WAIT_CHILD_STATUS"
  PPROF_PID=""
}

write_threshold_pprof_status() {
  local status reason valid trigger_kind metadata_relative temporary node kind profile_status profile_path
  temporary="$PPROF_STATUS_FILE.next.$$"
  if (( PPROF_TRIGGERED == 0 )); then
    if ! jq -e '
      .schema == "wukongim/chat-lifecycle-unified-timeline/v1" and
      .measured_first_breach.observed == false
    ' "$UNIFIED_TIMELINE_JSON" >/dev/null 2>&1; then
      jq -n '{
        schema:"wukongim/chat-lifecycle-threshold-pprof-status/v1",
        status:"operational_error", evidence_complete:false, capture_valid:false,
        reason:"measured_threshold_was_not_captured", trigger_kind:"",
        trigger_previous_utc:"", trigger_current_utc:"", metadata:""
      }' >"$temporary"
      mv "$temporary" "$PPROF_STATUS_FILE"
      return 0
    fi
    jq -n '{
      schema:"wukongim/chat-lifecycle-threshold-pprof-status/v1",
      status:"not_triggered", evidence_complete:true, capture_valid:true,
      reason:"no_measured_threshold", trigger_kind:"",
      trigger_previous_utc:"", trigger_current_utc:"", metadata:""
    }' >"$temporary"
    mv "$temporary" "$PPROF_STATUS_FILE"
    return 0
  fi
  metadata_relative="threshold-pprof/metadata.json"
  if (( PPROF_EXIT_STATUS != 0 )) || ! jq -e \
    --arg trigger_kind "$PPROF_TRIGGER_KIND" \
    --arg previous_utc "$PPROF_TRIGGER_PREVIOUS_UTC" \
    --arg current_utc "$PPROF_TRIGGER_CURRENT_UTC" '
    def exact_keys($expected): (keys | sort) == ($expected | sort);
    .schema == "wukongim.local_threshold_pprof/v1" and
    exact_keys(["schema","trigger","capture","nodes"]) and
    (.trigger | exact_keys(["kind","observed_phase","previous_utc","current_utc"])) and
    .trigger.kind == $trigger_kind and .trigger.observed_phase == "measurement" and
    .trigger.previous_utc == $previous_utc and
    .trigger.current_utc == $current_utc and
    (.capture | exact_keys(["status","valid","reason","start_phase","end_phase","started_at_utc","completed_at_utc","cpu_seconds"])) and
    (.capture.status == "complete" or .capture.status == "partial" or .capture.status == "invalid") and
    (.capture.valid | type == "boolean") and
    ((.capture.status == "complete" and .capture.valid == true) or
     (.capture.status != "complete" and .capture.valid == false)) and
    (.capture.reason | type == "string" and length > 0) and
    (.capture.start_phase == "warmup" or .capture.start_phase == "measurement" or
     .capture.start_phase == "drain" or .capture.start_phase == "shutdown" or
     .capture.start_phase == "missing" or .capture.start_phase == "invalid") and
    (.capture.end_phase == "warmup" or .capture.end_phase == "measurement" or
     .capture.end_phase == "drain" or .capture.end_phase == "shutdown" or
     .capture.end_phase == "missing" or .capture.end_phase == "invalid") and
    (.capture.started_at_utc | type == "string" and length > 0) and
    (.capture.completed_at_utc | type == "string" and length > 0) and
    (.capture.cpu_seconds | type == "number" and . >= 1 and . <= 30 and . == floor) and
    (.nodes | type == "array" and length == 3) and
    [.nodes[].node] == ["node-1","node-2","node-3"] and
    all(.nodes[];
      exact_keys(["node","cpu","heap","goroutine"]) and
      all([.cpu,.heap,.goroutine][]; . == "complete" or . == "missing"))
  ' "$PPROF_DIR/metadata.json" >/dev/null 2>&1; then
    jq -n --argjson helper_exit "$PPROF_EXIT_STATUS" \
      --arg trigger_kind "$PPROF_TRIGGER_KIND" \
      --arg previous_utc "$PPROF_TRIGGER_PREVIOUS_UTC" \
      --arg current_utc "$PPROF_TRIGGER_CURRENT_UTC" '{
      schema:"wukongim/chat-lifecycle-threshold-pprof-status/v1",
      status:"operational_error", evidence_complete:false, capture_valid:false,
      reason:"missing_or_invalid_helper_metadata", trigger_kind:$trigger_kind,
      trigger_previous_utc:$previous_utc, trigger_current_utc:$current_utc, metadata:"",
      helper_exit_status:$helper_exit
    }' >"$temporary"
    mv "$temporary" "$PPROF_STATUS_FILE"
    return 0
  fi
  for node in 1 2 3; do
    for kind in cpu heap goroutine; do
      profile_status="$(jq -er --arg node "node-$node" --arg kind "$kind" \
        '.nodes[] | select(.node == $node) | .[$kind]' "$PPROF_DIR/metadata.json")" || profile_status=""
      case "$kind" in
        cpu) profile_path="$PPROF_DIR/profiles/node-$node-cpu.pb.gz" ;;
        heap) profile_path="$PPROF_DIR/profiles/node-$node-heap.pb.gz" ;;
        goroutine) profile_path="$PPROF_DIR/profiles/node-$node-goroutine.txt" ;;
      esac
      if [[ "$profile_status" == complete ]]; then
        [[ -f "$profile_path" && ! -L "$profile_path" && -s "$profile_path" ]] || profile_status=invalid
      elif [[ "$profile_status" == missing ]]; then
        [[ ! -e "$profile_path" ]] || profile_status=invalid
      else
        profile_status=invalid
      fi
      if [[ "$profile_status" == invalid ]]; then
        jq -n --argjson helper_exit "$PPROF_EXIT_STATUS" \
          --arg trigger_kind "$PPROF_TRIGGER_KIND" \
          --arg previous_utc "$PPROF_TRIGGER_PREVIOUS_UTC" \
          --arg current_utc "$PPROF_TRIGGER_CURRENT_UTC" '{
          schema:"wukongim/chat-lifecycle-threshold-pprof-status/v1",
          status:"operational_error", evidence_complete:false, capture_valid:false,
          reason:"profile_blob_disagrees_with_metadata", trigger_kind:$trigger_kind,
          trigger_previous_utc:$previous_utc, trigger_current_utc:$current_utc, metadata:"",
          helper_exit_status:$helper_exit
        }' >"$temporary"
        mv "$temporary" "$PPROF_STATUS_FILE"
        return 0
      fi
    done
  done
  if ! jq -e --arg trigger_kind "$PPROF_TRIGGER_KIND" \
    --arg previous_utc "$PPROF_TRIGGER_PREVIOUS_UTC" \
    --arg current_utc "$PPROF_TRIGGER_CURRENT_UTC" '
      .schema == "wukongim/chat-lifecycle-unified-timeline/v1" and
      .measured_first_breach.observed == true and
      .measured_first_breach.trigger_kind == $trigger_kind and
      .measured_first_breach.previous_at == $previous_utc and
      .measured_first_breach.current_at == $current_utc
    ' "$UNIFIED_TIMELINE_JSON" >/dev/null 2>&1; then
    jq -n --argjson helper_exit "$PPROF_EXIT_STATUS" \
      --arg trigger_kind "$PPROF_TRIGGER_KIND" \
      --arg previous_utc "$PPROF_TRIGGER_PREVIOUS_UTC" \
      --arg current_utc "$PPROF_TRIGGER_CURRENT_UTC" '{
      schema:"wukongim/chat-lifecycle-threshold-pprof-status/v1",
      status:"operational_error", evidence_complete:false, capture_valid:false,
      reason:"profile_trigger_disagrees_with_unified_timeline", trigger_kind:$trigger_kind,
      trigger_previous_utc:$previous_utc, trigger_current_utc:$current_utc, metadata:"",
      helper_exit_status:$helper_exit
    }' >"$temporary"
    mv "$temporary" "$PPROF_STATUS_FILE"
    return 0
  fi
  status="$(jq -r '.capture.status' "$PPROF_DIR/metadata.json")"
  reason="$(jq -r '.capture.reason' "$PPROF_DIR/metadata.json")"
  valid="$(jq -r '.capture.valid' "$PPROF_DIR/metadata.json")"
  trigger_kind="$(jq -r '.trigger.kind' "$PPROF_DIR/metadata.json")"
  jq -n --arg status "$status" --arg reason "$reason" --arg trigger_kind "$trigger_kind" \
    --arg previous_utc "$PPROF_TRIGGER_PREVIOUS_UTC" --arg current_utc "$PPROF_TRIGGER_CURRENT_UTC" \
    --arg metadata "$metadata_relative" --argjson capture_valid "$valid" '{
      schema:"wukongim/chat-lifecycle-threshold-pprof-status/v1", status:$status,
      evidence_complete:true, capture_valid:$capture_valid, reason:$reason,
      trigger_kind:$trigger_kind, trigger_previous_utc:$previous_utc,
      trigger_current_utc:$current_utc, metadata:$metadata
    }' >"$temporary"
  mv "$temporary" "$PPROF_STATUS_FILE"
}

build_unified_timeline() {
  cp "$LOG_DIR/coordinator.log" "$FROZEN_WORKER_LOG"
  "$WKBENCH_BIN" report chat-lifecycle-timeline \
    --worker-log "$FROZEN_WORKER_LOG" \
    --boundary-timeline "$EVIDENCE_DIR/timeline.tsv" \
    --storage-overlap "$STORAGE_OVERLAP_FILE" \
    --run-id "$RUN_ID" \
    --offered-rate "$SEND_RATE" \
    --minimum-throughput-percent "$MINIMUM_THROUGHPUT_PERCENT" \
    --output-json "$UNIFIED_TIMELINE_JSON" \
    --output-tsv "$UNIFIED_TIMELINE_TSV"
}

capture_metric_target() {
  local url="$1" timeout="$2" destination="$3" temporary="$3.next"
  if curl -fsS --max-time "$timeout" "$url" >"$temporary"; then
    mv "$temporary" "$destination"
    return 0
  fi
  rm -f "$temporary"
  : >"$destination"
  return 1
}

capture_consistent_service_metric_target() {
  local url="$1" timeout="$2" destination="$3" temporary="$3.next" attempt=0
  while (( attempt < 5 )); do
    attempt=$((attempt + 1))
    if curl -fsS --max-time "$timeout" "$url" >"$temporary" &&
      awk -f "$STORAGE_METRICS_CUT_VALIDATOR" "$temporary"; then
      mv "$temporary" "$destination"
      return 0
    fi
    rm -f "$temporary"
    (( attempt == 5 )) || sleep 0.01
  done
  : >"$destination"
  return 1
}

capture_service_metrics() {
  local phase="$1" node destination observed status host_name index storage_observed row_file
  local -a capture_pids=() capture_names=() storage_pids=()
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for node in 1 2 3; do
    destination="$METRICS_DIR/node-$node-$phase.prom"
    if [[ "$phase" == before || "$phase" == after ]]; then
      capture_consistent_service_metric_target "http://127.0.0.1:$(api_port "$node")/metrics" 3 "$destination" &
    else
      capture_metric_target "http://127.0.0.1:$(api_port "$node")/metrics" 3 "$destination" &
    fi
    capture_pids+=("$!")
    capture_names+=("node-$node")
  done
  for node in 1 2 3; do
    host_name="host-node-$node"
    destination="$METRICS_DIR/$host_name-$phase.prom"
    capture_metric_target "http://127.0.0.1:$(host_metrics_port "$node")/metrics" 6 "$destination" &
    capture_pids+=("$!")
    capture_names+=("$host_name")
  done
  destination="$METRICS_DIR/host-load-$phase.prom"
  capture_metric_target "http://127.0.0.1:$(load_host_metrics_port)/metrics" 6 "$destination" &
  capture_pids+=("$!")
  capture_names+=(host-load)
  for index in "${!capture_pids[@]}"; do
    status=complete
    wait_child_uninterrupted "${capture_pids[$index]}"
    (( WAIT_CHILD_STATUS == 0 )) || status=missing
    printf '%s\t%s\t%s\t%s\n' "$observed" "$phase" "${capture_names[$index]}" "$status" >>"$EVIDENCE_DIR/timeline.tsv"
  done
  storage_observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for node in 1 2 3; do
    row_file="$EVIDENCE_DIR/snapshot-inventory/$phase-node-$node.row"
    "$STORAGE_OVERLAP_CAPTURE" \
      --metrics "$METRICS_DIR/node-$node-$phase.prom" \
      --snapshot-root "$DATA_DIR/node$node/slotraft-snapshots" \
      --inventory "$EVIDENCE_DIR/snapshot-inventory/$phase-node-$node.tsv" \
      --observed-at "$storage_observed" --run-id "$RUN_ID" --sample "$phase" --node "node-$node" \
      >"$row_file" &
    storage_pids+=("$!")
  done
  for node in 1 2 3; do
    row_file="$EVIDENCE_DIR/snapshot-inventory/$phase-node-$node.row"
    wait_child_uninterrupted "${storage_pids[$((node - 1))]}"
    if (( WAIT_CHILD_STATUS != 0 )) || [[ ! -s "$row_file" ]]; then
      printf '%s\t%s\t%s\tnode-%s\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n' \
        "$storage_observed" "$RUN_ID" "$phase" "$node" >"$row_file"
    fi
    cat "$row_file" >>"$STORAGE_OVERLAP_FILE"
    rm -f "$row_file"
  done
}

capture_product_queue_metrics() {
  local phase="$1" node destination observed status index
  local -a capture_pids=() capture_names=()
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  for node in 1 2 3; do
    destination="$METRICS_DIR/node-$node-$phase.prom"
    capture_metric_target "http://127.0.0.1:$(api_port "$node")/metrics" 3 "$destination" &
    capture_pids+=("$!")
    capture_names+=("node-$node")
  done
  for index in "${!capture_pids[@]}"; do
    status=complete
    wait_child_uninterrupted "${capture_pids[$index]}"
    (( WAIT_CHILD_STATUS == 0 )) || status=missing
    printf '%s\t%s\t%s\t%s\n' "$observed" "$phase" "${capture_names[$index]}" "$status" >>"$EVIDENCE_DIR/timeline.tsv"
  done
}

write_worker_authorization_header() {
  set +x
  printf 'Authorization: Bearer %s\n' "$WK_BENCH_WORKER_TOKEN"
}

capture_graceful_stop_worker_snapshot() {
  local node="$1"
  local raw="$GRACEFUL_STOP_SNAPSHOT_DIR/node-$1.json"
  local entry="$GRACEFUL_STOP_SNAPSHOT_DIR/.node-$1-entry.json"
  local temporary="$GRACEFUL_STOP_SNAPSHOT_DIR/node-$1.json.next.$$"
  local relative="graceful-stop-timeout/node-$1.json" worker_id=$((node - 1))
  set +x
  if curl -fsS --connect-timeout 2 --max-time 4 \
    --header @<(write_worker_authorization_header) \
    "http://127.0.0.1:$(worker_port "$node")/v1/chat-lifecycle/snapshot" >"$temporary" 2>/dev/null &&
    jq -e --arg run_id "$RUN_ID" --argjson worker_id "$worker_id" '
      def uint: type == "number" and . >= 0 and . == floor;
      .run_id == $run_id and .worker_id == $worker_id and
      (.phase == "running" or .phase == "stopping" or .phase == "final") and
      (.sessions.online | uint) and (.sessions.starting | uint) and (.sessions.closing | uint) and
      (.messages.sent | uint) and (.messages.send_acknowledged | uint) and
      (.messages.retry_attempts | uint) and (.messages.terminal | uint) and
      (.correlation.pending_unfinished | uint) and (.correlation.outstanding | uint) and
      (.queues.work_current | uint) and (.queues.retry_current | uint) and
      (.queues.inflight_current | uint) and (.queues.transport_current | uint)
    ' "$temporary" >/dev/null 2>&1; then
    mv "$temporary" "$raw"
    jq --arg node "node-$node" --arg snapshot "$relative" '{
      node:$node, capture_status:"complete", snapshot:$snapshot, phase:.phase,
      sessions:{online:.sessions.online, starting:.sessions.starting, closing:.sessions.closing},
      messages:{sent:.messages.sent, send_acknowledged:.messages.send_acknowledged,
        retry_attempts:.messages.retry_attempts, terminal:.messages.terminal},
      remaining_work:{pending_unfinished:.correlation.pending_unfinished,
        outstanding:.correlation.outstanding, work_current:.queues.work_current,
        retry_current:.queues.retry_current, inflight_current:.queues.inflight_current,
        transport_current:.queues.transport_current}
    }' "$raw" >"$entry"
    return 0
  fi
  rm -f "$temporary"
  jq -n --arg node "node-$node" '{
    node:$node, capture_status:"missing", snapshot:"", phase:"",
    sessions:null, messages:null, remaining_work:null
  }' >"$entry"
  return 1
}

capture_graceful_stop_timeout_evidence() {
  local observed terminal_cut_present=false evidence_complete=false node status
  local -a snapshot_pids=()
  mkdir -p "$GRACEFUL_STOP_SNAPSHOT_DIR"
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [[ -s "$CUT_QUERY_FILE" ]] && jq -e '.terminal_cut_present == true' "$CUT_QUERY_FILE" >/dev/null 2>&1; then
    terminal_cut_present=true
  fi
  for node in 1 2 3; do
    capture_graceful_stop_worker_snapshot "$node" &
    snapshot_pids+=("$!")
  done
  for node in 1 2 3; do
    wait_child_uninterrupted "${snapshot_pids[$((node - 1))]}"
  done
  if jq -s -e 'length == 3 and all(.[]; .capture_status == "complete")' \
    "$GRACEFUL_STOP_SNAPSHOT_DIR"/.node-*-entry.json >/dev/null 2>&1; then
    evidence_complete=true
  fi
  jq -s --arg observed "$observed" --argjson timeout_seconds "$GRACEFUL_STOP_TIMEOUT" \
    --argjson terminal_cut_present "$terminal_cut_present" --argjson evidence_complete "$evidence_complete" '{
      schema:"wukongim/chat-lifecycle-graceful-stop-status/v1", status:"timeout",
      reason:"coordinator_graceful_stop_timeout", observed_at_utc:$observed,
      timeout_seconds:$timeout_seconds, terminal_cut_present:$terminal_cut_present,
      evidence_complete:$evidence_complete, nodes:sort_by(.node)
    }' "$GRACEFUL_STOP_SNAPSHOT_DIR"/.node-*-entry.json >"$GRACEFUL_STOP_STATUS_FILE.next"
  mv "$GRACEFUL_STOP_STATUS_FILE.next" "$GRACEFUL_STOP_STATUS_FILE"
  rm -f "$GRACEFUL_STOP_SNAPSHOT_DIR"/.node-*-entry.json
}

write_graceful_stop_status_if_absent() {
  [[ ! -e "$GRACEFUL_STOP_STATUS_FILE" ]] || return 0
  jq -n '{
    schema:"wukongim/chat-lifecycle-graceful-stop-status/v1", status:"not_triggered",
    reason:"", observed_at_utc:"", timeout_seconds:0, terminal_cut_present:false,
    evidence_complete:true, nodes:[]
  }' >"$GRACEFUL_STOP_STATUS_FILE.next"
  mv "$GRACEFUL_STOP_STATUS_FILE.next" "$GRACEFUL_STOP_STATUS_FILE"
}

record_coordinator_stop_request_failure() {
  local observed terminal_cut_present=false
  HARNESS_FAILURE_REASON=coordinator_exited_before_stop_request
  observed="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  if [[ -s "$CUT_QUERY_FILE" ]] && jq -e '.terminal_cut_present == true' "$CUT_QUERY_FILE" >/dev/null 2>&1; then
    terminal_cut_present=true
  fi
  jq -n --arg observed "$observed" --argjson terminal_cut_present "$terminal_cut_present" '{
    schema:"wukongim/chat-lifecycle-graceful-stop-status/v1", status:"request_failed",
    reason:"coordinator_exited_before_stop_request", observed_at_utc:$observed,
    timeout_seconds:0, terminal_cut_present:$terminal_cut_present,
    evidence_complete:true, nodes:[]
  }' >"$GRACEFUL_STOP_STATUS_FILE.next"
  mv "$GRACEFUL_STOP_STATUS_FILE.next" "$GRACEFUL_STOP_STATUS_FILE"
}

force_stop_and_join_coordinator() {
  local reason="$1" deadline
  if kill -0 "$COORDINATOR_PID" 2>/dev/null; then
    log "$reason; forwarding a final TERM to the coordinator"
    kill -TERM "$COORDINATOR_PID" 2>/dev/null || true
    deadline=$((SECONDS + CLEANUP_TIMEOUT))
    while kill -0 "$COORDINATOR_PID" 2>/dev/null && (( SECONDS < deadline )); do
      sleep 1 || true
    done
    if kill -0 "$COORDINATOR_PID" 2>/dev/null; then
      kill -KILL "$COORDINATOR_PID" 2>/dev/null || true
    fi
  fi
  wait_child_uninterrupted "$COORDINATOR_PID"
  COORDINATOR_STATUS="$WAIT_CHILD_STATUS"
  COORDINATOR_JOINED=1
  mark_recorded_stopped coordinator || true
}

force_stop_timed_out_coordinator() {
  force_stop_and_join_coordinator 'graceful-stop timeout evidence closed'
}

write_product_queue_summary() {
  local phase="$1" output="$RUN_DIR/product_queue_summary.tsv" temporary="$RUN_DIR/product_queue_summary.tsv.next"
  local node baseline_status baseline_queue baseline_inflight drained_status drained_queue drained_inflight cluster_converged all_complete
  local baseline_queue_total=0 baseline_inflight_total=0 drained_queue_total=0 drained_inflight_total=0
  local -a evidence_statuses=() baseline_queues=() baseline_inflights=() drained_queues=() drained_inflights=()
  all_complete=true
  printf 'tag\tnode\tevidence\tbaseline_queue\tbaseline_inflight\tdrained_queue\tdrained_inflight\tconverged\n' >"$temporary"
  for node in 1 2 3; do
    baseline_status=missing
    baseline_queue=0
    baseline_inflight=0
    drained_status=missing
    drained_queue=0
    drained_inflight=0
    read -r baseline_status baseline_queue baseline_inflight < <(
      awk -f "$ROOT_DIR/scripts/product-queue-snapshot.awk" "$METRICS_DIR/node-$node-before.prom" 2>/dev/null || true
    ) || true
    read -r drained_status drained_queue drained_inflight < <(
      awk -f "$ROOT_DIR/scripts/product-queue-snapshot.awk" "$METRICS_DIR/node-$node-$phase.prom" 2>/dev/null || true
    ) || true
    if [[ "$baseline_status" != complete || "$drained_status" != complete ]]; then
      drained_status=missing
      all_complete=false
    fi
    evidence_statuses[$node]="$drained_status"
    baseline_queues[$node]="$baseline_queue"
    baseline_inflights[$node]="$baseline_inflight"
    drained_queues[$node]="$drained_queue"
    drained_inflights[$node]="$drained_inflight"
    baseline_queue_total=$((baseline_queue_total + baseline_queue))
    baseline_inflight_total=$((baseline_inflight_total + baseline_inflight))
    drained_queue_total=$((drained_queue_total + drained_queue))
    drained_inflight_total=$((drained_inflight_total + drained_inflight))
  done
  cluster_converged=false
  if [[ "$all_complete" == true ]] &&
    (( drained_queue_total <= baseline_queue_total && drained_inflight_total <= baseline_inflight_total )); then
    cluster_converged=true
  fi
  for node in 1 2 3; do
    printf 'rate-%s\tnode-%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
      "$SEND_RATE" "$node" "${evidence_statuses[$node]}" "${baseline_queues[$node]}" "${baseline_inflights[$node]}" \
      "${drained_queues[$node]}" "${drained_inflights[$node]}" "$cluster_converged" >>"$temporary"
  done
  mv "$temporary" "$output"
  [[ "$cluster_converged" == true ]]
}

wait_for_product_queue_convergence() {
  local attempt=0 phase deadline
  deadline="$GRACEFUL_STOP_DEADLINE"
  if (( deadline <= SECONDS )); then
    deadline=$((SECONDS + GRACEFUL_STOP_TIMEOUT))
  fi
  while true; do
    attempt=$((attempt + 1))
    phase="product-drain-$attempt"
    capture_product_queue_metrics "$phase"
    if write_product_queue_summary "$phase"; then
      log "post-drain product queues converged after $attempt sample(s)"
      return 0
    fi
    if (( SECONDS >= deadline )); then
      log "post-drain product queues did not converge within ${GRACEFUL_STOP_TIMEOUT}s"
      return 1
    fi
    sleep 1
  done
}

summarize_storage_metrics() {
  local output="$RUN_DIR/storage_metrics_summary.tsv" node before after
  local -a samples files
  awk -v header=1 -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" /dev/null >"$output"
  for node in 1 2 3; do
    before="$METRICS_DIR/node-$node-before.prom"
    after="$METRICS_DIR/node-$node-after.prom"
    samples=("$METRICS_DIR/node-$node-sample-"*.prom)
    files=()
    [[ -f "$before" ]] && files+=("$before")
    if [[ -e "${samples[0]}" ]]; then
      files+=("${samples[@]}")
    fi
    [[ -f "$after" ]] && files+=("$after")
    if [[ "${#files[@]}" -ge 2 ]]; then
      awk -v tag="rate-$SEND_RATE" -v node="node-$node" -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" "${files[@]}" >>"$output"
    else
      awk -v tag="rate-$SEND_RATE" -v node="node-$node" -f "$ROOT_DIR/scripts/storage-metrics-summary.awk" /dev/null /dev/null >>"$output"
    fi
  done
}

summarize_host_io() {
  local output="$RUN_DIR/host_io_summary.tsv" host node
  local -a files
  awk -v header=1 -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >"$output"
  for node in 1 2 3; do
    host="host-node-$node"
    files=("$METRICS_DIR/$host-"*.prom)
    if [[ -e "${files[0]}" ]]; then
      awk -v tag="rate-$SEND_RATE" -v host="$host" -f "$ROOT_DIR/scripts/host-io-summary.awk" "${files[@]}" >>"$output"
    else
      awk -v tag="rate-$SEND_RATE" -v host="$host" -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >>"$output"
    fi
  done
  files=("$METRICS_DIR/host-load-"*.prom)
  if [[ -e "${files[0]}" ]]; then
    awk -v tag="rate-$SEND_RATE" -v host=host-load -f "$ROOT_DIR/scripts/host-io-summary.awk" "${files[@]}" >>"$output"
  else
    awk -v tag="rate-$SEND_RATE" -v host=host-load -f "$ROOT_DIR/scripts/host-io-summary.awk" /dev/null >>"$output"
  fi
}

record_process_continuity() {
  local output="$EVIDENCE_DIR/process-continuity.tsv" name pid alive index
  printf 'name\talive\n' >"$output"
  for name in service-1 service-2 service-3 worker-1 worker-2 worker-3 host-metrics-1 host-metrics-2 host-metrics-3 host-metrics-load process-metrics-collector; do
    alive=false
    for index in "${!NAMES[@]}"; do
      if [[ "${NAMES[$index]}" == "$name" ]]; then
        pid="${PIDS[$index]}"
        kill -0 "$pid" 2>/dev/null && alive=true
        break
      fi
    done
    printf '%s\t%s\n' "$name" "$alive" >>"$output"
  done
}

write_artifact_checksums() {
  local output="$EVIDENCE_DIR/checksums.sha256" path digest
  local -a artifact_roots=(
    "$WUKONGIM_BIN" "$WKBENCH_BIN" "$LIFECYCLE_CONFIG" "$CONFIG_DIR" "$LOG_DIR"
    "$REPORT_DIR" "$EVIDENCE_DIR" "$METRICS_DIR"
  )
  for path in \
    "$RUN_DIR/storage_metrics_summary.tsv" "$RUN_DIR/host_io_summary.tsv" \
    "$RUN_DIR/product_queue_summary.tsv" "$RUN_DIR/local-step.json"; do
    [[ -e "$path" ]] && artifact_roots+=("$path")
  done
  : >"$output"
  while IFS= read -r path; do
    [[ "$path" == "$output" ]] && continue
    digest="$(sha256_file "$path")"
    printf '%s  %s\n' "$digest" "${path#"$RUN_DIR"/}" >>"$output"
  done < <(find "${artifact_roots[@]}" -type f -print | LC_ALL=C sort)
}

finalize_unmeasured_harness_failure() {
  summarize_storage_metrics
  summarize_host_io
  record_process_continuity
  stop_process_metrics_collector
  join_threshold_pprof_capture
  write_threshold_pprof_status
  write_graceful_stop_status_if_absent
  # No measured local-step schema applies to the legacy --stop-after mode.
  # Join every mutable writer and seal the typed wrapper failure plus raw
  # evidence instead of fabricating a zero-duration classifier result.
  stop_recorded_processes
  write_artifact_checksums
  log "unmeasured harness failure sealed without local-step.json: $HARNESS_FAILURE_REASON"
  exit 6
}

log 'starting coordinator'
write_phase_state warmup
record_timeline_boundary warmup_start
capture_service_metrics warmup-before
"$WKBENCH_BIN" soak chat-lifecycle --config "$LIFECYCLE_CONFIG" --output-dir "$REPORT_DIR" \
  >"$LOG_DIR/coordinator.log" 2>&1 &
COORDINATOR_PID=$!
record_pid coordinator "$COORDINATOR_PID"
start_process_metrics_collector

started_at=$SECONDS
qualification_seen=0
measurement_deadline=0
next_metrics_at=0
metrics_sequence=0
while kill -0 "$COORDINATOR_PID" 2>/dev/null; do
  request_pending_operator_stop || true
  if (( MEASURE_SECONDS > 0 && qualification_seen == 0 && STOP_SENT == 0 )) && [[ -s "$REPORT_DIR/qualification.json" ]]; then
    measurement_deadline=$((SECONDS + MEASURE_SECONDS))
    record_timeline_boundary warmup_end
    record_timeline_boundary measurement_start
    write_phase_state measurement
    start_measured_host_overlap_monitor
    capture_service_metrics before
    qualification_seen=1
    next_metrics_at=$((SECONDS + METRICS_SAMPLE_SECONDS))
    log "warmup evidence complete; measuring ${MEASURE_SECONDS}s at ${SEND_RATE} offered SEND/s"
  fi
  if (( qualification_seen == 1 && STOP_SENT == 0 && SECONDS >= measurement_deadline )); then
    # Consume the last complete measured cut before closing admission. This is
    # the final opportunity to begin causal profiles while phase=measurement.
    refresh_live_cut_query || log 'final measured worker-cut query was unavailable'
    record_timeline_boundary measurement_end
    record_timeline_boundary drain_start
    write_phase_state drain
    DRAIN_BOUNDARY_RECORDED=1
    if ! request_coordinator_stop 'measured interval elapsed'; then
      log 'coordinator exited before the measured stop request could be delivered; closing typed harness evidence'
      record_coordinator_stop_request_failure
      force_stop_and_join_coordinator 'coordinator stop-request race recorded'
      break
    fi
    check_measured_host_overlap
  elif (( qualification_seen == 1 && STOP_SENT == 0 && SECONDS >= next_metrics_at &&
    SECONDS + METRICS_SAMPLE_SECONDS * 2 < measurement_deadline )); then
    metrics_sequence=$((metrics_sequence + 1))
    capture_service_metrics "sample-$metrics_sequence"
    next_metrics_at=$((SECONDS + METRICS_SAMPLE_SECONDS))
  fi
  if (( STOP_AFTER > 0 && STOP_SENT == 0 && SECONDS - started_at >= STOP_AFTER )); then
    if ! request_coordinator_stop '--stop-after elapsed'; then
      log 'coordinator exited before the bounded stop request could be delivered; closing typed harness evidence'
      record_coordinator_stop_request_failure
      force_stop_and_join_coordinator 'coordinator stop-request race recorded'
      break
    fi
  fi
  if (( GRACEFUL_STOP_DEADLINE > 0 && SECONDS >= GRACEFUL_STOP_DEADLINE )); then
    HARNESS_FAILURE_REASON=coordinator_graceful_stop_timeout
    log "coordinator did not finish graceful stop within ${GRACEFUL_STOP_TIMEOUT}s; closing typed timeout evidence"
    refresh_live_cut_query || log 'graceful-stop timeout worker-cut query was unavailable'
    capture_graceful_stop_timeout_evidence
    force_stop_timed_out_coordinator
    break
  fi
  close_operator_phase_if_needed
  refresh_live_cut_query || log 'typed live worker-cut query will retry'
  sleep 1 || true
done

coordinator_status=0
if (( COORDINATOR_JOINED == 0 )); then
  wait_child_uninterrupted "$COORDINATOR_PID"
  COORDINATOR_STATUS="$WAIT_CHILD_STATUS"
  COORDINATOR_JOINED=1
  mark_recorded_stopped coordinator || true
fi
coordinator_status="$COORDINATOR_STATUS"
close_operator_phase_if_needed
refresh_live_cut_query || log 'terminal typed worker-cut query was unavailable'
if (( MEASURE_SECONDS == 0 )) && [[ -n "$HARNESS_FAILURE_REASON" ]]; then
  finalize_unmeasured_harness_failure
fi
if (( MEASURE_SECONDS > 0 )); then
  terminal_boundary_closed=0
  if ! close_terminal_drain_boundary; then
    if (( DRAIN_BOUNDARY_RECORDED == 0 )); then
      if (( qualification_seen == 1 )); then
        record_timeline_boundary measurement_end
      else
        record_timeline_boundary warmup_end
      fi
      record_timeline_boundary drain_start
      write_phase_state drain
      DRAIN_BOUNDARY_RECORDED=1
    fi
    log 'typed terminal cut was unavailable; drain/shutdown boundaries remain incomplete'
    write_phase_state shutdown
  else
    terminal_boundary_closed=1
  fi
  check_measured_host_overlap
  if (( terminal_boundary_closed != 0 )) && ! wait_for_service_sample_after_terminal_boundary; then
    log 'wall clock did not advance beyond the exact terminal boundary; timeline will fail closed'
  fi
  metrics_sequence=$((metrics_sequence + 1))
  capture_service_metrics "sample-$metrics_sequence"
  if [[ -n "$HARNESS_FAILURE_REASON" ]]; then
    log 'graceful-stop timeout: capturing one non-converged product queue cut'
    capture_product_queue_metrics product-drain-timeout
    write_product_queue_summary product-drain-timeout || true
  elif (( qualification_seen == 1 )); then
    wait_for_product_queue_convergence || true
  else
    log 'qualification evidence was not reached; capturing one non-converged product queue cut'
    capture_product_queue_metrics product-drain-unqualified
    write_product_queue_summary product-drain-unqualified || true
  fi
  capture_service_metrics after
  summarize_storage_metrics
  summarize_host_io
  record_process_continuity
  stop_process_metrics_collector
  join_threshold_pprof_capture
  timeline_status=0
  build_unified_timeline || timeline_status=$?
  if (( timeline_status != 0 )); then
    log "unified timeline evidence failed closed with status $timeline_status"
    rm -f "$UNIFIED_TIMELINE_JSON" "$UNIFIED_TIMELINE_TSV"
  fi
  write_threshold_pprof_status
  write_graceful_stop_status_if_absent
  classifier_status=0
  classifier_args=(report local-chat-lifecycle-step \
    --before "$REPORT_DIR/qualification.json" \
    --after "$REPORT_DIR/final.json" \
    --storage-summary "$RUN_DIR/storage_metrics_summary.tsv" \
    --host-io-summary "$RUN_DIR/host_io_summary.tsv" \
    --product-queue-summary "$RUN_DIR/product_queue_summary.tsv" \
    --process-continuity "$EVIDENCE_DIR/process-continuity.tsv" \
    --timeline "$UNIFIED_TIMELINE_JSON" \
    --profile-status "$PPROF_STATUS_FILE" \
    --run-id "$RUN_ID" \
    --output "$RUN_DIR/local-step.json" \
    --offered-rate "$SEND_RATE" \
    --measured-duration "${MEASURE_SECONDS}s" \
    --minimum-throughput-percent "$MINIMUM_THROUGHPUT_PERCENT")
  if (( OPERATOR_SIGNAL_STATUS != 0 )); then
    classifier_args+=(--operator-interrupted)
  fi
  if [[ -n "$HARNESS_FAILURE_REASON" ]]; then
    classifier_args+=(--harness-failure-reason "$HARNESS_FAILURE_REASON")
  fi
  if (( HOST_CONFOUNDED != 0 )); then
    classifier_args+=(--host-confounded)
  fi
  "$WKBENCH_BIN" "${classifier_args[@]}" || classifier_status=$?
  # The coordinator, service, worker, and sampler processes all own files under
  # logs/. Join every writer before computing the immutable step seal.
  stop_recorded_processes
  write_artifact_checksums
  if [[ -s "$RUN_DIR/local-step.json" ]]; then
    log "local diagnostic result: $RUN_DIR/local-step.json (status $classifier_status)"
    if (( OPERATOR_SIGNAL_STATUS != 0 )) && [[ -z "$HARNESS_FAILURE_REASON" ]]; then
      log "operator signal completed bounded evidence sealing; propagating status $OPERATOR_SIGNAL_STATUS"
      exit "$OPERATOR_SIGNAL_STATUS"
    fi
    exit "$classifier_status"
  fi
  die "local diagnostic classifier did not write local-step.json; coordinator status $coordinator_status"
fi
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
