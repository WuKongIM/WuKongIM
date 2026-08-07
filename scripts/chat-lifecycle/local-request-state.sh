#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 init REQUEST_ID SOURCE_SHA | cleanup REQUEST_ID ZERO_INVENTORY_JSON" >&2
}

resolve_default_state_root() {
  local account_home=''
  if command -v getent >/dev/null 2>&1; then
    account_home="$(getent passwd "$(id -u)" | awk -F: 'NR==1 {print $6}')"
  elif command -v dscl >/dev/null 2>&1; then
    account_home="$(dscl . -read "/Users/$(id -un)" NFSHomeDirectory | awk 'NR==1 {print $2}')"
  fi
  [[ "$account_home" == /* && -d "$account_home" ]] || {
    echo 'cannot resolve the current account home directory' >&2
    return 1
  }
  printf '%s\n' "$account_home/wukongim-leases/chat-lifecycle"
}

operation="${1:-}"
request_id="${2:-}"
[[ "$operation" == init || "$operation" == cleanup ]] || { usage; exit 2; }
[[ "$request_id" =~ ^chat-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$ ]] || {
  echo 'request ID must use chat-<UTC basic timestamp>-<8 lowercase hex>' >&2
  exit 2
}
command -v jq >/dev/null || { echo 'jq is required' >&2; exit 1; }
command -v ssh-keygen >/dev/null || { echo 'ssh-keygen is required' >&2; exit 1; }

state_root="${WK_CHAT_LIFECYCLE_STATE_ROOT:-}"
[[ -n "$state_root" ]] || state_root="$(resolve_default_state_root)"
[[ "$state_root" == /* && "$state_root" != / ]] || {
  echo 'state root must be an absolute non-root path' >&2
  exit 2
}
install -d -m 0700 "$state_root"
chmod 0700 "$state_root"
resolved_root="$(cd "$state_root" && pwd -P)"
request_dir="$resolved_root/$request_id"

case "$operation" in
  init)
    [[ $# -eq 3 ]] || { usage; exit 2; }
    source_sha="$3"
    [[ "$source_sha" =~ ^[0-9a-f]{40}$ ]] || {
      echo 'source SHA must be 40 lowercase hexadecimal characters' >&2
      exit 2
    }
    [[ ! -e "$request_dir" && ! -L "$request_dir" ]] || {
      echo 'request state already exists' >&2
      exit 1
    }
    install -d -m 0700 "$request_dir"
    identity="$request_dir/diagnostic_ed25519"
    ssh-keygen -q -t ed25519 -N '' -C "wukongim-chat-lifecycle-$request_id" -f "$identity"
    chmod 0600 "$identity" "$identity.pub"
    public_key="$(<"$identity.pub")"
    fingerprint="$(ssh-keygen -lf "$identity.pub" -E sha256 | awk '{print $2}')"
    jq -n --arg schema 'wukongim.chat_lifecycle.local_state/v1' \
      --arg request_id "$request_id" --arg source_sha "$source_sha" \
      --arg created_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg fingerprint "$fingerprint" \
      '{schema:$schema,request_id:$request_id,source_sha:$source_sha,created_at:$created_at,
        diagnostic_fingerprint:$fingerprint,state:"preflight"}' >"$request_dir/state.json"
    chmod 0600 "$request_dir/state.json"
    jq -n --arg request_id "$request_id" --arg state_dir "$request_dir" \
      --arg public_key "$public_key" --arg fingerprint "$fingerprint" \
      '{request_id:$request_id,state_dir:$state_dir,public_key:$public_key,fingerprint:$fingerprint}'
    ;;
  cleanup)
    [[ $# -eq 3 ]] || { usage; exit 2; }
    zero_inventory_path="$3"
    [[ -f "$zero_inventory_path" && ! -L "$zero_inventory_path" ]] || {
      echo 'zero-inventory proof must be one regular file' >&2
      exit 1
    }
    jq -e --arg request_id "$request_id" '
      .schema == "wukongim.cloud_lease.release/v1" and
      .result.zero_inventory.selector.request_id == $request_id and
      (.result.zero_inventory.account_id_hash | test("^sha256:[0-9a-f]{64}$")) and
      (.result.zero_inventory.observed_at | type == "string") and
      (.result.zero_inventory.scopes | type == "array" and length > 0)
    ' "$zero_inventory_path" >/dev/null || {
      echo 'authenticated exact zero-inventory proof is required before local credential deletion' >&2
      exit 1
    }
    [[ -d "$request_dir" && ! -L "$request_dir" ]] || {
      echo 'exact request state does not exist' >&2
      exit 1
    }
    resolved_request="$(cd "$request_dir" && pwd -P)"
    [[ "$(dirname "$resolved_request")" == "$resolved_root" && "$(basename "$resolved_request")" == "$request_id" ]] || {
      echo 'resolved cleanup target escaped the request-scoped state root' >&2
      exit 1
    }
    for name in \
      diagnostic_ed25519 diagnostic_ed25519.pub state.json access.json \
      encrypted-access.json encrypted-access-rehearsal.json encrypted-access-formal.json; do
      rm -f "$resolved_request/$name"
    done
    rmdir "$resolved_request" || {
      echo 'request state contains unexpected files; refusing broad deletion' >&2
      exit 1
    }
    jq -n --arg request_id "$request_id" --arg state_dir "$resolved_request" \
      '{request_id:$request_id,state_dir:$state_dir,credentials_deleted:true,zero_inventory_authenticated:true}'
    ;;
esac
