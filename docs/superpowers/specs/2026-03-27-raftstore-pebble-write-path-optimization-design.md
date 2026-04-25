# Raftstore Pebble Write Path Optimization Design

Date: 2026-03-27
Status: Approved in chat, pending written-spec review

## Context

`raftstore` already has correctness tests, benchmarks, and opt-in stress coverage for the Pebble-backed backend. Current baseline on 2026-03-27 shows that the main cost under pressure is not small-window reads, but durable write and reopen paths:

- `BenchmarkPebbleSaveEntries/single-group-append-small`: about `8.15 ms/op`
- `BenchmarkPebbleSaveEntries/multi-group-interleaved`: about `7.85 ms/op`
- `BenchmarkPebbleMarkApplied/single-group`: about `4.06 ms/op`
- `BenchmarkPebbleInitialStateAndReopen/single-group`: about `29.96 ms/op`

The current implementation pays this cost because:

- every `Save()` creates its own batch and commits with `pebble.Sync`
- every `MarkApplied()` does the same
- `InitialState()` reconstructs `ConfState` by scanning and decoding all persisted log entries for a group
- `FirstIndex()` and `LastIndex()` walk the entry keyspace instead of reading persisted index metadata

The user wants the optimization to be enabled by default and does not want to weaken the practical durability contract. Specifically, a call that returns success must still have its changes durably persisted, and normal clean close/reopen semantics must stay correct.

## Goals

- Improve `raftstore` throughput under concurrent write pressure.
- Reduce reopen and recovery overhead for `InitialState()`, `FirstIndex()`, and `LastIndex()`.
- Keep the existing public `multiraft.Storage` API unchanged.
- Preserve durable-return semantics: `Save()` and `MarkApplied()` may return only after their changes are durably committed.
- Keep all group data slot-prefixed and preserve business/raft keyspace separation.

## Non-Goals

- No changes to `multiraft`, `wkdb`, or `wkfsm` public contracts.
- No opt-in feature flag or external configuration surface for the optimization pass.
- No relaxation where successful `Save()` or `MarkApplied()` may be lost after an abnormal crash.
- No broad storage engine abstraction rewrite.
- No benchmark-threshold assertions tied to one machine.

## Chosen Approach

Use two coordinated internal changes:

1. add a default-on write aggregator inside `raftstore.DB` that batches concurrent `Save()` and `MarkApplied()` requests into fewer Pebble sync commits while preserving synchronous durable completion for each caller
2. persist per-group raft metadata so reopen and state reconstruction no longer require a full log scan

This combines the highest expected throughput gain with a large reduction in reopen latency, while keeping the caller-visible contract intact.

Rejected for this round:

- async write acknowledgement before durable commit
- configurable sync modes
- optimization limited to micro-allocation cleanup without changing write orchestration

Those either weaken semantics too much or are too small to matter against the measured bottlenecks.

## Design Constraints

The following must remain true after the change:

- `Save()` and `MarkApplied()` return success only after the relevant state is durably committed
- same-group request order is preserved
- reopen after clean close sees all previously acknowledged writes
- abnormal termination may interrupt in-flight operations, but must not lose a write that already returned success
- snapshot recovery semantics remain unchanged
- `multiraft.GroupID` continues to map directly to slot-prefixed raft keys

## Detailed Changes

### 1. Add a DB-level durable write aggregator

Current state:

- `pebbleStore.Save()` and `pebbleStore.MarkApplied()` each open their own Pebble batch
- each operation issues its own `Commit(pebble.Sync)`
- concurrent writers amplify sync cost even when writes could be durably committed together

Change:

- extend `DB` with an internal request queue and one background write worker
- `Save()` and `MarkApplied()` submit typed write requests to that worker instead of committing directly
- the worker collects a bounded batch of queued requests, folds them into a single Pebble batch, and commits once with `pebble.Sync`
- every request carries a completion channel; callers block until the sync commit that contains their request succeeds or fails

Required behavior:

- no caller gets success before the durable sync completes
- all requests included in the same Pebble commit receive the same success/failure result
- new submissions are rejected once shutdown begins
- `Close()` drains queued work, stops the worker, and only then closes Pebble

Why this is acceptable:

- it reduces sync frequency under concurrency
- it does not weaken the "success means durable" contract
- it preserves current API shape and most current call sites remain unchanged

### 2. Preserve same-group ordering while allowing cross-group coalescing

Current risk:

- batching across concurrent calls can accidentally reorder writes for the same group or split logically-related metadata updates

Change:

- the worker processes requests in queue order
- when multiple consecutive requests target the same group within one flush window, it folds them into one in-memory group mutation before writing to Pebble
- different groups may share the same Pebble batch freely because their keyspaces are isolated

Examples:

- `Save(group=1)` followed by `MarkApplied(group=1)` in the same flush becomes one group mutation and one durable commit
- `Save(group=1)` and `Save(group=2)` can land in the same Pebble sync without affecting ordering guarantees

Non-permitted behavior:

- reordering same-group requests
- returning success to a later request while an earlier queued same-group request failed

### 3. Persist per-group raft metadata

Current state:

- `InitialState()` reads snapshot, hard state, applied index, then scans all entries to derive committed `ConfState`
- `FirstIndex()` and `LastIndex()` iterate over entry keys even though index state is logically metadata

Change:

- add a new raftstore metadata record per group containing at least:
  - `firstIndex`
  - `lastIndex`
  - `appliedIndex`
  - `snapshotIndex`
  - `snapshotTerm`
  - `confState`
- update this metadata in the same Pebble write batch as the associated `Save()` or `MarkApplied()`

Expected result:

- `InitialState()` can read hard state plus metadata directly
- `FirstIndex()` and `LastIndex()` can return metadata without scanning the entry keyspace
- snapshot reopen no longer depends on decoding the whole surviving log to recover `ConfState`

### 4. Update metadata atomically with each mutation type

Metadata update rules:

- append or tail-replace:
  - update `lastIndex`
  - keep `firstIndex` unless entries start earlier than the currently tracked first durable entry
- snapshot save:
  - persist snapshot bytes
  - delete superseded entry range
  - update `snapshotIndex`, `snapshotTerm`, `confState`, and `firstIndex`
  - ensure `lastIndex` is at least the snapshot index when no post-snapshot entries remain
- `MarkApplied(index)`:
  - update `appliedIndex`
- hard-state-only save:
  - persist hard state without changing index metadata beyond any implied commit/state rules already carried by the call

Important invariant:

- metadata must describe exactly the state visible after the same durable Pebble commit completes

### 5. Add backward-compatible reopen fallback

Current concern:

- existing on-disk data created before this change has no metadata record

Change:

- on reopen, try loading the new metadata record first
- if it is missing, fall back to the current reconstruction path:
  - load snapshot
  - load hard state
  - load applied index
  - scan entries to derive indexes and `ConfState`
- once reconstructed, persist the metadata record so later opens are fast

This keeps old test fixtures and old local stores readable without a manual migration step.

### 6. Keep read-path semantics unchanged outside recovery shortcuts

`Entries()` and `Term()` still read the log/snapshot contents as they do today. This design does not change the externally visible behavior of:

- log range reads
- term lookups
- snapshot payload retrieval

The optimization focus is on:

- write-path sync amortization
- metadata-based reopen and index lookup

## File Structure

Planned production changes:

- `raftstore/pebble.go`
  - add the DB-level write worker, queueing, shutdown coordination, and metadata-based state loading
- `raftstore/pebble_codec.go`
  - add key encoding for the new per-group metadata record

Planned test changes:

- `raftstore/pebble_test.go`
  - extend correctness coverage for ordering, reopen, and backward-compatibility cases if existing helpers fit
- `raftstore/pebble_benchmark_test.go`
  - keep benchmark names and compare pre/post optimization behavior
- `raftstore/pebble_stress_test.go`
  - add pressure coverage aimed at acknowledged-write visibility across close/reopen
- `raftstore/pebble_test_helpers_test.go`
  - add helpers as needed for legacy-format setup and metadata assertions

## Validation Plan

Implementation must follow TDD:

- write a failing targeted test first
- run it and confirm the failure is for the intended missing behavior
- implement the minimum change to pass
- rerun the test and then broader verification

### Required correctness checks

Add focused tests for:

- concurrent `Save()` requests that all return only after durable visibility across reopen
- `Save()` followed by `MarkApplied()` on the same group being visible together after reopen
- `Close()` with queued in-flight writes draining safely without losing acknowledged work
- metadata correctness after append, tail replace, and snapshot save
- legacy stores without the new metadata reopening correctly and backfilling metadata

### Required benchmark checks

Run and compare:

- `go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1`
- `WRAFT_RAFTSTORE_BENCH_SCALE=heavy go test ./raftstore -run '^$' -bench 'BenchmarkPebble(SaveEntries|Entries|MarkApplied|InitialStateAndReopen)$' -count=1`

Success criteria:

- `BenchmarkPebbleSaveEntries` improves meaningfully under concurrent and interleaved cases
- `BenchmarkPebbleMarkApplied` improves under contention
- `BenchmarkPebbleInitialStateAndReopen` improves substantially due to metadata reads
- no correctness regressions in benchmark post-checks

### Required stress checks

Run at least:

- `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=3s go test ./raftstore -run 'TestPebbleStress(ConcurrentWriters|MixedReadWriteReopen|SnapshotAndRecovery)$' -count=1 -v`

Add and run a new stress-oriented check for:

- concurrent writers with periodic clean close/reopen
- validation that all acknowledged writes remain visible after reopen

Longer follow-up validation:

- `WRAFT_RAFTSTORE_STRESS=1 WRAFT_RAFTSTORE_STRESS_DURATION=5m go test ./raftstore -run '^TestPebbleStress' -count=1 -v`

## Risks

### Write worker ordering bugs

If queue draining or flush coalescing reorders same-group operations, reopen state may diverge from call order.

Mitigation:

- single worker ownership of request ordering
- explicit same-group ordering tests
- avoid parallel mutation application inside one flush

### Shutdown races

If `Close()` stops the worker too early, callers may observe hangs or lost acknowledged writes.

Mitigation:

- explicit DB shutdown state
- queue rejection for new requests after shutdown begins
- drain/flush before Pebble close
- tests that race close against inflight writes

### Metadata drift

If persisted metadata and entry keyspace diverge, reopen shortcuts could return wrong indexes or `ConfState`.

Mitigation:

- update metadata only in the same Pebble batch as the underlying mutation
- retain fallback reconstruction for missing metadata
- add tests for append, truncate, snapshot, and reopen sequences

### Backward-compatibility gaps

Older stores may fail if fallback reconstruction misses a corner case.

Mitigation:

- generate legacy-format stores in tests using the pre-metadata behavior
- verify first reopen succeeds and second reopen uses persisted metadata

## Open Questions Resolved In Chat

- Optimization should be enabled by default: yes
- We may optimize aggressively, but not by acknowledging writes before durable commit: yes
- External config flag for sync mode: no
- Primary scope: internal `raftstore` production changes plus supporting tests and benchmarks
