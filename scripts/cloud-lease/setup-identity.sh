#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 [--force] [owner/repository]" >&2
}

force=false
if [[ "${1:-}" == --force ]]; then
  force=true
  shift
fi
[[ $# -le 1 ]] || { usage; exit 2; }
repository="${1:-${GITHUB_REPOSITORY:-}}"
[[ "$repository" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]] || {
  echo 'repository must be an exact owner/name identity' >&2
  exit 2
}
command -v gh >/dev/null || { echo 'gh is required' >&2; exit 1; }
command -v jq >/dev/null || { echo 'jq is required' >&2; exit 1; }
gh auth status >/dev/null

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
"$script_dir/configure-github-identity.sh" apply "$repository" >/dev/null

setup_attempt=initial
if [[ "$force" == true ]]; then
  setup_attempt=forced
fi
setup_id="codex-$(date -u +%Y%m%dT%H%M%SZ)-$$-$setup_attempt"
gh workflow run cloud-lease-oidc-setup.yml \
  --repo "$repository" \
  --ref main \
  -f "setup_id=$setup_id" \
  -f operation=apply \
  -f "force_reconcile=$force"

run_id=""
for _ in {1..24}; do
  run_id="$(gh run list --repo "$repository" --workflow cloud-lease-oidc-setup.yml --event workflow_dispatch --limit 50 \
    --json databaseId,displayTitle \
    --jq "[.[] | select(.displayTitle == \"Cloud Lease OIDC Setup $setup_id\") | .databaseId][0] // empty")"
  [[ -n "$run_id" ]] && break
  sleep 5
done
[[ "$run_id" =~ ^[0-9]+$ ]] || {
  echo "could not resolve setup workflow run for $setup_id" >&2
  exit 1
}
if ! gh run watch "$run_id" --repo "$repository" --exit-status; then
  if [[ "$force" == false ]]; then
    echo 'Cloud Lease OIDC live check failed; retrying once through the existing AccessKey bootstrap pair.' >&2
    exec "$0" --force "$repository"
  fi
  exit 1
fi

temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
gh run download "$run_id" --repo "$repository" --name "cloud-lease-oidc-$setup_id" --dir "$temporary"
result="$temporary/cloud-lease-oidc-output.json"
jq -e '.schema == "wukongim.cloud_lease.oidc_bootstrap/v1"' "$result" >/dev/null

while IFS=$'\t' read -r name expression; do
  value="$(jq -er "$expression" "$result")"
  gh variable set "$name" --repo "$repository" --body "$value"
done <<'VARIABLES'
ALIBABA_CLOUD_LEASE_REGION	.result.region
ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH	.result.account_id_hash
ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN	.result.oidc_provider_arn
ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE	.result.oidc_audience
ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN	.result.provisioner_role_arn
ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN	.result.observer_role_arn
ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN	.result.releaser_role_arn
VARIABLES

configured="$(gh variable list --repo "$repository" --json name,value)"
for name in \
  ALIBABA_CLOUD_LEASE_REGION \
  ALIBABA_CLOUD_LEASE_ACCOUNT_ID_HASH \
  ALIBABA_CLOUD_LEASE_OIDC_PROVIDER_ARN \
  ALIBABA_CLOUD_LEASE_OIDC_AUDIENCE \
  ALIBABA_CLOUD_LEASE_PROVISIONER_ROLE_ARN \
  ALIBABA_CLOUD_LEASE_OBSERVER_ROLE_ARN \
  ALIBABA_CLOUD_LEASE_RELEASER_ROLE_ARN; do
  jq -e --arg name "$name" '[.[] | select(.name == $name and .value != "")] | length == 1' <<<"$configured" >/dev/null
done

jq -n \
  --arg repository "$repository" \
  --arg setup_id "$setup_id" \
  --argjson run_id "$run_id" \
  '{repository:$repository,setup_id:$setup_id,workflow_run_id:$run_id,status:"verified"}'
