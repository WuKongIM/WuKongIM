#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

run_id="${1:?run_id required}"
output="${2:?output required}"
[[ "$run_id" =~ ^[1-9][0-9]*$ ]]
[[ ! -e "$output" ]]
install -d -m 0700 "$(dirname "$output")" || exit 2
gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}" >"$output" || exit 2
jq -e --arg repository "$GITHUB_REPOSITORY" '
  .repository.full_name == $repository and .head_repository.full_name == $repository and
  .event == "workflow_dispatch" and .head_branch == "main" and
  .status == "completed" and (.conclusion == "success" or .conclusion == "failure") and
  (.path == ".github/workflows/chat-lifecycle-rehearsal.yml" or
   .path == ".github/workflows/chat-lifecycle-rehearsal.yml@refs/heads/main")
' "$output" >/dev/null
