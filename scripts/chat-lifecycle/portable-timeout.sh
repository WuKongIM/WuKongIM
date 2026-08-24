#!/usr/bin/env bash
set -euo pipefail

kill_after=5
while [[ "${1:-}" == --* ]]; do
  case "$1" in
    --foreground|--signal=TERM) shift ;;
    --kill-after=*)
      kill_after="${1#--kill-after=}"
      [[ "$kill_after" =~ ^([1-9][0-9]*)s$ ]] || {
        echo "usage: $0 [--foreground] [--signal=TERM] [--kill-after=SECONDSs] SECONDS[s] COMMAND [ARG...]" >&2
        exit 2
      }
      kill_after="${BASH_REMATCH[1]}"
      shift
      ;;
    *)
      echo "usage: $0 [--foreground] [--signal=TERM] [--kill-after=SECONDSs] SECONDS[s] COMMAND [ARG...]" >&2
      exit 2
      ;;
  esac
done

duration="${1:-}"
[[ "$duration" =~ ^([1-9][0-9]*)(s)?$ && $# -ge 2 ]] || {
  echo "usage: $0 [--foreground] [--signal=TERM] [--kill-after=SECONDSs] SECONDS[s] COMMAND [ARG...]" >&2
  exit 2
}
seconds="${BASH_REMATCH[1]}"
shift
exec python3 -c '
import os, signal, subprocess, sys

seconds = int(sys.argv[1])
kill_after = int(sys.argv[2])
command = sys.argv[3:]
child = subprocess.Popen(command, start_new_session=True)

def forward(signum, _frame):
    try:
        os.killpg(child.pid, signum)
    except ProcessLookupError:
        pass

signal.signal(signal.SIGINT, forward)
signal.signal(signal.SIGTERM, forward)
try:
    sys.exit(child.wait(timeout=seconds))
except subprocess.TimeoutExpired:
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        child.wait(timeout=kill_after)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait()
    sys.exit(124)
' "$seconds" "$kill_after" "$@"
