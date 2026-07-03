#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
printf '[deprecated] %s moved to scripts/e2e/dynamic-node-readiness-gate.sh\n' "$(basename "$0")" >&2
exec "$ROOT_DIR/scripts/e2e/dynamic-node-readiness-gate.sh" "$@"
