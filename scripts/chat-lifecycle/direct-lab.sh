#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat >&2 <<'EOF'
usage: direct-lab.sh preflight
       direct-lab.sh start REQUEST_ID [--duration DURATION] [--budget-cny WHOLE_CNY]
       direct-lab.sh deploy REQUEST_ID
       direct-lab.sh run REQUEST_ID
       direct-lab.sh status REQUEST_ID
       direct-lab.sh diagnose REQUEST_ID
       direct-lab.sh stop REQUEST_ID
EOF
}

readonly DEFAULT_QUALIFICATION_DURATION='60m'
readonly DEFAULT_BUDGET_CNY=300
readonly DEFAULT_OPERATIONAL_STOP_CNY=250
readonly MIN_BUDGET_CNY=300
readonly MAX_BUDGET_CNY=1500
readonly CUSTOM_BUDGET_STOP_RESERVE_CNY=20
readonly QUALIFICATION_RESERVE_SECONDS=900
readonly MAX_QUALIFICATION_DURATION_SECONDS=259200
readonly MIN_QUALIFICATION_DURATION_SECONDS=60
readonly MIN_LEASE_DURATION_SECONDS=21600
readonly LEASE_RESERVE_SECONDS=17100

die() {
  echo "chat lifecycle direct lab: $*" >&2
  exit 1
}

parse_duration_seconds() {
  local value="$1" remaining="$1" total=0 amount unit multiplier
  [[ -n "$value" ]] || return 1
  while [[ -n "$remaining" ]]; do
    [[ "$remaining" =~ ^([0-9]+)(h|m|s)(.*)$ ]] || return 1
    amount=$((10#${BASH_REMATCH[1]}))
    unit="${BASH_REMATCH[2]}"
    remaining="${BASH_REMATCH[3]}"
    case "$unit" in
      h) multiplier=3600 ;;
      m) multiplier=60 ;;
      s) multiplier=1 ;;
      *) return 1 ;;
    esac
    (( amount <= (MAX_QUALIFICATION_DURATION_SECONDS - total) / multiplier )) || return 1
    total=$((total + amount * multiplier))
  done
  (( total >= MIN_QUALIFICATION_DURATION_SECONDS && total <= MAX_QUALIFICATION_DURATION_SECONDS )) || return 1
  printf '%s\n' "$total"
}

parse_budget_cny() {
  local value="$1" budget
  [[ "$value" =~ ^[0-9]+$ ]] || return 1
  budget=$((10#$value))
  (( budget >= MIN_BUDGET_CNY && budget <= MAX_BUDGET_CNY )) || return 1
  printf '%s\n' "$budget"
}

require_budget_authorization() {
  local budget_cny="$1"
  if (( budget_cny <= DEFAULT_BUDGET_CNY )); then
    return 0
  fi
  [[ "${WK_CHAT_LAB_PAID_BUDGET_CNY:-}" == "$budget_cny" ]] ||
    die "budget above CNY $DEFAULT_BUDGET_CNY requires WK_CHAT_LAB_PAID_BUDGET_CNY=$budget_cny"
}

write_run_policy() {
  local directory="$1" duration="$2" budget_value="$3" duration_seconds max_seconds lease_seconds
  local budget_cny hard_limit_micros operational_stop_micros template root
  duration_seconds="$(parse_duration_seconds "$duration")" ||
    die "duration must be between 1m and 72h using whole h, m, or s units"
  budget_cny="$(parse_budget_cny "$budget_value")" ||
    die "budget must be a whole CNY amount between $MIN_BUDGET_CNY and $MAX_BUDGET_CNY"
  max_seconds=$((duration_seconds + QUALIFICATION_RESERVE_SECONDS))
  lease_seconds=$((max_seconds + LEASE_RESERVE_SECONDS))
  if (( lease_seconds < MIN_LEASE_DURATION_SECONDS )); then
    lease_seconds=$MIN_LEASE_DURATION_SECONDS
  fi
  hard_limit_micros=$((budget_cny * 1000000))
  if (( budget_cny == DEFAULT_BUDGET_CNY )); then
    operational_stop_micros=$((DEFAULT_OPERATIONAL_STOP_CNY * 1000000))
  else
    operational_stop_micros=$(((budget_cny - CUSTOM_BUDGET_STOP_RESERVE_CNY) * 1000000))
  fi
  jq -n --arg duration "$duration" --argjson duration_seconds "$duration_seconds" \
    --argjson max_seconds "$max_seconds" --argjson reserve "$QUALIFICATION_RESERVE_SECONDS" \
    --argjson lease_seconds "$lease_seconds" --argjson budget_cny "$budget_cny" \
    --argjson hard_limit_micros "$hard_limit_micros" \
    --argjson operational_stop_micros "$operational_stop_micros" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_run_policy/v1",duration:$duration,
      duration_seconds:$duration_seconds,max_duration_seconds:$max_seconds,
      qualification_reserve_seconds:$reserve,lease_duration_seconds:$lease_seconds,
      budget_cny:$budget_cny,hard_limit_micros:$hard_limit_micros,
      operational_stop_micros:$operational_stop_micros}' \
    >"$directory/run-policy.json"
  root="$(repository_root)"
  template="$directory/materialize-template.json"
  jq --argjson workload "$max_seconds" --argjson lease "$lease_seconds" \
    --argjson hard_limit "$hard_limit_micros" --argjson operational_stop "$operational_stop_micros" \
    '.workload_duration_seconds=$workload | .lease_duration_seconds=$lease |
      .budget.hard_limit_micros=$hard_limit | .budget.operational_stop_micros=$operational_stop' \
    "$root/configs/cloud/chat-lifecycle/repair-v1.json" >"$template"
  chmod 0600 "$directory/run-policy.json" "$template"
}

normalize_rfc3339_utc() {
  local value="$1" base fraction zone zone_for_date epoch utc_base
  [[ "$value" =~ ^([0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(\.[0-9]{1,9})?(Z|[+-][0-9]{2}:[0-9]{2})$ ]] || return 1
  base="${BASH_REMATCH[1]}"
  fraction="${BASH_REMATCH[2]:-}"
  zone="${BASH_REMATCH[3]}"
  epoch="$(date -u -d "${base}${zone}" '+%s' 2>/dev/null || true)"
  if [[ ! "$epoch" =~ ^-?[0-9]+$ ]]; then
    zone_for_date="$zone"
    [[ "$zone_for_date" == Z ]] && zone_for_date=+0000
    zone_for_date="${zone_for_date/:/}"
    epoch="$(date -u -j -f '%Y-%m-%dT%H:%M:%S%z' "${base}${zone_for_date}" '+%s' 2>/dev/null || true)"
  fi
  [[ "$epoch" =~ ^-?[0-9]+$ ]] || return 1
  utc_base="$(date -u -d "@$epoch" '+%Y-%m-%dT%H:%M:%S' 2>/dev/null || true)"
  if [[ -z "$utc_base" ]]; then
    utc_base="$(date -u -r "$epoch" '+%Y-%m-%dT%H:%M:%S' 2>/dev/null || true)"
  fi
  [[ "$utc_base" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}$ ]] || return 1
  printf '%s%sZ\n' "$utc_base" "$fraction"
}

rfc3339_epoch() {
  local normalized base epoch
  normalized="$(normalize_rfc3339_utc "$1")" || return 1
  base="${normalized:0:19}Z"
  epoch="$(date -u -d "$base" '+%s' 2>/dev/null || true)"
  if [[ ! "$epoch" =~ ^-?[0-9]+$ ]]; then
    epoch="$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$base" '+%s' 2>/dev/null || true)"
  fi
  [[ "$epoch" =~ ^-?[0-9]+$ ]] || return 1
  printf '%s\n' "$epoch"
}

resolve_account_home() {
  local account_home=''
  if command -v getent >/dev/null 2>&1; then
    account_home="$(getent passwd "$(id -u)" | awk -F: 'NR==1 {print $6}')"
  elif command -v dscl >/dev/null 2>&1; then
    account_home="$(dscl . -read "/Users/$(id -un)" NFSHomeDirectory | awk 'NR==1 {print $2}')"
  fi
  [[ "$account_home" == /* && -d "$account_home" ]] || return 1
  printf '%s\n' "$account_home"
}

state_root() {
  local root="${WK_CHAT_LAB_STATE_ROOT:-}"
  if [[ -z "$root" ]]; then
    root="$(resolve_account_home)/wukongim-leases/chat-lifecycle-direct"
  fi
  [[ "$root" == /* && "$root" != / ]] || die 'state root must be an absolute non-root path'
  printf '%s\n' "$root"
}

validate_request_id() {
  [[ "$1" =~ ^chat-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]] ||
    die 'request ID must use chat-<UTC basic timestamp>-<8 lowercase hex>'
}

resolve_request_dir() {
  local request_id="$1" root directory
  validate_request_id "$request_id"
  root="$(state_root)"
  [[ -d "$root" && ! -L "$root" ]] || die "state root does not exist: $root"
  root="$(cd "$root" && pwd -P)"
  directory="$root/$request_id"
  [[ -d "$directory" && ! -L "$directory" ]] || die "request state does not exist: $request_id"
  directory="$(cd "$directory" && pwd -P)"
  [[ "$(dirname "$directory")" == "$root" && "$(basename "$directory")" == "$request_id" ]] ||
    die 'request state escaped its fixed root'
  printf '%s\n' "$directory"
}

repository_root() {
  git rev-parse --show-toplevel
}

cloud_tool() {
  local directory="${1:-}"
  if [[ -n "${WK_CHAT_LAB_CLOUD_TOOL:-}" ]]; then
    printf '%s\n' "$WK_CHAT_LAB_CLOUD_TOOL"
  elif [[ -n "$directory" ]]; then
    printf '%s\n' "$directory/tools/wkcloudlease"
  else
    printf '%s\n' wkcloudlease
  fi
}

chat_tool() {
  local directory="$1"
  printf '%s\n' "${WK_CHAT_LAB_CHAT_TOOL:-$directory/tools/wkchatlifecycle}"
}

bundle_builder() {
  printf '%s\n' "${WK_CHAT_LAB_BUNDLE_BUILDER:-$(repository_root)/scripts/cloud-deployment/build-local-bundle.sh}"
}

gate_tool() {
  local directory="$1"
  printf '%s\n' "${WK_CHAT_LAB_GATE_TOOL:-$directory/tools/wkcloudgate}"
}

require_executable() {
  local tool="$1"
  if [[ "$tool" == */* ]]; then
    [[ -x "$tool" ]] || die "required executable is unavailable: $tool"
  else
    command -v "$tool" >/dev/null 2>&1 || die "required executable is unavailable: $tool"
  fi
}

require_committed_candidate() {
  local root status
  root="$(repository_root)"
  if [[ "${WK_CHAT_LAB_ALLOW_DIRTY_FOR_TESTS:-}" == true ]]; then
    return 0
  fi
  status="$(git -C "$root" status --porcelain --untracked-files=normal)"
  [[ -z "$status" ]] || die 'candidate worktree is dirty; commit the exact candidate before local bundle construction'
}

require_no_unreleased_request() {
  local root request_directory state
  root="$(state_root)"
  if [[ ! -e "$root" && ! -L "$root" ]]; then
    return 0
  fi
  [[ -d "$root" && ! -L "$root" ]] || die 'state root must be a real directory'
  root="$(cd "$root" && pwd -P)"
  for request_directory in "$root"/chat-*; do
    [[ -e "$request_directory" || -L "$request_directory" ]] || continue
    [[ -d "$request_directory" && ! -L "$request_directory" ]] ||
      die "invalid request entry blocks paid start: $request_directory"
    state="$request_directory/state.json"
    [[ -f "$state" && ! -L "$state" ]] ||
      die "request without typed state blocks paid start: $(basename "$request_directory")"
    if ! jq -e '.schema == "wukongim.chat_lifecycle.direct_lab_state/v1" and .state == "released"' \
      "$state" >/dev/null; then
      die "unreleased request blocks paid start: $(basename "$request_directory")"
    fi
    [[ -f "$request_directory/zero-inventory.json" && ! -L "$request_directory/zero-inventory.json" ]] ||
      die "released request lacks exact zero-inventory proof: $(basename "$request_directory")"
  done
}

check_temporary_credentials() {
  local missing=0 name
  temporary_credential_kind=""
  for name in ALIBABA_CLOUD_ACCESS_KEY_ID ALIBABA_CLOUD_ACCESS_KEY_SECRET; do
    if [[ -z "${!name:-}" ]]; then
      echo "missing temporary credential: $name" >&2
      missing=1
    fi
  done
  if [[ -n "${ALIBABA_CLOUD_SECURITY_TOKEN:-}" ]]; then
    temporary_credential_kind="temporary_sts"
  elif [[ "${WK_ALIBABA_CLOUD_SHELL_EPHEMERAL_AUTHORIZATION:-}" == unregistered-one-hour-cloud-shell ]]; then
    temporary_credential_kind="cloud_shell_ephemeral_unregistered"
  else
    echo 'missing temporary credential proof: set ALIBABA_CLOUD_SECURITY_TOKEN for STS or WK_ALIBABA_CLOUD_SHELL_EPHEMERAL_AUTHORIZATION=unregistered-one-hour-cloud-shell for a verified one-hour Cloud Shell credential' >&2
    missing=1
  fi
  if [[ "${WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION:-}" != create-and-delete-paid-cloud-lease ]]; then
    echo 'missing exact lifecycle authorization: WK_ALIBABA_LIFECYCLE_MUTATION_AUTHORIZATION=create-and-delete-paid-cloud-lease' >&2
    missing=1
  fi
  return "$missing"
}

preflight() {
  local failed=0 tool
  for tool in git go jq ssh scp ssh-keygen ssh-agent ssh-add openssl curl tar python3 \
    bun yarn sha256sum htpasswd; do
    if ! command -v "$tool" >/dev/null 2>&1; then
      echo "missing required host tool: $tool" >&2
      failed=1
    fi
  done
  if command -v bun >/dev/null 2>&1 && [[ "$(bun --version 2>/dev/null || true)" != 1.3.11 ]]; then
    echo 'required local bundle tool version is unavailable: bun 1.3.11' >&2
    failed=1
  fi
  if command -v yarn >/dev/null 2>&1 && [[ "$(yarn --version 2>/dev/null || true)" != 1.22.22 ]]; then
    echo 'required local bundle tool version is unavailable: yarn 1.22.22' >&2
    failed=1
  fi
  if [[ -n "${WK_CHAT_LAB_CLOUD_TOOL:-}" ]]; then
    if ! require_executable "$(cloud_tool)"; then
      failed=1
    fi
  elif ! [[ -f "$(repository_root)/cmd/wkcloudlease/main.go" ]]; then
    echo 'local wkcloudlease source is unavailable' >&2
    failed=1
  fi
  if ! check_temporary_credentials; then
    failed=1
  fi
  if (( failed != 0 )); then
    return 1
  fi
  jq -n --arg credential_kind "$temporary_credential_kind" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_preflight/v1",ready:true,provider_contacted:false,credential_kind:$credential_kind}'
}

ensure_control_tools() {
  local directory="$1" root
  root="$(repository_root)"
  install -d -m 0700 "$directory/tools"
  install -m 0700 "$root/scripts/chat-lifecycle/portable-timeout.sh" "$directory/tools/timeout"
  if [[ -z "${WK_CHAT_LAB_CLOUD_TOOL:-}" ]]; then
    GOWORK=off go build -trimpath -o "$directory/tools/wkcloudlease" "$root/cmd/wkcloudlease"
  fi
  if [[ -z "${WK_CHAT_LAB_CHAT_TOOL:-}" ]]; then
    GOWORK=off go build -trimpath -o "$directory/tools/wkchatlifecycle" "$root/cmd/wkchatlifecycle"
  fi
  if [[ -z "${WK_CHAT_LAB_GATE_TOOL:-}" ]]; then
    GOWORK=off go build -trimpath -o "$directory/tools/wkcloudgate" "$root/cmd/wkcloudgate"
  fi
  if [[ -z "${WK_CHAT_LAB_BUNDLE_TOOL:-}" ]]; then
    GOWORK=off go build -trimpath -o "$directory/tools/wkcloudbundle" "$root/cmd/wkcloudbundle"
  fi
}

initialize_request_state() {
  local request_id="$1" directory source_sha root
  validate_request_id "$request_id"
  root="$(state_root)"
  install -d -m 0700 "$root"
  chmod 0700 "$root"
  root="$(cd "$root" && pwd -P)"
  directory="$root/$request_id"
  [[ ! -e "$directory" && ! -L "$directory" ]] || die "request state already exists: $request_id"
  install -d -m 0700 "$directory" "$directory/bundle"
  source_sha="$(git -C "$(repository_root)" rev-parse HEAD)"
  [[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || die 'current source revision is invalid'
  ssh-keygen -q -t ed25519 -N '' -C "wukongim-chat-lab-diagnostic-$request_id" -f "$directory/diagnostic_ed25519"
  ssh-keygen -q -t ed25519 -N '' -C "wukongim-chat-lab-deployment-$request_id" -f "$directory/deployment_ed25519"
  chmod 0600 "$directory/diagnostic_ed25519" "$directory/diagnostic_ed25519.pub" \
    "$directory/deployment_ed25519" "$directory/deployment_ed25519.pub"
  jq -n --arg schema 'wukongim.chat_lifecycle.direct_lab_state/v1' \
    --arg request_id "$request_id" --arg source_sha "$source_sha" \
    --arg created_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '{schema:$schema,request_id:$request_id,source_sha:$source_sha,created_at:$created_at,state:"preparing",generation:0}' \
    >"$directory/state.json"
  chmod 0600 "$directory/state.json"
  printf '%s\n' "$directory"
}

write_active_state() {
  local directory="$1" lease_id="$2" bundle_digest="$3" temporary
  temporary="$directory/.state.next.$$"
  jq --arg lease "$lease_id" --arg bundle "$bundle_digest" \
    '.state="active" | .lease_id=$lease | .bundle_digest=$bundle | .activated_at=(now | todateiso8601)' \
    "$directory/state.json" >"$temporary"
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$directory/state.json"
}

next_deployment_generation() {
  local directory="$1" generation
  generation=$(( $(jq -er '.generation // 0' "$directory/state.json") + 1 ))
  while [[ -e "$directory/generations/$generation" || -L "$directory/generations/$generation" ]]; do
    [[ -d "$directory/generations/$generation" && ! -L "$directory/generations/$generation" ]] ||
      die "invalid preserved generation entry: $generation"
    (( generation < 10000 )) || die 'deployment generation limit exceeded'
    generation=$((generation + 1))
  done
  printf '%s\n' "$generation"
}

start_request() {
  local request_id="$1" duration="${2:-$DEFAULT_QUALIFICATION_DURATION}" budget_value="${3:-$DEFAULT_BUDGET_CNY}"
  local budget_cny directory root source_sha builder chat cloud bundle_digest lease_id
  parse_duration_seconds "$duration" >/dev/null ||
    die "duration must be between 1m and 72h using whole h, m, or s units"
  budget_cny="$(parse_budget_cny "$budget_value")" ||
    die "budget must be a whole CNY amount between $MIN_BUDGET_CNY and $MAX_BUDGET_CNY"
  [[ "${WK_CHAT_LAB_PAID_AUTHORIZATION:-}" == create-paid-cloud-lease ]] ||
    die 'start requires WK_CHAT_LAB_PAID_AUTHORIZATION=create-paid-cloud-lease'
  require_budget_authorization "$budget_cny"
  check_temporary_credentials || die 'temporary Alibaba credentials are required before paid start'
  require_committed_candidate
  require_no_unreleased_request
  directory="$(initialize_request_state "$request_id")"
  write_run_policy "$directory" "$duration" "$budget_cny"
  root="$(repository_root)"
  require_committed_candidate
  ensure_control_tools "$directory"
  export PATH="$directory/tools:$PATH"
  builder="$(bundle_builder)"
  chat="$(chat_tool "$directory")"
  cloud="$(cloud_tool "$directory")"
  require_executable "$builder"
  require_executable "$chat"
  require_executable "$cloud"
  source_sha="$(jq -er .source_sha "$directory/state.json")"

  "$builder" --source-sha "$source_sha" --output-dir "$directory/bundle"
  bundle_digest="$(jq -er '.bundle_digest | select(test("^sha256:[0-9a-f]{64}$"))' "$directory/bundle/bundle-manifest-output.json")"
  [[ -f "$directory/bundle/cloud-deployment-bundle.tar.gz" ]] || die 'sealed offline bundle archive is unavailable'

  "$chat" materialize \
    --template "$directory/materialize-template.json" \
    --source-sha "$source_sha" --operator tangtaoit \
    --codex-diagnostic-pubkey "$(<"$directory/diagnostic_ed25519.pub")" \
    --request-id "$request_id" --repository WuKongIM/WuKongIM \
    --bundle-digest "$bundle_digest" \
    --deployment-pubkey "$(<"$directory/deployment_ed25519.pub")" \
    --now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --attempt 1 \
    >"$directory/run-plan.json"
  jq -c '.lease_plan' "$directory/run-plan.json" >"$directory/lease-plan.json"
  jq -c '{schema:"wukongim.cloud_lease.bootstrap_access/v1",access:.bootstrap_access}' \
    "$directory/run-plan.json" >"$directory/bootstrap-access.json"

  if ! "$cloud" quote --plan "$directory/lease-plan.json" >"$directory/quote.json"; then
    finalize_not_acquired "$directory" quote_failed_before_acquire
    die 'read-only Quote failed before paid Acquire; request was finalized as not acquired'
  fi
  jq -e --arg request "$request_id" '
    .schema == "wukongim.cloud_lease.quote/v1" and .quote.request_id == $request and
    .quote.capacity_available == true and .quote.quota_available == true and
    (.quote.plan_digest | test("^[0-9a-f]{64}$"))
  ' "$directory/quote.json" >/dev/null
  "$chat" selector-from-plan --plan "$directory/lease-plan.json" --quote "$directory/quote.json" \
    >"$directory/release-selector.json"

  if ! "$cloud" acquire --plan "$directory/lease-plan.json" --quote "$directory/quote.json" \
    --bootstrap-access "$directory/bootstrap-access.json" >"$directory/receipt.json"; then
    jq '.state="cleanup_required" | .failure="acquire_failed"' "$directory/state.json" >"$directory/.state.next.$$"
    chmod 0600 "$directory/.state.next.$$"
    mv -f -- "$directory/.state.next.$$" "$directory/state.json"
    die "paid Acquire failed; preserved exact selector at $directory/release-selector.json"
  fi
  jq -e --arg request "$request_id" '
    .schema == "wukongim.cloud_lease.receipt/v1" and .receipt.request_id == $request and
    .receipt.state == "active"
  ' "$directory/receipt.json" >/dev/null || die 'Acquire returned no active typed Receipt'
  "$chat" selector --receipt "$directory/receipt.json" >"$directory/receipt-selector.json"
  cmp -s <(jq -S -c .selector "$directory/release-selector.json") \
    <(jq -S -c .selector "$directory/receipt-selector.json") || die 'Receipt selector differs from the pre-Acquire release selector'
  lease_id="$(jq -er .receipt.lease_id "$directory/receipt.json")"
  chmod 0600 "$directory"/*.json
  write_active_state "$directory" "$lease_id" "$bundle_digest"
  jq -n --arg request_id "$request_id" --arg lease_id "$lease_id" --arg state_dir "$directory" \
    --slurpfile policy "$directory/run-policy.json" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_start/v1",request_id:$request_id,lease_id:$lease_id,
      state:"active",state_dir:$state_dir,run_policy:$policy[0]}'
}

deploy_request() {
  local request_id="$1" directory root generation generation_dir source_sha bundle_digest builder preparer
  local ssh_writer activator readiness gate deadline temporary
  directory="$(resolve_request_dir "$request_id")"
  jq -e '.schema == "wukongim.chat_lifecycle.direct_lab_state/v1" and
    (.state == "active" or .state == "deployed" or .state == "diagnosis_ready")' \
    "$directory/state.json" >/dev/null || die 'request is not in a deployable state'
  [[ -f "$directory/receipt.json" && ! -L "$directory/receipt.json" ]] || die 'active Lease Receipt is unavailable'
  [[ -f "$directory/deployment_ed25519" && ! -L "$directory/deployment_ed25519" ]] || die 'deployment identity is unavailable'
  require_committed_candidate
  root="$(repository_root)"
  ensure_control_tools "$directory"
  export PATH="$directory/tools:$PATH"
  generation="$(next_deployment_generation "$directory")"
  generation_dir="$directory/generations/$generation"
  [[ ! -e "$generation_dir" && ! -L "$generation_dir" ]] || die "generation already exists: $generation"
  install -d -m 0700 "$generation_dir/bundle"
  source_sha="$(git -C "$root" rev-parse HEAD)"
  [[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || die 'current candidate source revision is invalid'
  builder="$(bundle_builder)"
  require_executable "$builder"
  if (( generation == 1 )) && [[ "$(jq -er .source_sha "$directory/state.json")" == "$source_sha" ]] &&
    [[ -f "$directory/bundle/cloud-deployment-bundle.tar.gz" && -f "$directory/bundle/bundle-manifest-output.json" ]]; then
    install -m 0600 "$directory/bundle/cloud-deployment-bundle.tar.gz" "$generation_dir/bundle/cloud-deployment-bundle.tar.gz"
    install -m 0600 "$directory/bundle/bundle-manifest-output.json" "$generation_dir/bundle/bundle-manifest-output.json"
    if [[ -f "$directory/bundle/cloud-deployment-bundle.tar.gz.sha256" ]]; then
      install -m 0600 "$directory/bundle/cloud-deployment-bundle.tar.gz.sha256" \
        "$generation_dir/bundle/cloud-deployment-bundle.tar.gz.sha256"
    fi
  else
    "$builder" --source-sha "$source_sha" --output-dir "$generation_dir/bundle"
  fi
  bundle_digest="$(jq -er '.bundle_digest | select(test("^sha256:[0-9a-f]{64}$"))' \
    "$generation_dir/bundle/bundle-manifest-output.json")"

  preparer="${WK_CHAT_LAB_DEPLOY_PREPARER:-$root/scripts/cloud-deployment/prepare-local-runtime.sh}"
  require_executable "$preparer"
  WK_CHAT_LAB_REQUEST_DIR="$directory" \
    WK_CHAT_LAB_GENERATION_DIR="$generation_dir" \
    WK_CHAT_LAB_GENERATION="$generation" \
    WK_CHAT_LAB_SOURCE_SHA="$source_sha" \
    WK_CHAT_LAB_GATE_TOOL="$(gate_tool "$directory")" \
    WK_CHAT_LAB_BUNDLE_TOOL="${WK_CHAT_LAB_BUNDLE_TOOL:-$directory/tools/wkcloudbundle}" \
    "$preparer"
  for name in deployment-plan.json runtime-node.tar.gz runtime-load.tar.gz readiness-credentials; do
    [[ -f "$generation_dir/$name" && ! -L "$generation_dir/$name" ]] || die "deployment preparation omitted $name"
  done

  export WK_CLOUD_DEPLOYMENT_PLAN="$generation_dir/deployment-plan.json"
  export WK_CLOUD_BUNDLE_ARCHIVE="$generation_dir/bundle/cloud-deployment-bundle.tar.gz"
  export WK_CLOUD_RUNTIME_NODE_ARCHIVE="$generation_dir/runtime-node.tar.gz"
  export WK_CLOUD_RUNTIME_LOAD_ARCHIVE="$generation_dir/runtime-load.tar.gz"
  export WK_CLOUD_SSH_KEY="$directory/deployment_ed25519"
  export WK_CLOUD_SSH_CONFIG="$directory/deployment-ssh-config"
  export WK_CLOUD_LOAD_PUBLIC_IP="$(jq -er '.hosts[] | select(.role=="load") | .public_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
  export WK_CLOUD_SERVICE1_IP="$(jq -er '.hosts[] | select(.role=="service-1") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
  export WK_CLOUD_SERVICE2_IP="$(jq -er '.hosts[] | select(.role=="service-2") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
  export WK_CLOUD_SERVICE3_IP="$(jq -er '.hosts[] | select(.role=="service-3") | .private_address' "$WK_CLOUD_DEPLOYMENT_PLAN")"
  export WK_CLOUD_FAILURE_OUTPUT="$generation_dir/deployment-failure-state.json"
  export WK_CLOUD_LAST_GATE_OUTPUT="$generation_dir/last-completed-gate.txt"
  export WK_CLOUD_READINESS_OUTPUT="$generation_dir/readiness-snapshot.json"

  ssh_writer="${WK_CHAT_LAB_SSH_CONFIG_WRITER:-$root/scripts/cloud-deployment/write-ssh-config.sh}"
  activator="${WK_CHAT_LAB_ACTIVATOR:-$root/scripts/cloud-deployment/activate-hosts.sh}"
  readiness="${WK_CHAT_LAB_READINESS:-$root/scripts/cloud-deployment/collect-readiness.sh}"
  require_executable "$ssh_writer"
  require_executable "$activator"
  require_executable "$readiness"
  "$ssh_writer"
  "$activator"

  # shellcheck disable=SC1090
  source "$generation_dir/readiness-credentials"
  gate="$(gate_tool "$directory")"
  require_executable "$gate"
  deadline=$(( $(date -u +%s) + ${WK_CHAT_LAB_READINESS_TIMEOUT_SECONDS:-1200} ))
  while true; do
    if "$readiness" && "$gate" deployment-gate \
      --lease-receipt "$directory/receipt.json" \
      --plan "$generation_dir/deployment-plan.json" \
      --bundle-manifest "$generation_dir/bundle-root/bundle-manifest.json" \
      --snapshot "$generation_dir/readiness-snapshot.json" \
      >"$generation_dir/deployment-outcome.json"; then
      break
    fi
    (( $(date -u +%s) < deadline )) || die 'deployment readiness deadline elapsed'
    sleep "${WK_CHAT_LAB_READINESS_POLL_SECONDS:-10}"
  done
  jq -e '.passed == true and .receipt.schema == "wukongim.cloud_deployment.receipt/v2"' \
    "$generation_dir/deployment-outcome.json" >/dev/null || die 'deployment gate did not pass'
  temporary="$directory/.state.next.$$"
  jq --argjson generation "$generation" --arg source "$source_sha" --arg bundle "$bundle_digest" \
    --arg plan "$(jq -er .plan_digest "$generation_dir/deployment-plan.json")" \
    '.state="deployed" | .generation=$generation | .source_sha=$source | .bundle_digest=$bundle |
      .deployment_plan_digest=$plan | .deployed_at=(now | todateiso8601)' \
    "$directory/state.json" >"$temporary"
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$directory/state.json"
  jq -n --arg request_id "$request_id" --argjson generation "$generation" \
    --arg source_sha "$source_sha" --arg state_dir "$directory" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_deploy/v1",request_id:$request_id,generation:$generation,source_sha:$source_sha,state:"deployed",state_dir:$state_dir}'
}

write_run_state() {
  local directory="$1" state="$2" reason="${3:-}" temporary
  temporary="$directory/.state.next.$$"
  jq --arg state "$state" --arg reason "$reason" '
    .state=$state | .run_updated_at=(now | todateiso8601) |
    if $reason == "" then del(.reason) else .reason=$reason end
  ' "$directory/state.json" >"$temporary"
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$directory/state.json"
}

stop_remote_rehearsal_best_effort() {
  local directory="$1" timeout_tool
  timeout_tool="$directory/tools/timeout"
  if [[ ! -x "$timeout_tool" ]]; then
    timeout_tool="$(repository_root)/scripts/chat-lifecycle/portable-timeout.sh"
  fi
  [[ -x "$timeout_tool" ]] || return 0
  "$timeout_tool" --kill-after=5s 20s ssh -F "$directory/deployment-ssh-config" wukong-load \
    "sudo systemctl stop wkbench-rehearsal.service || true" >/dev/null 2>&1 || true
}

run_request() {
  local request_id="$1" directory root generation generation_dir starter chat monitor run_start started_at
  local repair_state monitor_status reason duration_seconds max_seconds started_epoch expected_epoch
  directory="$(resolve_request_dir "$request_id")"
  jq -e '.schema == "wukongim.chat_lifecycle.direct_lab_state/v1" and .state == "deployed" and .generation > 0' \
    "$directory/state.json" >/dev/null || die 'request must have one gated deployment before a stability run'
  generation="$(jq -er .generation "$directory/state.json")"
  generation_dir="$directory/generations/$generation"
  [[ -d "$generation_dir" && ! -L "$generation_dir" ]] || die 'current deployment generation is unavailable'
  [[ -f "$directory/deployment-ssh-config" && ! -L "$directory/deployment-ssh-config" ]] ||
    die 'deployment SSH config is unavailable'
  [[ -f "$directory/run-policy.json" && ! -L "$directory/run-policy.json" ]] ||
    die 'immutable run policy is unavailable'
  jq -e --argjson reserve "$QUALIFICATION_RESERVE_SECONDS" '
    .schema == "wukongim.chat_lifecycle.direct_lab_run_policy/v1" and
    (.duration_seconds | type == "number" and . >= 60 and . <= 259200) and
    (.max_duration_seconds == .duration_seconds + $reserve) and
    (.qualification_reserve_seconds == $reserve)
  ' "$directory/run-policy.json" >/dev/null || die 'immutable run policy is invalid'
  duration_seconds="$(jq -er .duration_seconds "$directory/run-policy.json")"
  max_seconds="$(jq -er .max_duration_seconds "$directory/run-policy.json")"
  root="$(repository_root)"
  starter="${WK_CHAT_LAB_STAGE_STARTER:-$root/scripts/chat-lifecycle/start-local-stage.sh}"
  chat="$(chat_tool "$directory")"
  monitor="${WK_CHAT_LAB_REPAIR_MONITOR:-$root/scripts/chat-lifecycle/repair-monitor.sh}"
  require_executable "$starter"
  require_executable "$chat"
  require_executable "$monitor"
  run_start="$generation_dir/run-start.json"
  WK_CHAT_LAB_SSH_CONFIG="$directory/deployment-ssh-config" \
    WK_CHAT_LAB_RUN_START_OUTPUT="$run_start" \
    WK_CHAT_LAB_STAGE_SERVICE=wkbench-rehearsal.service \
    WK_CHAT_LAB_STAGE_REPORT_DIR=rehearsal \
    "$starter"
  if ! jq -e '
    .schema == "wukongim.chat_lifecycle.run_start/v1" and .stage == "rehearsal" and
    (.started_at | type == "string") and (.expected_end_at | type == "string") and
    (.run_hash | test("^sha256:[0-9a-f]{64}$")) and
    (.assignment_hash | test("^sha256:[0-9a-f]{64}$")) and .generation > 0
  ' "$run_start" >/dev/null; then
    stop_remote_rehearsal_best_effort "$directory"
    die 'remote stage did not publish a valid run-start document'
  fi
  if ! started_at="$(normalize_rfc3339_utc "$(jq -er .started_at "$run_start")")"; then
    stop_remote_rehearsal_best_effort "$directory"
    die 'remote stage published a non-normalizable run-start timestamp'
  fi
  if ! started_epoch="$(rfc3339_epoch "$(jq -er .started_at "$run_start")")"; then
    stop_remote_rehearsal_best_effort "$directory"
    die 'remote stage published an invalid start instant'
  fi
  if ! expected_epoch="$(rfc3339_epoch "$(jq -er .expected_end_at "$run_start")")"; then
    stop_remote_rehearsal_best_effort "$directory"
    die 'remote stage published an invalid expected end instant'
  fi
  if (( expected_epoch - started_epoch != max_seconds )); then
    stop_remote_rehearsal_best_effort "$directory"
    die 'remote workload duration differs from the immutable run policy'
  fi
  repair_state="$generation_dir/repair-state.json"
  if ! "$chat" repair-begin \
    --request-id "$request_id" --lease-id "$(jq -er .lease_id "$directory/state.json")" \
    --generation "$generation" --source-sha "$(jq -er .source_sha "$directory/state.json")" \
    --bundle-digest "$(jq -er .bundle_digest "$directory/state.json")" \
    --started-at "$started_at" \
    --target-online 10000 --minimum-online-percent 95 \
    --minimum-send-rate 1900 --maximum-ack-backlog 4000 \
    --warmup-timeout 5m --stall-after 15s --qualify-after "${duration_seconds}s" \
    >"$repair_state"; then
    stop_remote_rehearsal_best_effort "$directory"
    die 'failed to initialize the bounded repair monitor state'
  fi
  chmod 0600 "$repair_state"
  rm -f -- "$directory/stop-requested"
  write_run_state "$directory" running
  monitor_status=0
  WK_CHAT_REPAIR_TOOL="$chat" \
    WK_CHAT_REPAIR_STATE="$repair_state" \
    WK_CHAT_REPAIR_OUTPUT_DIR="$generation_dir" \
    WK_CHAT_REPAIR_SSH_CONFIG="$directory/deployment-ssh-config" \
    WK_CHAT_REPAIR_REQUEST_ID="$request_id" \
    WK_CHAT_REPAIR_MAX_SECONDS="$max_seconds" \
    WK_CHAT_REPAIR_POLL_SECONDS="${WK_CHAT_LAB_POLL_SECONDS:-5}" \
    WK_CHAT_REPAIR_SERVICE=wkbench-rehearsal.service \
    WK_CHAT_REPAIR_OPERATOR_STOP_FILE="$directory/stop-requested" \
    "$monitor" || monitor_status=$?
  case "$monitor_status" in
    0)
      write_run_state "$directory" qualified
      jq -n --arg request_id "$request_id" --argjson generation "$generation" \
        '{schema:"wukongim.chat_lifecycle.direct_lab_run/v1",request_id:$request_id,generation:$generation,state:"qualified",official_evidence_eligible:false}'
      ;;
    10)
      reason="$(jq -er '.decision.reason' "$generation_dir/repair-decision.json")"
      write_run_state "$directory" diagnosis_ready "$reason"
      jq -n --arg request_id "$request_id" --argjson generation "$generation" --arg reason "$reason" \
        '{schema:"wukongim.chat_lifecycle.direct_lab_run/v1",request_id:$request_id,generation:$generation,state:"diagnosis_ready",reason:$reason,lease_retained:true}'
      return 10
      ;;
    130)
      write_run_state "$directory" deployed operator_stop
      return 130
      ;;
    *)
      write_run_state "$directory" diagnosis_ready monitor_failed
      return "$monitor_status"
      ;;
  esac
}

diagnose_request() {
  local request_id="$1" directory root collector diagnosis_dir
  directory="$(resolve_request_dir "$request_id")"
  [[ -f "$directory/deployment-ssh-config" && ! -L "$directory/deployment-ssh-config" ]] ||
    die 'deployment SSH config is unavailable for live diagnosis'
  root="$(repository_root)"
  if [[ -d "$directory/tools" ]]; then
    export PATH="$directory/tools:$PATH"
  fi
  collector="${WK_CHAT_LAB_DIAGNOSIS_COLLECTOR:-$root/scripts/chat-lifecycle/collect-local-diagnosis.sh}"
  require_executable "$collector"
  diagnosis_dir="$directory/diagnoses/$(date -u +%Y%m%dT%H%M%SZ)"
  install -d -m 0700 "$diagnosis_dir"
  WK_CHAT_LAB_DIAGNOSIS_DIR="$diagnosis_dir" \
    WK_CHAT_LAB_SSH_CONFIG="$directory/deployment-ssh-config" \
    WK_CHAT_LAB_REQUEST_ID="$request_id" \
    "$collector"
  [[ -f "$diagnosis_dir/summary.json" && ! -L "$diagnosis_dir/summary.json" ]] ||
    die 'diagnosis collector did not publish its bounded summary'
  jq -n --slurpfile state "$directory/state.json" --slurpfile diagnosis "$diagnosis_dir/summary.json" \
    --arg evidence_dir "$diagnosis_dir" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_diagnose/v1",state:$state[0],diagnosis:$diagnosis[0],evidence_dir:$evidence_dir}'
}

write_released_state() {
  local directory="$1" temporary
  temporary="$directory/.state.next.$$"
  jq '.state="released" | .released_at=(now | todateiso8601)' "$directory/state.json" >"$temporary"
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$directory/state.json"
}

finalize_not_acquired() {
  local directory="$1" reason="$2" output temporary
  [[ "$(jq -er .state "$directory/state.json")" == preparing ]] ||
    die 'only a preparing request can be finalized without provider inventory'
  [[ ! -e "$directory/release-selector.json" && ! -L "$directory/release-selector.json" ]] ||
    die 'a request with a release selector requires provider-backed Release'
  [[ ! -e "$directory/receipt.json" && ! -L "$directory/receipt.json" ]] ||
    die 'a request with an Acquire receipt requires provider-backed Release'
  output="$directory/zero-inventory.json"
  temporary="$directory/.zero-inventory.next.$$"
  jq -n --slurpfile state "$directory/state.json" --arg reason "$reason" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_not_acquired/v1",request_id:$state[0].request_id,
      source_sha:$state[0].source_sha,acquire_invoked:false,residual_resources:0,
      basis:"release_selector_absent_before_acquire",reason:$reason,observed_at:(now | todateiso8601)}' \
    >"$temporary"
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$output"
  temporary="$directory/.state.next.$$"
  jq --arg reason "$reason" \
    '.state="released" | .failure=$reason | .acquire_invoked=false | .released_at=(now | todateiso8601)' \
    "$directory/state.json" >"$temporary"
  chmod 0600 "$temporary"
  mv -f -- "$temporary" "$directory/state.json"
}

stop_request() {
  local request_id="$1" directory selector output temporary cloud
  directory="$(resolve_request_dir "$request_id")"
  selector="$directory/release-selector.json"
  if [[ ! -f "$selector" || -L "$selector" ]]; then
    finalize_not_acquired "$directory" operator_finalized_pre_acquire_failure
    jq -n --arg request_id "$request_id" --arg proof "$directory/zero-inventory.json" \
      '{schema:"wukongim.chat_lifecycle.direct_lab_stop/v1",request_id:$request_id,state:"released",acquire_invoked:false,zero_inventory_proof:$proof}'
    return
  fi
  check_temporary_credentials || die 'temporary Alibaba credentials are required for exact Release'
  cloud="$(cloud_tool "$directory")"
  require_executable "$cloud"

  : >"$directory/stop-requested"
  chmod 0600 "$directory/stop-requested"

  if [[ -f "$directory/deployment-ssh-config" && ! -L "$directory/deployment-ssh-config" ]]; then
    ssh -F "$directory/deployment-ssh-config" wukong-load \
      "sudo systemctl stop wkbench-rehearsal.service || true" >/dev/null 2>&1 || true
  fi

  temporary="$directory/.zero-inventory.next.$$"
  rm -f -- "$temporary"
  if ! "$cloud" release --selector "$selector" >"$temporary"; then
    rm -f -- "$temporary"
    die 'provider Release did not complete; request state was preserved for retry'
  fi
  chmod 0600 "$temporary"
  jq -e --slurpfile expected "$selector" --arg request "$request_id" '
    .schema == "wukongim.cloud_lease.release/v1" and
    .result.zero_inventory.selector == $expected[0].selector and
    .result.zero_inventory.selector.request_id == $request and
    (.result.zero_inventory.account_id_hash | test("^sha256:[0-9a-f]{64}$")) and
    (.result.zero_inventory.observed_at | type == "string") and
    (.result.zero_inventory.scopes | type == "array" and length > 0)
  ' "$temporary" >/dev/null || {
    rm -f -- "$temporary"
    die 'Release returned no authenticated exact zero-inventory proof'
  }
  output="$directory/zero-inventory.json"
  mv -f -- "$temporary" "$output"
  write_released_state "$directory"
  jq -n --arg request_id "$request_id" --arg proof "$output" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_stop/v1",request_id:$request_id,state:"released",zero_inventory_proof:$proof}'
}

show_status() {
  local directory
  directory="$(resolve_request_dir "$1")"
  jq -n --slurpfile state "$directory/state.json" --slurpfile policy "$directory/run-policy.json" \
    --arg zero "$([[ -f "$directory/zero-inventory.json" ]] && printf true || printf false)" \
    '{schema:"wukongim.chat_lifecycle.direct_lab_status/v1",state:$state[0],run_policy:$policy[0],zero_inventory_proven:($zero=="true")}'
}

operation="${1:-}"
case "$operation" in
  preflight)
    [[ $# -eq 1 ]] || { usage; exit 2; }
    preflight
    ;;
  stop)
    [[ $# -eq 2 ]] || { usage; exit 2; }
    stop_request "$2"
    ;;
  status)
    [[ $# -eq 2 ]] || { usage; exit 2; }
    show_status "$2"
    ;;
  start)
    [[ $# -ge 2 ]] || { usage; exit 2; }
    request_id="$2"
    duration="$DEFAULT_QUALIFICATION_DURATION"
    budget_cny="$DEFAULT_BUDGET_CNY"
    duration_seen=false
    budget_seen=false
    shift 2
    while (( $# > 0 )); do
      case "$1" in
        --duration)
          [[ "$duration_seen" == false && $# -ge 2 ]] || { usage; exit 2; }
          duration="$2"
          duration_seen=true
          shift 2
          ;;
        --budget-cny)
          [[ "$budget_seen" == false && $# -ge 2 ]] || { usage; exit 2; }
          budget_cny="$2"
          budget_seen=true
          shift 2
          ;;
        *)
          usage
          exit 2
          ;;
      esac
    done
    start_request "$request_id" "$duration" "$budget_cny"
    ;;
  deploy|run|diagnose)
    [[ $# -eq 2 ]] || { usage; exit 2; }
    case "$operation" in
      deploy) deploy_request "$2" ;;
      run) run_request "$2" ;;
      diagnose) diagnose_request "$2" ;;
      *) die "$operation is not available until its direct local contract is installed" ;;
    esac
    ;;
  *)
    usage
    exit 2
    ;;
esac
