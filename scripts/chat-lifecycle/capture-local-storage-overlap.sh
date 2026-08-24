#!/usr/bin/env bash
set -euo pipefail
umask 077
export LC_ALL=C

METRICS_FILE=""
SNAPSHOT_ROOT=""
OUTPUT_INVENTORY=""
OBSERVED_AT=""
RUN_ID=""
SAMPLE=""
NODE=""
TEMP_DIR=""
ACTIVE_CHILD_PID=""
SCAN_DEADLINE=0
MAX_SNAPSHOT_FILES=4096
MAX_SCAN_SECONDS=3
MAX_RAW_OUTPUT_BYTES=1048576
MAX_INVENTORY_BYTES=524288
MAX_CHILD_FILE_BLOCKS=1024
MAX_FAST_CHILD_POLLS=512
CHILD_TERM_GRACE_SECONDS=1

terminate_active_child() {
  local child_pid="${ACTIVE_CHILD_PID:-}"
  [[ -n "$child_pid" ]] || return 0
  if kill -0 "$child_pid" 2>/dev/null; then
    kill -TERM "$child_pid" 2>/dev/null || true
    local term_deadline=$((SECONDS + CHILD_TERM_GRACE_SECONDS))
    while kill -0 "$child_pid" 2>/dev/null && (( SECONDS < term_deadline )); do
      /bin/sleep 0.05 || true
    done
    if kill -0 "$child_pid" 2>/dev/null; then
      kill -KILL "$child_pid" 2>/dev/null || true
    fi
  fi
  wait "$child_pid" 2>/dev/null || true
  ACTIVE_CHILD_PID=""
}

cleanup() {
  terminate_active_child
  if [[ -n "$TEMP_DIR" && -d "$TEMP_DIR" ]]; then
    rm -f "$TEMP_DIR/raw" "$TEMP_DIR/list" "$TEMP_DIR/find.err" "$TEMP_DIR/stat.out" "$TEMP_DIR/stat.err"
    rmdir "$TEMP_DIR" 2>/dev/null || true
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

run_bounded_child() {
  local stdout_path="$1"
  local stderr_path="$2"
  shift 2
  (( SCAN_DEADLINE > 0 && SECONDS < SCAN_DEADLINE )) || return 124
  (
    ulimit -f "$MAX_CHILD_FILE_BLOCKS" || exit 73
    exec "$@"
  ) >"$stdout_path" 2>"$stderr_path" &
  ACTIVE_CHILD_PID=$!
  local fast_polls=0
  while kill -0 "$ACTIVE_CHILD_PID" 2>/dev/null; do
    if (( SECONDS >= SCAN_DEADLINE )); then
      terminate_active_child
      return 124
    fi
    if (( fast_polls < MAX_FAST_CHILD_POLLS )); then
      fast_polls=$((fast_polls + 1))
      continue
    fi
    /bin/sleep 0.02 || true
  done
  local child_status=0
  wait "$ACTIVE_CHILD_PID" || child_status=$?
  ACTIVE_CHILD_PID=""
  return "$child_status"
}

usage() {
  printf 'usage: capture-local-storage-overlap.sh --metrics FILE --snapshot-root DIR --inventory FILE --observed-at UTC --run-id ID --sample NAME --node node-{1,2,3}\n' >&2
  exit 64
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --metrics) [[ $# -ge 2 ]] || usage; METRICS_FILE="$2"; shift 2 ;;
    --snapshot-root) [[ $# -ge 2 ]] || usage; SNAPSHOT_ROOT="$2"; shift 2 ;;
    --inventory) [[ $# -ge 2 ]] || usage; OUTPUT_INVENTORY="$2"; shift 2 ;;
    --observed-at) [[ $# -ge 2 ]] || usage; OBSERVED_AT="$2"; shift 2 ;;
    --run-id) [[ $# -ge 2 ]] || usage; RUN_ID="$2"; shift 2 ;;
    --sample) [[ $# -ge 2 ]] || usage; SAMPLE="$2"; shift 2 ;;
    --node) [[ $# -ge 2 ]] || usage; NODE="$2"; shift 2 ;;
    *) usage ;;
  esac
done

[[ -n "$METRICS_FILE" && -n "$SNAPSHOT_ROOT" && -n "$OUTPUT_INVENTORY" && -n "$OBSERVED_AT" && -n "$RUN_ID" && -n "$SAMPLE" && -n "$NODE" ]] || usage
[[ "$OBSERVED_AT" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?Z$ ]] || usage
[[ "$RUN_ID" =~ ^[A-Za-z0-9_-]{1,128}$ ]] || usage
[[ "$SAMPLE" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || usage
[[ "$OUTPUT_INVENTORY" == /* && ! -e "$OUTPUT_INVENTORY" && -d "$(dirname "$OUTPUT_INVENTORY")" &&
  ! -L "$(dirname "$OUTPUT_INVENTORY")" && "$(basename "$(dirname "$OUTPUT_INVENTORY")")" == snapshot-inventory &&
  "$(basename "$OUTPUT_INVENTORY")" == "$SAMPLE-$NODE.tsv" ]] || usage
case "$NODE" in node-1|node-2|node-3) ;; *) usage ;; esac

missing_row() {
  printf '%s\t%s\t%s\t%s\tmissing\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\tunavailable\n' \
    "$OBSERVED_AT" "$RUN_ID" "$SAMPLE" "$NODE"
  exit 0
}

[[ -f "$METRICS_FILE" && ! -L "$METRICS_FILE" && -s "$METRICS_FILE" ]] || missing_row
compaction_values="$(awk '
  function numeric(value) {
    return value ~ /^[-+]?([0-9]+([.][0-9]*)?|[.][0-9]+)([eE][-+]?[0-9]+)?$/
  }
  function metric_name(token, boundary) {
    boundary = substr(token, length(metric) + 1, 1)
    return index(token, metric) == 1 && (boundary == "" || boundary == "{")
  }
  $1 !~ /^#/ {
    metric = "wukongim_storage_pebble_compaction_count"
    if (metric_name($1)) {
      if (!numeric($2) || $2 < 0 || $2 != int($2)) exit 2
      count += $2; count_seen++
    }
    metric = "wukongim_storage_pebble_compactions_in_progress"
    if (metric_name($1)) {
      if (!numeric($2) || $2 < 0 || $2 != int($2)) exit 2
      active += $2; active_seen++
    }
  }
  END {
    if (count_seen == 0 || active_seen == 0) exit 3
    printf "%.0f\t%.0f\n", count, active
  }
' "$METRICS_FILE" 2>/dev/null)" || missing_row
[[ "$compaction_values" =~ ^[0-9]+$'\t'[0-9]+$ ]] || missing_row

snapshot_parent="$(dirname "$SNAPSHOT_ROOT")"
[[ -d "$snapshot_parent" && ! -L "$snapshot_parent" ]] || missing_row
platform="$(uname -s)" || missing_row
SCAN_DEADLINE=$((SECONDS + MAX_SCAN_SECONDS))
TEMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/wk-storage-overlap.XXXXXX")" || exit 73
: >"$TEMP_DIR/raw"
: >"$TEMP_DIR/list"
if [[ -e "$SNAPSHOT_ROOT" ]]; then
  [[ -d "$SNAPSHOT_ROOT" && ! -L "$SNAPSHOT_ROOT" ]] || missing_row
  : >"$TEMP_DIR/find.err"
  run_bounded_child "$TEMP_DIR/raw" "$TEMP_DIR/find.err" find "$SNAPSHOT_ROOT" -type f -print || missing_row
  [[ ! -s "$TEMP_DIR/find.err" ]] || missing_row
  raw_output_bytes="$(wc -c <"$TEMP_DIR/raw" | tr -d '[:space:]')"
  [[ "$raw_output_bytes" =~ ^[0-9]+$ ]] || missing_row
  (( raw_output_bytes <= MAX_RAW_OUTPUT_BYTES )) || missing_row
  (( SECONDS < SCAN_DEADLINE )) || missing_row
  LC_ALL=C sort "$TEMP_DIR/raw" -o "$TEMP_DIR/raw" || missing_row
fi

snapshot_candidates=0
while IFS= read -r snapshot_file; do
  [[ -n "$snapshot_file" ]] || missing_row
  snapshot_candidates=$((snapshot_candidates + 1))
  (( snapshot_candidates <= MAX_SNAPSHOT_FILES )) || missing_row
done <"$TEMP_DIR/raw"

snapshot_files=0
snapshot_bytes=0
inventory_bytes=0
while IFS= read -r snapshot_file; do
  [[ -n "$snapshot_file" && -f "$snapshot_file" && ! -L "$snapshot_file" ]] || missing_row
  : >"$TEMP_DIR/stat.out"
  : >"$TEMP_DIR/stat.err"
  if [[ "$platform" == Darwin ]]; then
    run_bounded_child "$TEMP_DIR/stat.out" "$TEMP_DIR/stat.err" stat -f '%z' "$snapshot_file" || missing_row
  else
    run_bounded_child "$TEMP_DIR/stat.out" "$TEMP_DIR/stat.err" stat -c '%s' "$snapshot_file" || missing_row
  fi
  [[ ! -s "$TEMP_DIR/stat.err" ]] || missing_row
  size="$(<"$TEMP_DIR/stat.out")"
  [[ "$size" =~ ^[0-9]+$ ]] || missing_row
  relative="${snapshot_file#"$SNAPSHOT_ROOT"/}"
  [[ "$relative" != "$snapshot_file" && -n "$relative" ]] || missing_row
  entry_bytes=$((${#relative} + ${#size} + 2))
  (( inventory_bytes + entry_bytes <= MAX_INVENTORY_BYTES )) || missing_row
  printf '%s\t%s\n' "$relative" "$size" >>"$TEMP_DIR/list"
  inventory_bytes=$((inventory_bytes + entry_bytes))
  snapshot_files=$((snapshot_files + 1))
  snapshot_bytes=$((snapshot_bytes + size))
done <"$TEMP_DIR/raw"

if ! mv "$TEMP_DIR/list" "$OUTPUT_INVENTORY"; then
  exit 73
fi

if command -v sha256sum >/dev/null 2>&1; then
  snapshot_identity="$(sha256sum "$OUTPUT_INVENTORY" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  snapshot_identity="$(shasum -a 256 "$OUTPUT_INVENTORY" | awk '{print $1}')"
else
  exit 73
fi
[[ "$snapshot_identity" =~ ^[0-9a-f]{64}$ ]] || exit 73

printf '%s\t%s\t%s\t%s\tcomplete\t%s\t%s\t%s\t%s\t%s\t%s\n' \
  "$OBSERVED_AT" "$RUN_ID" "$SAMPLE" "$NODE" "${compaction_values%%$'\t'*}" "${compaction_values#*$'\t'}" \
  "$snapshot_files" "$snapshot_bytes" "$snapshot_identity" "snapshot-inventory/${SAMPLE}-${NODE}.tsv"
