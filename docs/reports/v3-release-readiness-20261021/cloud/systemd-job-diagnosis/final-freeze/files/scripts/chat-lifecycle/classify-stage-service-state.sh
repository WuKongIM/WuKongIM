#!/usr/bin/env bash
set -euo pipefail

active_state=''
job=''
active_seen=0
job_seen=0
line_count=0
while IFS= read -r line || [[ -n "$line" ]]; do
  line_count=$(( line_count + 1 ))
  (( line_count <= 2 )) || exit 2
  case "$line" in
    ActiveState=*)
      [[ "$active_seen" == 0 ]] || exit 2
      active_seen=1
      active_state="${line#ActiveState=}"
      ;;
    Job=*)
      [[ "$job_seen" == 0 ]] || exit 2
      job_seen=1
      job="${line#Job=}"
      ;;
    *) exit 2 ;;
  esac
done

# systemctl show renders a present, zero-valued Job as empty. Missing fields
# remain invalid; explicit exits preserve that contract on Bash 3 and Bash 5.
[[ "$active_seen" == 1 && "$job_seen" == 1 && -n "$active_state" ]] || exit 2
if [[ -z "$job" ]]; then
  job=0
else
  [[ "$job" =~ ^[0-9]+$ && ${#job} -le 10 ]] || exit 2
  # Job is uint32. Bound digits before arithmetic and treat leading zeros as decimal.
  job=$(( 10#$job ))
  (( job <= 4294967295 )) || exit 2
fi
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
