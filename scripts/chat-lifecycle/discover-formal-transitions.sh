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
for page in 1 2 3 4 5; do
  page_file="$temporary/page-${page}.json"
  gh api "/repos/${GITHUB_REPOSITORY}/actions/artifacts?per_page=100&page=${page}" >"$page_file"
  jq -c . "$page_file" >>"$temporary/pages.json"
  [[ "$(jq -r '.artifacts | length' "$page_file")" == 100 ]] || break
done
jq -s '[.[].artifacts[] | select(.expired == false)]' "$temporary/pages.json" >"$temporary/artifacts.json"
jq -c --arg requested "$requested" -f scripts/chat-lifecycle/select-formal-start-matrix.jq \
  "$temporary/artifacts.json" >"$temporary/candidates.json"

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
