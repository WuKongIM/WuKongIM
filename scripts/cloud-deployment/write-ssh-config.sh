#!/usr/bin/env bash
set -euo pipefail

: "${WK_CLOUD_LOAD_PUBLIC_IP:?required}"
: "${WK_CLOUD_SERVICE1_IP:?required}"
: "${WK_CLOUD_SERVICE2_IP:?required}"
: "${WK_CLOUD_SERVICE3_IP:?required}"
: "${WK_CLOUD_SSH_KEY:?required}"
: "${WK_CLOUD_SSH_CONFIG:?required}"

for address in "$WK_CLOUD_LOAD_PUBLIC_IP" "$WK_CLOUD_SERVICE1_IP" "$WK_CLOUD_SERVICE2_IP" "$WK_CLOUD_SERVICE3_IP"; do
  [[ "$address" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || {
    echo "invalid deployment SSH address" >&2
    exit 1
  }
done
[[ -f "$WK_CLOUD_SSH_KEY" ]] || {
  echo "deployment SSH identity is missing" >&2
  exit 1
}

key_dir="$(cd "$(dirname "$WK_CLOUD_SSH_KEY")" && pwd -P)"
key_path="${key_dir}/$(basename "$WK_CLOUD_SSH_KEY")"
umask 077
temporary="${WK_CLOUD_SSH_CONFIG}.tmp.$$"
trap 'rm -f "$temporary"' EXIT
cat >"$temporary" <<EOF
Host wukong-load
  HostName $WK_CLOUD_LOAD_PUBLIC_IP
  User wkdeploy
  IdentityFile "$key_path"
  IdentitiesOnly yes
  BatchMode yes
  ConnectTimeout 10
  ConnectionAttempts 1
  ServerAliveInterval 15
  ServerAliveCountMax 3
  StrictHostKeyChecking accept-new

Host $WK_CLOUD_SERVICE1_IP $WK_CLOUD_SERVICE2_IP $WK_CLOUD_SERVICE3_IP
  User wkdeploy
  IdentityFile "$key_path"
  IdentitiesOnly yes
  BatchMode yes
  ConnectTimeout 10
  ConnectionAttempts 1
  ServerAliveInterval 15
  ServerAliveCountMax 3
  StrictHostKeyChecking accept-new
  ProxyJump wukong-load
EOF
chmod 0600 "$temporary"
mv "$temporary" "$WK_CLOUD_SSH_CONFIG"
trap - EXIT
