#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

requested="${1-}"
output="${2:?output required}"
if [[ -n "$requested" ]]; then
  [[ "$requested" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
fi
[[ ! -e "$output" ]]

temporary="$(mktemp -d)"
trap 'rm -r "$temporary"' EXIT
: >"$temporary/pages.json"
max_pages=50
inventory_complete=false
for ((page = 1; page <= max_pages; page++)); do
  page_file="$temporary/page-${page}.json"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/artifacts?per_page=100&page=${page}" >"$page_file"
  jq -c . "$page_file" >>"$temporary/pages.json"
  if [[ "$(jq -r '.artifacts | length' "$page_file")" != 100 ]]; then
    inventory_complete=true
    break
  fi
done
[[ "$inventory_complete" == true ]] || {
  echo "formal transition discovery exceeded the bounded ${max_pages}-page repository Artifact inventory" >&2
  exit 1
}
jq -s '[.[].artifacts[] | select(.expired == false)]' "$temporary/pages.json" >"$temporary/artifacts.json"
: >"$temporary/authenticated-stops.jsonl"
while IFS= read -r stop; do
  stop_run_id="$(jq -er .workflow_run.id <<<"$stop")"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${stop_run_id}" >"$temporary/stop-run-${stop_run_id}.json"
  if scripts/chat-lifecycle/authenticate-operator-stop-producer.sh "$temporary/stop-run-${stop_run_id}.json"; then
    printf '%s\n' "$stop" >>"$temporary/authenticated-stops.jsonl"
  fi
done < <(jq -c '.[] | select(.name | startswith("chat-lifecycle-operator-stop-"))' "$temporary/artifacts.json")
jq -s --slurpfile artifacts "$temporary/artifacts.json" '
  ($artifacts[0] | map(select(.name | startswith("chat-lifecycle-operator-stop-") | not))) + .
' "$temporary/authenticated-stops.jsonl" >"$temporary/authenticated-artifacts.json"
jq -c --arg requested "$requested" -f scripts/chat-lifecycle/select-formal-start-matrix.jq \
  "$temporary/authenticated-artifacts.json" >"$temporary/candidates.json"

: >"$temporary/rows.jsonl"
while IFS= read -r row; do
  run_id="$(jq -er .transition_run_id <<<"$row")"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}" >"$temporary/run-${run_id}.json"
  if jq -e --arg repository "$GITHUB_REPOSITORY" '
    .repository.full_name == $repository and .head_repository.full_name == $repository and
    (.event == "schedule" or .event == "workflow_dispatch") and .head_branch == "main" and
    .status == "completed" and (.conclusion == "success" or .conclusion == "failure") and
    (.path == ".github/workflows/chat-lifecycle-rehearsal-finalize.yml" or
     .path == ".github/workflows/chat-lifecycle-rehearsal-finalize.yml@refs/heads/main")
  ' "$temporary/run-${run_id}.json" >/dev/null; then
    printf '%s\n' "$row" >>"$temporary/rows.jsonl"
  fi
done < <(jq -c '.include[]' "$temporary/candidates.json")

jq -sc '{include:.}' "$temporary/rows.jsonl" >"$temporary/matrix.json"
install -m 0600 "$temporary/matrix.json" "$output"
