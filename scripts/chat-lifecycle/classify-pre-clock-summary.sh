#!/usr/bin/env bash
set -euo pipefail

diagnostic=false
if [[ "$#" -eq 2 && "$1" == --diagnostic-json ]]; then
  diagnostic=true
  shift
fi
[[ "$#" -eq 1 ]]
summary="$1"
syntax_valid=false

# The optional receipt exposes closed codes only. The default exit contract
# remains the control decision, including repairable preflight and unknown data.
emit_diagnostic() {
  local status="$1" metadata
  metadata="$(printf '%s' "$summary" | python3 "$(dirname "$0")/pre-clock-diagnostic.py" digest)"
  jq -cn --arg schema wukongim.chat_lifecycle.pre_clock_summary/v1 \
    --arg status "$status" --argjson syntax_valid "$syntax_valid" --argjson metadata "$metadata" \
    --arg coordinator_code "${safe_coordinator_code:-}" --arg preflight_code "${safe_preflight_code:-}" \
    --arg grant_failure_code "${safe_grant_code:-}" --arg worker_runtime_code "${safe_runtime_code:-}" \
    --arg observer_code "${safe_observer_code:-}" \
    '$metadata + {schema:$schema,status:$status,syntax_valid:$syntax_valid,
      coordinator_code:$coordinator_code,preflight_code:$preflight_code,
      grant_failure_code:$grant_failure_code,worker_runtime_code:$worker_runtime_code,observer_code:$observer_code}'
}

reject_summary() {
  if [[ "$diagnostic" == true ]]; then
    emit_diagnostic "$1"
    exit 0
  fi
  exit 1
}

(( ${#summary} <= 1024 )) || reject_summary oversized

pattern='^chat-lifecycle outcome=([a-z_]+) cause=([a-z_]+) coordinator_code=([a-z_]+)( grant_failure_code=([a-z_]*))?( worker_runtime_code=([a-z_]*))?( observer_code=([a-z_]*))? preflight_code=([a-z_]*) report=unavailable$'
if ! [[ "$summary" =~ $pattern ]]; then
  reject_summary invalid_syntax
fi
syntax_valid=true
coordinator_code="${BASH_REMATCH[3]}"
grant_field="${BASH_REMATCH[4]:-}"
grant_code="${BASH_REMATCH[5]:-}"
runtime_field="${BASH_REMATCH[6]:-}"
runtime_code="${BASH_REMATCH[7]:-}"
observer_field="${BASH_REMATCH[8]:-}"
observer_code="${BASH_REMATCH[9]:-}"
preflight_code="${BASH_REMATCH[10]:-}"

if [[ -n "$grant_field" ]]; then
  case "$grant_code" in
    ''|plan|delivery|tick|coverage) ;;
    *) reject_summary unknown_code ;;
  esac
fi

if [[ -n "$runtime_field" ]]; then
  case "$runtime_code" in
    ''|retry_queue_saturated|engine_queue_saturated|engine_cpu_saturated|engine_inflight_saturated|session_login_saturated|offered_load_under_delivery|session_scheduler_cpu_saturated|engine_clock_moved_backwards|lifecycle_fence_exhausted|lifecycle_lease_invalidated|lifecycle_replay_saturated) ;;
    *) reject_summary unknown_code ;;
  esac
fi

if [[ "$coordinator_code" == observer ]]; then
  if [[ -n "$observer_field" ]]; then
    case "$observer_code" in
      stopped|topology|service_health|cluster_health|leader_imbalance|evidence) ;;
      *) reject_summary unknown_code ;;
    esac
  fi
elif [[ -n "$observer_code" ]]; then
  reject_summary unknown_code
fi

if [[ "$diagnostic" == true ]]; then
  case "$coordinator_code" in
    preflight|completed|setup|assignment|start|grant|runtime|observer|checkpoint|capacity|finalize|generation_reuse|stopped) ;;
    *) reject_summary unknown_code ;;
  esac
  case "$preflight_code" in
    ''|ok|topology|target_unavailable|target_config|cluster|metrics|bench_capability|worker_unavailable|gateway|unauthorized|disk_ambiguous|disk_capacity|disk_free) ;;
    *) reject_summary unknown_code ;;
  esac
  safe_coordinator_code="$coordinator_code"
  safe_preflight_code="$preflight_code"
  safe_grant_code="$grant_code"
  safe_runtime_code="$runtime_code"
  safe_observer_code="$observer_code"
  emit_diagnostic parsed
  exit 0
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
