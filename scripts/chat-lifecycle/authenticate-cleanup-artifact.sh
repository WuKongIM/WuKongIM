#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${WK_CHAT_STAGE:=rehearsal}"

[[ "$WK_CHAT_STAGE" == rehearsal || "$WK_CHAT_STAGE" == formal ]]

request_id="${1:?request_id required}"
run_id="${2:?run_id required}"
handoff_run_id="${3:?handoff_run_id required}"
destination="${4:?destination required}"
[[ "$request_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
[[ "$run_id" =~ ^[1-9][0-9]*$ ]]
[[ "$handoff_run_id" =~ ^[1-9][0-9]*$ ]]
[[ ! -e "$destination" ]]
install -d -m 0700 "$destination"

gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}" >"$destination/producer-run.json"
jq -e --arg repository "$GITHUB_REPOSITORY" --arg stage "$WK_CHAT_STAGE" '
  .repository.full_name == $repository and .head_repository.full_name == $repository and
  (.event == "schedule" or .event == "workflow_dispatch") and .head_branch == "main" and
  .status == "completed" and (.conclusion == "success" or .conclusion == "failure") and
  (.path == (".github/workflows/chat-lifecycle-" + $stage + "-finalize.yml") or
   .path == (".github/workflows/chat-lifecycle-" + $stage + "-finalize.yml@refs/heads/main") or
   .path == (".github/workflows/chat-lifecycle-" + $stage + ".yml") or
   .path == (".github/workflows/chat-lifecycle-" + $stage + ".yml@refs/heads/main"))
' "$destination/producer-run.json" >/dev/null

artifact_name="chat-lifecycle-${WK_CHAT_STAGE}-cleanup-${request_id}"
gh run download "$run_id" --repo "$GITHUB_REPOSITORY" --name "$artifact_name" --dir "$destination/cleanup"
gh run download "$handoff_run_id" --repo "$GITHUB_REPOSITORY" \
  --name "chat-lifecycle-${WK_CHAT_STAGE}-handoff-${request_id}" --dir "$destination/handoff"
jq -e --arg request_id "$request_id" --arg stage "$WK_CHAT_STAGE" '
  .schema == ("wukongim.chat_lifecycle." + $stage + "_cleanup/v1") and
  .request_id == $request_id and (.release_run_id | type == "number") and
  .release_run_id > 0 and .zero_inventory == true
' "$destination/cleanup/cleanup.json" >/dev/null
jq -e '
  .schema == "wukongim.cloud_lease.release/v1" and
  (.result.zero_inventory | type == "object") and
  (.result.zero_inventory.account_id_hash | test("^sha256:[0-9a-f]{64}$")) and
  (.result.zero_inventory.observed_at | type == "string") and
  (.result.zero_inventory.scopes | type == "array" and length > 0)
' "$destination/cleanup/zero-inventory.json" >/dev/null
jq -e --arg request_id "$request_id" '
  .schema == "wukongim.cloud_lease.selector/v1" and .selector.request_id == $request_id
' "$destination/handoff/release-selector.json" >/dev/null
cmp -s \
  <(jq -S -c .selector "$destination/handoff/release-selector.json") \
  <(jq -S -c .result.zero_inventory.selector "$destination/cleanup/zero-inventory.json")
