#!/usr/bin/env bash
set -euo pipefail

if (($# != 2)); then
  echo "usage: validate-upstream-run.sh RUN_JSON WORKFLOW_PATH" >&2
  exit 2
fi

: "${WK_GITHUB_REPOSITORY:?required}"

run_json="$1"
workflow_path="$2"
[[ "$WK_GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]
[[ "$workflow_path" =~ ^\.github/workflows/[A-Za-z0-9._-]+\.yml$ ]]
[[ -f "$run_json" && ! -L "$run_json" ]]
if size="$(stat -c %s "$run_json" 2>/dev/null)"; then
  :
else
  size="$(stat -f %z "$run_json")"
fi
[[ "$size" =~ ^[0-9]+$ ]] && ((size > 0 && size <= 1048576))

jq -er --arg repository "$WK_GITHUB_REPOSITORY" --arg workflow "$workflow_path" '
  select(
    .repository.full_name == $repository and
    .head_repository.full_name == $repository and
    .event == "workflow_dispatch" and
    .head_branch == "main" and
    .status == "completed" and
    .conclusion == "success" and
    (.path == $workflow or .path == ($workflow + "@refs/heads/main")) and
    (.head_sha | test("^[0-9a-f]{40}$"))
  ) |
  .head_sha
' "$run_json"
