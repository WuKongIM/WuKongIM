#!/usr/bin/env bash

set -euo pipefail

if [[ "$#" -ne 11 ]]; then
  printf '%s\n' \
    'usage: agent-pr-validation-plan.sh PR_JSON COMMENTS_JSON FILES_JSON STATUSES_JSON ATTEMPT_RUNS_JSON TRIGGER_ACTOR HEAD_SHA MERGE_SHA GATE_RUN_ID GITHUB_OUTPUT PLAN_JSON' >&2
  exit 2
fi

pr_json="$1"
comments_json="$2"
files_json="$3"
statuses_json="$4"
attempt_runs_json="$5"
trigger_actor="$6"
expected_head_sha="$7"
expected_merge_sha="$8"
gate_run_id="$9"
github_output="${10}"
validated_plan_json="${11}"

for input in "$pr_json" "$comments_json" "$files_json" "$statuses_json" "$attempt_runs_json"; do
  test -f "$input"
done
[[ "$trigger_actor" =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}(\[bot\])?$ ]]
[[ "$expected_head_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$expected_merge_sha" =~ ^[0-9a-f]{40}$ ]]
[[ "$gate_run_id" =~ ^[1-9][0-9]{0,19}$ ]]

current_head_sha="$(jq -er '.head.sha | select(test("^[0-9a-f]{40}$"))' "$pr_json")"
test "$current_head_sha" = "$expected_head_sha"
pr_number="$(jq -er '.number | select(type == "number" and . > 0 and floor == .)' "$pr_json")"
changed_file_count="$(
  jq -er '.changed_files | select(type == "number" and . > 0 and floor == .)' "$pr_json"
)"
fetched_file_count="$(jq -er 'length' "$files_json")"
test "$fetched_file_count" -eq "$changed_file_count"
jq -e '
  all(.[];
    . as $file
    | ($file.filename | type == "string" and length > 0) and
      (($file | has("previous_filename") | not) or
        ($file.previous_filename |
          type == "string" and length > 0 and . != $file.filename))) and
  ([.[].filename] | length == (unique | length))
' >/dev/null "$files_json"
changed_paths="$(
  jq -cS '
    [
      .[]
      | (.filename, .previous_filename?)
      | select(type == "string" and length > 0)
    ]
    | unique
  ' "$files_json"
)"

plan_comment="$(
  jq -cer --arg actor "$trigger_actor" '
    [
      .[]
      | select(.user.login == $actor)
      | select(.body | startswith("<!-- agent-validation-plan:v1\n"))
    ]
    | last
    | {id, body}
  ' "$comments_json"
)"
plan_comment_id="$(jq -er '.id | select(type == "number" and . > 0)' <<<"$plan_comment")"
plan_body="$(jq -er '.body' <<<"$plan_comment")"
test "$(sed -n '1p' <<<"$plan_body")" = '<!-- agent-validation-plan:v1'
test "$(sed -n '3p' <<<"$plan_body")" = '-->'
plan_json="$(sed -n '2p' <<<"$plan_body")"

jq -e \
  --arg head_sha "$expected_head_sha" '
    (keys | sort) ==
      ["head_sha","reason","retry_of_run_id","risk","schema_version","selected_suites"] and
    .schema_version == 1 and
    .head_sha == $head_sha and
    (.risk | type == "string" and length > 0 and length <= 64) and
    (.reason | type == "string" and length > 0 and length <= 1000) and
    (.retry_of_run_id == null or
      (.retry_of_run_id | type == "number" and . > 0 and floor == .)) and
    (if .retry_of_run_id == null then
      true
    else
      (.reason |
        test("^retry-evidence:(runner|network|dependency-download|known-flake): .{10,}$"))
    end) and
    (.selected_suites | type == "array" and length > 0 and length <= 8) and
    (.selected_suites | length == (unique | length)) and
    all(.selected_suites[];
      . == "docs-only" or
      . == "go-fast" or
      . == "web" or
      . == "demo" or
      . == "go-race" or
      . == "go-integration" or
      . == "go-e2e" or
      . == "three-node-smoke")
  ' >/dev/null <<<"$plan_json"

selected_labels="$(
  jq -cS '
    [
      .labels[]?.name
      | select(startswith("agent-ci/"))
      | select(. != "agent-ci/run")
      | sub("^agent-ci/"; "")
    ]
    | sort
  ' "$pr_json"
)"
selected_suites="$(jq -cS '.selected_suites | sort' <<<"$plan_json")"
test "$selected_labels" = "$selected_suites"

request_status="$(
  jq -cer \
    --arg context \
      "Agent Validation Request / PR #${pr_number} / Gate #${gate_run_id}" '
    first(.[] | select(.context == $context))
    | {state, target_url}
  ' "$statuses_json"
)"
jq -e '
  .state == "pending" and
  (.target_url | type == "string" and test("/actions/runs/[1-9][0-9]*$"))
' >/dev/null <<<"$request_status"

retry_of_run_id="$(jq -r '.retry_of_run_id // ""' <<<"$plan_json")"
gate_statuses="$(
  jq -c \
    --arg context \
      "Agent Validation Evidence / PR #${pr_number} / Gate #${gate_run_id}" \
    '[.[] | select(.context == $context)]' "$statuses_json"
)"
jq -e '
  all(.[];
    (.target_url | type == "string") and
    (.target_url | test("/actions/runs/[1-9][0-9]*$")))
' >/dev/null <<<"$gate_statuses"
attempt_run_ids="$(
  jq -c '
    [
      .[]
      | .target_url
      | capture("/actions/runs/(?<run_id>[1-9][0-9]*)$").run_id
    ]
    | unique
  ' <<<"$gate_statuses"
)"
attempt_count="$(jq -r 'length' <<<"$attempt_run_ids")"
metadata_run_ids="$(
  jq -c '[.[].id | tostring] | unique | sort' "$attempt_runs_json"
)"
test "$metadata_run_ids" = "$attempt_run_ids"
if [[ "$attempt_count" -eq 0 ]]; then
  test -z "$retry_of_run_id"
elif [[ "$attempt_count" -eq 1 ]]; then
  prior_run_id="$(jq -er '.[0]' <<<"$attempt_run_ids")"
  test "$retry_of_run_id" = "$prior_run_id"
  latest_prior_state="$(
    jq -er --arg suffix "/actions/runs/$prior_run_id" '
      first(.[] | select(.target_url | endswith($suffix))) | .state
    ' <<<"$gate_statuses"
  )"
  prior_run="$(
    jq -cer --argjson run_id "$prior_run_id" '
      first(.[] | select(.id == $run_id))
    ' "$attempt_runs_json"
  )"
  jq -e \
    --arg prefix \
      "Agent PR #${pr_number} validation head ${expected_head_sha} merge ${expected_merge_sha} gate ${gate_run_id} request " '
    .status == "completed" and
    .path == ".github/workflows/agent-pr-validation.yml" and
    .event == "repository_dispatch" and
    (.display_title |
      type == "string" and
      startswith($prefix) and
      (ltrimstr($prefix) | test("^[1-9][0-9]*$")))
  ' >/dev/null <<<"$prior_run"
  prior_conclusion="$(jq -er '.conclusion' <<<"$prior_run")"
  if [[ "$latest_prior_state" == failure || "$latest_prior_state" == error ]]; then
    test "$prior_conclusion" = failure
  elif [[ "$latest_prior_state" == pending ]]; then
    [[
      "$prior_conclusion" == failure ||
        "$prior_conclusion" == cancelled ||
        "$prior_conclusion" == timed_out ||
        "$prior_conclusion" == startup_failure
    ]]
  else
    printf '%s\n' 'only a terminal failed or interrupted validation may be retried' >&2
    exit 1
  fi
else
  printf '%s\n' 'the same commit already consumed its single evidence-bound retry' >&2
  exit 1
fi

if jq -e 'index("docs-only") != null' >/dev/null <<<"$selected_suites"; then
  test "$(jq -r 'length' <<<"$selected_suites")" -eq 1
  jq -e '
    length > 0 and
    all(.[];
      (startswith(".github/workflows/") | not) and
      (startswith("docs/") or
        test("(^|/)README\\.md$") or
        test("^(CHANGELOG|CONTRIBUTING|SECURITY|CODE_OF_CONDUCT)\\.md$") or
        test("^LICENSE(\\..*)?$") or
        startswith(".github/ISSUE_TEMPLATE/") or
        . == ".github/PULL_REQUEST_TEMPLATE.md"))
  ' >/dev/null <<<"$changed_paths"
else
  if jq -e 'any(.[]; test("(^|/)go\\.(mod|sum|work|work\\.sum)$|\\.go$|^scripts/|^docker/|^Dockerfile(\\..*)?$|^(docker-)?compose\\.ya?ml$|^\\.github/(workflows/|actions/|CODEOWNERS$)|^wukongim\\.toml(\\.example)?$|^conf/"))' \
    >/dev/null <<<"$changed_paths"; then
    jq -e 'index("go-fast") != null' >/dev/null <<<"$selected_suites"
  fi
  if jq -e 'any(.[]; test("^scripts/.*\\.sh$|^scripts/.*_integration_test\\.go$"))' \
    >/dev/null <<<"$changed_paths"; then
    jq -e 'index("go-integration") != null' >/dev/null <<<"$selected_suites"
  fi
  if jq -e 'any(.[]; startswith("web/") or startswith("internal/access/manager/webui/dist/"))' \
    >/dev/null <<<"$changed_paths"; then
    jq -e 'index("web") != null' >/dev/null <<<"$selected_suites"
  fi
  if jq -e 'any(.[]; startswith("demo/chatdemo/") or startswith("internal/access/api/demoui/dist/"))' \
    >/dev/null <<<"$changed_paths"; then
    jq -e 'index("demo") != null' >/dev/null <<<"$selected_suites"
  fi
fi

jq -S . <<<"$plan_json" >"$validated_plan_json"
: >"$github_output"
for suite in \
  docs-only \
  go-fast \
  web \
  demo \
  go-race \
  go-integration \
  go-e2e \
  three-node-smoke; do
  output_name="${suite//-/_}"
  if jq -e --arg suite "$suite" 'index($suite) != null' >/dev/null <<<"$selected_suites"; then
    value=true
  else
    value=false
  fi
  printf '%s=%s\n' "$output_name" "$value" >>"$github_output"
done
printf 'plan_comment_id=%s\n' "$plan_comment_id" >>"$github_output"
printf 'retry_of_run_id=%s\n' "$retry_of_run_id" >>"$github_output"
