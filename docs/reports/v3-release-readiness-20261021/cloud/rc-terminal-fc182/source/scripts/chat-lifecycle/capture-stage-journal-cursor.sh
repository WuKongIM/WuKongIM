#!/usr/bin/env bash
set -euo pipefail

if (( $# != 2 )); then
  echo 'usage: capture-stage-journal-cursor.sh <ssh-config> <host>' >&2
  exit 2
fi

ssh_config="$1"
host="$2"
[[ -f "$ssh_config" && "$host" =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]] || exit 2

# Capture the global journal tail. A unit-scoped query has no cursor before a
# service's first start, which would make its first terminal summary unreadable.
cursor="$(timeout 60 ssh -F "$ssh_config" "$host" \
  "sudo journalctl --no-pager -n 0 --show-cursor" 2>/dev/null | \
  sed -n 's/^-- cursor: //p' | tail -n 1)"
(( ${#cursor} >= 1 && ${#cursor} <= 512 )) || exit 1
LC_ALL=C grep -Eq '^[A-Za-z0-9_=;:.-]+$' <<<"$cursor" || exit 1
printf '%s\n' "$cursor"
