#!/usr/bin/env bash
set -euo pipefail

active_state=''
job=''
line_count=0
while IFS= read -r line; do
  line_count=$(( line_count + 1 ))
  (( line_count <= 2 )) || exit 2
  case "$line" in
    ActiveState=*)
      [[ -z "$active_state" ]]
      active_state="${line#ActiveState=}"
      ;;
    Job=*)
      [[ -z "$job" ]]
      job="${line#Job=}"
      ;;
    *) exit 2 ;;
  esac
done

[[ -n "$active_state" && "$job" =~ ^[0-9]+$ ]]
case "$active_state" in
  active|activating|reloading)
    printf 'running\n'
    ;;
  inactive)
    if [[ "$job" == 0 ]]; then
      printf 'terminal\n'
    else
      printf 'pending\n'
    fi
    ;;
  failed|deactivating)
    printf 'terminal\n'
    ;;
  *) exit 2 ;;
esac
