#!/usr/bin/env bash
set -euo pipefail
trap 'printf "term\n" >>"$FAKE_SIGNAL_LOG"; sleep 0.25; printf "joined\n" >>"$FAKE_SIGNAL_LOG"; exit 143' TERM
printf '%s\n' "$$" >"$FAKE_CHILD_PID_FILE"
: >"$FAKE_READY_FILE"
while :; do sleep 0.05; done
