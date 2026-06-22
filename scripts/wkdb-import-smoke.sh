#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/wkdb-import-smoke.sh [--work-dir DIR] [--hash-slot-count N] [--keep]

Builds a minimal WKDB Import Bundle v1, validates it with wkdb import
--dry-run, imports it into a fresh node data directory, and verifies the
imported rows with wkdb query.

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
  work_dir="$(mktemp -d "${TMPDIR:-/tmp}/wkdb-import-smoke.XXXXXX")"
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

bundle_dir="$work_dir/wkdb-dump"
target_dir="$work_dir/node-new"
summary_file="$work_dir/summary.md"
rm -rf "$bundle_dir" "$target_dir"
mkdir -p "$bundle_dir/meta" "$bundle_dir/message" "$target_dir"

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
    echo "query output missing $needle" >&2
    echo "$haystack" >&2
    exit 1
  fi
}

write_jsonl() {
  local rel="$1"
  shift
  local path="$bundle_dir/$rel"
  mkdir -p "$(dirname "$path")"
  : > "$path"
  local line
  for line in "$@"; do
    printf '%s\n' "$line" >> "$path"
  done
}

write_jsonl "meta/users.jsonl" \
  '{"hash_slot":231,"uid":"smoke-u1","token":"smoke-token","device_flag":1,"device_level":2}'
write_jsonl "meta/devices.jsonl" \
  '{"hash_slot":231,"uid":"smoke-u1","device_flag":1,"token":"smoke-device-token","device_level":3}'
write_jsonl "meta/channels.jsonl" \
  '{"hash_slot":52,"channel_id":"smoke-g1","channel_type":2,"ban":0,"disband":0,"send_ban":0,"allow_stranger":1,"large":0,"subscriber_mutation_version":7}'
write_jsonl "meta/subscribers.jsonl" \
  '{"hash_slot":52,"channel_id":"smoke-g1","channel_type":2,"uid":"smoke-u1"}'
write_jsonl "meta/user_channel_memberships.jsonl" \
  '{"hash_slot":231,"uid":"smoke-u1","channel_id":"smoke-g1","channel_type":2,"join_seq":1,"updated_at_ms":1710000000000}'
write_jsonl "meta/conversations.jsonl" \
  '{"hash_slot":231,"uid":"smoke-u1","kind":"normal","channel_id":"smoke-g1","channel_type":2,"read_seq":1,"deleted_to_seq":0,"active_at":1710000000001,"updated_at":1710000000002,"sparse_active":true}'
write_jsonl "meta/channel_latest.jsonl" \
  '{"hash_slot":52,"channel_id":"smoke-g1","channel_type":2,"last_message_id":1001,"last_message_seq":1,"last_at":1710000000003,"from_uid":"smoke-u1","client_msg_no":"smoke-c1","last_payload_b64":"aGk=","updated_at":1710000000004}'
write_jsonl "message/channels.jsonl" \
  '{"channel_key":"smoke-g1:2","channel_id":"smoke-g1","channel_type":2}'
write_jsonl "message/messages-000001.jsonl" \
  '{"channel_key":"smoke-g1:2","message_seq":1,"message_id":1001,"client_msg_no":"smoke-c1","from_uid":"smoke-u1","server_timestamp_ms":1710000000003,"payload_b64":"aGk="}'

manifest="$bundle_dir/manifest.json"
{
  printf '{"format":"wkdb-import-bundle","version":1,"hash_slot_count":%s,"files":[' "$hash_slot_count"
  first=1
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
    file_path="$bundle_dir/$rel"
    printf '{"path":"%s","kind":"%s","rows":%s,"sha256":"%s"}' \
      "$rel" "$kind" "$(row_count "$file_path")" "$(sha256_file "$file_path")"
  done
  printf ']}'
} > "$manifest"

dry_run_output="$(capture_wkdb --hash-slot-count "$hash_slot_count" import --input "$bundle_dir" --dry-run)"
if [[ "$dry_run_output" != *"validated=9"* ]]; then
  echo "unexpected dry-run output:" >&2
  echo "$dry_run_output" >&2
  exit 1
fi
echo "dry-run ok"

import_output="$(capture_wkdb --data-dir "$target_dir" --hash-slot-count "$hash_slot_count" import --input "$bundle_dir" --require-empty)"
if [[ "$import_output" != *"validated=9"* || "$import_output" != *"written=8"* || "$import_output" != *"messages=1"* || "$import_output" != *"subscribers=1"* ]]; then
  echo "unexpected import output:" >&2
  echo "$import_output" >&2
  exit 1
fi
echo "import ok"

user_json="$(capture_wkdb --data-dir "$target_dir" --hash-slot-count "$hash_slot_count" --format json query "select * from meta.user where uid='smoke-u1'")"
channel_json="$(capture_wkdb --data-dir "$target_dir" --hash-slot-count "$hash_slot_count" --format json query "select * from meta.channel where channel_id='smoke-g1'")"
conversation_json="$(capture_wkdb --data-dir "$target_dir" --hash-slot-count "$hash_slot_count" --format json query "select * from meta.conversation where uid='smoke-u1'")"
message_json="$(capture_wkdb --data-dir "$target_dir" --format json query "select * from message.message where channel_key='smoke-g1:2' limit 10")"

assert_contains "$user_json" "smoke-token"
assert_contains "$channel_json" '"subscriber_count": 1'
assert_contains "$conversation_json" '"read_seq": 1'
assert_contains "$message_json" '"message_seq": 1'
assert_contains "$message_json" '"message_id": 1001'
assert_contains "$message_json" '"payload": "aGk="'
echo "query ok"

{
  echo "# wkdb import smoke"
  echo
  echo "- bundle: $bundle_dir"
  echo "- target_data_dir: $target_dir"
  echo "- dry_run: $dry_run_output"
  echo "- import: $import_output"
  echo "- user_query: ok"
  echo "- channel_query: ok"
  echo "- conversation_query: ok"
  echo "- message_query: ok"
} > "$summary_file"

echo "summary=$summary_file"
echo "wkdb import smoke passed"
