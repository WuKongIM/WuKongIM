#!/usr/bin/env bash
# Keep one open tracking Issue per nightly suite: create it on the first
# failure, comment on later failures. Closing the Issue is a human decision.
# Requires GH_TOKEN with issues:write and the standard GITHUB_* variables.
set -euo pipefail
[[ $# == 1 ]] || { echo 'usage: report-nightly-failure.sh SUITE_TITLE' >&2; exit 2; }
title="[nightly] $1 failing"
run_url="${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}"
body="Failed run: ${run_url} (commit ${GITHUB_SHA})"
number="$(gh issue list --repo "$GITHUB_REPOSITORY" --state open --search "\"$title\" in:title" \
  --json number,title --jq "map(select(.title == \"$title\")) | .[0].number // empty")"
if [[ -n "$number" ]]; then
  gh issue comment "$number" --repo "$GITHUB_REPOSITORY" --body "$body"
else
  gh issue create --repo "$GITHUB_REPOSITORY" --title "$title" --body "$body"
fi
