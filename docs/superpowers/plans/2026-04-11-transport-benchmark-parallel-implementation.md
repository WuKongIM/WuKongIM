# Transport Parallel Benchmark Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `pkg/transport/benchmark_test.go` with `RunParallel` benchmarks for `Send` and `RPC` so developers can compare serial and concurrent transport costs across payload sizes.

**Architecture:** Reuse the existing benchmark harness and payload sizing from `pkg/transport/benchmark_test.go`, then add two new benchmark families: `BenchmarkTransportSendParallel` and `BenchmarkTransportRPCParallel`. Keep the concurrent path minimal: use `b.RunParallel`, retain the lightweight send backpressure helper, and avoid new scenario complexity beyond the parallel driver itself.

**Tech Stack:** Go 1.23, `testing.B.RunParallel`, existing `pkg/transport` benchmark harness, `context`

---

## File Structure

### Production files

- None.
  Responsibility: benchmark-only coverage.

### Test files

- Modify: `pkg/transport/benchmark_test.go`
  Responsibility: add concurrent benchmark coverage for `Send` and `RPC`.

### Existing references

- Reference: `docs/superpowers/specs/2026-04-11-transport-benchmark-parallel-design.md`
  Responsibility: approved scope and non-goals.
- Reference: `pkg/transport/benchmark_test.go`
  Responsibility: existing harness, payload sizing, and send backpressure helpers.

## Implementation Notes

- Follow `@superpowers:test-driven-development`: write the failing benchmark or helper test first, run it red, then implement the minimum change.
- Do not change the serial benchmark behavior unless the parallel work exposes a real harness bug.
- Reuse the same payload sizes (`16B`, `256B`) and naming pattern so the output stays comparable.
- `BenchmarkTransportSendParallel` should continue to respect bounded in-flight waiting so it measures transport work rather than immediate queue saturation.
- `BenchmarkTransportRPCParallel` can use `context.Background()` and a shared harness because request IDs are already generated inside `Pool.RPC`.

### Task 1: Add parallel benchmark safety coverage if needed

**Files:**
- Modify: `pkg/transport/benchmark_test.go`

- [ ] **Step 1: Write a failing concurrency smoke test only if the existing harness lacks one**

```go
func TestTransportBenchmarkHarnessConcurrentSmoke(t *testing.T) {
    h := newTransportBenchmarkHarness(t)
    defer h.Close()

    // Launch a few concurrent sends and RPCs and assert they all complete.
}
```

If existing smoke coverage is already sufficient after inspection, skip adding this test and proceed directly to Task 2.

- [ ] **Step 2: Run the focused smoke test (if added) and confirm it fails for the expected reason**

Run: `go test ./pkg/transport -run TestTransportBenchmarkHarnessConcurrentSmoke -count=1`

Expected: FAIL because the harness or helper behavior needs adjustment for concurrent use.

- [ ] **Step 3: Implement the minimal harness fix, if any**

Implementation details:
- only change the harness if the new smoke test demonstrates a real concurrency problem
- keep the harness shared across goroutines

- [ ] **Step 4: Re-run the focused smoke test and confirm it passes**

Run: `go test ./pkg/transport -run TestTransportBenchmarkHarnessConcurrentSmoke -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/benchmark_test.go
git commit -m "test: harden transport benchmark harness for parallel use"
```

### Task 2: Add `BenchmarkTransportSendParallel`

**Files:**
- Modify: `pkg/transport/benchmark_test.go`

- [ ] **Step 1: Write the failing parallel send benchmark**

```go
func BenchmarkTransportSendParallel(b *testing.B) {
    for _, size := range []int{16, 256} {
        b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
            h := newTransportBenchmarkHarness(b)
            defer h.Close()
            payload := bytes.Repeat([]byte("a"), size)

            b.ReportAllocs()
            b.ResetTimer()
            b.RunParallel(func(pb *testing.PB) {
                for pb.Next() {
                    if err := waitForTransportBenchmarkCapacity(context.Background(), h, 128); err != nil {
                        b.Fatal(err)
                    }
                    if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
                        b.Fatal(err)
                    }
                }
            })
        })
    }
}
```

- [ ] **Step 2: Run the parallel send benchmark and confirm it fails for the expected missing pieces**

Run: `go test ./pkg/transport -run '^$' -bench BenchmarkTransportSendParallel -benchmem -count=1`

Expected: FAIL because the benchmark definition does not exist yet or exposes a missing helper update.

- [ ] **Step 3: Implement the minimal parallel send benchmark**

Implementation details:
- reuse the existing send payload helper and in-flight tracking
- increment `raftSent` on every successful send
- drain outstanding sends after `RunParallel` completes before tearing down the harness

- [ ] **Step 4: Re-run the parallel send benchmark and confirm it produces benchmark output**

Run: `go test ./pkg/transport -run '^$' -bench BenchmarkTransportSendParallel -benchmem -count=1`

Expected: PASS with `payload=16B` and `payload=256B` output.

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/benchmark_test.go
git commit -m "test: add transport parallel send benchmark"
```

### Task 3: Add `BenchmarkTransportRPCParallel` and verify all benchmarks

**Files:**
- Modify: `pkg/transport/benchmark_test.go`

- [ ] **Step 1: Write the failing parallel RPC benchmark**

```go
func BenchmarkTransportRPCParallel(b *testing.B) {
    for _, size := range []int{16, 256} {
        b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
            h := newTransportBenchmarkHarness(b)
            defer h.Close()
            payload := bytes.Repeat([]byte("r"), size)
            ctx := context.Background()

            b.ReportAllocs()
            b.ResetTimer()
            b.RunParallel(func(pb *testing.PB) {
                for pb.Next() {
                    if _, err := h.rpcClient.RPC(ctx, transportStressNodeID, 0, payload); err != nil {
                        b.Fatal(err)
                    }
                }
            })
        })
    }
}
```

- [ ] **Step 2: Run the parallel RPC benchmark and confirm it fails for the expected missing pieces**

Run: `go test ./pkg/transport -run '^$' -bench BenchmarkTransportRPCParallel -benchmem -count=1`

Expected: FAIL because the benchmark definition does not exist yet.

- [ ] **Step 3: Implement the minimal parallel RPC benchmark**

Implementation details:
- keep response handling minimal
- avoid extra instrumentation in the timed path
- preserve the serial RPC benchmark behavior

- [ ] **Step 4: Run full verification for tests and all benchmarks**

Run:
- `go test ./pkg/transport -run 'Test(NewTransportBenchmarkHarness|TransportBenchmarkHarnessSmoke)' -count=1`
- `go test ./pkg/transport -run '^$' -bench 'BenchmarkTransport(Send|RPC)$|BenchmarkTransport(Send|RPC)Parallel' -benchmem -count=1`
- `go test ./pkg/transport/...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/benchmark_test.go
git commit -m "test: add transport parallel rpc benchmark"
```
