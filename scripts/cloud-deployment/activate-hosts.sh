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
  wukong-load:/home/wukong/

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
    "scp -o BatchMode=yes -o StrictHostKeyChecking=accept-new /home/wukong/cloud-deployment-bundle.tar.gz /home/wukong/deployment-plan.json /home/wukong/runtime-node.tar.gz 'wukong@${address}:/home/wukong/'"
done
complete_gate bundle_transferred

write_failure bundle_digest_mismatch bundle_transferred load \
  "load host rejected the immutable deployment bundle" "load bundle digest is unverified"
cloud_ssh_retry load-unpack 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'rm -rf /home/wukong/bundle && mkdir /home/wukong/bundle && tar -xzf /home/wukong/cloud-deployment-bundle.tar.gz -C /home/wukong/bundle && /home/wukong/bundle/bin/wkcloudbundle verify-offline --root /home/wukong/bundle >/dev/null'
for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure bundle_digest_mismatch bundle_transferred "$role" \
    "service host rejected the immutable deployment bundle" "$role bundle digest is unverified"
  cloud_ssh_retry "${role}-unpack" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    'rm -rf /home/wukong/bundle /home/wukong/run-secrets && mkdir /home/wukong/bundle /home/wukong/run-secrets && tar -xzf /home/wukong/cloud-deployment-bundle.tar.gz -C /home/wukong/bundle && tar -xzf /home/wukong/runtime-node.tar.gz -C /home/wukong/run-secrets && /home/wukong/bundle/bin/wkcloudbundle verify-offline --root /home/wukong/bundle >/dev/null'
done
complete_gate bundle_verified

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure data_disk_mount_invalid bundle_verified "$role" \
    "service data disk discovery or native preparation failed" "$role is not prepared"
  data_device="$(cloud_ssh_retry_capture "${role}-data-device" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" 'root_source=$(findmnt -no SOURCE /); root_parent=$(lsblk -no PKNAME "$root_source" | head -1); test -n "$root_parent" || root_parent=$(lsblk -no NAME "$root_source" | head -1); mapfile -t candidates < <(lsblk -dpno NAME,TYPE | awk -v root="/dev/$root_parent" '\''$2=="disk" && $1!=root {print $1}'\''); ((${#candidates[@]} == 1)); printf "%s\n" "${candidates[0]}"')"
  [[ "$data_device" =~ ^/dev/[A-Za-z0-9._/-]+$ ]]
  cloud_ssh_retry "${role}-prepare" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    "sudo /home/wukong/bundle/bin/wkcloudhost install-offline --bundle /home/wukong/bundle --plan /home/wukong/deployment-plan.json --role '$role' --runtime-dir /home/wukong/run-secrets --data-device '$data_device' --no-systemd"
done

write_failure data_disk_mount_invalid bundle_verified load \
  "load data disk discovery or native preparation failed" "load is not prepared"
load_data_device="$(cloud_ssh_retry_capture load-data-device 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load 'root_source=$(findmnt -no SOURCE /); root_parent=$(lsblk -no PKNAME "$root_source" | head -1); test -n "$root_parent" || root_parent=$(lsblk -no NAME "$root_source" | head -1); mapfile -t candidates < <(lsblk -dpno NAME,TYPE | awk -v root="/dev/$root_parent" '\''$2=="disk" && $1!=root {print $1}'\''); ((${#candidates[@]} == 1)); printf "%s\n" "${candidates[0]}"')"
[[ "$load_data_device" =~ ^/dev/[A-Za-z0-9._/-]+$ ]]
cloud_ssh_retry load-secrets 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'rm -rf /home/wukong/run-secrets && mkdir /home/wukong/run-secrets && tar -xzf /home/wukong/runtime-load.tar.gz -C /home/wukong/run-secrets'
cloud_ssh_retry load-prepare 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  "sudo /home/wukong/bundle/bin/wkcloudhost install-offline --bundle /home/wukong/bundle --plan /home/wukong/deployment-plan.json --role load --runtime-dir /home/wukong/run-secrets --data-device '$load_data_device' --no-systemd"
complete_gate hosts_prepared

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure native_activation_failed hosts_prepared "$role" \
    "native service activation failed" "$role is prepared but not proven active"
  cloud_ssh_retry "${role}-activate" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    "sudo /home/wukong/bundle/bin/wkcloudhost activate-offline --role '$role'"
done
write_failure native_activation_failed hosts_prepared load \
  "native load-service activation failed" "load is prepared but not proven active"
cloud_ssh_retry load-activate 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'sudo /home/wukong/bundle/bin/wkcloudhost activate-offline --role load'
complete_gate services_active

for pair in "service-1:$service1" "service-2:$service2" "service-3:$service3"; do
  role="${pair%%:*}"
  address="${pair#*:}"
  write_failure credential_cleanup_failed services_active "$role" \
    "deployment staging credentials could not be removed" "$role services are active; staging cleanup is unconfirmed"
  cloud_ssh_retry "${role}-cleanup-secrets" 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" "$address" \
    'rm -rf /home/wukong/run-secrets /home/wukong/runtime-node.tar.gz'
done
write_failure credential_cleanup_failed services_active load \
  "deployment staging credentials could not be removed" "load services are active; staging cleanup is unconfirmed"
cloud_ssh_retry cleanup-load-secrets 3 5 ssh -F "$WK_CLOUD_SSH_CONFIG" wukong-load \
  'rm -rf /home/wukong/run-secrets /home/wukong/runtime-node.tar.gz /home/wukong/runtime-load.tar.gz'
