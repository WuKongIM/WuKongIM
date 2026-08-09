#!/usr/bin/env bash
set -euo pipefail

[[ "$#" -eq 1 ]]
summary="$1"
(( ${#summary} <= 1024 ))

pattern='^chat-lifecycle outcome=([a-z_]+) cause=([a-z_]+) coordinator_code=([a-z_]+)( observer_code=([a-z_]*))? preflight_code=([a-z_]*) report=unavailable$'
[[ "$summary" =~ $pattern ]]
coordinator_code="${BASH_REMATCH[3]}"
observer_field="${BASH_REMATCH[4]:-}"
observer_code="${BASH_REMATCH[5]:-}"

if [[ "$coordinator_code" == observer ]]; then
  if [[ -n "$observer_field" ]]; then
    case "$observer_code" in
      stopped|topology|service_health|cluster_health|leader_imbalance|evidence) ;;
      *) exit 1 ;;
    esac
  fi
elif [[ -n "$observer_code" ]]; then
  exit 1
fi

case "$coordinator_code" in
  preflight)
    # Preflight can fail because deployment readiness has not converged yet.
    exit 1
    ;;
  completed|setup|assignment|start|grant|runtime|observer|checkpoint|capacity|finalize|generation_reuse|stopped)
    printf '%s\n' "$coordinator_code"
    ;;
  *)
    # Unknown vocabulary fails closed into the existing bounded repair path.
    exit 1
    ;;
esac
