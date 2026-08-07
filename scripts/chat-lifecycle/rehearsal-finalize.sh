#!/usr/bin/env bash
set -euo pipefail

: "${WK_CHAT_HANDOFF_DIR:?required}"
: "${WK_CHAT_FINAL_DIR:?required}"
: "${WK_CHAT_DEPLOYMENT_KEY:?required}"
: "${WK_CHAT_TOOL:?required}"
: "${WK_CHAT_REQUEST_ID:?required}"

operator_stop="${WK_CHAT_OPERATOR_STOP:-false}"
[[ "$operator_stop" == true || "$operator_stop" == false ]]

for name in handoff.json deployment-plan.json run-start.json release-selector.json; do
  [[ -f "$WK_CHAT_HANDOFF_DIR/$name" ]]
done
jq -e --arg request_id "$WK_CHAT_REQUEST_ID" '
  .schema == "wukongim.chat_lifecycle.rehearsal_handoff/v1" and
  .request_id == $request_id and (.attempt == 1 or .attempt == 2) and
  (.source_sha | test("^[0-9a-f]{40}$")) and
  (.bundle_digest | test("^sha256:[0-9a-f]{64}$")) and
  (.started_at | type == "string") and (.expected_end_at | type == "string")
' "$WK_CHAT_HANDOFF_DIR/handoff.json" >/dev/null
request_id="$(jq -er '.request_id' "$WK_CHAT_HANDOFF_DIR/handoff.json")"
expected_end="$(jq -er '.expected_end_at' "$WK_CHAT_HANDOFF_DIR/handoff.json")"
expected_epoch="$(date -u -d "$expected_end" +%s)"

install -d -m 0700 "$WK_CHAT_FINAL_DIR"
export WK_CLOUD_LOAD_PUBLIC_IP="$(jq -er '.hosts[] | select(.role == "load") | .public_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SERVICE1_IP="$(jq -er '.hosts[] | select(.role == "service-1") | .private_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SERVICE2_IP="$(jq -er '.hosts[] | select(.role == "service-2") | .private_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SERVICE3_IP="$(jq -er '.hosts[] | select(.role == "service-3") | .private_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SSH_KEY="$WK_CHAT_DEPLOYMENT_KEY"
export WK_CLOUD_SSH_CONFIG="$WK_CHAT_FINAL_DIR/deployment-ssh-config"
scripts/cloud-deployment/write-ssh-config.sh

remote_json=/var/lib/wukongim-cloud/reports/rehearsal/final.json
remote_markdown=/var/lib/wukongim-cloud/reports/rehearsal/final.md
remote_report_max_bytes=4194304
remote_timeout=60
[[ "$operator_stop" == true ]] && remote_timeout=15

remote_report_exists() {
  timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
    "sudo test -f '$remote_json' && sudo test -s '$remote_json' && sudo test -f '$remote_markdown' && sudo test -s '$remote_markdown'"
}

fetch_bounded_remote_report() {
  local remote_path="$1"
  local local_path="$2"
  timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
    "sudo head -c $(( remote_report_max_bytes + 1 )) -- '$remote_path'" >"$local_path" &&
    [[ "$(stat --format='%s' "$local_path")" -le "$remote_report_max_bytes" ]]
}

operator_stop_budget_seconds=600
operator_stop_deadline=$(( $(date -u +%s) + operator_stop_budget_seconds - 180 ))
if [[ "$operator_stop" == true ]]; then
  stop_state="$(timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
    'sudo systemctl is-active wkbench-rehearsal.service || true' 2>/dev/null || printf unreachable)"
  if [[ "$stop_state" == active || "$stop_state" == activating ]]; then
    timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
      "sudo systemctl kill --kill-who=main --signal=SIGTERM wkbench-rehearsal.service || true"
    while [[ "$(date -u +%s)" -lt "$operator_stop_deadline" ]]; do
      remote_report_exists && break
      stop_state="$(timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        'sudo systemctl is-active wkbench-rehearsal.service || true' 2>/dev/null || printf unreachable)"
      [[ "$stop_state" == active || "$stop_state" == activating ]] || break
      sleep 5
    done
    stop_state="$(timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
      'sudo systemctl is-active wkbench-rehearsal.service || true' 2>/dev/null || printf unreachable)"
    if [[ ( "$stop_state" == active || "$stop_state" == activating ) ]] && ! remote_report_exists; then
      timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        "sudo systemctl kill --kill-who=main --signal=SIGKILL wkbench-rehearsal.service || true; \
         sudo systemctl stop wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service || true"
    fi
  fi
fi

report_ready=false
if remote_report_exists; then
  report_ready=true
fi
state=unreachable
if observed_state="$(timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo systemctl is-active wkbench-rehearsal.service || true')"; then
  state="$observed_state"
fi
if [[ "$operator_stop" != true && "$report_ready" != true && "$state" == unreachable && "$(date -u +%s)" -lt "$expected_epoch" ]]; then
  printf '%s\n' not_ready
  exit 0
fi
if [[ "$operator_stop" != true && "$report_ready" != true && "$state" == active && "$(date -u +%s)" -lt "$expected_epoch" ]]; then
  printf '%s\n' not_ready
  exit 0
fi
if [[ "$operator_stop" != true && "$report_ready" != true && "$state" == active ]]; then
  deadline=$(( $(date -u +%s) + 900 ))
  while [[ "$(date -u +%s)" -lt "$deadline" ]]; do
    if remote_report_exists; then
      report_ready=true
      break
    fi
    state=unreachable
    if observed_state="$(timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo systemctl is-active wkbench-rehearsal.service || true')"; then
      state="$observed_state"
    fi
    [[ "$state" == active ]] || break
    sleep 15
  done
fi

validated_report=false
if [[ "$report_ready" == true ]] &&
  fetch_bounded_remote_report "$remote_json" "$WK_CHAT_FINAL_DIR/final.json" &&
  fetch_bounded_remote_report "$remote_markdown" "$WK_CHAT_FINAL_DIR/final.md" &&
  "$WK_CHAT_TOOL" validate-rehearsal-report --report "$WK_CHAT_FINAL_DIR/final.json" \
    >"$WK_CHAT_FINAL_DIR/rehearsal-result.json" 2>/dev/null; then
  validated_report=true
  outcome="$(jq -er .outcome "$WK_CHAT_FINAL_DIR/rehearsal-result.json")"
  cause="$(jq -er .cause "$WK_CHAT_FINAL_DIR/rehearsal-result.json")"
else
  if [[ -e "$WK_CHAT_FINAL_DIR/final.json" || -e "$WK_CHAT_FINAL_DIR/final.md" ]]; then
    {
      printf 'report_validation=failed\n'
      for path in "$WK_CHAT_FINAL_DIR/final.json" "$WK_CHAT_FINAL_DIR/final.md"; do
        if [[ -f "$path" ]]; then
          stat --format='%n bytes=%s' "$path"
          sha256sum "$path"
        fi
      done
    } >"$WK_CHAT_FINAL_DIR/report-validation.txt"
    rm -f "$WK_CHAT_FINAL_DIR/final.json" "$WK_CHAT_FINAL_DIR/final.md" "$WK_CHAT_FINAL_DIR/rehearsal-result.json"
  fi
  {
    printf 'request_id=%s\n' "$request_id"
    printf 'expected_end_at=%s\n' "$expected_end"
    printf 'observed_state=%s\n' "$state"
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo systemctl show wkbench-rehearsal.service --property=ActiveState,SubState,Result,ExecMainCode,ExecMainStatus,NRestarts'
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo journalctl -u wkbench-rehearsal.service --no-pager -n 500 | sha256sum'
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo journalctl -u wkbench-rehearsal.service --no-pager -n 500 | wc -l'
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'df -B1 / /var/lib/wukongim-cloud'
  } >"$WK_CHAT_FINAL_DIR/diagnostics.txt" 2>&1 || true
  if [[ "$operator_stop" == true ]]; then
    outcome=operator_stop
    cause=operator_requested_evidence_incomplete
  elif [[ "$report_ready" == true && "$validated_report" != true ]]; then
    outcome=harness_invalid
    cause=invalid_terminal_report
  else
    outcome=runtime_failure
    cause=coordinator_exit_without_report
  fi
fi

jq -n --arg schema 'wukongim.chat_lifecycle.rehearsal_finalization/v1' \
  --arg request_id "$request_id" --arg outcome "$outcome" --arg cause "$cause" \
  --arg expected_end_at "$expected_end" --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg systemd_state "$state" \
  '{schema:$schema,request_id:$request_id,outcome:$outcome,cause:$cause,
    expected_end_at:$expected_end_at,observed_at:$observed_at,systemd_state:$systemd_state}' \
  >"$WK_CHAT_FINAL_DIR/finalization.json"
cp "$WK_CHAT_HANDOFF_DIR/handoff.json" "$WK_CHAT_FINAL_DIR/handoff.json"
cp "$WK_CHAT_HANDOFF_DIR/run-plan.json" "$WK_CHAT_FINAL_DIR/run-plan.json"
cp "$WK_CHAT_HANDOFF_DIR/quote.json" "$WK_CHAT_FINAL_DIR/quote.json"
cp "$WK_CHAT_HANDOFF_DIR/receipt.json" "$WK_CHAT_FINAL_DIR/receipt.json"
cp "$WK_CHAT_HANDOFF_DIR/deployment-outcome.json" "$WK_CHAT_FINAL_DIR/deployment-outcome.json"
printf '%s\n' ready
