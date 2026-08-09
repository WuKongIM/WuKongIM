#!/usr/bin/env bash
set -euo pipefail

[[ "$EUID" -eq 0 ]]

plan_path=/home/wkdeploy/deployment-plan.json
frozen_orchestrator_control=4daf86e4a88478ccdecd9675acee8414810413be
wrapper=/opt/wukongim/scripts/run-frozen-stage-after-process-evidence.sh

[[ -f "$plan_path" && ! -L "$plan_path" ]]
control_sha="$(jq -er '.control_sha | select(test("^[0-9a-f]{40}$"))' "$plan_path")"
[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0
stage="$(jq -er '.lease_id | capture("-(?<stage>rehearsal|formal)-[1-9][0-9]*$").stage' "$plan_path")"
unit="wkbench-${stage}.service"
unit_path="/etc/systemd/system/${unit}"
dropin_dir="/etc/systemd/system/${unit}.d"
dropin_path="${dropin_dir}/91-frozen-stage-process-evidence.conf"

case "$stage" in
  rehearsal) expected_unit_sha256=5f2b8469d3f027cc693d9ce15f60a38006b677a86e24c483e07b55440a209fde ;;
  formal) expected_unit_sha256=23598d4a8f2d76a7abbf3b211b1dd61be47f57a27773f9d4619b730569289df2 ;;
  *) exit 1 ;;
esac

[[ -f "$unit_path" && ! -L "$unit_path" ]]
[[ "$(sha256sum "$unit_path" | awk '{print $1}')" == "$expected_unit_sha256" ]]
[[ "$(systemctl show "$unit" --property=FragmentPath --value)" == "$unit_path" ]]

wrapper_temporary="$(mktemp /opt/wukongim/scripts/.run-frozen-stage.XXXXXX)"
dropin_temporary="$(mktemp /etc/systemd/system/.frozen-stage-process-evidence.XXXXXX)"
cleanup() { rm -f "$wrapper_temporary" "$dropin_temporary"; }
trap cleanup EXIT

cat >"$wrapper_temporary" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail

[[ "$#" -eq 1 ]]
stage="$1"
case "$stage" in
  rehearsal)
    config=/etc/wukongim/chat-lifecycle-rehearsal.yaml
    output_dir=/var/lib/wukongim-cloud/reports/rehearsal
    command=(/opt/wukongim/bin/wkbench soak chat-lifecycle --config "$config" --output-dir "$output_dir")
    ;;
  formal)
    config=/etc/wukongim/chat-lifecycle.yaml
    output_dir=/var/lib/wukongim-cloud/reports
    command=(/opt/wukongim/bin/wkbench formal-chain chat-lifecycle --config "$config" --output-dir "$output_dir")
    ;;
  *) exit 1 ;;
esac

stage_unit="wkbench-${stage}.service"
load_host_metrics="$(sed -n 's/^  load_host_metrics:.*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")"
[[ -n "$load_host_metrics" ]]
deadline=$(( $(date -u +%s) + 120 ))
while (( $(date -u +%s) < deadline )); do
  if curl --fail --silent --show-error --max-time 5 "http://${load_host_metrics}/metrics" | awk -v unit="$stage_unit" '
    $1 == "wukongim_process_up{unit=\"" unit "\"}" && NF == 2 && $2 == "1" { up++ }
    $1 == "wukongim_process_cpu_jiffies_total{unit=\"" unit "\"}" && NF == 2 && $2 ~ /^[0-9]+$/ { cpu++ }
    $1 == "wukongim_process_resident_memory_bytes{unit=\"" unit "\"}" && NF == 2 && $2 ~ /^[0-9]+$/ && $2 + 0 > 0 { memory++ }
    END { exit !(up == 1 && cpu == 1 && memory == 1) }
  '; then
    exec "${command[@]}"
  fi
  sleep 2
done
exit 1
EOF
chown root:root "$wrapper_temporary"
chmod 0755 "$wrapper_temporary"

cat >"$dropin_temporary" <<EOF
[Service]
ExecStart=
ExecStart=${wrapper} ${stage}
EOF
chown root:root "$dropin_temporary"
chmod 0644 "$dropin_temporary"

desired_wrapper_sha256="$(sha256sum "$wrapper_temporary" | awk '{print $1}')"
desired_dropin_sha256="$(sha256sum "$dropin_temporary" | awk '{print $1}')"
if [[ -e "$wrapper" || -L "$wrapper" ]]; then
  [[ -f "$wrapper" && ! -L "$wrapper" ]]
  [[ "$(stat -c '%U:%G:%a' "$wrapper")" == root:root:755 ]]
  [[ "$(sha256sum "$wrapper" | awk '{print $1}')" == "$desired_wrapper_sha256" ]]
fi
if [[ -e "$dropin_path" || -L "$dropin_path" ]]; then
  [[ -f "$dropin_path" && ! -L "$dropin_path" ]]
  [[ "$(stat -c '%U:%G:%a' "$dropin_path")" == root:root:644 ]]
  [[ "$(sha256sum "$dropin_path" | awk '{print $1}')" == "$desired_dropin_sha256" ]]
fi

install -d -o root -g root -m 0755 "$dropin_dir"
mv "$wrapper_temporary" "$wrapper"
mv "$dropin_temporary" "$dropin_path"
trap - EXIT
systemctl daemon-reload

[[ "$(sha256sum "$wrapper" | awk '{print $1}')" == "$desired_wrapper_sha256" ]]
[[ "$(sha256sum "$dropin_path" | awk '{print $1}')" == "$desired_dropin_sha256" ]]
[[ "$(systemctl show "$unit" --property=FragmentPath --value)" == "$unit_path" ]]
systemctl cat "$unit" | grep -qF "ExecStart=${wrapper} ${stage}"
