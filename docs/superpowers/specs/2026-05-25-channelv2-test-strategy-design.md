# Channelv2 Test Strategy Design

**Date:** 2026-05-25
**Status:** Approved

## Overview

`pkg/channelv2` is an experimental multi-reactor channel log runtime. It
currently validates append, fetch, follower apply, ACK, HW commit, PullHint
wake-up, recent leader record cache, and idle runtime eviction. The package is
already well covered by focused unit tests, but the coverage is uneven across
public facade, transport, lifecycle edge cases, store compatibility, and
explicit safety invariants.

This design defines a broader test strategy for `pkg/channelv2` without
changing runtime semantics. The intent is to make future changes safer by
pinning down correctness contracts at the right layer: pure machine tests for
state transitions, reactor tests for concurrency and fencing, service tests for
public API semantics, store contract tests for persistence compatibility, and
testkit scenarios for small in-memory clusters.

## Current Baseline

The package structure is:

```text
pkg/channelv2/
  machine/       pure per-channel append, fetch, meta, progress transitions
  reactor/       reactor ownership, mailbox, append batching, replication, lifecycle
  replication/   small pull/ack planning DTO helpers
  service/       synchronous facade and lazy metadata activation
  store/         memory store and old pkg/channel store adapter
  testkit/       in-memory multi-node harness
  transport/     local transport and replication DTOs
  worker/        typed bounded worker pools and tasks
```

Fresh verification on 2026-05-25:

```text
go test ./pkg/channelv2/...
```

passed with exit code 0.

Coverage snapshot:

```text
pkg/channelv2                  0.0%
pkg/channelv2/machine         77.1%
pkg/channelv2/reactor         80.4%
pkg/channelv2/service         73.3%
pkg/channelv2/store           77.8%
pkg/channelv2/testkit         93.5%
pkg/channelv2/transport       46.3%
pkg/channelv2/worker          74.0%
```

Notable uncovered or weakly covered areas include:

- `machine.CheckInvariants`
- store append error handling in the pure append state machine
- `machine.ApplyReadCommitted` error/default/trim behavior
- `service.HandleAck`
- direct `transport.LocalNetwork` Pull/Ack/Notify paths and counters
- selected lifecycle retry and close paths in `reactor`
- public `ExpectedChannelEpoch` and `ExpectedLeaderEpoch` semantics

## Goals

- Make cheap channel log invariants explicit in tests: `CheckpointHW <= HW <= LEO`.
- Prove append, fetch, pull, ack, checkpoint, and metadata fence results cannot
  mutate stale channel generations.
- Keep single-node deployment described and tested as a single-node cluster.
- Strengthen service facade tests around lazy metadata, context cancellation,
  close behavior, and replication RPC compatibility.
- Expand store contract coverage so memory store and old store adapter remain
  behaviorally aligned.
- Add enough multi-node testkit coverage to catch quorum, leader change,
  ISR-change, PullHint, and idle eviction regressions.
- Keep default unit tests fast and deterministic.

## Non-Goals

- No migration, retention, snapshot install, or leader repair tests in this
  phase. `pkg/channelv2/doc.go` explicitly excludes these v0 semantics.
- No Docker, real network, or full system e2e tests in the default channelv2
  unit-test suite.
- No performance tuning or benchmark pass/fail thresholds as part of this
  correctness strategy.
- No broad refactor of `pkg/channelv2` test helpers unless a specific test
  needs it.
- No new business branch that bypasses cluster semantics for single-node mode.

## Test Architecture

### Machine Tests

Machine tests should be deterministic and avoid goroutines. They should treat
`machine.ChannelState` as a pure state transition aggregate and verify both
decisions and post-state.

Primary contracts:

- metadata validation does not partially mutate state on rejection,
- append proposals produce exactly one store task per accepted durable batch,
- store append success assigns continuous offsets and advances `LEO`,
- quorum commit waits for enough ISR progress,
- local commit completes after local durable append,
- store append error fails all still-observed waiters and clears inflight state,
- fetch reads only up to the captured HW,
- stale read/append results are ignored,
- `CheckInvariants` rejects invalid ordering.

### Reactor Tests

Reactor tests should cover single-writer concurrency, bounded queues, stale
worker completions, lifecycle actions, and close/cancel paths. Prefer explicit
reactor events and controlled fake stores/transports over sleeps. Short
`Eventually` waits are acceptable only around real worker goroutines.

Primary contracts:

- high-priority worker results and cancellation events are not starved by
  normal append pressure,
- append queue backpressure either rejects before admission or retains enough
  state for retry,
- canceled append and pull waiters are removed and cannot be completed later,
- metadata fence fails pending waiters before new state is applied,
- stale `OpID`, generation, epoch, or leader epoch completions are ignored,
- leader recent-record cache never creates gaps and respects byte budgets,
- idle lifecycle only evicts runtimes after final recheck guards pass,
- group close completes or releases all observable futures.

### Service Tests

Service tests should validate the public `channelv2.Cluster` facade and the
`transport.Server` compatibility surface. They should not inspect reactor
internals unless that is the only practical way to assert cleanup.

Primary contracts:

- `Append` delegates to `AppendBatch` and returns the first item result,
- lazy append meta activation defaults empty key/ID when safe,
- lazy PullHint activation rejects stale metadata, local-leader activation,
  inactive status, or local node not in replicas,
- `HandleNotify` stays compatible with legacy no-op behavior for invalid
  hints,
- `HandlePull` and `HandleAck` route through allocated operation IDs and update
  leader-visible progress,
- context cancellation returns the context error, not an internal backpressure
  error,
- `Close` is idempotent and later public operations return `ErrClosed`.

### Store Contract Tests

Store tests should run a shared contract suite against both memory store and
old store adapter.

Primary contracts:

- `AppendLeader` assigns continuous offsets and preserves payload bytes,
- `ApplyFollower` is idempotent for duplicate prefixes,
- conflicting duplicate records return `ErrStaleMeta`,
- gaps and zero indexes return typed errors,
- `ReadCommitted` respects `FromSeq`, `MaxSeq`, `Limit`, and `MaxBytes`,
- `ReadLog` respects `FromOffset`, `MaxOffset`, and `MaxBytes`,
- returned payloads are cloned and cannot be mutated through caller slices,
- `StoreCheckpoint` is monotonic and preserves old adapter fields.

### Transport Tests

Transport tests should directly exercise `LocalNetwork` as a deterministic test
double.

Primary contracts:

- `Client()` returns a usable client,
- Pull/Ack/PullHint/Notify route to registered servers,
- missing target servers return `ErrChannelNotFound`,
- drop flags return `ErrNotReady` and increment the right counters,
- legacy `DropNotify` and current `DropPullHint` both affect `Notify`,
- setters can toggle drops safely during concurrent tests.

### Testkit Scenarios

Testkit scenarios should use in-memory stores and local transport to validate
small cluster behavior that is hard to prove with isolated unit tests.

Primary scenarios:

- single-node cluster append/fetch/reload after idle checkpoint,
- three-node quorum commit with `MinISR=2`,
- one follower pull drop or ack drop recovers and still commits,
- two followers unavailable blocks quorum append until one recovers,
- idle follower stop, unload, PullHint reactivation, and catch-up,
- leader epoch change fences old leader writes and stale pull/ack completions,
- ISR shrink/expand changes commit eligibility without regressing HW.

## Priority Plan

### P0: High-Risk Gaps

Add these first because they are small and guard core correctness:

- `machine.CheckInvariants` valid and invalid ordering tests.
- `machine.ApplyReadCommitted` store error, stale fence, message trim, and
  default `NextSeq` tests.
- `machine.ApplyAppendStored` store error test that clears inflight state and
  preserves offsets.
- `service.HandleAck` test that a follower ACK advances leader HW and completes
  a quorum append.
- Direct `transport.LocalNetwork` Pull/Ack/Notify missing-server, drop, and
  counter tests.
- Decide and test `ExpectedChannelEpoch` / `ExpectedLeaderEpoch`. If they are
  intended request fences, stale values should return `ErrStaleMeta`.

### P1: Reactor Edges

- `EventCheckState`, `Group.HasChannelState`, and `ReserveAppend` after close.
- `Group.Tick` with canceled context and low-priority mailbox pressure.
- `EventClose` and pending future completion.
- leader checkpoint error retry and stale checkpoint completion.
- final leader eviction blocked by append reservation or changed submit seq.
- follower stopped ACK transient error retry and stale meta recovery.

### P2: Cluster Scenarios

- Single-node cluster reload after idle eviction.
- Three-node quorum with one unavailable follower.
- Three-node quorum blocked with two unavailable followers.
- Leader switch fences old leader append/pull/ack paths.
- ISR membership changes and non-ISR follower ACK behavior.
- PullHint reactivates unloaded followers after idle stop.

### P3: Store And Compatibility

- Expand `testStoreContract` into a fuller suite shared by memory and old store.
- Add payload clone/mutation tests.
- Add old adapter decode fallback and `Close` idempotency tests.
- Cover context cancellation for all store methods.

### P4: Property And Fuzz Tests

- Deterministic property test for random machine transitions, checking
  `CheckInvariants` after every accepted step.
- Store contract property test comparing memory and old adapter observable
  results for generated record sequences.
- Small fuzz test for `ChannelKeyForID` stability and collision avoidance across
  channel type and ID combinations.

Large seed counts or longer randomized runs should be guarded by
`-tags=integration` or a nightly job.

## Determinism Rules

- Default tests should run under `go test ./pkg/channelv2/...` without long
  sleeps.
- Prefer explicit `Tick` events with fixed timestamps over wall-clock waiting.
- Use `context.WithTimeout` around goroutine waits to prevent deadlocks.
- Keep any simulated real-time test below one second unless it is moved behind
  the integration tag.
- Avoid relying on goroutine scheduling order unless the test owns the
  synchronization point.
- When testing backpressure, assert both the immediate error/retained state and
  the later retry path.

## Verification Commands

Daily development:

```sh
go test ./pkg/channelv2/...
```

Coverage review:

```sh
go test -coverprofile=/tmp/channelv2.cover ./pkg/channelv2/...
go tool cover -func=/tmp/channelv2.cover
```

Flake sweep for timing-sensitive slices:

```sh
go test ./pkg/channelv2/reactor ./pkg/channelv2/testkit -count=20
```

Race/nightly:

```sh
go test -race ./pkg/channelv2/...
```

Slow scenarios:

```sh
go test -tags=integration ./pkg/channelv2/...
```

## Acceptance Criteria

- P0 tests are implemented and pass with `go test ./pkg/channelv2/...`.
- Any chosen semantics for `ExpectedChannelEpoch` and `ExpectedLeaderEpoch` are
  documented by tests.
- New default tests are deterministic and do not materially slow the package
  suite.
- Store behavior differences between memory and old adapter are either removed
  or explicitly documented as accepted compatibility differences.
- Multi-node tests assert externally visible behavior through `Cluster`,
  `transport.Server`, and `testkit` helpers whenever practical.

## Open Questions

- Should `ExpectedChannelEpoch` and `ExpectedLeaderEpoch` be enforced for
  append/fetch in v0, or are they reserved fields?
- Should randomized property tests live in default unit tests with low seed
  counts, or only behind integration/nightly runs?
- Should lifecycle-heavy testkit scenarios remain in `pkg/channelv2/testkit`,
  or should slow variants move under `test/e2e` once channelv2 is wired into
  the application composition root?
