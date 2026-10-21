#!/usr/bin/env bash
set -euo pipefail

: "${GH_TOKEN:?required}"
: "${GITHUB_REPOSITORY:?required}"
: "${WK_CHAT_STAGE:=rehearsal}"

[[ "$WK_CHAT_STAGE" == rehearsal || "$WK_CHAT_STAGE" == formal ]]

requested="${1-}"
output="${2:?output required}"
if [[ -n "$requested" ]]; then
  [[ "$requested" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$ ]]
fi
[[ ! -e "$output" ]]

temporary="$(mktemp -d)"
trap 'rm -r "$temporary"' EXIT
: >"$temporary/artifacts-pages.json"
max_pages=200
artifact_api_attempts=4
inventory_complete=false

fetch_artifact_page() {
  local endpoint="$1" output="$2" attempt status delay
  for ((attempt = 1; attempt <= artifact_api_attempts; attempt++)); do
    status=0
    gh api "$endpoint" >"$output" || status=$?
    if (( status == 0 )); then
      return 0
    fi
    if (( attempt == artifact_api_attempts )); then
      return "$status"
    fi
    delay=$((1 << (attempt - 1)))
    echo "artifact inventory request failed; retrying attempt $((attempt + 1))/${artifact_api_attempts} after ${delay}s" >&2
    sleep "$delay"
  done
}

for ((page = 1; page <= max_pages; page++)); do
  page_file="$temporary/artifacts-page-${page}.json"
  fetch_artifact_page "/repos/${GITHUB_REPOSITORY}/actions/artifacts?per_page=100&page=${page}" "$page_file"
  jq -c . "$page_file" >>"$temporary/artifacts-pages.json"
  if [[ "$(jq -r '.artifacts | length' "$page_file")" != 100 ]]; then
    inventory_complete=true
    break
  fi
done
[[ "$inventory_complete" == true ]] || {
  echo "active handoff discovery exceeded the bounded ${max_pages}-page repository Artifact inventory" >&2
  exit 1
}
jq -s '[.[].artifacts[] | select(.expired == false)]' \
  "$temporary/artifacts-pages.json" >"$temporary/artifacts.json"
jq -c --arg prefix "chat-lifecycle-${WK_CHAT_STAGE}-handoff-" \
  --arg final_prefix "chat-lifecycle-${WK_CHAT_STAGE}-final-" \
  --arg cleanup_prefix "chat-lifecycle-${WK_CHAT_STAGE}-cleanup-" \
  --arg requested "$requested" \
  -f scripts/chat-lifecycle/select-finalization-matrix.jq \
  "$temporary/artifacts.json" >"$temporary/candidate-matrix.json"

: >"$temporary/active-rows.jsonl"
while IFS= read -r row; do
  request_id="$(jq -er .request_id <<<"$row")"
  handoff_run_id="$(jq -er .handoff_run_id <<<"$row")"
  cleanup_run_id="$(jq -er .cleanup_run_id <<<"$row")"
  handoff_status=0
  scripts/chat-lifecycle/authenticate-handoff-producer.sh \
    "$handoff_run_id" "$temporary/handoff-auth/$request_id.json" || handoff_status=$?
  case "$handoff_status" in
    0) ;;
    1) continue ;;
    *) echo 'handoff producer authentication could not reach its authority' >&2; exit "$handoff_status" ;;
  esac
  if [[ "$cleanup_run_id" != 0 ]] && \
    scripts/chat-lifecycle/authenticate-cleanup-artifact.sh \
      "$request_id" "$cleanup_run_id" "$handoff_run_id" "$temporary/cleanup-auth/$request_id"; then
    continue
  fi
  printf '%s\n' "$row" >>"$temporary/active-rows.jsonl"
done < <(jq -c '.include[]' "$temporary/candidate-matrix.json")

jq -sc '{include:.[0:20]}' "$temporary/active-rows.jsonl" >"$temporary/matrix.json"
install -m 0600 "$temporary/matrix.json" "$output"
