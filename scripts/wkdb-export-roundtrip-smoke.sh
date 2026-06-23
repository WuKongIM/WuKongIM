#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage: scripts/wkdb-export-roundtrip-smoke.sh [--work-dir DIR] [--hash-slot-count N] [--keep]

Builds a seed node with scripts/wkdb-import-smoke.sh, exports that node with
wkdb export, validates the exported WKDB Import Bundle v1, imports it into a
second fresh node data directory, and verifies copied rows with wkdb query.

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
  work_dir="$(mktemp -d "${TMPDIR:-/tmp}/wkdb-export-roundtrip-smoke.XXXXXX")"
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
roundtrip_dir="$work_dir/roundtrip"
summary_file="$work_dir/summary.md"
rm -rf "$seed_work_dir" "$export_dir" "$roundtrip_dir"
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

assert_contains() {
  local haystack="$1"
  local needle="$2"
  if [[ "$haystack" != *"$needle"* ]]; then
    echo "output missing $needle" >&2
    echo "$haystack" >&2
    exit 1
  fi
}

seed_output=""
if ! seed_output="$(cd "$repo_root" && WKDB_BIN="${WKDB_BIN:-}" GO="${GO:-go}" scripts/wkdb-import-smoke.sh --work-dir "$seed_work_dir" --hash-slot-count "$hash_slot_count")"; then
  echo "$seed_output" >&2
  exit 1
fi
assert_contains "$seed_output" "wkdb import smoke passed"
echo "seed import ok"

seed_data_dir="$seed_work_dir/node-new"
export_output="$(capture_wkdb --data-dir "$seed_data_dir" --hash-slot-count "$hash_slot_count" export --output "$export_dir" --page-size 1 --message-file-rows 1)"
assert_contains "$export_output" "exported=9"
assert_contains "$export_output" "messages=1"
assert_contains "$export_output" "subscribers=1"
echo "export ok"

dry_run_output="$(capture_wkdb --hash-slot-count "$hash_slot_count" import --input "$export_dir" --dry-run)"
assert_contains "$dry_run_output" "validated=9"
echo "export dry-run ok"

roundtrip_output="$(capture_wkdb --data-dir "$roundtrip_dir" --hash-slot-count "$hash_slot_count" import --input "$export_dir" --require-empty)"
assert_contains "$roundtrip_output" "validated=9"
assert_contains "$roundtrip_output" "written=8"
assert_contains "$roundtrip_output" "messages=1"
assert_contains "$roundtrip_output" "subscribers=1"
echo "roundtrip import ok"

user_json="$(capture_wkdb --data-dir "$roundtrip_dir" --hash-slot-count "$hash_slot_count" --format json query "select * from meta.user where uid='smoke-u1'")"
channel_json="$(capture_wkdb --data-dir "$roundtrip_dir" --hash-slot-count "$hash_slot_count" --format json query "select * from meta.channel where channel_id='smoke-g1'")"
conversation_json="$(capture_wkdb --data-dir "$roundtrip_dir" --hash-slot-count "$hash_slot_count" --format json query "select * from meta.conversation where uid='smoke-u1'")"
message_json="$(capture_wkdb --data-dir "$roundtrip_dir" --format json query "select * from message.message where channel_key='smoke-g1:2' limit 10")"

assert_contains "$user_json" "smoke-token"
assert_contains "$channel_json" '"subscriber_count": 1'
assert_contains "$conversation_json" '"read_seq": 1'
assert_contains "$message_json" '"message_seq": 1'
assert_contains "$message_json" '"message_id": 1001'
assert_contains "$message_json" '"server_timestamp_ms": 1710000000003'
assert_contains "$message_json" '"payload": "aGk="'
echo "query ok"

{
  echo "# wkdb export roundtrip smoke"
  echo
  echo "- seed_data_dir: $seed_data_dir"
  echo "- exported_bundle: $export_dir"
  echo "- roundtrip_data_dir: $roundtrip_dir"
  echo "- seed_import: ok"
  echo "- export: $export_output"
  echo "- dry_run: $dry_run_output"
  echo "- roundtrip_import: $roundtrip_output"
  echo "- user_query: ok"
  echo "- channel_query: ok"
  echo "- conversation_query: ok"
  echo "- message_query: ok"
} > "$summary_file"

echo "summary=$summary_file"
echo "wkdb export roundtrip smoke passed"
