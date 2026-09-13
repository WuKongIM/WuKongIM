#!/usr/bin/env bash
set -euo pipefail

# Startup readiness may change while initial Slot authority converges. Require
# three consecutive samples before issuing fixture mutations, within 90 polls.
if [[ $# -ne 2 || ! "$1" =~ ^[0-9]+$ ]]; then
  echo "usage: $0 <server-pid> <server-log>" >&2
  exit 2
fi
server_pid="$1"
server_log="$2"
consecutive=0
for attempt in $(seq 1 90); do
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "WuKongIM exited before stable readiness" >&2
    tail -200 "$server_log" >&2 || true
    exit 1
  fi
  if curl -fsS --max-time 1 http://127.0.0.1:5001/readyz >/dev/null; then
    consecutive=$((consecutive + 1))
    if [[ "$consecutive" -eq 3 ]]; then
      exit 0
    fi
  else
    consecutive=0
  fi
  [[ "$attempt" -eq 90 ]] || sleep 1
done
echo "WuKongIM did not reach stable readiness within 90 polls" >&2
tail -200 "$server_log" >&2 || true
exit 1
