#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_REPOSITORY:?required}"

[[ "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]

operation="${1:-}"
release_workflow=cloud-lease-release.yml
producer_workflows=(cloud-lease-provision.yml chat-lifecycle-rehearsal.yml chat-lifecycle-formal.yml)
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

set_release_state() {
  local state="$1"
  github_api PUT \
    "repos/${GITHUB_REPOSITORY}/actions/workflows/${release_workflow}/${state}" \
    "$temporary/release-${state}.json"
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

release_workflow_is_active() {
  workflow_is_active "$release_workflow"
}

enable_release() {
  set_release_state enable
  echo 'Generic Cloud Lease release schedule is enabled.'
}

enable_after_provision() {
  local deadline
  enable_release
  deadline=$(( $(date -u +%s) + 2700 ))
  while release_workflow_is_active; do
    [[ "$(date -u +%s)" -lt "$deadline" ]] || {
      echo 'Timed out waiting for an older generic Cloud Lease release pass before sealing the enabled backstop.' >&2
      return 1
    }
    sleep 5
  done
  set_release_state enable
  echo 'Generic Cloud Lease release schedule is enabled after all older release passes became terminal.'
}

disable_if_idle() {
  local workflow
  for workflow in "${producer_workflows[@]}"; do
    if workflow_is_active "$workflow"; then
      echo "Generic Cloud Lease release schedule remains enabled: protected producer ${workflow} is active."
      return 0
    fi
  done
  set_release_state disable
  echo 'Generic Cloud Lease release schedule is disabled after provider zero-inventory and producer-idle proof.'
}

case "$operation" in
  enable) enable_release ;;
  enable-after-provision) enable_after_provision ;;
  disable-if-idle) disable_if_idle ;;
  *)
    echo "usage: $0 <enable|enable-after-provision|disable-if-idle>" >&2
    exit 2
    ;;
esac
