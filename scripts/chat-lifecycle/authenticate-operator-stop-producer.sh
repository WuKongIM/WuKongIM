#!/usr/bin/env bash
set -euo pipefail

if (( $# != 1 )); then
  echo 'usage: authenticate-operator-stop-producer.sh RUN_JSON' >&2
  exit 2
fi

: "${GITHUB_REPOSITORY:?required}"

run_json="$1"
[[ -f "$run_json" ]]

jq -e --arg repository "$GITHUB_REPOSITORY" '
  .repository.full_name == $repository and .head_repository.full_name == $repository and
  .event == "workflow_dispatch" and .head_branch == "main" and
  (.status == "in_progress" or
    (.status == "completed" and (.conclusion | type == "string"))) and
  (.path == ".github/workflows/chat-lifecycle-stop.yml" or
   .path == ".github/workflows/chat-lifecycle-stop.yml@refs/heads/main")
' "$run_json" >/dev/null
