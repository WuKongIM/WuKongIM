#!/usr/bin/env bash
set -euo pipefail

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
