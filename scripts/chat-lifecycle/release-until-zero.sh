#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${WK_CHAT_REQUEST_ID:?required}"
: "${WK_CHAT_SELECTOR:?required}"
: "${WK_CHAT_CLEANUP_DIR:?required}"

[[ -f "$WK_CHAT_SELECTOR" ]]
install -d -m 0700 "$WK_CHAT_CLEANUP_DIR"
chat_stage="${WK_CHAT_STAGE:-rehearsal}"
[[ "$chat_stage" == rehearsal || "$chat_stage" == formal ]]

max_run_id() {
  gh run list --repo "$GITHUB_REPOSITORY" --workflow cloud-lease-release.yml --event workflow_dispatch --branch main \
    --limit 50 --json databaseId --jq 'map(.databaseId) | max // 0'
}

for attempt in 1; do
  before="$(max_run_id)"
  gh workflow run cloud-lease-release.yml --repo "$GITHUB_REPOSITORY" --ref main \
    -f request_id="$WK_CHAT_REQUEST_ID" -f operation=release \
    -f selector_json="$(jq -c . "$WK_CHAT_SELECTOR")" -f release_authorization=release-tagged-cloud-lease
  deadline=$(( $(date -u +%s) + 180 ))
  run_id=''
  while [[ -z "$run_id" ]]; do
    rows="$(gh run list --repo "$GITHUB_REPOSITORY" --workflow cloud-lease-release.yml --event workflow_dispatch --branch main \
      --limit 50 --json databaseId,displayTitle,headSha)"
    count="$(jq -r --argjson before "$before" --arg title "Cloud Lease Release $WK_CHAT_REQUEST_ID" \
      '[.[] | select(.databaseId > $before and .displayTitle == $title)] | length' <<<"$rows")"
    if [[ "$count" == 1 ]]; then
      run_id="$(jq -r --argjson before "$before" --arg title "Cloud Lease Release $WK_CHAT_REQUEST_ID" \
        '.[] | select(.databaseId > $before and .displayTitle == $title) | .databaseId' <<<"$rows")"
      break
    fi
    [[ "$count" == 0 && "$(date -u +%s)" -lt "$deadline" ]]
    sleep 2
  done
  gh run watch "$run_id" --repo "$GITHUB_REPOSITORY" --exit-status || true
  attempt_dir="$WK_CHAT_CLEANUP_DIR/attempt-$attempt"
  install -d -m 0700 "$attempt_dir"
  gh run download "$run_id" --repo "$GITHUB_REPOSITORY" --dir "$attempt_dir" || true
  mapfile -t release_files < <(find "$attempt_dir" -type f -name release.json -print)
  if (( ${#release_files[@]} == 1 )) && jq -e --slurpfile expected "$WK_CHAT_SELECTOR" '
    .schema == "wukongim.cloud_lease.release/v1" and
    (.result.zero_inventory | type == "object") and
    .result.zero_inventory.selector == $expected[0].selector and
    (.result.zero_inventory.account_id_hash | test("^sha256:[0-9a-f]{64}$")) and
    (.result.zero_inventory.observed_at | type == "string") and
    (.result.zero_inventory.scopes | type == "array" and length > 0)
  ' "${release_files[0]}" >/dev/null; then
    cp "${release_files[0]}" "$WK_CHAT_CLEANUP_DIR/zero-inventory.json"
    jq -n --arg schema "wukongim.chat_lifecycle.${chat_stage}_cleanup/v1" \
      --arg request_id "$WK_CHAT_REQUEST_ID" --argjson release_run_id "$run_id" \
      --slurpfile release "${release_files[0]}" \
      '{schema:$schema,request_id:$request_id,release_run_id:$release_run_id,
        zero_inventory:($release[0].result.zero_inventory != null)}' \
      >"$WK_CHAT_CLEANUP_DIR/cleanup.json"
    exit 0
  fi
done

cp "$WK_CHAT_SELECTOR" "$WK_CHAT_CLEANUP_DIR/release-selector.json"
jq -n --arg schema 'wukongim.chat_lifecycle.cleanup_pending/v1' \
  --arg request_id "$WK_CHAT_REQUEST_ID" --arg observed_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
  '{schema:$schema,request_id:$request_id,observed_at:$observed_at}' \
  >"$WK_CHAT_CLEANUP_DIR/cleanup-pending.json"
echo 'release did not prove zero inventory in this bounded pass; the scheduled sweeper remains active' >&2
exit 1
