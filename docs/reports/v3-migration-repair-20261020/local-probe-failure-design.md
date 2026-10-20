# Local recovery probe storage-error preservation

Recorded before test implementation or product changes on 2026-10-20.

The parent explicitly assigned the existing `batchingRecoveryProbeDispatcher`
callback seam for this focused regression. The parent separately owns real
`DurableQuorumLog.Install` coverage and the product fix. This targeted seam is
needed because an Install fixture that injects completion errors skips the
local ReplicaStore adapter where item errors are currently lost.

## Failure cases

1. `ReplicaStore.Load` returns a wrapped batch-level storage failure. The
   admitted local probe callback must retain that cause through `errors.Is`,
   return no proof, and not add `channel.ErrNotReady`. This is an existing
   passing control, not an expected red case.
2. `ReplicaStore.Load` returns one `LoadResult` with a wrapped storage failure
   in `Items[0].Err`. The same callback contract applies. Before repair,
   `mapProbeResult` replaces this with `errInvalidExchangeResult`, so the
   original cause identity assertion is expected to fail.

The root's real Install negative control covers the next boundary:
`collectRecoveryProbeRound` must not discard a local permanent failure and
turn it into the temporary recovery-quorum-unavailable cause.

## Execution and ownership

- Reuse `manualPeerExecutor` and `recordingReplicaStore`.
- Add only `pkg/channel/replication/recovery_probe_error_test.go`.
- A tiny embedded-store override injects the batch-level failure; no product
  code, new goroutines, sleeps, networking, or persistent storage is needed.
- Run only `TestBatchingRecoveryProbeDispatcherPreservesLocalStorageErrors`
  with `GOWORK=off`, `-count=1`, and `-json`; save the exact command, exit code,
  source digests, and output beside this design.
- Do not modify product code or commit. Parent owns the green step.

## RPC coverage review

Existing registered read-RPC tests wrap transport causes before serialization
and verify public NotReady identity after top-level and item decoding.
`TestTransportClientPreservesChannelApplicationErrors` and
`TestTransportClientPreservesForwardApplicationErrors` cover bare NotReady.
There is no explicit joined NotReady-plus-detail case in the reviewed channels
tests. To cover the exact new shape without a duplicate codec-only test, add
that cause to the existing registered read-RPC table if the product fix uses
`errors.Join` or multi-`%w` wrapping.
