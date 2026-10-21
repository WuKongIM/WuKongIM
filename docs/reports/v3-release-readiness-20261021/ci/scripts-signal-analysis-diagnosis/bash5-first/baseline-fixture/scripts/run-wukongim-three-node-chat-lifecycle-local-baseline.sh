#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHAKEOUT="$ROOT_DIR/scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh"
RUN_DIR=""
BASE_PORT=15000
READY_TIMEOUT=120
RATES="250,500,750,1000"
STEP_MEASURE_SECONDS=300
SOAK_RATE=1000
SOAK_MEASURE_SECONDS=600
WARMUP_SECONDS=60
DRAIN_TIMEOUT=90
MINIMUM_FREE_PERCENT=10
DRY_RUN=0
OBSERVED_FREE_PERCENT=0

usage() {
  sed -n '/^# Usage:/,/^#   -h, --help/p' "$0" | sed 's/^# \{0,1\}//'
}

# Usage: run-wukongim-three-node-chat-lifecycle-local-baseline.sh --run-dir DIR [options]
#
# Runs the reviewed three-node shared-storage SEND-rate staircase. Every fixed
# rate uses a fresh process generation. All four rates must pass before a fresh
# 1,000 SEND/s generation runs the required ten-minute qualification soak.
#
# Options:
#   --run-dir DIR       Required fresh root for all rate-step evidence.
#   --base-port PORT    First port in the reusable 64-port range (default 15000).
#   --ready-timeout S   Per-step readiness deadline (default 120).
#   --dry-run           Print the reviewed plan without creating files.
#   -h, --help          Show this help.

log() { printf '[chat-lifecycle-local-baseline] %s\n' "$*"; }
die() { printf '[chat-lifecycle-local-baseline] ERROR: %s\n' "$*" >&2; exit 1; }

require_uint() {
  local name="$1" value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer"
}

absolute_path() {
  local value="$1" parent base
  [[ "$value" == /* ]] || value="$PWD/$value"
  while [[ "$value" != / && "$value" == */ ]]; do value="${value%/}"; done
  parent="$(dirname "$value")"
  base="$(basename "$value")"
  [[ -d "$parent" ]] || die "--run-dir parent does not exist: $parent"
  parent="$(cd "$parent" && pwd -P)"
  printf '%s/%s' "$parent" "$base"
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --run-dir) [[ $# -ge 2 ]] || die '--run-dir requires a value'; RUN_DIR="$2"; shift 2 ;;
    --base-port) [[ $# -ge 2 ]] || die '--base-port requires a value'; BASE_PORT="$2"; shift 2 ;;
    --ready-timeout) [[ $# -ge 2 ]] || die '--ready-timeout requires a value'; READY_TIMEOUT="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1" ;;
  esac
done

[[ -n "$RUN_DIR" ]] || die '--run-dir is required'
RUN_DIR="$(absolute_path "$RUN_DIR")"
require_uint '--base-port' "$BASE_PORT"
require_uint '--ready-timeout' "$READY_TIMEOUT"
(( BASE_PORT >= 1024 && BASE_PORT <= 65472 )) || die '--base-port must reserve 64 ports within 1024..65535'
(( READY_TIMEOUT > 0 )) || die '--ready-timeout must be greater than zero'
case "$RUN_DIR" in /|"$ROOT_DIR"|"$(cd "$HOME" && pwd -P)") die "unsafe --run-dir: $RUN_DIR" ;; esac

if (( DRY_RUN == 1 )); then
  printf 'run_dir=%s\n' "$RUN_DIR"
  printf 'base_port=%s\n' "$BASE_PORT"
  printf 'rates=%s\n' "$RATES"
  printf 'step_measure_seconds=%s\n' "$STEP_MEASURE_SECONDS"
  printf 'soak_rate=%s\n' "$SOAK_RATE"
  printf 'soak_measure_seconds=%s\n' "$SOAK_MEASURE_SECONDS"
  printf 'warmup_seconds=%s\n' "$WARMUP_SECONDS"
  printf 'drain_timeout_seconds=%s\n' "$DRAIN_TIMEOUT"
  printf 'minimum_filesystem_free_percent=%s\n' "$MINIMUM_FREE_PERCENT"
  exit 0
fi

[[ ! -e "$RUN_DIR" ]] || die "--run-dir must be absent: $RUN_DIR"
mkdir -p "$RUN_DIR/steps"
STEPS_FILE="$RUN_DIR/steps.tsv"
RESULT_FILE="$RUN_DIR/local-baseline.json"
printf 'phase\trate_per_second\tmeasured_seconds\toutcome\texit_status\tartifact\n' >"$STEPS_FILE"
df -Pk "$(dirname "$RUN_DIR")" >"$RUN_DIR/filesystem-preflight.txt" || true
STEP_ARTIFACT_SEALS_COMPLETE=true
ALL_STEP_SOURCES_REBUILDABLE=true
VALIDATED_STEP_ARTIFACT_SEALS=0
OPERATOR_SIGNAL_STATUS=0
OPERATOR_SIGNAL_NAME=""
ACTIVE_STEP_PID=""
ACTIVE_STEP_SIGNAL_FORWARDED=0

write_result() {
  local outcome="$1" reason="$2" highest="$3" first_failing="$4" revision dirty rebuildable
  if (( OPERATOR_SIGNAL_STATUS != 0 )); then
    outcome=insufficient_evidence
    reason=operator_interrupted
  fi
  revision="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)"
  dirty=false
  if ! git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD -- 2>/dev/null ||
    [[ -n "$(git -C "$ROOT_DIR" ls-files --others --exclude-standard 2>/dev/null)" ]]; then
    dirty=true
  fi
  rebuildable=false
  if (( VALIDATED_STEP_ARTIFACT_SEALS > 0 )); then
    rebuildable="$ALL_STEP_SOURCES_REBUILDABLE"
  elif [[ "$revision" != unknown && "$dirty" == false ]]; then
    rebuildable=true
  fi
  {
    printf '{\n'
    printf '  "schema": "wukongim/chat-lifecycle-local-baseline/v1",\n'
    printf '  "outcome": "%s",\n' "$outcome"
    printf '  "reason": "%s",\n' "$reason"
    printf '  "highest_clean_rate": %s,\n' "$highest"
    printf '  "first_failing_rate": %s,\n' "$first_failing"
    printf '  "online_connections": 2500,\n'
    printf '  "rate_staircase": "%s",\n' "$RATES"
    printf '  "step_measured_seconds": %s,\n' "$STEP_MEASURE_SECONDS"
    printf '  "soak_rate": %s,\n' "$SOAK_RATE"
    printf '  "soak_measured_seconds": %s,\n' "$SOAK_MEASURE_SECONDS"
    printf '  "warmup_seconds": %s,\n' "$WARMUP_SECONDS"
    printf '  "drain_timeout_seconds": %s,\n' "$DRAIN_TIMEOUT"
    printf '  "logical_slot_groups": 12,\n'
    printf '  "hash_slots": 256,\n'
    printf '  "slot_replicas": 3,\n'
    printf '  "channel_replicas": 3,\n'
    printf '  "gateway_async_send_workers": 1000,\n'
    printf '  "gateway_async_send_batch_max_records": 1,\n'
    printf '  "commit_coordinator_flush_window": "500us",\n'
    printf '  "commit_coordinator_shards": 1,\n'
    printf '  "sync_commit": true,\n'
    printf '  "minimum_filesystem_free_percent": %s,\n' "$MINIMUM_FREE_PERCENT"
    printf '  "observed_filesystem_free_percent": %s,\n' "$OBSERVED_FREE_PERCENT"
    printf '  "source_revision": "%s",\n' "$revision"
    printf '  "source_dirty": %s,\n' "$dirty"
    printf '  "source_rebuildable_from_revision": %s,\n' "$rebuildable"
    if (( OPERATOR_SIGNAL_STATUS != 0 )); then
      printf '  "operator_interrupted": true,\n'
    else
      printf '  "operator_interrupted": false,\n'
    fi
    printf '  "step_artifact_seals_complete": %s,\n' "$STEP_ARTIFACT_SEALS_COMPLETE"
    printf '  "validated_step_artifact_seals": %s,\n' "$VALIDATED_STEP_ARTIFACT_SEALS"
    printf '  "filesystem_preflight": "filesystem-preflight.txt",\n'
    printf '  "steps": "steps.tsv",\n'
    printf '  "artifact_checksums": "checksums.sha256"\n'
    printf '}\n'
  } >"$RESULT_FILE"
  write_artifact_checksums
  log "result: $RESULT_FILE"
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
  else
    return 1
  fi
}

write_artifact_checksums() {
  local output="$RUN_DIR/checksums.sha256" path digest
  : >"$output"
  while IFS= read -r path; do
    [[ "$path" == "$output" ]] && continue
    digest="$(sha256_file "$path")"
    printf '%s  %s\n' "$digest" "${path#"$RUN_DIR"/}" >>"$output"
  done < <(find "$RUN_DIR" -type f -print | LC_ALL=C sort)
}

overlap="$(ps -axo pid=,comm= | awk -v self="$$" '
  {
    command = $2
    sub(/^.*\//, "", command)
    if ($1 != self && (command == "wukongim" || command == "wkcli")) print $0
  }
')"
if [[ -n "$overlap" ]]; then
  write_result host_confounded overlapping_wukongim_workload 0 0
  exit 2
fi

read -r filesystem_blocks filesystem_available < <(awk 'NR == 2 {print $2, $4}' "$RUN_DIR/filesystem-preflight.txt")
if [[ -z "${filesystem_blocks:-}" || "$filesystem_blocks" -le 0 ]]; then
  write_result insufficient_evidence filesystem_preflight_unavailable 0 0
  exit 6
fi
free_percent=$((filesystem_available * 100 / filesystem_blocks))
OBSERVED_FREE_PERCENT="$free_percent"
if (( free_percent < MINIMUM_FREE_PERCENT )); then
  write_result storage_confounded filesystem_free_below_10_percent 0 0
  exit 2
fi

[[ -n "${WK_BENCH_API_TOKEN:-}" ]] || die 'WK_BENCH_API_TOKEN is required'
[[ -n "${WK_BENCH_WORKER_TOKEN:-}" ]] || die 'WK_BENCH_WORKER_TOKEN is required'

STEP_OUTCOME=""
STEP_STATUS=0

read_typed_step_outcome() {
  local step_dir="$1" expected_rate="$2" expected_measured="$3"
  local result="$step_dir/local-step.json"
  [[ -s "$result" ]] || return 1
  jq -er --argjson expected_rate "$expected_rate" --argjson expected_measured "$expected_measured" '
    def uint: type == "number" and . >= 0 and . == floor;
    select(
      type == "object" and
      .schema == "wukongim/chat-lifecycle-local-step/v1" and
      (.outcome == "clean" or .outcome == "storage_confounded" or
       .outcome == "host_confounded" or .outcome == "rate_failed" or
       .outcome == "product_failure" or .outcome == "insufficient_evidence") and
      (.reason | type == "string" and length > 0) and
      (.offered_rate_per_second | uint and . == $expected_rate) and
      (.actual_rate_per_second | type == "number" and . >= 0) and
      (.minimum_throughput_percent | uint and . > 0 and . <= 100) and
      (.measured_duration_seconds | uint and . == $expected_measured) and
      (.qualification_reached | type == "boolean") and
      (.target_connections | uint) and
      (.online_connections | uint) and
      (.sent | uint) and
      (.acknowledged | uint) and
      (.expected | uint) and
      (.minimum_filesystem_free_percent | type == "number" and . >= 0 and . <= 100) and
      (.storage_evidence_complete | type == "boolean") and
      (.host_io_evidence_complete | type == "boolean") and
      (.product_metrics_complete | type == "boolean") and
      (.product_queue_evidence_complete | type == "boolean") and
      (.product_queues_converged | type == "boolean") and
      (.process_continuity_complete | type == "boolean") and
      (.timeline_evidence_complete | type == "boolean") and
      (.profile_evidence_complete | type == "boolean") and
      (.operator_interrupted | type == "boolean") and
      (.harness_failure_reason == "" or
       .harness_failure_reason == "coordinator_graceful_stop_timeout" or
       .harness_failure_reason == "coordinator_exited_before_stop_request")
    ) | .outcome
  ' "$result"
}

verify_step_artifact_checksums() {
  local step_dir="$1" manifest="$1/evidence/checksums.sha256"
  local line digest relative path actual entries=0 result_covered=false timeline_json_covered=false
  local timeline_tsv_covered=false storage_overlap_covered=false raw_cuts_covered=false profile_status_covered=false profile_metadata_covered=false
  local graceful_stop_status_covered=false
  local wrapper_status node kind blob_status blob_relative required identity schema source_revision source_dirty source_rebuildable source_capture
  local config_sha wukongim_sha wkbench_sha sealed_config_sha sealed_wukongim_sha sealed_wkbench_sha
  local harness_failure_reason graceful_stop_status snapshot_status snapshot_relative snapshot_path
  STEP_SOURCE_REBUILDABLE=false
  [[ -s "$manifest" && -f "$manifest" && ! -L "$manifest" ]] || return 1
  while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^([0-9a-f]{64})[[:space:]][[:space:]](.+)$ ]] || return 1
    digest="${BASH_REMATCH[1]}"
    relative="${BASH_REMATCH[2]}"
    case "$relative" in
      /*|..|../*|*/..|*/../*) return 1 ;;
    esac
    path="$step_dir/$relative"
    [[ -f "$path" && ! -L "$path" ]] || return 1
    actual="$(sha256_file "$path")" || return 1
    [[ "$actual" == "$digest" ]] || return 1
    entries=$((entries + 1))
    if [[ "$relative" == local-step.json ]]; then
      result_covered=true
    fi
    case "$relative" in
      evidence/unified-timeline.json) timeline_json_covered=true ;;
      evidence/unified-timeline.tsv) timeline_tsv_covered=true ;;
      evidence/storage-overlap.tsv) storage_overlap_covered=true ;;
      evidence/coordinator-worker-cuts.log) raw_cuts_covered=true ;;
      evidence/threshold-pprof-status.json) profile_status_covered=true ;;
      evidence/threshold-pprof/metadata.json) profile_metadata_covered=true ;;
      evidence/graceful-stop-status.json) graceful_stop_status_covered=true ;;
    esac
  done <"$manifest"
  (( entries > 0 )) && [[ "$result_covered" == true && "$timeline_json_covered" == true &&
    "$timeline_tsv_covered" == true && "$storage_overlap_covered" == true && "$raw_cuts_covered" == true && "$profile_status_covered" == true &&
    "$graceful_stop_status_covered" == true ]] || return 1
  for required in \
    chat-lifecycle.yaml bin/wukongim bin/wkcli \
    config/node1.toml config/node2.toml config/node3.toml \
    logs/coordinator.log logs/service-1.log logs/service-2.log logs/service-3.log \
    logs/worker-1.log logs/worker-2.log logs/worker-3.log \
    logs/host-metrics-1.log logs/host-metrics-2.log logs/host-metrics-3.log \
    logs/host-metrics-load.log logs/process-metrics.log evidence/identity.tsv; do
    awk -v expected="$required" '$2 == expected { found = 1 } END { exit !found }' "$manifest" || return 1
  done
  identity="$step_dir/evidence/identity.tsv"
  schema="$(awk -F '\t' '$1 == "schema" { print $2 }' "$identity")"
  source_revision="$(awk -F '\t' '$1 == "source_revision" { print $2 }' "$identity")"
  source_dirty="$(awk -F '\t' '$1 == "source_dirty" { print $2 }' "$identity")"
  source_rebuildable="$(awk -F '\t' '$1 == "source_rebuildable_from_revision" { print $2 }' "$identity")"
  source_capture="$(awk -F '\t' '$1 == "source_capture" { print $2 }' "$identity")"
  config_sha="$(awk -F '\t' '$1 == "config_sha256" { print $2 }' "$identity")"
  wukongim_sha="$(awk -F '\t' '$1 == "wukongim_binary_sha256" { print $2 }' "$identity")"
  wkbench_sha="$(awk -F '\t' '$1 == "wkbench_binary_sha256" { print $2 }' "$identity")"
  sealed_config_sha="$(awk '$2 == "chat-lifecycle.yaml" { print $1 }' "$manifest")"
  sealed_wukongim_sha="$(awk '$2 == "bin/wukongim" { print $1 }' "$manifest")"
  sealed_wkbench_sha="$(awk '$2 == "bin/wkcli" { print $1 }' "$manifest")"
  [[ "$schema" == wukongim/chat-lifecycle-local-evidence/v1 && -n "$source_revision" ]] || return 1
  case "$source_dirty:$source_rebuildable:$source_capture" in
    false:true:git_revision) STEP_SOURCE_REBUILDABLE=true ;;
    true:false:binary_identity_only|false:false:binary_identity_only) ;;
    *) return 1 ;;
  esac
  [[ "$config_sha" == "$sealed_config_sha" && "$wukongim_sha" == "$sealed_wukongim_sha" &&
    "$wkbench_sha" == "$sealed_wkbench_sha" ]] || return 1
  harness_failure_reason="$(jq -er '.harness_failure_reason' "$step_dir/local-step.json")" || return 1
  graceful_stop_status="$step_dir/evidence/graceful-stop-status.json"
  case "$harness_failure_reason" in
    "")
      jq -e '
        .schema == "wukongim/chat-lifecycle-graceful-stop-status/v1" and
        .status == "not_triggered" and .reason == "" and .observed_at_utc == "" and
        .timeout_seconds == 0 and .terminal_cut_present == false and
        .evidence_complete == true and .nodes == []
      ' "$graceful_stop_status" >/dev/null 2>&1 || return 1
      ;;
    coordinator_graceful_stop_timeout)
      jq -e '
        def uint: type == "number" and . >= 0 and . == floor;
        .schema == "wukongim/chat-lifecycle-graceful-stop-status/v1" and
        .status == "timeout" and .reason == "coordinator_graceful_stop_timeout" and
        (.observed_at_utc | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
        (.timeout_seconds | uint and . > 0) and (.terminal_cut_present | type == "boolean") and
        (.evidence_complete | type == "boolean") and (.nodes | type == "array" and length == 3) and
        (.evidence_complete == ([.nodes[].capture_status == "complete"] | all)) and
        [.nodes[].node] == ["node-1","node-2","node-3"] and
        all(.nodes[];
          (.capture_status == "complete" or .capture_status == "missing") and
          (if .capture_status == "complete" then
             .snapshot == ("graceful-stop-timeout/" + .node + ".json") and
             (.phase == "running" or .phase == "stopping" or .phase == "final") and
             (.sessions | type == "object" and keys == ["closing","online","starting"]) and
             (.sessions.online | uint) and (.sessions.starting | uint) and (.sessions.closing | uint) and
             (.messages | type == "object" and keys == ["retry_attempts","send_acknowledged","sent","terminal"]) and
             (.messages.sent | uint) and (.messages.send_acknowledged | uint) and
             (.messages.retry_attempts | uint) and (.messages.terminal | uint) and
             (.remaining_work | type == "object" and keys == ["inflight_current","outstanding","pending_unfinished","retry_current","transport_current","work_current"]) and
             (.remaining_work.pending_unfinished | uint) and (.remaining_work.outstanding | uint) and
             (.remaining_work.work_current | uint) and (.remaining_work.retry_current | uint) and
             (.remaining_work.inflight_current | uint) and (.remaining_work.transport_current | uint)
          else .snapshot == "" and .phase == "" and .sessions == null and
             .messages == null and .remaining_work == null end))
      ' "$graceful_stop_status" >/dev/null 2>&1 || return 1
      for node in 1 2 3; do
        snapshot_status="$(jq -er --arg node "node-$node" '.nodes[] | select(.node == $node) | .capture_status' \
          "$graceful_stop_status")" || return 1
        snapshot_relative="$(jq -er --arg node "node-$node" '.nodes[] | select(.node == $node) | .snapshot' \
          "$graceful_stop_status")" || return 1
        if [[ "$snapshot_status" == complete ]]; then
          [[ "$snapshot_relative" == "graceful-stop-timeout/node-$node.json" ]] || return 1
          snapshot_path="$step_dir/evidence/$snapshot_relative"
          [[ -s "$snapshot_path" && -f "$snapshot_path" && ! -L "$snapshot_path" ]] || return 1
          awk -v expected="evidence/$snapshot_relative" '$2 == expected { found = 1 } END { exit !found }' \
            "$manifest" || return 1
          jq -e --arg run_id local-chat-lifecycle-shakeout --argjson worker_id "$((node - 1))" '
            def uint: type == "number" and . >= 0 and . == floor;
            .run_id == $run_id and .worker_id == $worker_id and
            (.phase == "running" or .phase == "stopping" or .phase == "final") and
            (.sessions.online | uint) and (.sessions.starting | uint) and (.sessions.closing | uint) and
            (.messages.sent | uint) and (.messages.send_acknowledged | uint) and
            (.messages.retry_attempts | uint) and (.messages.terminal | uint) and
            (.correlation.pending_unfinished | uint) and (.correlation.outstanding | uint) and
            (.queues.work_current | uint) and (.queues.retry_current | uint) and
            (.queues.inflight_current | uint) and (.queues.transport_current | uint)
          ' "$snapshot_path" >/dev/null 2>&1 || return 1
          jq -e --arg node "node-$node" --slurpfile snapshot "$snapshot_path" '
            (.nodes[] | select(.node == $node)) as $status | $snapshot[0] as $raw |
            $status.phase == $raw.phase and
            $status.sessions == {online:$raw.sessions.online, starting:$raw.sessions.starting, closing:$raw.sessions.closing} and
            $status.messages == {sent:$raw.messages.sent, send_acknowledged:$raw.messages.send_acknowledged,
              retry_attempts:$raw.messages.retry_attempts, terminal:$raw.messages.terminal} and
            $status.remaining_work == {pending_unfinished:$raw.correlation.pending_unfinished,
              outstanding:$raw.correlation.outstanding, work_current:$raw.queues.work_current,
              retry_current:$raw.queues.retry_current, inflight_current:$raw.queues.inflight_current,
              transport_current:$raw.queues.transport_current}
          ' "$graceful_stop_status" >/dev/null 2>&1 || return 1
        elif [[ -n "$snapshot_relative" ]]; then
          return 1
        fi
      done
      ;;
    coordinator_exited_before_stop_request)
      jq -e '
        .schema == "wukongim/chat-lifecycle-graceful-stop-status/v1" and
        .status == "request_failed" and .reason == "coordinator_exited_before_stop_request" and
        (.observed_at_utc | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T")) and
        .timeout_seconds == 0 and (.terminal_cut_present | type == "boolean") and
        .evidence_complete == true and .nodes == []
      ' "$graceful_stop_status" >/dev/null 2>&1 || return 1
      ;;
    *) return 1 ;;
  esac
  wrapper_status="$(jq -r '.status // ""' "$step_dir/evidence/threshold-pprof-status.json" 2>/dev/null)"
  if [[ "$wrapper_status" != not_triggered ]]; then
    [[ "$profile_metadata_covered" == true ]] || return 1
    for node in 1 2 3; do
      for kind in cpu heap goroutine; do
        blob_status="$(jq -er --arg node "node-$node" --arg kind "$kind" \
          '.nodes[] | select(.node == $node) | .[$kind]' \
          "$step_dir/evidence/threshold-pprof/metadata.json" 2>/dev/null)" || return 1
        case "$kind" in
          cpu) blob_relative="evidence/threshold-pprof/profiles/node-$node-cpu.pb.gz" ;;
          heap) blob_relative="evidence/threshold-pprof/profiles/node-$node-heap.pb.gz" ;;
          goroutine) blob_relative="evidence/threshold-pprof/profiles/node-$node-goroutine.txt" ;;
        esac
        case "$blob_status" in
          complete)
            [[ -s "$step_dir/$blob_relative" && -f "$step_dir/$blob_relative" && ! -L "$step_dir/$blob_relative" ]] || return 1
            awk -v expected="$blob_relative" '$2 == expected { found = 1 } END { exit !found }' "$manifest" || return 1
            ;;
          missing) [[ ! -e "$step_dir/$blob_relative" ]] || return 1 ;;
          *) return 1 ;;
        esac
      done
    done
  elif [[ "$profile_metadata_covered" == true || -e "$step_dir/evidence/threshold-pprof/metadata.json" ]]; then
    return 1
  fi
}

prune_step_runtime_state() {
  local step_dir="$1" marker="$1/runtime-state-pruned.txt"
  case "$step_dir" in
    "$RUN_DIR"/steps/*) ;;
    *) die "refusing to prune runtime state outside baseline steps: $step_dir" ;;
  esac
  # A clean revision can reproduce the sealed binaries, node databases, and
  # worker state. Keep reports, logs, raw metrics, normalized summaries,
  # configuration, and evidence for later diagnosis.
  for path in "$step_dir/bin" "$step_dir/data" "$step_dir/workers"; do
    if [[ -d "$path" ]]; then
      find "$path" -xdev -depth -delete
    fi
  done
  printf 'pruned=bin,data,workers\nretained=report,logs,metrics,evidence,config,summaries\n' >"$marker"
}

forward_operator_signal_to_active_step() {
  (( OPERATOR_SIGNAL_STATUS != 0 && ACTIVE_STEP_SIGNAL_FORWARDED == 0 )) || return 0
  [[ -n "$ACTIVE_STEP_PID" ]] && kill -0 "$ACTIVE_STEP_PID" 2>/dev/null || return 0
  # Fence the forwarding attempt before kill so another delivered signal can
  # never send a second signal to the child while it seals its own evidence.
  ACTIVE_STEP_SIGNAL_FORWARDED=1
  log "forwarding one $OPERATOR_SIGNAL_NAME to active step $ACTIVE_STEP_PID"
  kill -s "$OPERATOR_SIGNAL_NAME" "$ACTIVE_STEP_PID" 2>/dev/null || true
}

handle_operator_signal() {
  local signal_name="$1" exit_status="$2"
  if (( OPERATOR_SIGNAL_STATUS == 0 )); then
    OPERATOR_SIGNAL_NAME="$signal_name"
    OPERATOR_SIGNAL_STATUS="$exit_status"
    log "received $signal_name; waiting for the active step to seal and exit"
    forward_operator_signal_to_active_step
    return
  fi
  log "received another operator signal ($signal_name); forwarding it while retaining the first status and join"
  if [[ -n "$ACTIVE_STEP_PID" ]] && kill -0 "$ACTIVE_STEP_PID" 2>/dev/null; then
    kill -s "$signal_name" "$ACTIVE_STEP_PID" 2>/dev/null || true
  fi
}

wait_active_step_uninterrupted() {
  local pid="$1" status=0
  while true; do
    if wait "$pid"; then
      status=0
    else
      status=$?
    fi
    if kill -0 "$pid" 2>/dev/null; then
      # A trapped parent signal interrupts wait before the child has completed
      # its bounded finalization. Re-enter wait until the exact PID is reaped.
      continue
    fi
    return "$status"
  done
}

exit_with_operator_status() {
  local status="$1"
  if (( OPERATOR_SIGNAL_STATUS != 0 )); then
    exit "$OPERATOR_SIGNAL_STATUS"
  fi
  exit "$status"
}

trap 'handle_operator_signal INT 130' INT
trap 'handle_operator_signal TERM 143' TERM

run_step() {
  local phase="$1" rate="$2" measured="$3"
  local step_dir="$RUN_DIR/steps/${phase}-rate-$rate" status=0 outcome step_valid=true
  log "$phase step: ${rate} offered SEND/s for ${measured}s"
  if (( OPERATOR_SIGNAL_STATUS == 0 )); then
    ACTIVE_STEP_SIGNAL_FORWARDED=0
    "$SHAKEOUT" --run-dir "$step_dir" --base-port "$BASE_PORT" --ready-timeout "$READY_TIMEOUT" \
      --send-rate "$rate" --measure-seconds "$measured" --warmup-seconds "$WARMUP_SECONDS" \
      --drain-timeout "$DRAIN_TIMEOUT" >"$RUN_DIR/steps/${phase}-rate-$rate.log" 2>&1 &
    ACTIVE_STEP_PID=$!
    # Close the launch/assignment race if the signal trap ran immediately
    # after bash created the child but before ACTIVE_STEP_PID was assigned.
    forward_operator_signal_to_active_step
    wait_active_step_uninterrupted "$ACTIVE_STEP_PID" || status=$?
    ACTIVE_STEP_PID=""
  fi
  if (( OPERATOR_SIGNAL_STATUS != 0 )); then
    outcome=insufficient_evidence
    status=6
    step_valid=false
  elif ! outcome="$(read_typed_step_outcome "$step_dir" "$rate" "$measured" 2>/dev/null)"; then
    outcome=insufficient_evidence
    status=6
    step_valid=false
  fi
  case "$outcome:$status" in
    clean:0|storage_confounded:2|host_confounded:2|rate_failed:3|product_failure:3|insufficient_evidence:6) ;;
    *) outcome=insufficient_evidence; status=6; step_valid=false ;;
  esac
  if [[ "$step_valid" == true ]] && ! verify_step_artifact_checksums "$step_dir"; then
    outcome=insufficient_evidence
    status=6
    step_valid=false
  fi
  if [[ "$step_valid" == true ]]; then
    VALIDATED_STEP_ARTIFACT_SEALS=$((VALIDATED_STEP_ARTIFACT_SEALS + 1))
    if [[ "$STEP_SOURCE_REBUILDABLE" != true ]]; then
      ALL_STEP_SOURCES_REBUILDABLE=false
      log "step source is not rebuildable from its revision; preserving sealed runtime state in $step_dir"
    else
      prune_step_runtime_state "$step_dir"
    fi
  else
    STEP_ARTIFACT_SEALS_COMPLETE=false
    log "step result validation failed; preserving runtime state in $step_dir"
  fi
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$rate" "$measured" "$outcome" "$status" "steps/${phase}-rate-$rate" >>"$STEPS_FILE"
  STEP_OUTCOME="$outcome"
  STEP_STATUS="$status"
}

highest_clean_rate=0
first_failing_rate=0
IFS=',' read -r -a reviewed_rates <<<"$RATES"
for rate in "${reviewed_rates[@]}"; do
  run_step step "$rate" "$STEP_MEASURE_SECONDS"
  case "$STEP_OUTCOME" in
    clean)
      highest_clean_rate="$rate"
      ;;
    rate_failed|product_failure)
      first_failing_rate="$rate"
      write_result "$STEP_OUTCOME" required_staircase_step_failed "$highest_clean_rate" "$first_failing_rate"
      exit_with_operator_status 3
      ;;
    storage_confounded|host_confounded)
      write_result "$STEP_OUTCOME" step_confounded "$highest_clean_rate" "$rate"
      exit_with_operator_status 2
      ;;
    *)
      write_result insufficient_evidence step_evidence_incomplete "$highest_clean_rate" "$rate"
      exit_with_operator_status 6
      ;;
  esac
done

if (( highest_clean_rate != SOAK_RATE )); then
  write_result insufficient_evidence reviewed_staircase_incomplete "$highest_clean_rate" 0
  exit_with_operator_status 6
fi

run_step soak "$SOAK_RATE" "$SOAK_MEASURE_SECONDS"
if [[ "$STEP_OUTCOME" != clean ]]; then
  case "$STEP_OUTCOME" in
    storage_confounded|host_confounded)
      write_result "$STEP_OUTCOME" required_1000_soak_confounded "$highest_clean_rate" "$SOAK_RATE"
      exit_with_operator_status 2
      ;;
    insufficient_evidence)
      write_result insufficient_evidence required_1000_soak_evidence_incomplete "$highest_clean_rate" "$SOAK_RATE"
      exit_with_operator_status 6
      ;;
  esac
  first_failing_rate="$SOAK_RATE"
  write_result "$STEP_OUTCOME" required_1000_soak_failed "$highest_clean_rate" "$first_failing_rate"
  exit_with_operator_status 3
fi

write_result clean required_1000_soak_passed "$highest_clean_rate" "$first_failing_rate"
exit_with_operator_status 0
