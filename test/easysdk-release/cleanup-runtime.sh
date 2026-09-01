#!/usr/bin/env bash

set -euo pipefail

if [[ -z "${ACCEPTANCE_ROOT:-}" ]]; then
  exit 0
fi

if [[ "${JOB_STATUS:-failure}" != "success" ]]; then
  echo "WuKongIM log (tail):"
  tail -200 "${ACCEPTANCE_ROOT}/server.log" 2>/dev/null || true
  echo "Released Web peer log:"
  cat "${ACCEPTANCE_ROOT}/peer.log" 2>/dev/null || true
fi

[[ -z "${PEER_PID:-}" ]] || kill "${PEER_PID}" 2>/dev/null || true
[[ -z "${WUKONGIM_PID:-}" ]] || kill "${WUKONGIM_PID}" 2>/dev/null || true
