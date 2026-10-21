#!/usr/bin/env bash
set -euo pipefail

if (( $# > 1 )) || { (( $# == 1 )) && [[ "$1" != --preflight ]]; }; then
  printf 'usage: %s [--preflight]\n' "${0##*/}" >&2
  exit 2
fi

if (( $# == 1 )); then
  # Preflight observes one exact open Issue before procurement. It never writes,
  # and does not promise future comment permission or prevent later Issue edits.
  preflight_receipt() {
    local status="$1" reason="$2" number="${3:-null}" url=""
    if [[ "$number" != null ]]; then
      url="https://github.com/${GITHUB_REPOSITORY}/issues/${number}"
    fi
    jq -cn --arg status "$status" --arg reason "$reason" \
      --arg repository "${GITHUB_REPOSITORY:-}" --arg request_id "${WK_CHAT_REQUEST_ID:-}" \
      --argjson number "$number" --arg url "$url" \
      '{schema:"wukongim.chat_lifecycle.issue_preflight/v1",status:$status,reason:$reason,
        repository:$repository,request_id:$request_id,issue_number:$number,
        issue_url:(if $number == null then null else $url end)}'
  }
  reject_preflight() {
    preflight_receipt rejected "$1"
    exit 1
  }
  if [[ -z "${GH_TOKEN:-}" ]] ||
     [[ ! "${GITHUB_REPOSITORY:-}" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] ||
     [[ ! "${WK_CHAT_REQUEST_ID:-}" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]; then
    reject_preflight environment_invalid
  fi
  title="[chat-lifecycle] $WK_CHAT_REQUEST_ID"
  query="repo:$GITHUB_REPOSITORY is:issue in:title \"$title\""
  if ! search="$(gh api --hostname github.com --method GET /search/issues -f q="$query" -f per_page=10 2>/dev/null)"; then
    reject_preflight search_failed
  fi
  if ! jq -e '
    type == "object" and (.items | type == "array")
    and (.total_count | type == "number" and . >= 0 and floor == .)
    and (.incomplete_results | type == "boolean")
    and all(.items[]; type == "object" and (.title | type == "string"))
  ' <<<"$search" >/dev/null 2>&1; then
    reject_preflight search_invalid
  fi
  # A single bounded page is sufficient only when the response proves complete.
  if ! jq -e '.incomplete_results == false and .total_count == (.items | length) and .total_count <= 10' <<<"$search" >/dev/null; then
    reject_preflight search_incomplete
  fi
  matches="$(jq -c --arg title "$title" '[.items[] | select(.title == $title and (has("pull_request") | not))]' <<<"$search")"
  match_count="$(jq -r length <<<"$matches")"
  [[ "$match_count" != 0 ]] || reject_preflight issue_missing
  [[ "$match_count" == 1 ]] || reject_preflight issue_ambiguous
  issue_match="$(jq -c '.[0]' <<<"$matches")"
  valid_preflight_issue() {
    jq -e --arg title "$title" --arg repo "$GITHUB_REPOSITORY" '
      def natural: type == "number" and . >= 0 and floor == .;
      type == "object" and (has("pull_request") | not)
      and .title == $title and (.number | natural and . > 0)
      and (.id | natural and . > 0) and (.comments | natural)
      and (.locked | type == "boolean") and (.state == "open" or .state == "closed")
      and .repository_url == ("https://api.github.com/repos/" + $repo)
      and .url == ("https://api.github.com/repos/" + $repo + "/issues/" + (.number | tostring))
      and .html_url == ("https://github.com/" + $repo + "/issues/" + (.number | tostring))
    ' >/dev/null 2>&1
  }
  valid_preflight_issue <<<"$issue_match" || reject_preflight issue_invalid
  issue_number="$(jq -r .number <<<"$issue_match")"
  # Search can lag edits or transfers; re-read the exact Issue without retry.
  if ! issue="$(gh api --hostname github.com --method GET "/repos/${GITHUB_REPOSITORY}/issues/${issue_number}" 2>/dev/null)"; then
    reject_preflight issue_read_failed
  fi
  valid_preflight_issue <<<"$issue" || reject_preflight issue_invalid
  if ! jq -e --argjson prior "$issue_match" '.id == $prior.id and .number == $prior.number' <<<"$issue" >/dev/null; then
    reject_preflight issue_invalid
  fi
  [[ "$(jq -r .state <<<"$issue")" == open ]] || reject_preflight issue_closed
  [[ "$(jq -r .locked <<<"$issue")" == false ]] || reject_preflight issue_locked
  preflight_receipt ready issue_ready "$issue_number"
  exit 0
fi

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${WK_CHAT_REQUEST_ID:?required}"
: "${WK_CHAT_ISSUE_STATE:?required}"

[[ "$GITHUB_REPOSITORY" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]
[[ "$WK_CHAT_REQUEST_ID" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
[[ "$WK_CHAT_ISSUE_STATE" =~ ^[a-z][a-z0-9._-]{0,95}$ ]]
issue_dedupe_key="${WK_CHAT_ISSUE_DEDUPE_KEY:-$WK_CHAT_ISSUE_STATE}"
issue_close="${WK_CHAT_ISSUE_CLOSE:-false}"
[[ "$issue_dedupe_key" =~ ^[a-z][a-z0-9._-]{0,127}$ ]]
[[ "$issue_close" == true || "$issue_close" == false ]]

title="[chat-lifecycle] $WK_CHAT_REQUEST_ID"
query="repo:$GITHUB_REPOSITORY is:issue in:title \"$title\""
search="$(gh api --method GET /search/issues -f q="$query" -f per_page=10)"
issue_match="$(jq -cer --arg title "$title" '
  [.items[] | select(.title == $title and (.pull_request | not))]
  | select(length == 1) | .[0]
' <<<"$search")"
issue_number="$(jq -er .number <<<"$issue_match")"
comment_count="$(jq -er '.comments // 0' <<<"$issue_match")"
[[ "$issue_number" =~ ^[1-9][0-9]*$ ]]
[[ "$comment_count" =~ ^[0-9]+$ ]]

marker="<!-- chat-lifecycle:${WK_CHAT_REQUEST_ID}:${issue_dedupe_key} -->"
comment_page=$(( comment_count == 0 ? 1 : (comment_count - 1) / 100 + 1 ))
comments="$(gh api --method GET "/repos/${GITHUB_REPOSITORY}/issues/${issue_number}/comments" \
  -f per_page=100 -f page="$comment_page")"
if (( comment_page > 1 )); then
  prior_comments="$(gh api --method GET "/repos/${GITHUB_REPOSITORY}/issues/${issue_number}/comments" \
    -f per_page=100 -f page="$(( comment_page - 1 ))")"
  comments="$(jq -cn --argjson prior "$prior_comments" --argjson current "$comments" '$prior + $current')"
fi
if ! jq -e --arg marker "$marker" 'any(.[]; .body | contains($marker))' <<<"$comments" >/dev/null; then
  detail="${WK_CHAT_ISSUE_BODY:-state=${WK_CHAT_ISSUE_STATE}}"
  case "$WK_CHAT_ISSUE_STATE" in
    *_failed|*_diagnosis_pending|*_cleanup_pending|*_final_artifact_pending|request_complete)
      detail="@tangtaoit ${detail}"
      ;;
  esac
  (( ${#detail} <= 2000 ))
  observed_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  observed_asia="$(TZ=Asia/Shanghai date +%Y-%m-%dT%H:%M:%S%z)"
  body="$(printf '%s\nstate=%s\nobserved_at_utc=%s\nobserved_at_asia_shanghai=%s\n%s' \
    "$marker" "$WK_CHAT_ISSUE_STATE" "$observed_utc" "$observed_asia" "$detail")"
  gh api --method POST "/repos/${GITHUB_REPOSITORY}/issues/${issue_number}/comments" -f body="$body" >/dev/null
fi

if [[ "$issue_close" == true ]]; then
  gh api --method PATCH "/repos/${GITHUB_REPOSITORY}/issues/${issue_number}" \
    -f state=closed -f state_reason=completed >/dev/null
fi
