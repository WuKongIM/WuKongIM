#!/usr/bin/env bash
set -euo pipefail

if (( $# != 5 )); then
  echo 'usage: accrued-cost.sh RUN_PLAN QUOTE CREATED_AT ENDED_AT NETWORK_TRANSMIT_BYTES_OR_MINUS_ONE' >&2
  exit 2
fi

run_plan="$1"
quote="$2"
created_at="$3"
ended_at="$4"
network_bytes="$5"
[[ -f "$run_plan" && -f "$quote" ]]
[[ "$network_bytes" =~ ^-1$|^[0-9]+$ ]]

parse_utc_epoch() {
  local value="$1"
  local normalized="$value"
  if [[ "$value" =~ ^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})\.[0-9]+Z$ ]]; then
    normalized="${BASH_REMATCH[1]}Z"
  fi
  [[ "$normalized" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]]
  date -u -d "$normalized" +%s 2>/dev/null ||
    date -j -u -f '%Y-%m-%dT%H:%M:%SZ' "$normalized" +%s
}

created_epoch="$(parse_utc_epoch "$created_at")"
ended_epoch="$(parse_utc_epoch "$ended_at")"
(( created_epoch > 0 && ended_epoch >= created_epoch ))
held_seconds=$(( ended_epoch - created_epoch ))
held_hours=$(( (held_seconds + 3599) / 3600 ))
(( held_hours > 0 )) || held_hours=1

jq -en --slurpfile plan "$run_plan" --slurpfile priced "$quote" \
  --argjson held_hours "$held_hours" --argjson network_bytes "$network_bytes" '
  def ceildiv($n; $d): (($n + $d - 1) / $d | floor);
  ($plan[0].lease_plan.host_groups // []) as $groups |
  ($priced[0].quote.line_items // []) as $lines |
  if ($groups | length) == 0 or ($lines | length) == 0 then error("missing cost evidence") else . end |
  reduce $lines[] as $line (0;
    if ($line.quantity | type) != "number" or $line.quantity <= 0 or
       ($line.cost_micros | type) != "number" or $line.cost_micros < 0 then
      error("invalid cost line")
    elif $line.kind == "postpaid_host_hour" then
      ([ $groups[] | select(.role == $line.role) | .count ] | if length == 1 then .[0] else error("host role mismatch") end) as $count |
      . + ceildiv($line.cost_micros * ($count * $held_hours); $line.quantity)
    elif $line.kind == "eip_public_egress_gib" then
      (if $network_bytes == -1 then $line.quantity else ceildiv($network_bytes; 1073741824) end) as $gib |
      . + ceildiv($line.cost_micros * $gib; $line.quantity)
    elif $line.kind == "eip_retention_policy_risk_hour" then
      . + $line.cost_micros
    else
      error("unknown cost line")
    end
  ) |
  if type == "number" and . >= 0 and . <= 1500000000 then floor else error("cost overflow") end
  '
