#!/bin/sh
set -eu

# Configuration, data, logs, and the service account intentionally survive
# removal so an uninstall or rollback cannot destroy operator-owned state.
if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || :
fi
