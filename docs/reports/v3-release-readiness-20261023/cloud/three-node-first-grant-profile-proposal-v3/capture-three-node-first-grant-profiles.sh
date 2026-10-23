#!/usr/bin/env bash
set -euo pipefail
umask 077
# A caller may have enabled xtrace through SHELLOPTS. Disable it before the
# inherited API token is copied, validated, or passed through an anonymous FD.
set +x

usage() {
  cat <<'USAGE'
Temporary explicit-node first-grant adapter (node-id defaults to 2). Required: --out-dir, --phase-state-file,
--trigger-kind first_grant_receipt, --trigger-observed-phase measurement,
--previous-utc (actual first grant), --current-utc (receipt detection),
--node (one explicit loopback URL), --node-id 1/2/3, --cpu-seconds10.
One CPU request then one goroutine request at local submission+5s. No heap/context.
This adapts frozen threshold-helper ownership; it is not an unchanged threshold mode.
USAGE
}

die_usage() {
  printf '[local-threshold-pprof] ERROR: %s\n' "$*" >&2
  exit 64
}

# Producer node identity is explicit; it never derives from a URL or output path.
NODE_ID=2
OUT_DIR=''
PHASE_STATE_FILE=''
TRIGGER_KIND=''
TRIGGER_OBSERVED_PHASE=''
PREVIOUS_UTC=''
CURRENT_UTC=''
CPU_SECONDS=10
CAPTURE_CONTEXT=0
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
    --node-id)
      [[ $# -ge 2 ]] || die_usage '--node-id requires a value'
      NODE_ID="$2"
      shift 2
      ;;
    --node)
      [[ $# -ge 2 ]] || die_usage '--node requires a value'
      NODE_URLS+=("$2")
      shift 2
      ;;
    --capture-context)
      die_usage "context capture is not permitted in temporary first-grant mode"
      shift
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

[[ "$NODE_ID" =~ ^[123]$ ]] || die_usage '--node-id must be exactly 1, 2, or 3'
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
  first_grant_receipt) ;;
  *) die_usage "unsupported --trigger-kind: $TRIGGER_KIND" ;;
esac
[[ "$TRIGGER_OBSERVED_PHASE" == measurement ]] ||
  die_usage '--trigger-observed-phase must be measurement'
[[ "$CPU_SECONDS" =~ ^[0-9]+$ ]] && (( CPU_SECONDS >= 1 && CPU_SECONDS <= 30 )) ||
  die_usage "--cpu-seconds must be an integer from 1 through 30: $CPU_SECONDS"
[[ "$CPU_SECONDS" -eq 10 ]] || die_usage "temporary mode requires cpu-seconds10"
NODE_COUNT="${#NODE_URLS[@]}"
[[ "$NODE_COUNT" -eq 1 ]] ||
  die_usage 'exactly one physical --node value is required'

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
REQUEST_DIR="$OUT_DIR/requests"
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

if [[ -e "$REQUEST_DIR" || -L "$REQUEST_DIR" || -e "$METADATA_FILE" || -e "$PROFILE_DIR" || -L "$PROFILE_DIR" || -e "$OUT_DIR/context" || -L "$OUT_DIR/context" ]]; then
  rmdir "$CLAIM_DIR" 2>/dev/null || true
  printf '[local-threshold-pprof] ERROR: unclaimed output already contains threshold profile artifacts\n' >&2
  exit 73
fi
if ! mkdir "$PROFILE_DIR" "$REQUEST_DIR" 2>/dev/null; then
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
  HEAP_STATUS[$index]=not_requested
  GOROUTINE_STATUS[$index]=missing
done
TIMER_PID=""
CAPTURE_PIDS=()
CAPTURE_TMP_FILES=()
CAPTURE_FINAL_FILES=()
CAPTURE_NODE_INDEXES=()
CAPTURE_KINDS=()
CAPTURE_STARTED=()
CAPTURE_HTTP_FILES=()
CAPTURE_RESULTS_RECORDED=()
START_PHASE="$(read_phase_state)"
END_PHASE="$START_PHASE"
STARTED_AT_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
COMPLETED_AT_UTC="$STARTED_AT_UTC"
OVERALL_STATUS=partial
OVERALL_REASON=capture_in_progress
VALID=false
FINALIZED=0
CAPTURE_REGISTERING=0
SIGNAL_PENDING=0

write_metadata() {
  local temporary="$METADATA_FILE.next.$$" node comma
  if ! {
    printf '{\n'
    printf '  "schema": "wukongim.local_first_grant_profile/v2",\n'
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
        "$NODE_ID" "${CPU_STATUS[$node]}" "${HEAP_STATUS[$node]}" "${GOROUTINE_STATUS[$node]}" "$comma"
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

# Store only finite curl/HTTP evidence; raw URL, header and stderr are excluded.
record_request_result() {
  local index="$1" curl_code="$2" http_code http_json=null error_class=none bytes=0 limit=8388608
  [[ "${CAPTURE_RESULTS_RECORDED[$index]}" == 0 ]] || return 0
  http_code="$(LC_ALL=C head -c 4 "${CAPTURE_HTTP_FILES[$index]}" 2>/dev/null || true)"
  if [[ "$http_code" =~ ^[0-9][0-9][0-9]$ ]]; then http_json="\"$http_code\""; else http_code=unknown; fi
  if [[ -f "${CAPTURE_TMP_FILES[$index]}" ]]; then bytes="$(wc -c <"${CAPTURE_TMP_FILES[$index]}")"; fi
  [[ "${CAPTURE_KINDS[$index]}" == goroutine ]] && limit=4194304
  case "$curl_code" in
    0) if [[ "$http_code" != 200 ]]; then error_class=http_status; elif (( bytes == 0 )); then error_class=empty_body; elif (( bytes > limit )); then error_class=max_filesize; fi ;;
    22) error_class=http_error ;;
    28) error_class=timeout ;;
    63) error_class=max_filesize ;;
    7) error_class=connect_error ;;
    129|130|143) error_class=interrupted ;;
    *) error_class=curl_error ;;
  esac
  printf '{"schema":"wukongim/local-profile-request-status/v1","node_id":%s,"kind":"%s","curl_exit_code":%s,"http_status":%s,"error_class":"%s","retained_body_bytes":%s,"max_bytes":%s}\n' \
    "$NODE_ID" "${CAPTURE_KINDS[$index]}" "$curl_code" "$http_json" "$error_class" "$bytes" "$limit" \
    >"$REQUEST_DIR/node-${NODE_ID}-${CAPTURE_KINDS[$index]}.json"
  CAPTURE_RESULTS_RECORDED[$index]=1
}

terminate_children() {
  local pid index code
  if [[ -n "$TIMER_PID" ]]; then
    kill "$TIMER_PID" 2>/dev/null || true
    wait "$TIMER_PID" 2>/dev/null || true
    TIMER_PID=""
  fi
  # Bash 3 treats an empty array expansion as unset under nounset.
  if (( ${#CAPTURE_PIDS[@]} == 0 )); then return 0; fi
  for pid in "${CAPTURE_PIDS[@]}"; do
    [[ -n "$pid" ]] || continue
    kill "$pid" 2>/dev/null || true
  done
  for index in "${!CAPTURE_PIDS[@]}"; do
    pid="${CAPTURE_PIDS[$index]}"
    [[ -n "$pid" ]] || continue
    code=0
    wait "$pid" 2>/dev/null || code=$?
    record_request_result "$index" "$code"
    CAPTURE_PIDS[$index]=''
  done
}

remove_temporary_files() {
  local temporary
  if (( ${#CAPTURE_TMP_FILES[@]} == 0 )); then return 0; fi
  for temporary in "${CAPTURE_TMP_FILES[@]}"; do
    [[ -n "$temporary" ]] || continue
    rm -f "$temporary"
  done
}

handle_signal() {
  # A spawned child must acquire its complete cleanup record before a signal
  # can terminate and join it. This branch must not disturb the pending $!.
  if (( CAPTURE_REGISTERING == 1 )); then
    SIGNAL_PENDING=1
    return
  fi
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
  local temporary="$destination.next.$$" max_bytes=8388608
  local http_file="$REQUEST_DIR/node-${NODE_ID}-$kind-http-code"
  case "$kind" in heap) max_bytes=16777216 ;; goroutine|context-*) max_bytes=4194304 ;; esac
  CAPTURE_STARTED+=("$(date -u +%Y-%m-%dT%H:%M:%SZ)")
  CAPTURE_REGISTERING=1
  curl -fsS --connect-timeout "$CONNECT_TIMEOUT_SECONDS" --max-time "$max_time" --max-filesize "$max_bytes" \
    --header @<(write_authorization_header) \
    -H 'X-WK-Bench-Evidence: local-threshold-pprof' --output "$temporary" --write-out '%{http_code}' "$url" >"$http_file" 2>/dev/null &
  CAPTURE_PIDS+=("$!")
  CAPTURE_TMP_FILES+=("$temporary")
  CAPTURE_FINAL_FILES+=("$destination")
  CAPTURE_NODE_INDEXES+=("$node_index")
  CAPTURE_KINDS+=("$kind")
  CAPTURE_HTTP_FILES+=("$http_file")
  CAPTURE_RESULTS_RECORDED+=(0)
  CAPTURE_REGISTERING=0
  if (( SIGNAL_PENDING == 1 )); then handle_signal; fi
}

# Context is opt-in, finite, and scoped separately from the profile/verdict metadata.
# Raw counter names are fixed; missing or oversized inputs remain explicit.
capture_context() {
  local phase="$1" path name bytes status index node_number
  local directory="$OUT_DIR/context/$phase"
  mkdir -p "$directory"
  date -u +%Y-%m-%dT%H:%M:%SZ >"$directory/observed-at-utc"
  printf 'source\tstatus\tbytes\n' >"$directory/sources.tsv"
  for path in /proc/stat /proc/diskstats /proc/pressure/cpu /proc/pressure/io /proc/pressure/memory /sys/fs/cgroup/cpu.stat /sys/fs/cgroup/io.stat; do
    name="${path//\//_}"
    status=complete
    if ! head -c 65537 "$path" >"$directory/$name" 2>/dev/null; then status=missing; fi
    bytes="$(wc -c <"$directory/$name")"
    if (( bytes > 65536 )); then status=oversized; fi
    [[ "$status" == complete ]] || rm -f "$directory/$name"
    printf '%s\t%s\t%s\n' "$path" "$status" "$bytes" >>"$directory/sources.tsv"
  done
  for index in "${!NODE_URLS[@]}"; do
    node_number="$NODE_ID"
    start_profile_request "$index" "context-$phase" "${NODE_URLS[$index]}/metrics" \
      "$directory/node-${node_number}.prom" "$SNAPSHOT_MAX_TIME_SECONDS"
  done
}

join_profile_requests() {
  local capture_index capture_status node_index curl_code http_code
  for capture_index in "${!CAPTURE_PIDS[@]}"; do
    [[ -n "${CAPTURE_PIDS[$capture_index]}" ]] || continue
    capture_status=missing
    curl_code=0
    wait "${CAPTURE_PIDS[$capture_index]}" || curl_code=$?
    record_request_result "$capture_index" "$curl_code"
    http_code="$(LC_ALL=C head -c 4 "${CAPTURE_HTTP_FILES[$capture_index]}" 2>/dev/null || true)"
    if [[ "$curl_code" -eq 0 && "$http_code" == 200 && -s "${CAPTURE_TMP_FILES[$capture_index]}" ]]; then
      limit=8388608
      [[ "${CAPTURE_KINDS[$capture_index]}" == goroutine ]] && limit=4194304
      bytes="$(wc -c <"${CAPTURE_TMP_FILES[$capture_index]}")"
      if (( bytes <= limit )) && mv "${CAPTURE_TMP_FILES[$capture_index]}" "${CAPTURE_FINAL_FILES[$capture_index]}"; then
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
      context-*) printf '%s\t%s\t%s\t%s\t%s\n' "${CAPTURE_KINDS[$capture_index]}" "$NODE_ID" "${CAPTURE_STARTED[$capture_index]}" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$capture_status" >>"$OUT_DIR/context/requests.tsv" ;;
    esac
  done
}

if (( CAPTURE_CONTEXT == 1 )); then
  mkdir -p "$OUT_DIR/context"
  printf 'phase\tnode\tstarted_at_utc\tjoined_at_utc\tstatus\n' >"$OUT_DIR/context/requests.tsv"
  capture_context before
fi

# Temporary fixed-role schedule. The original request-registration, auth,
# cancellation and joins remain in use; this is not a threshold trigger.
index=0
start_profile_request "$index" cpu   "${NODE_URLS[$index]}/debug/pprof/profile?seconds=${CPU_SECONDS}"   "$PROFILE_DIR/node-${NODE_ID}-cpu.pb.gz" "$CPU_MAX_TIME_SECONDS"
CAPTURE_REGISTERING=1
sleep 5 &
TIMER_PID=$!
CAPTURE_REGISTERING=0
if (( SIGNAL_PENDING == 1 )); then handle_signal; fi
wait "$TIMER_PID"
TIMER_PID=""
if [[ "$(read_phase_state)" != measurement ]]; then
  handle_signal
fi
start_profile_request "$index" goroutine   "${NODE_URLS[$index]}/debug/pprof/goroutine?debug=0"   "$PROFILE_DIR/node-${NODE_ID}-goroutine.pb.gz" "$SNAPSHOT_MAX_TIME_SECONDS"

join_profile_requests
if (( CAPTURE_CONTEXT == 1 )); then
  capture_context after
  join_profile_requests
fi

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
    if [[ "${CPU_STATUS[$index]}" != complete ||
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
