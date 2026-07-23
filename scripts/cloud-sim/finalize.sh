#!/usr/bin/env bash

set -euo pipefail

umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
# shellcheck disable=SC1091
source "$SCRIPT_DIR/local-runtime.sh"

run_id=""
repository=""
diagnostic_focus=""
allow_fix_pr=false
finalize_temp=""
analysis_started=false
interrupted=false
interrupt_status=130
active_bounded_pid=""
analysis_status=0
cleanup_armed=false
cleanup_attempted=false
cleanup_running=false
billing_cleanup_proven=false
release_verified=false
remote_cleanup_needed=false

usage() {
  cat <<'EOF'
Usage: ./scripts/cloud-sim/finalize.sh RUN_ID [options]

Wait for one exact WuKongIM Simulation Run to finish, analyze it with the local
ChatGPT-authenticated Codex CLI, destroy the exact cloud resources, and prove
that provider inventory is empty.

Options:
  --repository OWNER/REPO       GitHub repository (default: detected from checkout)
  --diagnostic-focus TEXT       Optional bounded diagnostic focus
  --allow-fix-pr                Compatibility flag; remediation is deferred until after paid-run cleanup
  -h, --help                    Show this help

Keep this command running locally. No OpenAI API key is required.
EOF
}

fail() {
  printf 'cloud-sim finalize: %s\n' "$*" >&2
  exit 1
}

cleanup_local_state() {
  if [[ -n "$finalize_temp" && -d "$finalize_temp" ]]; then
    rm -rf "$finalize_temp"
  fi
  wk_cleanup_local_cloud_tools
}
trap cleanup_local_state EXIT

handle_signal() {
  local requested_status="$1"
  local signal_target="${active_bounded_pid:-${WK_BOUNDED_WRAPPER_PID:-}}"
  interrupted=true
  interrupt_status="$requested_status"
  remote_cleanup_needed=true
  if [[ -n "$signal_target" ]]; then
    wk_stop_bounded "$signal_target"
  fi
  if [[ "$analysis_started" == true ]]; then
    printf 'Interrupt received during analysis; exact cleanup will run before exit.\n' >&2
  else
    printf 'Interrupt received before analysis; the wait will stop and exact cleanup will run before exit.\n' >&2
  fi
}

detect_checkout_repository() {
  local origin_url=""
  origin_url="$(git -C "$REPO_ROOT" remote get-url origin 2>/dev/null)" || return 1
  origin_url="${origin_url%.git}"
  case "$origin_url" in
    https://github.com/*) printf '%s\n' "${origin_url#https://github.com/}" ;;
    git@github.com:*) printf '%s\n' "${origin_url#git@github.com:}" ;;
    ssh://git@github.com/*) printf '%s\n' "${origin_url#ssh://git@github.com/}" ;;
    *) return 1 ;;
  esac
}

parse_rfc3339_epoch() {
  local value="$1"
  local normalized="$value"
  if [[ "$normalized" == *.* ]]; then
    normalized="${normalized%%.*}Z"
  fi
  if date -u -d "$normalized" +%s 2>/dev/null; then
    return
  fi
  date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$normalized" '+%s' 2>/dev/null
}

wait_for_finalize_delay() {
  local delay_seconds="$1"
  local wait_status=0
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if ! wk_start_bounded "$((delay_seconds + 1))" sleep "$delay_seconds"; then
    return 125
  fi
  active_bounded_pid="$WK_BOUNDED_WRAPPER_PID"
  if [[ "$interrupted" == true ]]; then
    wk_stop_bounded "$active_bounded_pid"
  fi
  set +e
  wait "$active_bounded_pid"
  wait_status=$?
  set -e
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if [[ "$interrupted" == true ]]; then
    return "$interrupt_status"
  fi
  return "$wait_status"
}

wait_until() {
  local target_epoch="$1"
  local target_rfc3339="$2"
  local now remaining interval wait_status
  now="$(date -u +%s)"
  if ((now >= target_epoch)); then
    return
  fi
  printf 'Waiting for terminal workload data at %s. Keep this command running.\n' "$target_rfc3339"
  while ((now < target_epoch)); do
    remaining=$((target_epoch - now))
    interval=60
    if ((remaining < interval)); then
      interval="$remaining"
    fi
    wait_status=0
    wait_for_finalize_delay "$interval" || wait_status=$?
    if ((wait_status != 0)); then
      return "$wait_status"
    fi
    now="$(date -u +%s)"
  done
}

run_exact_cleanup_and_verify() {
  local finalize_nonce=""
  local request_hex=""
  local index=0
  local byte_hex=""
  local request_id=""
  local cleanup_title=""
  local cleanup_run=""
  local verification_log=""
  local verification_result=""
  local verification_timeout_input=""
  local verification_timeout_seconds=0
  local verification_status=0

  cleanup_attempted=true
  cleanup_running=true
  # Cleanup and released-state proof must survive repeated terminal signals.
  # Bounded operations retain their deadlines while their wrappers ignore the
  # local terminal process group's HUP/INT/TERM delivery.
  trap '' HUP INT TERM
  WK_RUN_BOUNDED_IGNORE_SIGNALS=true
  export WK_RUN_BOUNDED_IGNORE_SIGNALS

  # finalize_temp is created atomically by mktemp and is private to this
  # process. Encode its eight random suffix bytes as lower-case hex so the
  # correlation preserves the workflow's finalize-[0-9a-f]{16} contract.
  finalize_nonce="${finalize_temp##*/}"
  finalize_nonce="${finalize_nonce##*.}"
  if [[ ${#finalize_nonce} -ne 8 || ! "$finalize_nonce" =~ ^[A-Za-z0-9]+$ ]]; then
    printf '%s\n' 'cloud-sim finalize: cannot derive a safe cleanup request correlation' >&2
    cleanup_running=false
    return 1
  fi
  for ((index = 0; index < 8; index += 1)); do
    printf -v byte_hex '%02x' "'${finalize_nonce:index:1}"
    request_hex="${request_hex}${byte_hex}"
  done
  request_id="finalize-${request_hex}"
  if [[ ! "$request_id" =~ ^finalize-[0-9a-f]{16}$ ]]; then
    printf '%s\n' 'cloud-sim finalize: cannot derive a valid cleanup request correlation' >&2
    cleanup_running=false
    return 1
  fi

  cleanup_title="Cloud Simulation Cleanup $run_id $request_id"
  if ! cleanup_run="$(wk_dispatch_and_join_workflow "$repository" cloud-sim-cleanup.yml \
    "$cleanup_title" "Exact Cleanup" -f "run_id=$run_id" -f "request_id=$request_id")"; then
    printf '%s\n' \
      'cloud-sim finalize: exact Cleanup was not proven; the remote lease and scheduled sweeper remain the billing backstop' >&2
    cleanup_running=false
    return 1
  fi
  billing_cleanup_proven=true
  printf 'Billing cleanup proven by exact Cleanup workflow: https://github.com/%s/actions/runs/%s\n' \
    "$repository" "$cleanup_run"

  verification_log="$finalize_temp/release-verification.log"
  verification_result="$finalize_temp/release-verification.json"
  verification_timeout_input="${WK_LOCAL_FINALIZE_VERIFICATION_TIMEOUT_SECONDS:-1800}"
  if [[ ! "$verification_timeout_input" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s\n' \
      'Billing cleanup is already proven, but WK_LOCAL_FINALIZE_VERIFICATION_TIMEOUT_SECONDS must be a positive integer.' >&2
    cleanup_running=false
    return 125
  fi
  verification_timeout_seconds=$((10#$verification_timeout_input))
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if ! wk_start_bounded "$verification_timeout_seconds" /bin/bash -c '
    set -o pipefail
    log_file="$1"
    shift
    "$@" 2>&1 | tee "$log_file"
  ' finalize-release-verification "$verification_log" "$SCRIPT_DIR/analyze.sh" \
    "$run_id" --repository "$repository" --result-file "$verification_result"; then
    printf '%s\n' \
      'Billing cleanup is already proven, but independent provider release verification could not start.' >&2
    cleanup_running=false
    return 125
  fi
  active_bounded_pid="$WK_BOUNDED_WRAPPER_PID"
  set +e
  wait "$active_bounded_pid"
  verification_status=$?
  set -e
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if ((verification_status != 0)); then
    printf 'Billing cleanup is already proven by exact Cleanup workflow %s, but independent provider release verification failed with status %d.\n' \
      "$cleanup_run" "$verification_status" >&2
    cleanup_running=false
    return "$verification_status"
  fi
  if ! jq -e --arg run_id "$run_id" '
    .schema == "wukongim/cloud-simulation-analysis-result/v1" and
    .run_id == $run_id and .state == "released" and .diagnosis == null and
    .provider.state == "released" and .provider.resources == []
  ' "$verification_result" >/dev/null; then
    printf 'Billing cleanup is already proven by exact Cleanup workflow %s, but independent provider verification did not return the structured released state.\n' \
      "$cleanup_run" >&2
    cleanup_running=false
    return 1
  fi
  release_verified=true
  cleanup_running=false
  return 0
}

finalize_exit() {
  local original_status=$?
  local cleanup_status=0
  trap - EXIT
  trap '' HUP INT TERM
  set +e
  if [[ "$cleanup_armed" == true && "$remote_cleanup_needed" == true &&
    "$cleanup_attempted" != true && "$cleanup_running" != true ]]; then
    analysis_status="$interrupt_status"
    run_exact_cleanup_and_verify
    cleanup_status=$?
    if ((cleanup_status == 0)); then
      original_status="$interrupt_status"
      printf '%s\n' \
        'Finalization cleaned and verified the exact Run after a pre-analysis interrupt.' >&2
    else
      original_status="$cleanup_status"
    fi
  fi
  cleanup_local_state
  exit "$original_status"
}

abort_if_interrupted() {
  if [[ "$interrupted" == true ]]; then
    exit "$interrupt_status"
  fi
}

if (($# == 0)); then
  usage >&2
  exit 2
fi
if [[ "$1" == -h || "$1" == --help ]]; then
  usage
  exit 0
fi
run_id="$1"
shift

while (($#)); do
  case "$1" in
    --repository)
      [[ $# -ge 2 ]] || fail "--repository requires OWNER/REPO"
      repository="$2"
      shift 2
      ;;
    --diagnostic-focus)
      [[ $# -ge 2 ]] || fail "--diagnostic-focus requires text"
      diagnostic_focus="$2"
      shift 2
      ;;
    --allow-fix-pr)
      allow_fix_pr=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *) fail "unknown option: $1" ;;
  esac
done

[[ "$run_id" =~ ^gh-([0-9]+)-([1-9][0-9]*)$ ]] || \
  fail "Run Identity must be the exact gh-WORKFLOW_RUN_ID-RUN_ATTEMPT printed by Provision"
provision_run_id="${BASH_REMATCH[1]}"

wk_resolve_local_github_tool || fail "cannot resolve the pinned local GitHub CLI"
for command in gh git jq date; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done

checkout_repository="$(detect_checkout_repository || true)"
if [[ -z "$repository" ]]; then
  repository="$checkout_repository"
fi
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "cannot determine a valid GitHub OWNER/REPO"
[[ "$checkout_repository" == "$repository" ]] || fail "--repository must match the current checkout origin"

finalize_temp="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-cloud-finalize.XXXXXXXX")" || \
  fail "cannot create the private finalization directory"
cleanup_armed=true
trap finalize_exit EXIT
trap 'handle_signal 129' HUP
trap 'handle_signal 130' INT
trap 'handle_signal 130' TERM

wk_gh auth status --hostname github.com >/dev/null 2>&1 || fail "GitHub CLI login is required; run 'gh auth login'"
abort_if_interrupted
wk_gh api "repos/$repository/contents/.github/workflows/cloud-sim-cleanup.yml?ref=main" >/dev/null || \
  fail "cloud-sim-cleanup.yml is not present on remote main"
abort_if_interrupted

schedule_dir="$finalize_temp/schedule"
mkdir -p "$schedule_dir"
wk_gh run download "$provision_run_id" --repo "$repository" \
  --name "cloud-sim-finalize-$run_id" --dir "$schedule_dir" || \
  fail "Provision did not publish a finalization schedule for $run_id; use analyze.sh and Cleanup manually"
abort_if_interrupted

schedule_json="$schedule_dir/finalize.json"
[[ -f "$schedule_json" ]] || fail "finalization schedule is missing finalize.json"
jq -e --arg run_id "$run_id" '
  .schema == "wukongim/cloud-simulation-finalize/v1" and
  .run_id == $run_id and
  (.active_until | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$")) and
  (.expires_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$")) and
  ((.analysis_ready_at // .active_until) | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$"))
' "$schedule_json" >/dev/null || fail "finalization schedule is invalid or belongs to another Run Identity"
abort_if_interrupted

active_until="$(jq -er .active_until "$schedule_json")"
analysis_ready_at="$(jq -er '.analysis_ready_at // .active_until' "$schedule_json")"
expires_at="$(jq -er .expires_at "$schedule_json")"
active_epoch="$(parse_rfc3339_epoch "$active_until")" || fail "cannot parse workload deadline"
ready_epoch="$(parse_rfc3339_epoch "$analysis_ready_at")" || fail "cannot parse analysis-ready deadline"
expires_epoch="$(parse_rfc3339_epoch "$expires_at")" || fail "cannot parse Run Lease expiry"
[[ "$active_epoch" =~ ^[0-9]+$ && "$ready_epoch" =~ ^[0-9]+$ && "$expires_epoch" =~ ^[0-9]+$ ]] || \
  fail "finalization schedule contains invalid deadlines"
((active_epoch <= ready_epoch && ready_epoch < expires_epoch)) || \
  fail "finalization schedule deadlines are not ordered"

printf 'Simulation Run: %s\nWorkload deadline: %s\nAnalysis ready: %s\nLease expiry: %s\n' \
  "$run_id" "$active_until" "$analysis_ready_at" "$expires_at"
wait_status=0
wait_until "$ready_epoch" "$analysis_ready_at" || wait_status=$?
if ((wait_status != 0)) && [[ "$interrupted" != true ]]; then
  interrupted=true
  interrupt_status="$wait_status"
  printf 'Finalization wait failed with status %d; exact cleanup will still run.\n' "$wait_status" >&2
fi

analysis_args=("$run_id" --repository "$repository")
if [[ -n "$diagnostic_focus" ]]; then
  analysis_args+=(--diagnostic-focus "$diagnostic_focus")
fi
if [[ "$allow_fix_pr" == true ]]; then
  printf '%s\n' \
    'Automatic Draft-PR remediation is deferred: Finalize always proves exact Cleanup before any optional code work.' >&2
fi
analysis_started=true
analysis_status=0
analysis_attempt=0
minimum_retry_lease_seconds=2100

run_terminal_analysis_attempt() {
  local log_file="$1"
  local result_path="$2"
  local now_epoch=""
  local available_seconds=0
  local timeout_input="${WK_LOCAL_FINALIZE_ANALYSIS_TIMEOUT_SECONDS:-2700}"
  local timeout_seconds=0
  local wait_status=0

  if [[ ! "$timeout_input" =~ ^[1-9][0-9]*$ ]]; then
    printf '%s\n' \
      'WK_LOCAL_FINALIZE_ANALYSIS_TIMEOUT_SECONDS must be a positive integer; exact cleanup will still run.' >&2
    return 125
  fi
  timeout_seconds=$((10#$timeout_input))
  now_epoch="$(date -u +%s)" || return 125
  [[ "$now_epoch" =~ ^[0-9]+$ ]] || return 125
  available_seconds=$((expires_epoch - now_epoch - minimum_retry_lease_seconds))
  ((available_seconds > 0)) || return 125
  if ((timeout_seconds > available_seconds)); then
    timeout_seconds="$available_seconds"
  fi

  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if ! wk_start_bounded "$timeout_seconds" /bin/bash -c '
    set -o pipefail
    log_file="$1"
    shift
    "$@" 2>&1 | tee "$log_file"
  ' finalize-analysis "$log_file" "$SCRIPT_DIR/analyze.sh" \
    "${analysis_args[@]}" --result-file "$result_path"; then
    return 125
  fi
  active_bounded_pid="$WK_BOUNDED_WRAPPER_PID"
  if [[ "$interrupted" == true ]]; then
    wk_stop_bounded "$active_bounded_pid"
  fi
  set +e
  wait "$active_bounded_pid"
  wait_status=$?
  if [[ "$interrupted" == true ]] && kill -0 "$active_bounded_pid" 2>/dev/null; then
    wait "$active_bounded_pid"
  fi
  set -e
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if [[ "$interrupted" == true ]]; then
    return "$interrupt_status"
  fi
  return "$wait_status"
}

wait_for_analysis_retry() {
  local wait_status=0
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if ! wk_start_bounded 61 sleep 60; then
    return 125
  fi
  active_bounded_pid="$WK_BOUNDED_WRAPPER_PID"
  if [[ "$interrupted" == true ]]; then
    wk_stop_bounded "$active_bounded_pid"
  fi
  set +e
  wait "$active_bounded_pid"
  wait_status=$?
  if [[ "$interrupted" == true ]] && kill -0 "$active_bounded_pid" 2>/dev/null; then
    wait "$active_bounded_pid"
  fi
  set -e
  active_bounded_pid=""
  WK_BOUNDED_WRAPPER_PID=""
  if [[ "$interrupted" == true ]]; then
    return "$interrupt_status"
  fi
  return "$wait_status"
}

while [[ "$interrupted" != true ]]; do
  analysis_attempt=$((analysis_attempt + 1))
  analysis_log="$finalize_temp/analysis-$analysis_attempt.log"
  analysis_result="$finalize_temp/analysis-result-$analysis_attempt.json"
  printf 'Starting terminal analysis attempt %d.\n' "$analysis_attempt"
  analysis_status=0
  run_terminal_analysis_attempt "$analysis_log" "$analysis_result" || analysis_status=$?

  if [[ -f "$analysis_result" ]] && jq -e --arg run_id "$run_id" '
    .schema == "wukongim/cloud-simulation-analysis-result/v1" and
    .run_id == $run_id and .state == "released" and .diagnosis == null and
    .provider.state == "released" and .provider.resources == []
  ' "$analysis_result" >/dev/null; then
    printf 'Finalization complete: the exact Simulation Run was already released with empty provider inventory.\n'
    exit 0
  fi
  if ((analysis_status != 0)); then
    printf 'Analysis failed with status %d; exact cleanup will still run to stop billing.\n' "$analysis_status" >&2
    break
  fi
  if [[ ! -f "$analysis_result" ]] || ! jq -e --arg run_id "$run_id" '
    .schema == "wukongim/cloud-simulation-analysis-result/v1" and
    .run_id == $run_id and .state == "diagnosed" and (.diagnosis | type == "object")
  ' "$analysis_result" >/dev/null; then
    analysis_status=1
    printf 'Analysis returned no valid structured outcome; exact cleanup will still run.\n' >&2
    break
  fi
  if ! jq -e '
    [.diagnosis.observation_references[]? |
      select(.tool == "workload_inspect" and .state == "in_progress")] | length > 0
  ' "$analysis_result" >/dev/null; then
    break
  fi

  set +e
  now_epoch="$(date -u +%s)"
  now_status=$?
  set -e
  if [[ "$interrupted" == true ]]; then
    analysis_status="$interrupt_status"
    break
  fi
  if ((now_status != 0)) || [[ ! "$now_epoch" =~ ^[0-9]+$ ]]; then
    analysis_status="${now_status:-1}"
    ((analysis_status != 0)) || analysis_status=1
    printf 'Cannot read the lease clock; exact cleanup will still run.\n' >&2
    break
  fi
  if ((now_epoch + minimum_retry_lease_seconds >= expires_epoch)); then
    analysis_status=1
    printf 'Workload is still in progress and the lease cannot safely admit another analysis; cleanup will run.\n' >&2
    break
  fi
  if [[ "$interrupted" == true ]]; then
    analysis_status="$interrupt_status"
    break
  fi
  printf 'Workload is still in progress; retaining the run and retrying terminal analysis in 60 seconds.\n'
  retry_sleep_status=0
  wait_for_analysis_retry || retry_sleep_status=$?
  if [[ "$interrupted" == true ]]; then
    analysis_status="$interrupt_status"
    break
  fi
  if ((retry_sleep_status != 0)); then
    analysis_status="$retry_sleep_status"
    printf 'Terminal-analysis retry wait failed with status %d; exact cleanup will still run.\n' \
      "$analysis_status" >&2
    break
  fi
done

if [[ "$interrupted" == true && "$analysis_status" == 0 ]]; then
  analysis_status="$interrupt_status"
fi

cleanup_status=0
run_exact_cleanup_and_verify || cleanup_status=$?
if ((cleanup_status != 0)); then
  exit "$cleanup_status"
fi

if ((analysis_status != 0)); then
  printf 'Finalization cleaned and verified the exact Run, but terminal analysis failed.\n' >&2
  exit "$analysis_status"
fi
printf 'Finalization complete: terminal analysis finished and provider inventory is empty.\n'
