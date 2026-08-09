#!/usr/bin/env bash
set -euo pipefail

[[ "$EUID" -eq 0 ]]

plan_path=/home/wkdeploy/deployment-plan.json
target=/opt/wukongim/scripts/wait-coordinator-dependencies.sh
frozen_orchestrator_control=4daf86e4a88478ccdecd9675acee8414810413be
legacy_sha256=b3a93b9f5f0ca88462ea9f77e910afdc8601c8ea24b4e1fe52916d416907118c
authenticated_sha256=7624a9237b0d40583eedd4447a01714b312cd1e957561e1f55e74fe424f7836b
prestart_process_wait_sha256=5d9c417ddb91a670a8336e775e93a064aca8f95b332b0876a8932d2ebf2ab6ed

[[ -f "$plan_path" && ! -L "$plan_path" ]]
control_sha="$(jq -er '.control_sha | select(test("^[0-9a-f]{40}$"))' "$plan_path")"
[[ "$control_sha" == "$frozen_orchestrator_control" ]] || exit 0
jq -e '.lease_id | test("-(rehearsal|formal)-[1-9][0-9]*$")' "$plan_path" >/dev/null

[[ -f "$target" && ! -L "$target" ]]
[[ "$(stat -c '%U:%G:%a' "$target")" == root:root:755 ]]
temporary="$(mktemp /opt/wukongim/scripts/.wait-coordinator-dependencies.XXXXXX)"
cleanup() { rm -f "$temporary"; }
trap cleanup EXIT
cat >"$temporary" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
config="${WK_CHAT_LIFECYCLE_CONFIG:-/etc/wukongim/chat-lifecycle.yaml}"
[[ "${WK_BENCH_WORKER_TOKEN:-}" =~ ^[0-9a-f]{64}$ ]]
mapfile -t services < <(sed -n '/^  service_nodes:/,/^  workers:/ s/.*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")
mapfile -t host_metrics < <(sed -n '/^  host_metrics:/,/^  load_host_metrics:/ s/^    - .*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")
load_host_metrics="$(sed -n 's/^  load_host_metrics:.*address: "http:\/\/\([^"]*\)".*/\1/p' "$config")"
((${#services[@]} == 3 && ${#host_metrics[@]} == 3))
[[ -n "$load_host_metrics" ]]
deadline=$(( $(date -u +%s) + 900 ))
while (( $(date -u +%s) < deadline )); do
  ready=true
  for address in "${services[@]}"; do
    curl --fail --silent --show-error --max-time 5 "http://${address}/readyz" >/dev/null || ready=false
  done
  for address in "${host_metrics[@]}"; do
    curl --fail --silent --show-error --max-time 5 "http://${address}/healthz" >/dev/null || ready=false
  done
  curl --fail --silent --show-error --max-time 5 "http://${load_host_metrics}/healthz" >/dev/null || ready=false
  for port in 19091 19092 19093; do
    curl --fail --silent --show-error --max-time 5 -H "Authorization: Bearer ${WK_BENCH_WORKER_TOKEN}" "http://127.0.0.1:${port}/healthz" >/dev/null || ready=false
  done
  curl --fail --silent --show-error --max-time 5 http://127.0.0.1:9090/-/ready >/dev/null || ready=false
  if [[ "$ready" == true ]]; then
    exit 0
  fi
  sleep 5
done
exit 1
EOF
chown root:root "$temporary"
chmod 0755 "$temporary"

target_sha256="$(sha256sum "$target" | awk '{print $1}')"
desired_sha256="$(sha256sum "$temporary" | awk '{print $1}')"
if [[ "$target_sha256" == "$desired_sha256" ]]; then
  exit 0
fi
[[ "$target_sha256" == "$legacy_sha256" || "$target_sha256" == "$authenticated_sha256" || "$target_sha256" == "$prestart_process_wait_sha256" ]]
mv "$temporary" "$target"
trap - EXIT
[[ "$(sha256sum "$target" | awk '{print $1}')" == "$desired_sha256" ]]
[[ "$(stat -c '%U:%G:%a' "$target")" == root:root:755 ]]
