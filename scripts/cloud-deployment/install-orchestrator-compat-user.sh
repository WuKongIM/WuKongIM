#!/usr/bin/env bash
set -euo pipefail

[[ "$EUID" -eq 0 ]]

source_user=wkdeploy
compat_user=wukong
source_home=/home/wkdeploy
compat_home=/home/wukong
source_keys="$source_home/.ssh/authorized_keys"
compat_keys="$compat_home/.ssh/authorized_keys"
sudoers_path=/etc/sudoers.d/91-wukong-orchestrator
plan_path=/home/wkdeploy/deployment-plan.json
frozen_orchestrator_control=4daf86e4a88478ccdecd9675acee8414810413be

[[ -f "$plan_path" && ! -L "$plan_path" ]]
control_sha="$(jq -er '.control_sha | select(test("^[0-9a-f]{40}$"))' "$plan_path")"
[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0

source_record="$(getent passwd "$source_user")"
[[ -n "$source_record" && "$(cut -d: -f6 <<<"$source_record")" == "$source_home" ]]
[[ -f "$source_keys" && ! -L "$source_keys" ]]
awk '
  NF >= 2 && $1 == "ssh-ed25519" && length($2) >= 68 { valid++ }
  END { exit !(NR == 2 && valid == 2) }
' "$source_keys"

if compat_record="$(getent passwd "$compat_user")"; then
  [[ "$(cut -d: -f6 <<<"$compat_record")" == "$compat_home" ]]
  [[ "$(cut -d: -f7 <<<"$compat_record")" == /bin/bash ]]
  [[ "$(cut -d: -f3 <<<"$compat_record")" != 0 ]]
else
  useradd --create-home --home-dir "$compat_home" --shell /bin/bash "$compat_user"
fi
usermod --lock "$compat_user"

install -d -o "$compat_user" -g "$compat_user" -m 0750 "$compat_home"
install -d -o "$compat_user" -g "$compat_user" -m 0700 "$compat_home/.ssh"
[[ ! -L "$compat_keys" ]]
install -o "$compat_user" -g "$compat_user" -m 0600 "$source_keys" "$compat_keys"
[[ "$(sha256sum "$source_keys" | awk '{print $1}')" == "$(sha256sum "$compat_keys" | awk '{print $1}')" ]]

umask 077
temporary="$(mktemp /etc/sudoers.d/.wukong-orchestrator.XXXXXX)"
cleanup() { rm -f "$temporary"; }
trap cleanup EXIT
printf '%s\n' 'wukong ALL=(ALL) NOPASSWD:ALL' >"$temporary"
chown root:root "$temporary"
chmod 0440 "$temporary"
/usr/sbin/visudo -cf "$temporary" >/dev/null
mv "$temporary" "$sudoers_path"
trap - EXIT
/usr/sbin/visudo -cf "$sudoers_path" >/dev/null

[[ "$(stat -c '%U:%G:%a' "$compat_home/.ssh")" == "$compat_user:$compat_user:700" ]]
[[ "$(stat -c '%U:%G:%a' "$compat_keys")" == "$compat_user:$compat_user:600" ]]
[[ "$(stat -c '%U:%G:%a' "$sudoers_path")" == root:root:440 ]]
