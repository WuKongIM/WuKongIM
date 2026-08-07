#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${WK_CHAT_SOURCE_SHA:?required}"
: "${WK_CHAT_OPERATOR:?required}"
: "${WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY:?required}"
: "${WK_CHAT_REQUEST_ID:?required}"
: "${WK_CHAT_DEPLOYMENT_PUBKEY:?required}"
: "${WK_CHAT_DEPLOYMENT_KEY:?required}"
: "${WK_CHAT_TOOL:?required}"
: "${WK_CHAT_TEMPLATE:?required}"
: "${WK_CHAT_OUTPUT_DIR:?required}"
: "${WK_CHAT_WORK_DIR:?required}"

[[ "$WK_CHAT_SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$WK_CHAT_OPERATOR" == tangtaoit ]]
[[ "$WK_CHAT_REQUEST_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
[[ -x "$WK_CHAT_TOOL" && -f "$WK_CHAT_TEMPLATE" && -f "$WK_CHAT_DEPLOYMENT_KEY" ]]

chat_stage="${WK_CHAT_STAGE:-rehearsal}"
case "$chat_stage" in
  rehearsal)
    stage_service=wkbench-rehearsal.service
    stage_report_dir=rehearsal
    stage_duration_seconds=7200
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
    stage_duration_seconds=259200
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
DISPATCH_RUN_ID=''
orchestration_started_epoch="$(date -u +%s)"
orchestration_deadline_epoch=$(( orchestration_started_epoch + 20700 ))

workflow_run_max_id() {
  local workflow="$1"
  gh run list --repo "$GITHUB_REPOSITORY" --workflow "$workflow" --event workflow_dispatch --branch main \
    --limit 50 --json databaseId --jq 'map(.databaseId) | max // 0'
}

dispatch_and_wait() {
  local workflow="$1"
  local title="$2"
  shift 2
  local before deadline rows count watch_status=0
  DISPATCH_RUN_ID=''
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
  local watch_timeout_seconds
  case "$workflow" in
    cloud-deployment-bundle.yml|cloud-lease-provision.yml) watch_timeout_seconds=2880 ;;
    cloud-deployment-activate.yml) watch_timeout_seconds=3780 ;;
    cloud-lease-release.yml) watch_timeout_seconds=2580 ;;
    *) return 1 ;;
  esac
  timeout --signal=TERM "$watch_timeout_seconds" \
    gh run watch "$DISPATCH_RUN_ID" --repo "$GITHUB_REPOSITORY" --exit-status || watch_status=$?
  if [[ "$watch_status" == 124 ]]; then
    gh run cancel "$DISPATCH_RUN_ID" --repo "$GITHUB_REPOSITORY" || true
  fi
  return "$watch_status"
}

download_run() {
  local run_id="$1"
  local destination="$2"
  [[ ! -e "$destination" ]] || return 1
  install -d -m 0700 "$destination" || return
  gh run download "$run_id" --repo "$GITHUB_REPOSITORY" --dir "$destination"
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
  exit "$status"
}
trap cleanup_on_exit EXIT
trap 'exit 130' INT TERM

if [[ "$chat_stage" == rehearsal ]]; then
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
excluded_zone=''
excluded_compute_type=''

complete_failed_attempt() {
  local quote_file="$1"
  local retry_allowed="$2"
  excluded_zone="$(jq -er .quote.zone "$quote_file")"
  excluded_compute_type="$(jq -er .quote.selection.instance_type "$quote_file")"
  release_current
  local attempt_dir created_at ended_at actual_cost
  attempt_dir="$(dirname "$quote_file")"
  created_at="$(jq -er '.receipt.created_at // empty' "$attempt_dir/receipt.json" 2>/dev/null || jq -er .quote.quoted_at "$quote_file")"
  ended_at="$(jq -er .result.zero_inventory.observed_at "$WK_CHAT_OUTPUT_DIR/zero-inventory.json")"
  actual_cost="$(scripts/chat-lifecycle/accrued-cost.sh "$attempt_dir/run-plan.json" "$quote_file" "$created_at" "$ended_at" -1)"
  committed_micros=$(( committed_micros + actual_cost ))
  if [[ "$retry_allowed" != true ]]; then
    echo 'acquisition failure is terminal after zero-inventory cleanup' >&2
    exit 1
  fi
  if [[ "$attempt" == 2 ]]; then
    echo 'second acquisition/deployment/readiness attempt failed after zero-inventory cleanup' >&2
    exit 1
  fi
  retry_safety_seconds=17100
  if (( $(date -u +%s) + retry_safety_seconds > orchestration_deadline_epoch )); then
    echo 'deployment retry skipped because the bounded orchestration safety window is exhausted' >&2
    exit 1
  fi
  rm -f "$WK_CHAT_OUTPUT_DIR/cleanup.json" "$WK_CHAT_OUTPUT_DIR/zero-inventory.json" \
    "$WK_CHAT_OUTPUT_DIR/release-selector.json"
  cleanup_attempted=false
}

for attempt in 1 2; do
  attempt_dir="$WK_CHAT_OUTPUT_DIR/attempt-$attempt"
  install -d -m 0700 "$attempt_dir"
  materialize_args=(
    materialize --template "$WK_CHAT_TEMPLATE" --source-sha "$WK_CHAT_SOURCE_SHA"
    --operator "$WK_CHAT_OPERATOR" --codex-diagnostic-pubkey "$WK_CHAT_CODEX_DIAGNOSTIC_PUBKEY"
    --request-id "$WK_CHAT_REQUEST_ID" --repository "$GITHUB_REPOSITORY"
    --bundle-digest "$bundle_digest" --deployment-pubkey "$WK_CHAT_DEPLOYMENT_PUBKEY"
    --now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --attempt "$attempt"
    --committed-micros "$committed_micros"
  )
  if [[ "$attempt" == 2 ]]; then
    materialize_args+=(--excluded-zone "$excluded_zone" --excluded-compute-type "$excluded_compute_type")
  fi
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

  acquire_failed=false
  if ! dispatch_and_wait cloud-lease-provision.yml "$provision_title" \
    -f request_id="$WK_CHAT_REQUEST_ID" -f plan_json="$(cat "$attempt_dir/lease-plan.json")" \
    -f bootstrap_access_json="$(cat "$attempt_dir/bootstrap-access.json")" -f quote_only=false \
    -f admitted_quote_json="$(cat "$attempt_dir/preflight-quote.json")" \
    -f paid_authorization=create-paid-cloud-lease; then
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
    if jq -e --arg request "$WK_CHAT_REQUEST_ID" --arg source "$WK_CHAT_SOURCE_SHA" --arg bundle "$bundle_digest" '
      .schema == "wukongim.cloud_lease.receipt/v1" and .receipt.request_id == $request and
      .receipt.state == "active" and .receipt.provenance.source_sha == $source and
      .receipt.provenance.bundle_digest == $bundle
    ' "$attempt_dir/receipt.json" >/dev/null &&
      "$WK_CHAT_TOOL" selector --receipt "$attempt_dir/receipt.json" >"$attempt_dir/receipt-selector.json" &&
      cmp -s <(jq -S -c .selector "$attempt_dir/receipt-selector.json") \
        <(jq -S -c .selector "$attempt_dir/release-selector.json") &&
      cmp -s <(jq -S -c .receipt.quote "$attempt_dir/receipt.json") \
        <(jq -S -c .quote "$attempt_dir/quote.json"); then
      receipt_active=true
    fi
  fi
  if [[ "$acquire_failed" == true || "$receipt_active" != true ]]; then
    complete_failed_attempt "$attempt_dir/preflight-quote.json" false
  fi

  lease_artifact="cloud-lease-provision-$WK_CHAT_REQUEST_ID"
  deployment_title="Cloud Deployment $lease_artifact"
  deployment_failed=false
  if ! dispatch_and_wait cloud-deployment-activate.yml "$deployment_title" \
    -f lease_artifact_run_id="$acquire_run_id" -f lease_artifact_name="$lease_artifact" \
    -f bundle_artifact_run_id="$bundle_run_id" -f bundle_artifact_name="$bundle_artifact"; then
    deployment_failed=true
  fi
  deployment_run_id="$DISPATCH_RUN_ID"
  download_run "$deployment_run_id" "$attempt_dir/deployment-artifact" || true
  mapfile -t deployment_outcomes < <(find "$attempt_dir/deployment-artifact" -type f -name deployment-outcome.json -print)
  if (( ${#deployment_outcomes[@]} != 1 )) || ! jq -e '.passed == true and .receipt.schema == "wukongim.cloud_deployment.receipt/v1"' "${deployment_outcomes[0]:-/dev/null}" >/dev/null; then
    deployment_failed=true
  fi

  if [[ "$deployment_failed" != true ]]; then
    mapfile -t deployment_plans < <(find "$attempt_dir/deployment-artifact" -type f -name deployment-plan.json -print)
    (( ${#deployment_plans[@]} == 1 ))
    cp "${deployment_plans[0]}" "$attempt_dir/deployment-plan.json"
    load_public="$(jq -er '.hosts[] | select(.role == "load") | .public_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_LOAD_PUBLIC_IP="$load_public"
    export WK_CLOUD_SERVICE1_IP="$(jq -er '.hosts[] | select(.role == "service-1") | .private_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_SERVICE2_IP="$(jq -er '.hosts[] | select(.role == "service-2") | .private_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_SERVICE3_IP="$(jq -er '.hosts[] | select(.role == "service-3") | .private_address' "$attempt_dir/deployment-plan.json")"
    export WK_CLOUD_SSH_KEY="$WK_CHAT_DEPLOYMENT_KEY"
    export WK_CLOUD_SSH_CONFIG="$attempt_dir/deployment-ssh-config"
    scripts/cloud-deployment/write-ssh-config.sh
    ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load "sudo systemctl start --no-block '$stage_service'"
    readiness_timeout="$(jq -er .readiness_timeout_seconds "$attempt_dir/run-plan.json")"
    readiness_deadline=$(( $(date -u +%s) + readiness_timeout ))
    while true; do
      if timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
        "sudo test -f '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json' && sudo test -s '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json'"; then
        timeout 60 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
          "sudo head -c 65537 -- '/var/lib/wukongim-cloud/reports/$stage_report_dir/run-start.json'" \
          >"$attempt_dir/run-start.json" || true
        if [[ -f "$attempt_dir/run-start.json" && "$(stat --format='%s' "$attempt_dir/run-start.json")" -le 65536 ]] && jq -e --arg stage "$chat_stage" '
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
          cp "$attempt_dir/run-start.json" "$WK_CHAT_OUTPUT_DIR/run-start.json"
          cp "$active_selector" "$WK_CHAT_OUTPUT_DIR/release-selector.json"
          jq -n --arg schema "$stage_handoff_schema" \
            --arg request_id "$WK_CHAT_REQUEST_ID" --argjson attempt "$attempt" \
            --arg source_sha "$WK_CHAT_SOURCE_SHA" --arg bundle_digest "$bundle_digest" \
            --argjson bundle_run_id "$bundle_run_id" --argjson acquire_run_id "$acquire_run_id" \
            --argjson deployment_run_id "$deployment_run_id" --slurpfile start "$attempt_dir/run-start.json" \
            '{schema:$schema,request_id:$request_id,attempt:$attempt,source_sha:$source_sha,bundle_digest:$bundle_digest,
              bundle_run_id:$bundle_run_id,acquire_run_id:$acquire_run_id,deployment_run_id:$deployment_run_id,
              started_at:$start[0].started_at,expected_end_at:$start[0].expected_end_at}' \
            >"$WK_CHAT_OUTPUT_DIR/handoff.json"
          keep_active=true
          trap - EXIT
          exit 0
        fi
      fi
      state="$(ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load "sudo systemctl is-active '$stage_service' || true")"
      if [[ "$state" == failed || "$state" == inactive || "$(date -u +%s)" -ge "$readiness_deadline" ]]; then
        deployment_failed=true
        break
      fi
      sleep 10
    done
  fi

  complete_failed_attempt "$attempt_dir/quote.json" true
done

exit 1
