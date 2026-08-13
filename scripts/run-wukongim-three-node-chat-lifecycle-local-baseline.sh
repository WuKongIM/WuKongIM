#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
SHAKEOUT="$ROOT_DIR/scripts/run-wukongim-three-node-chat-lifecycle-shakeout.sh"
RUN_DIR=""
BASE_PORT=15000
READY_TIMEOUT=120
RATES="100,150,250,400,500,750,1000"
SEARCH_MEASURE_SECONDS=120
REPEAT_MEASURE_SECONDS=600
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
# Runs the reviewed three-node shared-storage SEND-rate staircase. It stops at
# the first failed rate, refines the last passing interval, and repeats the
# highest clean rate for ten measured minutes before recording the local knee.
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
  printf 'search_measure_seconds=%s\n' "$SEARCH_MEASURE_SECONDS"
  printf 'repeat_measure_seconds=%s\n' "$REPEAT_MEASURE_SECONDS"
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

write_result() {
  local outcome="$1" reason="$2" highest="$3" first_failing="$4" revision dirty
  revision="$(git -C "$ROOT_DIR" rev-parse HEAD 2>/dev/null || printf unknown)"
  dirty=false
  if ! git -C "$ROOT_DIR" diff --quiet --ignore-submodules HEAD -- 2>/dev/null ||
    [[ -n "$(git -C "$ROOT_DIR" ls-files --others --exclude-standard 2>/dev/null)" ]]; then
    dirty=true
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
    printf '  "search_measured_seconds": %s,\n' "$SEARCH_MEASURE_SECONDS"
    printf '  "repeat_measured_seconds": %s,\n' "$REPEAT_MEASURE_SECONDS"
    printf '  "warmup_seconds": %s,\n' "$WARMUP_SECONDS"
    printf '  "drain_timeout_seconds": %s,\n' "$DRAIN_TIMEOUT"
    printf '  "logical_slot_groups": 12,\n'
    printf '  "hash_slots": 256,\n'
    printf '  "slot_replicas": 3,\n'
    printf '  "channel_replicas": 3,\n'
    printf '  "commit_coordinator_flush_window": "200us",\n'
    printf '  "commit_coordinator_shards": 1,\n'
    printf '  "sync_commit": true,\n'
    printf '  "minimum_filesystem_free_percent": %s,\n' "$MINIMUM_FREE_PERCENT"
    printf '  "observed_filesystem_free_percent": %s,\n' "$OBSERVED_FREE_PERCENT"
    printf '  "source_revision": "%s",\n' "$revision"
    printf '  "source_dirty": %s,\n' "$dirty"
    printf '  "filesystem_preflight": "filesystem-preflight.txt",\n'
    printf '  "steps": "steps.tsv",\n'
    printf '  "artifact_checksums": "checksums.sha256"\n'
    printf '}\n'
  } >"$RESULT_FILE"
  write_artifact_checksums
  log "result: $RESULT_FILE"
}

write_artifact_checksums() {
  local output="$RUN_DIR/checksums.sha256" path digest
  : >"$output"
  while IFS= read -r path; do
    [[ "$path" == "$output" || "$path" == "$RESULT_FILE" ]] && continue
    if command -v sha256sum >/dev/null 2>&1; then
      digest="$(sha256sum "$path" | awk '{print $1}')"
    else
      digest="$(shasum -a 256 "$path" | awk '{print $1}')"
    fi
    printf '%s  %s\n' "$digest" "${path#"$RUN_DIR"/}" >>"$output"
  done < <(find "$RUN_DIR" -type f -print | LC_ALL=C sort)
}

overlap="$(ps -axo pid=,comm= | awk -v self="$$" '
  {
    command = $2
    sub(/^.*\//, "", command)
    if ($1 != self && (command == "wukongim" || command == "wkbench")) print $0
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
run_step() {
  local phase="$1" rate="$2" measured="$3" step_dir="$RUN_DIR/steps/${phase}-rate-$rate" status=0 outcome
  log "$phase step: ${rate} offered SEND/s for ${measured}s"
  "$SHAKEOUT" --run-dir "$step_dir" --base-port "$BASE_PORT" --ready-timeout "$READY_TIMEOUT" \
    --send-rate "$rate" --measure-seconds "$measured" --warmup-seconds "$WARMUP_SECONDS" \
    --drain-timeout "$DRAIN_TIMEOUT" >"$RUN_DIR/steps/${phase}-rate-$rate.log" 2>&1 || status=$?
  outcome="$(awk -F '"' '/"outcome"[[:space:]]*:/ {print $4; exit}' "$step_dir/local-step.json" 2>/dev/null || true)"
  [[ -n "$outcome" ]] || outcome=insufficient_evidence
  case "$outcome:$status" in
    clean:0|storage_confounded:2|host_confounded:2|rate_failed:3|product_failure:3|insufficient_evidence:6) ;;
    *) outcome=insufficient_evidence; status=6 ;;
  esac
  printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$phase" "$rate" "$measured" "$outcome" "$status" "steps/${phase}-rate-$rate" >>"$STEPS_FILE"
  STEP_OUTCOME="$outcome"
  STEP_STATUS="$status"
}

highest_clean_rate=0
first_failing_rate=0
last_terminal_outcome=""
CLEAN_RATES=()
IFS=',' read -r -a reviewed_rates <<<"$RATES"
for rate in "${reviewed_rates[@]}"; do
  run_step search "$rate" "$SEARCH_MEASURE_SECONDS"
  case "$STEP_OUTCOME" in
    clean)
      highest_clean_rate="$rate"
      CLEAN_RATES+=("$rate")
      ;;
    rate_failed|product_failure)
      first_failing_rate="$rate"
      last_terminal_outcome="$STEP_OUTCOME"
      break
      ;;
    storage_confounded|host_confounded)
      write_result "$STEP_OUTCOME" step_confounded "$highest_clean_rate" "$rate"
      exit 2
      ;;
    *)
      write_result insufficient_evidence step_evidence_incomplete "$highest_clean_rate" "$rate"
      exit 6
      ;;
  esac
done

if (( first_failing_rate > 0 && highest_clean_rate > 0 )); then
  original_gap=$((first_failing_rate - highest_clean_rate))
  refine_increment=$((original_gap / 10))
  (( refine_increment > 0 )) || refine_increment=1
  candidate=$((highest_clean_rate + refine_increment))
  while (( candidate < first_failing_rate )); do
    run_step refine "$candidate" "$SEARCH_MEASURE_SECONDS"
    if [[ "$STEP_OUTCOME" == clean ]]; then
      highest_clean_rate="$candidate"
      CLEAN_RATES+=("$candidate")
      candidate=$((candidate + refine_increment))
      continue
    fi
    case "$STEP_OUTCOME" in
      rate_failed|product_failure)
        first_failing_rate="$candidate"
        last_terminal_outcome="$STEP_OUTCOME"
        ;;
      storage_confounded|host_confounded)
        write_result "$STEP_OUTCOME" refine_confounded "$highest_clean_rate" "$candidate"
        exit 2
        ;;
      *)
        write_result insufficient_evidence refine_evidence_incomplete "$highest_clean_rate" "$candidate"
        exit 6
        ;;
    esac
    break
  done
fi

if (( highest_clean_rate == 0 )); then
  outcome="${last_terminal_outcome:-rate_failed}"
  write_result "$outcome" no_clean_rate 0 "$first_failing_rate"
  exit 3
fi

repeat_rate="$highest_clean_rate"
run_step repeat "$repeat_rate" "$REPEAT_MEASURE_SECONDS"
if [[ "$STEP_OUTCOME" != clean ]]; then
  case "$STEP_OUTCOME" in
    storage_confounded|host_confounded)
      write_result "$STEP_OUTCOME" repeat_confounded "$highest_clean_rate" "$repeat_rate"
      exit 2
      ;;
    insufficient_evidence)
      write_result insufficient_evidence repeat_evidence_incomplete "$highest_clean_rate" "$repeat_rate"
      exit 6
      ;;
  esac
  fallback=0
  for rate in "${CLEAN_RATES[@]}"; do
    if (( rate < repeat_rate && rate > fallback )); then fallback="$rate"; fi
  done
  highest_clean_rate="$fallback"
  first_failing_rate="$repeat_rate"
  write_result "$STEP_OUTCOME" highest_clean_repeat_failed "$highest_clean_rate" "$first_failing_rate"
  exit 3
fi

write_result clean_knee highest_clean_repeat_passed "$highest_clean_rate" "$first_failing_rate"
