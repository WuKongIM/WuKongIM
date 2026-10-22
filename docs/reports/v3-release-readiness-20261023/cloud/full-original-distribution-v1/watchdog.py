import datetime
import json
import os
from pathlib import Path
import signal
import sys
import time

group = int(sys.argv[1])
deadline = float(sys.argv[2])
receipt = Path(sys.argv[3])
if group <= 1 or os.getpgrp() != group or deadline <= time.monotonic():
    raise SystemExit("watchdog owner/deadline invalid")
cancelled = False


def record(state):
    tmp = receipt.with_suffix(".next")
    tmp.write_text(json.dumps({"state": state, "pgid": group,
        "at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat()}) + "\n")
    tmp.replace(receipt)


def cancel(_signal, _frame):
    global cancelled
    cancelled = True


signal.signal(signal.SIGTERM, signal.SIG_IGN)
signal.signal(signal.SIGUSR1, cancel)
record("armed")
while not cancelled and time.monotonic() < deadline:
    time.sleep(min(0.2, max(0, deadline - time.monotonic())))
if cancelled:
    record("cancelled_after_coordinator_exit")
    raise SystemExit(0)
record("deadline_term")
os.killpg(group, signal.SIGTERM)
time.sleep(5)
record("deadline_kill")
os.killpg(group, signal.SIGKILL)
