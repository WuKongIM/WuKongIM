# Message update implementation validation

This is the first implementation-pass record. The subsequent
[review and performance report](2026-09-13-message-updates-review-performance.md)
supersedes its noop-read and cross-restore write limitations.

Date: 2026-09-13. Base: `0160c1f7d068b555c872df79ab6ab57f2cd4bc76`.
Branch: `codex/message-updates`; worktree: `.worktrees/message-updates`.
Environment: Apple M4, darwin/arm64, Go 1.25.11.

## Delivered scope

The server implements payload replacement, a single-channel incremental edit
feed, authoritative overlays on existing history/exact reads, and latest tails
on **both** `/conversation/list` and `/conversation/sync`. Negotiated body-free
EVENT hints have durable, bounded retry checkpoints. CMD messages remain
uneditable and their protocol is unchanged. Metadata includes lifecycle cleanup,
snapshot support and offline transfer/inspection support.

The [API contract](../specs/message-update-api.md) contains request examples,
bootstrap order, cursor/version rules, SDK merge requirements, rollout boundaries
and the measured performance cost.

## Executed checks

Commands below use `GOWORK=off`. Detailed raw logs are in the worktree's ignored
`tmp/message-update-*.log` files.

| Check | Outcome |
| --- | --- |
| Final unit suites: `go test ./pkg/db/meta ./pkg/db/transfer ./internal/access/api ./internal/access/gateway ./internal/app ./internal/runtime/messageupdates ./pkg/slot/proxy ./pkg/slot/fsm -count=1` | Passed |
| Affected regression suites: `go test ./pkg/db/transfer ./cmd/wkcli/internal/database ./internal/infra/migrationv2 ./pkg/cluster ./scripts ./internal/app -count=1` | Passed |
| Real clusters: `go test -tags=integration ./internal/app ./pkg/cluster -run 'TestMessageUpdateSingleNodeClusterHTTPFlow\|TestMessageUpdateThreeNodeQuorumAndLeaderTransfer' -count=1 -timeout=2m -v` | Both passed |
| Race checks: `go test -race ./internal/usecase/message ./internal/runtime/messageupdates ./internal/runtime/online ./internal/infra/delivery ./pkg/slot/proxy ./pkg/slot/fsm -run 'MessageUpdate\|RepairBudget' -count=1` | Passed for matching tests; online package had no matching tests |
| Index benchmark: `go test ./pkg/db/meta -run '^$' -bench BenchmarkMessageUpdateIncrementalIndex -benchtime=1000x -count=1` | Passed; 1k/10k retained edits: 15.0/14.4 μs per lookup, 45 allocations |
| Named `flow-doc-contracts`: `go run ./scripts/flowcheck --mode check` | Passed after index regeneration; eight nonblocking length warnings |
| `git diff --check` | Passed |

Unit coverage includes same-batch CAS/idempotency, latest-index movement,
cursor paging and visibility resets, stale notification completion, bounded
worker fairness, snapshot restoration, retention and delete/recreate fences,
negotiated exact-session hint delivery, and offline round-trip preservation of
all four edit projections and integers above JavaScript's exact-number boundary.

The single-node cluster test exercises the HTTP write/read/sync flow, both
conversation APIs, unchanged original log content, and pagination after two
600 KiB replacements. The three-node test edits through a nonleader, reads from
every node, transfers Slot leadership, writes with one activated replica offline,
reconstructs that node from its data directory, and rejects reads/writes without
quorum. Restore cursor/reset logic is unit-tested; this is not a complete
process-level backup/restore acceptance run.

## Repository-wide result

The full unit command was run:

```sh
GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1
```

It did **not** pass as a whole. Newly exposed offline inspection/transfer and
release-note category failures were fixed, and all affected package suites were
rerun successfully. The cluster readiness test also passed in its package rerun
after a timeout during the concurrent full run.

The remaining Grafana test `TestDashboardAssetsCoverAllExportedMetrics` also
fails on the original baseline checkout. It reports six missing existing
conversation persisted metrics: admission total, batch items, hold seconds,
inflight, limit and occupancy. That unrelated dashboard was not changed.
The full suite was not rerun after focused corrections; do not interpret the
focused passes as a clean repository-wide run.

## Remaining rollout work and limits

- Official SDK repositories were not changed. Capability negotiation,
  transactional merges, preview refresh and reconnect handling need SDK tests.
- Each physical Slot read group currently adds a fresh quorum/applied noop.
  HTTP fixture measurements are tens of milliseconds and do not establish
  production capacity or an acceptable polling interval. Run a baseline
  comparison with 256 Hash Slots, cross-Slot conversation pages, 100,000-member
  groups, edit bursts, large bodies and reconnects before enabling at scale.
- Matching server/CLI binaries are required after activating edits. The replica
  proof stores IDs, not build identities; mixed-version rejoin/downgrade is not
  supported.
- Restore generation protects pull cursors and client cache comparisons. Write
  requests do not carry an expected restore generation; coordinated restore must
  invalidate pre-restore backend editing workflows and retry queues.
- Changes are local to this worktree; no merge, deployment or public release was
  performed.
