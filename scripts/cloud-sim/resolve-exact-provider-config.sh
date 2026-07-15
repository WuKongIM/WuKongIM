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
artifact_region=""
artifact_account_id_hash=""
if selection="$("$script_dir/select-exact-provider-config-artifact.sh" "$run_id" \
  <"$temporary/provider-artifacts.tsv" 2>"$temporary/selector-error")"; then
  IFS=$'\t' read -r artifact_id artifact_region artifact_account_id_hash <<<"$selection"
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
if [[ -n "$artifact_region" ]]; then
  test "$(jq -er .region "$destination")" = "$artifact_region"
  test "$(jq -er .account_id_hash "$destination")" = "$artifact_account_id_hash"
fi
expected_region="${EXPECTED_REGION:-}"
expected_account_id_hash="${EXPECTED_ACCOUNT_ID_HASH:-}"
if [[ -z "$expected_region" && -z "$expected_account_id_hash" ]]; then
  locator_name="cloud-sim-locator-${run_id}"
  locators="$(gh api -X GET "repos/${GITHUB_REPOSITORY}/actions/artifacts" -f name="$locator_name" -f per_page=2)"
  locator_count="$(jq -er '[.artifacts[] | select(.expired == false)] | length' <<<"$locators")"
  case "$locator_count" in
    0)
      if [[ -z "$artifact_region" || -z "$artifact_account_id_hash" ]]; then
        echo "no trusted provider binding or run locator exists for Simulation Run $run_id" >&2
        exit 1
      fi
      expected_region="$artifact_region"
      expected_account_id_hash="$artifact_account_id_hash"
      ;;
    1)
      locator_id="$(jq -er '[.artifacts[] | select(.expired == false)][0].id' <<<"$locators")"
      gh api "repos/${GITHUB_REPOSITORY}/actions/artifacts/${locator_id}/zip" >"$temporary/locator.zip"
      mkdir "$temporary/locator"
      unzip -q "$temporary/locator.zip" -d "$temporary/locator"
      test "$(jq -er .run_id "$temporary/locator/run-locator.json")" = "$run_id"
      expected_region="$(jq -er '.region | select(test("^[a-z0-9-]+$"))' "$temporary/locator/run-locator.json")"
      expected_account_id_hash="$(jq -er '.account_id_hash | select(test("^sha256:[0-9a-f]{64}$"))' "$temporary/locator/run-locator.json")"
      ;;
    *)
      echo "multiple run locators exist for Simulation Run $run_id" >&2
      exit 1
      ;;
  esac
elif [[ -z "$expected_region" || -z "$expected_account_id_hash" ]]; then
  echo "expected provider region and account hash must be supplied together" >&2
  exit 1
fi
test "$(jq -er .region "$destination")" = "$expected_region"
test "$(jq -er .account_id_hash "$destination")" = "$expected_account_id_hash"
