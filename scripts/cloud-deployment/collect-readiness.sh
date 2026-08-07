#!/usr/bin/env bash
set -euo pipefail

: "${WK_CLOUD_DEPLOYMENT_PLAN:?required}"
: "${WK_CLOUD_SSH_CONFIG:?required}"
: "${WK_CLOUD_READINESS_OUTPUT:?required}"
: "${WK_CLOUD_MANAGER_USER:?required}"
: "${WK_CLOUD_MANAGER_PASSWORD:?required}"
: "${WK_CLOUD_DEMO_USER:?required}"
: "${WK_CLOUD_DEMO_PASSWORD:?required}"

[[ "$WK_CLOUD_MANAGER_USER" =~ ^[A-Za-z0-9._-]{1,64}$ ]]
[[ "$WK_CLOUD_MANAGER_PASSWORD" =~ ^[0-9a-f]{64}$ ]]
[[ "$WK_CLOUD_DEMO_USER" =~ ^[A-Za-z0-9._-]{1,64}$ ]]
[[ "$WK_CLOUD_DEMO_PASSWORD" =~ ^[0-9a-f]{32,128}$ ]]
[[ "$WK_CLOUD_MANAGER_USER" == "$WK_CLOUD_DEMO_USER" ]]
[[ "$WK_CLOUD_MANAGER_PASSWORD" == "$WK_CLOUD_DEMO_PASSWORD" ]]

plan_digest="$(jq -er .plan_digest "$WK_CLOUD_DEPLOYMENT_PLAN")"
load_public="$(jq -er '.hosts[] | select(.role=="load") | .public_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
service1="$(jq -er '.hosts[] | select(.role=="service-1") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
service2="$(jq -er '.hosts[] | select(.role=="service-2") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
service3="$(jq -er '.hosts[] | select(.role=="service-3") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"

ssh_load() { ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load "$@"; }
ssh_service() {
  local address="$1"
  shift
  ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" "$@"
}

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT

collect_host() {
  local role="$1"
  local address="$2"
  local remote
  if [[ "$role" == load ]]; then
    remote=(ssh_load)
  else
    remote=(ssh_service "$address")
  fi
  local required_units
  if [[ "$role" == load ]]; then
    required_units='wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service prometheus.service wkanalysis.service caddy.service node-exporter.service wukongim-process-metrics.service wukongim-evidence.timer'
  else
    required_units='wukongim.service wkbench-host-metrics.service node-exporter.service wukongim-process-metrics.service wukongim-evidence.timer'
  fi
  local before after raw
  before="$(date -u +%s%3N)"
  raw="$(
    "${remote[@]}" "sudo bash -s -- '$required_units'" <<'REMOTE'
set -u
required_units="$1"
. /etc/os-release
base=false
/opt/wukongim/scripts/verify-base-tools.sh >/dev/null 2>&1 && base=true
digest=$(sed -n 's/.*digest="\([^" ]*\)".*/\1/p' /var/lib/wukongim/textfile/bundle.prom | head -1)
disk=$(cat /var/lib/wukongim-cloud/.wukongim-data-disk-id 2>/dev/null || true)
mount=$(findmnt -rn -T /var/lib/wukongim-cloud -o TARGET 2>/dev/null || true)
read -r system_size system_free < <(df -B1 --output=size,avail / | tail -1)
read -r data_size data_free < <(df -B1 --output=size,avail /var/lib/wukongim-cloud | tail -1)
units=
for unit in $required_units; do
  if systemctl is-active --quiet "$unit"; then units="${units}${units:+,}$unit"; fi
done
printf '%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s\n' "$ID" "$VERSION_ID" "$(uname -m)" "$base" "$digest" "$disk" "$mount" "$data_size" "$data_free" "$system_size" "$system_free" "$(date -u +%s%3N)" "$units"
REMOTE
  )"
  after="$(date -u +%s%3N)"
  local os_id os_version architecture base digest disk mount data_size data_free system_size system_free host_time units
  IFS='|' read -r os_id os_version architecture base digest disk mount data_size data_free system_size system_free host_time units <<<"$raw"
  local midpoint offset
  midpoint="$(((before + after) / 2))"
  offset="$((host_time - midpoint))"
  jq -n \
    --arg role "$role" --arg os "$os_id" --arg version "$os_version" --arg arch "$architecture" \
    --argjson base "$base" --arg digest "$digest" --arg disk "$disk" --arg mount "$mount" \
    --argjson data_size "$data_size" --argjson data_free "$data_free" \
    --argjson system_size "$system_size" --argjson system_free "$system_free" --argjson offset "$offset" \
    --arg units "$units" \
    '{role:$role,operating_system:$os,operating_system_version:$version,architecture:$arch,
      base_tools_available:$base,bundle_digest:$digest,data_disk_id:$disk,data_mount:$mount,
      data_filesystem_bytes:$data_size,data_free_bytes:$data_free,
      system_filesystem_bytes:$system_size,system_free_bytes:$system_free,
      clock_offset_milliseconds:$offset,active_units:($units|split(",")|map(select(length>0)))}'
}

collect_host service-1 "$service1" >"$temporary/service-1.json" &
pid1=$!
collect_host service-2 "$service2" >"$temporary/service-2.json" &
pid2=$!
collect_host service-3 "$service3" >"$temporary/service-3.json" &
pid3=$!
collect_host load "" >"$temporary/load.json" &
pid4=$!
wait "$pid1" "$pid2" "$pid3" "$pid4"
hosts="$(jq -s . "$temporary/service-1.json" "$temporary/service-2.json" "$temporary/service-3.json" "$temporary/load.json")"

ready_nodes=0
for address in "$service1" "$service2" "$service3"; do
  if ssh_load "curl --fail --silent --show-error --max-time 5 'http://${address}:5001/readyz' >/dev/null"; then
    ready_nodes=$((ready_nodes + 1))
  fi
done

manager_base="http://${load_public}"
login_payload="$(jq -cn --arg username "$WK_CLOUD_MANAGER_USER" --arg password "$WK_CLOUD_MANAGER_PASSWORD" '{username:$username,password:$password}')"
manager_login="$(curl --fail --silent --show-error --max-time 10 -H 'Content-Type: application/json' --data "$login_payload" "${manager_base}/manager/login")"
jq -e --arg username "$WK_CLOUD_MANAGER_USER" '
  .username == $username and
  (.permissions == [{resource:"*",actions:["r"]}])
' <<<"$manager_login" >/dev/null
manager_token="$(jq -er .access_token <<<"$manager_login")"
nodes_json="$(curl --fail --silent --show-error --max-time 10 -H "Authorization: Bearer ${manager_token}" "${manager_base}/manager/nodes")"
slots_json="$(curl --fail --silent --show-error --max-time 15 -H "Authorization: Bearer ${manager_token}" "${manager_base}/manager/slots")"
tasks_json="$(curl --fail --silent --show-error --max-time 10 -H "Authorization: Bearer ${manager_token}" "${manager_base}/manager/controller/tasks?limit=50")"
members="$(jq -er '[.items[] | select(.membership.join_state == "active" and .health.runtime_ready == true)] | length' <<<"$nodes_json")"
logical_groups="$(jq -er '.items | length' <<<"$slots_json")"
physical_slots="$(jq -er '[.items[].hash_slots.count // 0] | add // 0' <<<"$slots_json")"
slot_health="$(jq -cer -f "$(dirname "$0")/../cloud-sim/bootstrap-slot-health.jq" <<<"$slots_json")"
healthy_leaders="$(jq -er .healthy_slot_leaders <<<"$slot_health")"
healthy_replicas="$(jq -er .healthy_slot_replicas <<<"$slot_health")"
pending_tasks="$(jq -er .total <<<"$tasks_json")"

runtime_contracts=()
for node_id in 1 2 3; do
  config_json="$(curl --fail --silent --show-error --max-time 10 -H "Authorization: Bearer ${manager_token}" "${manager_base}/manager/nodes/${node_id}/config")"
  runtime_contracts+=("$(jq -cer --argjson node_id "$node_id" \
    -f "$(dirname "$0")/deployment-runtime-contract.jq" <<<"$config_json")")
done
runtime_contracts_json="$(printf '%s\n' "${runtime_contracts[@]}" | jq -s .)"
expected_physical="$(jq -er .topology.physical_hash_slots "$WK_CLOUD_DEPLOYMENT_PLAN")"
expected_groups="$(jq -er .topology.logical_slot_groups "$WK_CLOUD_DEPLOYMENT_PLAN")"
expected_slot_replicas="$(jq -er .topology.slot_replicas "$WK_CLOUD_DEPLOYMENT_PLAN")"
expected_channel_replicas="$(jq -er .topology.channel_replicas "$WK_CLOUD_DEPLOYMENT_PLAN")"
runtime_config_nodes="$(jq -er \
  --argjson physical "$expected_physical" --argjson groups "$expected_groups" \
  --argjson slots "$expected_slot_replicas" --argjson channels "$expected_channel_replicas" \
  '[.[] | select(.physical_hash_slots == $physical and .logical_slot_groups == $groups and
    .slot_replicas == $slots and .channel_replicas == $channels)] | length' <<<"$runtime_contracts_json")"
slot_replicas="$(jq -er 'map(.slot_replicas) | unique | if length == 1 then .[0] else 0 end' <<<"$runtime_contracts_json")"
channel_replicas="$(jq -er 'map(.channel_replicas) | unique | if length == 1 then .[0] else 0 end' <<<"$runtime_contracts_json")"

ready_workers=0
for port in 19091 19092 19093; do
  if ssh_load "curl --fail --silent --show-error --max-time 5 'http://127.0.0.1:${port}/healthz' >/dev/null"; then
    ready_workers=$((ready_workers + 1))
  fi
done
prometheus_targets="$(ssh_load "curl --fail --silent --show-error --max-time 10 'http://127.0.0.1:9090/api/v1/targets?state=active'")"
targets_want="$(jq -er '.data.activeTargets | length' <<<"$prometheus_targets")"
targets_up="$(jq -er '[.data.activeTargets[] | select(.health == "up")] | length' <<<"$prometheus_targets")"

workload_valid=false
ssh_load "sudo -u wukongim /opt/wukongim/bin/wkbench validate chat-lifecycle --config /etc/wukongim/chat-lifecycle.yaml >/dev/null && sudo -u wukongim /opt/wukongim/bin/wkbench validate chat-lifecycle --config /etc/wukongim/chat-lifecycle-rehearsal.yaml >/dev/null" && workload_valid=true
analysis_ready=false
ssh_load "sudo curl --fail --silent --show-error --max-time 10 --cacert /etc/wukongim/secrets/analysis-cert.pem https://127.0.0.1:19444/self-check >/dev/null" && analysis_ready=true
proxy_ready=false
manager_ready=true
demo_ready=false
curl --fail --silent --show-error --max-time 10 "http://${load_public}/" >/dev/null && proxy_ready=true && manager_ready=true
demo_html="$(curl --fail --silent --show-error --max-time 10 --user "${WK_CLOUD_DEMO_USER}:${WK_CLOUD_DEMO_PASSWORD}" "http://${load_public}/demo/")"
demo_asset="$(sed -n 's/.*\(\/demo\/assets\/[^" ]*\).*/\1/p' <<<"$demo_html" | head -1)"
test -n "$demo_asset"
curl --fail --silent --show-error --max-time 10 --user "${WK_CLOUD_DEMO_USER}:${WK_CLOUD_DEMO_PASSWORD}" "http://${load_public}${demo_asset}" >/dev/null && demo_ready=true

observed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
jq -n \
  --arg schema 'wukongim.cloud_deployment.readiness/v1' --arg plan_digest "$plan_digest" --arg observed_at "$observed_at" \
  --argjson hosts "$hosts" --argjson ready_nodes "$ready_nodes" --argjson members "$members" \
  --argjson slots "$physical_slots" --argjson leaders "$healthy_leaders" --argjson replicas "$healthy_replicas" \
  --argjson groups "$logical_groups" --argjson runtime_nodes "$runtime_config_nodes" \
  --argjson slot_replicas "$slot_replicas" --argjson channel_replicas "$channel_replicas" \
  --argjson pending "$pending_tasks" --argjson workers "$ready_workers" \
  --argjson targets_up "$targets_up" --argjson targets_want "$targets_want" \
  --argjson workload "$workload_valid" --argjson proxy "$proxy_ready" --argjson manager "$manager_ready" \
  --argjson demo "$demo_ready" --argjson analysis "$analysis_ready" \
  '{schema:$schema,deployment_plan_digest:$plan_digest,observed_at:$observed_at,hosts:$hosts,
    cluster:{ready_nodes:$ready_nodes,members:$members,physical_hash_slots:$slots,healthy_slot_leaders:$leaders,
      healthy_slot_replica_sets:$replicas,logical_slot_groups:$groups,runtime_config_nodes:$runtime_nodes,
      slot_replicas:$slot_replicas,channel_replicas:$channel_replicas,pending_controller_tasks:$pending},
    load:{ready_workers:$workers,prometheus_targets_up:$targets_up,prometheus_targets_want:$targets_want,
      workload_config_valid:$workload,proxy_ready:$proxy,manager_ready:$manager,demo_ready:$demo,analysis_ready:$analysis}}' \
  >"$WK_CLOUD_READINESS_OUTPUT"
