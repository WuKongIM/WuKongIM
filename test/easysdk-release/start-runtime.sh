#!/usr/bin/env bash

set -euo pipefail

if [[ $# -ne 1 ]]; then
  echo "usage: $0 <android|ios|flutter>" >&2
  exit 2
fi

readonly PLATFORM="$1"
readonly RECEIPT_ROOT="${RUNNER_TEMP}/easysdk-release-${PLATFORM}"
readonly PEER_ROOT="${GITHUB_WORKSPACE}/test/easysdk-release/peer"
readonly RUN_SUFFIX="${GITHUB_RUN_ID}-${GITHUB_RUN_ATTEMPT}"
readonly ALICE_UID_VALUE="release-${PLATFORM}-alice-${RUN_SUFFIX}"
readonly BOB_UID_VALUE="release-${PLATFORM}-bob-${RUN_SUFFIX}"
readonly ALICE_TOKEN_VALUE="release-${PLATFORM}-alice-token-${RUN_SUFFIX}"
readonly BOB_TOKEN_VALUE="release-${PLATFORM}-bob-token-${RUN_SUFFIX}"
readonly ALICE_TO_BOB_VALUE="${PLATFORM}-released-package-alice-to-bob-${RUN_SUFFIX}"
readonly BOB_TO_ALICE_VALUE="${PLATFORM}-released-package-bob-to-alice-${RUN_SUFFIX}"
server_pid=""
peer_pid=""

cleanup_on_error() {
  local status=$?
  trap - EXIT
  if [[ ${status} -ne 0 ]]; then
    [[ -z "${peer_pid}" ]] || kill "${peer_pid}" 2>/dev/null || true
    [[ -z "${server_pid}" ]] || kill "${server_pid}" 2>/dev/null || true
  fi
  exit "${status}"
}

trap cleanup_on_error EXIT

mkdir -p "${RECEIPT_ROOT}"

GOWORK=off go build -o "${RECEIPT_ROOT}/wukongim" ./cmd/wukongim

pushd "${RECEIPT_ROOT}" >/dev/null
nohup "${RECEIPT_ROOT}/wukongim" \
  -config "${GITHUB_WORKSPACE}/wukongim.toml.example" \
  >"${RECEIPT_ROOT}/server.log" 2>&1 &
server_pid=$!
popd >/dev/null

for _ in $(seq 1 90); do
  if curl -fsS http://127.0.0.1:5001/readyz >/dev/null; then
    break
  fi
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    echo "WuKongIM exited before readiness" >&2
    tail -200 "${RECEIPT_ROOT}/server.log" >&2
    exit 1
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:5001/readyz >/dev/null

curl -fsS -X POST http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d "{\"uid\":\"${ALICE_UID_VALUE}\",\"token\":\"${ALICE_TOKEN_VALUE}\",\"device_flag\":0,\"device_level\":1}" \
  >/dev/null
curl -fsS -X POST http://127.0.0.1:5001/user/token \
  -H 'Content-Type: application/json' \
  -d "{\"uid\":\"${BOB_UID_VALUE}\",\"token\":\"${BOB_TOKEN_VALUE}\",\"device_flag\":1,\"device_level\":1}" \
  >/dev/null

npm ci --prefix "${PEER_ROOT}" --ignore-scripts --no-audit --no-fund

PEER_WS_URL=ws://127.0.0.1:5200 \
ALICE_UID="${ALICE_UID_VALUE}" \
BOB_UID="${BOB_UID_VALUE}" \
BOB_TOKEN="${BOB_TOKEN_VALUE}" \
ALICE_TO_BOB_TEXT="${ALICE_TO_BOB_VALUE}" \
BOB_TO_ALICE_TEXT="${BOB_TO_ALICE_VALUE}" \
  nohup node "${PEER_ROOT}/peer.cjs" >"${RECEIPT_ROOT}/peer.log" 2>&1 &
peer_pid=$!

for _ in $(seq 1 30); do
  if grep -q '^PEER_READY ' "${RECEIPT_ROOT}/peer.log"; then
    break
  fi
  if ! kill -0 "${peer_pid}" 2>/dev/null; then
    echo "Released Web peer exited before readiness" >&2
    cat "${RECEIPT_ROOT}/peer.log" >&2
    exit 1
  fi
  sleep 1
done
grep -q '^PEER_READY ' "${RECEIPT_ROOT}/peer.log"

{
  echo "ACCEPTANCE_PLATFORM=${PLATFORM}"
  echo "ACCEPTANCE_ROOT=${RECEIPT_ROOT}"
  echo "ALICE_UID=${ALICE_UID_VALUE}"
  echo "BOB_UID=${BOB_UID_VALUE}"
  echo "ALICE_TOKEN=${ALICE_TOKEN_VALUE}"
  echo "ALICE_TO_BOB_TEXT=${ALICE_TO_BOB_VALUE}"
  echo "BOB_TO_ALICE_TEXT=${BOB_TO_ALICE_VALUE}"
  echo "WUKONGIM_PID=${server_pid}"
  echo "PEER_PID=${peer_pid}"
} >>"${GITHUB_ENV}"

trap - EXIT
echo "RUNTIME_READY platform=${PLATFORM} server_source=${GITHUB_SHA}"
cat "${RECEIPT_ROOT}/peer.log"
