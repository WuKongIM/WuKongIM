# MultiRaft Runtime Optimization Design

Date: 2026-03-26
Status: Approved in chat, pending written-spec review

## Context

The current `multiraft` runtime is functionally hardened:

- proposal/config futures are correlated by committed `(index, term)` rather than FIFO
- `StateMachine.Apply()` / `Restore()` and `Storage.MarkApplied()` failures are fatal
- future success is delayed until the full ready pipeline completes
- scheduler and close-path behavior have already been hardened

Recent benchmarks show that the earlier end-to-end benchmark harness had meaningful polling overhead, but after moving to an event-driven applied notification path the remaining cost is now mostly in the runtime hot path itself.

Measured on 2026-03-26:

- `BenchmarkThreeNodeMultiGroupProposalRoundTrip/groups=8`: `12.26 ms/op`, `107622 B/op`, `1075 allocs/op`
- `BenchmarkThreeNodeMultiGroupProposalRoundTrip/groups=32`: `11.71 ms/op`, `344375 B/op`, `3258 allocs/op`
- `BenchmarkThreeNodeMultiGroupProposalRoundTripNotified/groups=8`: `3.69 ms/op`, `48899 B/op`, `504 allocs/op`
- `BenchmarkThreeNodeMultiGroupProposalRoundTripNotified/groups=32`: `3.28 ms/op`, `107068 B/op`, `1035 allocs/op`
- `BenchmarkThreeNodeMultiGroupConcurrentProposalThroughput/groups=8`: `0.57 ms/op`, `21023 B/op`, `230 allocs/op`
- `BenchmarkThreeNodeMultiGroupConcurrentProposalThroughput/groups=32`: `0.43 ms/op`, `21011 B/op`, `229 allocs/op`

These numbers suggest the next meaningful optimization step is internal runtime allocation and copy reduction, not further benchmark harness simplification.

## Goals

- Reduce steady-state allocations on the runtime hot path.
- Reduce avoidable slice copying in worker processing.
- Preserve all current public API and correctness semantics.
- Keep the optimization set small, local, and reviewable.

## Non-Goals

- No public API changes.
- No weakening of fatal semantics.
- No rollback to FIFO proposal/config resolution.
- No global architecture rewrite such as cross-group batching or shared ready executors.
- No pooling of public `Future` objects whose lifetime is owned by callers.

## Chosen Approach

The chosen approach is a low-risk internal optimization pass focused on queue/buffer reuse and capacity planning.

Rejected for this round:

- aggressive object pooling across public boundaries
- removing message deep-copy semantics for transport
- broad scheduler or execution model rewrites

Those ideas may have future value, but they carry higher correctness risk and weaker short-term signal than the selected changes.

## Design Constraints

The following semantics must remain unchanged:

- proposal/config correlation remains based on committed `(index, term)`
- `Apply()`, `Restore()`, and `MarkApplied()` errors remain fatal
- a proposal/config future succeeds only after:
  - ready persistence
  - transport send
  - apply/restore
  - `MarkApplied()`
  - `Advance()`
  - status refresh
- non-leader admission still rejects `Propose()` / `ChangeConfig()` with `ErrNotLeader`
- transport still receives data it can safely hold after the ready batch returns

## Detailed Changes

### 1. Request and control queue processing

Current state:

- `processRequests()` copies `g.requests` into a fresh slice before processing
- `processControls()` does the same for `g.controls`

Problem:

- every worker pass pays an avoidable allocation and copy cost, even in stable steady-state traffic

Change:

- add reusable scratch buffers on `group`, such as request and control work buffers
- under lock, swap active queues with their work buffers instead of allocating fresh slices
- after processing, retain capacity and reset length to zero for reuse

Expected effect:

- fewer per-iteration allocations
- less queue-copy overhead under sustained traffic

### 2. Ready resolution buffer reuse

Current state:

- `processReady()` builds a local `[]futureResolution` during committed-entry processing

Problem:

- each ready batch can allocate while collecting future completions

Change:

- store a reusable `resolutionBuf` on `group`
- start each batch with `buf := g.resolutionBuf[:0]`
- after completion, keep the capacity for reuse in later ready batches

Expected effect:

- lower allocation rate on the ready path
- no semantic change to when or how futures are resolved

### 3. Pending map capacity planning

Current state:

- `pendingProposals` and `pendingConfigs` are created lazily
- growth happens reactively as ready entries are tracked

Problem:

- repeated growth and rehashing under bursts or many active groups

Change:

- estimate the number of normal entries and conf changes in the current ready batch
- pre-grow pending maps before tracking entries
- continue matching futures by `(index, term)` exactly as today

Expected effect:

- fewer map growth events
- smoother allocation profile under bursty multi-group proposal traffic

### 4. Transport envelope slice reuse

Current state:

- `wrapMessages()` allocates a fresh `[]Envelope` every batch
- message bodies are deep-cloned to decouple transport lifetime from ready lifetime

Change:

- keep message deep-copy semantics
- only optimize the outer `[]Envelope` allocation by reusing a temporary buffer

Why keep deep cloning:

- transports may be asynchronous
- ready-owned message memory must not leak across `Advance()`

Expected effect:

- moderate allocation reduction without crossing a risky ownership boundary

## Implementation Order

1. queue swap buffers for requests and controls
2. resolution buffer reuse
3. pending map pre-growth
4. envelope slice reuse while preserving deep-cloned message contents

This order prioritizes low-risk wins first and keeps regression scope narrow at each step.

## Validation Plan

Each step should follow TDD and be validated separately.

### Correctness regression checks

Run the existing `multiraft` test suite with emphasis on:

- proposal/config correlation behavior
- fatal group behavior
- non-leader admission behavior
- scheduler and close-path safety
- realistic 3-node end-to-end replication tests

Required commands:

- `go test ./multiraft/...`
- `go test -race ./multiraft/...`

### New targeted tests

Add focused tests that prove:

- queue buffer swapping does not drop, duplicate, or reorder work
- reused resolution buffers do not leak prior batch state
- pending-map pre-growth does not change correlation behavior
- reusable transport envelope buffers do not expose mutable data after ready processing

### Benchmark checks

Track all three realistic benchmark views:

- `BenchmarkThreeNodeMultiGroupProposalRoundTrip`
- `BenchmarkThreeNodeMultiGroupProposalRoundTripNotified`
- `BenchmarkThreeNodeMultiGroupConcurrentProposalThroughput`

Success criteria:

- `allocs/op` decreases measurably
- `B/op` decreases, though likely less dramatically than `allocs/op`
- `ns/op` improves without any correctness regression

## Risks

### Buffer reuse bugs

Reused slices can accidentally retain stale data or be observed concurrently if ownership boundaries are unclear.

Mitigation:

- keep buffers group-local
- reuse only within worker-owned critical paths
- add explicit tests for repeated multi-batch processing

### Hidden ownership assumptions

Reducing copies too aggressively near the transport boundary could break async transports.

Mitigation:

- preserve deep cloning of message contents in this round
- only remove outer-slice allocation first

### Benchmark-driven overfitting

A synthetic benchmark can reward unsafe shortcuts.

Mitigation:

- use realistic 3-node, multi-group benchmarks as the primary signal
- require the full correctness suite to pass before accepting any optimization

## Expected Outcome

This optimization pass should reduce runtime hot-path allocation pressure while preserving the recently hardened correctness model. The expected first-order improvement is lower `allocs/op`, followed by moderate `B/op` and `ns/op` gains in the realistic multi-node, multi-group benchmarks.
