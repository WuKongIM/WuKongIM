#!/usr/bin/env bash
set -euo pipefail

: "${WK_CHAT_REPAIR_TOOL:?required}"
: "${WK_CHAT_REPAIR_STATE:?required}"
: "${WK_CHAT_REPAIR_OUTPUT_DIR:?required}"
: "${WK_CHAT_REPAIR_SSH_CONFIG:?required}"
: "${WK_CHAT_REPAIR_REQUEST_ID:?required}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
# shellcheck source=../cloud-sim/local-runtime.sh
source "$script_dir/../cloud-sim/local-runtime.sh"

poll_seconds="${WK_CHAT_REPAIR_POLL_SECONDS:-5}"
max_seconds="${WK_CHAT_REPAIR_MAX_SECONDS:-4500}"
stage_service="${WK_CHAT_REPAIR_SERVICE:-wkbench-rehearsal.service}"
[[ "$poll_seconds" =~ ^[1-9][0-9]*$ && "$max_seconds" =~ ^[1-9][0-9]*$ ]]
(( poll_seconds <= 30 && max_seconds >= 60 && max_seconds <= 260100 ))
[[ -x "$WK_CHAT_REPAIR_TOOL" && -f "$WK_CHAT_REPAIR_STATE" && -f "$WK_CHAT_REPAIR_SSH_CONFIG" ]]
[[ "$stage_service" =~ ^[A-Za-z0-9@._-]{1,128}\.service$ ]]
[[ "$WK_CHAT_REPAIR_REQUEST_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
if [[ -n "${WK_CHAT_REPAIR_OPERATOR_STOP_FILE:-}" ]]; then
  [[ "$WK_CHAT_REPAIR_OPERATOR_STOP_FILE" == /* && ! -L "$WK_CHAT_REPAIR_OPERATOR_STOP_FILE" ]]
fi

install -d -m 0700 "$WK_CHAT_REPAIR_OUTPUT_DIR"
work_dir="$(mktemp -d "$WK_CHAT_REPAIR_OUTPUT_DIR/.repair-monitor.XXXXXX")"
chmod 0700 "$work_dir"
cleanup() {
  find -P "$work_dir" -type f -delete 2>/dev/null || true
  rmdir -- "$work_dir" 2>/dev/null || true
}
trap cleanup EXIT

stop_stage() {
  wk_run_bounded 60 ssh -F "$WK_CHAT_REPAIR_SSH_CONFIG" wukong-load \
    "sudo systemctl stop '$stage_service' wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service &&
     ! sudo systemctl is-active --quiet '$stage_service' &&
     ! sudo systemctl is-active --quiet wkbench-worker@1.service &&
     ! sudo systemctl is-active --quiet wkbench-worker@2.service &&
     ! sudo systemctl is-active --quiet wkbench-worker@3.service" >/dev/null 2>&1
}
trap 'stop_stage || true; exit 130' INT TERM

operator_stop_requested() {
  if [[ -n "${WK_CHAT_REPAIR_OPERATOR_STOP_FILE:-}" ]]; then
    [[ -f "$WK_CHAT_REPAIR_OPERATOR_STOP_FILE" && ! -L "$WK_CHAT_REPAIR_OPERATOR_STOP_FILE" ]]
    return
  fi
  "$script_dir/operator-stop-requested.sh" "$WK_CHAT_REPAIR_REQUEST_ID" >/dev/null
}

query_stage_service_state() {
  local attempt candidate=''
  for attempt in 1 2 3; do
    candidate=''
    if candidate="$(wk_run_bounded 5 ssh -F "$WK_CHAT_REPAIR_SSH_CONFIG" wukong-load \
      "sudo systemctl is-active '$stage_service' || true" 2>/dev/null)"; then
      candidate="${candidate//$'\r'/}"
      if [[ "$candidate" != *$'\n'* && ${#candidate} -le 64 ]]; then
        case "$candidate" in
          active|activating|reloading|refreshing|inactive|failed|deactivating|maintenance|unknown)
            printf '%s\n' "$candidate"
            return 0
            ;;
        esac
      fi
    fi
    if (( attempt < 3 )); then
      sleep 1
    fi
  done
  return 1
}

remote_get() {
  local port="$1" path="$2" output="$3" temporary
  [[ "$port" =~ ^1909[1-3]$ ]]
  [[ "$path" == /v1/chat-lifecycle/status || "$path" == /v1/chat-lifecycle/snapshot ]]
  temporary="${output}.next"
  rm -f -- "$temporary"
  if ! wk_run_bounded 45 ssh -F "$WK_CHAT_REPAIR_SSH_CONFIG" wukong-load \
    "sudo bash -euo pipefail -c 'source /etc/wukongim/secrets/load.env; exec curl --silent --show-error --fail --max-time 20 -H \"Authorization: Bearer \${WK_BENCH_WORKER_TOKEN}\" http://127.0.0.1:${port}${path}'" \
    >"$temporary"; then
    rm -f -- "$temporary"
    return 1
  fi
  size="$(stat -c '%s' "$temporary" 2>/dev/null || stat -f '%z' "$temporary")"
  [[ -s "$temporary" && "$size" =~ ^[1-9][0-9]*$ && "$size" -le 8192 ]] || {
    rm -f -- "$temporary"
    return 1
  }
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$output"
}

record_diagnosis() {
  local reason="$1" observed_at="$2" service_state journal_digest
  service_state="$(query_stage_service_state || true)"
  journal_digest="$(wk_run_bounded 30 ssh -F "$WK_CHAT_REPAIR_SSH_CONFIG" wukong-load \
    "sudo journalctl -u '$stage_service' --no-pager -n 500 -o cat | sha256sum | awk '{print \$1}'" \
    2>/dev/null | head -c 64 || true)"
  [[ "$journal_digest" =~ ^[0-9a-f]{64}$ ]] || journal_digest=''
  jq -n --arg schema 'wukongim.chat_lifecycle.repair_diagnosis/v1' \
    --arg reason "$reason" --arg observed_at "$observed_at" \
    --arg service_state "$service_state" --arg journal_sha256 "$journal_digest" \
    '{schema:$schema,reason:$reason,observed_at:$observed_at,
      service_state:$service_state,journal_sha256:$journal_sha256}' \
    >"$WK_CHAT_REPAIR_OUTPUT_DIR/repair-diagnosis.json"
  chmod 0600 "$WK_CHAT_REPAIR_OUTPUT_DIR/repair-diagnosis.json"
}

persist_terminal_cut() {
  local terminal_dir worker
  terminal_dir="$WK_CHAT_REPAIR_OUTPUT_DIR/terminal-cut"
  install -d -m 0700 "$terminal_dir"
  for worker in 1 2 3; do
    install -m 0600 "$work_dir/status-${worker}.json" "$terminal_dir/status-${worker}.json"
    install -m 0600 "$work_dir/snapshot-${worker}.json" "$terminal_dir/snapshot-${worker}.json"
  done
}

persist_observation_failure() {
  local reason="$1" observed_at="$2" attempt="$3" failure_dir worker source
  failure_dir="$WK_CHAT_REPAIR_OUTPUT_DIR/observation-failure"
  install -d -m 0700 "$failure_dir"
  for worker in 1 2 3; do
    for source in status snapshot; do
      if [[ -f "$work_dir/${source}-${worker}.json" && ! -L "$work_dir/${source}-${worker}.json" ]]; then
        install -m 0600 "$work_dir/${source}-${worker}.json" "$failure_dir/${source}-${worker}.json"
      else
        rm -f -- "$failure_dir/${source}-${worker}.json"
      fi
    done
  done
  jq -n --arg schema 'wukongim.chat_lifecycle.repair_observation_failure/v1' \
    --arg reason "$reason" --arg observed_at "$observed_at" --argjson attempt "$attempt" \
    '{schema:$schema,reason:$reason,observed_at:$observed_at,attempt:$attempt}' \
    >"$failure_dir/failure.json"
  chmod 0600 "$failure_dir/failure.json"
}

seal_abort() {
  local reason="$1" observed_at="$2"
  "$WK_CHAT_REPAIR_TOOL" repair-abort --state "$WK_CHAT_REPAIR_STATE" \
    --observed-at "$observed_at" --reason "$reason" \
    >"$WK_CHAT_REPAIR_OUTPUT_DIR/repair-decision.json"
  jq -c .state "$WK_CHAT_REPAIR_OUTPUT_DIR/repair-decision.json" >"${WK_CHAT_REPAIR_STATE}.next"
  chmod 0600 "${WK_CHAT_REPAIR_STATE}.next"
  mv -f -- "${WK_CHAT_REPAIR_STATE}.next" "$WK_CHAT_REPAIR_STATE"
  if ! stop_stage; then
    record_diagnosis "$reason" "$observed_at"
    return 20
  fi
  record_diagnosis "$reason" "$observed_at"
  return 10
}

started_epoch="$(date -u +%s)"
deadline_epoch=$(( started_epoch + max_seconds ))
while true; do
  observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  operator_stop_status=0
  operator_stop_requested || operator_stop_status=$?
  case "$operator_stop_status" in
    0)
      seal_abort operator_stop "$observed_at" || true
      exit 130
      ;;
    1) ;;
    *)
      seal_abort observation_unavailable "$observed_at" || true
      exit 2
      ;;
  esac
  if (( $(date -u +%s) >= deadline_epoch )); then
    seal_abort monitor_timeout "$observed_at"
    exit $?
  fi
  if ! service_state="$(query_stage_service_state)"; then
    seal_abort observation_unavailable "$observed_at"
    exit $?
  fi
  if [[ "$service_state" != active && "$service_state" != activating &&
    "$service_state" != reloading && "$service_state" != refreshing ]]; then
    seal_abort service_inactive "$observed_at"
    exit $?
  fi

  capture_succeeded=false
  for capture_attempt in 1 2 3; do
    observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    capture_args=(repair-capture --state "$WK_CHAT_REPAIR_STATE" --observed-at "$observed_at")
    capture_failed=false
    for worker in 1 2 3; do
      port=$(( 19090 + worker ))
      status_path="$work_dir/status-${worker}.json"
      snapshot_path="$work_dir/snapshot-${worker}.json"
      rm -f -- "$status_path" "$snapshot_path"
      remote_get "$port" /v1/chat-lifecycle/status "$status_path" || capture_failed=true
      remote_get "$port" /v1/chat-lifecycle/snapshot "$snapshot_path" || capture_failed=true
      capture_args+=(--worker-status "$status_path" --worker-snapshot "$snapshot_path")
    done
    if [[ "$capture_failed" == true ]]; then
      persist_observation_failure remote_fetch_failed "$observed_at" "$capture_attempt"
    elif "$WK_CHAT_REPAIR_TOOL" "${capture_args[@]}" >"$work_dir/observation.json"; then
      capture_succeeded=true
      break
    else
      persist_observation_failure strict_capture_rejected "$observed_at" "$capture_attempt"
    fi
    if (( capture_attempt < 3 )); then
      sleep 1
    fi
  done
  if [[ "$capture_succeeded" != true ]]; then
    seal_abort observation_unavailable "$observed_at"
    exit $?
  fi
  jq -c . "$work_dir/observation.json" >>"$WK_CHAT_REPAIR_OUTPUT_DIR/repair-observations.jsonl"
  chmod 0600 "$WK_CHAT_REPAIR_OUTPUT_DIR/repair-observations.jsonl"
  "$WK_CHAT_REPAIR_TOOL" repair-observe --state "$WK_CHAT_REPAIR_STATE" \
    --observation "$work_dir/observation.json" >"$work_dir/step.json" || {
      persist_observation_failure strict_observe_rejected "$observed_at" 1
      seal_abort observation_unavailable "$observed_at"
      exit $?
    }
  jq -c .state "$work_dir/step.json" >"${WK_CHAT_REPAIR_STATE}.next"
  chmod 0600 "${WK_CHAT_REPAIR_STATE}.next"
  mv -f -- "${WK_CHAT_REPAIR_STATE}.next" "$WK_CHAT_REPAIR_STATE"
  action="$(jq -er .decision.action "$work_dir/step.json")"
  case "$action" in
    continue)
      sleep "$poll_seconds"
      ;;
    stop_and_diagnose)
      persist_terminal_cut
      install -m 0600 "$work_dir/step.json" "$WK_CHAT_REPAIR_OUTPUT_DIR/repair-decision.json"
      reason="$(jq -er .decision.reason "$work_dir/step.json")"
      if ! stop_stage; then
        record_diagnosis "$reason" "$observed_at"
        exit 20
      fi
      record_diagnosis "$reason" "$observed_at"
      exit 10
      ;;
    qualified)
      persist_terminal_cut
      install -m 0600 "$work_dir/step.json" "$WK_CHAT_REPAIR_OUTPUT_DIR/repair-decision.json"
      if ! stop_stage; then
        record_diagnosis service_inactive "$observed_at"
        exit 20
      fi
      exit 0
      ;;
    *)
      seal_abort observation_unavailable "$observed_at"
      exit $?
      ;;
  esac
done
