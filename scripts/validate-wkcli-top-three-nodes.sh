#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TIMESTAMP="${WK_TOP_VALIDATE_TIMESTAMP:-$(date +%Y%m%d-%H%M%S)}"
OUT_DIR="${WK_TOP_VALIDATE_OUT_DIR:-$ROOT_DIR/docs/development/perf-runs/${TIMESTAMP}-wkcli-top-three-nodes}"
WKCLI_BIN="${WK_TOP_VALIDATE_WKCLI_BIN:-$ROOT_DIR/data/wkcli-top-validation/wkcli}"
START_SCRIPT="${WK_TOP_VALIDATE_START_SCRIPT:-$ROOT_DIR/scripts/start-wukongimv2-three-nodes.sh}"
READY_TIMEOUT="${WK_TOP_VALIDATE_READY_TIMEOUT:-90}"
TOP_WINDOW="${WK_TOP_VALIDATE_WINDOW:-10s}"
TOP_LIMIT="${WK_TOP_VALIDATE_LIMIT:-20}"
WARMUP_SECONDS="${WK_TOP_VALIDATE_WARMUP:-3}"
TOP_WAIT_SECONDS="${WK_TOP_VALIDATE_TOP_WAIT:-2}"
ALERT_WAIT_SECONDS="${WK_TOP_VALIDATE_ALERT_WAIT:-2}"

API_ADDRS="${WK_TOP_VALIDATE_API_ADDRS:-http://127.0.0.1:5011,http://127.0.0.1:5012,http://127.0.0.1:5013}"
GATEWAY_ADDRS="${WK_TOP_VALIDATE_GATEWAY_ADDRS:-127.0.0.1:5111,127.0.0.1:5112,127.0.0.1:5113}"
CLIENTS="${WK_TOP_VALIDATE_CLIENTS:-6}"
MESSAGES="${WK_TOP_VALIDATE_MESSAGES:-1200}"
CHANNELS="${WK_TOP_VALIDATE_CHANNELS:-60}"
CHANNEL_PREFIX="${WK_TOP_VALIDATE_CHANNEL_PREFIX:-top-smoke}"
CHANNEL_TYPE="${WK_TOP_VALIDATE_CHANNEL_TYPE:-group}"
PAYLOAD_SIZE="${WK_TOP_VALIDATE_SIZE:-128B}"
THROUGHPUT="${WK_TOP_VALIDATE_THROUGHPUT:-600}"
CONNECT_RATE="${WK_TOP_VALIDATE_CONNECT_RATE:-30}"

START_CLUSTER=1
CLEAN_CLUSTER=1
BUILD_WKCLI=1
INJECT_PROTOCOL_ERROR=0
CLUSTER_PID=""
TOP_HEALTH_STATUS="not_run"
BENCH_STATUS="not_run"
EXIT_STATUS=0
TOP_HEALTH_EXIT_STATUS=8
BENCH_EXIT_STATUS=9

usage() {
  cat <<'USAGE'
Usage: scripts/validate-wkcli-top-three-nodes.sh [options]

Starts a local cmd/wukongimv2 three-node cluster, runs wkcli bench send, then
validates wkcli top JSON evidence for readiness, resource fields, and sticky
alert behavior. Normal benchmark client disconnects must not create
gateway/session_error alerts.

Options:
  --out-dir DIR              Evidence output directory.
  --wkcli-bin PATH           wkcli binary path. Default: data/wkcli-top-validation/wkcli.
  --no-build                 Reuse --wkcli-bin instead of running go build.
  --no-start                 Use an already-running three-node cluster.
  --no-clean                 Keep existing node data when starting the cluster.
  --start-script PATH        Three-node startup script.
  --ready-timeout SECS       Cluster ready wait timeout. Default: 90.
  --api LIST                 Comma-separated HTTP API base URLs.
  --gateway LIST             Comma-separated WKProto gateway addresses.
  --clients N                wkcli bench client count. Default: 6.
  --msgs N                   wkcli bench message count. Default: 1200.
  --channels N               wkcli bench generated channel count. Default: 60.
  --channel-prefix PREFIX    wkcli bench channel prefix. Default: top-smoke.
  --channel-type TYPE        wkcli bench channel type. Default: group.
  --size SIZE                wkcli bench payload size. Default: 128B.
  --throughput N             wkcli bench target messages/sec. Default: 600.
  --connect-rate N           wkcli bench connect attempts/sec. Default: 30.
  --top-window DURATION      wkcli top aggregation window. Default: 10s.
  --top-limit N              wkcli top pressure/alert item limit. Default: 20.
  --warmup SECS              Wait after readiness before first top sample. Default: 3.
  --top-wait SECS            Wait after bench before post-bench top sample. Default: 2.
  --inject-protocol-error    Send invalid bytes to each gateway and require a gateway/session_error alert.
  --alert-wait SECS          Wait after protocol injection before top sample. Default: 2.
  -h, --help                 Show this help.
USAGE
}

log() {
  printf '[wkcli-top-three] %s\n' "$*"
}

die() {
  printf '[wkcli-top-three] ERROR: %s\n' "$*" >&2
  exit 1
}

require_positive_int() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a positive integer: $value"
  (( value > 0 )) || die "$name must be a positive integer: $value"
}

require_nonnegative_int() {
  local name="$1"
  local value="$2"
  [[ "$value" =~ ^[0-9]+$ ]] || die "$name must be a non-negative integer: $value"
}

split_csv() {
  local raw="$1"
  local var_name="$2"
  eval "$var_name=()"
  local values=()
  local item
  IFS=',' read -ra values <<<"$raw"
  for item in "${values[@]}"; do
    item="${item//[[:space:]]/}"
    [[ -n "$item" ]] || die "comma-separated list contains an empty item: $raw"
    eval "$var_name+=(\"\$item\")"
  done
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out-dir)
      [[ $# -ge 2 ]] || die '--out-dir requires a value'
      OUT_DIR="$2"
      shift 2
      ;;
    --wkcli-bin)
      [[ $# -ge 2 ]] || die '--wkcli-bin requires a value'
      WKCLI_BIN="$2"
      shift 2
      ;;
    --no-build)
      BUILD_WKCLI=0
      shift
      ;;
    --no-start)
      START_CLUSTER=0
      shift
      ;;
    --no-clean)
      CLEAN_CLUSTER=0
      shift
      ;;
    --start-script)
      [[ $# -ge 2 ]] || die '--start-script requires a value'
      START_SCRIPT="$2"
      shift 2
      ;;
    --ready-timeout)
      [[ $# -ge 2 ]] || die '--ready-timeout requires a value'
      READY_TIMEOUT="$2"
      shift 2
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
    --clients)
      [[ $# -ge 2 ]] || die '--clients requires a value'
      CLIENTS="$2"
      shift 2
      ;;
    --msgs)
      [[ $# -ge 2 ]] || die '--msgs requires a value'
      MESSAGES="$2"
      shift 2
      ;;
    --channels)
      [[ $# -ge 2 ]] || die '--channels requires a value'
      CHANNELS="$2"
      shift 2
      ;;
    --channel-prefix)
      [[ $# -ge 2 ]] || die '--channel-prefix requires a value'
      CHANNEL_PREFIX="$2"
      shift 2
      ;;
    --channel-type)
      [[ $# -ge 2 ]] || die '--channel-type requires a value'
      CHANNEL_TYPE="$2"
      shift 2
      ;;
    --size)
      [[ $# -ge 2 ]] || die '--size requires a value'
      PAYLOAD_SIZE="$2"
      shift 2
      ;;
    --throughput)
      [[ $# -ge 2 ]] || die '--throughput requires a value'
      THROUGHPUT="$2"
      shift 2
      ;;
    --connect-rate)
      [[ $# -ge 2 ]] || die '--connect-rate requires a value'
      CONNECT_RATE="$2"
      shift 2
      ;;
    --top-window)
      [[ $# -ge 2 ]] || die '--top-window requires a value'
      TOP_WINDOW="$2"
      shift 2
      ;;
    --top-limit)
      [[ $# -ge 2 ]] || die '--top-limit requires a value'
      TOP_LIMIT="$2"
      shift 2
      ;;
    --warmup)
      [[ $# -ge 2 ]] || die '--warmup requires a value'
      WARMUP_SECONDS="$2"
      shift 2
      ;;
    --top-wait)
      [[ $# -ge 2 ]] || die '--top-wait requires a value'
      TOP_WAIT_SECONDS="$2"
      shift 2
      ;;
    --inject-protocol-error)
      INJECT_PROTOCOL_ERROR=1
      shift
      ;;
    --alert-wait)
      [[ $# -ge 2 ]] || die '--alert-wait requires a value'
      ALERT_WAIT_SECONDS="$2"
      shift 2
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

require_positive_int '--ready-timeout' "$READY_TIMEOUT"
require_positive_int '--clients' "$CLIENTS"
require_positive_int '--msgs' "$MESSAGES"
require_positive_int '--channels' "$CHANNELS"
require_positive_int '--top-limit' "$TOP_LIMIT"
require_nonnegative_int '--throughput' "$THROUGHPUT"
require_nonnegative_int '--connect-rate' "$CONNECT_RATE"
require_nonnegative_int '--warmup' "$WARMUP_SECONDS"
require_nonnegative_int '--top-wait' "$TOP_WAIT_SECONDS"
require_nonnegative_int '--alert-wait' "$ALERT_WAIT_SECONDS"

declare -a API_VALUES GATEWAY_VALUES
split_csv "$API_ADDRS" API_VALUES
split_csv "$GATEWAY_ADDRS" GATEWAY_VALUES

cleanup() {
  if [[ -n "$CLUSTER_PID" ]]; then
    log "stopping three-node cluster pid=$CLUSTER_PID"
    kill "$CLUSTER_PID" >/dev/null 2>&1 || true
    wait "$CLUSTER_PID" 2>/dev/null || true
    CLUSTER_PID=""
  fi
}

trap cleanup EXIT

write_summary() {
  mkdir -p "$OUT_DIR"
  cat >"$OUT_DIR/summary.md" <<SUMMARY
# wkcli top three-node validation

- generated_at: $(date -u '+%Y-%m-%dT%H:%M:%SZ')
- api: $API_ADDRS
- gateway: $GATEWAY_ADDRS
- top_health_status: $TOP_HEALTH_STATUS
- bench_status: $BENCH_STATUS
- exit_status: $EXIT_STATUS
- top_before: top-before.json
- bench_send: bench-send.json
- top_after_bench: top-after-bench.json
- top_health: top-health.txt
SUMMARY
  if [[ "$INJECT_PROTOCOL_ERROR" -eq 1 ]]; then
    printf '%s\n' "- top_after_injection: top-after-injection.json" >>"$OUT_DIR/summary.md"
  fi
}

ensure_wkcli_binary() {
  if [[ "$BUILD_WKCLI" -eq 0 ]]; then
    [[ -x "$WKCLI_BIN" ]] || die "--no-build requested but wkcli binary is not executable: $WKCLI_BIN"
    return
  fi
  mkdir -p "$(dirname "$WKCLI_BIN")"
  log "building wkcli: $WKCLI_BIN"
  (
    cd "$ROOT_DIR"
    go build -o "$WKCLI_BIN" ./cmd/wkcli
  )
}

start_cluster() {
  if [[ "$START_CLUSTER" -eq 0 ]]; then
    log "cluster startup disabled; using existing cluster"
    return
  fi
  [[ -x "$START_SCRIPT" ]] || die "start script is not executable: $START_SCRIPT"
  mkdir -p "$OUT_DIR"
  local clean_arg=()
  if [[ "$CLEAN_CLUSTER" -eq 1 ]]; then
    clean_arg=(--clean)
  fi
  log "starting three-node cluster with $START_SCRIPT"
  "$START_SCRIPT" "${clean_arg[@]}" --ready-timeout "$READY_TIMEOUT" >"$OUT_DIR/cluster-start.log" 2>&1 &
  CLUSTER_PID="$!"
}

check_cluster_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT))
  local api all_ready
  while (( SECONDS <= deadline )); do
    if [[ -n "$CLUSTER_PID" ]] && ! kill -0 "$CLUSTER_PID" 2>/dev/null; then
      tail -n 120 "$OUT_DIR/cluster-start.log" >&2 || true
      die "three-node cluster exited before becoming ready"
    fi
    all_ready=1
    for api in "${API_VALUES[@]}"; do
      if ! curl -fsS --max-time 3 "${api%/}/readyz" >/dev/null 2>&1; then
        all_ready=0
        break
      fi
    done
    if [[ "$all_ready" -eq 1 ]]; then
      log "cluster ready"
      return
    fi
    sleep 1
  done
  tail -n 120 "$OUT_DIR/cluster-start.log" >&2 || true
  die "timed out waiting for cluster readyz"
}

capture_top() {
  local out="$1"
  local args=(top --once --json --view all --window "$TOP_WINDOW" --limit "$TOP_LIMIT")
  local api
  for api in "${API_VALUES[@]}"; do
    args+=(--server "$api")
  done
  "$WKCLI_BIN" "${args[@]}" >"$out"
}

run_bench_send() {
  local out="$OUT_DIR/bench-send.json"
  local args=(bench send)
  local gateway
  for gateway in "${GATEWAY_VALUES[@]}"; do
    args+=(--gateway "$gateway")
  done
  args+=(
    --clients "$CLIENTS"
    --msgs "$MESSAGES"
    --channels "$CHANNELS"
    --channel-prefix "$CHANNEL_PREFIX"
    --channel-type "$CHANNEL_TYPE"
    --size "$PAYLOAD_SIZE"
    --throughput "$THROUGHPUT"
    --connect-rate "$CONNECT_RATE"
    --no-progress
    --json
  )
  "$WKCLI_BIN" "${args[@]}" >"$out"
}

check_top_no_gateway_session_error() {
  local file="$1"
  if go run "$ROOT_DIR/scripts/wkcli-top-json-check.go" \
    --top "$file" \
    --want-nodes "${#API_VALUES[@]}" \
    --require-ready \
    --require-resources \
    --forbid-alert gateway/session_error >>"$OUT_DIR/top-health.txt" 2>&1; then
    TOP_HEALTH_STATUS="passed"
    return 0
  fi
  TOP_HEALTH_STATUS="failed"
  return 1
}

check_top_requires_gateway_session_error() {
  local file="$1"
  if go run "$ROOT_DIR/scripts/wkcli-top-json-check.go" \
    --top "$file" \
    --want-nodes "${#API_VALUES[@]}" \
    --require-ready \
    --require-resources \
    --require-alert gateway/session_error >>"$OUT_DIR/top-health.txt" 2>&1; then
    TOP_HEALTH_STATUS="passed"
    return 0
  fi
  TOP_HEALTH_STATUS="failed"
  return 1
}

check_bench_send() {
  if go run "$ROOT_DIR/scripts/wkcli-top-json-check.go" \
    --bench "$OUT_DIR/bench-send.json" \
    --want-bench-messages "$MESSAGES" >>"$OUT_DIR/top-health.txt" 2>&1; then
    BENCH_STATUS="passed"
    return 0
  fi
  BENCH_STATUS="failed"
  return 1
}

inject_protocol_error() {
	mkdir -p "$OUT_DIR"
	local gateway index
	index=0
	for gateway in "${GATEWAY_VALUES[@]}"; do
		index=$((index + 1))
		go run "$ROOT_DIR/scripts/wkcli-top-protocol-error-probe.go" \
			--gateway "$gateway" \
			--uid "wkcli-top-probe-${index}" \
			--device "wkcli-top-probe-device-${index}" >>"$OUT_DIR/protocol-injection.log" 2>&1 || true
	done
}

mkdir -p "$OUT_DIR"
: >"$OUT_DIR/top-health.txt"
ensure_wkcli_binary
start_cluster
check_cluster_ready
if (( WARMUP_SECONDS > 0 )); then
  sleep "$WARMUP_SECONDS"
fi

log "capturing baseline top snapshot"
capture_top "$OUT_DIR/top-before.json"
if ! check_top_no_gateway_session_error "$OUT_DIR/top-before.json"; then
  EXIT_STATUS="$TOP_HEALTH_EXIT_STATUS"
  write_summary
  exit "$EXIT_STATUS"
fi

log "running wkcli bench send"
run_bench_send
if ! check_bench_send; then
  EXIT_STATUS="$BENCH_EXIT_STATUS"
  write_summary
  exit "$EXIT_STATUS"
fi

if (( TOP_WAIT_SECONDS > 0 )); then
  sleep "$TOP_WAIT_SECONDS"
fi
log "capturing post-bench top snapshot"
capture_top "$OUT_DIR/top-after-bench.json"
if ! check_top_no_gateway_session_error "$OUT_DIR/top-after-bench.json"; then
  EXIT_STATUS="$TOP_HEALTH_EXIT_STATUS"
  write_summary
  exit "$EXIT_STATUS"
fi

if [[ "$INJECT_PROTOCOL_ERROR" -eq 1 ]]; then
  log "injecting invalid protocol bytes"
  inject_protocol_error
  if (( ALERT_WAIT_SECONDS > 0 )); then
    sleep "$ALERT_WAIT_SECONDS"
  fi
  capture_top "$OUT_DIR/top-after-injection.json"
  if ! check_top_requires_gateway_session_error "$OUT_DIR/top-after-injection.json"; then
    EXIT_STATUS="$TOP_HEALTH_EXIT_STATUS"
    write_summary
    exit "$EXIT_STATUS"
  fi
fi

write_summary
log "validation evidence written to $OUT_DIR"
