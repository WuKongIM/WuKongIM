# ISR Pressure Analysis

## Environment

- `go version`: go1.23.4 darwin/arm64
- `GOOS`: darwin
- `GOARCH`: arm64
- `GOPATH`: /Users/tt/go
- `GOMOD`: /Users/tt/Desktop/work/go/WuKongIM-v3.1/.worktrees/isr-library-implementation/go.mod
- `uname -a`: Darwin ttdeMac-mini.local 24.1.0 Darwin Kernel Version 24.1.0: Thu Oct 10 21:06:23 PDT 2024; root:xnu-11215.41.3~3/RELEASE_ARM64_T8132 arm64
- `CPU`: Apple M4

## Commands

### Baseline

```bash
go test ./pkg/isr -run '^$' -bench '^BenchmarkReplicaAppend$' -benchmem -benchtime=3s | tee tmp/isr-pressure/2026-04-02/append-baseline.txt
go test ./pkg/isr -run '^$' -bench '^BenchmarkThreeReplicaReplicationRoundTrip$' -benchmem -benchtime=3s | tee tmp/isr-pressure/2026-04-02/roundtrip-baseline.txt
go test ./pkg/isr -run '^$' -bench '^(BenchmarkReplicaFetch|BenchmarkReplicaApplyFetch|BenchmarkNewReplicaRecovery)$' -benchmem -benchtime=3s | tee tmp/isr-pressure/2026-04-02/aux-baseline.txt
```

### Profiles

```bash
go test ./pkg/isr -run '^$' -bench '^BenchmarkReplicaAppend$' -benchtime=3s \
  -cpuprofile tmp/isr-pressure/2026-04-02/append.cpu.pprof \
  -memprofile tmp/isr-pressure/2026-04-02/append.mem.pprof
go tool pprof -top tmp/isr-pressure/2026-04-02/append.cpu.pprof > tmp/isr-pressure/2026-04-02/append.cpu.top.txt
go tool pprof -top tmp/isr-pressure/2026-04-02/append.mem.pprof > tmp/isr-pressure/2026-04-02/append.mem.top.txt
go test ./pkg/isr -run '^$' -bench '^BenchmarkThreeReplicaReplicationRoundTrip$' -benchtime=3s \
  -cpuprofile tmp/isr-pressure/2026-04-02/roundtrip.cpu.pprof \
  -memprofile tmp/isr-pressure/2026-04-02/roundtrip.mem.pprof
go tool pprof -top tmp/isr-pressure/2026-04-02/roundtrip.cpu.pprof > tmp/isr-pressure/2026-04-02/roundtrip.cpu.top.txt
go tool pprof -top tmp/isr-pressure/2026-04-02/roundtrip.mem.pprof > tmp/isr-pressure/2026-04-02/roundtrip.mem.top.txt
```

## Baseline Results

Append baseline:

- `BenchmarkReplicaAppend/batch=1/payload=128`: `2148 ns/op`, `1909 B/op`, `23 allocs/op`
- `BenchmarkReplicaAppend/batch=16/payload=128`: `5528 ns/op`, `13463 B/op`, `109 allocs/op`
- `BenchmarkReplicaAppend/batch=16/payload=1024`: `15018 ns/op`, `85158 B/op`, `109 allocs/op`

Round-trip baseline:

- `BenchmarkThreeReplicaReplicationRoundTrip/batch=1/payload=128`: `2221 ns/op`, `1907 B/op`, `23 allocs/op`

Supporting baselines:

- `BenchmarkReplicaFetch/max_bytes=4096/backlog=32`: `1167 ns/op`, `6264 B/op`, `39 allocs/op`
- `BenchmarkReplicaFetch/max_bytes=65536/backlog=256`: `7998 ns/op`, `51832 B/op`, `266 allocs/op`
- `BenchmarkReplicaApplyFetch/mode=append_only`: `129.9 ns/op`, `347 B/op`, `2 allocs/op`
- `BenchmarkReplicaApplyFetch/mode=truncate_append`: `851.1 ns/op`, `417 B/op`, `10 allocs/op`
- `BenchmarkNewReplicaRecovery/state=empty`: `92.12 ns/op`, `368 B/op`, `2 allocs/op`
- `BenchmarkNewReplicaRecovery/state=clean_checkpoint`: `100.3 ns/op`, `400 B/op`, `4 allocs/op`
- `BenchmarkNewReplicaRecovery/state=dirty_tail`: `947.9 ns/op`, `2793 B/op`, `9 allocs/op`

## CPU Hotspots

Append CPU profile (`tmp/isr-pressure/2026-04-02/append.cpu.top.txt`):

- Runtime wake/sleep primitives dominate the sample: `runtime.pthread_cond_signal` `45.69%`, `runtime.pthread_cond_wait` `23.01%`, `runtime.usleep` `9.80%`, `runtime.madvise` `9.69%`.
- No `pkg/isr` function appears as a meaningful flat CPU hotspot in this capture. The visible `pkg/isr` rows are cumulative only and stay below `1%` flat, for example `(*benchmarkRoundTripHarness).runOnce` and `(*replica).Fetch`.
- Interpretation: append-path CPU time is dominated by scheduler, parking, and wakeup behavior from the goroutine-driven benchmark harness, not by a concentrated `pkg/isr` compute hotspot.

Round-trip CPU profile (`tmp/isr-pressure/2026-04-02/roundtrip.cpu.top.txt`):

- The same runtime pattern repeats: `runtime.pthread_cond_signal` `45.43%`, `runtime.pthread_cond_wait` `25.19%`, `runtime.usleep` `15.06%`, `runtime.kevent` `4.94%`.
- The only visible flat `pkg/isr` CPU row is `(*fakeLogStore).Append` at `0.12%`; `(*replica).Append` appears only as `0.62%` cumulative.
- Interpretation: the round-trip benchmark is also CPU-bound mainly by runtime synchronization and goroutine scheduling, so this profile does not show a single `pkg/isr` function that dominates CPU consumption.

## Memory Hotspots

Append alloc profile (`tmp/isr-pressure/2026-04-02/append.mem.top.txt`):

- `cloneRecord` dominates allocations at `79.45%`.
- Secondary contributors are `(*fakeLogStore).Read` at `5.09%` flat / `36.67%` cumulative, `(*benchmarkRoundTripHarness).rebuild` at `4.42%`, `(*callLog).add` at `1.96%`, and `(*replica).Append` at `1.76%` flat / `18.56%` cumulative.
- Timer and checkpoint bookkeeping also show up: `time.NewTimer` `1.17%`, `(*fakeCheckpointStore).Store` `1.33%`, `(*replica).advanceHWLocked` `1.06%`.
- Interpretation: append-path allocation pressure is real, but it is split between `pkg/isr` record-copy paths and benchmark fixture churn in the fake log/checkpoint stores and harness rebuild path.

Round-trip alloc profile (`tmp/isr-pressure/2026-04-02/roundtrip.mem.top.txt`):

- `cloneRecord` remains the largest allocator at `30.26%`.
- Additional heavy allocators are `(*callLog).add` `10.79%`, `(*benchmarkRoundTripHarness).runOnce` `10.09%` flat / `73.35%` cumulative, `(*replica).Append` `9.32%` flat / `19.42%` cumulative, `(*fakeCheckpointStore).Store` `6.78%`, and `(*replica).advanceHWLocked` `6.09%`.
- Timer and harness-reset overhead are material: `time.NewTimer` `6.66%`, `(*benchmarkRoundTripHarness).rebuild` `5.79%`, and `(*fakeLogStore).Read` `3.23%` flat / `15.50%` cumulative.
- Interpretation: round-trip allocations are dominated by `pkg/isr` data-copying and bookkeeping paths, but benchmark doubles and reset mechanics still contribute a meaningful share of the allocation profile.

## Recommendation

Based on the current evidence, a production optimization pass is not justified yet:

- CPU time is dominated by runtime scheduling and synchronization primitives, not by a small set of `pkg/isr` compute hotspots.
- The largest allocation sites are split between `pkg/isr` record-copying/bookkeeping and test-only harness code such as `benchmarkRoundTripHarness`, `fakeLogStore`, and checkpoint/reset helpers.
- The benchmark doubles and reset mechanics contribute enough noise that the end-to-end allocation profile is not cleanly attributable to production logic alone.
- The remaining `pkg/isr` allocation sites are real, but the evidence here is not yet specific enough to define a bounded change that stays clearly within the current semantics and test boundary.

## Conclusion

暂不建议优化
