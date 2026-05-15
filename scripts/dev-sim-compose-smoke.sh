#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="dev-sim"
STATUS_URL="${WK_DEV_SIM_STATUS_URL:-http://127.0.0.1:19091/status}"
READY_TIMEOUT="${WK_DEV_SIM_READY_TIMEOUT:-90}"
POLL_INTERVAL="${WK_DEV_SIM_POLL_INTERVAL:-2}"
UP_RETRIES="${WK_DEV_SIM_UP_RETRIES:-3}"
UP_RETRY_BACKOFF="${WK_DEV_SIM_UP_RETRY_BACKOFF:-2}"
LOG_TAIL="${WK_DEV_SIM_LOG_TAIL:-300}"
RUN_UP=1
BUILD=1
CHECK_LOGS=1
SERVICES=(wk-node1 wk-node2 wk-node3 wk-sim)
LOG_SERVICES=(wk-sim wk-node1 wk-node2 wk-node3)

usage() {
  cat <<'USAGE'
Usage: scripts/dev-sim-compose-smoke.sh [options]

Starts the local three-node Compose cluster with the dev-sim profile, waits for
wk-sim to report running simulated users with non-zero traffic, then checks
recent logs for simulator traffic and obvious panics.

Options:
  --no-build          Run docker compose up without --build.
  --no-up             Skip compose up and only check an already running stack.
  --skip-logs         Skip recent log inspection.
  --timeout SECONDS   Status wait timeout. Default: WK_DEV_SIM_READY_TIMEOUT or 90.
  --poll SECONDS      Status polling interval. Default: WK_DEV_SIM_POLL_INTERVAL or 2.
  --log-tail LINES    Recent log lines to inspect. Default: WK_DEV_SIM_LOG_TAIL or 300.
  -h, --help          Show this help.

Environment:
  WK_DEV_SIM_UP_RETRIES       compose up retry count for transient build/pull errors, default 3.
  WK_DEV_SIM_UP_RETRY_BACKOFF base seconds between compose up retries, default 2.
  WK_DEV_SIM_STATUS_URL       simulator status URL, default http://127.0.0.1:19091/status.
USAGE
}

log() {
  printf '[dev-sim-smoke] %s\n' "$*"
}

die() {
  printf '[dev-sim-smoke] ERROR: %s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-build)
      BUILD=0
      shift
      ;;
    --no-up)
      RUN_UP=0
      shift
      ;;
    --skip-logs)
      CHECK_LOGS=0
      shift
      ;;
    --timeout)
      [[ $# -ge 2 ]] || die '--timeout requires a value'
      READY_TIMEOUT="$2"
      shift 2
      ;;
    --poll)
      [[ $# -ge 2 ]] || die '--poll requires a value'
      POLL_INTERVAL="$2"
      shift 2
      ;;
    --log-tail)
      [[ $# -ge 2 ]] || die '--log-tail requires a value'
      LOG_TAIL="$2"
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

require_positive_uint '--timeout' "$READY_TIMEOUT"
require_uint '--poll' "$POLL_INTERVAL"
require_uint '--log-tail' "$LOG_TAIL"
require_positive_uint 'WK_DEV_SIM_UP_RETRIES' "$UP_RETRIES"
require_uint 'WK_DEV_SIM_UP_RETRY_BACKOFF' "$UP_RETRY_BACKOFF"

compose() {
  docker compose --profile "$PROFILE" "$@"
}

run_compose_up() {
  local attempt=1

  while (( attempt <= UP_RETRIES )); do
    log "compose up attempt ${attempt}/${UP_RETRIES}"
    if [[ "$BUILD" -eq 1 ]]; then
      if compose up -d --build "${SERVICES[@]}"; then
        return 0
      fi
    else
      if compose up -d "${SERVICES[@]}"; then
        return 0
      fi
    fi
    if (( attempt == UP_RETRIES )); then
      return 1
    fi
    sleep $(( attempt * UP_RETRY_BACKOFF ))
    attempt=$(( attempt + 1 ))
  done
}

json_string_field() {
  local json="$1"
  local field="$2"
  printf '%s' "$json" | tr -d '\n' | sed -nE "s/.*\"${field}\"[[:space:]]*:[[:space:]]*\"([^\"]*)\".*/\1/p" | head -n 1
}

json_number_field() {
  local json="$1"
  local field="$2"
  printf '%s' "$json" | tr -d '\n' | sed -nE "s/.*\"${field}\"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/p" | head -n 1
}

wait_status() {
  local deadline=$(( SECONDS + READY_TIMEOUT ))
  local last_status=''
  local last_error=''
  while (( SECONDS <= deadline )); do
    if last_status="$(curl -fsS --max-time 2 "$STATUS_URL" 2>&1)"; then
      local state connected sent error_text
      state="$(json_string_field "$last_status" state)"
      connected="$(json_number_field "$last_status" connected_users)"
      sent="$(json_number_field "$last_status" messages_sent)"
      error_text="$(json_string_field "$last_status" last_error)"
      connected="${connected:-0}"
      sent="${sent:-0}"
      log "status state=${state:-unknown} connected_users=${connected} messages_sent=${sent} last_error=${error_text:-}"
      if [[ "$state" == "running" ]] && (( connected > 0 )) && (( sent > 0 )); then
        return 0
      fi
    else
      last_error="$last_status"
      log "status not ready: $last_error"
    fi
    sleep "$POLL_INTERVAL"
  done
  die "timed out waiting for $STATUS_URL; last_status=${last_status:-<empty>} last_error=${last_error:-<empty>}"
}

check_logs() {
  local logs
  logs="$(compose logs "--tail=${LOG_TAIL}" "${LOG_SERVICES[@]}" 2>&1)" || die "docker compose logs failed: $logs"
  if printf '%s\n' "$logs" | grep -E 'panic:|assignment to entry in nil map' >/dev/null; then
    printf '%s\n' "$logs" >&2
    die 'recent logs contain panic markers'
  fi
  if ! printf '%s\n' "$logs" | grep -E 'sim-msg|delivery[.]diag[.]committed_route' >/dev/null; then
    printf '%s\n' "$logs" >&2
    die 'recent logs do not show simulator traffic markers'
  fi
}

cd "$ROOT_DIR"
if [[ "$RUN_UP" -eq 1 ]]; then
  run_compose_up || die 'docker compose up failed after retries'
fi
wait_status
if [[ "$CHECK_LOGS" -eq 1 ]]; then
  check_logs
fi
log 'dev-sim smoke passed'
