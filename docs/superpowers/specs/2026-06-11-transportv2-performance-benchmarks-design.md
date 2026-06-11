# TransportV2 Performance Benchmarks Design

Date: 2026-06-11

## Context

`pkg/transportv2` already has benchmarks for top-level RPC, send backpressure,
wire frame encoding, slab buffers, and the outbound scheduler. After moving
server-side service execution to a shared ants-backed executor, the current
benchmark set is missing focused coverage for the service execution layer
itself and for multi-service executor sharing.

## Goal

Add repeatable Go benchmarks that separate transportv2 performance into three
layers:

- RPC service execution without network I/O.
- End-to-end transport RPC across different connection-pool and service
  layouts.
- Backpressure paths where caller-side loops retry explicit `ErrBusy` or
  `ErrQueueFull` results.

The benchmarks should be easy to run with:

```sh
GOWORK=off go test -run '^$' -bench 'TransportV2|Service|Executor' -benchmem ./pkg/transportv2/...
```

## Non-Goals

- Do not change production transport behavior.
- Do not add public tuning config.
- Do not add a separate load-test framework.
- Do not make unit tests slower; benchmarks only run when requested with
  `-bench`.
- Do not update deployment terminology or introduce non-cluster semantics.

## Benchmark Coverage

### Internal RPC Layer

Add `pkg/transportv2/internal/rpc/benchmark_test.go` with:

- `BenchmarkExecutorSubmit`: measures ants executor submission and completion
  under direct task pressure.
- `BenchmarkServiceRPCReplyPayloadSizes`: measures service queue, pump,
  executor task, handler, and reply-channel cost for 64B, 1KiB, and 64KiB
  payloads.
- `BenchmarkServiceSendOnlyParallel`: measures async send-only service
  admission and executor completion under parallel callers.
- `BenchmarkServiceSharedExecutorMultipleServices`: measures multiple services
  sharing one executor while preserving per-service concurrency.

### Top-Level Transport Layer

Extend `pkg/transportv2/benchmark_test.go` with:

- Connection pool size cases for RPC calls.
- Parallel multi-service RPC over one server-owned shared executor.
- Parallel send-only calls with caller-side `ErrQueueFull` retry accounting.

These benchmarks keep using local TCP listeners because the goal is to measure
the real client, peer, connection, scheduler, wire, server dispatch, and service
stack together.

## Backpressure Rules

Benchmarks that intentionally pressure bounded queues must retry only documented
transport errors:

- `transportv2.ErrQueueFull` on client-side send pressure.
- `core.ErrBusy` in internal service benchmarks.

Any other error fails the benchmark. This makes saturation visible while keeping
benchmark results successful and comparable.

## Testing

Use focused compile/execution checks for the new benchmark names:

```sh
GOWORK=off go test ./pkg/transportv2/internal/rpc -run '^$' -bench 'Executor|Service' -benchmem -benchtime=100ms
GOWORK=off go test ./pkg/transportv2 -run '^$' -bench 'TransportV2' -benchmem -benchtime=100ms
GOWORK=off go test ./pkg/transportv2/... -count=1
```

The short `-benchtime=100ms` runs are for development verification. Longer
comparison runs can use the same benchmark names with `-count=5` and benchstat.
