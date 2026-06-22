# internalv2 Plugin Benchmark Baseline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add focused benchmark baselines for command-style NoPersist realtime dispatch and plugin HTTP forward fanout-deferred behavior.

**Architecture:** Reuse existing benchmark helpers in `internalv2/runtime/channelappend` and `internalv2/usecase/plugin`. Keep the benchmarks in package-local `_test.go` files so they can access existing test ports and sinks without production changes.

**Tech Stack:** Go `testing`, internalv2 channelappend runtime, internalv2 plugin usecase, PDK-compatible `pluginproto`.

---

## File Structure

- Modify `internalv2/runtime/channelappend/benchmark_test.go`
  - Add a scoped command-style NoPersist realtime submit benchmark.
  - Add a lightweight recipient delivery enqueuer benchmark helper.
- Modify `internalv2/usecase/plugin/benchmark_test.go`
  - Add the `/plugin/httpForward toNodeId=-1` deferred branch benchmark.

No production code should change.

---

### Task 1: Add command-style NoPersist realtime submit benchmark

**Files:**
- Modify: `internalv2/runtime/channelappend/benchmark_test.go`

- [ ] **Step 1: Write the benchmark**

Add this benchmark near the existing submit benchmarks:

```go
func BenchmarkSubmitLocalNoPersistRealtimeScoped(b *testing.B) {
	group := newBenchmarkChannelAppendGroup(b, Options{
		LocalNodeID:                1,
		AuthorityShardCount:        1,
		EffectPoolSize:             4,
		AdmissionCapacityPerShard:  4096,
		InboxCoalesceWindow:        -time.Nanosecond,
		RecipientDeliveryEnqueuer:  &benchmarkRealtimeDeliveryEnqueuer{},
	})
	target := benchmarkAuthorityTarget("bench-nopersist-realtime")
	item := benchmarkSendItem("bench-nopersist-realtime")
	item.Command.NoPersist = true
	item.Command.SyncOnce = true
	item.Command.MessageScopedUIDs = []string{"u1"}
	startGoroutines := runtime.NumGoroutine()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		results, err := submitAndWaitBenchmark(group, target, item)
		if err != nil {
			b.Fatalf("submit/wait error = %v", err)
		}
		if len(results) != 1 || results[0].Err != nil || results[0].Result.Reason != ReasonSuccess {
			b.Fatalf("results = %#v, want one successful realtime result", results)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(runtime.NumGoroutine()-startGoroutines), "goroutine-delta")
}
```

Add this helper near the other benchmark helpers:

```go
type benchmarkRealtimeDeliveryEnqueuer struct {
	count atomic.Uint64
}

func (e *benchmarkRealtimeDeliveryEnqueuer) EnqueueRecipientBatch(context.Context, RecipientAuthorityTarget, RecipientBatch) error {
	e.count.Add(1)
	return nil
}
```

- [ ] **Step 2: Run the benchmark smoke**

Run:

```bash
go test ./internalv2/runtime/channelappend -run '^$' -bench '^BenchmarkSubmitLocalNoPersistRealtimeScoped$' -benchtime=1x
```

Expected: PASS with one benchmark result and allocation data.

---

### Task 2: Add plugin HTTP forward fanout-deferred benchmark

**Files:**
- Modify: `internalv2/usecase/plugin/benchmark_test.go`

- [ ] **Step 1: Write the benchmark**

Add this benchmark near `BenchmarkHTTPForward`:

```go
func BenchmarkHTTPForwardFanoutDeferred(b *testing.B) {
	app, err := NewApp(Options{Runtime: &recordingRuntime{}, Invoker: &recordingHTTPRouteInvoker{}, HTTPForwarder: &recordingHTTPForwarder{}})
	require.NoError(b, err)
	req := &pluginproto.ForwardHttpReq{
		PluginNo: "bench.plugin",
		ToNodeId: -1,
		Request: &pluginproto.HttpRequest{
			Method: http.MethodPost,
			Path:   "/fanout",
			Body:   []byte("fanout"),
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := app.HTTPForward(context.Background(), req, "bench.plugin")
		if !errors.Is(err, ErrHTTPForwardFanoutDeferred) {
			b.Fatalf("HTTPForward() error = %v, want ErrHTTPForwardFanoutDeferred", err)
		}
		if resp != nil {
			b.Fatalf("HTTPForward() response = %#v, want nil", resp)
		}
	}
}
```

Add `errors` to the import block.

- [ ] **Step 2: Run the benchmark smoke**

Run:

```bash
go test ./internalv2/usecase/plugin -run '^$' -bench '^BenchmarkHTTPForwardFanoutDeferred$' -benchtime=1x
```

Expected: PASS with one benchmark result and allocation data.

---

### Task 3: Verify the benchmark baseline

**Files:**
- Verify: `internalv2/runtime/channelappend/benchmark_test.go`
- Verify: `internalv2/usecase/plugin/benchmark_test.go`

- [ ] **Step 1: Run focused benchmark smoke together**

Run:

```bash
go test ./internalv2/runtime/channelappend ./internalv2/usecase/plugin -run '^$' -bench 'Benchmark(SubmitLocalNoPersistRealtimeScoped|HTTPForwardFanoutDeferred)$' -benchtime=1x
```

Expected: PASS. Both benchmark names appear in output.

- [ ] **Step 2: Run focused package tests**

Run:

```bash
go test ./internalv2/runtime/channelappend ./internalv2/usecase/plugin -count=1
```

Expected: PASS.

- [ ] **Step 3: Commit**

Run:

```bash
git add docs/superpowers/specs/2026-06-22-internalv2-plugin-benchmark-baseline-design.md docs/superpowers/plans/2026-06-22-internalv2-plugin-benchmark-baseline.md internalv2/runtime/channelappend/benchmark_test.go internalv2/usecase/plugin/benchmark_test.go
git commit -m "test: add plugin benchmark baselines"
```
