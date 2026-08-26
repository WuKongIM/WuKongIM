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
known_hosts="${WK_CLOUD_SSH_KNOWN_HOSTS:-${WK_CLOUD_SSH_CONFIG}.known_hosts}"
known_hosts_dir="$(dirname "$known_hosts")"
[[ -d "$known_hosts_dir" && ! -L "$known_hosts_dir" && "$known_hosts" != *$'\n'* && "$known_hosts" != *'"'* ]] || {
  echo "invalid deployment known-hosts path" >&2
  exit 1
}
if [[ -e "$known_hosts" || -L "$known_hosts" ]]; then
  [[ -f "$known_hosts" && ! -L "$known_hosts" ]] || {
    echo "deployment known-hosts path is not a regular file" >&2
    exit 1
  }
  chmod 0600 "$known_hosts"
else
  install -m 0600 /dev/null "$known_hosts"
fi
known_hosts_dir="$(cd "$known_hosts_dir" && pwd -P)"
known_hosts="${known_hosts_dir}/$(basename "$known_hosts")"
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
  UserKnownHostsFile "$known_hosts"

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
  UserKnownHostsFile "$known_hosts"
  ProxyJump wukong-load
EOF
chmod 0600 "$temporary"
mv "$temporary" "$WK_CLOUD_SSH_CONFIG"
trap - EXIT
