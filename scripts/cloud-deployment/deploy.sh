#!/usr/bin/env bash
# Shared Deployment entry: callers own Lease provenance, credentials and workload
# policy; this module owns activation, bounded readiness and the typed outcome.
set -euo pipefail
umask 077

: "${WK_CLOUD_DEPLOYMENT_PLAN:?required}"
: "${WK_CLOUD_LEASE_RECEIPT:?required}"
: "${WK_CLOUD_BUNDLE_MANIFEST:?required}"
: "${WK_CLOUD_GATE_TOOL:?required}"
: "${WK_CLOUD_READINESS_CREDENTIALS:?required}"
: "${WK_CLOUD_READINESS_OUTPUT:?required}"
: "${WK_CLOUD_OUTCOME_OUTPUT:?required}"
: "${WK_CLOUD_FAILURE_OUTPUT:?required}"
: "${WK_CLOUD_LAST_GATE_OUTPUT:?required}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ssh_writer="${WK_CLOUD_SSH_CONFIG_WRITER:-$script_dir/write-ssh-config.sh}"
activator="${WK_CLOUD_ACTIVATOR:-$script_dir/activate-hosts.sh}"
collector="${WK_CLOUD_READINESS_COLLECTOR:-$script_dir/collect-readiness.sh}"
# Use the same process-group supervisor on macOS and Linux. System timeout
# implementations can stop supervising once a shell exits but leave descendants.
timeout_tool="$script_dir/../chat-lifecycle/portable-timeout.sh"
readiness_seconds="${WK_CLOUD_READINESS_TIMEOUT_SECONDS:-1200}"
poll_seconds="${WK_CLOUD_READINESS_POLL_SECONDS:-10}"
attempt_output="${WK_CLOUD_OUTCOME_OUTPUT}.attempt.$$"
active_pid=""

write_failure() {
  "$script_dir/write-deployment-failure.sh" "$WK_CLOUD_FAILURE_OUTPUT" "$@"
}
valid_failure() {
  jq -e '.passed == false and .failure.schema == "wukongim.cloud_deployment.failure/v1"' "$1" >/dev/null 2>&1
}
finish() {
  local status=$?
  trap - EXIT INT TERM HUP
  rm -f -- "$attempt_output"
  if (( status != 0 )) && ! valid_failure "$WK_CLOUD_OUTCOME_OUTPUT"; then
    if ! valid_failure "$WK_CLOUD_FAILURE_OUTPUT"; then
      write_failure readiness_evidence_invalid none '' \
        'deployment stopped without complete typed evidence' 'known host state is unavailable'
    fi
    cp -- "$WK_CLOUD_FAILURE_OUTPUT" "$attempt_output"
    chmod 0600 "$attempt_output"
    mv -f -- "$attempt_output" "$WK_CLOUD_OUTCOME_OUTPUT"
  fi
  exit "$status"
}
interrupt() {
  local status="$1"
  trap '' INT TERM HUP
  if [[ -n "$active_pid" ]]; then
    # The timeout supervisor owns the complete local command process group.
    kill -TERM "$active_pid" 2>/dev/null || true
    wait "$active_pid" 2>/dev/null || true
    active_pid=""
  fi
  exit "$status"
}
# Each invocation starts a fresh evidence generation, including same-plan retries.
for output in "$WK_CLOUD_OUTCOME_OUTPUT" "$WK_CLOUD_FAILURE_OUTPUT" \
  "$WK_CLOUD_LAST_GATE_OUTPUT" "$WK_CLOUD_READINESS_OUTPUT"; do
  [[ ! -L "$output" ]]
  rm -f -- "$output"
done
trap finish EXIT
trap 'interrupt 130' INT
trap 'interrupt 143' TERM
trap 'interrupt 129' HUP

write_failure invalid_plan none '' 'deployment inputs are incomplete or invalid' 'no host operation was admitted'
for input in "$WK_CLOUD_DEPLOYMENT_PLAN" "$WK_CLOUD_LEASE_RECEIPT" \
  "$WK_CLOUD_BUNDLE_MANIFEST" "$WK_CLOUD_READINESS_CREDENTIALS"; do
  [[ -f "$input" && ! -L "$input" ]]
done
for tool in "$ssh_writer" "$activator" "$collector" "$WK_CLOUD_GATE_TOOL" "$timeout_tool"; do
  [[ -x "$tool" ]]
done
[[ "$readiness_seconds" =~ ^[1-9][0-9]{0,5}$ ]]
[[ "$poll_seconds" =~ ^[0-9]{1,5}([.][0-9]{1,3})?$ ]]
plan_digest="$(jq -er '.plan_digest | select(test("^sha256:[0-9a-f]{64}$"))' "$WK_CLOUD_DEPLOYMENT_PLAN")"

# Activation retains the existing shared SSH deadline and per-command retry
# policy. Readiness gets its own deadline only after activation and cleanup.
export WK_CLOUD_SSH_DEADLINE_EPOCH="${WK_CLOUD_SSH_DEADLINE_EPOCH:-$(( $(date -u +%s) + 1500 ))}"
[[ "$WK_CLOUD_SSH_DEADLINE_EPOCH" =~ ^[1-9][0-9]{0,11}$ ]]
run_before() {
  local deadline="$1" remaining status
  shift
  remaining=$((deadline - $(date -u +%s)))
  (( remaining > 0 )) || return 124
  "$timeout_tool" --signal=TERM --kill-after=10s "${remaining}s" "$@" &
  active_pid=$!
  if wait "$active_pid"; then status=0; else status=$?; fi
  active_pid=""
  return "$status"
}

write_failure bundle_transfer_failed plan_validated load \
  'deployment SSH transport initialization failed' 'load host was not contacted'
run_before "$WK_CLOUD_SSH_DEADLINE_EPOCH" "$ssh_writer"
run_before "$WK_CLOUD_SSH_DEADLINE_EPOCH" "$activator"

write_failure readiness_evidence_invalid services_active load \
  'bounded readiness collection did not complete' 'native services were activated; readiness is unknown'
# shellcheck disable=SC1090
source "$WK_CLOUD_READINESS_CREDENTIALS"
deadline=$(( $(date -u +%s) + readiness_seconds ))
while (( $(date -u +%s) < deadline )); do
  # A failed collection must never let the gate consume an older snapshot.
  rm -f -- "$WK_CLOUD_READINESS_OUTPUT" "$attempt_output"
  if run_before "$deadline" "$collector"; then
    if run_before "$deadline" "$WK_CLOUD_GATE_TOOL" deployment-gate \
      --lease-receipt "$WK_CLOUD_LEASE_RECEIPT" \
      --plan "$WK_CLOUD_DEPLOYMENT_PLAN" \
      --bundle-manifest "$WK_CLOUD_BUNDLE_MANIFEST" \
      --snapshot "$WK_CLOUD_READINESS_OUTPUT" >"$attempt_output"; then
      if jq -e --arg digest "$plan_digest" '
        .passed == true and .receipt.schema == "wukongim.cloud_deployment.receipt/v2" and
        .receipt.deployment_plan_digest == $digest
      ' "$attempt_output" >/dev/null 2>&1 && (( $(date -u +%s) < deadline )); then
        mv -f -- "$attempt_output" "$WK_CLOUD_OUTCOME_OUTPUT"
        printf '%s\n' ready >"$WK_CLOUD_LAST_GATE_OUTPUT"
        rm -f -- "$WK_CLOUD_FAILURE_OUTPUT"
        exit 0
      fi
    fi
    # Preserve a real gate failure across later unavailable probes. Raw command
    # output and incomplete JSON never become the public deployment outcome.
    if valid_failure "$attempt_output"; then
      mv -f -- "$attempt_output" "$WK_CLOUD_OUTCOME_OUTPUT"
    fi
  fi
  run_before "$deadline" sleep "$poll_seconds" || break
done
printf 'deployment readiness deadline elapsed\n' >&2
exit 124
