#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

mode="${1:?handoff or cleanup required}"
run_id="${2:?run id required}"
output="${3:?output required}"
request_id="${4-}"
[[ "$run_id" =~ ^[1-9][0-9]*$ && ! -e "$output" ]]
install -d -m 0700 "$(dirname "$output")"
gh api "/repos/${GITHUB_REPOSITORY}/actions/runs/${run_id}" >"$output"

case "$mode" in
  handoff)
    [[ "$request_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
    jq -e --arg repository "$GITHUB_REPOSITORY" --arg request "$request_id" '
      .repository.full_name == $repository and .head_repository.full_name == $repository and
      .head_branch == "main" and .event == "workflow_dispatch" and .status == "completed" and
      .conclusion == "success" and (.display_title | startswith("Chat Lifecycle Repair Handoff " + $request + " ")) and
      (.path == ".github/workflows/chat-lifecycle-repair-handoff.yml" or
       .path == ".github/workflows/chat-lifecycle-repair-handoff.yml@refs/heads/main")
    ' "$output" >/dev/null
    ;;
  acquire)
    [[ "$request_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
    jq -e --arg repository "$GITHUB_REPOSITORY" --arg request "$request_id" '
      .repository.full_name == $repository and .head_repository.full_name == $repository and
      .head_branch == "main" and .event == "workflow_dispatch" and .status == "completed" and
      .conclusion == "success" and .display_title == ("Cloud Lease Provision " + $request) and
      (.path == ".github/workflows/cloud-lease-provision.yml" or
       .path == ".github/workflows/cloud-lease-provision.yml@refs/heads/main")
    ' "$output" >/dev/null
    ;;
  cleanup)
    jq -e --arg repository "$GITHUB_REPOSITORY" '
      .repository.full_name == $repository and .head_repository.full_name == $repository and
      .head_branch == "main" and .status == "completed" and
      (.conclusion == "success" or .conclusion == "failure") and
      ((.path == ".github/workflows/chat-lifecycle-repair-finalize.yml" or
        .path == ".github/workflows/chat-lifecycle-repair-finalize.yml@refs/heads/main" or
        .path == ".github/workflows/chat-lifecycle-repair.yml" or
        .path == ".github/workflows/chat-lifecycle-repair.yml@refs/heads/main") and
       (.event == "schedule" or .event == "workflow_dispatch"))
    ' "$output" >/dev/null
    ;;
  *) exit 2 ;;
esac
