#!/usr/bin/env bash
set -euo pipefail

seconds="${1:-}"
[[ "$seconds" =~ ^[1-9][0-9]*$ && $# -ge 2 ]] || {
  echo "usage: $0 SECONDS COMMAND [ARG...]" >&2
  exit 2
}
shift
exec python3 -c '
import os, signal, subprocess, sys

seconds = int(sys.argv[1])
command = sys.argv[2:]
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
        child.wait(timeout=5)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait()
    sys.exit(124)
' "$seconds" "$@"
