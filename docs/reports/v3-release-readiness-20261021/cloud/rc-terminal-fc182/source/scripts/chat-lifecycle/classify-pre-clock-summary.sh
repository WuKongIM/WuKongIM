#!/usr/bin/env bash
set -euo pipefail

[[ "$#" -eq 1 ]]
summary="$1"
(( ${#summary} <= 1024 ))

pattern='^chat-lifecycle outcome=([a-z_]+) cause=([a-z_]+) coordinator_code=([a-z_]+)( grant_failure_code=([a-z_]*))?( worker_runtime_code=([a-z_]*))?( observer_code=([a-z_]*))? preflight_code=([a-z_]*) report=unavailable$'
if ! [[ "$summary" =~ $pattern ]]; then
  exit 1
fi
coordinator_code="${BASH_REMATCH[3]}"
grant_field="${BASH_REMATCH[4]:-}"
grant_code="${BASH_REMATCH[5]:-}"
runtime_field="${BASH_REMATCH[6]:-}"
runtime_code="${BASH_REMATCH[7]:-}"
observer_field="${BASH_REMATCH[8]:-}"
observer_code="${BASH_REMATCH[9]:-}"

if [[ -n "$grant_field" ]]; then
  case "$grant_code" in
    ''|plan|delivery|tick|coverage) ;;
    *) exit 1 ;;
  esac
fi

if [[ -n "$runtime_field" ]]; then
  case "$runtime_code" in
    ''|retry_queue_saturated|engine_queue_saturated|engine_cpu_saturated|engine_inflight_saturated|session_login_saturated|offered_load_under_delivery|session_scheduler_cpu_saturated|engine_clock_moved_backwards|lifecycle_fence_exhausted|lifecycle_lease_invalidated|lifecycle_replay_saturated) ;;
    *) exit 1 ;;
  esac
fi

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
