#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

requested="${1-}"
output="${2:?output required}"
[[ -z "$requested" || "$requested" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
[[ ! -e "$output" ]]

temporary="$(mktemp -d)"
trap 'rm -r "$temporary"' EXIT
: >"$temporary/pages.jsonl"
inventory_complete=false
for ((page = 1; page <= 50; page++)); do
  page_file="$temporary/page-${page}.json"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/artifacts?per_page=100&page=${page}" >"$page_file"
  jq -c . "$page_file" >>"$temporary/pages.jsonl"
  if [[ "$(jq -r '.artifacts | length' "$page_file")" != 100 ]]; then
    inventory_complete=true
    break
  fi
done
[[ "$inventory_complete" == true ]]
jq -s '[.[].artifacts[] | select(.expired == false)]' "$temporary/pages.jsonl" >"$temporary/artifacts.json"
jq -c --arg handoff_prefix 'chat-lifecycle-repair-handoff-' \
  --arg acquire_prefix 'cloud-lease-provision-' \
  --arg cleanup_prefix 'chat-lifecycle-repair-cleanup-' \
  --arg requested "$requested" '
  def latest: if length == 0 then null else max_by(.created_at) end;
  . as $artifacts |
  ([ $artifacts[] | select(.name | startswith($handoff_prefix)) |
     . + {request_id:(.name | ltrimstr($handoff_prefix)),handoff_kind:"handoff"} |
     select(($requested == "" or .request_id == $requested) and
            (.request_id | test("^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$"))) ] |
     group_by(.request_id) | map(latest)) as $handoffs |
  ([ $artifacts[] | select(.name | startswith($acquire_prefix)) |
     . + {request_id:(.name | ltrimstr($acquire_prefix)),handoff_kind:"acquire"} |
     select(($requested == "" or .request_id == $requested) and
            (.request_id | test("^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$"))) ] |
     map(select(.request_id as $request | any($handoffs[]; .request_id == $request) | not)) |
     sort_by(.created_at) | reverse) as $acquires |
  (($handoffs + $acquires) | sort_by(.created_at) | reverse |
   map(. as $owner |
     ([ $artifacts[] | select(.name == ($cleanup_prefix + $owner.request_id) and
                                .created_at >= $owner.created_at) ] | latest) as $cleanup |
     {request_id:$owner.request_id,handoff_kind:$owner.handoff_kind,
      handoff_run_id:$owner.workflow_run.id,handoff_artifact_id:$owner.id,
      artifact_name:$owner.name,owner_created_at:$owner.created_at,
      cleanup_run_id:($cleanup.workflow_run.id // 0),
      cleanup_artifact_id:($cleanup.id // 0)})) |
  {include:.}
' "$temporary/artifacts.json" >"$temporary/candidates.json"

GOWORK=off go build -trimpath -o "$temporary/wkchatlifecycle" ./cmd/wkchatlifecycle

: >"$temporary/active.jsonl"
: >"$temporary/selected-acquire-requests"
while IFS= read -r row; do
  request_id="$(jq -er .request_id <<<"$row")"
  handoff_run_id="$(jq -er .handoff_run_id <<<"$row")"
  handoff_artifact_id="$(jq -er .handoff_artifact_id <<<"$row")"
  handoff_kind="$(jq -er .handoff_kind <<<"$row")"
  cleanup_run_id="$(jq -er .cleanup_run_id <<<"$row")"
  cleanup_artifact_id="$(jq -er .cleanup_artifact_id <<<"$row")"
  if [[ "$handoff_kind" == acquire ]] && grep -Fqx "$request_id" "$temporary/selected-acquire-requests"; then
    continue
  fi
  auth_file="$temporary/handoff-auth-${request_id}-${handoff_run_id}-${handoff_artifact_id}.json"
  if ! scripts/chat-lifecycle/authenticate-repair-artifact-producer.sh "$handoff_kind" \
    "$handoff_run_id" "$auth_file" "$request_id"; then
    exit 1
  fi
  handoff_dir="$temporary/handoff-${request_id}-${handoff_artifact_id}"
  install -d -m 0700 "$handoff_dir"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/artifacts/${handoff_artifact_id}/zip" >"$temporary/handoff-${request_id}.zip"
  unzip -q "$temporary/handoff-${request_id}.zip" -d "$handoff_dir"
  case "$handoff_kind" in
    handoff)
      if [[ ! -s "$handoff_dir/repair-handoff.json" || ! -s "$handoff_dir/release-selector.json" ]] ||
        ! jq -e --arg request "$request_id" --slurpfile selector "$handoff_dir/release-selector.json" '
        .schema == "wukongim.chat_lifecycle.repair_handoff/v1" and .request_id == $request and
        .selector == $selector[0].selector
      ' "$handoff_dir/repair-handoff.json" >/dev/null; then
        exit 1
      fi
      ;;
    acquire)
      [[ -s "$handoff_dir/receipt.json" ]] || continue
      if ! jq -e --arg request "$request_id" --arg repository "$GITHUB_REPOSITORY" '
        .schema == "wukongim.cloud_lease.receipt/v1" and .receipt.request_id == $request and
        .receipt.repository == $repository and .receipt.state == "active" and
        .receipt.tags.stage == "repair" and (.receipt.lease_id | length > 0) and
        (.receipt.provenance.source_sha | test("^[0-9a-f]{40}$")) and
        (.receipt.provenance.bundle_digest | test("^sha256:[0-9a-f]{64}$"))
      ' "$handoff_dir/receipt.json" >/dev/null; then
        continue
      fi
      if [[ ! -s "$handoff_dir/repair-owner.json" ]] || ! jq -e --arg request "$request_id" '
        .schema == "wukongim.chat_lifecycle.repair_acquire_owner/v1" and
        .request_id == $request and (.parent_run_id | type == "number") and .parent_run_id > 0
      ' "$handoff_dir/repair-owner.json" >/dev/null; then
        exit 1
      fi
      "$temporary/wkchatlifecycle" selector --receipt "$handoff_dir/receipt.json" \
        >"$handoff_dir/release-selector.json"
      printf '%s\n' "$request_id" >>"$temporary/selected-acquire-requests"
      ;;
    *) exit 2 ;;
  esac
  if [[ "$cleanup_run_id" != 0 ]] && scripts/chat-lifecycle/authenticate-repair-artifact-producer.sh cleanup \
    "$cleanup_run_id" "$temporary/cleanup-auth-${request_id}.json" "$request_id"; then
    cleanup_dir="$temporary/cleanup-${request_id}-${cleanup_artifact_id}"
    install -d -m 0700 "$cleanup_dir"
    if gh api "/repos/${GITHUB_REPOSITORY}/actions/artifacts/${cleanup_artifact_id}/zip" >"$temporary/cleanup-${request_id}.zip" &&
      unzip -q "$temporary/cleanup-${request_id}.zip" -d "$cleanup_dir" &&
      [[ -s "$cleanup_dir/cleanup.json" && -s "$cleanup_dir/zero-inventory.json" ]] &&
      jq -e --arg request "$request_id" '
        .schema == "wukongim.chat_lifecycle.repair_cleanup/v1" and
        .request_id == $request and .zero_inventory == true
      ' "$cleanup_dir/cleanup.json" >/dev/null &&
      jq -e --slurpfile selector "$handoff_dir/release-selector.json" '
        .schema == "wukongim.cloud_lease.release/v1" and
        .result.zero_inventory.selector == $selector[0].selector and
        (.result.zero_inventory.account_id_hash | test("^sha256:[0-9a-f]{64}$")) and
        (.result.zero_inventory.observed_at | type == "string") and
        (.result.zero_inventory.scopes | type == "array" and length > 0)
      ' "$cleanup_dir/zero-inventory.json" >/dev/null; then
      continue
    fi
  fi
  printf '%s\n' "$row" >>"$temporary/active.jsonl"
done < <(jq -c '.include[]' "$temporary/candidates.json")

jq -sc '{include:.[0:20]}' "$temporary/active.jsonl" >"$temporary/matrix.json"
install -m 0600 "$temporary/matrix.json" "$output"
