#!/usr/bin/env bash
set -euo pipefail
umask 077
# A caller may have enabled xtrace through SHELLOPTS. Disable it before the
# inherited API token is copied, validated, or passed through an anonymous FD.
set +x

usage() {
  cat <<'USAGE'
Usage: scripts/capture-wukongim-local-threshold-pprof.sh \
  --out-dir DIR \
  --phase-state-file FILE \
  --trigger-kind actual_offered_ratio|sendack_p99|terminal_product_failure \
  --trigger-observed-phase measurement \
  --previous-utc RFC3339Nano \
  --current-utc RFC3339Nano \
  --node http://127.0.0.1:PORT \
  [--node http://127.0.0.1:PORT \
   --node http://127.0.0.1:PORT] \
  [--cpu-seconds N]

Captures one bounded local profile set when a measured threshold is first
crossed. --trigger-observed-phase is the authoritative phase at the exact
typed worker-cut bracket. The live phase-state file contains one of warmup,
measurement, drain, or shutdown. A capture is valid only when both live phase
reads are measurement. If the parent has already closed SEND admission, the
helper records a typed partial result without starting network requests. The
required WK_BENCH_API_TOKEN environment variable authenticates every pprof
request without placing the credential in command arguments or artifacts.

Exit status is 0 for complete, partial, phase-invalid, and repeated requests.
Invalid invocation parameters exit 64. Artifact/metadata write failures exit 73.
USAGE
}

die_usage() {
  printf '[local-threshold-pprof] ERROR: %s\n' "$*" >&2
  exit 64
}

OUT_DIR=''
PHASE_STATE_FILE=''
TRIGGER_KIND=''
TRIGGER_OBSERVED_PHASE=''
PREVIOUS_UTC=''
CURRENT_UTC=''
CPU_SECONDS=10
NODE_URLS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      [[ $# -ge 2 ]] || die_usage '--out-dir requires a value'
      OUT_DIR="$2"
      shift 2
      ;;
    --phase-state-file)
      [[ $# -ge 2 ]] || die_usage '--phase-state-file requires a value'
      PHASE_STATE_FILE="$2"
      shift 2
      ;;
    --trigger-kind)
      [[ $# -ge 2 ]] || die_usage '--trigger-kind requires a value'
      TRIGGER_KIND="$2"
      shift 2
      ;;
    --trigger-observed-phase)
      [[ $# -ge 2 ]] || die_usage '--trigger-observed-phase requires a value'
      TRIGGER_OBSERVED_PHASE="$2"
      shift 2
      ;;
    --previous-utc)
      [[ $# -ge 2 ]] || die_usage '--previous-utc requires a value'
      PREVIOUS_UTC="$2"
      shift 2
      ;;
    --current-utc)
      [[ $# -ge 2 ]] || die_usage '--current-utc requires a value'
      CURRENT_UTC="$2"
      shift 2
      ;;
    --node)
      [[ $# -ge 2 ]] || die_usage '--node requires a value'
      NODE_URLS+=("$2")
      shift 2
      ;;
    --cpu-seconds)
      [[ $# -ge 2 ]] || die_usage '--cpu-seconds requires a value'
      CPU_SECONDS="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die_usage "unknown argument: $1"
      ;;
  esac
done

[[ -n "$OUT_DIR" ]] || die_usage '--out-dir is required'
[[ -n "$PHASE_STATE_FILE" ]] || die_usage '--phase-state-file is required'
LOCAL_PPROF_API_TOKEN="${WK_BENCH_API_TOKEN:-}"
[[ -n "$LOCAL_PPROF_API_TOKEN" ]] || die_usage 'WK_BENCH_API_TOKEN is required'
if [[ "$LOCAL_PPROF_API_TOKEN" == *$'\r'* || "$LOCAL_PPROF_API_TOKEN" == *$'\n'* ]]; then
  die_usage 'WK_BENCH_API_TOKEN must not contain CR or LF'
fi
if [[ "$LOCAL_PPROF_API_TOKEN" == [[:space:]]* || "$LOCAL_PPROF_API_TOKEN" == *[[:space:]] ]]; then
  die_usage 'WK_BENCH_API_TOKEN must not have leading or trailing whitespace'
fi
# Keep the inherited credential out of every curl child environment. The
# unexported copy is written only to an anonymous process-substitution pipe.
export -n LOCAL_PPROF_API_TOKEN
unset WK_BENCH_API_TOKEN
case "$TRIGGER_KIND" in
  actual_offered_ratio|sendack_p99|terminal_product_failure) ;;
  *) die_usage "unsupported --trigger-kind: $TRIGGER_KIND" ;;
esac
[[ "$TRIGGER_OBSERVED_PHASE" == measurement ]] ||
  die_usage '--trigger-observed-phase must be measurement'
[[ "$CPU_SECONDS" =~ ^[0-9]+$ ]] && (( CPU_SECONDS >= 1 && CPU_SECONDS <= 30 )) ||
  die_usage "--cpu-seconds must be an integer from 1 through 30: $CPU_SECONDS"
NODE_COUNT="${#NODE_URLS[@]}"
[[ "$NODE_COUNT" -eq 1 || "$NODE_COUNT" -eq 3 ]] ||
  die_usage 'exactly one or three --node values are required'

normalize_rfc3339_nano() {
  local value="$1" base fraction zone zone_for_date epoch utc_base fraction_digits fraction_nanos
  [[ "$value" =~ ^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(\.[0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})$ ]] || return 1
  base="${BASH_REMATCH[1]}"
  fraction="${BASH_REMATCH[2]}"
  zone="${BASH_REMATCH[3]}"
  epoch="$(date -u -d "${base}${zone}" '+%s' 2>/dev/null || true)"
  if [[ ! "$epoch" =~ ^-?[0-9]+$ ]]; then
    zone_for_date="$zone"
    [[ "$zone_for_date" == Z ]] && zone_for_date=+0000
    zone_for_date="${zone_for_date/:/}"
    epoch="$(date -u -j -f '%Y-%m-%dT%H:%M:%S%z' "${base}${zone_for_date}" '+%s' 2>/dev/null || true)"
  fi
  [[ "$epoch" =~ ^-?[0-9]+$ ]] || return 1
  utc_base="$(date -u -d "@$epoch" '+%Y-%m-%dT%H:%M:%S' 2>/dev/null || true)"
  if [[ -z "$utc_base" ]]; then
    utc_base="$(date -u -r "$epoch" '+%Y-%m-%dT%H:%M:%S' 2>/dev/null || true)"
  fi
  [[ "$utc_base" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}$ ]] || return 1
  fraction_digits="${fraction#.}"
  while [[ "$fraction_digits" == *0 ]]; do
    fraction_digits="${fraction_digits%0}"
  done
  fraction_nanos="${fraction#.}000000000"
  fraction_nanos="${fraction_nanos:0:9}"
  if [[ -n "$fraction_digits" ]]; then
    printf '%s.%sZ\t%s\t%s\n' "$utc_base" "$fraction_digits" "$epoch" "$fraction_nanos"
  else
    printf '%sZ\t%s\t%s\n' "$utc_base" "$epoch" "$fraction_nanos"
  fi
}

PREVIOUS_NORMALIZED=''
CURRENT_NORMALIZED=''
PREVIOUS_EPOCH=''
CURRENT_EPOCH=''
PREVIOUS_NANOS=''
CURRENT_NANOS=''
IFS=$'\t' read -r PREVIOUS_NORMALIZED PREVIOUS_EPOCH PREVIOUS_NANOS < <(normalize_rfc3339_nano "$PREVIOUS_UTC" || true)
IFS=$'\t' read -r CURRENT_NORMALIZED CURRENT_EPOCH CURRENT_NANOS < <(normalize_rfc3339_nano "$CURRENT_UTC" || true)
[[ -n "$PREVIOUS_NORMALIZED" ]] || die_usage "--previous-utc must be valid RFC3339Nano: $PREVIOUS_UTC"
[[ -n "$CURRENT_NORMALIZED" ]] || die_usage "--current-utc must be valid RFC3339Nano: $CURRENT_UTC"
if (( PREVIOUS_EPOCH > CURRENT_EPOCH )) ||
  { (( PREVIOUS_EPOCH == CURRENT_EPOCH )) && (( 10#$PREVIOUS_NANOS >= 10#$CURRENT_NANOS )); }; then
  die_usage '--previous-utc must be earlier than --current-utc'
fi
PREVIOUS_UTC="$PREVIOUS_NORMALIZED"
CURRENT_UTC="$CURRENT_NORMALIZED"

normalize_loopback_url() {
  local value="${1%/}" port=''
  if [[ "$value" =~ ^http://127\.0\.0\.1:([0-9]+)$ ]]; then
    port="${BASH_REMATCH[1]}"
  elif [[ "$value" =~ ^http://\[::1\]:([0-9]+)$ ]]; then
    port="${BASH_REMATCH[1]}"
  else
    return 1
  fi
  [[ "$port" =~ ^[0-9]+$ ]] && (( port >= 1 && port <= 65535 )) || return 1
  printf '%s\n' "$value"
}

for index in "${!NODE_URLS[@]}"; do
  normalized="$(normalize_loopback_url "${NODE_URLS[$index]}" || true)"
  [[ -n "$normalized" ]] || die_usage "--node must be an explicit loopback HTTP base URL: ${NODE_URLS[$index]}"
  NODE_URLS[$index]="$normalized"
done
for left in "${!NODE_URLS[@]}"; do
  for right in "${!NODE_URLS[@]}"; do
    (( right > left )) || continue
    [[ "${NODE_URLS[$left]}" != "${NODE_URLS[$right]}" ]] ||
      die_usage 'multiple --node values must be distinct'
  done
done

[[ "$OUT_DIR" == /* && "$OUT_DIR" != / && ! -L "$OUT_DIR" ]] ||
  die_usage "unsafe --out-dir (must be an absolute non-symlink directory below /): $OUT_DIR"
if ! mkdir -p "$OUT_DIR" 2>/dev/null; then
  printf '[local-threshold-pprof] ERROR: cannot create output directory\n' >&2
  exit 73
fi
RESOLVED_OUT_DIR="$(cd -P "$OUT_DIR" 2>/dev/null && pwd)"
[[ -n "$RESOLVED_OUT_DIR" && "$RESOLVED_OUT_DIR" != / ]] ||
  die_usage "unsafe --out-dir resolution: $OUT_DIR"
OUT_DIR="$RESOLVED_OUT_DIR"
CLAIM_DIR="$OUT_DIR/.threshold-pprof.claim"
METADATA_FILE="$OUT_DIR/metadata.json"
PROFILE_DIR="$OUT_DIR/profiles"
if ! mkdir "$CLAIM_DIR" 2>/dev/null; then
  # A concurrent or earlier caller owns the immutable first-trigger evidence.
  # Briefly allow its initial metadata write to become visible, then return.
  for _ in 1 2 3; do
    [[ -s "$METADATA_FILE" ]] && break
    sleep 1
  done
  printf '[local-threshold-pprof] first trigger already claimed\n'
  exit 0
fi

if [[ -e "$METADATA_FILE" || -e "$PROFILE_DIR" || -L "$PROFILE_DIR" ]]; then
  rmdir "$CLAIM_DIR" 2>/dev/null || true
  printf '[local-threshold-pprof] ERROR: unclaimed output already contains threshold profile artifacts\n' >&2
  exit 73
fi
if ! mkdir "$PROFILE_DIR" 2>/dev/null; then
  printf '[local-threshold-pprof] ERROR: cannot create profile directory\n' >&2
  exit 73
fi

read_phase_state() {
  local raw=''
  if [[ ! -f "$PHASE_STATE_FILE" ]]; then
    printf 'missing\n'
    return
  fi
  raw="$(LC_ALL=C head -c 65 "$PHASE_STATE_FILE" 2>/dev/null || true)"
  case "$raw" in
    warmup|measurement|drain|shutdown) printf '%s\n' "$raw" ;;
    *) printf 'invalid\n' ;;
  esac
}

CPU_STATUS=()
HEAP_STATUS=()
GOROUTINE_STATUS=()
for index in "${!NODE_URLS[@]}"; do
  CPU_STATUS[$index]=missing
  HEAP_STATUS[$index]=missing
  GOROUTINE_STATUS[$index]=missing
done
CAPTURE_PIDS=()
CAPTURE_TMP_FILES=()
CAPTURE_FINAL_FILES=()
CAPTURE_NODE_INDEXES=()
CAPTURE_KINDS=()
START_PHASE="$(read_phase_state)"
END_PHASE="$START_PHASE"
STARTED_AT_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMPLETED_AT_UTC="$STARTED_AT_UTC"
OVERALL_STATUS=partial
OVERALL_REASON=capture_in_progress
VALID=false
FINALIZED=0

write_metadata() {
  local temporary="$METADATA_FILE.next.$$" node comma
  if ! {
    printf '{\n'
    printf '  "schema": "wukongim.local_threshold_pprof/v1",\n'
    printf '  "trigger": {"kind": "%s", "observed_phase": "%s", "previous_utc": "%s", "current_utc": "%s"},\n' \
      "$TRIGGER_KIND" "$TRIGGER_OBSERVED_PHASE" "$PREVIOUS_UTC" "$CURRENT_UTC"
    printf '  "capture": {"status": "%s", "valid": %s, "reason": "%s", "start_phase": "%s", "end_phase": "%s", "started_at_utc": "%s", "completed_at_utc": "%s", "cpu_seconds": %s},\n' \
      "$OVERALL_STATUS" "$VALID" "$OVERALL_REASON" "$START_PHASE" "$END_PHASE" \
      "$STARTED_AT_UTC" "$COMPLETED_AT_UTC" "$CPU_SECONDS"
    printf '  "nodes": [\n'
    for node in "${!NODE_URLS[@]}"; do
      comma=,
      [[ "$node" -eq $((NODE_COUNT - 1)) ]] && comma=''
      printf '    {"node": "node-%s", "cpu": "%s", "heap": "%s", "goroutine": "%s"}%s\n' \
        "$((node + 1))" "${CPU_STATUS[$node]}" "${HEAP_STATUS[$node]}" "${GOROUTINE_STATUS[$node]}" "$comma"
    done
    printf '  ]\n'
    printf '}\n'
  } >"$temporary"; then
    rm -f "$temporary"
    return 1
  fi
  if ! mv "$temporary" "$METADATA_FILE"; then
    rm -f "$temporary"
    return 1
  fi
}

terminate_children() {
  local pid
  for pid in "${CAPTURE_PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
    kill "$pid" 2>/dev/null || true
  done
  for pid in "${CAPTURE_PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
    wait "$pid" 2>/dev/null || true
  done
}

remove_temporary_files() {
  local temporary
  for temporary in "${CAPTURE_TMP_FILES[@]}"; do
    [[ -n "$temporary" ]] || continue
    rm -f "$temporary"
  done
}

handle_signal() {
  trap - HUP INT TERM
  terminate_children
  remove_temporary_files
  END_PHASE="$(read_phase_state)"
  COMPLETED_AT_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  OVERALL_STATUS=partial
  OVERALL_REASON=interrupted
  VALID=false
  write_metadata || true
  FINALIZED=1
  exit 0
}
trap handle_signal HUP INT TERM

cleanup_on_exit() {
  local exit_status="$?"
  trap - EXIT HUP INT TERM
  if [[ "$FINALIZED" -eq 0 ]]; then
    terminate_children
    remove_temporary_files
    END_PHASE="$(read_phase_state)"
    COMPLETED_AT_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    OVERALL_STATUS=partial
    OVERALL_REASON=internal_error
    VALID=false
    write_metadata || true
  fi
  exit "$exit_status"
}
trap cleanup_on_exit EXIT

if ! write_metadata; then
  printf '[local-threshold-pprof] ERROR: cannot write metadata\n' >&2
  exit 73
fi

if [[ "$START_PHASE" != "$TRIGGER_OBSERVED_PHASE" ]]; then
  COMPLETED_AT_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  OVERALL_STATUS=partial
  OVERALL_REASON=capture_start_missed_measurement
  VALID=false
  write_metadata || exit 73
  FINALIZED=1
  exit 0
fi

CONNECT_TIMEOUT_SECONDS=2
SNAPSHOT_MAX_TIME_SECONDS=5
CPU_MAX_TIME_SECONDS=$((CPU_SECONDS + 5))

write_authorization_header() {
  printf 'Authorization: Bearer %s\n' "$LOCAL_PPROF_API_TOKEN"
}

start_profile_request() {
  local node_index="$1" kind="$2" url="$3" destination="$4" max_time="$5"
  local temporary="$destination.next.$$"
  curl -fsS --connect-timeout "$CONNECT_TIMEOUT_SECONDS" --max-time "$max_time" \
    --header @<(write_authorization_header) \
    -H 'X-WK-Bench-Evidence: local-threshold-pprof' "$url" >"$temporary" 2>/dev/null &
  CAPTURE_PIDS+=("$!")
  CAPTURE_TMP_FILES+=("$temporary")
  CAPTURE_FINAL_FILES+=("$destination")
  CAPTURE_NODE_INDEXES+=("$node_index")
  CAPTURE_KINDS+=("$kind")
}

for index in "${!NODE_URLS[@]}"; do
  node_number=$((index + 1))
  start_profile_request "$index" cpu \
    "${NODE_URLS[$index]}/debug/pprof/profile?seconds=${CPU_SECONDS}" \
    "$PROFILE_DIR/node-${node_number}-cpu.pb.gz" "$CPU_MAX_TIME_SECONDS"
  start_profile_request "$index" heap \
    "${NODE_URLS[$index]}/debug/pprof/heap" \
    "$PROFILE_DIR/node-${node_number}-heap.pb.gz" "$SNAPSHOT_MAX_TIME_SECONDS"
  start_profile_request "$index" goroutine \
    "${NODE_URLS[$index]}/debug/pprof/goroutine?debug=2" \
    "$PROFILE_DIR/node-${node_number}-goroutine.txt" "$SNAPSHOT_MAX_TIME_SECONDS"
done

for capture_index in "${!CAPTURE_PIDS[@]}"; do
  capture_status=missing
  if wait "${CAPTURE_PIDS[$capture_index]}" && [[ -s "${CAPTURE_TMP_FILES[$capture_index]}" ]]; then
    if mv "${CAPTURE_TMP_FILES[$capture_index]}" "${CAPTURE_FINAL_FILES[$capture_index]}"; then
      capture_status=complete
    fi
  fi
  [[ "$capture_status" == complete ]] || rm -f "${CAPTURE_TMP_FILES[$capture_index]}"
  CAPTURE_PIDS[$capture_index]=''
  node_index="${CAPTURE_NODE_INDEXES[$capture_index]}"
  case "${CAPTURE_KINDS[$capture_index]}" in
    cpu) CPU_STATUS[$node_index]="$capture_status" ;;
    heap) HEAP_STATUS[$node_index]="$capture_status" ;;
    goroutine) GOROUTINE_STATUS[$node_index]="$capture_status" ;;
  esac
done

END_PHASE="$(read_phase_state)"
COMPLETED_AT_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OVERALL_STATUS=complete
OVERALL_REASON=ok
VALID=true
if [[ "$END_PHASE" != "measurement" ]]; then
  OVERALL_STATUS=partial
  OVERALL_REASON=phase_changed_during_capture
  VALID=false
else
  for index in "${!NODE_URLS[@]}"; do
    if [[ "${CPU_STATUS[$index]}" != complete || "${HEAP_STATUS[$index]}" != complete ||
      "${GOROUTINE_STATUS[$index]}" != complete ]]; then
      OVERALL_STATUS=partial
      OVERALL_REASON=profile_capture_missing
      VALID=false
      break
    fi
  done
fi
write_metadata || exit 73
FINALIZED=1
trap - HUP INT TERM
exit 0
