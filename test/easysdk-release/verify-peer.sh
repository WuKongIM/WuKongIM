#!/usr/bin/env bash

set -euo pipefail

for _ in $(seq 1 60); do
  if grep -q '^PEER_PASS ' "${ACCEPTANCE_ROOT}/peer.log"; then
    cat "${ACCEPTANCE_ROOT}/peer.log"
    exit 0
  fi
  if ! kill -0 "${PEER_PID}" 2>/dev/null; then
    break
  fi
  sleep 1
done

echo "Released Web peer did not complete the bidirectional exchange" >&2
cat "${ACCEPTANCE_ROOT}/peer.log" >&2
exit 1
