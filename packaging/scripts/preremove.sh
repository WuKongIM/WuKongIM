#!/bin/sh
set -eu

# Debian passes "remove" for a final uninstall; RPM passes 0. Upgrades must not
# interrupt a running cluster node because the operator owns restart timing.
case "${1:-}" in
  remove|purge|0)
    if command -v systemctl >/dev/null 2>&1; then
      systemctl stop wukongim.service >/dev/null 2>&1 || :
      systemctl disable wukongim.service >/dev/null 2>&1 || :
    fi
    ;;
esac
