#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${WK_CHAT_SOURCE_SHA:?required}"
: "${WK_CHAT_OPERATOR:?required}"
: "${WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY:?required}"
: "${WK_CHAT_REQUEST_ID:?required}"
: "${WK_CHAT_WRAPPING_PUBLIC_KEY:?required}"
: "${WK_CHAT_TOOL:?required}"
: "${WK_CHAT_TEMPLATE:?required}"
: "${WK_CHAT_OUTPUT_DIR:?required}"
: "${WK_CHAT_WORK_DIR:?required}"

[[ "$WK_CHAT_SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$WK_CHAT_OPERATOR" == tangtaoit ]]
[[ "$WK_CHAT_REQUEST_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
[[ -x "$WK_CHAT_TOOL" && -f "$WK_CHAT_TEMPLATE" ]]

chat_stage="${WK_CHAT_STAGE:-rehearsal}"
case "$chat_stage" in
  repair)
    : "${GITHUB_RUN_ID:?required for repair handoff recovery}"
    [[ "$GITHUB_RUN_ID" =~ ^[1-9][0-9]*$ ]]
    stage_service=wkbench-rehearsal.service
    stage_report_dir=rehearsal
    stage_runtime_name=rehearsal
    stage_duration_seconds=7200
    reserved_stage_duration_seconds=600
    repair_max_seconds=600
    stage_handoff_schema=wukongim.chat_lifecycle.repair_handoff/v1
    ;;
  rehearsal)
    stage_service=wkbench-rehearsal.service
    stage_report_dir=rehearsal
    stage_runtime_name=rehearsal
    stage_duration_seconds=7200
    reserved_stage_duration_seconds="$stage_duration_seconds"
    stage_handoff_schema=wukongim.chat_lifecycle.rehearsal_handoff/v1
    ;;
  formal)
    : "${WK_CHAT_BUNDLE_RUN_ID:?required for formal}"
    : "${WK_CHAT_BUNDLE_DIGEST:?required for formal}"
    : "${WK_CHAT_TRANSITION:?required for formal}"
    [[ "$WK_CHAT_BUNDLE_RUN_ID" =~ ^[1-9][0-9]*$ ]]
    [[ "$WK_CHAT_BUNDLE_DIGEST" =~ ^sha256:[0-9a-f]{64}$ ]]
    [[ -f "$WK_CHAT_TRANSITION" ]]
    stage_service=wkbench-formal.service
    stage_report_dir=formal
    stage_runtime_name=formal
    stage_duration_seconds=259200
    reserved_stage_duration_seconds="$stage_duration_seconds"
    stage_handoff_schema=wukongim.chat_lifecycle.formal_handoff/v1
    ;;
  *)
    echo 'unsupported chat lifecycle stage' >&2
    exit 2
    ;;
esac

install -d -m 0700 "$WK_CHAT_OUTPUT_DIR"
install -d -m 0700 "$WK_CHAT_WORK_DIR"
active_selector=''
keep_active=false
release_generation=0
cleanup_attempted=false
deployment_key=''
DISPATCH_RUN_ID=''
operator_stop_requested=false
operator_stop_authority_failed=false
orchestration_started_epoch="$(date -u +%s)"
orchestration_deadline_epoch=$(( orchestration_started_epoch + 17100 ))

check_operator_stop() {
  local marker status=0
  if [[ "$operator_stop_requested" == true ]]; then
    return 0
  fi
  marker="$(scripts/chat-lifecycle/operator-stop-requested.sh "$WK_CHAT_REQUEST_ID")" || status=$?
  case "$status" in
    0)
      operator_stop_requested=true
      printf '%s\n' "$marker" >"$WK_CHAT_OUTPUT_DIR/operator-stop.json"
      return 0
      ;;
    1) return 1 ;;
    *)
      operator_stop_authority_failed=true
      return "$status"
      ;;
  esac
}

workflow_run_max_id() {
  local workflow="$1"
  gh run list --repo "$GITHUB_REPOSITORY" --workflow "$workflow" --event workflow_dispatch --branch main \
    --limit 50 --json databaseId --jq 'map(.databaseId) | max // 0'
}

dispatch_and_wait() {
  local workflow="$1"
  local title="$2"
  shift 2
  local before deadline rows count watch_timeout_seconds stop_status run_json run_status conclusion
  local cancel_requested=false
  DISPATCH_RUN_ID=''
  if [[ "$workflow" != cloud-lease-release.yml ]]; then
    stop_status=0
    check_operator_stop || stop_status=$?
    case "$stop_status" in
      0) return 130 ;;
      1) ;;
      *) return "$stop_status" ;;
    esac
  fi
  before="$(workflow_run_max_id "$workflow")" || return
  gh workflow run "$workflow" --repo "$GITHUB_REPOSITORY" --ref main "$@" || return
  deadline=$(( $(date -u +%s) + 180 ))
  while true; do
    rows="$(gh run list --repo "$GITHUB_REPOSITORY" --workflow "$workflow" --event workflow_dispatch --branch main \
      --limit 50 --json databaseId,displayTitle,status,conclusion)" || return
    count="$(jq -r --argjson before "$before" --arg title "$title" \
      '[.[] | select(.databaseId > $before and .displayTitle == $title)] | length' <<<"$rows")" || return
    if [[ "$count" == 1 ]]; then
      DISPATCH_RUN_ID="$(jq -r --argjson before "$before" --arg title "$title" \
        '.[] | select(.databaseId > $before and .displayTitle == $title) | .databaseId' <<<"$rows")" || return
      break
    fi
    if [[ "$count" != 0 || "$(date -u +%s)" -ge "$deadline" ]]; then
      return 1
    fi
    sleep 2
  done
  case "$workflow" in
    cloud-deployment-bundle.yml|cloud-lease-provision.yml) watch_timeout_seconds=2880 ;;
    cloud-deployment-activate.yml) watch_timeout_seconds=3780 ;;
    chat-lifecycle-repair-handoff.yml) watch_timeout_seconds=900 ;;
    cloud-lease-release.yml) watch_timeout_seconds=2580 ;;
    *) return 1 ;;
  esac
  deadline=$(( $(date -u +%s) + watch_timeout_seconds ))
  while true; do
    if [[ "$workflow" != cloud-lease-release.yml && "$operator_stop_requested" != true ]]; then
      stop_status=0
      check_operator_stop || stop_status=$?
      case "$stop_status" in
        0) ;;
        1) ;;
        *) operator_stop_authority_failed=true ;;
      esac
    fi
    if [[ "$operator_stop_requested" == true && "$workflow" != cloud-lease-release.yml && "$cancel_requested" != true ]]; then
      for cancel_attempt in 1 2 3; do
        if gh run cancel "$DISPATCH_RUN_ID" --repo "$GITHUB_REPOSITORY"; then
          cancel_requested=true
          break
        fi
        sleep 2
      done
    fi

    run_json="$(gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${DISPATCH_RUN_ID}")" || {
      [[ "$(date -u +%s)" -lt "$deadline" ]] || return 124
      sleep 10
      continue
    }
    run_status="$(jq -er .status <<<"$run_json")" || return 2
    if [[ "$run_status" == completed ]]; then
      conclusion="$(jq -er .conclusion <<<"$run_json")" || return 2
      if [[ "$workflow" != cloud-lease-release.yml ]]; then
        [[ "$operator_stop_requested" != true ]] || return 130
        [[ "$operator_stop_authority_failed" != true ]] || return 2
      fi
      [[ "$conclusion" == success ]]
      return
    fi
    if [[ "$(date -u +%s)" -ge "$deadline" ]]; then
      gh run cancel "$DISPATCH_RUN_ID" --repo "$GITHUB_REPOSITORY" || true
      return 124
    fi
    sleep 10
  done
}

download_run() {
  local run_id="$1"
  local destination="$2"
  [[ ! -e "$destination" ]] || return 1
  install -d -m 0700 "$destination" || return
  gh run download "$run_id" --repo "$GITHUB_REPOSITORY" --dir "$destination"
}

rebuild_repair_bundle() {
  local candidate_sha="$1" candidate_dir candidate_title
  [[ "$chat_stage" == repair && "$candidate_sha" =~ ^[0-9a-f]{40}$ ]]
  candidate_dir="$WK_CHAT_WORK_DIR/repair-bundle-$candidate_sha"
  candidate_title="Cloud Deployment Bundle $candidate_sha $WK_CHAT_REQUEST_ID"
  dispatch_and_wait cloud-deployment-bundle.yml "$candidate_title" \
    -f source_sha="$candidate_sha" -f request_id="$WK_CHAT_REQUEST_ID"
  bundle_run_id="$DISPATCH_RUN_ID"
  download_run "$bundle_run_id" "$candidate_dir"
  mapfile -t candidate_manifests < <(find "$candidate_dir" -type f -name bundle-manifest-output.json -print)
  (( ${#candidate_manifests[@]} == 1 ))
  bundle_digest="$(jq -er '.bundle_digest | select(test("^sha256:[0-9a-f]{64}$"))' "${candidate_manifests[0]}")"
  bundle_artifact="cloud-deployment-bundle-${bundle_digest#sha256:}"
  WK_CHAT_SOURCE_SHA="$candidate_sha"
  export WK_CHAT_SOURCE_SHA
}

publish_repair_checkpoint() {
  local generation_dir="$1" checkpoint_title terminal_cut terminal_cut_bytes
  [[ "$chat_stage" == repair && -s "$generation_dir/repair-decision.json" && -s "$generation_dir/repair-diagnosis.json" ]]
  for worker in 1 2 3; do
    [[ -s "$generation_dir/terminal-cut/status-${worker}.json" &&
       -s "$generation_dir/terminal-cut/snapshot-${worker}.json" ]]
  done
  terminal_cut="$generation_dir/terminal-cut.json"
  jq -n --arg schema 'wukongim.chat_lifecycle.repair_terminal_cut/v1' \
    --argjson generation "$deployment_generation" \
    --slurpfile status1 "$generation_dir/terminal-cut/status-1.json" \
    --slurpfile snapshot1 "$generation_dir/terminal-cut/snapshot-1.json" \
    --slurpfile status2 "$generation_dir/terminal-cut/status-2.json" \
    --slurpfile snapshot2 "$generation_dir/terminal-cut/snapshot-2.json" \
    --slurpfile status3 "$generation_dir/terminal-cut/status-3.json" \
    --slurpfile snapshot3 "$generation_dir/terminal-cut/snapshot-3.json" '
      {schema:$schema,generation:$generation,workers:[
        {worker:1,status:$status1[0],snapshot:$snapshot1[0]},
        {worker:2,status:$status2[0],snapshot:$snapshot2[0]},
        {worker:3,status:$status3[0],snapshot:$snapshot3[0]}]}
    ' >"$terminal_cut"
  terminal_cut_bytes="$(stat -c '%s' "$terminal_cut" 2>/dev/null || stat -f '%z' "$terminal_cut")"
  [[ "$terminal_cut_bytes" =~ ^[1-9][0-9]*$ && "$terminal_cut_bytes" -le 45000 ]]
  checkpoint_title="Chat Lifecycle Repair Handoff $WK_CHAT_REQUEST_ID $GITHUB_RUN_ID"
  dispatch_and_wait chat-lifecycle-repair-handoff.yml "$checkpoint_title" \
    -f request_id="$WK_CHAT_REQUEST_ID" -f parent_run_id="$GITHUB_RUN_ID" \
    -f artifact_kind=checkpoint -f generation="$deployment_generation" \
    -f receipt_json="$(jq -c . "$attempt_dir/receipt.json")" \
    -f selector_json="$(jq -c . "$active_selector")" \
    -f decision_json="$(jq -c . "$generation_dir/repair-decision.json")" \
    -f diagnosis_json="$(jq -c . "$generation_dir/repair-diagnosis.json")" \
    -f terminal_cut_json="$(jq -c . "$terminal_cut")" \
    -f publish_authorization=publish-chat-lifecycle-repair-handoff
}

release_current() {
  [[ -n "$active_selector" && -f "$active_selector" ]] || return 0
  cleanup_attempted=true
  local selector="$active_selector"
  release_generation=$(( release_generation + 1 ))
  local release_index release_dir release_ok=false
  for release_index in 1; do
    release_dir="$WK_CHAT_OUTPUT_DIR/release-${release_generation}-${release_index}"
    if dispatch_and_wait cloud-lease-release.yml "Cloud Lease Release $WK_CHAT_REQUEST_ID" \
      -f request_id="$WK_CHAT_REQUEST_ID" -f operation=release \
      -f selector_json="$(jq -c . "$selector")" -f release_authorization=release-tagged-cloud-lease; then
      :
    fi
    download_run "$DISPATCH_RUN_ID" "$release_dir" || true
    mapfile -t release_files < <(find "$release_dir" -type f -name release.json -print)
    if (( ${#release_files[@]} == 1 )) && jq -e --slurpfile expected "$selector" '
      .schema == "wukongim.cloud_lease.release/v1" and
      (.result.zero_inventory | type == "object") and
      .result.zero_inventory.selector == $expected[0].selector and
      (.result.zero_inventory.account_id_hash | test("^sha256:[0-9a-f]{64}$")) and
      (.result.zero_inventory.observed_at | type == "string") and
      (.result.zero_inventory.scopes | type == "array" and length > 0)
    ' \
      "${release_files[0]}" >/dev/null; then
      release_ok=true
      active_selector=''
      cp "$selector" "$WK_CHAT_OUTPUT_DIR/release-selector.json"
      cp "${release_files[0]}" "$WK_CHAT_OUTPUT_DIR/zero-inventory.json"
      jq -n --arg schema "wukongim.chat_lifecycle.${chat_stage}_cleanup/v1" \
        --arg request_id "$WK_CHAT_REQUEST_ID" --argjson release_run_id "$DISPATCH_RUN_ID" \
        --slurpfile release "${release_files[0]}" \
        '{schema:$schema,request_id:$request_id,release_run_id:$release_run_id,
          zero_inventory:($release[0].result.zero_inventory != null)}' \
        >"$WK_CHAT_OUTPUT_DIR/cleanup.json"
      break
    fi
  done
  if [[ "$release_ok" != true ]]; then
    cp "$selector" "$WK_CHAT_OUTPUT_DIR/release-selector.json"
    jq -n --arg schema 'wukongim.chat_lifecycle.cleanup_pending/v1' \
      --arg request_id "$WK_CHAT_REQUEST_ID" --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      --argjson attempt "$attempt" \
      '{schema:$schema,request_id:$request_id,attempt:$attempt,observed_at:$observed_at}' \
      >"$WK_CHAT_OUTPUT_DIR/cleanup-pending.json"
  fi
  [[ "$release_ok" == true ]]
}

cleanup_on_exit() {
  local status=$?
  if [[ "$keep_active" != true && "$cleanup_attempted" != true && -n "$active_selector" ]]; then
    release_current || status=1
  fi
  [[ -z "$deployment_key" ]] || rm -f "$deployment_key" "${deployment_key}.pub"
  exit "$status"
}
trap cleanup_on_exit EXIT
trap 'exit 130' INT TERM

if [[ "$chat_stage" == rehearsal || "$chat_stage" == repair ]]; then
  bundle_title="Cloud Deployment Bundle $WK_CHAT_SOURCE_SHA $WK_CHAT_REQUEST_ID"
  dispatch_and_wait cloud-deployment-bundle.yml "$bundle_title" \
    -f source_sha="$WK_CHAT_SOURCE_SHA" -f request_id="$WK_CHAT_REQUEST_ID"
  bundle_run_id="$DISPATCH_RUN_ID"
  download_run "$bundle_run_id" "$WK_CHAT_WORK_DIR/bundle"
  mapfile -t bundle_manifests < <(find "$WK_CHAT_WORK_DIR/bundle" -type f -name bundle-manifest-output.json -print)
  (( ${#bundle_manifests[@]} == 1 ))
  bundle_digest="$(jq -er '.bundle_digest | select(test("^sha256:[0-9a-f]{64}$"))' "${bundle_manifests[0]}")"
else
  bundle_run_id="$WK_CHAT_BUNDLE_RUN_ID"
  bundle_digest="$WK_CHAT_BUNDLE_DIGEST"
fi
bundle_artifact="cloud-deployment-bundle-${bundle_digest#sha256:}"

committed_micros=0
if [[ "$chat_stage" == formal ]]; then
  committed_micros="$(jq -er --arg request "$WK_CHAT_REQUEST_ID" --arg source "$WK_CHAT_SOURCE_SHA" \
    --arg bundle "$bundle_digest" -f scripts/chat-lifecycle/select-formal-transition-committed.jq \
    "$WK_CHAT_TRANSITION")"
fi
release_failed_attempt() {
  local quote_file="$1"
  local reason="$2"
  release_current
  local attempt_dir created_at ended_at actual_cost
  attempt_dir="$(dirname "$quote_file")"
  created_at="$(jq -er '.receipt.created_at // empty' "$attempt_dir/receipt.json" 2>/dev/null || jq -er .quote.quoted_at "$quote_file")"
  ended_at="$(jq -er .result.zero_inventory.observed_at "$WK_CHAT_OUTPUT_DIR/zero-inventory.json")"
  actual_cost="$(scripts/chat-lifecycle/accrued-cost.sh "$attempt_dir/run-plan.json" "$quote_file" "$created_at" "$ended_at" -1)"
  committed_micros=$(( committed_micros + actual_cost ))
  rm -f "$deployment_key" "${deployment_key}.pub"
  while IFS= read -r encrypted_identity; do
    rm -f "$encrypted_identity"
  done < <(find "$attempt_dir" -type f -name encrypted-deployment-identity.json -print)
  deployment_key=''
  if [[ "$operator_stop_requested" == true ]]; then
    echo 'operator stop canceled the in-flight stage after exact zero-inventory cleanup' >&2
    exit 130
  fi
  if [[ "$operator_stop_authority_failed" == true ]]; then
    echo 'operator-stop authority became unavailable; paid Lease was released after cleanup' >&2
    exit 1
  fi
  echo "$reason" >&2
  exit 1
}

read_pre_clock_terminal_code() {
  local journal_cursor="$1"
  local summary terminal_code
  [[ "$journal_cursor" =~ ^[A-Za-z0-9_=\;:.-]{1,512}$ ]] || return 1
  summary="$(timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
    "sudo journalctl -u '$stage_service' --after-cursor='$journal_cursor' --no-pager -o cat | grep '^chat-lifecycle outcome=' | tail -n 1 | head -c 1025" \
    2>/dev/null)" || return 1
  (( ${#summary} <= 1024 )) || return 1
  terminal_code="$(scripts/chat-lifecycle/classify-pre-clock-summary.sh "$summary")" || return 1
  jq -n --arg schema 'wukongim.chat_lifecycle.pre_clock_terminal/v1' \
    --arg request_id "$WK_CHAT_REQUEST_ID" --arg stage "$chat_stage" \
    --arg summary "$summary" --arg coordinator_code "$terminal_code" \
    '{schema:$schema,request_id:$request_id,stage:$stage,summary:$summary,
      coordinator_code:$coordinator_code}' \
    >"$WK_CHAT_OUTPUT_DIR/pre-clock-terminal.json"
  printf '%s\n' "$terminal_code"
}

record_deployment_repair_pending() {
  local failure_code="$1"
  local last_gate="$2"
  local repair_deadline="$3"
  local issue_status=1
  jq -n --arg schema "wukongim.chat_lifecycle.${chat_stage}_deployment_repair/v1" \
    --arg request_id "$WK_CHAT_REQUEST_ID" --arg source_sha "$WK_CHAT_SOURCE_SHA" \
    --arg bundle_digest "$bundle_digest" --arg lease_id "$lease_id" \
    --arg failure_code "$failure_code" --arg last_successful_gate "$last_gate" \
    --arg control_sha "$deployment_control_sha" --arg repair_deadline "$repair_deadline" \
    --argjson acquire_run_id "$acquire_run_id" --argjson deployment_run_id "$deployment_run_id" \
    --argjson generation "$deployment_generation" \
    '{schema:$schema,request_id:$request_id,source_sha:$source_sha,bundle_digest:$bundle_digest,
      lease_id:$lease_id,failure_code:$failure_code,last_successful_gate:$last_successful_gate,
      control_sha:$control_sha,repair_deadline:$repair_deadline,acquire_run_id:$acquire_run_id,
      deployment_run_id:$deployment_run_id,generation:$generation}' \
    >"$WK_CHAT_OUTPUT_DIR/deployment-repair-pending.json"
  export WK_CHAT_ISSUE_STATE="${chat_stage}_deployment_repair_pending"
  export WK_CHAT_ISSUE_DEDUPE_KEY="${chat_stage}_deployment_repair_${deployment_generation}"
  export WK_CHAT_ISSUE_BODY="failure_code=${failure_code} last_gate=${last_gate} deployment_run=https://github.com/${GITHUB_REPOSITORY}/actions/runs/${deployment_run_id} control_sha=${deployment_control_sha} repair_deadline_utc=${repair_deadline}; retaining the exact Lease and waiting for a protected-main revision with trailer Chat-Lifecycle-Repair: ${WK_CHAT_REQUEST_ID}"
  for issue_attempt in 1 2 3; do
    if scripts/chat-lifecycle/comment-request-issue.sh; then
      issue_status=0
      break
    fi
    sleep 2
  done
  unset WK_CHAT_ISSUE_STATE WK_CHAT_ISSUE_DEDUPE_KEY WK_CHAT_ISSUE_BODY
  return "$issue_status"
}

wait_for_deployment_repair_revision() {
  local attempted_shas_file="$1"
  local repair_deadline_epoch="$2"
  local created_at current_cost aggregate_cost operational_stop stop_status candidate_json candidate_sha
  created_at="$(jq -er .receipt.created_at "$attempt_dir/receipt.json")"
  operational_stop="$(jq -er .lease_plan.budget.operational_stop_micros "$attempt_dir/run-plan.json")"
  while true; do
    stop_status=0
    check_operator_stop || stop_status=$?
    case "$stop_status" in
      0) return 130 ;;
      1) ;;
      *) operator_stop_authority_failed=true; return "$stop_status" ;;
    esac
    if (( $(date -u +%s) >= repair_deadline_epoch )); then
      return 124
    fi
    current_cost="$(scripts/chat-lifecycle/accrued-cost.sh "$attempt_dir/run-plan.json" \
      "$attempt_dir/quote.json" "$created_at" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" -1)" || return
    aggregate_cost=$(( committed_micros + current_cost ))
    if (( aggregate_cost >= operational_stop )); then
      return 125
    fi
    candidate_json="$(gh api "/repos/${GITHUB_REPOSITORY}/commits/main" \
      --jq '{sha:.sha,message:.commit.message}' 2>/dev/null || true)"
    candidate_sha="$(jq -er .sha <<<"$candidate_json" 2>/dev/null || true)"
    if [[ "$candidate_sha" =~ ^[0-9a-f]{40}$ ]] && ! grep -Fqx "$candidate_sha" "$attempted_shas_file" &&
      jq -e --arg trailer "Chat-Lifecycle-Repair: $WK_CHAT_REQUEST_ID" \
        '.message | split("\n") | any(. == $trailer)' <<<"$candidate_json" >/dev/null; then
      printf '%s\n' "$candidate_sha"
      return 0
    fi
    sleep 30
  done
}

run_deployment_action() {
  local control_identity_attempt deployment_purpose deployment_plan_generation
  deployment_generation=$(( deployment_generation + 1 ))
  deployment_purpose=immutable
  deployment_plan_generation=1
  if [[ "$chat_stage" == repair ]]; then
    deployment_purpose=repair
    deployment_plan_generation="$deployment_generation"
  fi
  deployment_title="Cloud Deployment $lease_artifact $deployment_purpose generation $deployment_plan_generation"
  deployment_artifact_dir="$attempt_dir/deployment-${deployment_generation}-artifact"
  deployment_failed=false
  if ! dispatch_and_wait cloud-deployment-activate.yml "$deployment_title" \
    -f lease_artifact_run_id="$acquire_run_id" -f lease_artifact_name="$lease_artifact" \
    -f bundle_artifact_run_id="$bundle_run_id" -f bundle_artifact_name="$bundle_artifact" \
    -f codex_diagnostic_pubkey="$WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY" \
    -f deployment_purpose="$deployment_purpose" -f deployment_generation="$deployment_plan_generation" \
    -f encrypted_deployment_identity_json="$(jq -c . "$attempt_dir/encrypted-deployment-identity.json")"; then
    deployment_failed=true
  fi
  deployment_run_id="${DISPATCH_RUN_ID:-0}"
  deployment_control_sha=''
  deployment_control_identity_valid=false
  if [[ "$deployment_run_id" =~ ^[1-9][0-9]*$ ]]; then
    for control_identity_attempt in 1 2 3; do
      deployment_control_sha="$(gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${deployment_run_id}" \
        --jq .head_sha 2>/dev/null || true)"
      if [[ "$deployment_control_sha" =~ ^[0-9a-f]{40}$ ]]; then
        deployment_control_identity_valid=true
        break
      fi
      sleep 2
    done
  fi
  if [[ "$deployment_control_identity_valid" == true ]] && \
    ! grep -Fqx "$deployment_control_sha" "$attempted_control_shas_file"; then
    printf '%s\n' "$deployment_control_sha" >>"$attempted_control_shas_file"
  fi
  if [[ "$deployment_run_id" =~ ^[1-9][0-9]*$ ]]; then
    download_run "$deployment_run_id" "$deployment_artifact_dir" || true
  fi
  mapfile -t deployment_outcomes < <(find "$deployment_artifact_dir" -type f -name deployment-outcome.json -print 2>/dev/null || true)
  if (( ${#deployment_outcomes[@]} != 1 )) || ! jq -e \
    '.passed == true and .receipt.schema == "wukongim.cloud_deployment.receipt/v2"' \
    "${deployment_outcomes[0]:-/dev/null}" >/dev/null; then
    deployment_failed=true
  fi
  mapfile -t encrypted_access < <(find "$deployment_artifact_dir" -type f -name encrypted-access.json -print 2>/dev/null || true)
  mapfile -t analysis_endpoints < <(find "$deployment_artifact_dir" -type f -name analysis-endpoint.json -print 2>/dev/null || true)
  mapfile -t encrypted_deployment_identities < <(find "$deployment_artifact_dir" -type f -name encrypted-deployment-identity.json -print 2>/dev/null || true)
  if [[ "$deployment_failed" == true ]] ||
    (( ${#deployment_outcomes[@]} != 1 || ${#encrypted_access[@]} != 1 || ${#analysis_endpoints[@]} != 1 || ${#encrypted_deployment_identities[@]} != 1 )) ||
    ! cmp -s "$attempt_dir/encrypted-deployment-identity.json" "${encrypted_deployment_identities[0]:-/dev/null}"; then
    deployment_failed=true
    return 0
  fi
  deployment_lease_id="$(jq -er .receipt.lease_id "${deployment_outcomes[0]}")" || {
    deployment_failed=true
    return 0
  }
  deployment_plan_digest="$(jq -er .receipt.deployment_plan_digest "${deployment_outcomes[0]}")" || {
    deployment_failed=true
    return 0
  }
  if ! jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg source "$WK_CHAT_SOURCE_SHA" \
    --arg lease "$deployment_lease_id" --arg plan "$deployment_plan_digest" '
      .schema == "wukongim.chat_lifecycle.encrypted_access/v1" and
      .algorithm == "x25519-xsalsa20-poly1305-sealed-box" and .request_id == $request and
      .source_sha == $source and .lease_id == $lease and .deployment_plan_digest == $plan and
      (.recipient_fingerprint | startswith("SHA256:")) and (.ciphertext_base64 | length > 0)
    ' "${encrypted_access[0]}" >/dev/null; then
    deployment_failed=true
  fi
  if ! jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg source "$WK_CHAT_SOURCE_SHA" \
    --arg lease "$deployment_lease_id" --arg plan "$deployment_plan_digest" '
      .schema == "wukongim.chat_lifecycle.analysis_endpoint/v1" and
      .request_id == $request and .source_sha == $source and .lease_id == $lease and
      .deployment_plan_digest == $plan and .provider == "alibaba" and .region == "cn-hangzhou" and
      (.mcp_url | test("^https://[0-9.]+:19444/mcp$")) and
      (.ca_fingerprint | test("^sha256:[0-9a-f]{64}$"))
    ' "${analysis_endpoints[0]:-/dev/null}" >/dev/null; then
    deployment_failed=true
  fi
}

for attempt in 1; do
  attempt_dir="$WK_CHAT_OUTPUT_DIR/attempt-$attempt"
  install -d -m 0700 "$attempt_dir"
  deployment_key="$WK_CHAT_WORK_DIR/deployment-${chat_stage}-${attempt}"
  ssh-keygen -q -t ed25519 -N '' -C "wukongim-chat-lifecycle-${WK_CHAT_REQUEST_ID}-${chat_stage}-${attempt}" -f "$deployment_key"
  chmod 0600 "$deployment_key"
  deployment_public_key="$(<"${deployment_key}.pub")"
  materialize_args=(
    materialize --template "$WK_CHAT_TEMPLATE" --source-sha "$WK_CHAT_SOURCE_SHA"
    --operator "$WK_CHAT_OPERATOR" --codex-diagnostic-pubkey "$WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY"
    --request-id "$WK_CHAT_REQUEST_ID" --repository "$GITHUB_REPOSITORY"
    --bundle-digest "$bundle_digest" --deployment-pubkey "$deployment_public_key"
    --now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --attempt "$attempt"
    --committed-micros "$committed_micros"
  )
  if [[ "$chat_stage" == formal ]]; then
    materialize_args+=(--transition "$WK_CHAT_TRANSITION")
  fi
  "$WK_CHAT_TOOL" "${materialize_args[@]}" >"$attempt_dir/run-plan.json"
  jq -c '.lease_plan' "$attempt_dir/run-plan.json" >"$attempt_dir/lease-plan.json"
  jq -c '{schema:"wukongim.cloud_lease.bootstrap_access/v1",access:.bootstrap_access}' \
    "$attempt_dir/run-plan.json" >"$attempt_dir/bootstrap-access.json"

  provision_title="Cloud Lease Provision $WK_CHAT_REQUEST_ID"
  dispatch_and_wait cloud-lease-provision.yml "$provision_title" \
    -f request_id="$WK_CHAT_REQUEST_ID" -f plan_json="$(cat "$attempt_dir/lease-plan.json")" \
    -f bootstrap_access_json="$(cat "$attempt_dir/bootstrap-access.json")" -f quote_only=true
  quote_run_id="$DISPATCH_RUN_ID"
  download_run "$quote_run_id" "$attempt_dir/quote-artifact"
  mapfile -t quote_files < <(find "$attempt_dir/quote-artifact" -type f -name quote.json -print)
  (( ${#quote_files[@]} == 1 ))
  cp "${quote_files[0]}" "$attempt_dir/quote.json"
  jq -e --arg request "$WK_CHAT_REQUEST_ID" '
    .schema == "wukongim.cloud_lease.quote/v1" and .quote.request_id == $request and
    .quote.capacity_available == true and .quote.quota_available == true and
    (.quote.plan_digest | test("^[0-9a-f]{64}$")) and
    (.quote.selection.instance_type | length > 0) and (.quote.zone | length > 0)
  ' "$attempt_dir/quote.json" >/dev/null
  cp "$attempt_dir/quote.json" "$attempt_dir/preflight-quote.json"
  "$WK_CHAT_TOOL" selector-from-plan --plan "$attempt_dir/lease-plan.json" \
    --quote "$attempt_dir/preflight-quote.json" >"$attempt_dir/release-selector.json"
  active_selector="$attempt_dir/release-selector.json"

  planned_lease_id="$(jq -er .lease_plan.lease_id "$attempt_dir/run-plan.json")"
  planned_plan_digest="$(jq -er .selector.plan_digest "$attempt_dir/release-selector.json")"
  planned_expires_at="$(jq -er .lease_plan.expires_at "$attempt_dir/run-plan.json")"
  "$WK_CHAT_TOOL" seal-deployment-identity \
    --recipient "$WK_CHAT_WRAPPING_PUBLIC_KEY" --identity "$deployment_key" \
    --request-id "$WK_CHAT_REQUEST_ID" --lease-id "$planned_lease_id" \
    --source-sha "$WK_CHAT_SOURCE_SHA" --plan-digest "$planned_plan_digest" \
    --expires-at "$planned_expires_at" >"$attempt_dir/encrypted-deployment-identity.json"

  acquire_failed=false
  if ! dispatch_and_wait cloud-lease-provision.yml "$provision_title" \
    -f request_id="$WK_CHAT_REQUEST_ID" -f plan_json="$(cat "$attempt_dir/lease-plan.json")" \
    -f bootstrap_access_json="$(cat "$attempt_dir/bootstrap-access.json")" -f quote_only=false \
    -f admitted_quote_json="$(cat "$attempt_dir/preflight-quote.json")" \
    -f paid_authorization=create-paid-cloud-lease \
    -f repair_parent_run_id="$([[ "$chat_stage" == repair ]] && printf '%s' "$GITHUB_RUN_ID" || printf '0')"; then
    acquire_failed=true
  fi
  acquire_run_id="$DISPATCH_RUN_ID"
  download_run "$acquire_run_id" "$attempt_dir/acquire-artifact" || true
  mapfile -t acquired_quote_files < <(find "$attempt_dir/acquire-artifact" -type f -name quote.json -print)
  if (( ${#acquired_quote_files[@]} == 1 )) && jq -e --arg request "$WK_CHAT_REQUEST_ID" \
    --arg plan_digest "$(jq -er .quote.plan_digest "$attempt_dir/preflight-quote.json")" '
      .schema == "wukongim.cloud_lease.quote/v1" and .quote.request_id == $request and
      .quote.plan_digest == $plan_digest and .quote.capacity_available == true and
      .quote.quota_available == true and (.quote.selection.instance_type | length > 0) and
      (.quote.zone | length > 0)
    ' "${acquired_quote_files[0]}" >/dev/null; then
    cp "${acquired_quote_files[0]}" "$attempt_dir/quote.json"
  else
    acquire_failed=true
  fi
  mapfile -t receipt_files < <(find "$attempt_dir/acquire-artifact" -type f -name receipt.json -print)
  receipt_active=false
  if (( ${#receipt_files[@]} == 1 )); then
    cp "${receipt_files[0]}" "$attempt_dir/receipt.json"
    if jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg source "$WK_CHAT_SOURCE_SHA" --arg bundle "$bundle_digest" \
      --arg lease "$planned_lease_id" --arg plan "$planned_plan_digest" --arg expires "$planned_expires_at" '
      .schema == "wukongim.cloud_lease.receipt/v1" and .receipt.request_id == $request and
      .receipt.lease_id == $lease and .receipt.plan_digest == $plan and .receipt.expires_at == $expires and
      .receipt.state == "active" and .receipt.provenance.source_sha == $source and
      .receipt.provenance.bundle_digest == $bundle
    ' "$attempt_dir/receipt.json" >/dev/null &&
      "$WK_CHAT_TOOL" selector --receipt "$attempt_dir/receipt.json" >"$attempt_dir/receipt-selector.json" &&
      cmp -s <(jq -S -c .selector "$attempt_dir/receipt-selector.json") \
        <(jq -S -c .selector "$attempt_dir/release-selector.json") &&
      cmp -s <(jq -S -c .receipt.quote "$attempt_dir/receipt.json") \
        <(jq -S -c .quote "$attempt_dir/quote.json") &&
      jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg source "$WK_CHAT_SOURCE_SHA" \
        --arg lease "$planned_lease_id" --arg plan "$planned_plan_digest" --arg expires "$planned_expires_at" '
        .schema == "wukongim.chat_lifecycle.encrypted_deployment_identity/v1" and
        .request_id == $request and .lease_id == $lease and .source_sha == $source and
        .plan_digest == $plan and .expires_at == $expires and
        (.deployment_public_key | startswith("ssh-ed25519 ")) and
        (.recipient_fingerprint | startswith("SHA256:")) and (.ciphertext_base64 | length > 0)
      ' "$attempt_dir/encrypted-deployment-identity.json" >/dev/null; then
      receipt_active=true
    fi
  fi
  if [[ "$acquire_failed" == true || "$receipt_active" != true ]]; then
    release_failed_attempt "$attempt_dir/preflight-quote.json" \
      'acquisition failure is terminal after exact zero-inventory cleanup'
  fi

  lease_id="$(jq -er .receipt.lease_id "$attempt_dir/receipt.json")"
  lease_plan_digest="$(jq -er .receipt.plan_digest "$attempt_dir/receipt.json")"
  lease_expires_at="$(jq -er .receipt.expires_at "$attempt_dir/receipt.json")"

  if [[ "$chat_stage" == repair ]]; then
    repair_handoff_title="Chat Lifecycle Repair Handoff $WK_CHAT_REQUEST_ID $GITHUB_RUN_ID"
    if ! dispatch_and_wait chat-lifecycle-repair-handoff.yml "$repair_handoff_title" \
      -f request_id="$WK_CHAT_REQUEST_ID" -f parent_run_id="$GITHUB_RUN_ID" \
      -f artifact_kind=handoff -f generation=0 \
      -f receipt_json="$(jq -c . "$attempt_dir/receipt.json")" \
      -f selector_json="$(jq -c . "$active_selector")" \
      -f publish_authorization=publish-chat-lifecycle-repair-handoff; then
      release_failed_attempt "$attempt_dir/quote.json" \
        'durable repair handoff publication failed; exact Lease was released'
    fi
    repair_handoff_run_id="$DISPATCH_RUN_ID"
    repair_handoff_dir="$attempt_dir/repair-handoff-artifact"
    download_run "$repair_handoff_run_id" "$repair_handoff_dir"
    mapfile -t repair_handoffs < <(find "$repair_handoff_dir" -type f -name repair-handoff.json -print)
    mapfile -t repair_handoff_selectors < <(find "$repair_handoff_dir" -type f -name release-selector.json -print)
    if (( ${#repair_handoffs[@]} != 1 || ${#repair_handoff_selectors[@]} != 1 )) ||
      ! jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg lease "$lease_id" \
        --argjson parent_run_id "$GITHUB_RUN_ID" '
        .schema == "wukongim.chat_lifecycle.repair_handoff/v1" and .request_id == $request and
        .lease_id == $lease and .parent_run_id == $parent_run_id
      ' "${repair_handoffs[0]:-/dev/null}" >/dev/null ||
      ! cmp -s <(jq -S -c . "$active_selector") <(jq -S -c . "${repair_handoff_selectors[0]:-/dev/null}"); then
      release_failed_attempt "$attempt_dir/quote.json" \
        'durable repair handoff identity is invalid; exact Lease was released'
    fi
  fi

  lease_artifact="cloud-lease-provision-$WK_CHAT_REQUEST_ID"
  readiness_timeout="$(jq -er .readiness_timeout_seconds "$attempt_dir/run-plan.json")"
  lease_expires_epoch="$(date -u -d "$lease_expires_at" +%s)"
  repair_finalizer_safety_seconds=7200
  repair_reserve_seconds=$(( 2880 + 3780 + readiness_timeout + reserved_stage_duration_seconds + 3600 ))
  repair_deadline_epoch=$(( lease_expires_epoch - repair_finalizer_safety_seconds - repair_reserve_seconds ))
  if (( repair_deadline_epoch > orchestration_deadline_epoch )); then
    repair_deadline_epoch="$orchestration_deadline_epoch"
  fi
  if (( repair_deadline_epoch <= $(date -u +%s) )); then
    release_failed_attempt "$attempt_dir/quote.json" \
      'Lease has insufficient time before the independent finalizer cutoff for bundle, deployment, readiness, measured execution, and release reserve'
  fi
  repair_deadline="$(date -u -d "@$repair_deadline_epoch" +%Y-%m-%dT%H:%M:%SZ)"
  attempted_control_shas_file="$attempt_dir/attempted-deployment-control-shas"
  : >"$attempted_control_shas_file"
  attempted_source_shas_file="$attempt_dir/attempted-repair-source-shas"
  printf '%s\n' "$WK_CHAT_SOURCE_SHA" >"$attempted_source_shas_file"
  deployment_generation=0

  while true; do
    run_deployment_action
    if [[ "$deployment_failed" == true ]]; then
      if [[ "$deployment_control_identity_valid" != true ]]; then
        release_failed_attempt "$attempt_dir/quote.json" \
          'Deployment Action control identity is ambiguous; exact Lease was released'
      fi
      failure_code='deployment_action_incomplete'
      last_gate='none'
      if (( ${#deployment_outcomes[@]} == 1 )); then
        failure_code="$(jq -er '.failure.code // "deployment_action_incomplete"' "${deployment_outcomes[0]}" 2>/dev/null || printf deployment_action_incomplete)"
        last_gate="$(jq -er '.failure.last_successful_gate // "none"' "${deployment_outcomes[0]}" 2>/dev/null || printf none)"
      fi
      if ! record_deployment_repair_pending "$failure_code" "$last_gate" "$repair_deadline"; then
        release_failed_attempt "$attempt_dir/quote.json" \
          'deployment repair state could not be published; exact Lease was released'
      fi
      repair_wait_status=0
      candidate_sha="$(wait_for_deployment_repair_revision "$attempted_control_shas_file" "$repair_deadline_epoch")" || repair_wait_status=$?
      case "$repair_wait_status" in
        0)
          if ! grep -Fqx "$candidate_sha" "$attempted_source_shas_file"; then
            printf '%s\n' "$candidate_sha" >>"$attempted_source_shas_file"
          fi
          rebuild_repair_bundle "$candidate_sha"
          rm -f "$WK_CHAT_OUTPUT_DIR/deployment-repair-pending.json"
          continue
          ;;
        124)
          release_failed_attempt "$attempt_dir/quote.json" \
            'deployment repair deadline expired; exact Lease was released'
          ;;
        125)
          release_failed_attempt "$attempt_dir/quote.json" \
            'aggregate conservative cost reached the operational stop; exact Lease was released'
          ;;
        130)
          release_failed_attempt "$attempt_dir/quote.json" \
            'operator stop canceled deployment repair after exact zero-inventory cleanup'
          ;;
        *)
          release_failed_attempt "$attempt_dir/quote.json" \
            'deployment repair control became unavailable; exact Lease was released'
          ;;
      esac
    fi

    mapfile -t deployment_plans < <(find "$deployment_artifact_dir" -type f -name deployment-plan.json -print)
    (( ${#deployment_plans[@]} == 1 ))
    cp "${deployment_plans[0]}" "$attempt_dir/deployment-plan.json"
    load_public="$(jq -er '.hosts[] | select(.role == "load") | .public_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_LOAD_PUBLIC_IP="$load_public"
    export WK_CLOUD_SERVICE1_IP="$(jq -er '.hosts[] | select(.role == "service-1") | .private_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_SERVICE2_IP="$(jq -er '.hosts[] | select(.role == "service-2") | .private_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_SERVICE3_IP="$(jq -er '.hosts[] | select(.role == "service-3") | .private_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_SSH_KEY="$deployment_key"
    export WK_CLOUD_SSH_CONFIG="$attempt_dir/deployment-ssh-config"
    scripts/cloud-deployment/write-ssh-config.sh
    rm -f "$attempt_dir/run-start.json"
    if ! stage_journal_cursor="$(scripts/chat-lifecycle/capture-stage-journal-cursor.sh \
      "$WK_CLOUD_SSH_CONFIG" wukong-load)"; then
      release_failed_attempt "$attempt_dir/quote.json" \
        'pre-clock journal cursor unavailable; exact Lease was released'
    fi
    stage_terminal_code=''
    stage_readiness_failure_code='stage_start_failed'
    if ! timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
      "sudo rm -f '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json' && (sudo systemctl reset-failed '$stage_service' || true) && sudo systemctl start --no-block '$stage_service'"; then
      deployment_failed=true
    else
      stage_readiness_failure_code='stage_readiness_timeout'
      deployment_failed=false
    fi
    readiness_deadline=$(( $(date -u +%s) + readiness_timeout ))
    while [[ "$deployment_failed" != true ]]; do
      stop_status=0
      check_operator_stop || stop_status=$?
      case "$stop_status" in
        0)
          timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
            "sudo systemctl kill --kill-who=main --signal=SIGTERM '$stage_service' || true" || true
          exit 130
          ;;
        1) ;;
        *)
          timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
            "sudo systemctl kill --kill-who=main --signal=SIGTERM '$stage_service' || true" || true
          operator_stop_authority_failed=true
          exit 1
          ;;
      esac
      if timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        "sudo test -f '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json' && sudo test -s '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json'"; then
        timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
          "sudo head -c 65537 -- '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json'" \
          >"$attempt_dir/run-start.json" || true
        if [[ -f "$attempt_dir/run-start.json" && "$(stat --format='%s' "$attempt_dir/run-start.json")" -le 65536 ]] && jq -e --arg stage "$stage_runtime_name" '
          .schema == "wukongim.chat_lifecycle.run_start/v1" and .stage == $stage and
          (.started_at | type == "string") and (.expected_end_at | type == "string") and
          (.run_hash | test("^sha256:[0-9a-f]{64}$")) and
          (.assignment_hash | test("^sha256:[0-9a-f]{64}$")) and .generation > 0
        ' "$attempt_dir/run-start.json" >/dev/null &&
          started_epoch="$(date -u -d "$(jq -er .started_at "$attempt_dir/run-start.json")" +%s)" &&
          expected_epoch="$(date -u -d "$(jq -er .expected_end_at "$attempt_dir/run-start.json")" +%s)" &&
          (( expected_epoch - started_epoch == stage_duration_seconds )); then
          cp "$attempt_dir/run-plan.json" "$WK_CHAT_OUTPUT_DIR/run-plan.json"
          cp "$attempt_dir/quote.json" "$WK_CHAT_OUTPUT_DIR/quote.json"
          cp "$attempt_dir/receipt.json" "$WK_CHAT_OUTPUT_DIR/receipt.json"
          cp "$attempt_dir/deployment-plan.json" "$WK_CHAT_OUTPUT_DIR/deployment-plan.json"
          cp "${deployment_outcomes[0]}" "$WK_CHAT_OUTPUT_DIR/deployment-outcome.json"
          cp "${analysis_endpoints[0]}" "$WK_CHAT_OUTPUT_DIR/analysis-endpoint.json"
          cp "${encrypted_access[0]}" "$WK_CHAT_OUTPUT_DIR/encrypted-access.json"
          cp "${encrypted_deployment_identities[0]}" "$WK_CHAT_OUTPUT_DIR/encrypted-deployment-identity.json"
          while IFS= read -r nested_identity; do
            [[ "$nested_identity" == "$WK_CHAT_OUTPUT_DIR/encrypted-deployment-identity.json" ]] || rm -f "$nested_identity"
          done < <(find "$attempt_dir" -type f -name encrypted-deployment-identity.json -print)
          cp "$attempt_dir/run-start.json" "$WK_CHAT_OUTPUT_DIR/run-start.json"
          cp "$active_selector" "$WK_CHAT_OUTPUT_DIR/release-selector.json"
          if [[ "$chat_stage" == repair ]]; then
            generation_dir="$WK_CHAT_OUTPUT_DIR/generation-$deployment_generation"
            install -d -m 0700 "$generation_dir"
            cp "$attempt_dir/run-start.json" "$generation_dir/run-start.json"
            cp "$attempt_dir/deployment-plan.json" "$generation_dir/deployment-plan.json"
            cp "${deployment_outcomes[0]}" "$generation_dir/deployment-outcome.json"
            repair_state="$generation_dir/repair-state.json"
            "$WK_CHAT_TOOL" repair-begin \
              --request-id "$WK_CHAT_REQUEST_ID" --lease-id "$lease_id" \
              --generation "$deployment_generation" --source-sha "$WK_CHAT_SOURCE_SHA" \
              --bundle-digest "$bundle_digest" \
              --started-at "$(jq -er .started_at "$attempt_dir/run-start.json")" \
              --target-online 10000 --minimum-online-percent 95 \
              --minimum-send-rate 1900 --maximum-ack-backlog 4000 \
              --warmup-timeout 5m --stall-after 15s --qualify-after 2m \
              >"$repair_state"
            chmod 0600 "$repair_state"
            repair_monitor_status=0
            WK_CHAT_REPAIR_TOOL="$WK_CHAT_TOOL" \
              WK_CHAT_REPAIR_STATE="$repair_state" \
              WK_CHAT_REPAIR_OUTPUT_DIR="$generation_dir" \
              WK_CHAT_REPAIR_SSH_CONFIG="$WK_CLOUD_SSH_CONFIG" \
              WK_CHAT_REPAIR_REQUEST_ID="$WK_CHAT_REQUEST_ID" \
              WK_CHAT_REPAIR_MAX_SECONDS="$repair_max_seconds" \
              WK_CHAT_REPAIR_POLL_SECONDS=5 \
              WK_CHAT_REPAIR_SERVICE="$stage_service" \
              scripts/chat-lifecycle/repair-monitor.sh || repair_monitor_status=$?
            case "$repair_monitor_status" in
              0)
                jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg lease "$lease_id" \
                  --arg source "$WK_CHAT_SOURCE_SHA" --arg bundle "$bundle_digest" \
                  --argjson generation "$deployment_generation" '
                  .schema == "wukongim.chat_lifecycle.repair_step/v1" and
                  .decision.action == "qualified" and .decision.generation == $generation and
                  .state.candidate.request_id == $request and .state.candidate.lease_id == $lease and
                  .state.candidate.generation == $generation and .state.candidate.source_sha == $source and
                  .state.candidate.bundle_digest == $bundle
                ' "$generation_dir/repair-decision.json" >/dev/null
                jq -n --arg schema 'wukongim.chat_lifecycle.repair_qualified/v1' \
                  --arg request_id "$WK_CHAT_REQUEST_ID" --arg lease_id "$lease_id" \
                  --arg source_sha "$WK_CHAT_SOURCE_SHA" --arg bundle_digest "$bundle_digest" \
                  --arg observed_at "$(jq -er .decision.observed_at "$generation_dir/repair-decision.json")" \
                  --argjson generation "$deployment_generation" \
                  --slurpfile decision "$generation_dir/repair-decision.json" \
                  '{schema:$schema,request_id:$request_id,lease_id:$lease_id,generation:$generation,
                    source_sha:$source_sha,bundle_digest:$bundle_digest,observed_at:$observed_at,
                    decision:$decision[0].decision,official_evidence_eligible:false}' \
                  >"$WK_CHAT_OUTPUT_DIR/repair-qualified.json"
                if ! release_current; then
                  echo 'repair qualified but exact zero-inventory release proof is unavailable' >&2
                  exit 1
                fi
                rm -f "$deployment_key" "${deployment_key}.pub"
                deployment_key=''
                exit 0
                ;;
              10)
                repair_reason="$(jq -er .decision.reason "$generation_dir/repair-decision.json")"
                if ! publish_repair_checkpoint "$generation_dir"; then
                  release_failed_attempt "$attempt_dir/quote.json" \
                    'terminal repair checkpoint publication failed; exact Lease was released'
                fi
                if ! record_deployment_repair_pending "$repair_reason" repair_monitor "$repair_deadline"; then
                  release_failed_attempt "$attempt_dir/quote.json" \
                    'repair monitor failure state could not be published; exact Lease was released'
                fi
                repair_wait_status=0
                candidate_sha="$(wait_for_deployment_repair_revision "$attempted_source_shas_file" "$repair_deadline_epoch")" || repair_wait_status=$?
                case "$repair_wait_status" in
                  0)
                    printf '%s\n' "$candidate_sha" >>"$attempted_source_shas_file"
                    rebuild_repair_bundle "$candidate_sha"
                    rm -f "$WK_CHAT_OUTPUT_DIR/deployment-repair-pending.json"
                    continue
                    ;;
                  124)
                    release_failed_attempt "$attempt_dir/quote.json" \
                      'repair short-run revision deadline expired; exact Lease was released'
                    ;;
                  125)
                    release_failed_attempt "$attempt_dir/quote.json" \
                      'repair short-run reached the operational cost stop; exact Lease was released'
                    ;;
                  130)
                    release_failed_attempt "$attempt_dir/quote.json" \
                      'operator stopped the repair short-run after exact zero-inventory cleanup'
                    ;;
                  *)
                    release_failed_attempt "$attempt_dir/quote.json" \
                      'repair revision control became unavailable; exact Lease was released'
                    ;;
                esac
                ;;
              130)
                operator_stop_requested=true
                release_failed_attempt "$attempt_dir/quote.json" \
                  'operator stopped the repair short-run after exact zero-inventory cleanup'
                ;;
              *)
                release_failed_attempt "$attempt_dir/quote.json" \
                  'repair monitor failed without a sealed diagnosis; exact Lease was released'
                ;;
            esac
          fi
          jq -n --arg schema "$stage_handoff_schema" \
            --arg request_id "$WK_CHAT_REQUEST_ID" --argjson attempt "$attempt" \
            --arg source_sha "$WK_CHAT_SOURCE_SHA" --arg bundle_digest "$bundle_digest" \
            --argjson bundle_run_id "$bundle_run_id" --argjson acquire_run_id "$acquire_run_id" \
            --argjson deployment_run_id "$deployment_run_id" --slurpfile start "$attempt_dir/run-start.json" \
            '{schema:$schema,request_id:$request_id,attempt:$attempt,source_sha:$source_sha,bundle_digest:$bundle_digest,
              bundle_run_id:$bundle_run_id,acquire_run_id:$acquire_run_id,deployment_run_id:$deployment_run_id,
              started_at:$start[0].started_at,expected_end_at:$start[0].expected_end_at}' \
            >"$WK_CHAT_OUTPUT_DIR/handoff.json"
          stop_status=0
          check_operator_stop || stop_status=$?
          case "$stop_status" in
            0)
              timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
                "sudo systemctl kill --kill-who=main --signal=SIGTERM '$stage_service' || true" || true
              rm -f "$WK_CHAT_OUTPUT_DIR/handoff.json"
              exit 130
              ;;
            1) ;;
            *)
              timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
                "sudo systemctl kill --kill-who=main --signal=SIGTERM '$stage_service' || true" || true
              rm -f "$WK_CHAT_OUTPUT_DIR/handoff.json"
              operator_stop_authority_failed=true
              exit 1
              ;;
          esac
          keep_active=true
          rm -f "$deployment_key" "${deployment_key}.pub"
          deployment_key=''
          trap - EXIT
          exit 0
        fi
      fi
      state="$(ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        "sudo systemctl is-active '$stage_service' || true" 2>/dev/null || printf unreachable)"
      if [[ "$state" == failed || "$state" == inactive ]]; then
        stage_terminal_code="$(read_pre_clock_terminal_code "$stage_journal_cursor")" || true
        deployment_failed=true
        break
      fi
      if [[ "$state" == unreachable || "$(date -u +%s)" -ge "$readiness_deadline" ]]; then
        deployment_failed=true
        break
      fi
      sleep 10
    done
    timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
      "sudo systemctl kill --kill-who=main --signal=SIGTERM '$stage_service' || true" || true
    if [[ -n "$stage_terminal_code" ]]; then
      release_failed_attempt "$attempt_dir/quote.json" \
        "stage terminated before run-start with coordinator_code=${stage_terminal_code}; exact Lease was released"
    fi
    if [[ "$deployment_control_identity_valid" != true ]]; then
      release_failed_attempt "$attempt_dir/quote.json" \
        'Deployment Action control identity is ambiguous after readiness failure; exact Lease was released'
    fi
    if ! record_deployment_repair_pending "$stage_readiness_failure_code" deployment_receipt "$repair_deadline"; then
      release_failed_attempt "$attempt_dir/quote.json" \
        'pre-clock readiness repair state could not be published; exact Lease was released'
    fi
    repair_wait_status=0
    candidate_sha="$(wait_for_deployment_repair_revision "$attempted_control_shas_file" "$repair_deadline_epoch")" || repair_wait_status=$?
    case "$repair_wait_status" in
      0)
        if ! grep -Fqx "$candidate_sha" "$attempted_source_shas_file"; then
          printf '%s\n' "$candidate_sha" >>"$attempted_source_shas_file"
        fi
        rebuild_repair_bundle "$candidate_sha"
        rm -f "$WK_CHAT_OUTPUT_DIR/deployment-repair-pending.json"
        continue
        ;;
      124)
        release_failed_attempt "$attempt_dir/quote.json" \
          'pre-clock readiness repair deadline expired; exact Lease was released'
        ;;
      125)
        release_failed_attempt "$attempt_dir/quote.json" \
          'aggregate conservative cost reached the operational stop; exact Lease was released'
        ;;
      130)
        release_failed_attempt "$attempt_dir/quote.json" \
          'operator stop canceled pre-clock readiness repair after exact zero-inventory cleanup'
        ;;
      *)
        release_failed_attempt "$attempt_dir/quote.json" \
          'pre-clock readiness repair control became unavailable; exact Lease was released'
        ;;
    esac
  done
done

exit 1
