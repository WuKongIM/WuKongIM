# TransportV2 Performance Benchmarks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add repeatable transportv2 benchmarks for the ants-backed service executor, service admission, multi-service sharing, and end-to-end RPC/send pressure.

**Architecture:** Add benchmark-only code under existing `_test.go` files. Internal RPC benchmarks isolate service/executor behavior without network I/O; top-level benchmarks use the existing local TCP transport stack to measure full client/server behavior.

**Tech Stack:** Go benchmark framework, `pkg/transportv2`, `pkg/transportv2/internal/rpc`, `pkg/transportv2/internal/core`, existing `testkit` patterns.

---

## File Structure

- Create `pkg/transportv2/internal/rpc/benchmark_test.go`
  - Own internal executor and service benchmark helpers.
  - Keep all helpers unexported and benchmark-only.
- Modify `pkg/transportv2/benchmark_test.go`
  - Add configurable harness helper for benchmark variants.
  - Add connection-pool, multi-service RPC, and parallel send benchmarks.
- Add docs:
  - `docs/superpowers/specs/2026-06-11-transportv2-performance-benchmarks-design.md`
  - `docs/superpowers/plans/2026-06-11-transportv2-performance-benchmarks.md`

## Task 1: Add Internal RPC Benchmarks

**Files:**
- Create: `pkg/transportv2/internal/rpc/benchmark_test.go`

- [ ] **Step 1: Write benchmark file**

Create benchmarks for:

- `BenchmarkExecutorSubmit`
- `BenchmarkServiceRPCReplyPayloadSizes`
- `BenchmarkServiceSendOnlyParallel`
- `BenchmarkServiceSharedExecutorMultipleServices`

Use `core.CopyOwnedBuffer` for request ownership and retry `core.ErrBusy` with
`runtime.Gosched()` in pressure cases.

- [ ] **Step 2: Run focused benchmark check**

Run:

```sh
GOWORK=off go test ./pkg/transportv2/internal/rpc -run '^$' -bench 'Executor|Service' -benchmem -benchtime=100ms
```

Expected: PASS, with benchmark rows for all four benchmark families.

## Task 2: Add Top-Level Transport Benchmarks

**Files:**
- Modify: `pkg/transportv2/benchmark_test.go`

- [ ] **Step 1: Add benchmark harness helper**

Add an unexported helper that creates a local server/client pair with custom
pool size and service registrations.

- [ ] **Step 2: Add benchmark cases**

Add benchmarks for:

- `BenchmarkTransportV2RPCPoolSizes`
- `BenchmarkTransportV2RPCMultiServiceParallel`
- `BenchmarkTransportV2SendParallelWithBackpressure`

Retry only `transportv2.ErrQueueFull` in send pressure cases.

- [ ] **Step 3: Run focused benchmark check**

Run:

```sh
GOWORK=off go test ./pkg/transportv2 -run '^$' -bench 'TransportV2' -benchmem -benchtime=100ms
```

Expected: PASS, with existing and new top-level benchmark rows.

## Task 3: Verify Package Health

**Files:**
- No code changes expected.

- [ ] **Step 1: Run package tests**

Run:

```sh
GOWORK=off go test ./pkg/transportv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run combined benchmark smoke**

Run:

```sh
GOWORK=off go test -run '^$' -bench 'TransportV2|Service|Executor' -benchmem -benchtime=100ms ./pkg/transportv2/...
```

Expected: PASS.
