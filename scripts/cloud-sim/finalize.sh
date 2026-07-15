#!/usr/bin/env bash

set -euo pipefail

umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

run_id=""
repository=""
diagnostic_focus=""
allow_fix_pr=false
finalize_temp=""
analysis_started=false
interrupted=false

usage() {
  cat <<'EOF'
Usage: ./scripts/cloud-sim/finalize.sh RUN_ID [options]

Wait for one exact WuKongIM Simulation Run to finish, analyze it with the local
ChatGPT-authenticated Codex CLI, destroy the exact cloud resources, and prove
that provider inventory is empty.

Options:
  --repository OWNER/REPO       GitHub repository (default: detected from checkout)
  --diagnostic-focus TEXT       Optional bounded diagnostic focus
  --allow-fix-pr                Allow an eligible product defect to create a tested Draft PR
  -h, --help                    Show this help

Keep this command running locally. No OpenAI API key is required.
EOF
}

fail() {
  printf 'cloud-sim finalize: %s\n' "$*" >&2
  exit 1
}

cleanup() {
  if [[ -n "$finalize_temp" && -d "$finalize_temp" ]]; then
    rm -rf "$finalize_temp"
  fi
}
trap cleanup EXIT

handle_signal() {
  if [[ "$analysis_started" == true ]]; then
    interrupted=true
    printf 'Interrupt received after analysis started; exact cleanup will run before exit.\n' >&2
    return
  fi
  exit 130
}
trap handle_signal INT TERM

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

find_cleanup_run() {
  local request_id="$1"
  local expected_title="Cloud Simulation Cleanup $run_id $request_id"
  local runs candidate attempt
  for ((attempt = 0; attempt < 30; attempt += 1)); do
    runs="$(gh run list --workflow cloud-sim-cleanup.yml --repo "$repository" \
      --event workflow_dispatch --branch main --limit 20 --json databaseId,displayTitle)"
    candidate="$(jq -r --arg title "$expected_title" \
      '[.[] | select(.displayTitle == $title)] | sort_by(.databaseId) | reverse | .[0].databaseId // empty' \
      <<<"$runs")"
    if [[ "$candidate" =~ ^[0-9]+$ && "$candidate" != 0 ]]; then
      printf '%s\n' "$candidate"
      return
    fi
    sleep 2
  done
  return 1
}

wait_until() {
  local target_epoch="$1"
  local target_rfc3339="$2"
  local now remaining interval
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
    sleep "$interval"
    now="$(date -u +%s)"
  done
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

for command in gh jq openssl date; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done
gh auth status --hostname github.com >/dev/null 2>&1 || fail "GitHub CLI login is required; run 'gh auth login'"

if [[ -z "$repository" ]]; then
  repository="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
fi
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "cannot determine a valid GitHub OWNER/REPO"
checkout_repository="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
[[ "$checkout_repository" == "$repository" ]] || fail "--repository must match the current checkout origin"
gh api "repos/$repository/contents/.github/workflows/cloud-sim-cleanup.yml?ref=main" >/dev/null || \
  fail "cloud-sim-cleanup.yml is not present on remote main"

finalize_temp="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-cloud-finalize.XXXXXX")"
schedule_dir="$finalize_temp/schedule"
mkdir -p "$schedule_dir"
gh run download "$provision_run_id" --repo "$repository" \
  --name "cloud-sim-finalize-$run_id" --dir "$schedule_dir" || \
  fail "Provision did not publish a finalization schedule for $run_id; use analyze.sh and Cleanup manually"

schedule_json="$schedule_dir/finalize.json"
[[ -f "$schedule_json" ]] || fail "finalization schedule is missing finalize.json"
jq -e --arg run_id "$run_id" '
  .schema == "wukongim/cloud-simulation-finalize/v1" and
  .run_id == $run_id and
  (.active_until | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$")) and
  (.expires_at | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$")) and
  ((.analysis_ready_at // .active_until) | type == "string" and test("^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\\.[0-9]+)?Z$"))
' "$schedule_json" >/dev/null || fail "finalization schedule is invalid or belongs to another Run Identity"

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
wait_until "$ready_epoch" "$analysis_ready_at"

analysis_args=("$run_id" --repository "$repository")
if [[ -n "$diagnostic_focus" ]]; then
  analysis_args+=(--diagnostic-focus "$diagnostic_focus")
fi
if [[ "$allow_fix_pr" == true ]]; then
  analysis_args+=(--allow-fix-pr)
fi
analysis_started=true
analysis_status=0
analysis_attempt=0
minimum_retry_lease_seconds=2100
while :; do
  analysis_attempt=$((analysis_attempt + 1))
  analysis_log="$finalize_temp/analysis-$analysis_attempt.log"
  analysis_result="$finalize_temp/analysis-result-$analysis_attempt.json"
  printf 'Starting terminal analysis attempt %d.\n' "$analysis_attempt"
  set +e
  "$SCRIPT_DIR/analyze.sh" "${analysis_args[@]}" --result-file "$analysis_result" 2>&1 | tee "$analysis_log"
  analysis_status="${PIPESTATUS[0]}"
  set -e

  if [[ -f "$analysis_result" ]] && jq -e --arg run_id "$run_id" '
    .schema == "wukongim/cloud-simulation-analysis-result/v1" and
    .run_id == $run_id and .state == "released" and .diagnosis == null
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
    analysis_status=130
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
    analysis_status=130
    break
  fi
  printf 'Workload is still in progress; retaining the run and retrying terminal analysis in 60 seconds.\n'
  set +e
  sleep 60
  retry_sleep_status=$?
  set -e
  if [[ "$interrupted" == true ]]; then
    analysis_status=130
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
  analysis_status=130
fi

# Once analysis has started, billing cleanup and provider verification are not
# interruptible from this local process. The remote lease remains the backstop.
trap '' INT TERM

request_id="finalize-$(openssl rand -hex 8)"
gh workflow run cloud-sim-cleanup.yml --repo "$repository" --ref main \
  -f "run_id=$run_id" -f "request_id=$request_id" >&2
cleanup_run="$(find_cleanup_run "$request_id")" || fail "cannot locate the correlated exact Cleanup run"
gh run watch "$cleanup_run" --repo "$repository" --exit-status >&2 || \
  fail "exact Cleanup failed: https://github.com/$repository/actions/runs/$cleanup_run"

verification_log="$finalize_temp/release-verification.log"
verification_result="$finalize_temp/release-verification.json"
set +e
"$SCRIPT_DIR/analyze.sh" "$run_id" --repository "$repository" --result-file "$verification_result" 2>&1 | tee "$verification_log"
verification_status="${PIPESTATUS[0]}"
set -e
((verification_status == 0)) || fail "provider release verification failed after exact Cleanup"
jq -e --arg run_id "$run_id" '
  .schema == "wukongim/cloud-simulation-analysis-result/v1" and
  .run_id == $run_id and .state == "released" and .diagnosis == null
' "$verification_result" >/dev/null || \
  fail "exact Cleanup completed but provider inventory did not return the structured released state"

if ((analysis_status != 0)); then
  printf 'Finalization cleaned and verified the exact Run, but terminal analysis failed.\n' >&2
  exit "$analysis_status"
fi
printf 'Finalization complete: terminal analysis finished and provider inventory is empty.\n'
