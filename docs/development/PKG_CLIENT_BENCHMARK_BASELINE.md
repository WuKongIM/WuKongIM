# pkg/client Benchmark Baseline

This document records the `pkg/client` microbenchmark baseline for the WKProto client batch-send path. Use it before and after changes that touch `pkg/client`, `internal/bench/wkproto`, or e2e WKProto helpers.

## Baseline Commit

- Commit: `2e32506c perf: add send batch pending fast path`
- Date: 2026-06-14
- Host for recorded numbers: Apple M4, darwin/arm64
- Go: `go1.25.0`

## Regression Guards

These tests are intentionally small enough for normal package testing and should remain hard gates:

```bash
GOWORK=off go test -count=1 -run 'Test(PrepareSendAllocationBudget|WriteBatchEncodeAllocationBudget|PendingTrackerTimeoutAllocationBudget|SendFutureWaitAllocationBudget|SendFutureWaitOnceAllocationBudget|ClientSendBatchRoundTripAllocationBudget)$' ./pkg/client
```

Budgets currently enforced:

| Test | Budget |
| --- | ---: |
| `TestPrepareSendAllocationBudget` | `<= 2` allocs |
| `TestWriteBatchEncodeAllocationBudget` | `<= 500` allocs |
| `TestPendingTrackerTimeoutAllocationBudget` | `<= 5` allocs |
| `TestSendFutureWaitAllocationBudget` | `<= 3` allocs |
| `TestSendFutureWaitOnceAllocationBudget` | `<= 2` allocs |
| `TestClientSendBatchRoundTripAllocationBudget` | `<= 1800` allocs |

Do not raise these budgets without a measured reason and an updated baseline note.

## Benchmark Command

Use the helper script for a stable local baseline run:

```bash
BENCHTIME=2s COUNT=3 scripts/bench-pkg-client-baseline.sh
```

The script writes:

- `metadata.txt`
- `allocation-guards.txt`
- `bench.txt`

under `tmp/pkg-client-baseline/<timestamp>/`.

Equivalent raw benchmark command:

```bash
GOWORK=off go test -run '^$' -bench 'Benchmark(PrepareSend|PendingTrackerResolve|PendingTrackerResolveWithTimeout|SendFutureWaitReady|SendFutureWaitOnceReady|WriteBatchEncode|ClientSendBatchRoundTrip)$' -benchmem -benchtime=2s -count=3 ./pkg/client
```

## Recorded Stable Numbers

Representative results from the baseline commit:

| Benchmark | ns/op | B/op | allocs/op |
| --- | ---: | ---: | ---: |
| `BenchmarkPrepareSend` | `~68.7-69.7` | `416` | `2` |
| `BenchmarkPendingTrackerResolve` | `~140.0-140.4` | `288` | `3` |
| `BenchmarkPendingTrackerResolveWithTimeout` | `~233.7-240.0` | `424` | `5` |
| `BenchmarkSendFutureWaitOnceReady` | `~68.9-82.5` | `176` | `2` |
| `BenchmarkSendFutureWaitReady` | `~78.4-81.3` | `288` | `3` |
| `BenchmarkWriteBatchEncode/batch_1` | `~721-763` | `216` | `9` |
| `BenchmarkWriteBatchEncode/batch_64` | `~6.9-7.4 us` | `~1.2 KB` | `324` |
| `BenchmarkWriteBatchEncode/batch_512` | `~48.3-48.9 us` | `~8.5 KB` | `2564` |
| `BenchmarkClientSendBatchRoundTrip/batch_1` | `~6.1-6.5 us` | `2681` | `41` |
| `BenchmarkClientSendBatchRoundTrip/batch_64` | `~281-288 us` | `~158 KB` | `1748` |

`ns/op` is machine-load sensitive. Treat sustained `B/op` or `allocs/op` growth as a stronger regression signal than a single slower timing run.

## Interpreting Regressions

- Any allocation guard failure is a regression unless the change intentionally trades allocations for a measured higher-level win.
- For benchmark output, investigate when `allocs/op` increases by more than roughly 5% on `ClientSendBatchRoundTrip/batch_64` or `WriteBatchEncode/batch_64`.
- Investigate timing only after rerunning with `BENCHTIME=2s COUNT=3` on a quiet machine. A single slow sample is not enough evidence.
- If a performance change is intentional, update this document in the same commit as the implementation.
