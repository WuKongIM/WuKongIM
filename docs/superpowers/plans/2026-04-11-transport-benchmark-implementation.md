# Transport Benchmark Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `pkg/transport/benchmark_test.go` with end-to-end microbenchmarks for `Send` and `RPC` so developers can compare `ns/op`, `B/op`, and `alloc/op` across payload sizes.

**Architecture:** Keep the benchmark code inside `pkg/transport/benchmark_test.go` and reuse the same in-process transport topology as `stress_test.go`: one local `Server`, one `raft` client, one `rpc` client, and fixed payload buffers. Build the work in TDD order by first writing a small harness smoke test for benchmark setup, then add `BenchmarkTransportSend` and `BenchmarkTransportRPC` with `16B` and `256B` sub-benchmarks, and finally run targeted benchmark verification plus the package test suite.

**Tech Stack:** Go 1.23, `testing.B`, `context`, `net`, `sync/atomic`, existing `pkg/transport` client/pool/server types

---

## File Structure

### Production files

- None.
  Responsibility: this work only adds benchmark and test-only helper coverage.

### Test files

- Create: `pkg/transport/benchmark_test.go`
  Responsibility: benchmark-local harness helpers and end-to-end `Send` / `RPC` microbenchmarks.

### Existing references

- Reference: `docs/superpowers/specs/2026-04-11-transport-benchmark-design.md`
  Responsibility: approved benchmark scope and non-goals.
- Reference: `pkg/transport/stress_test.go`
  Responsibility: reuse the existing single-node local transport harness pattern and constants.
- Reference: `pkg/transport/pool_test.go`
  Responsibility: reuse existing `staticDiscovery` and transport test idioms.

## Implementation Notes

- Follow `@superpowers:test-driven-development` literally: write a failing test first, run it red, implement the minimum code, run it green.
- Keep benchmark setup outside the timed region by doing all server/pool/client construction before `b.ResetTimer()`.
- Keep benchmark payloads preallocated and reused so the benchmark measures transport costs rather than repeated payload construction.
- Call `b.ReportAllocs()` in every benchmark and sub-benchmark.
- Do not add mixed-load or `RunParallel` benchmarks in this first pass.
- Reuse the existing message type and node id from `stress_test.go` so the benchmark path stays comparable to the new stress test.

### Task 1: Add benchmark harness smoke coverage

**Files:**
- Create: `pkg/transport/benchmark_test.go`
- Reference: `pkg/transport/stress_test.go`

- [ ] **Step 1: Write the failing benchmark harness smoke test**

```go
func TestNewTransportBenchmarkHarnessStartsServerAndClients(t *testing.T) {
    h := newTransportBenchmarkHarness(t)
    defer h.Close()

    if h.server.Listener() == nil {
        t.Fatal("expected active listener")
    }
    if h.raftClient == nil || h.rpcClient == nil {
        t.Fatal("expected both clients to be initialized")
    }
}
```

Also add a smoke test that sends one message and one RPC through the benchmark harness so setup is validated before timing anything.

- [ ] **Step 2: Run the focused smoke tests and confirm they fail**

Run: `go test ./pkg/transport -run 'Test(NewTransportBenchmarkHarness|TransportBenchmarkHarnessSmoke)' -count=1`

Expected: FAIL because `newTransportBenchmarkHarness` and its helpers do not exist yet.

- [ ] **Step 3: Implement the minimal benchmark harness**

```go
type transportBenchmarkHarness struct {
    server     *Server
    raftClient *Client
    rpcClient  *Client
}
```

Implementation details:
- start one local `Server` on `127.0.0.1:0`
- register one lightweight send handler and one RPC handler returning `[]byte("ok")`
- create two `Pool`s with shared discovery but different default priorities (`PriorityRaft`, `PriorityRPC`)
- add `Close()` to shut down clients and server cleanly

- [ ] **Step 4: Re-run the focused smoke tests and confirm they pass**

Run: `go test ./pkg/transport -run 'Test(NewTransportBenchmarkHarness|TransportBenchmarkHarnessSmoke)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/benchmark_test.go
git commit -m "test: add transport benchmark harness"
```

### Task 2: Add `BenchmarkTransportSend`

**Files:**
- Modify: `pkg/transport/benchmark_test.go`
- Reference: `docs/superpowers/specs/2026-04-11-transport-benchmark-design.md`

- [ ] **Step 1: Write the failing benchmark definition for send**

```go
func BenchmarkTransportSend(b *testing.B) {
    for _, size := range []int{16, 256} {
        b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
            h := newTransportBenchmarkHarness(b)
            defer h.Close()
            payload := bytes.Repeat([]byte("a"), size)

            b.ReportAllocs()
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                if err := h.raftClient.Send(transportStressNodeID, 0, transportStressMsgType, payload); err != nil {
                    b.Fatal(err)
                }
            }
        })
    }
}
```

- [ ] **Step 2: Run the send benchmark and confirm it fails for the expected missing pieces**

Run: `go test ./pkg/transport -run '^$' -bench BenchmarkTransportSend -benchmem -count=1`

Expected: FAIL because the benchmark harness is incomplete or the benchmark definition does not exist yet.

- [ ] **Step 3: Implement the minimal send benchmark**

Implementation details:
- keep payload allocation outside the timed loop
- reuse one harness per sub-benchmark
- leave the server send handler lightweight so `Send` measures the real transport path without extra logic

- [ ] **Step 4: Re-run the send benchmark and confirm it produces benchmark output**

Run: `go test ./pkg/transport -run '^$' -bench BenchmarkTransportSend -benchmem -count=1`

Expected: PASS with `BenchmarkTransportSend/payload=16B` and `BenchmarkTransportSend/payload=256B` results.

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/benchmark_test.go
git commit -m "test: add transport send benchmark"
```

### Task 3: Add `BenchmarkTransportRPC` and verify package-wide behavior

**Files:**
- Modify: `pkg/transport/benchmark_test.go`
- Reference: `pkg/transport/stress_test.go`

- [ ] **Step 1: Write the failing benchmark definition for RPC**

```go
func BenchmarkTransportRPC(b *testing.B) {
    for _, size := range []int{16, 256} {
        b.Run(fmt.Sprintf("payload=%dB", size), func(b *testing.B) {
            h := newTransportBenchmarkHarness(b)
            defer h.Close()
            payload := bytes.Repeat([]byte("r"), size)
            ctx := context.Background()

            b.ReportAllocs()
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                if _, err := h.rpcClient.RPC(ctx, transportStressNodeID, 0, payload); err != nil {
                    b.Fatal(err)
                }
            }
        })
    }
}
```

Also add a tiny assertion-oriented test if needed to keep any new RPC helper behavior under `go test` coverage.

- [ ] **Step 2: Run the RPC benchmark and confirm it fails for the expected missing pieces**

Run: `go test ./pkg/transport -run '^$' -bench BenchmarkTransportRPC -benchmem -count=1`

Expected: FAIL because the RPC benchmark or helper path is not fully implemented yet.

- [ ] **Step 3: Implement the minimal RPC benchmark and cleanup**

Implementation details:
- keep the RPC response body fixed and tiny (`"ok"`)
- use `context.Background()` inside the benchmark since timing is controlled by the benchmark loop
- avoid extra per-iteration helper allocations beyond the transport path itself

- [ ] **Step 4: Run full verification for benchmarks and tests**

Run:
- `go test ./pkg/transport -run 'Test(NewTransportBenchmarkHarness|TransportBenchmarkHarnessSmoke)' -count=1`
- `go test ./pkg/transport -run '^$' -bench 'BenchmarkTransport(Send|RPC)' -benchmem -count=1`
- `go test ./pkg/transport/...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/benchmark_test.go
git commit -m "test: add transport rpc benchmark"
```
