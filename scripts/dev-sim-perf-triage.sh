#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROFILE="dev-sim"
STATUS_URL="${WK_DEV_SIM_STATUS_URL:-http://127.0.0.1:19091/status}"
DURATION="${WK_DEV_SIM_TRIAGE_DURATION:-120}"
POLL_INTERVAL="${WK_DEV_SIM_TRIAGE_POLL_INTERVAL:-2}"
PROFILE_SECONDS="${WK_DEV_SIM_TRIAGE_PROFILE_SECONDS:-30}"
OUT_BASE="${WK_DEV_SIM_TRIAGE_OUT_DIR:-docs/development/perf-runs}"
LOG_TAIL="${WK_DEV_SIM_TRIAGE_LOG_TAIL:-2000}"
CLUSTER_DATA_DIR="${WK_DEV_SIM_TRIAGE_CLUSTER_DATA_DIR:-$ROOT_DIR/docker/dev-cluster}"
BUILD=1
RUN_UP=1
CLEAN=0
SCENARIO=""
SERVICES=(wk-node1 wk-node2 wk-node3 wk-sim)

usage() {
  cat <<'USAGE'
Usage: scripts/dev-sim-perf-triage.sh <scenario> [options]

Runs a wk-sim Docker Compose scenario and stores evidence for later analysis.

Scenarios:
  smoke-default         Compose default mixed workload.
  sampled-correctness  Small sampled-receive correctness workload.
  person-hotpath       Person-channel send path isolation workload.
  group-fanout         Group fanout isolation workload.
  mixed-highrate       Higher-rate mixed contention workload.
  custom               Use caller-provided WK_SIM_* environment variables.

Options:
  --duration SECONDS        Evidence sampling duration. Default: WK_DEV_SIM_TRIAGE_DURATION or 120.
  --poll SECONDS            Status/docker-stats polling interval. Default: WK_DEV_SIM_TRIAGE_POLL_INTERVAL or 2.
  --profile-seconds SECONDS CPU pprof duration per node. Default: WK_DEV_SIM_TRIAGE_PROFILE_SECONDS or 30.
  --out-dir DIR             Base evidence directory. Default: docs/development/perf-runs.
  --log-tail LINES          Lines per service log. Default: WK_DEV_SIM_TRIAGE_LOG_TAIL or 2000.
  --no-build                Run docker compose up without --build.
  --no-up                   Skip docker compose up and collect from an already running stack.
  --clean                   Stop Compose and remove docker/dev-cluster plus docker/dev-sim before run.
  -h, --help                Show this help.

The script exits non-zero only after writing evidence when final /status is not
healthy or reports send/recv errors.
USAGE
}

log() {
  printf '[dev-sim-triage] %s\n' "$*"
}

die() {
  printf '[dev-sim-triage] ERROR: %s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --duration)
      [[ $# -ge 2 ]] || die '--duration requires a value'
      DURATION="$2"
      shift 2
      ;;
    --poll)
      [[ $# -ge 2 ]] || die '--poll requires a value'
      POLL_INTERVAL="$2"
      shift 2
      ;;
    --profile-seconds)
      [[ $# -ge 2 ]] || die '--profile-seconds requires a value'
      PROFILE_SECONDS="$2"
      shift 2
      ;;
    --out-dir)
      [[ $# -ge 2 ]] || die '--out-dir requires a value'
      OUT_BASE="$2"
      shift 2
      ;;
    --log-tail)
      [[ $# -ge 2 ]] || die '--log-tail requires a value'
      LOG_TAIL="$2"
      shift 2
      ;;
    --no-build)
      BUILD=0
      shift
      ;;
    --no-up)
      RUN_UP=0
      shift
      ;;
    --clean)
      CLEAN=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      die "unknown option: $1"
      ;;
    *)
      if [[ -n "$SCENARIO" ]]; then
        die "unexpected argument: $1"
      fi
      SCENARIO="$1"
      shift
      ;;
  esac
done

[[ -n "$SCENARIO" ]] || die 'scenario is required'

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

require_uint '--duration' "$DURATION"
require_uint '--poll' "$POLL_INTERVAL"
require_positive_uint '--profile-seconds' "$PROFILE_SECONDS"
require_uint '--log-tail' "$LOG_TAIL"
if (( DURATION > 0 && POLL_INTERVAL == 0 )); then
  die '--poll must be greater than zero when --duration is greater than zero'
fi

timestamp() {
  if [[ -n "${WK_DEV_SIM_TRIAGE_TIMESTAMP:-}" ]]; then
    printf '%s' "$WK_DEV_SIM_TRIAGE_TIMESTAMP"
  else
    date +%Y%m%d-%H%M%S
  fi
}

iso_now() {
  date -u +%Y-%m-%dT%H:%M:%SZ
}

set_default() {
  local name="$1"
  local value="$2"
  if [[ -z "${!name:-}" ]]; then
    export "$name=$value"
  fi
}

apply_scenario_defaults() {
  local ts="$1"
  case "$SCENARIO" in
    smoke-default)
      set_default WK_SIM_UID_PREFIX "smoke-default-${ts}"
      ;;
    sampled-correctness)
      set_default WK_SIM_USERS 40
      set_default WK_SIM_PERSON_CHANNELS 10
      set_default WK_SIM_GROUP_CHANNELS 3
      set_default WK_SIM_GROUP_MEMBERS 12
      set_default WK_SIM_RATE '0.5/s'
      set_default WK_SIM_TRAFFIC_CONCURRENCY 16
      set_default WK_SIM_VERIFY_RECV sampled
      set_default WK_SIM_UID_PREFIX "sampled-correctness-${ts}"
      ;;
    person-hotpath)
      set_default WK_SIM_USERS 500
      set_default WK_SIM_PERSON_CHANNELS 250
      set_default WK_SIM_GROUP_CHANNELS 0
      set_default WK_SIM_RATE '1/s'
      set_default WK_SIM_TRAFFIC_CONCURRENCY 256
      set_default WK_SIM_VERIFY_RECV none
      set_default WK_SIM_UID_PREFIX "person-hotpath-${ts}"
      ;;
    group-fanout)
      set_default WK_SIM_USERS 500
      set_default WK_SIM_PERSON_CHANNELS 0
      set_default WK_SIM_GROUP_CHANNELS 250
      set_default WK_SIM_GROUP_MEMBERS 10
      set_default WK_SIM_RATE '1/s'
      set_default WK_SIM_TRAFFIC_CONCURRENCY 256
      set_default WK_SIM_VERIFY_RECV none
      set_default WK_SIM_UID_PREFIX "group-fanout-${ts}"
      ;;
    mixed-highrate)
      set_default WK_SIM_USERS 500
      set_default WK_SIM_PERSON_CHANNELS 250
      set_default WK_SIM_GROUP_CHANNELS 250
      set_default WK_SIM_GROUP_MEMBERS 10
      set_default WK_SIM_RATE '1/s'
      set_default WK_SIM_TRAFFIC_CONCURRENCY 256
      set_default WK_SIM_VERIFY_RECV none
      set_default WK_SIM_UID_PREFIX "mixed-highrate-${ts}"
      ;;
    custom)
      set_default WK_SIM_UID_PREFIX "custom-${ts}"
      ;;
    *)
      die "unknown scenario: $SCENARIO"
      ;;
  esac
}

compose() {
  docker compose --profile "$PROFILE" "$@"
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

collect_url() {
  local url="$1"
  local out="$2"
  local timeout="$3"
  if ! curl -fsS --max-time "$timeout" "$url" > "$out" 2> "${out}.err"; then
    log "failed to collect $url; see ${out}.err"
    return 0
  fi
  rm -f "${out}.err"
}

collect_status_sample() {
  local out="$1"
  local status
  if status="$(curl -fsS --max-time 5 "$STATUS_URL" 2>&1)"; then
    status="$(printf '%s' "$status" | tr -d '\n')"
    final_status="$status"
    printf '{"sample_time":"%s","status":%s}\n' "$(iso_now)" "$status" >> "$out"
  else
    printf '{"sample_time":"%s","error":"%s"}\n' "$(iso_now)" "$(printf '%s' "$status" | tr -d '\"\\\n')" >> "$out"
  fi
}

collect_docker_stats_sample() {
  local out="$1"
  docker stats --no-stream --format '{{json .}}' >> "$out" 2>> "${out}.err" || true
}

run_compose_up() {
  if [[ "$BUILD" -eq 1 ]]; then
    compose up -d --build "${SERVICES[@]}"
  else
    compose up -d "${SERVICES[@]}"
  fi
}

write_env_file() {
  local out="$1"
  {
    printf 'SCENARIO=%s\n' "$SCENARIO"
    printf 'RUN_DIR=%s\n' "$RUN_DIR"
    printf 'CLUSTER_DATA_DIR=%s\n' "$CLUSTER_DATA_DIR"
    printf 'DURATION=%s\n' "$DURATION"
    printf 'POLL_INTERVAL=%s\n' "$POLL_INTERVAL"
    printf 'PROFILE_SECONDS=%s\n' "$PROFILE_SECONDS"
    env | LC_ALL=C sort | grep '^WK_SIM_' || true
  } > "$out"
}

write_git_file() {
  local out="$1"
  {
    git rev-parse --abbrev-ref HEAD 2>/dev/null || true
    git rev-parse HEAD 2>/dev/null || true
    git status --short 2>/dev/null || true
  } > "$out"
}

collect_static_evidence() {
  compose config > "$RUN_DIR/compose-config.yml" 2> "$RUN_DIR/compose-config.err" || true
  write_env_file "$RUN_DIR/env.txt"
  write_git_file "$RUN_DIR/git.txt"
}

collect_runtime_samples() {
  local status_out="$RUN_DIR/status.jsonl"
  local stats_out="$RUN_DIR/docker-stats.jsonl"
  local deadline=$(( SECONDS + DURATION ))

  : > "$status_out"
  : > "$stats_out"
  while true; do
    collect_status_sample "$status_out"
    collect_docker_stats_sample "$stats_out"
    (( SECONDS >= deadline )) && break
    sleep "$POLL_INTERVAL"
  done
}

collect_logs() {
  local service
  for service in "${SERVICES[@]}"; do
    compose logs --no-color "--tail=${LOG_TAIL}" "$service" > "$RUN_DIR/logs/compose/${service}.log" 2> "$RUN_DIR/logs/compose/${service}.err" || true
  done
  collect_node_log_files
  collect_sim_severity_logs
}

copy_log_or_empty() {
  local src="$1"
  local dst="$2"
  if [[ -f "$src" ]]; then
    cp "$src" "$dst"
  else
    : > "$dst"
  fi
}

extract_warn_lines() {
  local src="$1"
  local dst="$2"
  if [[ -f "$src" ]]; then
    grep -Ei '"level"[[:space:]]*:[[:space:]]*"warn"|"level"[[:space:]]*:[[:space:]]*"warning"|(^|[^[:alpha:]])warn(ing)?([^[:alpha:]]|$)' "$src" > "$dst" || : > "$dst"
  else
    : > "$dst"
  fi
}

extract_error_lines() {
  local src="$1"
  local dst="$2"
  if [[ -f "$src" ]]; then
    grep -Ei '"level"[[:space:]]*:[[:space:]]*"error"|(^|[^[:alpha:]])error([^[:alpha:]]|$)|panic:' "$src" > "$dst" || : > "$dst"
  else
    : > "$dst"
  fi
}

collect_node_log_files() {
  local id service src_dir app_src error_src debug_src warn_src
  for id in 1 2 3; do
    service="wk-node${id}"
    src_dir="$CLUSTER_DATA_DIR/node${id}/logs"
    app_src="$src_dir/app.log"
    error_src="$src_dir/error.log"
    debug_src="$src_dir/debug.log"
    warn_src="$src_dir/warn.log"

    copy_log_or_empty "$app_src" "$RUN_DIR/logs/app/${service}.log"
    copy_log_or_empty "$error_src" "$RUN_DIR/logs/error/${service}.log"
    copy_log_or_empty "$debug_src" "$RUN_DIR/logs/debug/${service}.log"
    if [[ -f "$warn_src" ]]; then
      cp "$warn_src" "$RUN_DIR/logs/warn/${service}.log"
    else
      extract_warn_lines "$app_src" "$RUN_DIR/logs/warn/${service}.log"
    fi
  done
}

collect_sim_severity_logs() {
  local sim_log="$RUN_DIR/logs/compose/wk-sim.log"
  extract_error_lines "$sim_log" "$RUN_DIR/logs/error/wk-sim.log"
  extract_warn_lines "$sim_log" "$RUN_DIR/logs/warn/wk-sim.log"
}

collect_node_http_evidence() {
  local ids=(1 2 3)
  local ports=(15001 15002 15003)
  local i id port base cpu_timeout
  cpu_timeout=$(( PROFILE_SECONDS + 10 ))
  for i in "${!ids[@]}"; do
    id="${ids[$i]}"
    port="${ports[$i]}"
    base="http://127.0.0.1:${port}"
    collect_url "${base}/metrics" "$RUN_DIR/metrics/node${id}.prom" 10
    collect_url "${base}/debug/pprof/goroutine?debug=2" "$RUN_DIR/pprof/node${id}-goroutine.txt" 10
    collect_url "${base}/debug/pprof/heap" "$RUN_DIR/pprof/node${id}-heap.pb.gz" 10
    collect_url "${base}/debug/pprof/profile?seconds=${PROFILE_SECONDS}" "$RUN_DIR/pprof/node${id}-cpu.pb.gz" "$cpu_timeout"
  done
}

write_summary() {
  local out="$RUN_DIR/summary.md"
  local state connected active reconnected sent send_errors recv_errors last_error result
  state="$(json_string_field "$final_status" state)"
  connected="$(json_number_field "$final_status" connected_users)"
  active="$(json_number_field "$final_status" active_users)"
  reconnected="$(json_number_field "$final_status" reconnected_users)"
  sent="$(json_number_field "$final_status" messages_sent)"
  send_errors="$(json_number_field "$final_status" send_errors)"
  recv_errors="$(json_number_field "$final_status" recv_errors)"
  last_error="$(json_string_field "$final_status" last_error)"
  connected="${connected:-0}"
  active="${active:-$connected}"
  reconnected="${reconnected:-0}"
  sent="${sent:-0}"
  send_errors="${send_errors:-0}"
  recv_errors="${recv_errors:-0}"
  result=FAIL
  if [[ "$state" == "running" ]] && (( connected > 0 )) && (( active > 0 )) && (( sent > 0 )) && (( send_errors == 0 )) && (( recv_errors == 0 )) && [[ -z "$last_error" ]]; then
    result=PASS
  fi

  cat > "$out" <<EOF_SUMMARY
# dev-sim performance triage

## Scenario
- workload: ${SCENARIO}
- clean or accumulated: $(if [[ "$CLEAN" -eq 1 ]]; then printf 'clean'; else printf 'accumulated'; fi)
- duration: ${DURATION}s
- success criteria: final state running with connected users, sent messages, send_errors=0, recv_errors=0, and empty last_error

## Evidence
- status: status.jsonl
- compose logs: logs/compose/
- app logs: logs/app/
- error logs: logs/error/
- warn logs: logs/warn/
- debug logs: logs/debug/
- metrics: metrics/
- pprof: pprof/
- docker stats: docker-stats.jsonl

## Result
- result: ${result}
- state: ${state:-unknown}
- connected_users: ${connected}
- active_users: ${active}
- reconnected_users: ${reconnected}
- messages_sent: ${sent}
- send_errors: ${send_errors}
- recv_errors: ${recv_errors}
- last_error: ${last_error}

## Classification
- category: pending
- confidence: pending
- reason: evidence captured only; classify before tuning or code changes

## Hypothesis
- hypothesis: pending
- falsification test: pending

## Next Experiment
- one variable to change: pending
- expected result: pending
- stop condition: pending

## Fix Eligibility
- code change needed: pending
- reason: pending
- required regression test: pending
EOF_SUMMARY

  [[ "$result" == "PASS" ]]
}

cd "$ROOT_DIR"
RUN_TS="$(timestamp)"
apply_scenario_defaults "$RUN_TS"
RUN_DIR="$OUT_BASE/${RUN_TS}-${SCENARIO}"
mkdir -p "$RUN_DIR/logs/compose" "$RUN_DIR/logs/app" "$RUN_DIR/logs/error" "$RUN_DIR/logs/warn" "$RUN_DIR/logs/debug" "$RUN_DIR/metrics" "$RUN_DIR/pprof"
final_status=""

log "scenario=${SCENARIO} run_dir=${RUN_DIR}"
if [[ "$CLEAN" -eq 1 ]]; then
  log 'cleaning docker Compose development data'
  compose down >/dev/null 2>&1 || true
  rm -rf "$ROOT_DIR/docker/dev-cluster" "$ROOT_DIR/docker/dev-sim"
fi

collect_static_evidence
if [[ "$RUN_UP" -eq 1 ]]; then
  run_compose_up
fi
collect_runtime_samples
collect_logs
collect_node_http_evidence

if write_summary; then
  log "triage evidence collected: $RUN_DIR"
else
  log "triage captured failing evidence: $RUN_DIR"
  exit 1
fi
