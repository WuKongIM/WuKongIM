#!/usr/bin/env bash
set -euo pipefail

if (($# < 2)); then
  echo "usage: $0 CHAT_REQUEST_ID LEASE_ID [cloud analysis options]" >&2
  exit 2
fi

chat_request_id="$1"
lease_id="$2"
shift 2

[[ "$chat_request_id" =~ ^chat-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]] || {
  echo 'chat-lifecycle analyze: invalid chat request identity' >&2
  exit 2
}
case "$lease_id" in
  "$chat_request_id-rehearsal-"[1-8]|"$chat_request_id-formal-"[1-8]) ;;
  *) echo 'chat-lifecycle analyze: Lease identity does not belong to the exact chat request' >&2; exit 2 ;;
esac

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec "$script_dir/../cloud-sim/analyze.sh" "$lease_id" --chat-request-id "$chat_request_id" "$@"
