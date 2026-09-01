#!/usr/bin/env bash

set -euo pipefail

simulator_id="$({ xcrun simctl list devices available -j; } | python3 -c '
import json
import sys

devices = json.load(sys.stdin)["devices"]
preferred = []
fallback = []
for runtime, candidates in devices.items():
    if "iOS" not in runtime:
        continue
    for device in candidates:
        if not device.get("isAvailable") or not device.get("name", "").startswith("iPhone"):
            continue
        fallback.append(device["udid"])
        if device["name"].startswith(("iPhone 16", "iPhone 17")):
            preferred.append(device["udid"])

selected = preferred[0] if preferred else (fallback[0] if fallback else None)
if selected is None:
    raise SystemExit("No available iPhone Simulator")
print(selected)
')"

xcrun simctl boot "${simulator_id}" 2>/dev/null || true
xcrun simctl bootstatus "${simulator_id}" -b
echo "SIMULATOR_ID=${simulator_id}" >>"${GITHUB_ENV}"
echo "IOS_SIMULATOR_READY id=${simulator_id}"
