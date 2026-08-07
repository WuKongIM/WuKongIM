#!/usr/bin/env bash
set -euo pipefail

if (( $# < 4 )); then
  echo 'usage: diagnosis-budget.sh RUN_PLAN QUOTE RECEIPT NOW [REPORT...]' >&2
  exit 2
fi

run_plan="$1"
quote="$2"
receipt="$3"
now="$4"
shift 4
[[ -f "$run_plan" && -f "$quote" && -f "$receipt" ]]
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"

prior="$(jq -er '.lease_plan.budget.committed_micros | select(type == "number" and . >= 0)' "$run_plan")"
operational_stop="$(jq -er '.lease_plan.budget.operational_stop_micros | select(type == "number" and . > 0)' "$run_plan")"
created_at="$(jq -er '.receipt.created_at | select(type == "string")' "$receipt")"
network_bytes=-1
for report in "$@"; do
  if [[ -s "$report" ]]; then
    candidate="$(jq -er '.resources.capacity.network_transmit_bytes | select(type == "number" and . >= 0)' "$report" 2>/dev/null || true)"
    if [[ "$candidate" =~ ^[0-9]+$ ]]; then
      network_bytes="$candidate"
    fi
  fi
done

lease_cost="$("$script_dir/accrued-cost.sh" "$run_plan" "$quote" "$created_at" "$now" "$network_bytes")"
aggregate=$(( prior + lease_cost ))
(( aggregate >= 0 && aggregate <= 1500000000 ))
safe=false
(( aggregate < operational_stop )) && safe=true
jq -n --argjson aggregate "$aggregate" --argjson operational_stop "$operational_stop" \
  --argjson network_bytes "$network_bytes" --argjson safe "$safe" \
  '{safe:$safe,aggregate_cost_micros:$aggregate,operational_stop_micros:$operational_stop,
    network_transmit_bytes:$network_bytes}'
