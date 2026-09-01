#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"

[[ "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]

operation="${1:-}"
starter_workflow=chat-lifecycle-formal.yml
finalizer_workflow=chat-lifecycle-formal-finalize.yml
transition_producer_workflow=chat-lifecycle-rehearsal-finalize.yml
formal_producer_workflow=chat-lifecycle-formal.yml
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

set_workflow_state() {
  local workflow="$1" state="$2"
  github_api PUT \
    "repos/${GITHUB_REPOSITORY}/actions/workflows/${workflow}/${state}" \
    "$temporary/${workflow}-${state}.json"
}

workflow_is_active() {
  local workflow="$1" status response
  for status in queued in_progress waiting requested pending; do
    response="$temporary/${workflow}-${status}.json"
    github_api GET \
      "repos/${GITHUB_REPOSITORY}/actions/workflows/${workflow}/runs?branch=main&status=${status}&per_page=100" \
      "$response"
    jq -e '(.total_count | type == "number") and .total_count <= 100 and
      (.workflow_runs | type == "array")' "$response" >/dev/null
    if jq -e --arg repository "$GITHUB_REPOSITORY" --arg status "$status" '
      any(.workflow_runs[];
        .repository.full_name == $repository and
        .head_repository.full_name == $repository and
        (.event == "schedule" or .event == "workflow_dispatch") and
        .head_branch == "main" and .status == $status)
    ' "$response" >/dev/null; then
      return 0
    fi
  done
  return 1
}

enable_starter() {
  set_workflow_state "$starter_workflow" enable
  echo 'Formal continuation schedule is enabled.'
}

enable_finalizer() {
  set_workflow_state "$finalizer_workflow" enable
  echo 'Formal finalizer schedule is enabled.'
}

disable_starter_if_idle() {
  local matrix="$temporary/formal-transitions.json"
  scripts/chat-lifecycle/discover-formal-transitions.sh '' "$matrix"
  if jq -e '.include | length > 0' "$matrix" >/dev/null; then
    echo 'Formal continuation schedule remains enabled: an authenticated transition is awaiting consumption.'
    return 0
  fi
  if workflow_is_active "$transition_producer_workflow"; then
    echo 'Formal continuation schedule remains enabled: a protected rehearsal finalizer may still publish a transition.'
    return 0
  fi
  set_workflow_state "$starter_workflow" disable
  echo 'Formal continuation schedule is disabled after authenticated global idle proof.'
}

disable_finalizer_if_idle() {
  local matrix="$temporary/active-formal-handoffs.json"
  WK_CHAT_STAGE=formal scripts/chat-lifecycle/discover-active-handoffs.sh '' "$matrix"
  if jq -e '.include | length > 0' "$matrix" >/dev/null; then
    echo 'Formal finalizer schedule remains enabled: an authenticated handoff still lacks zero-inventory proof.'
    return 0
  fi
  if workflow_is_active "$formal_producer_workflow"; then
    echo 'Formal finalizer schedule remains enabled: a protected formal producer may still publish a handoff.'
    return 0
  fi
  set_workflow_state "$finalizer_workflow" disable
  echo 'Formal finalizer schedule is disabled after authenticated global idle proof.'
}

case "$operation" in
  enable-starter) enable_starter ;;
  enable-finalizer) enable_finalizer ;;
  disable-starter-if-idle) disable_starter_if_idle ;;
  disable-finalizer-if-idle) disable_finalizer_if_idle ;;
  *)
    echo "usage: $0 <enable-starter|enable-finalizer|disable-starter-if-idle|disable-finalizer-if-idle>" >&2
    exit 2
    ;;
esac
