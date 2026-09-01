#!/usr/bin/env bash

set -euo pipefail

readonly FLUTTER_RELEASE_SMOKE_BUILD_TIMEOUT_SECONDS="${FLUTTER_RELEASE_SMOKE_BUILD_TIMEOUT_SECONDS:-480}"
readonly FLUTTER_RELEASE_SMOKE_COMMAND_TIMEOUT_SECONDS="${FLUTTER_RELEASE_SMOKE_COMMAND_TIMEOUT_SECONDS:-60}"
readonly FLUTTER_RELEASE_SMOKE_RECEIPT_TIMEOUT_SECONDS="${FLUTTER_RELEASE_SMOKE_RECEIPT_TIMEOUT_SECONDS:-90}"

run_bounded_command() {
  local timeout_seconds="$1"
  shift
  python3 - "${timeout_seconds}" "$@" <<'PY'
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
        "FLUTTER_RELEASE_SMOKE_TIMEOUT "
        f"command={os.path.basename(command[0])} seconds={timeout_seconds}",
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

run_stage() {
  local stage="$1"
  local timeout_seconds="$2"
  shift 2

  echo "FLUTTER_RELEASE_SMOKE_STAGE stage=${stage}"
  set +e
  run_bounded_command "${timeout_seconds}" "$@"
  local status=$?
  set -e
  if [[ ${status} -eq 124 ]]; then
    print_timeout_diagnostics
  fi
  if [[ ${status} -ne 0 ]]; then
    echo "Flutter release smoke stage failed: ${stage} (status ${status})" >&2
  fi
  return "${status}"
}

run_stage build "${FLUTTER_RELEASE_SMOKE_BUILD_TIMEOUT_SECONDS}" \
  flutter build ios --simulator --debug --target=lib/release_smoke_app.dart

readonly app_path="build/ios/iphonesimulator/Runner.app"
test -d "${app_path}"
bundle_id="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' \
  "${app_path}/Info.plist")"
test -n "${bundle_id}"

xcrun simctl terminate "${SIMULATOR_ID}" "${bundle_id}" 2>/dev/null || true
xcrun simctl uninstall "${SIMULATOR_ID}" "${bundle_id}" 2>/dev/null || true
run_stage install "${FLUTTER_RELEASE_SMOKE_COMMAND_TIMEOUT_SECONDS}" \
  xcrun simctl install "${SIMULATOR_ID}" "${app_path}"

container="$(xcrun simctl get_app_container \
  "${SIMULATOR_ID}" "${bundle_id}" data)"
config="${container}/tmp/release-smoke-config.json"
receipt="${container}/tmp/release-smoke.json"

cleanup_release_smoke_files() {
  rm -f -- "${config}" "${receipt}"
}
trap cleanup_release_smoke_files EXIT

python3 - "${config}" "${receipt}" <<'PY'
import json
import os
from pathlib import Path
import sys

config_path = Path(sys.argv[1])
receipt_path = Path(sys.argv[2])
try:
    receipt_path.unlink()
except FileNotFoundError:
    pass

names = (
    "ALICE_UID",
    "ALICE_TOKEN",
    "BOB_UID",
    "ALICE_TO_BOB_TEXT",
    "BOB_TO_ALICE_TEXT",
)
payload = {}
for name in names:
    value = os.environ.get(name)
    if not value:
        raise SystemExit(f"Missing required release smoke setting: {name}")
    payload[name] = value

temporary_path = config_path.with_name(f"{config_path.name}.tmp")
with temporary_path.open("w", encoding="utf-8") as config_file:
    json.dump(payload, config_file)
    config_file.flush()
    os.fsync(config_file.fileno())
os.chmod(temporary_path, 0o600)
os.replace(temporary_path, config_path)
PY

run_stage launch "${FLUTTER_RELEASE_SMOKE_COMMAND_TIMEOUT_SECONDS}" \
  xcrun simctl launch "${SIMULATOR_ID}" "${bundle_id}"

for _ in $(seq 1 "${FLUTTER_RELEASE_SMOKE_RECEIPT_TIMEOUT_SECONDS}"); do
  if [[ -f "${receipt}" ]]; then
    break
  fi
  sleep 1
done

if [[ ! -f "${receipt}" ]]; then
  echo "Flutter release smoke receipt was not created" >&2
  print_timeout_diagnostics
  exit 1
fi

cat "${receipt}"
python3 - "${receipt}" <<'PY'
import json
import sys

with open(sys.argv[1], encoding="utf-8") as receipt_file:
    receipt = json.load(receipt_file)
if receipt.get("status") != "PASS":
    raise SystemExit(
        "Flutter release smoke failed at "
        f"{receipt.get('stage', 'unknown stage')}: "
        f"{receipt.get('error_type', 'unknown error')}"
    )
PY

"${GITHUB_WORKSPACE}/test/easysdk-release/verify-peer.sh"
