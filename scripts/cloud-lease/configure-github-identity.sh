#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 plan|apply [owner/repository]" >&2
}

operation="${1:-}"
repository="${2:-${GITHUB_REPOSITORY:-}}"
case "$operation" in
  plan|apply) ;;
  *) usage; exit 2 ;;
esac
[[ "$repository" =~ ^[^/[:space:]]+/[^/[:space:]]+$ ]] || {
  echo 'repository must be an exact owner/name identity' >&2
  exit 2
}
command -v gh >/dev/null || { echo 'gh is required' >&2; exit 1; }
command -v jq >/dev/null || { echo 'jq is required' >&2; exit 1; }
gh auth status >/dev/null

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT
api_version='2026-03-10'
oidc_endpoint="repos/${repository}/actions/oidc/customization/sub"
oidc_body="$temporary/oidc.json"
jq -n '{use_default:false,use_immutable_subject:false,include_claim_keys:["repo","context","job_workflow_ref"]}' >"$oidc_body"

get_api() {
  local endpoint="$1" output="$2" error="$3"
  if gh api "$endpoint" -H "X-GitHub-Api-Version: $api_version" >"$output" 2>"$error"; then
    return 0
  fi
  if [[ "$(<"$error")" == *"HTTP 404"* ]]; then
    return 4
  fi
  return 1
}

changes=()
current_oidc="$temporary/current-oidc.json"
oidc_error="$temporary/oidc-error"
if get_api "$oidc_endpoint" "$current_oidc" "$oidc_error"; then
  if ! jq -e --slurpfile desired "$oidc_body" '
    .use_default == $desired[0].use_default and
    (.use_immutable_subject // false) == $desired[0].use_immutable_subject and
    .include_claim_keys == $desired[0].include_claim_keys
  ' "$current_oidc" >/dev/null; then
    changes+=(oidc_subject)
  fi
else
  status=$?
  [[ "$status" -eq 4 ]] || { echo "$(<"$oidc_error")" >&2; exit 1; }
  changes+=(oidc_subject)
fi

environments=(cloud-lease-provision cloud-lease-observe cloud-lease-release cloud-deployment)
for environment in "${environments[@]}"; do
  endpoint="repos/${repository}/environments/${environment}"
  current="$temporary/environment-${environment}.json"
  error="$temporary/environment-${environment}.error"
  if get_api "$endpoint" "$current" "$error"; then
    if ! jq -e '[.protection_rules[]? | select(.type == "required_reviewers")] | length == 0' "$current" >/dev/null; then
      changes+=("environment:${environment}:remove_reviewers")
    fi
  else
    status=$?
    [[ "$status" -eq 4 ]] || { echo "$(<"$error")" >&2; exit 1; }
    changes+=("environment:${environment}:create")
  fi
done

if [[ "$operation" == plan ]]; then
  jq -n --arg repository "$repository" --args '$ARGS.positional' -- "${changes[@]}" |
    jq --arg repository "$repository" '{repository:$repository,changes:.}'
  exit 0
fi

if [[ " ${changes[*]} " == *" oidc_subject "* ]]; then
  gh api --method PUT "$oidc_endpoint" -H "X-GitHub-Api-Version: $api_version" --input "$oidc_body" >/dev/null
fi
for environment in "${environments[@]}"; do
  endpoint="repos/${repository}/environments/${environment}"
  current="$temporary/environment-${environment}.json"
  body="$temporary/environment-${environment}-body.json"
  if [[ -s "$current" ]]; then
    jq -f "$script_dir/environment-without-reviewers.jq" "$current" >"$body"
  else
    jq -n '{wait_timer:0,prevent_self_review:false,reviewers:[]}' >"$body"
  fi
  if [[ ! -s "$current" ]] || ! jq -e '[.protection_rules[]? | select(.type == "required_reviewers")] | length == 0' "$current" >/dev/null; then
    gh api --method PUT "$endpoint" -H "X-GitHub-Api-Version: $api_version" --input "$body" >/dev/null
  fi
done

verified_oidc="$temporary/verified-oidc.json"
gh api "$oidc_endpoint" -H "X-GitHub-Api-Version: $api_version" >"$verified_oidc"
jq -e '
  .use_default == false and
  (.use_immutable_subject // false) == false and
  .include_claim_keys == ["repo","context","job_workflow_ref"]
' "$verified_oidc" >/dev/null
for environment in "${environments[@]}"; do
  gh api "repos/${repository}/environments/${environment}" -H "X-GitHub-Api-Version: $api_version" |
    jq -e '[.protection_rules[]? | select(.type == "required_reviewers")] | length == 0' >/dev/null
done
jq -n --arg repository "$repository" --args '$ARGS.positional' -- "${environments[@]}" |
  jq --arg repository "$repository" '{repository:$repository,oidc_subject:["repo","context","job_workflow_ref"],environments:.}'
