#!/usr/bin/env bash
set -euo pipefail

: "${WK_CHAT_LAB_REQUEST_DIR:?required}"
: "${WK_CHAT_LAB_GENERATION_DIR:?required}"
: "${WK_CHAT_LAB_GENERATION:?required}"
: "${WK_CHAT_LAB_SOURCE_SHA:?required}"
: "${WK_CHAT_LAB_GATE_TOOL:?required}"
: "${WK_CHAT_LAB_BUNDLE_TOOL:?required}"

request_dir="$WK_CHAT_LAB_REQUEST_DIR"
generation_dir="$WK_CHAT_LAB_GENERATION_DIR"
generation="$WK_CHAT_LAB_GENERATION"
source_sha="$WK_CHAT_LAB_SOURCE_SHA"
[[ "$generation" =~ ^[1-9][0-9]*$ && "$source_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$request_dir" == /* && "$generation_dir" == "$request_dir"/generations/"$generation" ]]
for input in "$request_dir/receipt.json" "$request_dir/deployment_ed25519.pub" \
  "$request_dir/diagnostic_ed25519.pub" "$generation_dir/bundle/cloud-deployment-bundle.tar.gz"; do
  [[ -f "$input" && ! -L "$input" ]]
done
for tool in jq openssl htpasswd tar; do
  command -v "$tool" >/dev/null 2>&1 || { echo "required runtime preparation tool is unavailable: $tool" >&2; exit 1; }
done
[[ -x "$WK_CHAT_LAB_GATE_TOOL" && -x "$WK_CHAT_LAB_BUNDLE_TOOL" ]]

umask 077
bundle_root="$generation_dir/bundle-root"
runtime_node="$generation_dir/runtime-node"
runtime_load="$generation_dir/runtime-load"
install -d -m 0700 "$bundle_root" "$runtime_node" "$runtime_load"
tar -xpzf "$generation_dir/bundle/cloud-deployment-bundle.tar.gz" -C "$bundle_root"
"$WK_CHAT_LAB_BUNDLE_TOOL" verify-offline --root "$bundle_root" >"$generation_dir/bundle-verification.json"
jq -e --arg source "$source_sha" '.source_sha == $source and (.bundle_digest | test("^sha256:[0-9a-f]{64}$"))' \
  "$generation_dir/bundle-verification.json" >/dev/null

"$WK_CHAT_LAB_GATE_TOOL" deployment-plan \
  --lease-receipt "$request_dir/receipt.json" \
  --bundle-manifest "$bundle_root/bundle-manifest.json" \
  --bootstrap-pubkey "$(<"$request_dir/deployment_ed25519.pub")" \
  --bootstrap-pubkey "$(<"$request_dir/diagnostic_ed25519.pub")" \
  --purpose repair --generation "$generation" \
  >"$generation_dir/deployment-plan.json"
jq -e --arg source "$source_sha" --argjson generation "$generation" '
  .schema == "wukongim.cloud_deployment.plan/v2" and .purpose == "repair" and
  .source_sha == $source and .generation == $generation and
  .topology.physical_hash_slots == 256 and .topology.logical_slot_groups == 12 and
  (.hosts | length) == 4
' "$generation_dir/deployment-plan.json" >/dev/null

manager_user="operator-$(openssl rand -hex 12)"
manager_password="$(openssl rand -hex 32)"
analysis_password="$(openssl rand -hex 32)"
analysis_token="$(openssl rand -hex 32)"
manager_jwt="$(openssl rand -hex 32)"
worker_token="$(openssl rand -hex 32)"
bench_token="$(openssl rand -hex 32)"
demo_user="$manager_user"
demo_password="$manager_password"
demo_hash="$(htpasswd -bnBC 14 "$demo_user" "$demo_password" | sed -n 's/^[^:]*://p')"
[[ "$demo_hash" == \$2* ]]
users="$(jq -cn --arg manager_user "$manager_user" --arg manager_password "$manager_password" \
  --arg analysis_password "$analysis_password" '[
  {username:$manager_user,password:$manager_password,permissions:[{resource:"*",actions:["r"]}]},
  {username:"analysis",password:$analysis_password,permissions:[
    {resource:"cluster.node",actions:["r"]},{resource:"cluster.slot",actions:["r"]},
    {resource:"cluster.controller",actions:["r"]},{resource:"cluster.diagnostics",actions:["r","w"]},
    {resource:"cluster.log",actions:["r"]}]}]')"

plan="$generation_dir/deployment-plan.json"
expires_at="$(jq -er .expires_at "$plan")"
created_at="$(jq -er .lease_created_at "$plan")"
budget_limit="$(jq -er .budget.limit_micros "$plan")"
budget_stop="$(jq -er .budget.operational_stop_micros "$plan")"
budget_committed="$(jq -er .budget.committed_micros "$plan")"
budget_estimated="$(jq -er .budget.estimated_cost_micros "$plan")"
budget_line_items="$(jq -er '.budget.line_items | @base64' "$plan")"
load_public="$(jq -er '.hosts[] | select(.role=="load") | .public_address' "$plan")"
lease_id="$(jq -er .lease_id "$plan")"
provider="$(jq -er .provider "$plan")"
region="$(jq -er .region "$plan")"

printf "WK_MANAGER_JWT_SECRET=%s\nWK_MANAGER_USERS='%s'\nWK_BENCH_API_TOKEN=%s\n" \
  "$manager_jwt" "$users" "$bench_token" >"$runtime_node/node.env"
printf "WK_BENCH_WORKER_TOKEN=%s\nWK_BENCH_API_TOKEN=%s\nWK_DEMO_BASIC_AUTH_USER=%s\nWK_DEMO_BASIC_AUTH_HASH='%s'\nWK_CHAT_LEASE_CREATED_AT=%s\nWK_CHAT_LEASE_EXPIRES_AT=%s\nWK_CHAT_BUDGET_LIMIT_MICROS=%s\nWK_CHAT_BUDGET_OPERATIONAL_STOP_MICROS=%s\nWK_CHAT_BUDGET_COMMITTED_MICROS=%s\nWK_CHAT_BUDGET_ESTIMATED_MICROS=%s\nWK_CHAT_BUDGET_LINE_ITEMS_BASE64=%s\nWK_CHAT_RUNTIME_ENVELOPE=direct_repair\n" \
  "$worker_token" "$bench_token" "$demo_user" "$demo_hash" "$created_at" "$expires_at" \
  "$budget_limit" "$budget_stop" "$budget_committed" "$budget_estimated" "$budget_line_items" \
  >"$runtime_load/load.env"

openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 5 -subj "/CN=${load_public}" \
  -addext "subjectAltName=IP:${load_public},IP:127.0.0.1" \
  -keyout "$runtime_load/analysis-key.pem" -out "$runtime_load/analysis-cert.pem" >/dev/null 2>&1
node_urls="$(jq -cn \
  --arg one "http://$(jq -r '.hosts[]|select(.role=="service-1")|.private_address' "$plan"):5001" \
  --arg two "http://$(jq -r '.hosts[]|select(.role=="service-2")|.private_address' "$plan"):5001" \
  --arg three "http://$(jq -r '.hosts[]|select(.role=="service-3")|.private_address' "$plan"):5001" \
  '{"1":$one,"2":$two,"3":$three}')"
cat >"$runtime_load/analysis.env" <<EOF
WK_ANALYSIS_RUN_ID=$lease_id
WK_ANALYSIS_RUN_STATE=running
WK_ANALYSIS_RUN_EXPIRES_AT=$expires_at
WK_ANALYSIS_PROVIDER=$provider
WK_ANALYSIS_REGION=$region
WK_ANALYSIS_SOURCE_SHA=$source_sha
WK_ANALYSIS_SCENARIO_PATH=/etc/wukongim/analysis-scenario.yaml
WK_ANALYSIS_MANAGER_BASE_URL=http://$(jq -r '.hosts[]|select(.role=="service-1")|.private_address' "$plan"):5301
WK_ANALYSIS_MANAGER_USERNAME=analysis
WK_ANALYSIS_MANAGER_PASSWORD=$analysis_password
WK_ANALYSIS_PROMETHEUS_BASE_URL=http://127.0.0.1:9090
WK_ANALYSIS_NODE_API_URLS='$node_urls'
WK_ANALYSIS_WORKLOAD_REPORT_DIR=/var/lib/wukongim-cloud/reports
WK_ANALYSIS_LISTEN_ADDR=0.0.0.0:19444
WK_ANALYSIS_MCP_TOKEN=$analysis_token
WK_ANALYSIS_GITHUB_OIDC_ENABLED=false
EOF
chmod 0600 "$runtime_node/node.env" "$runtime_load"/*
tar -czf "$generation_dir/runtime-node.tar.gz" -C "$runtime_node" .
tar -czf "$generation_dir/runtime-load.tar.gz" -C "$runtime_load" .
printf 'export WK_CLOUD_MANAGER_USER=%q\nexport WK_CLOUD_MANAGER_PASSWORD=%q\nexport WK_CLOUD_DEMO_USER=%q\nexport WK_CLOUD_DEMO_PASSWORD=%q\n' \
  "$manager_user" "$manager_password" "$demo_user" "$demo_password" >"$generation_dir/readiness-credentials"
chmod 0600 "$generation_dir/runtime-node.tar.gz" "$generation_dir/runtime-load.tar.gz" \
  "$generation_dir/readiness-credentials" "$generation_dir/deployment-plan.json"

plan_digest="$(jq -er .plan_digest "$plan")"
jq -n --arg schema 'wukongim.chat_lifecycle.access/v1' --arg request_id "$(jq -er .request_id "$plan")" \
  --arg lease_id "$lease_id" --arg source_sha "$source_sha" --arg deployment_plan_digest "$plan_digest" \
  --arg manager_url "http://${load_public}/" --arg demo_url "http://${load_public}/demo/" \
  --arg username "$manager_user" --arg password "$manager_password" --arg lease_expires_at "$expires_at" \
  '{schema:$schema,request_id:$request_id,lease_id:$lease_id,source_sha:$source_sha,
    deployment_plan_digest:$deployment_plan_digest,manager_url:$manager_url,demo_url:$demo_url,
    username:$username,password:$password,lease_expires_at:$lease_expires_at}' \
  >"$request_dir/access.json"
chmod 0600 "$request_dir/access.json"
jq -n --arg schema 'wukongim.cloud_deployment.local_runtime/v1' --arg source_sha "$source_sha" \
  --argjson generation "$generation" --arg plan_digest "$plan_digest" \
  '{schema:$schema,source_sha:$source_sha,generation:$generation,deployment_plan_digest:$plan_digest,credentials_retained_locally:true}'
