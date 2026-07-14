#!/usr/bin/env bash

set -euo pipefail

umask 077

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
TOOLCHAIN_FILE="$REPO_ROOT/.github/cloud-sim/toolchain.env"

[[ -f "$TOOLCHAIN_FILE" ]] || {
  printf 'cloud-sim setup: required toolchain manifest is missing: %s\n' "$TOOLCHAIN_FILE" >&2
  exit 1
}
# shellcheck disable=SC1090
source "$TOOLCHAIN_FILE"

region=""
repository=""
assume_yes=false
setup_temp=""

cleanup() {
  if [[ -n "$setup_temp" && -d "$setup_temp" ]]; then
    rm -rf "$setup_temp"
  fi
}
trap cleanup EXIT INT TERM

usage() {
  cat <<'EOF'
Usage: ./scripts/cloud-sim/setup.sh [options]

One-time Alibaba CloudShell setup for WuKongIM Cloud Simulation.

Options:
  --region REGION       Alibaba Cloud region (default: current CloudShell profile recommendation)
  --repository OWNER/REPO
                        GitHub repository (default: detected from the checkout)
  --yes                 Accept the displayed non-billable bootstrap plan
  -h, --help            Show this help

This command creates only OIDC/RAM trust and GitHub configuration. It does not create billable cloud resources.
Live diagnosis uses the local Codex CLI signed in with a ChatGPT subscription; no OpenAI API key is required.
Missing tools use checksum-pinned, bounded, resumable downloads under the user cache.
EOF
}

fail() {
  printf 'cloud-sim setup: %s\n' "$*" >&2
  exit 1
}

for toolchain_variable in \
  GO_VERSION GO_LINUX_AMD64_SHA256 GO_LINUX_ARM64_SHA256 \
  GH_CLI_VERSION GH_CLI_LINUX_AMD64_SHA256 GH_CLI_LINUX_ARM64_SHA256; do
  [[ -n "${!toolchain_variable:-}" ]] || fail "$TOOLCHAIN_FILE is missing $toolchain_variable"
done

while (($#)); do
  case "$1" in
    --region)
      [[ $# -ge 2 ]] || fail "--region requires a value"
      region="$2"
      shift 2
      ;;
    --repository)
      [[ $# -ge 2 ]] || fail "--repository requires OWNER/REPO"
      repository="$2"
      shift 2
      ;;
    --yes)
      assume_yes=true
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

for command in aliyun curl git jq tar; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required in Alibaba CloudShell"
done

machine_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf '%s\n' amd64 ;;
    aarch64|arm64) printf '%s\n' arm64 ;;
    *) fail "unsupported CloudShell architecture: $(uname -m)" ;;
  esac
}

sha256_file() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$path" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$path" | awk '{print $1}'
  else
    fail "sha256sum or shasum is required"
  fi
}

verify_sha256() {
  local path="$1"
  local expected="$2"
  local actual
  actual="$(sha256_file "$path")"
  if [[ "$actual" != "$expected" ]]; then
    printf 'cloud-sim setup: checksum mismatch for %s\n' "$(basename "$path")" >&2
    return 1
  fi
}

download_with_resume() {
  local label="$1"
  local destination="$2"
  local expected="$3"
  shift 3
  local partial="${destination}.part"
  local url curl_status
  mkdir -p "$(dirname "$destination")"
  if [[ -f "$partial" && "$(sha256_file "$partial")" == "$expected" ]]; then
    mv "$partial" "$destination"
    return
  fi
  for url in "$@"; do
    printf 'Downloading %s (resumable): %s\n' "$label" "$url"
    curl_status=0
    curl --fail --location --ipv4 --http1.1 \
      --connect-timeout 8 --max-time 180 \
      --speed-time 15 --speed-limit 1024 \
      --retry 2 --retry-delay 1 --retry-max-time 90 \
      --continue-at - --proto '=https' --tlsv1.2 \
      "$url" --output "$partial" || curl_status=$?
    if ((curl_status == 0)); then
      mv "$partial" "$destination"
      return
    fi
    if [[ -f "$partial" && "$(sha256_file "$partial")" == "$expected" ]]; then
      mv "$partial" "$destination"
      return
    fi
    if ((curl_status == 33)) && [[ -s "$partial" ]]; then
      printf 'Server cannot resume %s; retrying this source from byte zero.\n' "$label" >&2
      rm -f "$partial"
      curl_status=0
      curl --fail --location --ipv4 --http1.1 \
        --connect-timeout 8 --max-time 180 \
        --speed-time 15 --speed-limit 1024 \
        --retry 2 --retry-delay 1 --retry-max-time 90 \
        --continue-at - --proto '=https' --tlsv1.2 \
        "$url" --output "$partial" || curl_status=$?
      if ((curl_status == 0)); then
        mv "$partial" "$destination"
        return
      fi
    fi
    printf 'Download source failed for %s (curl exit %d); trying the next trusted source.\n' \
      "$label" "$curl_status" >&2
  done
  fail "cannot download $label within the bounded network window; partial data is retained at $partial for the next run"
}

ensure_go() {
  if command -v go >/dev/null 2>&1; then
    return
  fi
  local arch archive expected cache download_cache downloaded_archive
  arch="$(machine_arch)"
  archive="go${GO_VERSION}.linux-${arch}.tar.gz"
  if [[ "$arch" == amd64 ]]; then expected="$GO_LINUX_AMD64_SHA256"; else expected="$GO_LINUX_ARM64_SHA256"; fi
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/wukongim-cloud-sim/go${GO_VERSION}"
  if [[ ! -x "$cache/go/bin/go" ]]; then
    mkdir -p "$cache"
    download_cache="${XDG_CACHE_HOME:-$HOME/.cache}/wukongim-cloud-sim/downloads"
    downloaded_archive="$download_cache/$archive"
    if [[ -f "$downloaded_archive" ]]; then
      if ! verify_sha256 "$downloaded_archive" "$expected"; then
        rm -f "$downloaded_archive"
      fi
    fi
    if [[ ! -f "$downloaded_archive" ]]; then
      download_with_resume "Go $GO_VERSION" "$downloaded_archive" "$expected" \
        "https://mirrors.aliyun.com/golang/${archive}" \
        "https://go.dev/dl/${archive}"
      verify_sha256 "$downloaded_archive" "$expected"
    fi
    tar -xzf "$downloaded_archive" -C "$cache"
  fi
  PATH="$cache/go/bin:$PATH"
  export PATH
}

ensure_gh() {
  if command -v gh >/dev/null 2>&1; then
    return
  fi
  local arch archive expected cache extracted download_cache downloaded_archive
  arch="$(machine_arch)"
  archive="gh_${GH_CLI_VERSION}_linux_${arch}.tar.gz"
  if [[ "$arch" == amd64 ]]; then expected="$GH_CLI_LINUX_AMD64_SHA256"; else expected="$GH_CLI_LINUX_ARM64_SHA256"; fi
  cache="${XDG_CACHE_HOME:-$HOME/.cache}/wukongim-cloud-sim/gh-${GH_CLI_VERSION}"
  if [[ ! -x "$cache/bin/gh" ]]; then
    mkdir -p "$cache/bin"
    download_cache="${XDG_CACHE_HOME:-$HOME/.cache}/wukongim-cloud-sim/downloads"
    downloaded_archive="$download_cache/$archive"
    if [[ -f "$downloaded_archive" ]]; then
      if ! verify_sha256 "$downloaded_archive" "$expected"; then
        rm -f "$downloaded_archive"
      fi
    fi
    if [[ ! -f "$downloaded_archive" ]]; then
      download_with_resume "GitHub CLI $GH_CLI_VERSION" "$downloaded_archive" "$expected" \
        "https://github.com/cli/cli/releases/download/v${GH_CLI_VERSION}/${archive}"
      verify_sha256 "$downloaded_archive" "$expected"
    fi
    extracted="$setup_temp/gh_${GH_CLI_VERSION}_linux_${arch}"
    tar -xzf "$downloaded_archive" -C "$setup_temp"
    cp "$extracted/bin/gh" "$cache/bin/gh"
    chmod 0755 "$cache/bin/gh"
  fi
  PATH="$cache/bin:$PATH"
  export PATH
}

setup_temp="$(mktemp -d "${TMPDIR:-/tmp}/wukongim-cloud-setup.XXXXXX")"
ensure_go
ensure_gh

if ! gh auth status --hostname github.com >/dev/null 2>&1; then
  printf '%s\n' 'GitHub login is required. Follow the device/browser prompt once.'
  gh auth login --hostname github.com --git-protocol https --web --scopes repo,workflow
fi

if [[ -z "$repository" ]]; then
  repository="$(gh repo view --json nameWithOwner --jq .nameWithOwner 2>/dev/null || true)"
fi
[[ "$repository" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] || fail "cannot determine a valid GitHub OWNER/REPO"

repo_json="$(gh api "repos/$repository")"
jq -e '.permissions.admin == true' <<<"$repo_json" >/dev/null || fail "GitHub repository admin permission is required"
default_branch="$(gh repo view --repo "$repository" --json defaultBranchRef --jq .defaultBranchRef.name)"
[[ "$default_branch" == main ]] || fail "the current trust policy requires main to be the default branch"
for workflow in cloud-sim-oidc-subject.yml cloud-sim-provision.yml cloud-sim-analyze.yml cloud-sim-cleanup.yml; do
  gh api "repos/$repository/contents/.github/workflows/$workflow?ref=main" >/dev/null || \
    fail "$workflow is not present on remote main; push the implementation first"
done

identity_json="$(aliyun sts GetCallerIdentity)"
account_id="$(jq -er '.AccountId | select(type == "string" and test("^[0-9]{6,32}$"))' <<<"$identity_json")" || \
  fail "cannot read the current Alibaba account identity"

regions_json="$(aliyun ecs DescribeRegions)"
regions=()
while IFS= read -r candidate_region; do
  regions+=("$candidate_region")
done < <(jq -r '.Regions.Region[]?.RegionId | select(type == "string" and length > 0)' <<<"$regions_json" | sort -u)
((${#regions[@]} > 0)) || fail "Alibaba returned no available ECS regions"

if [[ -n "${ALIBABA_CLOUD_PROFILE:-}" ]]; then
  profile_json="$(aliyun configure get --profile "$ALIBABA_CLOUD_PROFILE" 2>/dev/null || printf '%s' '{}')"
else
  profile_json="$(aliyun configure get 2>/dev/null || printf '%s' '{}')"
fi
recommended_region="${ALIBABA_CLOUD_REGION_ID:-}"
if [[ -z "$recommended_region" ]]; then
  recommended_region="$(jq -r '.region_id // .RegionId // .regionId // empty' <<<"$profile_json" 2>/dev/null || true)"
fi
region_available() {
  local wanted="$1"
  local candidate
  for candidate in "${regions[@]}"; do
    [[ "$candidate" == "$wanted" ]] && return 0
  done
  return 1
}
if ! region_available "$recommended_region"; then
  if region_available cn-hangzhou; then
    recommended_region=cn-hangzhou
  else
    recommended_region="${regions[0]}"
  fi
fi
if [[ -z "$region" ]]; then
  printf 'Alibaba region [%s]: ' "$recommended_region" >/dev/tty
  IFS= read -r region </dev/tty
  region="${region:-$recommended_region}"
fi
[[ "$region" =~ ^[a-z0-9-]+$ ]] || fail "invalid Alibaba region"
region_available "$region" || fail "Alibaba ECS region is not available to the current account: $region"

zones_json="$(aliyun ecs DescribeZones --RegionId "$region" --InstanceChargeType PostPaid --SpotStrategy SpotAsPriceGo --Verbose true)"
zones=()
while IFS= read -r candidate_zone; do
  zones+=("$candidate_zone")
done < <(jq -r '
  .Zones.Zone[]?
  | select((.ZoneType // "AvailabilityZone") == "AvailabilityZone")
  | select((.AvailableDiskCategories.DiskCategories // []) | index("cloud_essd"))
  | .ZoneId
' <<<"$zones_json" | sort)
((${#zones[@]} > 0)) || fail "no spot-capable zone with cloud_essd was found in $region"

image_json="$(aliyun ecs DescribeImages \
  --RegionId "$region" \
  --ImageFamily acs:alibaba_cloud_linux_3_2104_lts_x64 \
  --ImageOwnerAlias system \
  --Architecture x86_64 \
  --OSType linux \
  --Status Available \
  --IsSupportCloudinit true \
  --PageSize 100)"
image_id="$(jq -er '
  [.Images.Image[]? | select(.IsSupportCloudinit == true)]
  | sort_by(.CreationTime) | reverse | .[0].ImageId
  | select(type == "string" and length > 0)
' <<<"$image_json")" || fail "no supported Alibaba Cloud Linux 3 image was found in $region"

query_specs() {
  local cpu="$1"
  local memory="$2"
  local next_token=""
  local page
  local pages=0
  local all_types='[]'
  local arguments
  while :; do
    arguments=(
      ecs DescribeInstanceTypes
      --MinimumCpuCoreCount "$cpu" --MaximumCpuCoreCount "$cpu"
      --MinimumMemorySize "$memory" --MaximumMemorySize "$memory"
      --MaxResults 100
    )
    if [[ -n "$next_token" ]]; then
      arguments+=(--NextToken "$next_token")
    fi
    page="$(aliyun "${arguments[@]}")"
    all_types="$(jq -cn --argjson current "$all_types" --argjson page "$page" \
      '$current + ($page.InstanceTypes.InstanceType // [])')"
    next_token="$(jq -r '.NextToken // empty' <<<"$page")"
    ((pages += 1))
    ((pages <= 20)) || fail "DescribeInstanceTypes pagination exceeded 20 pages"
    [[ -n "$next_token" ]] || break
  done
  jq -cn --argjson types "$all_types" '{InstanceTypes:{InstanceType:$types}}'
}

small_specs="$(query_specs 2 4)"
standard_specs="$(query_specs 4 8)"
stress_specs="$(query_specs 8 16)"

available_now() {
  local zone="$1"
  local instance_type="$2"
  local result
  result="$(aliyun ecs DescribeAvailableResource \
    --RegionId "$region" --ZoneId "$zone" \
    --DestinationResource InstanceType --ResourceType instance \
    --InstanceChargeType PostPaid --SpotStrategy SpotAsPriceGo \
    --InstanceType "$instance_type" --IoOptimized optimized --NetworkCategory vpc)" || return 1
  jq -e --arg zone "$zone" --arg instance_type "$instance_type" '
    any(.AvailableZones.AvailableZone[]?;
      .ZoneId == $zone and .Status == "Available" and
      any(.AvailableResources.AvailableResource[]?.SupportedResources.SupportedResource[]?;
        .Value == $instance_type and .Status == "Available"))
  ' <<<"$result" >/dev/null
}

select_types() {
  local zone="$1"
  local specs="$2"
  local candidates candidate
  candidates=()
  while IFS= read -r candidate; do
    candidates+=("$candidate")
  done < <(jq -r '
    [.InstanceTypes.InstanceType[]?
      | (.CpuArchitecture // "" | ascii_downcase) as $architecture
      | select($architecture == "x86" or $architecture == "x86_64" or $architecture == "amd64")
      | select(((.GPUAmount // 0) | tonumber?) == 0)
      | select((.InstanceTypeId // "") | test("^ecs\\.[cg][0-9]"))
      | select((.InstanceFamilyLevel // "EnterpriseLevel") != "EntryLevel")
      | select((.EniPrivateIpAddressQuantity // 0) >= 4)
      | .InstanceTypeId]
    | sort | reverse | .[]
  ' <<<"$specs")
  selected_types=()
  for candidate in "${candidates[@]}"; do
    if available_now "$zone" "$candidate"; then
      selected_types+=("$candidate")
      ((${#selected_types[@]} == 3)) && break
    fi
  done
  ((${#selected_types[@]} > 0))
}

zone_id=""
small_types=()
standard_types=()
stress_types=()
for candidate_zone in "${zones[@]}"; do
  if select_types "$candidate_zone" "$small_specs"; then small_candidate=("${selected_types[@]}"); else continue; fi
  if select_types "$candidate_zone" "$standard_specs"; then standard_candidate=("${selected_types[@]}"); else continue; fi
  if select_types "$candidate_zone" "$stress_specs"; then stress_candidate=("${selected_types[@]}"); else continue; fi
  zone_id="$candidate_zone"
  small_types=("${small_candidate[@]}")
  standard_types=("${standard_candidate[@]}")
  stress_types=("${stress_candidate[@]}")
  break
done
[[ -n "$zone_id" ]] || fail "no zone has live spot candidates for the small, standard, and stress presets"

sha256_text() {
  if command -v sha256sum >/dev/null 2>&1; then
    printf '%s' "$1" | sha256sum | awk '{print $1}'
  else
    printf '%s' "$1" | shasum -a 256 | awk '{print $1}'
  fi
}

account_hash="$(sha256_text "$account_id")"
repository_suffix="$(sha256_text "$repository")"
repository_suffix="${repository_suffix:0:8}"
oidc_provider_name="wukongim-github-$repository_suffix"
provisioner_role_name="wukongim-cloud-sim-provisioner-$repository_suffix"
analyzer_role_name="wukongim-cloud-sim-analyzer-$repository_suffix"

bootstrap_config="$setup_temp/bootstrap.json"
jq -n \
  --arg account_id "$account_id" --arg account_hash "sha256:$account_hash" \
  --arg region "$region" --arg zone "$zone_id" --arg repository "$repository" --arg image "$image_id" \
  --arg oidc_provider_name "$oidc_provider_name" \
  --arg provisioner_role_name "$provisioner_role_name" --arg analyzer_role_name "$analyzer_role_name" \
  --argjson small "$(printf '%s\n' "${small_types[@]}" | jq -R . | jq -s .)" \
  --argjson standard "$(printf '%s\n' "${standard_types[@]}" | jq -R . | jq -s .)" \
  --argjson stress "$(printf '%s\n' "${stress_types[@]}" | jq -R . | jq -s .)" \
  '{
    bootstrap: {
      account_id: $account_id, region: $region, repository: $repository, default_branch: "main",
      oidc_provider_name: $oidc_provider_name, oidc_audience: "wukongim-cloud-sim",
      provision_environment: "cloud-sim-provision", cleanup_environment: "cloud-sim-cleanup",
      analysis_environment: "cloud-sim-analysis",
      provisioner_role_name: $provisioner_role_name,
      analyzer_role_name: $analyzer_role_name
    },
    provider: {
      region: $region, zone_id: $zone, image_id: $image, account_id_hash: $account_hash,
      vpc_ipv4_cidr: "10.42.0.0/16", vswitch_ipv4_cidr: "10.42.0.0/24",
      system_disk_category: "cloud_essd", system_disk_size_gib: 40,
      data_disk_category: "cloud_essd", data_disk_size_gib: 100,
      public_bandwidth_mbps: 20,
      private_ipv4: {"node-1":"10.42.0.11","node-2":"10.42.0.12","node-3":"10.42.0.13","sim":"10.42.0.20"},
      simulator_source_ipv4: ["10.42.0.20","10.42.0.21","10.42.0.22"],
      presets: {small:{instance_types:$small},standard:{instance_types:$standard},stress:{instance_types:$stress}}
    }
  }' >"$bootstrap_config"

printf '\nWuKongIM Cloud Simulation setup recommendation\n'
printf '  Repository: %s\n' "$repository"
printf '  Region/zone: %s / %s\n' "$region" "$zone_id"
printf '  Image: %s\n' "$image_id"
printf '  Small: %s\n' "${small_types[*]}"
printf '  Standard: %s\n' "${standard_types[*]}"
printf '  Stress: %s\n' "${stress_types[*]}"
printf '  Billable resources created now: none\n\n'

export GOWORK=off
initial_plan="$setup_temp/initial-plan.json"
(cd "$REPO_ROOT" && go run ./cmd/wkcloudbootstrap --config "$bootstrap_config" plan) >"$initial_plan"
jq . "$initial_plan"

if [[ "$assume_yes" != true ]]; then
  printf 'Apply this non-billable OIDC/RAM bootstrap? [y/N] ' >/dev/tty
  IFS= read -r confirmation </dev/tty
  [[ "$confirmation" =~ ^[Yy]$ ]] || fail "cancelled before mutation"
fi

bootstrap_result="$setup_temp/bootstrap-result.json"
(cd "$REPO_ROOT" && go run ./cmd/wkcloudbootstrap --config "$bootstrap_config" apply) >"$bootstrap_result"
(cd "$REPO_ROOT" && go run ./cmd/wkcloudbootstrap --config "$bootstrap_config" plan) >"$setup_temp/final-plan.json"
jq -e '.changes | length == 0' "$setup_temp/final-plan.json" >/dev/null || fail "bootstrap is not idempotent after apply"

repository_state_name="${repository//\//_}"
state_dir="${XDG_CONFIG_HOME:-$HOME/.config}/wukongim/cloud-sim/$repository_state_name"
mkdir -p "$state_dir"
chmod 0700 "$state_dir"
cp "$bootstrap_config" "$state_dir/bootstrap.json"
cp "$bootstrap_result" "$state_dir/bootstrap-result.json"
chmod 0600 "$state_dir/bootstrap.json" "$state_dir/bootstrap-result.json"

api_version_header='X-GitHub-Api-Version: 2026-03-10'
environment_body='{"deployment_branch_policy":null}'
for environment in cloud-sim-provision cloud-sim-cleanup cloud-sim-analysis; do
  environment_error="$setup_temp/environment-$environment.err"
  if gh api "repos/$repository/environments/$environment" -H "$api_version_header" >/dev/null 2>"$environment_error"; then
    :
  elif grep -Eq '\(HTTP 404\)' "$environment_error"; then
    printf '%s' "$environment_body" | gh api --method PUT "repos/$repository/environments/$environment" \
      -H "$api_version_header" --input - >/dev/null
  else
    cat "$environment_error" >&2
    fail "cannot inspect GitHub environment $environment; refusing to overwrite it"
  fi
done

provider_json="$setup_temp/provider.json"
bootstrap_hash="$(jq -er .account_id_hash "$bootstrap_result")"
jq --arg hash "$bootstrap_hash" '.provider.account_id_hash = $hash | .provider' "$bootstrap_config" | jq -c . >"$provider_json"

oidc_provider_arn="$(jq -er .oidc_provider_arn "$bootstrap_result")"
oidc_audience="$(jq -er .oidc_audience "$bootstrap_result")"
provider_config_json="$(cat "$provider_json")"
printf '%s' "$oidc_provider_arn" | \
  gh variable set ALIBABA_CLOUD_SIM_OIDC_PROVIDER_ARN --repo "$repository"
printf '%s' "$oidc_audience" | \
  gh variable set ALIBABA_CLOUD_SIM_OIDC_AUDIENCE --repo "$repository"
gh variable set ALIBABA_CLOUD_SIM_CONFIG_JSON --repo "$repository" <"$provider_json"

provisioner_role="$(jq -er .provisioner_role_arn "$bootstrap_result")"
for environment in cloud-sim-provision cloud-sim-cleanup; do
  printf '%s' "$provisioner_role" | \
    gh variable set ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN --env "$environment" --repo "$repository"
done
analyzer_role="$(jq -er .analyzer_role_arn "$bootstrap_result")"
printf '%s' "$analyzer_role" | \
  gh variable set ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN --env cloud-sim-analysis --repo "$repository"

verify_repo_variable() {
  local name="$1"
  local expected="$2"
  local current
  current="$(gh api "repos/$repository/actions/variables/$name" -H "$api_version_header")"
  jq -e --arg name "$name" --arg expected "$expected" \
    '.name == $name and .value == $expected' <<<"$current" >/dev/null || \
    fail "GitHub variable verification failed: $name"
}

verify_environment_variable() {
  local environment="$1"
  local name="$2"
  local expected="$3"
  local current
  current="$(gh api "repos/$repository/environments/$environment/variables/$name" -H "$api_version_header")"
  jq -e --arg name "$name" --arg expected "$expected" \
    '.name == $name and .value == $expected' <<<"$current" >/dev/null || \
    fail "GitHub variable verification failed: $environment/$name"
}

verify_repo_variable ALIBABA_CLOUD_SIM_OIDC_PROVIDER_ARN "$oidc_provider_arn"
verify_repo_variable ALIBABA_CLOUD_SIM_OIDC_AUDIENCE "$oidc_audience"
verify_repo_variable ALIBABA_CLOUD_SIM_CONFIG_JSON "$provider_config_json"
verify_environment_variable cloud-sim-provision ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN "$provisioner_role"
verify_environment_variable cloud-sim-cleanup ALIBABA_CLOUD_SIM_PROVISIONER_ROLE_ARN "$provisioner_role"
verify_environment_variable cloud-sim-analysis ALIBABA_CLOUD_SIM_ANALYZER_ROLE_ARN "$analyzer_role"
oidc_subject='{"use_default":false,"use_immutable_subject":false,"include_claim_keys":["repo","context","job_workflow_ref"]}'
printf '%s' "$oidc_subject" | gh api --method PUT "repos/$repository/actions/oidc/customization/sub" \
  -H "$api_version_header" --input - >/dev/null
current_subject="$(gh api "repos/$repository/actions/oidc/customization/sub" -H "$api_version_header")"
jq -e '
  .use_default == false and (.use_immutable_subject // false) == false and
  .include_claim_keys == ["repo","context","job_workflow_ref"]
' <<<"$current_subject" >/dev/null || fail "GitHub OIDC subject verification failed"

verification_digest="$(sha256_text "$repository:$account_id:$(date -u +%Y%m%dT%H%M%SZ):$$:$setup_temp")"
verification_id="setup-$repository_suffix-${verification_digest:0:16}"
verification_title="Cloud Simulation OIDC Verification $verification_id"
gh workflow run cloud-sim-oidc-subject.yml --repo "$repository" --ref main -f "verification_id=$verification_id"
verification_run=""
for ((attempt = 0; attempt < 30; attempt += 1)); do
  verification_runs="$(gh run list --workflow cloud-sim-oidc-subject.yml --repo "$repository" \
    --event workflow_dispatch --branch main --limit 20 --json databaseId,displayTitle)"
  candidate_run="$(jq -r --arg title "$verification_title" \
    '[.[] | select(.displayTitle == $title)] | sort_by(.databaseId) | reverse | .[0].databaseId // empty' \
    <<<"$verification_runs")"
  if [[ "$candidate_run" =~ ^[0-9]+$ && "$candidate_run" != 0 ]]; then
    verification_run="$candidate_run"
    break
  fi
  sleep 2
done
[[ -n "$verification_run" ]] || fail "cannot locate the dispatched Alibaba OIDC verification workflow"
gh run watch "$verification_run" --repo "$repository" --exit-status || \
  fail "GitHub OIDC could not assume the Alibaba analyzer role"

source_sha="$(gh api "repos/$repository/commits/main" --jq .sha)"
[[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || fail "cannot resolve the trusted main source SHA"

cat <<EOF

Setup complete. GitHub OIDC successfully assumed the Alibaba analyzer role.
No ECS instance, disk, EIP, or VPC was created.
Saved removal/reconfiguration state:
  $state_dir/bootstrap.json

Start the first real canary here:
  https://github.com/$repository/actions/workflows/cloud-sim-provision.yml

Recommended inputs:
  region=$region
  source_sha=$source_sha
  scenario=cloud-small
  infrastructure_preset=small
  duration=2h
  analysis_grace=1h
  max_total_cost=50

After Provision prints a Run Identity, analyze it with your ChatGPT login:
  ./scripts/cloud-sim/analyze.sh <run_id>
EOF
