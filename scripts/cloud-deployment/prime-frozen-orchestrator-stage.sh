#!/usr/bin/env bash
set -euo pipefail

[[ "$EUID" -eq 0 ]]

plan_path=/home/wkdeploy/deployment-plan.json
frozen_orchestrator_control=4daf86e4a88478ccdecd9675acee8414810413be

[[ -f "$plan_path" && ! -L "$plan_path" ]]
control_sha="$(jq -er '.control_sha | select(test("^[0-9a-f]{40}$"))' "$plan_path")"
[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0

stage="$(jq -er '.lease_id | capture("-(?<stage>rehearsal|formal)-[1-9][0-9]*$").stage' "$plan_path")"
unit="wkbench-${stage}.service"
unit_path="/etc/systemd/system/${unit}"
dropin_dir="/run/systemd/system/${unit}.d"
dropin_path="${dropin_dir}/90-frozen-orchestrator-reset-prime.conf"

[[ -f "$unit_path" && ! -L "$unit_path" ]]
[[ "$(systemctl show "$unit" --property=FragmentPath --value)" == "$unit_path" ]]
if systemctl is-active --quiet "$unit"; then
  echo "refusing to prime an active stage service" >&2
  exit 1
fi
if systemctl is-failed --quiet "$unit"; then
  exit 0
fi

install -d -o root -g root -m 0755 "$dropin_dir"
[[ ! -e "$dropin_path" && ! -L "$dropin_path" ]]
cleanup() {
  rm -f "$dropin_path"
  rmdir "$dropin_dir" 2>/dev/null || true
  systemctl daemon-reload >/dev/null 2>&1 || true
}
trap cleanup EXIT
cat >"$dropin_path" <<'EOF'
[Service]
ExecStartPre=
ExecStart=
ExecStart=/bin/false
EOF
chown root:root "$dropin_path"
chmod 0644 "$dropin_path"
systemctl daemon-reload

# Type=simple may report a successful start job before /bin/false exits. The
# resulting unit state, rather than the synchronous start exit code, is the
# compatibility contract consumed by the frozen orchestrator.
systemctl start "$unit" >/dev/null 2>&1 || true
primed=false
for ((probe = 0; probe < 50; probe++)); do
  if systemctl is-failed --quiet "$unit"; then
    primed=true
    break
  fi
  sleep 0.1
done
[[ "$primed" == true ]]

rm -f "$dropin_path"
rmdir "$dropin_dir" 2>/dev/null || true
systemctl daemon-reload
trap - EXIT

[[ "$(systemctl show "$unit" --property=FragmentPath --value)" == "$unit_path" ]]
systemctl is-failed --quiet "$unit"
! systemctl cat "$unit" | grep -q '90-frozen-orchestrator-reset-prime.conf'
