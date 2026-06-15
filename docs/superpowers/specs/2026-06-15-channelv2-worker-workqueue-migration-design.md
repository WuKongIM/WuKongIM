# ChannelV2 Worker Workqueue Migration Design

## Context

`pkg/workqueue` now provides reusable bounded pool and sharded mailbox
primitives. The first migration target is `pkg/channelv2/worker`, because it
already isolates ChannelV2 blocking effects behind a typed worker package while
keeping business-specific batching, fences, and completion semantics outside the
generic executor.

The migration must be performance-led. We will not replace local worker
mechanics until the current implementation has benchmark baselines. After the
migration, the same benchmarks must show neutral or better behavior under the
approved gate:

- `ns/op` regression must be no more than 5%.
- `allocs/op` must not increase.
- `B/op` should not materially increase; any increase needs explicit analysis.

## Goals

- Move `pkg/channelv2/worker` from package-owned queue and ants executor
  mechanics to `pkg/workqueue.BoundedPool`.
- Preserve the worker package's domain semantics: `Task`, `Result`, `Fence`,
  task grouping, batching, completion sink behavior, panic-to-result mapping,
  and typed observer surface.
- Add worker benchmarks before implementation changes, and use them as the
  migration baseline.
- Refactor boldly where it simplifies ownership. Do not layer compatibility
  shims over the old queue, slot, dispatcher, and executor structures.

## Non-Goals

- Do not migrate gateway SEND in this phase.
- Do not migrate `internalv2/runtime/channelappend` in this phase.
- Do not keep the old worker queue/executor path behind a feature flag.
- Do not preserve package-internal APIs merely for compatibility with deleted
  mechanics. Update tests and package internals to match the cleaner design.

## Current Shape

`pkg/channelv2/worker.Pool` currently owns:

- bounded admission with `queue`, `slots`, `stop`, and admission locks;
- dispatcher goroutine that forms task groups and submits execution windows;
- ants-backed executor wrapper;
- shutdown completion for queued tasks;
- queue, admission, wait, task, batch, inflight, and ants usage observations.

The batching logic in `batch.go` is domain-specific and should remain in the
worker package. The generic workqueue should only own bounded admission,
execution, close waiting, queue depth, worker capacity, and generic low-cardinality
observations.

## Proposed Architecture

Replace the local pool mechanics with a single `workqueue.BoundedPool` whose
item is a worker-owned queued task or execution window.

The worker package keeps a small adapter around `BoundedPool`:

- `Pool.Submit` validates task input and maps generic errors to
  `channelv2.ErrBackpressured`, `channelv2.ErrClosed`, or caller context errors.
- A worker-local handler receives accepted work on the pool runtime context.
- The handler performs worker-specific grouping and batching, executes task
  groups, and completes the `CompletionSink`.
- A worker observer adapter translates `workqueue.BoundedPoolObservation` into
  the existing worker observer interfaces.

If the cleanest implementation requires changing package-internal helper types,
do it. Delete obsolete dispatcher, slot, executor, and shutdown code instead of
keeping wrappers that mimic the old layout. Public behavior should remain
covered by tests, but package internals do not need compatibility.

## Benchmark Plan

Add `pkg/channelv2/worker` benchmarks before migration:

- `BenchmarkWorkerPoolSubmitAndRun`: hot path submit, execute, complete.
- `BenchmarkWorkerPoolFullReject`: full admission rejection path.
- `BenchmarkWorkerPoolObserverOverhead`: observer disabled vs enabled.
- `BenchmarkWorkerPoolBatchStoreAppend` or equivalent stable batch benchmark
  if existing store test doubles can exercise batching without noisy setup.

Baseline command:

```sh
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

Post-migration command must be identical. Compare with `benchstat` when
available. If `benchstat` is unavailable, compare the raw repeated results and
report the limitation.

## Test Plan

Before migration:

```sh
go test ./pkg/channelv2/worker
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

After migration:

```sh
go test ./pkg/channelv2/worker ./pkg/channelv2/...
go test -race -count=1 ./pkg/channelv2/worker
go test -run '^$' -bench 'BenchmarkWorkerPool' -benchmem -benchtime=500ms -count=5 ./pkg/channelv2/worker
```

The migration is accepted only if behavior tests pass and the benchmark gate is
met. If the gate fails, stop and investigate before migrating any other package.

## Rollout

1. Add worker benchmarks and record baseline results in the implementation
   report.
2. Refactor `pkg/channelv2/worker` to use `workqueue.BoundedPool`.
3. Delete obsolete local queue/executor mechanics rather than wrapping them.
4. Run behavior, race, and benchmark verification.
5. Update `pkg/channelv2/worker/FLOW.md` if the implementation flow changes.
6. Decide on gateway SEND only after this migration is neutral or better.

