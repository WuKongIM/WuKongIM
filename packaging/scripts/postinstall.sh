#!/bin/sh
set -eu

# The package owns the service account and writable directories, but it never
# creates an active configuration or starts/enables the service.
systemd-sysusers /usr/lib/sysusers.d/wukongim.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/wukongim.conf

if command -v systemctl >/dev/null 2>&1; then
  systemctl daemon-reload >/dev/null 2>&1 || :
fi
