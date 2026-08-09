#!/usr/bin/env bash
set -euo pipefail

: "${WK_CLOUD_DEPLOYMENT_PLAN:?required}"
: "${WK_CLOUD_BUNDLE_ARCHIVE:?required}"
: "${WK_CLOUD_RUNTIME_NODE_ARCHIVE:?required}"
: "${WK_CLOUD_RUNTIME_LOAD_ARCHIVE:?required}"
: "${WK_CLOUD_SSH_CONFIG:?required}"
: "${WK_CLOUD_FAILURE_OUTPUT:?required}"
: "${WK_CLOUD_LAST_GATE_OUTPUT:?required}"

for input in "$WK_CLOUD_DEPLOYMENT_PLAN" "$WK_CLOUD_BUNDLE_ARCHIVE" \
  "$WK_CLOUD_RUNTIME_NODE_ARCHIVE" "$WK_CLOUD_RUNTIME_LOAD_ARCHIVE" "$WK_CLOUD_SSH_CONFIG"; do
  [[ -f "$input" && ! -L "$input" ]]
done

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
source "$script_dir/../cloud-sim/ssh-retry.sh"

write_failure() {
  "$script_dir/write-deployment-failure.sh" "$WK_CLOUD_FAILURE_OUTPUT" "$@"
}
complete_gate() {
  printf '%s\n' "$1" >"$WK_CLOUD_LAST_GATE_OUTPUT"
}

service1="$(jq -er '.hosts[] | select(.role=="service-1") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
service2="$(jq -er '.hosts[] | select(.role=="service-2") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
service3="$(jq -er '.hosts[] | select(.role=="service-3") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"

export WK_CLOUD_SSH_DEADLINE_EPOCH="${WK_CLOUD_SSH_DEADLINE_EPOCH:-$(( $(date -u +%s) + 1500 ))}"
write_failure bundle_transfer_failed plan_validated load \
  "load host did not accept the deployment payload" "load payload state is unknown"
cloud_ssh_retry load-ready 30 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load true
cloud_ssh_retry load-upload 3 5 scp -F "$WK_CLOUD_SSH_CONFIG" \
  "$WK_CLOUD_BUNDLE_ARCHIVE" "$WK_CLOUD_DEPLOYMENT_PLAN" \
  "$WK_CLOUD_RUNTIME_NODE_ARCHIVE" "$WK_CLOUD_RUNTIME_LOAD_ARCHIVE" \
  "$script_dir/install-orchestrator-compat-user.sh" \
  wukong-load:/home/wkdeploy/

eval "$(ssh-agent -s)"
agent_started=true
cleanup_agent() {
  if [[ "${agent_started:-false}" == true ]]; then ssh-agent -k >/dev/null 2>&1 || true; fi
}
trap cleanup_agent EXIT
ssh-add "${WK_CLOUD_SSH_KEY:?required}"

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure bundle_transfer_failed plan_validated "$role" \
    "private service host did not receive the deployment payload" "$role payload is absent or incomplete"
  cloud_ssh_retry "${role}-relay" 3 5 ssh -A -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
    "scp -o BatchMode=yes -o StrictHostKeyChecking=accept-new /home/wkdeploy/cloud-deployment-bundle.tar.gz /home/wkdeploy/deployment-plan.json /home/wkdeploy/runtime-node.tar.gz /home/wkdeploy/install-orchestrator-compat-user.sh 'wkdeploy@${address}:/home/wkdeploy/'"
done
complete_gate bundle_transferred

write_failure bundle_digest_mismatch bundle_transferred load \
  "load host rejected the immutable deployment bundle" "load bundle digest is unverified"
cloud_ssh_retry load-unpack 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'rm -rf /home/wkdeploy/bundle && mkdir /home/wkdeploy/bundle && tar -xzf /home/wkdeploy/cloud-deployment-bundle.tar.gz -C /home/wkdeploy/bundle && /home/wkdeploy/bundle/bin/wkcloudbundle verify-offline --root /home/wkdeploy/bundle >/dev/null'
for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure bundle_digest_mismatch bundle_transferred "$role" \
    "service host rejected the immutable deployment bundle" "$role bundle digest is unverified"
  cloud_ssh_retry "${role}-unpack" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    'rm -rf /home/wkdeploy/bundle /home/wkdeploy/run-secrets && mkdir /home/wkdeploy/bundle /home/wkdeploy/run-secrets && tar -xzf /home/wkdeploy/cloud-deployment-bundle.tar.gz -C /home/wkdeploy/bundle && tar -xzf /home/wkdeploy/runtime-node.tar.gz -C /home/wkdeploy/run-secrets && /home/wkdeploy/bundle/bin/wkcloudbundle verify-offline --root /home/wkdeploy/bundle >/dev/null'
done
complete_gate bundle_verified

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure credential_materialization_failed bundle_verified "$role" \
    "frozen orchestrator SSH compatibility could not be installed" "$role compatibility access is unavailable"
  cloud_ssh_retry "${role}-orchestrator-compat" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    'sudo bash /home/wkdeploy/install-orchestrator-compat-user.sh'
done
write_failure credential_materialization_failed bundle_verified load \
  "frozen orchestrator SSH compatibility could not be installed" "load compatibility access is unavailable"
cloud_ssh_retry load-orchestrator-compat 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'sudo bash /home/wkdeploy/install-orchestrator-compat-user.sh'

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure data_disk_mount_invalid bundle_verified "$role" \
    "service data disk discovery or native preparation failed" "$role is not prepared"
  data_device="$(cloud_ssh_retry_capture "${role}-data-device" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" 'root_source=$(findmnt -no SOURCE /); root_parent=$(lsblk -no PKNAME "$root_source" | head -1); test -n "$root_parent" || root_parent=$(lsblk -no NAME "$root_source" | head -1); mapfile -t candidates < <(lsblk -dpno NAME,TYPE | awk -v root="/dev/$root_parent" '\''$2=="disk" && $1!=root {print $1}'\''); ((${#candidates[@]} == 1)); printf "%s\n" "${candidates[0]}"')"
  [[ "$data_device" =~ ^/dev/[A-Za-z0-9._/-]+$ ]]
  # A repair deployment may replace binaries left running by an earlier
  # partial activation. Quiesce the known role units before install-offline
  # so the immutable bundle installer never truncates an executing inode.
  cloud_ssh_retry "${role}-quiesce" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    'sudo systemctl stop node-exporter.service wukongim.service wkbench-host-metrics.service'
  cloud_ssh_retry "${role}-prepare" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    "sudo /home/wkdeploy/bundle/bin/wkcloudhost install-offline --bundle /home/wkdeploy/bundle --plan /home/wkdeploy/deployment-plan.json --role '$role' --runtime-dir /home/wkdeploy/run-secrets --data-device '$data_device' --no-systemd"
  cloud_ssh_retry "${role}-normalize-config" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    "first=\"\$(sudo sed -n '1p' /etc/wukongim/wukongim.toml)\"; second=\"\$(sudo sed -n '2p' /etc/wukongim/wukongim.toml)\"; if test \"\$first\" = 'mode = \"release\"' && test -z \"\$second\"; then sudo sed -i '1,2d' /etc/wukongim/wukongim.toml; fi; test \"\$(sudo sed -n '1p' /etc/wukongim/wukongim.toml)\" = '[node]' && ! sudo grep -q '^mode[[:space:]]*=' /etc/wukongim/wukongim.toml; if ! sudo grep -qxF '[log]' /etc/wukongim/wukongim.toml; then sudo sh -c 'printf \"\\n[log]\\ndir = \\\"/var/lib/wukongim-cloud/logs\\\"\\n\" >> /etc/wukongim/wukongim.toml'; fi; test \"\$(sudo grep -cxF '[log]' /etc/wukongim/wukongim.toml)\" = 1 && test \"\$(sudo grep -cxF 'dir = \"/var/lib/wukongim-cloud/logs\"' /etc/wukongim/wukongim.toml)\" = 1"
done

write_failure data_disk_mount_invalid bundle_verified load \
  "load data disk discovery or native preparation failed" "load is not prepared"
load_data_device="$(cloud_ssh_retry_capture load-data-device 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'root_source=$(findmnt -no SOURCE /); root_parent=$(lsblk -no PKNAME "$root_source" | head -1); test -n "$root_parent" || root_parent=$(lsblk -no NAME "$root_source" | head -1); mapfile -t candidates < <(lsblk -dpno NAME,TYPE | awk -v root="/dev/$root_parent" '\''$2=="disk" && $1!=root {print $1}'\''); ((${#candidates[@]} == 1)); printf "%s\n" "${candidates[0]}"')"
[[ "$load_data_device" =~ ^/dev/[A-Za-z0-9._/-]+$ ]]
cloud_ssh_retry load-secrets 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'rm -rf /home/wkdeploy/run-secrets && mkdir /home/wkdeploy/run-secrets && tar -xzf /home/wkdeploy/runtime-load.tar.gz -C /home/wkdeploy/run-secrets'
cloud_ssh_retry load-quiesce 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'sudo systemctl stop node-exporter.service wkbench-host-metrics.service wkbench-worker@1.service wkbench-worker@2.service wkbench-worker@3.service wkbench-coordinator.service wkbench-formal.service wkbench-rehearsal.service prometheus.service wkanalysis.service caddy.service'
cloud_ssh_retry load-prepare 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  "sudo /home/wkdeploy/bundle/bin/wkcloudhost install-offline --bundle /home/wkdeploy/bundle --plan /home/wkdeploy/deployment-plan.json --role load --runtime-dir /home/wkdeploy/run-secrets --data-device '$load_data_device' --no-systemd"
complete_gate hosts_prepared

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure native_activation_failed hosts_prepared "$role" \
    "native service activation failed" "$role is prepared but not proven active"
  cloud_ssh_retry "${role}-activate" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    "sudo /home/wkdeploy/bundle/bin/wkcloudhost activate-offline --role '$role'"
done
write_failure native_activation_failed hosts_prepared load \
  "native load-service activation failed" "load is prepared but not proven active"
cloud_ssh_retry load-activate 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'sudo /home/wkdeploy/bundle/bin/wkcloudhost activate-offline --role load'
complete_gate services_active

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure credential_cleanup_failed services_active "$role" \
    "deployment staging credentials could not be removed" "$role services are active; staging cleanup is unconfirmed"
  cloud_ssh_retry "${role}-cleanup-secrets" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    'rm -rf /home/wkdeploy/run-secrets /home/wkdeploy/runtime-node.tar.gz /home/wkdeploy/install-orchestrator-compat-user.sh'
done
write_failure credential_cleanup_failed services_active load \
  "deployment staging credentials could not be removed" "load services are active; staging cleanup is unconfirmed"
cloud_ssh_retry cleanup-load-secrets 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'rm -rf /home/wkdeploy/run-secrets /home/wkdeploy/runtime-node.tar.gz /home/wkdeploy/runtime-load.tar.gz /home/wkdeploy/install-orchestrator-compat-user.sh'
