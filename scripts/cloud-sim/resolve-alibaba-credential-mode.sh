#!/usr/bin/env bash
set -euo pipefail

: "${GITHUB_ENV:?GITHUB_ENV is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

access_key_id="${ACCESS_KEY_ID:-}"
access_key_secret="${ACCESS_KEY_SECRET:-}"
if [[ -n "$access_key_id" || -n "$access_key_secret" ]]; then
  if [[ -z "$access_key_id" || -z "$access_key_secret" ]]; then
    echo 'Both ALIBABA_CLOUD_ACCESS_KEY_ID and ALIBABA_CLOUD_ACCESS_KEY_SECRET must be configured together.' >&2
    exit 1
  fi
  [[ "$access_key_id" != *$'\n'* && "$access_key_id" != *$'\r'* ]]
  [[ "$access_key_secret" != *$'\n'* && "$access_key_secret" != *$'\r'* ]]
  echo "::add-mask::$access_key_id"
  echo "::add-mask::$access_key_secret"
  printf 'ALIBABA_CLOUD_ACCESS_KEY_ID=%s\n' "$access_key_id" >>"$GITHUB_ENV"
  printf 'ALIBABA_CLOUD_ACCESS_KEY_SECRET=%s\n' "$access_key_secret" >>"$GITHUB_ENV"
  echo 'mode=access-key' >>"$GITHUB_OUTPUT"
else
  echo 'mode=oidc' >>"$GITHUB_OUTPUT"
fi
