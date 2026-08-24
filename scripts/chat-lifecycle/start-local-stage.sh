#!/usr/bin/env bash
set -euo pipefail

: "${WK_CHAT_LAB_SSH_CONFIG:?required}"
: "${WK_CHAT_LAB_RUN_START_OUTPUT:?required}"
stage_service="${WK_CHAT_LAB_STAGE_SERVICE:-wkbench-rehearsal.service}"
report_dir="${WK_CHAT_LAB_STAGE_REPORT_DIR:-rehearsal}"
readiness_seconds="${WK_CHAT_LAB_STAGE_READINESS_SECONDS:-1800}"
poll_seconds="${WK_CHAT_LAB_STAGE_POLL_SECONDS:-5}"
[[ -f "$WK_CHAT_LAB_SSH_CONFIG" && ! -L "$WK_CHAT_LAB_SSH_CONFIG" ]]
[[ "$WK_CHAT_LAB_RUN_START_OUTPUT" == /* && "$stage_service" =~ ^[A-Za-z0-9@._-]+\.service$ ]]
[[ "$report_dir" =~ ^[a-z][a-z0-9_-]*$ && "$readiness_seconds" =~ ^[1-9][0-9]*$ && "$poll_seconds" =~ ^[1-9][0-9]*$ ]]

remote_report="/var/lib/wukongim-cloud/reports/$report_dir/run-start.json"
ssh -o ConnectTimeout=15 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 \
  -F "$WK_CHAT_LAB_SSH_CONFIG" wukong-load \
  "sudo rm -f '$remote_report' && (sudo systemctl reset-failed '$stage_service' || true) && sudo systemctl start --no-block '$stage_service'"

deadline=$(( $(date -u +%s) + readiness_seconds ))
temporary="${WK_CHAT_LAB_RUN_START_OUTPUT}.next.$$"
rm -f -- "$temporary"
while true; do
  if ssh -o ConnectTimeout=15 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 \
    -F "$WK_CHAT_LAB_SSH_CONFIG" wukong-load \
    "sudo test -s '$remote_report' && sudo head -c 65537 -- '$remote_report'" >"$temporary" 2>/dev/null; then
    bytes="$(wc -c <"$temporary" | tr -d ' ')"
    if [[ "$bytes" =~ ^[1-9][0-9]*$ ]] && (( bytes <= 65536 )) && jq -e --arg stage "$report_dir" '
      .schema == "wukongim.chat_lifecycle.run_start/v1" and .stage == $stage and
      (.started_at | type == "string") and (.expected_end_at | type == "string") and
      (.run_hash | test("^sha256:[0-9a-f]{64}$")) and
      (.assignment_hash | test("^sha256:[0-9a-f]{64}$")) and .generation > 0
    ' "$temporary" >/dev/null; then
      chmod 0600 "$temporary"
      mv -f -- "$temporary" "$WK_CHAT_LAB_RUN_START_OUTPUT"
      exit 0
    fi
  fi
  rm -f -- "$temporary"
  service_state="$(ssh -o ConnectTimeout=15 -F "$WK_CHAT_LAB_SSH_CONFIG" wukong-load \
    "sudo systemctl is-active '$stage_service' || true" 2>/dev/null | head -c 64 || true)"
  if [[ "$service_state" != active && "$service_state" != activating ]]; then
    echo "stage service became $service_state before run-start" >&2
    exit 1
  fi
  (( $(date -u +%s) < deadline )) || {
    echo 'stage readiness deadline elapsed before run-start' >&2
    exit 1
  }
  sleep "$poll_seconds"
done
