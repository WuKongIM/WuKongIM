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
  .schema == "wukongim.chat_lifecycle.formal_handoff/v1" and
  .request_id == $request_id and (.attempt == 1 or .attempt == 2) and
  (.source_sha | test("^[0-9a-f]{40}$")) and
  (.bundle_digest | test("^sha256:[0-9a-f]{64}$")) and
  (.started_at | type == "string") and (.expected_end_at | type == "string")
' "$WK_CHAT_HANDOFF_DIR/handoff.json" >/dev/null
request_id="$(jq -er '.request_id' "$WK_CHAT_HANDOFF_DIR/handoff.json")"
expected_end="$(jq -er '.expected_end_at' "$WK_CHAT_HANDOFF_DIR/handoff.json")"
# The formal workload may spend eight hours finding capacity plus thirty
# minutes proving 2k SEND/s recovery after the 72-hour Soak.
safety_epoch=$(( $(date -u -d "$expected_end" +%s) + 9 * 60 * 60 ))

install -d -m 0700 "$WK_CHAT_FINAL_DIR/formal" "$WK_CHAT_FINAL_DIR/capacity"
export WK_CLOUD_LOAD_PUBLIC_IP="$(jq -er '.hosts[] | select(.role == "load") | .public_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SERVICE1_IP="$(jq -er '.hosts[] | select(.role == "service-1") | .private_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SERVICE2_IP="$(jq -er '.hosts[] | select(.role == "service-2") | .private_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SERVICE3_IP="$(jq -er '.hosts[] | select(.role == "service-3") | .private_address' "$WK_CHAT_HANDOFF_DIR/deployment-plan.json")"
export WK_CLOUD_SSH_KEY="$WK_CHAT_DEPLOYMENT_KEY"
export WK_CLOUD_SSH_CONFIG="$WK_CHAT_FINAL_DIR/deployment-ssh-config"
scripts/cloud-deployment/write-ssh-config.sh

remote_root=/var/lib/wukongim-cloud/reports
remote_report_max_bytes=4194304
remote_timeout=60
[[ "$operator_stop" == true ]] && remote_timeout=15

remote_nonempty() {
  local path="$1"
  timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load "sudo test -s '$path'"
}

fetch_bounded() {
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
    'sudo systemctl is-active wkbench-formal.service || true' 2>/dev/null || printf unreachable)"
  if [[ "$stop_state" == active || "$stop_state" == activating ]]; then
    timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
      "sudo systemctl kill --kill-who=main --signal=SIGTERM wkbench-formal.service || true"
    while [[ "$(date -u +%s)" -lt "$operator_stop_deadline" ]]; do
      if remote_nonempty "$remote_root/formal/final.json" || remote_nonempty "$remote_root/capacity/final.json"; then
        break
      fi
      stop_state="$(timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        'sudo systemctl is-active wkbench-formal.service || true' 2>/dev/null || printf unreachable)"
      [[ "$stop_state" == active || "$stop_state" == activating ]] || break
      sleep 5
    done
    stop_state="$(timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
      'sudo systemctl is-active wkbench-formal.service || true' 2>/dev/null || printf unreachable)"
    if [[ ( "$stop_state" == active || "$stop_state" == activating ) ]] && \
      ! remote_nonempty "$remote_root/formal/final.json" && ! remote_nonempty "$remote_root/capacity/final.json"; then
      timeout 15 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        "sudo systemctl kill --kill-who=main --signal=SIGKILL wkbench-formal.service || true; \
         sudo systemctl stop wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service || true"
    fi
  fi
fi

state=unreachable
if observed_state="$(timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo systemctl is-active wkbench-formal.service || true')"; then
  state="$observed_state"
fi
formal_ready=false
remote_nonempty "$remote_root/formal/final.json" && formal_ready=true || true

if [[ "$operator_stop" != true && "$state" == active && "$(date -u +%s)" -lt "$safety_epoch" ]]; then
  # One formal-chain process owns Soak, capacity, and recovery. Even complete
  # report files are not a handoff boundary until that process has exited.
  printf '%s\n' not_ready
  exit 0
fi
if [[ "$operator_stop" != true && "$state" == unreachable && "$(date -u +%s)" -lt "$safety_epoch" && "$formal_ready" != true ]]; then
  printf '%s\n' not_ready
  exit 0
fi

for name in qualification final; do
  for extension in json md; do
    remote="$remote_root/formal/${name}.${extension}"
    if remote_nonempty "$remote"; then
      fetch_bounded "$remote" "$WK_CHAT_FINAL_DIR/formal/${name}.${extension}" || true
    fi
  done
done
for extension in json md; do
  remote="$remote_root/capacity/final.${extension}"
  if remote_nonempty "$remote"; then
    fetch_bounded "$remote" "$WK_CHAT_FINAL_DIR/capacity/final.${extension}" || true
  fi
done

validated=false
pair_nonempty() {
  [[ -s "$1" && -s "$2" ]]
}
if pair_nonempty "$WK_CHAT_FINAL_DIR/formal/final.json" "$WK_CHAT_FINAL_DIR/formal/final.md"; then
  formal_outcome="$(jq -er '.verdict.outcome' "$WK_CHAT_FINAL_DIR/formal/final.json" 2>/dev/null || true)"
  capacity_args=()
  artifact_pairs_complete=true
  if [[ "$formal_outcome" == pass ]]; then
    if pair_nonempty "$WK_CHAT_FINAL_DIR/formal/qualification.json" "$WK_CHAT_FINAL_DIR/formal/qualification.md" &&
      pair_nonempty "$WK_CHAT_FINAL_DIR/capacity/final.json" "$WK_CHAT_FINAL_DIR/capacity/final.md"; then
      capacity_args=(--capacity-report "$WK_CHAT_FINAL_DIR/capacity/final.json")
    else
      artifact_pairs_complete=false
    fi
  elif [[ -e "$WK_CHAT_FINAL_DIR/formal/qualification.json" || -e "$WK_CHAT_FINAL_DIR/formal/qualification.md" ]] &&
    ! pair_nonempty "$WK_CHAT_FINAL_DIR/formal/qualification.json" "$WK_CHAT_FINAL_DIR/formal/qualification.md"; then
    artifact_pairs_complete=false
  fi
  if [[ "$artifact_pairs_complete" == true ]] && "$WK_CHAT_TOOL" validate-formal-chain \
    --formal-report "$WK_CHAT_FINAL_DIR/formal/final.json" "${capacity_args[@]}" \
    >"$WK_CHAT_FINAL_DIR/formal-result.json" 2>/dev/null; then
    validated=true
    outcome="$(jq -er .outcome "$WK_CHAT_FINAL_DIR/formal-result.json")"
    cause="$(jq -er .cause "$WK_CHAT_FINAL_DIR/formal-result.json")"
  fi
fi

if [[ "$validated" != true ]]; then
  {
    printf 'request_id=%s\n' "$request_id"
    printf 'expected_end_at=%s\n' "$expected_end"
    printf 'safety_deadline_epoch=%s\n' "$safety_epoch"
    printf 'observed_state=%s\n' "$state"
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo systemctl show wkbench-formal.service --property=ActiveState,SubState,Result,ExecMainCode,ExecMainStatus,NRestarts'
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo journalctl -u wkbench-formal.service --no-pager -n 500 | sha256sum'
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'sudo journalctl -u wkbench-formal.service --no-pager -n 500 | wc -l'
    timeout "$remote_timeout" ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'df -B1 / /var/lib/wukongim-cloud'
  } >"$WK_CHAT_FINAL_DIR/diagnostics.txt" 2>&1 || true
  if [[ "$operator_stop" == true ]]; then
    outcome=operator_stop
    cause=operator_requested_evidence_incomplete
  elif [[ -s "$WK_CHAT_FINAL_DIR/formal/final.json" || -s "$WK_CHAT_FINAL_DIR/capacity/final.json" ]]; then
    outcome=harness_invalid
    cause=invalid_terminal_report
  else
    outcome=runtime_failure
    cause=coordinator_exit_without_report
  fi
fi

jq -n --arg schema 'wukongim.chat_lifecycle.formal_finalization/v1' \
  --arg request_id "$request_id" --arg outcome "$outcome" --arg cause "$cause" \
  --arg expected_end_at "$expected_end" --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  --arg systemd_state "$state" \
  '{schema:$schema,request_id:$request_id,outcome:$outcome,cause:$cause,
    expected_end_at:$expected_end_at,observed_at:$observed_at,systemd_state:$systemd_state}' \
  >"$WK_CHAT_FINAL_DIR/finalization.json"
for name in handoff.json run-plan.json quote.json receipt.json deployment-outcome.json; do
  cp "$WK_CHAT_HANDOFF_DIR/$name" "$WK_CHAT_FINAL_DIR/$name"
done
printf '%s\n' ready
