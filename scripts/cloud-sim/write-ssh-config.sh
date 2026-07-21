#!/usr/bin/env bash
set -euo pipefail

: "${WK_CLOUD_SIM_PUBLIC_IP:?required}"
: "${WK_CLOUD_NODE1_IP:?required}"
: "${WK_CLOUD_NODE2_IP:?required}"
: "${WK_CLOUD_NODE3_IP:?required}"
: "${WK_CLOUD_SSH_KEY:?required}"
: "${WK_CLOUD_SSH_CONFIG:?required}"

for address in "$WK_CLOUD_SIM_PUBLIC_IP" "$WK_CLOUD_NODE1_IP" "$WK_CLOUD_NODE2_IP" "$WK_CLOUD_NODE3_IP"; do
  [[ "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || {
    echo "invalid SSH host address: $address" >&2
    exit 1
  }
done
[[ -f "$WK_CLOUD_SSH_KEY" ]] || {
  echo "SSH identity file not found: $WK_CLOUD_SSH_KEY" >&2
  exit 1
}

key_dir="$(cd "$(dirname "$WK_CLOUD_SSH_KEY")" && pwd -P)"
key_path="${key_dir}/$(basename "$WK_CLOUD_SSH_KEY")"
output_dir="$(dirname "$WK_CLOUD_SSH_CONFIG")"
[[ -d "$output_dir" ]] || {
  echo "SSH config directory not found: $output_dir" >&2
  exit 1
}

umask 077
temporary="${WK_CLOUD_SSH_CONFIG}.tmp.$$"
trap 'rm -f "$temporary"' EXIT
cat >"$temporary" <<EOF
Host wukong-sim-jump
  HostName $WK_CLOUD_SIM_PUBLIC_IP
  User wukong
  IdentityFile "$key_path"
  IdentitiesOnly yes
  BatchMode yes
  ConnectTimeout 10
  ConnectionAttempts 1
  ServerAliveInterval 15
  ServerAliveCountMax 3
  StrictHostKeyChecking accept-new

Host $WK_CLOUD_NODE1_IP $WK_CLOUD_NODE2_IP $WK_CLOUD_NODE3_IP
  User wukong
  IdentityFile "$key_path"
  IdentitiesOnly yes
  BatchMode yes
  ConnectTimeout 10
  ConnectionAttempts 1
  ServerAliveInterval 15
  ServerAliveCountMax 3
  StrictHostKeyChecking accept-new
  ProxyJump wukong-sim-jump
EOF
chmod 0600 "$temporary"
mv "$temporary" "$WK_CLOUD_SSH_CONFIG"
trap - EXIT
