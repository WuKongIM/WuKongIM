# Channelv2 Replication Robustness Design

**Date:** 2026-05-24
**Status:** Proposed

## Overview

Phase 2 moved `pkg/channelv2` replication into reactor-owned runtime state and made `Pull`, follower apply, and `Ack` execute through typed bounded worker pools. That slice validates the intended concurrency model, but it is still an experimental correctness slice. Phase 3 should harden the existing short-poll replication protocol before adding production-only features.

This design strengthens the current `Pull` + explicit `Ack` model under failure, backpressure, cancellation, shutdown, and metadata changes. The goal is not to make `channelv2` production-ready by itself. The goal is to prove that the reactor-owned replication state machine is deterministic and safe enough to justify later productionization work.

Phase 3 keeps the current public facade shape and package boundary. It does not replace `pkg/channel`, does not wire into `internal/app`, and does not introduce long-poll sessions, snapshots, retention, migration, or leader repair.

## Goals

- Make follower replication state deterministic across pull, apply, ack, retry, and metadata fence transitions.
- Prove stale worker completions cannot mutate current follower state or complete current leader-side pull waiters.
- Preserve one-inflight limits for follower pull RPC, follower store apply, follower ack RPC, and one pending pull response.
- Keep follower pull offsets based on local `LEO + 1`.
- Ensure RPC and store worker backpressure retain necessary pending replication state and retry by explicit ticks.
- Ensure caller cancellation, metadata fence changes, group close, and worker-pool close complete or release all leader-side pull waiters.
- Prevent stale or regressive ACKs from advancing leader HW incorrectly.
- Document the hardened replication failure semantics in `pkg/channelv2/FLOW.md`.

## Non-Goals

- No long-poll lane, streaming replication, or ACK piggyback protocol.
- No snapshot install, snapshot catch-up, or retention trim.
- No leader repair, follower truncation, reconcile loop, or promotion dry run.
- No migration cutover, fence-and-drain, or `internal/app` integration.
- No replacement of existing `pkg/channel` or changes outside `pkg/channelv2` except docs under `docs/superpowers`.
- No real network, Docker, or slow integration tests in this phase.
- No single-node bypass branch. Single-node behavior remains single-node cluster quorum behavior.

## Current State

The current Phase 2 implementation already has the main shape required for robust replication:

```text
follower reactor tick
  -> TaskRPCPull(leader, LEO+1)
  -> TaskStoreApply(records, leaderHW)
  -> TaskRPCAck(matchOffset)
  -> leader EventAck advances HW
```

The follower runtime currently tracks:

- `pullInflight` and `pullOpID`
- `pendingPull`
- `applyOpID`, `applyBlocked`, and `applyRetryAt`
- `ackInflight`, `ackOpID`, and `ackMatch`
- `pendingAck`, `pendingAckMatch`, and `nextAckAt`
- `nextPullAt`, retry backoff, last leader HW, and last error

Existing tests cover important Phase 2 cases such as local `LEO + 1` pull offsets, stale pull and ack completions, metadata fence reset, store apply backpressure, ack retry, leader-side async pull, leader pull cancellation, and stale ACK after leader epoch bump.

Phase 3 should turn those coverage points into a stricter contract and close edge cases that are not yet explicitly specified.

## Robustness Contract

### Follower State Invariants

For each follower channel runtime:

- At most one `TaskRPCPull` may be inflight.
- At most one pulled response may be retained as `pendingPull`.
- At most one `TaskStoreApply` may be inflight for the retained `pendingPull`.
- At most one `TaskRPCAck` may be inflight.
- `pendingAck` is retried before any new pull is submitted.
- `pendingAckMatch` must be stable across retry attempts until an ACK succeeds or metadata fences the state.
- `pendingPull` must not be overwritten by a newer pull response before the current response is applied or fenced.
- Metadata changes that alter generation, epoch, leader epoch, role, status, or leader reset follower-only replication state before scheduling work for the new metadata.

### Follower Pull Semantics

The follower submits pulls only when:

- local role is active follower,
- no pull is inflight,
- no pending pull is waiting for apply,
- no ack is inflight or pending,
- `nextPullAt` has elapsed,
- a transport client and worker pool are available.

The pull request uses:

```text
NextOffset = state.LEO + 1
Epoch = state.Epoch
LeaderEpoch = state.LeaderEpoch
Follower = local node id
```

A pull result is accepted only if the worker result fence matches the current channel generation, epoch, leader epoch, and pull operation id. The response payload must also match the current channel key, epoch, and leader epoch.

On accepted empty pull:

- follower HW may advance to `min(local LEO, leader HW)`,
- no ACK is sent,
- next pull is delayed by the idle poll interval.

On accepted non-empty pull:

- response becomes the single `pendingPull`,
- store apply is submitted when no ACK is pending or inflight,
- pull state is not overwritten until apply succeeds, fails, or metadata fences it.

### Follower Store Apply Semantics

Store apply results are accepted only if the fence matches current generation, epoch, leader epoch, and `applyOpID`.

On store apply success:

- local `LEO` becomes the worker-reported `LEO`,
- local `HW` becomes `min(local LEO, lastLeaderHW)`,
- `pendingPull` is cleared,
- an ACK is submitted for the applied match offset,
- replication remains dirty until the ACK succeeds.

On store apply error or store-apply worker backpressure:

- `pendingPull` remains intact,
- `applyOpID` is cleared if a task completed,
- retry is delayed by bounded backoff,
- no new pull is issued while this pending response exists.

### Follower ACK Semantics

ACK requests are accepted for submission only with a positive match offset. ACK completions are accepted only if the fence matches current generation, epoch, leader epoch, and `ackOpID`.

On ACK success:

- `ackInflight`, `ackOpID`, and `ackMatch` are cleared,
- `pendingAck` and `pendingAckMatch` are cleared,
- retry backoff is reset,
- if another pending pull exists, apply may continue,
- otherwise the next pull may be scheduled.

On ACK error or RPC worker backpressure:

- the exact match offset becomes or remains `pendingAckMatch`,
- retry is delayed by bounded backoff,
- no new pull is submitted until this ACK succeeds or metadata fences it.

### Leader Pull Semantics

Leader-side `HandlePull` remains an inbound RPC facade. The leader reactor validates current role, channel key, epoch, leader epoch, follower membership, and pull range, then submits a `TaskStoreReadLog` worker task.

Leader-side pull waiters must be released when:

- store read succeeds,
- store read returns an error,
- metadata fences the waiter,
- caller context cancels,
- reactor or group closes.

Late store-read completions after cancellation, close, or metadata fence are ignored and cannot complete a different waiter.

### Leader ACK Semantics

Leader ACK handling must reject or ignore stale metadata and regressive progress:

- ACK channel key, epoch, and leader epoch must match current state.
- ACK follower must be part of the current replica set or progress map.
- ACK match offset must not reduce follower progress.
- HW only advances through the existing quorum rules.
- A stale ACK from an older leader epoch must not complete quorum append waiters.

## Backpressure And Retry Contract

Backpressure is a first-class replication outcome:

- RPC pull pool full: no pull is recorded inflight; next pull is delayed by backoff.
- Store apply pool full: the existing pending pull remains; apply retry is delayed by backoff.
- RPC ACK pool full: the exact match offset remains pending; ACK retry is delayed by backoff.
- Store read-log pool full on leader pull: the pull future completes with `ErrBackpressured` unless the request is already accepted into a worker.
- Low-priority tick drops must not permanently stall replication. A later explicit tick must retry eligible work.

Backoff must remain deterministic in unit tests. Tests should use explicit `EventTick` timestamps rather than real sleeps except for short `Eventually` waits around worker goroutines.

## Error And Shutdown Semantics

- New requests after `Group.Close` return `ErrClosed`.
- Pending leader-side pull futures complete with `ErrClosed` during reactor close.
- Pending follower replication tasks do not have client futures, but their late worker completions are ignored after close.
- Worker contexts are canceled when worker pools close.
- Caller cancellation for leader-side `HandlePull` removes the waiter and completes with the caller context error.
- Metadata fence changes complete leader-side pull waiters with `ErrStaleMeta`.

## Observability

Phase 3 should avoid a broad metrics subsystem. It may add narrow observer hooks only where they directly help validate replication robustness:

- stale worker completion dropped,
- replication retry scheduled,
- pending pull retained after apply backpressure,
- pending ACK retained after ACK failure or backpressure.

If added, hooks must default to no-op and must not allocate heavily on the reactor hot path.

## Testing Strategy

### Unit Tests

Add or strengthen deterministic tests under `pkg/channelv2/reactor`:

- stale pull completion after metadata fence does not clear the newer pull inflight,
- stale store-apply completion does not clear a newer apply operation,
- stale ACK completion does not clear a newer ACK operation,
- ACK retry keeps the same match offset after RPC error and RPC pool backpressure,
- store apply backpressure keeps one pending pull and does not issue duplicate pulls,
- apply error retries the same pending pull,
- empty pull advances HW only up to local LEO and schedules idle retry,
- leader pull waiter returns `ErrClosed` on group close,
- leader pull waiter returns `ErrStaleMeta` on metadata fence,
- leader pull waiter returns context error on caller cancellation,
- stale ACK after leader epoch bump does not complete quorum append waiters,
- regressive ACK does not lower follower progress or HW,
- tick drop/coalescing does not prevent later retry.

### Testkit Tests

Add or strengthen `pkg/channelv2/testkit` scenarios with memory transport:

- three-node cluster catches up after temporary pull drops,
- three-node cluster catches up after temporary ACK drops,
- follower apply backpressure or transient store error recovers after ticks,
- leader epoch bump while follower has inflight pull does not commit with stale ACK.

### Verification Commands

Phase 3 implementation should use at least:

```bash
GOWORK=off go test ./pkg/channelv2/reactor -run 'TestFollower|TestLeader|Test.*Replication|Test.*Ack|Test.*Pull' -count=1
GOWORK=off go test ./pkg/channelv2/testkit -run 'TestThreeNode|Test.*Replication|Test.*Ack|Test.*Pull' -count=1
GOWORK=off go test ./pkg/channelv2/... -count=1
GOWORK=off go test -race ./pkg/channelv2/... -count=1
rg 'pkg/channel"|pkg/channel/store' pkg/channelv2 -g'*.go'
git diff --check
```

MacOS linker warnings during race tests are acceptable if tests pass.

## File Scope

Expected production files:

- `pkg/channelv2/reactor/replication_state.go`
- `pkg/channelv2/reactor/replication_runtime.go`
- `pkg/channelv2/reactor/reactor.go`
- `pkg/channelv2/reactor/effect.go`
- `pkg/channelv2/reactor/metrics.go` only if narrow observer hooks are needed
- `pkg/channelv2/FLOW.md`

Expected test files:

- `pkg/channelv2/reactor/replication_state_test.go`
- `pkg/channelv2/reactor/async_fetch_test.go` only if leader pull waiter close/cancel helpers live there today
- `pkg/channelv2/testkit/cluster_test.go`

Do not move replication scheduling back into `service.Tick`. Do not add imports from old `pkg/channel` outside `pkg/channelv2/store/channel_adapter.go`.

## Risks

- Tests that rely on wall-clock sleeps can become flaky. Prefer explicit tick timestamps and bounded fake blocking stores/transports.
- Over-instrumentation can add allocations to the reactor hot path. Keep observer additions narrow and optional.
- Treating duplicate or regressive records as successful apply could hide store contract bugs. If duplicate apply semantics are needed, design them explicitly instead of silently accepting them.
- Combining protocol changes with robustness fixes would make failures harder to isolate. Keep long-poll and ACK piggyback out of this phase.

## Open Questions

- Should duplicate follower apply responses be rejected by the store contract or tolerated as idempotent no-ops?
- Should replication retry counters be exposed through the generic observer now, or deferred until the productionization decision phase?
- Should leader-side pull range validation reject `NextOffset > LEO + 1`, or return an empty response using the current leader frontier?
- Should `ErrBackpressured` from leader read-log worker submission be returned to the remote follower, or converted to `ErrNotReady` at the transport boundary in a later API layer?
