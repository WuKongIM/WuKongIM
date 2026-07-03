#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
printf '[deprecated] %s moved to scripts/start-wukongim-three-nodes.sh\n' "$(basename "$0")" >&2
exec "$ROOT_DIR/scripts/start-wukongim-three-nodes.sh" "$@"
