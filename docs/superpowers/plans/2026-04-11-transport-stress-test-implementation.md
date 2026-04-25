# Transport Stress Test Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a default-skipped `pkg/transport` stress test that runs mixed `PriorityRaft` send traffic and `PriorityRPC` load, then fails if high-priority send latency regresses or transport errors appear.

**Architecture:** Keep the implementation inside `pkg/transport/stress_test.go` so the stress harness, config parsing, and helper assertions stay close to the existing transport test suite. Build the work in TDD order: first nail down config and latency-summary helpers with ordinary unit tests, then add a real `Server + Pool + Client` harness, and finally wire the end-to-end stress case that is opt-in through environment variables.

**Tech Stack:** Go 1.23, `testing`, `context`, `net`, `sync`, `sync/atomic`, `time`, existing `pkg/transport` test helpers

---

## File Structure

### Production files

- None.
  Responsibility: this change only adds test coverage and helper code inside `_test.go`.

### Test files

- Create: `pkg/transport/stress_test.go`
  Responsibility: transport stress config parsing, latency summaries, local harness setup, and the mixed-load stress test.

### Existing references

- Reference: `docs/superpowers/specs/2026-04-11-transport-stress-test-design.md`
  Responsibility: approved scope, thresholds, environment variables, and non-goals.
- Reference: `pkg/transport/pool_test.go`
  Responsibility: reuse `staticDiscovery`, `requireEventually`, and existing pool/server setup patterns.
- Reference: `internal/app/send_stress_test.go`
  Responsibility: mirror the repository’s stress-test config parsing and percentile summary style.

## Implementation Notes

- Follow `@superpowers:test-driven-development` literally for each task: write the failing test first, run it red, implement the minimum code, run it green.
- Keep the stress path opt-in: if `WK_TRANSPORT_STRESS` is unset or false, the main stress test must `t.Skip` with a clear message.
- Keep helper logic deterministic and unit-testable even though the main stress case is time-based.
- Reuse a fixed `NodeID` and in-process listener; do not add multi-node scaffolding.
- Prefer small, fixed-size payloads so the test measures scheduling and muxing, not payload serialization overhead.
- Make sure `Server.Stop()` and both pools/clients are always closed so the stress test cannot leak goroutines or sockets into later tests.

### Task 1: Add config parsing and latency summary helpers

**Files:**
- Create: `pkg/transport/stress_test.go`
- Reference: `internal/app/send_stress_test.go`

- [ ] **Step 1: Write the failing helper tests**

```go
func TestLoadTransportStressConfigDefaults(t *testing.T) {
    t.Setenv("WK_TRANSPORT_STRESS", "")

    cfg := loadTransportStressConfig(t)

    if cfg.Enabled {
        t.Fatal("expected stress test to be disabled by default")
    }
    if cfg.Duration != 5*time.Second {
        t.Fatalf("Duration = %s, want %s", cfg.Duration, 5*time.Second)
    }
    if cfg.P99Budget != 20*time.Millisecond {
        t.Fatalf("P99Budget = %s, want %s", cfg.P99Budget, 20*time.Millisecond)
    }
}

func TestSummarizeTransportLatencies(t *testing.T) {
    summary := summarizeTransportLatencies([]time.Duration{
        5 * time.Millisecond,
        1 * time.Millisecond,
        3 * time.Millisecond,
        9 * time.Millisecond,
    })

    if summary.Count != 4 || summary.P50 != 3*time.Millisecond || summary.P99 != 9*time.Millisecond {
        t.Fatalf("unexpected summary: %#v", summary)
    }
}
```

Also add a negative-config test, for example `WK_TRANSPORT_STRESS_DURATION=0`, to prove validation fails loudly.

- [ ] **Step 2: Run the focused helper tests and confirm they fail**

Run: `go test ./pkg/transport -run 'Test(LoadTransportStressConfig|SummarizeTransportLatencies)' -count=1`

Expected: FAIL because `loadTransportStressConfig`, `transportStressConfig`, and the latency summary helpers do not exist yet.

- [ ] **Step 3: Implement the minimal helper code in `pkg/transport/stress_test.go`**

```go
type transportStressConfig struct {
    Enabled     bool
    Duration    time.Duration
    RaftWorkers int
    RPCWorkers  int
    RPCDelay    time.Duration
    Seed        int64
    P99Budget   time.Duration
}

type transportLatencySummary struct {
    Count int
    P50   time.Duration
    P95   time.Duration
    P99   time.Duration
    Max   time.Duration
}
```

Implementation details:
- parse `WK_TRANSPORT_STRESS`, `WK_TRANSPORT_STRESS_DURATION`, `WK_TRANSPORT_STRESS_RAFT_WORKERS`, `WK_TRANSPORT_STRESS_RPC_WORKERS`, `WK_TRANSPORT_STRESS_RPC_DELAY`, `WK_TRANSPORT_STRESS_SEED`, and `WK_TRANSPORT_STRESS_P99_BUDGET`
- default `RaftWorkers` to `max(2, runtime.GOMAXPROCS(0)/2)` and `RPCWorkers` to `max(2, runtime.GOMAXPROCS(0))`
- reuse the repository’s percentile calculation pattern (`math.Ceil(len*pct)-1`)

- [ ] **Step 4: Re-run the focused helper tests and confirm they pass**

Run: `go test ./pkg/transport -run 'Test(LoadTransportStressConfig|SummarizeTransportLatencies)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/stress_test.go
git commit -m "test: add transport stress config helpers"
```

### Task 2: Add the opt-in stress harness and default-skip behavior

**Files:**
- Modify: `pkg/transport/stress_test.go`
- Reference: `pkg/transport/pool_test.go`

- [ ] **Step 1: Write the failing harness tests**

```go
func TestRequireTransportStressEnabledSkipsWhenDisabled(t *testing.T) {
    cfg := transportStressConfig{}
    requireTransportStressEnabled(t, cfg)
}

func TestNewTransportStressHarnessStartsServerAndClients(t *testing.T) {
    h := newTransportStressHarness(t, transportStressConfig{RPCDelay: time.Millisecond})
    defer h.Close()

    if h.server.Listener() == nil {
        t.Fatal("expected active listener")
    }
    if h.raftClient == nil || h.rpcClient == nil {
        t.Fatal("expected both clients to be initialized")
    }
}
```

Also add a harness smoke test that sends one `PriorityRaft` message and one RPC through the harness, then waits for one latency sample and one RPC success.

- [ ] **Step 2: Run the focused harness tests and confirm they fail**

Run: `go test ./pkg/transport -run 'Test(RequireTransportStressEnabled|NewTransportStressHarness)' -count=1`

Expected: FAIL because the harness helpers and skip gate do not exist yet.

- [ ] **Step 3: Implement the harness and smoke-path helpers**

```go
type transportStressHarness struct {
    server     *Server
    raftClient *Client
    rpcClient  *Client
    raftStats  *transportSampleCollector
    rpcStats   *transportSampleCollector
}
```

Implementation details:
- start `NewServer()` on `127.0.0.1:0`
- register a dedicated test message type for raft samples
- store `sentAtUnixNano` and `sequence` in a fixed 16-byte payload
- use two pools that share the same `staticDiscovery` address but differ in `DefaultPri`
- keep handler work minimal: decode, record latency, release
- make `Close()` shut down clients/pools before `server.Stop()`

- [ ] **Step 4: Re-run the focused harness tests and confirm they pass**

Run: `go test ./pkg/transport -run 'Test(RequireTransportStressEnabled|NewTransportStressHarness)' -count=1`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/stress_test.go
git commit -m "test: add transport stress harness"
```

### Task 3: Add the mixed-load stress test and final assertions

**Files:**
- Modify: `pkg/transport/stress_test.go`
- Reference: `docs/superpowers/specs/2026-04-11-transport-stress-test-design.md`

- [ ] **Step 1: Write the failing end-to-end stress test**

```go
func TestTransportStressRaftSendUnderRPCLoad(t *testing.T) {
    cfg := loadTransportStressConfig(t)
    requireTransportStressEnabled(t, cfg)

    h := newTransportStressHarness(t, cfg)
    defer h.Close()

    ctx, cancel := context.WithTimeout(context.Background(), cfg.Duration)
    defer cancel()

    runTransportStressWorkers(ctx, t, h, cfg)

    raftSummary := summarizeTransportLatencies(h.raftStats.Snapshot())
    rpcSummary := summarizeTransportLatencies(h.rpcStats.Snapshot())

    if raftSummary.Count < 1000 {
        t.Fatalf("raft sample count = %d, want >= 1000", raftSummary.Count)
    }
    if raftSummary.P99 > cfg.P99Budget {
        t.Fatalf("raft p99 = %s, want <= %s", raftSummary.P99, cfg.P99Budget)
    }
}
```

Also assert:
- `rpcSummary.Count >= 1000`
- zero raft send errors
- zero RPC errors
- structured `t.Logf` output with `seed`, worker counts, counts, and percentiles

- [ ] **Step 2: Run the stress test with explicit env vars and confirm it fails for the expected missing pieces**

Run:

```bash
WK_TRANSPORT_STRESS=1 \
WK_TRANSPORT_STRESS_DURATION=2s \
WK_TRANSPORT_STRESS_RPC_DELAY=2ms \
go test ./pkg/transport -run TestTransportStressRaftSendUnderRPCLoad -count=1
```

Expected: FAIL because worker orchestration, sample collectors, and/or assertions are not fully implemented yet.

- [ ] **Step 3: Implement the worker loop, collectors, and final assertions**

```go
func runTransportStressWorkers(ctx context.Context, t *testing.T, h *transportStressHarness, cfg transportStressConfig) {
    // Start raft workers that continuously Send fixed 16-byte payloads.
    // Start rpc workers that continuously call RPC and record round-trip latency.
    // Stop all workers on ctx.Done(), capturing the first unexpected error.
}
```

Implementation details:
- use `sync.WaitGroup` plus an error channel or first-error slot
- record `Send` timestamps before enqueueing the payload
- keep RPC responses tiny and deterministic (for example `"ok"`)
- log a one-line metrics summary at the end
- ensure the test remains skipped unless `WK_TRANSPORT_STRESS=1`

- [ ] **Step 4: Re-run both the targeted unit tests and the live stress test until green**

Run:
- `go test ./pkg/transport -run 'Test(LoadTransportStressConfig|SummarizeTransportLatencies|RequireTransportStressEnabled|NewTransportStressHarness)' -count=1`
- `WK_TRANSPORT_STRESS=1 WK_TRANSPORT_STRESS_DURATION=2s WK_TRANSPORT_STRESS_RPC_DELAY=2ms go test ./pkg/transport -run TestTransportStressRaftSendUnderRPCLoad -count=1`
- `go test ./pkg/transport/...`

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/transport/stress_test.go
git commit -m "test: add transport mixed-load stress coverage"
```
