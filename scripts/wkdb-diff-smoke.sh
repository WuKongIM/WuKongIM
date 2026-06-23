#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/wkdb-diff-smoke.sh [--work-dir DIR] [--hash-slot-count N] [--keep]

Builds a seed node with scripts/wkdb-import-smoke.sh, exports it, imports the
export into a second node, and verifies both equal and mismatch wkdb diff exits.

Environment:
  WKDB_BIN  Path to a prebuilt wkdb binary. If unset, the script runs
            "go run ./cmd/wkdb" with GOWORK=off.
  GO        Go executable used when WKDB_BIN is unset. Defaults to "go".
  WKDB_SMOKE_VERBOSE=1 prints wkdb stderr from successful commands.
USAGE
}

hash_slot_count=256
keep=0
work_dir=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --work-dir)
      if [[ $# -lt 2 ]]; then
        echo "--work-dir requires a value" >&2
        exit 2
      fi
      work_dir="$2"
      shift 2
      ;;
    --hash-slot-count)
      if [[ $# -lt 2 ]]; then
        echo "--hash-slot-count requires a value" >&2
        exit 2
      fi
      hash_slot_count="$2"
      shift 2
      ;;
    --keep)
      keep=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [[ "$hash_slot_count" != "256" ]]; then
  echo "this smoke fixture currently supports --hash-slot-count 256 only" >&2
  exit 2
fi

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
if [[ ! -d "$repo_root/cmd/wkdb" ]]; then
  echo "must run from a WuKongIM checkout with cmd/wkdb" >&2
  exit 2
fi

created_work_dir=0
if [[ -z "$work_dir" ]]; then
  work_dir="$(mktemp -d "${TMPDIR:-/tmp}/wkdb-diff-smoke.XXXXXX")"
  created_work_dir=1
else
  mkdir -p "$work_dir"
fi

cleanup() {
  if [[ "$created_work_dir" -eq 1 && "$keep" -eq 0 ]]; then
    rm -rf "$work_dir"
  fi
}
trap cleanup EXIT

seed_work_dir="$work_dir/seed"
export_dir="$work_dir/exported"
target_dir="$work_dir/target"
mismatch_bundle="$work_dir/mismatch-bundle"
mismatch_target_dir="$work_dir/mismatch-target"
summary_file="$work_dir/summary.md"
rm -rf "$seed_work_dir" "$export_dir" "$target_dir" "$mismatch_bundle" "$mismatch_target_dir"
mkdir -p "$work_dir"

if [[ -n "${WKDB_BIN:-}" ]]; then
  wkdb_cmd=("$WKDB_BIN")
else
  wkdb_cmd=("${GO:-go}" run ./cmd/wkdb)
fi

run_wkdb() {
  (cd "$repo_root" && GOWORK=off "${wkdb_cmd[@]}" "$@")
}

capture_wkdb() {
  local err_file="$work_dir/wkdb-stderr.log"
  local out
  : > "$err_file"
  if ! out="$(run_wkdb "$@" 2>"$err_file")"; then
    cat "$err_file" >&2
    return 1
  fi
  if [[ "${WKDB_SMOKE_VERBOSE:-0}" == "1" && -s "$err_file" ]]; then
    cat "$err_file" >&2
  fi
  printf '%s\n' "$out"
}

sha256_file() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  shasum -a 256 "$1" | awk '{print $1}'
}

row_count() {
  wc -l < "$1" | tr -d '[:space:]'
}

assert_contains() {
  local haystack="$1"
  local needle="$2"
  if [[ "$haystack" != *"$needle"* ]]; then
    echo "output missing $needle" >&2
    echo "$haystack" >&2
    exit 1
  fi
}

rewrite_import_smoke_manifest() {
  local bundle_dir="$1"
  local manifest="$bundle_dir/manifest.json"
  {
    printf '{"format":"wkdb-import-bundle","version":1,"hash_slot_count":%s,"files":[' "$hash_slot_count"
    local first=1
    local rel
    for rel in \
      meta/users.jsonl \
      meta/devices.jsonl \
      meta/channels.jsonl \
      meta/subscribers.jsonl \
      meta/user_channel_memberships.jsonl \
      meta/conversations.jsonl \
      meta/channel_latest.jsonl \
      message/channels.jsonl \
      message/messages-000001.jsonl
    do
      local kind
      case "$rel" in
        meta/users.jsonl) kind="meta.users" ;;
        meta/devices.jsonl) kind="meta.devices" ;;
        meta/channels.jsonl) kind="meta.channels" ;;
        meta/subscribers.jsonl) kind="meta.subscribers" ;;
        meta/user_channel_memberships.jsonl) kind="meta.user_channel_memberships" ;;
        meta/conversations.jsonl) kind="meta.conversations" ;;
        meta/channel_latest.jsonl) kind="meta.channel_latest" ;;
        message/channels.jsonl) kind="message.channels" ;;
        message/messages-000001.jsonl) kind="message.messages" ;;
        *) echo "unknown bundle file $rel" >&2; exit 2 ;;
      esac
      if [[ "$first" -eq 0 ]]; then
        printf ','
      fi
      first=0
      local path="$bundle_dir/$rel"
      printf '{"path":"%s","kind":"%s","rows":%s,"sha256":"%s"}' \
        "$rel" "$kind" "$(row_count "$path")" "$(sha256_file "$path")"
    done
    printf ']}'
  } > "$manifest"
}

seed_output=""
if ! seed_output="$(cd "$repo_root" && WKDB_BIN="${WKDB_BIN:-}" GO="${GO:-go}" scripts/wkdb-import-smoke.sh --work-dir "$seed_work_dir" --hash-slot-count "$hash_slot_count")"; then
  echo "$seed_output" >&2
  exit 1
fi
assert_contains "$seed_output" "wkdb import smoke passed"
echo "seed import ok"

seed_data_dir="$seed_work_dir/node-new"
seed_bundle="$seed_work_dir/wkdb-dump"
export_output="$(capture_wkdb --data-dir "$seed_data_dir" --hash-slot-count "$hash_slot_count" export --output "$export_dir" --page-size 1 --message-file-rows 1)"
assert_contains "$export_output" "exported=9"
assert_contains "$export_output" "messages=1"
assert_contains "$export_output" "subscribers=1"
echo "export ok"

target_output="$(capture_wkdb --data-dir "$target_dir" --hash-slot-count "$hash_slot_count" import --input "$export_dir" --require-empty)"
assert_contains "$target_output" "validated=9"
assert_contains "$target_output" "written=8"
echo "target import ok"

diff_equal_output="$(capture_wkdb --hash-slot-count "$hash_slot_count" diff --source-data-dir "$seed_data_dir" --target-data-dir "$target_dir" --mode full --page-size 1)"
assert_contains "$diff_equal_output" "equal=true"
assert_contains "$diff_equal_output" "mismatches=0"
echo "diff equal ok"

cp -R "$seed_bundle" "$mismatch_bundle"
user_file="$mismatch_bundle/meta/users.jsonl"
tmp_user_file="$user_file.tmp"
sed 's/smoke-token/smoke-token-changed/' "$user_file" > "$tmp_user_file"
mv "$tmp_user_file" "$user_file"
rewrite_import_smoke_manifest "$mismatch_bundle"

mismatch_import_output="$(capture_wkdb --data-dir "$mismatch_target_dir" --hash-slot-count "$hash_slot_count" import --input "$mismatch_bundle" --require-empty)"
assert_contains "$mismatch_import_output" "validated=9"
assert_contains "$mismatch_import_output" "written=8"

mismatch_err_file="$work_dir/wkdb-diff-mismatch-stderr.log"
: > "$mismatch_err_file"
set +e
diff_mismatch_output="$(run_wkdb --hash-slot-count "$hash_slot_count" diff --source-data-dir "$seed_data_dir" --target-data-dir "$mismatch_target_dir" --page-size 1 2>"$mismatch_err_file")"
diff_mismatch_code=$?
set -e
if [[ -z "${WKDB_BIN:-}" && "$diff_mismatch_code" -eq 1 ]] && grep -q "exit status 2" "$mismatch_err_file"; then
  diff_mismatch_code=2
fi
if [[ "$diff_mismatch_code" -ne 2 ]]; then
  cat "$mismatch_err_file" >&2
  echo "unexpected mismatch diff exit code: $diff_mismatch_code" >&2
  echo "$diff_mismatch_output" >&2
  exit 1
fi
if [[ "${WKDB_SMOKE_VERBOSE:-0}" == "1" && -s "$mismatch_err_file" ]]; then
  cat "$mismatch_err_file" >&2
fi
assert_contains "$diff_mismatch_output" "equal=false"
echo "diff mismatch ok"

{
  echo "# wkdb diff smoke"
  echo
  echo "- seed_data_dir: $seed_data_dir"
  echo "- exported_bundle: $export_dir"
  echo "- target_data_dir: $target_dir"
  echo "- mismatch_target_data_dir: $mismatch_target_dir"
  echo "- export: $export_output"
  echo "- target_import: $target_output"
  echo "- diff_equal: $diff_equal_output"
  echo "- mismatch_import: $mismatch_import_output"
  echo "- diff_mismatch_exit: $diff_mismatch_code"
} > "$summary_file"

echo "summary=$summary_file"
echo "wkdb diff smoke passed"
