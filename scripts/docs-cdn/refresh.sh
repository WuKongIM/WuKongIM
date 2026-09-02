#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly expected_domain="docs.githubim.com"

fail() {
  printf 'docs CDN refresh: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 0 ]] || fail "this helper does not accept arguments"
[[ "${DOCS_CDN_ENABLED:-}" == true ]] || fail "DOCS_CDN_ENABLED must be exactly true"
[[ "${DOCS_CDN_DOMAIN:-}" == "$expected_domain" ]] || \
  fail "DOCS_CDN_DOMAIN must be exactly ${expected_domain}"
[[ -n "${ALIBABA_CLOUD_ACCESS_KEY_ID:-}" ]] || fail "temporary Alibaba access key ID is required"
[[ -n "${ALIBABA_CLOUD_ACCESS_KEY_SECRET:-}" ]] || fail "temporary Alibaba access key secret is required"
[[ -n "${ALIBABA_CLOUD_SECURITY_TOKEN:-}" ]] || fail "temporary Alibaba security token is required"

for command in aliyun jq; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done

readonly object_paths="$(printf '%s\n' \
  "https://${expected_domain}/" \
  "https://${expected_domain}/zh/" \
  "https://${expected_domain}/en/" \
  "https://${expected_domain}/api/search")"

export ALIBABA_CLOUD_IGNORE_PROFILE=TRUE
export ALIBABA_CLOUD_DISABLE_EXTERNAL_PROCESS=TRUE
response="$(aliyun --region cn-hangzhou cdn RefreshObjectCaches \
  --ObjectPath "$object_paths" \
  --ObjectType File)" || fail "Alibaba Cloud rejected the refresh request"

refresh_task_id="$(jq -er '
  .RefreshTaskId |
  select(type == "string" and test("^[0-9]+(,[0-9]+)*$"))
' <<<"$response")" || fail "Alibaba Cloud returned no valid refresh task ID"
request_id="$(jq -er '
  .RequestId |
  select(type == "string" and test("^[A-Za-z0-9-]{1,128}$"))
' <<<"$response")" || fail "Alibaba Cloud returned no valid request ID"

printf 'Alibaba CDN refresh accepted: task_id=%s request_id=%s urls=4\n' \
  "$refresh_task_id" "$request_id"

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    printf '### Alibaba CDN refresh\n\n'
    printf -- '- Domain: `%s`\n' "$expected_domain"
    printf -- '- Refresh task: `%s`\n' "$refresh_task_id"
    printf -- '- Request: `%s`\n' "$request_id"
    printf -- '- URLs: 4 bounded stable public URLs\n'
  } >>"$GITHUB_STEP_SUMMARY"
fi
