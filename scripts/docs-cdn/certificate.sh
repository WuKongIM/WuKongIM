#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly expected_domain="docs.githubim.com"
readonly expected_cdn_cname="docs.githubim.com.w.kunlunaq.com"
readonly expected_pages_cname="wukongim.github.io"
readonly expected_acme_server="https://acme-v02.api.letsencrypt.org/directory"
readonly renewal_window_seconds="$((30 * 24 * 60 * 60))"
readonly certificate_helper="${DOCS_CERTIFICATE_HELPER:-docs-cdn-certificate-helper}"
readonly force_renew="${DOCS_CERT_FORCE_RENEW:-false}"

fail() {
  printf 'docs CDN certificate: %s\n' "$1" >&2
  exit 1
}

[[ $# -eq 1 && ("$1" == inspect || "$1" == rotate) ]] || \
  fail "usage: $0 inspect|rotate"
readonly operation="$1"

[[ "$force_renew" == true || "$force_renew" == false ]] || \
  fail "DOCS_CERT_FORCE_RENEW must be true or false"
if [[ "$force_renew" == true && "${GITHUB_EVENT_NAME:-}" != workflow_dispatch ]]; then
  fail "forced renewal is allowed only from workflow_dispatch"
fi

account_bundle_value=""
if [[ "$operation" == rotate ]]; then
  [[ -n "${DOCS_ACME_EMAIL:-}" ]] || fail "DOCS_ACME_EMAIL is required"
  [[ -n "${DOCS_ACME_ACCOUNT_BUNDLE_B64:-}" ]] || fail "DOCS_ACME_ACCOUNT_BUNDLE_B64 is required"
  account_bundle_value="$DOCS_ACME_ACCOUNT_BUNDLE_B64"
fi
# Never pass the encoded Environment secret to child processes.
unset DOCS_ACME_ACCOUNT_BUNDLE_B64

[[ "${DOCS_CDN_ENABLED:-}" == true ]] || fail "DOCS_CDN_ENABLED must be exactly true"
[[ "${DOCS_CDN_DOMAIN:-}" == "$expected_domain" ]] || \
  fail "DOCS_CDN_DOMAIN must be exactly ${expected_domain}"
[[ "${DOCS_CDN_CNAME:-}" == "$expected_cdn_cname" ]] || \
  fail "DOCS_CDN_CNAME must be exactly ${expected_cdn_cname}"
case "${DOCS_CDN_PUBLIC_ROUTE_MODE:-}" in
  github-pages-precutover | alibaba-cdn) ;;
  *) fail "DOCS_CDN_PUBLIC_ROUTE_MODE must be github-pages-precutover or alibaba-cdn" ;;
esac
readonly public_route_mode="$DOCS_CDN_PUBLIC_ROUTE_MODE"
[[ -n "${ALIBABA_CLOUD_ACCESS_KEY_ID:-}" ]] || fail "temporary Alibaba access key ID is required"
[[ -n "${ALIBABA_CLOUD_ACCESS_KEY_SECRET:-}" ]] || fail "temporary Alibaba access key secret is required"
[[ -n "${ALIBABA_CLOUD_SECURITY_TOKEN:-}" ]] || fail "temporary Alibaba security token is required"
[[ "${RUNNER_TEMP:-}" == /* && "${RUNNER_TEMP}" != / ]] || \
  fail "RUNNER_TEMP must be an absolute non-root directory"

for command in aliyun awk dig grep jq mktemp openssl rm sed sha256sum sleep timeout tr "$certificate_helper"; do
  command -v "$command" >/dev/null 2>&1 || fail "$command is required"
done

readonly temporary_directory="$(mktemp -d "${RUNNER_TEMP}/docs-cdn-certificate.XXXXXX")"
trap 'rm -rf -- "$temporary_directory"' EXIT
readonly api_error_path="${temporary_directory}/aliyun-error.log"
readonly current_response_path="${temporary_directory}/current-certificate.json"

export ALIBABA_CLOUD_IGNORE_PROFILE=TRUE
export ALIBABA_CLOUD_DISABLE_EXTERNAL_PROCESS=TRUE
unset DEBUG

describe_certificate() {
  local output_path="$1"
  if ! timeout 60 aliyun --region cn-hangzhou cdn DescribeDomainCertificateInfo \
    --DomainName "$expected_domain" >"$output_path" 2>"$api_error_path"; then
    : >"$api_error_path"
    fail "Alibaba Cloud rejected the certificate inspection request"
  fi
  : >"$api_error_path"
}

inspect_current_certificate() {
  local allow_missing="$1"
  local -a helper_arguments=(inspect-cdn --response "$current_response_path")
  if [[ "$allow_missing" == true ]]; then
    helper_arguments+=(--allow-missing)
  fi
  describe_certificate "$current_response_path"
  "$certificate_helper" "${helper_arguments[@]}"
}

verify_public_edge() {
  local expected_fingerprint="$1"
  local attempt_count="$2"
  local delay_seconds="$3"
  [[ "$expected_fingerprint" =~ ^[0-9a-f]{64}$ ]] || return 2
  [[ "$attempt_count" =~ ^[0-9]+$ && "$delay_seconds" =~ ^[0-9]+$ ]] || return 2
  (( attempt_count >= 1 && attempt_count <= 40 && delay_seconds <= 60 )) || return 2

  local edge_response_path="${temporary_directory}/edge-response.pem"
  local edge_leaf_path="${temporary_directory}/edge-leaf.pem"
  local attempt edge_fingerprint
  for ((attempt = 1; attempt <= attempt_count; attempt++)); do
    if (( attempt > 1 )); then
      sleep "$delay_seconds"
    fi
    if timeout 20 openssl s_client \
        -connect "${expected_domain}:443" \
        -servername "$expected_domain" \
        -showcerts \
        -verify_hostname "$expected_domain" \
        -verify_return_error \
        -CApath /etc/ssl/certs \
        </dev/null >"$edge_response_path" 2>/dev/null &&
      awk '/-----BEGIN CERTIFICATE-----/{capture=1} capture{print} /-----END CERTIFICATE-----/{exit}' \
        "$edge_response_path" >"$edge_leaf_path" &&
      openssl x509 -in "$edge_leaf_path" -noout -checkhost "$expected_domain" >/dev/null 2>&1; then
      if ! edge_fingerprint="$(openssl x509 -in "$edge_leaf_path" -noout -fingerprint -sha256 2>/dev/null |
        sed -E 's/^[^=]+=//' | tr -d ':' | tr '[:upper:]' '[:lower:]')"; then
        continue
      fi
      if [[ "$edge_fingerprint" == "$expected_fingerprint" ]]; then
        return 0
      fi
    fi
  done
  return 1
}

query_public_cname() {
  local resolver="$1"
  local raw_answer normalized_answer
  raw_answer="$(timeout 10 dig "@${resolver}" "$expected_domain" CNAME +short +time=3 +tries=1)" || return 1
  normalized_answer="$(LC_ALL=C awk '
    {
      line = $0
      sub(/\r$/, "", line)
      if (line ~ /^[[:space:]]*$/) {
        next
      }
      if (split(line, fields, /[[:space:]]+/) != 1) {
        invalid = 1
        next
      }
      answer = tolower(fields[1])
      sub(/\.$/, "", answer)
      count++
    }
    END {
      if (invalid || count != 1) {
        exit 1
      }
      print answer
    }
  ' <<<"$raw_answer")" || return 1
  [[ -n "$normalized_answer" && ${#normalized_answer} -le 253 ]] || return 1
  printf '%s\n' "$normalized_answer"
}

observe_public_cname() {
  local resolver answer observed_answer=""
  local -a resolvers=(223.5.5.5 1.1.1.1 8.8.8.8)
  for resolver in "${resolvers[@]}"; do
    answer="$(query_public_cname "$resolver")" || return 1
    if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
      printf -- '- Public CNAME via `%s`: `%s`\n' "$resolver" "$answer" >>"$GITHUB_STEP_SUMMARY"
    fi
    if [[ -z "$observed_answer" ]]; then
      observed_answer="$answer"
    elif [[ "$answer" != "$observed_answer" ]]; then
      return 1
    fi
  done
  printf '%s\n' "$observed_answer"
}

assess_public_edge() {
  local expected_fingerprint="$1"
  local attempt_count="$2"
  local delay_seconds="$3"
  local observed_cname expected_public_cname

  # Alibaba's DomainCnameStatus is provider diagnostics, not public DNS truth.
  # Require one direct, matching CNAME answer from every fixed public resolver.
  observed_cname="$(observe_public_cname)" || return 2
  case "$public_route_mode" in
    github-pages-precutover)
      expected_public_cname="$expected_pages_cname"
      ;;
    alibaba-cdn)
      expected_public_cname="$DOCS_CDN_CNAME"
      ;;
    *)
      return 2
      ;;
  esac
  [[ "$observed_cname" == "$expected_public_cname" ]] || return 2

  case "$public_route_mode" in
    alibaba-cdn)
      verify_public_edge "$expected_fingerprint" "$attempt_count" "$delay_seconds" || return
      printf 'passed\n'
      ;;
    github-pages-precutover)
      printf 'skipped-public-dns-not-on-alibaba-cdn\n'
      ;;
  esac
}

write_public_edge_summary() {
  local edge_verification="$1"
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf -- '- Public edge verification: `%s`\n' "$edge_verification" >>"$GITHUB_STEP_SUMMARY"
  fi
}

write_inspection_outputs() {
  local summary="$1"
  local certificate_present days_remaining domain_cname_status fingerprint not_after renewal_required seconds_remaining
  certificate_present="$(jq -er '.certificate_present | booleans | tostring' <<<"$summary")" || \
    fail "invalid certificate presence"
  fingerprint="$(jq -er '.fingerprint | strings' <<<"$summary")" || fail "invalid inspection fingerprint"
  domain_cname_status="$(jq -er '.domain_cname_status | strings' <<<"$summary")" || \
    fail "invalid inspection CNAME status"
  days_remaining="$(jq -er '.days_remaining | numbers' <<<"$summary")" || fail "invalid inspection days"
  not_after="$(jq -er '.not_after | strings' <<<"$summary")" || fail "invalid inspection expiry"
  renewal_required="$(jq -er '.renewal_required | booleans | tostring' <<<"$summary")" || \
    fail "invalid renewal decision"
  seconds_remaining="$(jq -er '.seconds_remaining | numbers' <<<"$summary")" || fail "invalid inspection seconds"
  if [[ "$certificate_present" == true ]]; then
    [[ "$not_after" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T ]] || fail "invalid inspection expiry"
    [[ "$fingerprint" =~ ^[0-9a-f]{64}$ ]] || fail "invalid inspection fingerprint"
    [[ "$domain_cname_status" == ok || "$domain_cname_status" == cname_error ||
      "$domain_cname_status" == top_domain_cname_error ]] || fail "invalid inspection CNAME status"
  else
    [[ "$force_renew" == true && "$not_after" == missing && "$renewal_required" == true &&
      "$days_remaining" == 0 && "$seconds_remaining" == 0 && -z "$fingerprint" &&
      -z "$domain_cname_status" ]] || \
      fail "missing certificate is allowed only for an explicit forced bootstrap"
  fi
  if [[ -n "${GITHUB_OUTPUT:-}" ]]; then
    {
      printf 'certificate_present=%s\n' "$certificate_present"
      printf 'fingerprint=%s\n' "$fingerprint"
      printf 'domain_cname_status=%s\n' "$domain_cname_status"
      printf 'days_remaining=%s\n' "$days_remaining"
      printf 'not_after=%s\n' "$not_after"
      printf 'renewal_required=%s\n' "$renewal_required"
      printf 'seconds_remaining=%s\n' "$seconds_remaining"
    } >>"$GITHUB_OUTPUT"
  fi
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    {
      printf '### Documentation CDN certificate\n\n'
      printf -- '- Domain: `%s`\n' "$expected_domain"
      printf -- '- Certificate installed: `%s`\n' "$certificate_present"
      printf -- '- Current expiry: `%s`\n' "$not_after"
      printf -- '- Whole days remaining: `%s`\n' "$days_remaining"
      printf -- '- Renewal required: `%s`\n' "$renewal_required"
      printf -- '- Alibaba API leaf SHA-256: `%s`\n' "${fingerprint:-not-applicable}"
      printf -- '- Alibaba CNAME status: `%s`\n' "${domain_cname_status:-not-applicable}"
    } >>"$GITHUB_STEP_SUMMARY"
  fi
}

if [[ "$operation" == inspect ]]; then
  inspection_summary="$(inspect_current_certificate "$force_renew")" || fail "could not validate the active CDN certificate"
  write_inspection_outputs "$inspection_summary"
  inspection_certificate_present="$(jq -er '.certificate_present | booleans | tostring' \
    <<<"$inspection_summary")" || \
    fail "invalid certificate presence"
  edge_verification="skipped-no-certificate-installed"
  if [[ "$inspection_certificate_present" == true ]]; then
    inspection_fingerprint="$(jq -er '.fingerprint | strings | select(test("^[0-9a-f]{64}$"))' \
      <<<"$inspection_summary")" || fail "invalid inspection fingerprint"
    if ! edge_verification="$(assess_public_edge "$inspection_fingerprint" 3 10)"; then
      write_public_edge_summary "failed"
      fail "the public CNAME route or trusted edge certificate does not match the declared route mode"
    fi
  fi
  write_public_edge_summary "$edge_verification"
  printf '%s\n' "$inspection_summary"
  exit 0
fi

command -v lego >/dev/null 2>&1 || fail "the pinned lego executable is required"

inspection_summary="$(inspect_current_certificate "$force_renew")" || fail "could not validate the active CDN certificate"
certificate_present="$(jq -er '.certificate_present | booleans | tostring' \
  <<<"$inspection_summary")" || \
  fail "invalid certificate presence"
renewal_required="$(jq -er '.renewal_required | booleans | tostring' <<<"$inspection_summary")" || \
  fail "invalid renewal decision"
seconds_remaining="$(jq -er '.seconds_remaining | numbers' <<<"$inspection_summary")" || \
  fail "invalid remaining validity"
if [[ "$renewal_required" != true && "$force_renew" != true ]]; then
  fail "refusing renewal outside the fixed 30-day window without an explicit manual force"
fi
if [[ "$certificate_present" == false && "$force_renew" != true ]]; then
  fail "refusing a missing-certificate bootstrap without an explicit manual force"
fi
if [[ "$certificate_present" == true ]] && (( seconds_remaining > renewal_window_seconds )) && [[ "$force_renew" != true ]]; then
  fail "renewal threshold and certificate lifetime disagree"
fi

"$certificate_helper" verify-delegation >/dev/null || \
  fail "the fixed ACME challenge CNAME delegation is missing"

readonly account_bundle_path="${temporary_directory}/account.bundle.b64"
readonly lego_state_path="${temporary_directory}/lego-state"
printf '%s' "$account_bundle_value" >"$account_bundle_path"
unset account_bundle_value
"$certificate_helper" restore-account \
  --bundle "$account_bundle_path" \
  --email "$DOCS_ACME_EMAIL" \
  --state "$lego_state_path" || fail "the persisted ACME account identity is invalid"
rm -f -- "$account_bundle_path"

export ALICLOUD_ACCESS_KEY="$ALIBABA_CLOUD_ACCESS_KEY_ID"
export ALICLOUD_SECRET_KEY="$ALIBABA_CLOUD_ACCESS_KEY_SECRET"
export ALICLOUD_SECURITY_TOKEN="$ALIBABA_CLOUD_SECURITY_TOKEN"
export ALICLOUD_REGION_ID="cn-hangzhou"
export ALICLOUD_TTL="600"
export ALICLOUD_PROPAGATION_TIMEOUT="900"
export ALICLOUD_POLLING_INTERVAL="15"
export ALICLOUD_HTTP_TIMEOUT="30"
unset LEGO_DEBUG_ACME_HTTP_CLIENT LEGO_DISABLE_CNAME_SUPPORT

timeout 20m lego \
  --server "$expected_acme_server" \
  --email "$DOCS_ACME_EMAIL" \
  --path "$lego_state_path" \
  --key-type rsa2048 \
  --dns alidns \
  --domains "$expected_domain" \
  --accept-tos \
  run || fail "Let's Encrypt DNS-01 issuance failed"

readonly certificate_path="${lego_state_path}/certificates/${expected_domain}.crt"
readonly issuer_path="${lego_state_path}/certificates/${expected_domain}.issuer.crt"
readonly private_key_path="${lego_state_path}/certificates/${expected_domain}.key"
readonly leaf_path="${temporary_directory}/leaf.pem"
[[ -s "$certificate_path" && -s "$issuer_path" && -s "$private_key_path" ]] || \
  fail "lego did not produce the complete ephemeral certificate artifacts"

openssl x509 -in "$certificate_path" -out "$leaf_path" || fail "issued leaf certificate is invalid"
openssl x509 -in "$leaf_path" -noout -checkhost "$expected_domain" >/dev/null || \
  fail "issued certificate does not cover the exact documentation domain"
san_output="$(openssl x509 -in "$leaf_path" -noout -ext subjectAltName)" || \
  fail "could not inspect issued certificate SANs"
mapfile -t dns_sans < <(grep -oE 'DNS:[^,[:space:]]+' <<<"$san_output" || true)
[[ ${#dns_sans[@]} -eq 1 && "${dns_sans[0]}" == "DNS:${expected_domain}" ]] || \
  fail "issued certificate SANs are not exactly ${expected_domain}"
openssl x509 -in "$leaf_path" -noout -checkend "$renewal_window_seconds" >/dev/null || \
  fail "issued certificate is not valid for at least 30 days"
certificate_public_key_digest="$(openssl x509 -in "$leaf_path" -pubkey -noout | \
  openssl pkey -pubin -outform DER 2>/dev/null | sha256sum | awk '{print $1}')" || \
  fail "could not derive the issued certificate public key"
private_public_key_digest="$(openssl pkey -in "$private_key_path" -pubout -outform DER 2>/dev/null | \
  sha256sum | awk '{print $1}')" || fail "could not derive the issued private key public key"
[[ -n "$certificate_public_key_digest" && "$certificate_public_key_digest" == "$private_public_key_digest" ]] || \
  fail "issued certificate and private key do not match"
unset certificate_public_key_digest private_public_key_digest san_output dns_sans
openssl verify \
  -purpose sslserver \
  -verify_hostname "$expected_domain" \
  -CApath /etc/ssl/certs \
  -untrusted "$issuer_path" \
  "$leaf_path" >/dev/null || fail "issued certificate chain does not validate against system trust"

issued_summary="$("$certificate_helper" validate-issued \
  --certificate "$certificate_path" \
  --key "$private_key_path")" || fail "issued certificate violates the fixed local contract"
certificate_name="$(jq -er '.certificate_name | strings | select(test("^wukongim-docs-le-[0-9]{8}-[0-9a-f]{12}$"))' \
  <<<"$issued_summary")" || fail "issued certificate name is outside the controlled namespace"
expected_fingerprint="$(jq -er '.fingerprint | strings | select(test("^[0-9a-f]{64}$"))' \
  <<<"$issued_summary")" || fail "issued certificate fingerprint is invalid"
issued_not_after="$(jq -er '.not_after | strings' <<<"$issued_summary")" || \
  fail "issued certificate expiry is invalid"

readonly deployment_response_path="${temporary_directory}/deployment.json"
certificate_public="$(<"$certificate_path")"
certificate_private="$(<"$private_key_path")"
set +e
timeout 90 aliyun --region cn-hangzhou cdn SetCdnDomainSSLCertificate \
  --DomainName "$expected_domain" \
  --CertName "$certificate_name" \
  --CertType upload \
  --SSLProtocol on \
  --SSLPub "$certificate_public" \
  --SSLPri "$certificate_private" >"$deployment_response_path" 2>"$api_error_path"
deployment_status=$?
set -e
unset certificate_public certificate_private
: >"$api_error_path"
(( deployment_status == 0 )) || fail "Alibaba Cloud rejected the exact CDN certificate upload"
request_id="$(jq -er '.RequestId | strings | select(test("^[A-Za-z0-9-]{1,128}$"))' \
  "$deployment_response_path")" || fail "Alibaba Cloud returned no valid certificate deployment request ID"

readonly verification_response_path="${temporary_directory}/verification.json"
cdn_verified=false
for attempt in {1..20}; do
  if (( attempt > 1 )); then
    sleep 15
  fi
  if timeout 60 aliyun --region cn-hangzhou cdn DescribeDomainCertificateInfo \
    --DomainName "$expected_domain" >"$verification_response_path" 2>"$api_error_path" &&
    "$certificate_helper" verify-cdn \
      --response "$verification_response_path" \
      --certificate "$certificate_path" \
      --certificate-name "$certificate_name" >/dev/null 2>&1; then
    cdn_verified=true
    break
  fi
  : >"$api_error_path"
done
[[ "$cdn_verified" == true ]] || fail "Alibaba CDN did not activate the exact uploaded certificate within five minutes"

# Keep the provider-reported status as validated diagnostics only. Public route
# authority comes from the fixed direct CNAME observations in assess_public_edge.
cdn_cname_status="$(jq -er \
  '.CertInfos.CertInfo | arrays | select(length == 1) | .[0].DomainCnameStatus |
   strings | select(. == "ok" or . == "cname_error" or . == "top_domain_cname_error")' \
  "$verification_response_path")" || fail "Alibaba CDN returned an invalid CNAME status"

if ! edge_verification="$(assess_public_edge "$expected_fingerprint" 40 15)"; then
  fail "the public CNAME route or trusted edge certificate did not match the declared route mode within ten minutes"
fi

if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    printf '\n### Documentation CDN certificate rotation\n\n'
    printf -- '- Domain: `%s`\n' "$expected_domain"
    printf -- '- Certificate: `%s`\n' "$certificate_name"
    printf -- '- Expiry: `%s`\n' "$issued_not_after"
    printf -- '- SHA-256: `%s`\n' "$expected_fingerprint"
    printf -- '- Alibaba request: `%s`\n' "$request_id"
    printf -- '- Alibaba API certificate readback: passed\n'
    printf -- '- Alibaba CNAME status: `%s`\n' "$cdn_cname_status"
    printf -- '- Public edge verification: `%s`\n' "$edge_verification"
  } >>"$GITHUB_STEP_SUMMARY"
fi
printf '{"domain":"%s","certificate_name":"%s","not_after":"%s","fingerprint":"%s","request_id":"%s","cdn_cname_status":"%s","edge_verification":"%s"}\n' \
  "$expected_domain" "$certificate_name" "$issued_not_after" "$expected_fingerprint" "$request_id" \
  "$cdn_cname_status" "$edge_verification"
