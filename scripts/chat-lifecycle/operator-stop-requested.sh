#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )); then
  echo 'usage: operator-stop-requested.sh REQUEST_ID' >&2
  exit 2
fi

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

request_id="$1"
[[ "$request_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
artifact_name="chat-lifecycle-operator-stop-${request_id}"

temporary="$(mktemp -d)"
trap 'rm -r "$temporary"' EXIT
: >"$temporary/pages.json"
max_pages=50
inventory_complete=false
for ((page = 1; page <= max_pages; page++)); do
  page_file="$temporary/page-${page}.json"
  gh api --method GET "/repos/${GITHUB_REPOSITORY}/actions/artifacts" \
    -f name="$artifact_name" -f per_page=100 -f page="$page" >"$page_file"
  jq -e '.artifacts | type == "array"' "$page_file" >/dev/null
  jq -c . "$page_file" >>"$temporary/pages.json"
  if [[ "$(jq -r '.artifacts | length' "$page_file")" != 100 ]]; then
    inventory_complete=true
    break
  fi
done
[[ "$inventory_complete" == true ]] || {
  echo "operator-stop discovery exceeded the bounded ${max_pages}-page exact-name Artifact inventory" >&2
  exit 2
}

jq -cs --arg name "$artifact_name" '
  [.[].artifacts[] |
    select(.expired == false and .name == $name and (.id | type == "number") and
      (.workflow_run.id | type == "number") and (.created_at | type == "string"))]
  | sort_by(.created_at) | reverse | .[]
' "$temporary/pages.json" >"$temporary/candidates.jsonl"

while IFS= read -r artifact; do
  run_id="$(jq -er .workflow_run.id <<<"$artifact")"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}" >"$temporary/run-${run_id}.json"
  if scripts/chat-lifecycle/authenticate-operator-stop-producer.sh "$temporary/run-${run_id}.json"; then
    jq -cn --arg schema 'wukongim.chat_lifecycle.operator_stop_observation/v1' \
      --arg request_id "$request_id" --argjson run_id "$run_id" \
      --argjson artifact_id "$(jq -er .id <<<"$artifact")" \
      --arg observed_at "$(jq -er .created_at <<<"$artifact")" \
      '{schema:$schema,request_id:$request_id,run_id:$run_id,artifact_id:$artifact_id,observed_at:$observed_at}'
    exit 0
  fi
done <"$temporary/candidates.jsonl"

exit 1
