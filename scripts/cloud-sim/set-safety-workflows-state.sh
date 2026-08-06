#!/usr/bin/env bash

set -euo pipefail

operation="${1:-}"
case "$operation" in
  enable)
    # Cleanup is the billing backstop, so it must become active first.
    workflows=(cloud-sim-cleanup.yml cloud-sim-monitor.yml)
    ;;
  disable)
    # Keep cleanup active until the observer has stopped accepting triggers.
    workflows=(cloud-sim-monitor.yml cloud-sim-cleanup.yml)
    ;;
  *)
    echo "usage: $0 <enable|disable>" >&2
    exit 2
    ;;
esac

[[ "${GITHUB_REPOSITORY:-}" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || {
  echo "GITHUB_REPOSITORY must be an owner/repository identity" >&2
  exit 2
}

for workflow in "${workflows[@]}"; do
  gh api -X PUT \
    "repos/${GITHUB_REPOSITORY}/actions/workflows/${workflow}/${operation}" >/dev/null
done
