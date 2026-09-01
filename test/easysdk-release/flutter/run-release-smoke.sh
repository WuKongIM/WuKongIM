#!/usr/bin/env bash

set -euo pipefail

readonly FLUTTER_RELEASE_SMOKE_TIMEOUT_SECONDS="${FLUTTER_RELEASE_SMOKE_TIMEOUT_SECONDS:-480}"

run_bounded_flutter_test() {
  python3 - "${FLUTTER_RELEASE_SMOKE_TIMEOUT_SECONDS}" \
    flutter test integration_test/release_smoke_test.dart \
      -d "${SIMULATOR_ID}" \
      --dart-define="ALICE_UID=${ALICE_UID}" \
      --dart-define="ALICE_TOKEN=${ALICE_TOKEN}" \
      --dart-define="BOB_UID=${BOB_UID}" \
      --dart-define="ALICE_TO_BOB_TEXT=${ALICE_TO_BOB_TEXT}" \
      --dart-define="BOB_TO_ALICE_TEXT=${BOB_TO_ALICE_TEXT}" <<'PY'
import os
import signal
import subprocess
import sys

timeout_seconds = int(sys.argv[1])
command = sys.argv[2:]
process = subprocess.Popen(command, start_new_session=True)

try:
    status = process.wait(timeout=timeout_seconds)
except subprocess.TimeoutExpired:
    print(
        f"FLUTTER_RELEASE_SMOKE_TIMEOUT seconds={timeout_seconds}",
        file=sys.stderr,
        flush=True,
    )
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        pass
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
    raise SystemExit(124)

raise SystemExit(status)
PY
}

print_timeout_diagnostics() {
  echo "Flutter timeout diagnostics (commands omitted to protect test credentials):" >&2
  xcrun simctl list devices >&2 || true
  ps -Ao pid=,ppid=,state=,etime=,comm= \
    | awk '$5 ~ /(Flutter|flutter|dart|xcodebuild|CoreSimulator|Simulator)/' >&2 \
    || true
}

echo "FLUTTER_RELEASE_SMOKE_ATTEMPT attempt=1"
set +e
run_bounded_flutter_test
status=$?
set -e

if [[ ${status} -eq 124 ]]; then
  print_timeout_diagnostics
fi
if [[ ${status} -ne 0 ]]; then
  exit "${status}"
fi

"${GITHUB_WORKSPACE}/test/easysdk-release/verify-peer.sh"
