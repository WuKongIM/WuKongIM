#!/usr/bin/env bash
set -euo pipefail

# Print each non-owned local WuKongIM workload as "PID<TAB>COMMAND". Callers
# pass every PID they own so the three-node diagnostic never confuses its own
# service, worker, host-metrics, or coordinator processes with interference.
owned=""
for pid in "$@"; do
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || {
    printf 'detect-local-workload-overlap: invalid owned PID\n' >&2
    exit 2
  }
  owned+=" $pid"
done

LC_ALL=C ps -axo pid=,stat=,comm= | awk -v owned="$owned" '
  BEGIN {
    count = split(owned, pids, /[[:space:]]+/)
    for (pid_index = 1; pid_index <= count; pid_index++) {
      if (pids[pid_index] != "") owned_pid[pids[pid_index]] = 1
    }
  }
  {
    pid = $1
    state = $2
    command = $3
    sub(/^.*\//, "", command)
    if (state !~ /[EZ]/ && !(pid in owned_pid) && (command == "wukongim" || command == "wkbench" || command == "wkbench-test")) {
      printf "%s\t%s\n", pid, command
    }
  }
'
