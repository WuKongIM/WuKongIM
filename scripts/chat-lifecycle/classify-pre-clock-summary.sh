#!/usr/bin/env bash
set -euo pipefail

[[ "$#" -eq 1 ]]
summary="$1"
(( ${#summary} <= 1024 ))

pattern='^chat-lifecycle outcome=([a-z_]+) cause=([a-z_]+) coordinator_code=([a-z_]+) preflight_code=([a-z_]*) report=unavailable$'
[[ "$summary" =~ $pattern ]]
coordinator_code="${BASH_REMATCH[3]}"

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
