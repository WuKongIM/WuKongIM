#!/usr/bin/env bash
set -euo pipefail

: "${WK_CLOUD_SIM_PUBLIC_IP:?required}"
: "${WK_CLOUD_NODE1_IP:?required}"
: "${WK_CLOUD_NODE2_IP:?required}"
: "${WK_CLOUD_NODE3_IP:?required}"
: "${WK_CLOUD_SSH_KEY:?required}"
: "${WK_CLOUD_BUNDLE_DIGEST:?required}"
: "${WK_CLOUD_GATE_OUTPUT:?required}"
: "${WK_ANALYSIS_MANAGER_PASSWORD:?required}"
: "${WK_CLOUD_PUBLIC_OBSERVATION:?required}"
if [[ "$WK_CLOUD_PUBLIC_OBSERVATION" != "true" && "$WK_CLOUD_PUBLIC_OBSERVATION" != "false" ]]; then
  echo "WK_CLOUD_PUBLIC_OBSERVATION must be true or false" >&2
  exit 1
fi

ssh_config="$(mktemp)"
trap 'rm -f "$ssh_config"' EXIT
WK_CLOUD_SSH_CONFIG="$ssh_config" \
  "$(dirname "$0")/write-ssh-config.sh"
ssh_common=(-F "$ssh_config")

ssh_sim() {
  ssh "${ssh_common[@]}" wukong-sim-jump "$@"
}

ssh_node() {
  local address="$1"
  shift
  ssh "${ssh_common[@]}" "$address" "$@"
}

bundle_digest() {
  local command='sudo sed -n '\''s/.*digest="\([^"]*\)".*/\1/p'\'' /var/lib/wukongim/textfile/bundle.prom'
  if [[ "$1" == "sim" ]]; then
    ssh_sim "$command"
  else
    ssh_node "$2" "$command"
  fi
}

active_services() {
  local role="$1"
  local address="${2:-}"
  shift 2 || true
  local services=("$@")
  local active=()
  for service in "${services[@]}"; do
    if [[ "$role" == "sim" ]]; then
      ssh_sim "systemctl is-active --quiet '${service}.service'"
    else
      ssh_node "$address" "systemctl is-active --quiet '${service}.service'"
    fi
    active+=("$service")
  done
  printf '%s\n' "${active[@]}" | jq -Rsc 'split("\n") | map(select(length > 0))'
}

node1_digest="$(bundle_digest node-1 "$WK_CLOUD_NODE1_IP")"
node2_digest="$(bundle_digest node-2 "$WK_CLOUD_NODE2_IP")"
node3_digest="$(bundle_digest node-3 "$WK_CLOUD_NODE3_IP")"
sim_digest="$(bundle_digest sim "")"

node1_services="$(active_services node-1 "$WK_CLOUD_NODE1_IP" wukongim node-exporter wukongim-cgroup-metrics)"
node2_services="$(active_services node-2 "$WK_CLOUD_NODE2_IP" wukongim node-exporter wukongim-cgroup-metrics)"
node3_services="$(active_services node-3 "$WK_CLOUD_NODE3_IP" wukongim node-exporter wukongim-cgroup-metrics)"
sim_required_services=(wkbench-worker prometheus node-exporter wkanalysis)
if [[ "$WK_CLOUD_PUBLIC_OBSERVATION" == "true" ]]; then
  sim_required_services+=(wkcloudview)
fi
sim_services="$(active_services sim "" "${sim_required_services[@]}")"

cgroup_metric_nodes=()
for pair in "1:$WK_CLOUD_NODE1_IP" "2:$WK_CLOUD_NODE2_IP" "3:$WK_CLOUD_NODE3_IP"; do
  node_id="${pair%%:*}"
  address="${pair#*:}"
  ssh_node "$address" "test \"\$(sudo sed -n 's/^wukongim_service_cgroup_available 1$/1/p' /var/lib/wukongim/textfile/wukongim-cgroup.prom | tail -n 1)\" = 1"
  cgroup_metric_nodes+=("$node_id")
done
cgroup_metric_nodes_json="$(printf '%s\n' "${cgroup_metric_nodes[@]}" | jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)')"

ready_nodes=()
for pair in "1:$WK_CLOUD_NODE1_IP" "2:$WK_CLOUD_NODE2_IP" "3:$WK_CLOUD_NODE3_IP"; do
  node_id="${pair%%:*}"
  address="${pair#*:}"
  ssh_sim "curl --fail --silent --show-error --max-time 5 'http://${address}:5001/readyz' >/dev/null"
  ready_nodes+=("$node_id")
done
ready_json="$(printf '%s\n' "${ready_nodes[@]}" | jq -Rsc 'split("\n") | map(select(length > 0) | tonumber)')"

manager_base="http://${WK_CLOUD_NODE1_IP}:5301"
login_payload="$(jq -cn --arg password "$WK_ANALYSIS_MANAGER_PASSWORD" '{username:"analysis",password:$password}')"
manager_token="$(ssh_sim "curl --fail --silent --show-error --max-time 10 -H 'Content-Type: application/json' --data '${login_payload}' '${manager_base}/manager/login'" | jq -er .access_token)"
nodes_json="$(ssh_sim "curl --fail --silent --show-error --max-time 10 -H 'Authorization: Bearer ${manager_token}' '${manager_base}/manager/nodes'")"
slots_json="$(ssh_sim "curl --fail --silent --show-error --max-time 15 -H 'Authorization: Bearer ${manager_token}' '${manager_base}/manager/slots'")"
tasks_json="$(ssh_sim "curl --fail --silent --show-error --max-time 10 -H 'Authorization: Bearer ${manager_token}' '${manager_base}/manager/controller/tasks?limit=50'")"
runtime_scale="$(ssh_sim "sudo cat /etc/wukongim/scenario.yaml" | awk -f "$(dirname "$0")/read-scenario-scale.awk")"
expected_node_runtime_contract="$(ssh_sim "sudo cat /etc/wukongim/effective-node-runtime-contract.json" | jq -ce .)"

node_runtime_contract() {
  local node_id="$1"
  local config_json
  config_json="$(ssh_sim "curl --fail --silent --show-error --max-time 10 -H 'Authorization: Bearer ${manager_token}' '${manager_base}/manager/nodes/${node_id}/config'")"
  jq -cer --arg scale "$runtime_scale" --argjson node_id "$node_id" \
    -f "$(dirname "$0")/bootstrap-runtime-contract.jq" <<<"$config_json"
}

node1_runtime_contract="$(node_runtime_contract 1)"
node2_runtime_contract="$(node_runtime_contract 2)"
node3_runtime_contract="$(node_runtime_contract 3)"
node_runtime_contracts="$(jq -cn \
  --argjson node1 "$node1_runtime_contract" \
  --argjson node2 "$node2_runtime_contract" \
  --argjson node3 "$node3_runtime_contract" \
  '{"node-1":$node1,"node-2":$node2,"node-3":$node3}')"

member_count="$(jq -er '[.items[] | select(.membership.join_state == "active" and .health.runtime_ready == true)] | length' <<<"$nodes_json")"
slot_group_count="$(jq -er '.items | length' <<<"$slots_json")"
hash_slot_count="$(jq -er '[.items[].hash_slots.count // 0] | add // 0' <<<"$slots_json")"
slot_health="$(jq -cer -f "$(dirname "$0")/bootstrap-slot-health.jq" <<<"$slots_json")"
healthy_slot_leaders="$(jq -er '.healthy_slot_leaders' <<<"$slot_health")"
healthy_slot_replicas="$(jq -er '.healthy_slot_replicas' <<<"$slot_health")"
pending_tasks="$(jq -er '.total' <<<"$tasks_json")"

prometheus_targets="$(ssh_sim "curl --fail --silent --show-error --max-time 10 'http://127.0.0.1:9090/api/v1/targets?state=active'")"
targets_want="$(jq -er '.data.activeTargets | length' <<<"$prometheus_targets")"
targets_up="$(jq -er '[.data.activeTargets[] | select(.health == "up")] | length' <<<"$prometheus_targets")"

ssh_sim "sudo -u wukongim curl --fail --silent --show-error --max-time 10 --resolve '${WK_CLOUD_SIM_PUBLIC_IP}:19092:127.0.0.1' 'https://${WK_CLOUD_SIM_PUBLIC_IP}:19092/self-check' --cacert /etc/wukongim/tls/mcp-ca.pem >/dev/null"
analysis_self_check=true
cloud_view_self_check=false
if [[ "$WK_CLOUD_PUBLIC_OBSERVATION" == "true" ]]; then
  ssh_sim "curl --fail --silent --show-error --max-time 10 'http://127.0.0.1:19443/cloud-view/status' >/dev/null"
  cloud_view_self_check=true
fi
ssh_sim "sudo -u wukongim bash -c 'set -a; source /etc/wukongim/sim.env; set +a; /opt/wukongim/bin/wkbench validate --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml'"
wkbench_validate=true
ssh_sim "sudo -u wukongim bash -c 'set -a; source /etc/wukongim/sim.env; set +a; /opt/wukongim/bin/wkbench doctor --target /etc/wukongim/target.yaml --workers /etc/wukongim/workers.yaml --scenario /etc/wukongim/scenario.yaml'"
wkbench_doctor=true

jq -n \
  --arg expected "$WK_CLOUD_BUNDLE_DIGEST" \
  --arg node1_digest "$node1_digest" --arg node2_digest "$node2_digest" --arg node3_digest "$node3_digest" --arg sim_digest "$sim_digest" \
  --argjson node1_services "$node1_services" --argjson node2_services "$node2_services" --argjson node3_services "$node3_services" --argjson sim_services "$sim_services" \
  --argjson cgroup_metric_nodes "$cgroup_metric_nodes_json" \
  --argjson ready_nodes "$ready_json" --argjson member_count "$member_count" --argjson hash_slot_count "$hash_slot_count" --argjson slot_group_count "$slot_group_count" \
  --argjson healthy_slot_leaders "$healthy_slot_leaders" --argjson healthy_slot_replicas "$healthy_slot_replicas" \
  --argjson pending_tasks "$pending_tasks" --argjson targets_up "$targets_up" --argjson targets_want "$targets_want" \
  --argjson analysis_self_check "$analysis_self_check" --argjson public_view_enabled "$WK_CLOUD_PUBLIC_OBSERVATION" \
  --argjson cloud_view_self_check "$cloud_view_self_check" --argjson wkbench_validate "$wkbench_validate" --argjson wkbench_doctor "$wkbench_doctor" \
  --arg runtime_scale "$runtime_scale" --argjson expected_node_runtime_contract "$expected_node_runtime_contract" --argjson node_runtime_contracts "$node_runtime_contracts" \
  '{
    bundle_digests:{"node-1":$node1_digest,"node-2":$node2_digest,"node-3":$node3_digest,sim:$sim_digest},
    active_services:{"node-1":$node1_services,"node-2":$node2_services,"node-3":$node3_services,sim:$sim_services},
    cgroup_metrics_available_node_ids:$cgroup_metric_nodes,
    ready_node_ids:$ready_nodes,cluster_member_count:$member_count,hash_slot_count:$hash_slot_count,slot_group_count:$slot_group_count,
    healthy_slot_leaders:$healthy_slot_leaders,healthy_slot_replicas:$healthy_slot_replicas,
    pending_controller_tasks:$pending_tasks,prometheus_targets_up:$targets_up,prometheus_targets_want:$targets_want,
    analysis_mcp_self_check:$analysis_self_check,public_view_enabled:$public_view_enabled,
    cloud_view_self_check:$cloud_view_self_check,wkbench_validate:$wkbench_validate,wkbench_doctor:$wkbench_doctor,
    runtime_scale:$runtime_scale,expected_node_runtime_contract:$expected_node_runtime_contract,node_runtime_contracts:$node_runtime_contracts
  }' >"$WK_CLOUD_GATE_OUTPUT"
