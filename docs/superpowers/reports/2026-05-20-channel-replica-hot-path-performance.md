# Channel Replica Hot Path Performance Report

Date: 2026-05-20
Branch: `perf/channel-replica-hot-path`
Worktree: `.worktrees/channel-replica-hot-path-performance`

## Summary

This pass keeps the channel replica correctness model unchanged and focuses on low-risk hot-path improvements around scheduling, diagnostics, durable-lane contention, and allocation visibility.

Completed changes:

- Added focused microbenchmarks for replica append, pooled mailbox delivery, quorum candidate calculation, follower cursor progress, durable lane acquire/release, and clone-vs-owned append paths.
- Replaced pooled loop mailbox slice-shift dequeue with a bounded ring buffer to avoid O(n) queue compaction under pressure.
- Reduced normal-path follower cursor diagnostic allocations by avoiding progress snapshots unless debug logging is enabled or an error path needs diagnostics.
- Replaced durable-mutation `TryLock` polling with a cancellable durable lane that preserves serialized durable writes and honors context cancellation.
- Verified the runtime `AppendOwned` path reaches replica-owned append without an extra caller-record clone.
- Fixed a race surfaced during final verification where successful append completion could publish to the caller before all map cleanup captured the pooled request ID.

Deferred items remain unchanged: quorum semantics, checkpoint commit-readiness relaxation, fetch long-polling, ACK coalescing, and virtual channel sharding.

## Benchmark Results

Environment:

- `goos=darwin`, `goarch=arm64`, `cpu=Apple M4`
- Command: `GOWORK=off go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkReplicaAppendHotPath|BenchmarkPooledMailboxSubmitResult|BenchmarkQuorumProgressCandidate|BenchmarkReplicaApplyFollowerCursor|BenchmarkReplicaAppendCloneVsOwned' -benchmem -count=5`

Representative post-change results:

| Benchmark | Result |
| --- | --- |
| `BenchmarkReplicaAppendHotPath/local/batch=1` | ~6.8-7.0 us/op, ~2.9 KB/op, 26 allocs/op |
| `BenchmarkReplicaAppendHotPath/local/batch=16` | ~9.1-11.3 us/op, ~13 KB/op, 56 allocs/op |
| `BenchmarkReplicaAppendHotPath/local/batch=64` | ~14.6-15.8 us/op, ~45-47 KB/op, 152 allocs/op |
| `BenchmarkReplicaAppendHotPath/quorum/batch=1` | ~6.1-6.8 us/op, ~2.8 KB/op, 26 allocs/op |
| `BenchmarkReplicaAppendHotPath/quorum/batch=16` | ~8.5-9.3 us/op, ~13 KB/op, 56 allocs/op |
| `BenchmarkReplicaAppendHotPath/quorum/batch=64` | ~14.2-15.3 us/op, ~46-49 KB/op, 152 allocs/op |
| `BenchmarkPooledMailboxSubmitResult` | ~161-168 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkQuorumProgressCandidate/isr=3` | ~14.5-15.3 ns/op, 0 B/op, 0 allocs/op |
| `BenchmarkQuorumProgressCandidate/isr=64` | ~382-390 ns/op, 512 B/op, 1 alloc/op |
| `BenchmarkReplicaApplyFollowerCursor` | ~785-830 ns/op, 368 B/op, 4 allocs/op |
| `BenchmarkReplicaAppendCloneVsOwned/clone` | ~7.5-8.0 us/op, ~4.7 KB/op, 26 allocs/op |
| `BenchmarkReplicaAppendCloneVsOwned/owned` | ~7.4-7.5 us/op, ~3.6 KB/op, 24 allocs/op |

Notable before/after observation from the working session:

- `BenchmarkReplicaApplyFollowerCursor` improved from roughly `~1030 ns/op, 944 B/op, 12 allocs/op` to roughly `~785-830 ns/op, 368 B/op, 4 allocs/op` by avoiding normal-path diagnostic map/slice snapshots.
- `AppendOwned` saves about `~1.1 KB/op` and `2 allocs/op` in the clone-vs-owned microbenchmark, confirming the runtime owned batch path avoids the extra caller-facing record clone.

## Verification

Passed:

```bash
GOWORK=off go test -race ./pkg/channel/replica -run 'TestAppendPipelineBatchesBurstAppends|TestAppendLocalModeCompletesAfterLeaderDurableAppend|TestFenceAndDrainWaitsForLocalCommitTailToBecomeStable|TestDrainedFailClosedReopensOnlyAfterNewerVersionClear|TestAppendDurableResultAfterLeaseRenewalPublishesLEO|TestApplyMetaSameLeader|TestCursorDeltaAdvancesHWWithoutFollowUpFetch' -count=1 -timeout=60s
GOWORK=off go test -race ./pkg/channel/replica -timeout=120s
GOWORK=off go test -race ./pkg/channel/replica ./pkg/channel/runtime -timeout=120s
GOWORK=off go test ./pkg/channel/replica ./pkg/channel/runtime ./pkg/channel/store
GOWORK=off go test ./pkg/channel/replica -run '^$' -bench 'BenchmarkReplicaAppendHotPath|BenchmarkPooledMailboxSubmitResult|BenchmarkQuorumProgressCandidate|BenchmarkReplicaApplyFollowerCursor|BenchmarkReplicaAppendCloneVsOwned' -benchmem -count=5
```

Notes:

- Commands use `GOWORK=off` because the parent repository has a `go.work`; without it, the isolated worktree resolves imports through the parent workspace.
- A first combined race run surfaced the append request reuse race and was fixed in this branch before the passing race runs above.

## Follow-Up Optimization Candidates

- Reduce append allocations in `BenchmarkReplicaAppendHotPath` by separating immutable batch metadata from pooled request envelopes and reusing effect record buffers safely.
- Remove the quorum candidate scratch allocation for larger ISR sizes by using bounded stack storage or a reusable loop-owned scratch buffer.
- Add a deeper pooled-mailbox pressure benchmark that keeps the mailbox near capacity; the current submit-result microbenchmark validates 0 allocations but does not fully expose the O(n) slice-shift cost that the ring buffer removes.
- Investigate ACK coalescing and fetch long-polling in separate plans because those change cross-component timing behavior and need broader correctness coverage.
