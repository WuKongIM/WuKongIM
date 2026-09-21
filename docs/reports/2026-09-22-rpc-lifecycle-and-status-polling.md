# RPC lifecycle isolation and benchmark status polling

The local RPC experiments did not establish a safe throughput improvement.
The benchmark worker now reads counters and gauges for lifecycle status without
copying or sorting active latency history. Full report collection and exact
legacy latency summaries remain unchanged. No cloud resources were purchased
for this work; the previous cloud lease remains released.

Source baseline: `c2e1e64f86bff97c043867725cec8466f463a4bb`.
Raw benchmark output, diagnostic patches, source digests, and all measurements
are retained in [the evidence JSON](assets/rpc-lifecycle-status-2026-09-22.json).
The [previous cloud report](2026-09-21-transport-response-cloud-validation.md)
remains the authority for the x86 cloud comparison.

## RPC isolation

The outstanding cloud result is a 7.71% throughput regression against the original
baseline for 64-byte/16-worker RPCs, and a 2.26% regression against the previous
candidate for 65,537-byte/16-worker RPCs. Local ARM64 measurements cannot certify
recovery of either result.

Four independent hypotheses were tested: queued cancellation registration,
execution deadline allocation, service-to-executor handoff, and unnecessary
pool-stat collection with the observer disabled. Each variant changed one path
using an external Go overlay; none was merged into production code. Removing
cancellation/deadline enforcement or bypassing the shared executor is unsafe for
production and was used only to distinguish costs.

All RPC binaries used the same source and native Linux ARM64 compilation,
GOMAXPROCS=4, CPUs 0–3 and a 6-GiB Docker memory cap on the local Apple M4.
The existing Debian amd64 root filesystem was reused only to launch the static
ARM64 executable directly: benchmark output reports `goarch: arm64`. This is
not a same-architecture reproduction of the earlier x86 cloud run.
The transport fixture used 16 prewarmed connections, request budgets enabled,
observer disabled, verified echo payloads, and asserted zero RPC errors.
Three sequential trials rotated variant order; each case ran for two seconds.
No builds, profiling, or regression suites overlapped timing windows.

64-byte, 16-worker results (all three samples retained in the evidence):

| Diagnostic variant | Median ns/op | Range ns/op | B/op | Allocations/op |
| --- | ---: | ---: | ---: | ---: |
| Baseline | 3,792 | 3,732–3,793 | 2,826 | 45 |
| Disable queued cancellation watch | 3,888 | 3,850–3,925 | 2,394 | 40 |
| Disable execution timeout | 3,690 | 3,419–3,752 | 2,554 | 41 |
| Dispatch directly to a goroutine | 3,712 | 3,591–4,081 | 2,842 | 46 |
| Skip pool stats with no observer | 3,869 | 3,735–3,912 | 2,826 | 45 |

`ns/op` is elapsed benchmark time divided by completed calls, not per-request
RTT. Removing queue watches saved five allocations and 432 bytes per operation
but reduced median throughput by 2.47%. Allocation concentration alone therefore
does not identify the throughput bottleneck. Execution timeouts and observer
pool-stat collection were already present in the original `68662c4c8` baseline;
their cost alone cannot explain the introduced cloud regression. Direct goroutine
dispatch adds an allocation, violates the shared executor bound, and has wide
timing variation. None of these probes justifies weakening lifecycle guarantees.

The unresolved candidates include request-context tracking, retained-admission
bookkeeping, and queue wakeup/handoff interactions. These remain hypotheses.
The earlier rejected experiment moving cancellation registration outside the
service mutex was not repeated. Future transport work should isolate those
remaining paths while preserving FIFO, bounded queues, cancellation, ownership,
and started-write completion semantics.

## Status polling change

The previous cloud worker CPU profiles attributed about 77–78% of CPU to
`summarizeDurations`. The current call chain confirmed that `LifecycleStatus`
requested a complete `MetricsSnapshot`, then discarded every latency summary.

`Registry.CollectProgress` now captures independently owned counter and gauge
maps under one registry lock. It does not copy latency history, gather error
samples, or wait on the collector mutex while a report sorts its private samples.
Both normal lifecycle polling and terminal traffic projection use this path.
They share the existing spatial/temporal counter and gauge merge logic with
reports. Archived windows already contain bounded summaries; connection-manager
metrics contain connection counters and bounded error samples rather than raw
latency history. Reports and archival collection continue to call `Collect`.
This does not change legacy raw-sample retention or final percentile semantics.

`BenchmarkLifecycleStatus` exercises the actual worker status projection with a
fixed seeded history in an active workload. No network clients are started.
It runs natively on macOS ARM64/Apple M4 with GOMAXPROCS=4, three one-second
repetitions, and setup outside timing. Median results:

| History samples | Before ns/query | After ns/query | Before B/query | After B/query |
| --- | ---: | ---: | ---: | ---: |
| 0 | 5,738 | 5,247 | 9,524 | 9,476 |
| 30,000 | 1,101,397 | 5,323 | 258,305 | 9,477 |
| 810,000 | 46,392,507 | 5,234 | 6,501,600 | 9,469 |

The 810,000-sample case fell from roughly 46.4 ms/6.5 MB to 5.2 µs/9.5 KB
per query. Query cost no longer grows with latency sample count in this fixture;
it still scales with metric series and active workload count. These are harness
query measurements, not server throughput or SEND latency claims. A separate
two-second CPU profile of the new path contained no sampled
`summarizeDurations` stack; profile timing is excluded from the table.

Reproduce the timing comparison with the baseline and candidate source:

```sh
GOWORK=off go test ./internal/bench/worker -run '^$' \
  -bench '^BenchmarkLifecycleStatus$' -benchtime=1s -count=3 -cpu=4 -benchmem
```

## Validation and limitations

- Full `internal/bench/...` unit tests passed.
- The allocation regression test fails with the old lifecycle call restored
  through an external overlay: 89 allocations without history versus 94 with
  history. The corrected path passed ten repetitions. This assertion uses
  `!race`, because race instrumentation adds variable allocations; semantic and
  concurrency tests still run under the race detector.
- Tests verify owned progress maps, one-cut counter/gauge capture, progress
  during report aggregation, unchanged latency samples and error evidence,
  warmup versus measured accounting, and active/archived generation merge rules.
- Race/integration coverage includes benchmark metrics/worker and all transport
  packages. The initial pass exposed an existing panic-test ordering error:
  receipt of a reply precedes deferred payload cleanup. That test now waits for
  `Request.Finish` before checking exactly-once release; production transport
  behavior is unchanged. Final rerun results accompany the evidence.
- `flow-doc-contracts` passes after regenerating the FLOW index. The nine
  advisory length warnings already existed; there are no invalid FLOW files.
- No new same-architecture cloud run was performed, and no business P99 or
  large-payload transport improvement is claimed for this change. Preserve the
  outstanding cloud regression until a lifecycle change has convincing local
  evidence and a controlled x86 cloud comparison confirms it.
