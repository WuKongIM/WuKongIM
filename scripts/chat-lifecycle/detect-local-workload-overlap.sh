#!/usr/bin/env bash
set -euo pipefail

# Print each non-owned local WuKongIM workload as "PID<TAB>COMMAND". Callers
# pass stable owned roots; every descendant is owned as well so short-lived
# helper CLIs cannot be mistaken for an overlapping benchmark.
owned=""
for pid in "$@"; do
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || {
    printf 'detect-local-workload-overlap: invalid owned PID\n' >&2
    exit 2
  }
  owned+=" $pid"
done

LC_ALL=C ps -axo pid=,ppid=,stat=,comm= | awk -v owned="$owned" '
  BEGIN {
    count = split(owned, pids, /[[:space:]]+/)
    for (pid_index = 1; pid_index <= count; pid_index++) {
      if (pids[pid_index] != "") owned_pid[pids[pid_index]] = 1
    }
  }
  {
    pid = $1
    parent[pid] = $2
    state_by_pid[pid] = $3
    command = $4
    sub(/^.*\//, "", command)
    command_by_pid[pid] = command
    process[++process_count] = pid
  }
  END {
    changed = 1
    while (changed) {
      changed = 0
      for (row = 1; row <= process_count; row++) {
        pid = process[row]
        if (!(pid in owned_pid) && (parent[pid] in owned_pid)) {
          owned_pid[pid] = 1
          changed = 1
        }
      }
    }
    for (row = 1; row <= process_count; row++) {
      pid = process[row]
      command = command_by_pid[pid]
      if (state_by_pid[pid] !~ /[EZ]/ && !(pid in owned_pid) &&
          (command == "wukongim" || command == "wkbench" || command == "wkbench-test")) {
        printf "%s\t%s\n", pid, command
      }
    }
  }
'
