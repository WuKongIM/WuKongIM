#!/usr/bin/env bash

set -euo pipefail

fail() {
  printf 'docs Pages origin verification failed: %s\n' "$*" >&2
  exit 1
}

readonly expected_repository="WuKongIM/WuKongIM"
readonly github_pages_origin="wukongim.github.io"
readonly repository="${DOCS_PAGES_REPOSITORY:-}"
readonly configured_domain="${DOCS_PAGES_DOMAIN:-}"
readonly cache_bust="${DOCS_PAGES_CACHE_BUST:-}"
readonly attempts="${DOCS_PAGES_VERIFY_ATTEMPTS:-60}"
readonly retry_seconds="${DOCS_PAGES_VERIFY_RETRY_SECONDS:-5}"

[[ "$repository" == "$expected_repository" ]] || \
  fail "DOCS_PAGES_REPOSITORY must be exactly ${expected_repository}"
if [[ -n "$configured_domain" ]]; then
  [[ "$configured_domain" == docs.githubim.com || "$configured_domain" == origin-docs.githubim.com ]] || \
    fail "DOCS_PAGES_DOMAIN must be docs.githubim.com or origin-docs.githubim.com"
fi
[[ "$cache_bust" =~ ^[A-Za-z0-9._-]{1,160}$ ]] || \
  fail "DOCS_PAGES_CACHE_BUST must contain 1-160 safe characters"
[[ "$attempts" =~ ^[0-9]+$ && "$attempts" -ge 1 && "$attempts" -le 60 ]] || \
  fail "DOCS_PAGES_VERIFY_ATTEMPTS must be between 1 and 60"
[[ "$retry_seconds" =~ ^[0-9]+$ && "$retry_seconds" -ge 1 && "$retry_seconds" -le 30 ]] || \
  fail "DOCS_PAGES_VERIFY_RETRY_SECONDS must be between 1 and 30"

for dependency in gh jq curl mktemp grep; do
  command -v "$dependency" >/dev/null 2>&1 || fail "missing dependency: ${dependency}"
done

work_directory="$(mktemp -d)"
cleanup() {
  [[ -n "${work_directory:-}" && -d "$work_directory" && "$work_directory" != / ]] || return
  rm -rf -- "$work_directory"
}
trap cleanup EXIT

readonly -a paths=(
  "/"
  "/zh/"
  "/en/"
  "/zh/guide/quick-start/first-message/"
  "/api/search"
)

verify_path() {
  local domain="$1"
  local path="$2"
  local attempt="$3"

  local slug="${path//\//_}"
  local body="${work_directory}/${attempt}-${slug}.body"
  local metadata="${work_directory}/${attempt}-${slug}.metadata"
  local url="https://${domain}${path}?__wk_pages_origin_verification=${cache_bust}-${attempt}"

  if ! curl \
    --silent \
    --show-error \
    --fail-with-body \
    --compressed \
    --proto '=https' \
    --tlsv1.2 \
    --connect-timeout 15 \
    --max-time 90 \
    --connect-to "${domain}:443:${github_pages_origin}:443" \
    --output "$body" \
    --write-out '%{http_code}\t%{content_type}\t%{size_download}\n' \
    "$url" >"$metadata"; then
    printf 'content_not_ready domain=%s path=%s attempt=%s transport_or_http_failure=true\n' \
      "$domain" "$path" "$attempt" >&2
    return 1
  fi

  local status content_type downloaded_bytes
  IFS=$'\t' read -r status content_type downloaded_bytes <"$metadata"
  content_type_ready=false
  if [[ "$path" == /api/search ]]; then
    if [[ "$content_type" == application/json* || "$content_type" == application/octet-stream* ]]; then
      content_type_ready=true
    fi
  elif [[ "$content_type" == text/html* ]]; then
    content_type_ready=true
  fi
  if [[ "$status" != 200 || "$content_type_ready" != true || ! -s "$body" ]]; then
    printf 'content_not_ready domain=%s path=%s attempt=%s status=%s content_type=%s bytes=%s\n' \
      "$domain" "$path" "$attempt" "${status:-missing}" "${content_type:-missing}" "${downloaded_bytes:-missing}" >&2
    return 1
  fi
  if [[ ! "$downloaded_bytes" =~ ^[0-9]+$ ]] || ((downloaded_bytes <= 0)); then
    printf 'content_not_ready domain=%s path=%s attempt=%s invalid_download_size=%s\n' \
      "$domain" "$path" "$attempt" "${downloaded_bytes:-missing}" >&2
    return 1
  fi

  if [[ "$path" != /api/search ]]; then
    grep -Eqi '<html([ >])' "$body" && grep -Eqi '</html>' "$body" || {
      printf 'content_not_ready domain=%s path=%s attempt=%s invalid_html=true\n' \
        "$domain" "$path" "$attempt" >&2
      return 1
    }
  else
    jq -e '.type == "i18n" and (.data | type == "object" and has("zh") and has("en"))' \
      "$body" >/dev/null || {
      printf 'content_not_ready domain=%s path=%s attempt=%s invalid_search_payload=true\n' \
        "$domain" "$path" "$attempt" >&2
      return 1
    }
  fi
}

for ((attempt = 1; attempt <= attempts; attempt++)); do
  pages_json=""
  if ! pages_json="$(gh api \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2026-03-10' \
    "/repos/${repository}/pages")"; then
    printf 'api_not_ready attempt=%s reason=request_failed\n' "$attempt" >&2
  else
    current_domain="$(jq -r '.cname // ""' <<<"$pages_json")"
    domain="$configured_domain"
    if [[ -z "$domain" ]]; then
      domain="$current_domain"
    fi

    if [[ "$domain" != docs.githubim.com && "$domain" != origin-docs.githubim.com ]]; then
      printf 'api_not_ready attempt=%s reason=unexpected_domain domain=%s\n' \
        "$attempt" "${domain:-missing}" >&2
    elif ! jq -e --arg domain "$domain" '
      .cname == $domain and
      .build_type == "workflow" and
      .protected_domain_state == "verified" and
      .https_enforced == true and
      .https_certificate.state == "approved" and
      ((.https_certificate.domains // []) | index($domain) != null)
    ' <<<"$pages_json" >/dev/null; then
      printf 'api_not_ready attempt=%s domain=%s build_type=%s certificate=%s https_enforced=%s\n' \
        "$attempt" "$domain" \
        "$(jq -r '.build_type // "missing"' <<<"$pages_json")" \
        "$(jq -r '.https_certificate.state // "missing"' <<<"$pages_json")" \
        "$(jq -r '.https_enforced // "missing"' <<<"$pages_json")" >&2
    else
      all_paths_ready=true
      for path in "${paths[@]}"; do
        if ! verify_path "$domain" "$path" "$attempt"; then
          all_paths_ready=false
          break
        fi
      done
      if [[ "$all_paths_ready" == true ]]; then
        printf 'origin_ready domain=%s attempt=%s paths=%s bypass=%s\n' \
          "$domain" "$attempt" "${#paths[@]}" "$github_pages_origin"
        exit 0
      fi
    fi
  fi

  if ((attempt < attempts)); then
    sleep "$retry_seconds"
  fi
done

fail "origin did not become ready after ${attempts} attempts"
