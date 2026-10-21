#!/usr/bin/env python3
"""Reduce pre-clock streams to fixed non-secret fields and bounded digests."""

import hashlib
import json
import signal
import sys


def reduce_stream(mode):
    digest = hashlib.sha256()
    count = 0
    prefix = bytearray()
    complete = True
    try:
        while True:
            chunk = sys.stdin.buffer.read(65536)
            if not chunk:
                break
            digest.update(chunk)
            count += len(chunk)
            if mode == "state" and len(prefix) < 8192:
                prefix.extend(chunk[:8192 - len(prefix)])
    except TimeoutError:
        complete = False
    result = {"bytes": count, "sha256": digest.hexdigest(), "complete": complete}
    if mode != "state":
        return result
    enums = {
        "ActiveState": {"active", "reloading", "inactive", "failed", "activating", "deactivating", "maintenance", "refreshing"},
        "LoadState": {"stub", "loaded", "not-found", "bad-setting", "error", "merged", "masked"},
        "SubState": {"dead", "condition", "start-pre", "start", "start-post", "running", "exited", "reload", "reload-signal", "reload-notify", "stop", "stop-watchdog", "stop-sigterm", "stop-sigkill", "stop-post", "final-watchdog", "final-sigterm", "final-sigkill", "failed", "auto-restart", "auto-restart-queued", "cleaning"},
        "Result": {"success", "resources", "protocol", "timeout", "exit-code", "signal", "core-dump", "watchdog", "start-limit-hit", "oom-kill", "exec-condition"},
    }
    numbers = {"Job": 4294967295, "ExecMainCode": 6, "ExecMainStatus": 255}
    values = {}
    invalid = set()
    hashes = {}
    unknown = False
    for line in bytes(prefix).splitlines():
        key, sep, value = line.partition(b"=")
        try:
            name = key.decode("ascii")
        except UnicodeDecodeError:
            unknown = True
            continue
        if not sep or name not in enums and name not in numbers:
            unknown = True
            continue
        if name in hashes:
            invalid.add(name)
            values.pop(name, None)
            continue
        hashes[name] = hashlib.sha256(value).hexdigest()
        try:
            text = value.decode("ascii")
        except UnicodeDecodeError:
            invalid.add(name)
            continue
        if name in enums and text in enums[name]:
            values[name] = text
        elif name in numbers and text.isascii() and text.isdecimal() and len(text) <= 10 and int(text) <= numbers[name]:
            values[name] = int(text)
        else:
            invalid.add(name)
    expected = set(enums) | set(numbers)
    result.update(schema="wukongim.chat_lifecycle.pre_clock_unit/v1", valid=count <= 8192 and not unknown and not invalid and set(values) == expected,
                  properties=values, field_sha256=hashes, invalid_fields=sorted(invalid), missing_fields=sorted(expected - set(hashes)),
                  unknown_fields=unknown, oversized=count > 8192)
    return result


if not (len(sys.argv) == 2 and sys.argv[1] in {"digest", "state"} or
        len(sys.argv) == 3 and sys.argv[1] == "stderr"):
    print("usage: pre-clock-diagnostic.py digest|state|stderr <metadata-output>", file=sys.stderr)
    sys.exit(2)
if sys.argv[1] == "stderr":
    # The original SSH command remains timeout 60. A stuck inherited descriptor
    # cannot make its diagnostic collector delay cleanup indefinitely.
    def expired(_signum, _frame):
        raise TimeoutError()
    signal.signal(signal.SIGALRM, expired)
    signal.alarm(65)
    record = reduce_stream("stderr")
    signal.alarm(0)
    try:
        with open(sys.argv[2], "w") as output:
            output.write(json.dumps(record, separators=(",", ":")) + "\n")
    except OSError:
        # Missing diagnostics never replace the SSH status or block Release.
        pass
else:
    print(json.dumps(reduce_stream(sys.argv[1]), separators=(",", ":")))
