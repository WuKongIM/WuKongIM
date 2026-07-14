#!/usr/bin/env bash
set -euo pipefail

run_id="${1:-}"
destination="${2:-provider.json}"
[[ "$run_id" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || {
  echo "invalid Simulation Run identity" >&2
  exit 1
}
: "${GITHUB_REPOSITORY:?GITHUB_REPOSITORY is required}"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
temporary="$(mktemp -d)"
trap 'rm -rf "$temporary"' EXIT

gh api --paginate -X GET "repos/${GITHUB_REPOSITORY}/actions/artifacts" -f per_page=100 \
  --jq '.artifacts[] | select(.expired == false and (.name | startswith("cloud-sim-provider-config"))) | [.id,.name] | @tsv' \
  >"$temporary/provider-artifacts.tsv"
if artifact_id="$("$script_dir/select-exact-provider-config-artifact.sh" "$run_id" \
  <"$temporary/provider-artifacts.tsv" 2>"$temporary/selector-error")"; then
  gh api "repos/${GITHUB_REPOSITORY}/actions/artifacts/${artifact_id}/zip" >"$temporary/provider-config.zip"
  mkdir "$temporary/provider-config"
  unzip -q "$temporary/provider-config.zip" -d "$temporary/provider-config"
  cp "$temporary/provider-config/provider.json" "$destination"
elif [[ -n "${LEGACY_PROVIDER_CONFIG_JSON:-}" ]]; then
  printf '%s' "$LEGACY_PROVIDER_CONFIG_JSON" >"$destination"
else
  cat "$temporary/selector-error" >&2
  exit 1
fi

jq -e '.region | type == "string" and test("^[a-z0-9-]+$")' "$destination" >/dev/null
jq -e '.account_id_hash | test("^sha256:[0-9a-f]{64}$")' "$destination" >/dev/null
if [[ -n "${EXPECTED_REGION:-}" ]]; then
  test "$(jq -er .region "$destination")" = "$EXPECTED_REGION"
fi
if [[ -n "${EXPECTED_ACCOUNT_ID_HASH:-}" ]]; then
  test "$(jq -er .account_id_hash "$destination")" = "$EXPECTED_ACCOUNT_ID_HASH"
fi
