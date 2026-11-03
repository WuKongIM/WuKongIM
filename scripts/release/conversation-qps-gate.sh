#!/usr/bin/env bash
# Run the fixed conversation QPS release gate against the checked-out source
# and validate its report. Used by the release workflow (blocking, exact tag)
# and by perf.yml (nightly early warning on main).
#
# Cluster data lives on tmpfs: hosted-runner disk speed differs widely between
# allocations, and the gate measures cold-runtime reads with warm page caches,
# not disk latency. Fixed workload and limits stay in profile.json.
#
# Usage: conversation-qps-gate.sh EVIDENCE_DIR
set -euo pipefail
[[ $# == 1 ]] || { echo 'usage: conversation-qps-gate.sh EVIDENCE_DIR' >&2; exit 2; }
evidence="$1"
mkdir -p "$evidence"
evidence="$(cd "$evidence" && pwd)"

[[ "$(findmnt -n -o FSTYPE -T /dev/shm)" == tmpfs ]] || { echo '/dev/shm is not tmpfs' >&2; exit 2; }
data_dir="$(mktemp -d /dev/shm/wk-conversation-qps.XXXXXXXX)"
trap 'rm -rf -- "$data_dir"' EXIT

export GOWORK=off TMPDIR="$data_dir"
export WK_E2E_CONVERSATION_QPS=1 WK_E2E_CONVERSATION_QPS_REPORT="$evidence/report.json"
unset WK_E2E_BINARY
git rev-parse HEAD >"$evidence/source.sha"
lscpu --json >"$evidence/host.json" 2>/dev/null || :

go test ./test/e2e/message/conversation_qps -count=1
go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSReleaseGate$' \
  -count=1 -timeout=18m -p=1 -v 2>&1 | tee "$evidence/test.log"

profile=test/e2e/message/conversation_qps/profile.json
jq -e --arg sha "$(git rev-parse HEAD)" \
  --arg profile_sha "$(sha256sum "$profile" | cut -d ' ' -f 1)" \
  --slurpfile profile "$profile" \
  -f scripts/validate-conversation-qps-report.jq "$WK_E2E_CONVERSATION_QPS_REPORT"
[[ -z "$(git status --porcelain)" ]]
