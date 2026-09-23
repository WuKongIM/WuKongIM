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

class Interrupted(Exception):
    def __init__(self, signum):
        self.signum = signum

def interrupt(signum, _frame):
    raise Interrupted(signum)

def stop_group():
    # Once stopping, additional caller signals must not abandon owned children.
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        child.wait(timeout=kill_after)
    except subprocess.TimeoutExpired:
        pass
    finally:
        # A shell can exit on TERM while a grandchild ignores it. Clean the
        # complete owned group even when the direct child has already exited.
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait()

signal.signal(signal.SIGINT, interrupt)
signal.signal(signal.SIGTERM, interrupt)
try:
    status = child.wait(timeout=seconds)
    sys.exit(status if status >= 0 else 128 - status)
except subprocess.TimeoutExpired:
    stop_group()
    sys.exit(124)
except Interrupted as stopped:
    stop_group()
    sys.exit(128 + stopped.signum)
' "$seconds" "$kill_after" "$@"
