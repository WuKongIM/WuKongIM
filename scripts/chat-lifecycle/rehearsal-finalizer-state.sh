#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

[[ "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]

operation="${1:-}"
finalizer_workflow=chat-lifecycle-rehearsal-finalize.yml
producer_workflow=chat-lifecycle-rehearsal.yml
api_attempts=4

temporary="$(mktemp -d)"
trap 'rm -r "$temporary"' EXIT

github_api() {
  local method="$1" endpoint="$2" output="$3" attempt status delay
  for ((attempt = 1; attempt <= api_attempts; attempt++)); do
    status=0
    gh api --method "$method" "$endpoint" >"$output" || status=$?
    if (( status == 0 )); then
      return 0
    fi
    if (( attempt == api_attempts )); then
      return "$status"
    fi
    delay=$((1 << (attempt - 1)))
    echo "workflow state request failed; retrying attempt $((attempt + 1))/${api_attempts} after ${delay}s" >&2
    sleep "$delay"
  done
}

enable_finalizer() {
  github_api PUT \
    "repos/${GITHUB_REPOSITORY}/actions/workflows/${finalizer_workflow}/enable" \
    "$temporary/enable.json"
  echo 'Rehearsal finalizer schedule is enabled.'
}

producer_is_active() {
  local status response
  for status in queued in_progress waiting requested pending; do
    response="$temporary/producer-${status}.json"
    github_api GET \
      "repos/${GITHUB_REPOSITORY}/actions/workflows/${producer_workflow}/runs?branch=main&event=workflow_dispatch&status=${status}&per_page=100" \
      "$response"
    jq -e '(.total_count | type == "number") and .total_count <= 100 and
      (.workflow_runs | type == "array")' "$response" >/dev/null
    if jq -e --arg repository "$GITHUB_REPOSITORY" --arg status "$status" '
      any(.workflow_runs[];
        .repository.full_name == $repository and
        .head_repository.full_name == $repository and
        .event == "workflow_dispatch" and .head_branch == "main" and
        .status == $status)
    ' "$response" >/dev/null; then
      return 0
    fi
  done
  return 1
}

disable_if_idle() {
  local matrix="$temporary/active-rehearsals.json"
  scripts/chat-lifecycle/discover-active-handoffs.sh '' "$matrix"
  if jq -e '.include | length > 0' "$matrix" >/dev/null; then
    echo 'Rehearsal finalizer schedule remains enabled: an authenticated handoff still lacks zero-inventory proof.'
    return 0
  fi
  if producer_is_active; then
    echo 'Rehearsal finalizer schedule remains enabled: a protected rehearsal producer may still publish a handoff.'
    return 0
  fi
  github_api PUT \
    "repos/${GITHUB_REPOSITORY}/actions/workflows/${finalizer_workflow}/disable" \
    "$temporary/disable.json"
  echo 'Rehearsal finalizer schedule is disabled after authenticated global idle proof.'
}

case "$operation" in
  enable) enable_finalizer ;;
  disable-if-idle) disable_if_idle ;;
  *)
    echo "usage: $0 <enable|disable-if-idle>" >&2
    exit 2
    ;;
esac
