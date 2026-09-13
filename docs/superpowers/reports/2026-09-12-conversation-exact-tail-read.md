# Conversation exact-tail read comparison

2026-09-12. Local dirty-worktree Linux ARM64 diagnosis, not release publication.

## Evidence and selected change

The previous instrumented comparison localized HTTP refusals to the shared
16-batch serving-node pool. Average occupancy was low, so it did not establish
continuous saturation or one root cause for every transient full-pool event.
This follow-up tested three hypotheses: duplicate metadata/RPC work, repeated
storage work within admitted batches, and scheduler/lock stalls.

Existing new-variant list node-3 CPU samples attributed 4.66 of 11.73 sampled
CPU seconds to stored-head reads, including 1.37 s in tail selection. A sync-420
five-second trace attributed only about 70 ms aggregate synchronization blocking
to selected stored-read paths; it does not establish a sustained storage-lock
bottleneck or rule out unsampled stalls. Runnable-delay profiles include RPC
worker/observer wakeups and cannot be interpreted as per-request queue latency.
RPC counters show about 18.67 relevant calls per list request and 37.33 per sync
request (background control/raft excluded). Sync hydrates 200 candidate heads in
the page-100 fixture. These are additional costs, not proof they trigger every
refusal. Directory selection, authority revalidation and RPC topology remain
unchanged in this controlled experiment.

The only production change is 14 lines in `pkg/db/message/read.go`: a reverse
single-record read with a finite positive sequence uses the existing durable
primary-row lookup. A found row returns immediately with existing decode,
materialization validation and owned payload semantics. Only an absent row
falls back to the original bounded predecessor iterator. Corruption and I/O
errors fail directly; unresolved/max sequence bounds retain iteration. A public
zero/latest request may first resolve LEO, then use that exact sequence. First-record
MaxBytes behavior, frontier, retention and the shared 16-batch admission cap
remain unchanged. No caching, runtime activation, new queue or retry is added.

## Tight feedback loop

`GOWORK=off go test ./pkg/db/message -run '^TestReverseTailWarmAllocationBudget$' -count=1`
failed before the change (35 allocations exceeded the 27-allocation ceiling)
and passes after it (16 observed allocations). This is a deterministic cost
regression, not a deterministic reproduction of intermittent HTTP refusal.
The original symptom is checked separately by the fixed long-load windows.
Boundary tests compare one-row and range reads at exact, missing, zero and
maximum bounds; they cover retention, oversized first payloads, corruption,
cancellation and unavailable storage. The existing older-corrupt-row test also
passes, preserving bounded history access.

Darwin ARM64, Apple M4, Go 1.25.0; three one-second benchmark samples each:

| Reverse read | Before median | After median | Before allocs | After allocs |
| --- | ---: | ---: | ---: | ---: |
| Exact known sequence | 1523 ns | 1043 ns | 35 | 16 |
| Missing finite bound | 1530 ns | 1869 ns | 35 | 41 |

The exact path improves about 31.5%; a missing bound costs about 22.2% more due
to one extra point miss before the unchanged scan. Both endpoints usually start
preview reads at their selected persisted tail. Sparse/missing-bound workloads
are a tradeoff, not claimed to improve. Benchmark bytes are about 2614→1907 for
exact reads and 2615→2735 for missing bounds. These are function microbenchmarks,
not HTTP throughput or cross-OS capacity claims.

## Controlled long-load protocol

The before binary is rebuilt with a Go overlay restoring only the prior
`read.go`. Its hash exactly matches the preceding measured instrumented binary.
Both variants use Go 1.25.11, CGO enabled, Linux ARM64 and identical remaining
production source. Binary/source hashes and build metadata are in the companion
JSON. HEAD is only the dirty checkout base, not an exact release revision.

Six fresh three-node cluster runs use counterbalanced pairs: old-1/new-1,
new-2/old-2, old-3/new-3. Each run uses 600 groups, 24 users, 200 memberships/user,
three 256-byte messages/group, 256 hash slots, 12 physical Slots, GOMAXPROCS=2
per node, 10 for the driver and 48 workers. The local Docker environment has
10 vCPUs, a 6 GiB memory cap and no CPU quota. Fixture runtimes are evicted before
measurement; warm disk caches are intentional.

Each variant receives three uninterrupted 180-second page-100 windows at list
1200 QPS and sync 420 QPS. There are no profiled measurements, measured retries,
rate halving or threshold changes. Every refused/dropped window remains rejected;
unexpected errors, runtime activation or membership writes abort collection.
Complete diagnostic collection does not mean capacity passed. The normal
8-worker 12-case release gate runs separately on the selected candidate.

```sh
WK_E2E_CONVERSATION_PEAK_CONFIRM=1 \
WK_E2E_CONVERSATION_BACKPRESSURE_REPORT=/out/RUN/report.json \
WK_E2E_BINARY=/out/VARIANT GOWORK=off \
  go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSPeakConfirmation$' -count=1 -timeout=12m -p=1 -v
```

## Results

Each row is one uninterrupted 180-second, 48-worker window. All 12 windows
had zero driver drops, unexpected errors, runtime loads, active runtimes and
membership writes. HTTP refusals below matched serving-node admission rejection
counts, with no byte-budget failures. Accepted/completed batches balanced on every
node and kind, and post-window in-flight counts were zero.

| Run order | Endpoint | HTTP refusals | P99 | Verdict |
| --- | --- | ---: | ---: | --- |
| old-1 | /conversation/list | 2 | 72.86 ms | incomplete or failed requests |
| old-1 | /conversation/sync | 31 | 143.72 ms | incomplete or failed requests |
| new-1 | /conversation/list | 0 | 41.87 ms | pass |
| new-1 | /conversation/sync | 6 | 89.58 ms | incomplete or failed requests |
| new-2 | /conversation/list | 0 | 34.84 ms | pass |
| new-2 | /conversation/sync | 2 | 84.91 ms | incomplete or failed requests |
| old-2 | /conversation/list | 0 | 45.83 ms | pass |
| old-2 | /conversation/sync | 2 | 111.56 ms | incomplete or failed requests |
| old-3 | /conversation/list | 0 | 42.18 ms | pass |
| old-3 | /conversation/sync | 5 | 124.51 ms | incomplete or failed requests |
| new-3 | /conversation/list | 0 | 32.39 ms | pass |
| new-3 | /conversation/sync | 13 | 133.74 ms | incomplete or failed requests |

| Offered load | Variant | Passing windows | Total refusals | CPU ms/success | Allocated bytes/success |
| --- | --- | ---: | ---: | ---: | ---: |
| /conversation/list 1200 QPS | old | 2/3 | 2 | 3.639 | 2146116 |
| /conversation/sync 420 QPS | old | 0/3 | 38 | 10.922 | 8182806 |
| /conversation/list 1200 QPS | new | 3/3 | 0 | 3.467 | 2077329 |
| /conversation/sync 420 QPS | new | 0/3 | 21 | 10.599 | 8041663 |

The new list candidate passes all three local 1200-QPS windows, with P99
32.39–41.87 ms. The old candidate passed two windows and rejected one (two HTTP
refusals). List server CPU per successful response fell 4.75%, and allocation
bytes per successful response fell 3.21%. This supports retaining the point-read
optimization for this fixture; it is not a production capacity guarantee.

Sync at 420 QPS remains unqualified: both variants failed all three windows.
Old/new rejection pairs are 31/6, 2/2, and 5/13. The totals (38 old, 21 new)
therefore must not be presented as a reliably established refusal-rate gain;
the last pair regressed, and these are only three pairs. New sync CPU/success
fell 2.96% and allocation bytes/success fell 1.72%, but the target of zero
refusals was not met. The optimization is a cost reduction, not a verified fix
for every transient admission burst. P99 values are per-window percentiles;
CPU includes failed/background work in the measured interval divided by
successful responses, rather than isolated request CPU.

The new exact-tail candidate keeps all shared concurrency, error and visibility
semantics. No additional RPC/metadata/pagination changes were mixed into the
experiment to obtain a passing result. Remaining sync work should specifically
investigate the 200-head candidate hydration and repeated authoritative metadata
rounds, with visibility, ordering, and failure coverage before reducing work.
That is separate work; this report does not declare the overall two-endpoint
high-load stability goal achieved.

The unchanged 8-worker release gate passed all 12 cases on the new binary:
26,400 scheduled and successful requests, zero errors/drops/runtime loads/
membership writes, zero active runtimes, and maximum window P99 57.58 ms.
The gate checks both endpoints and page sizes 25/100/200 on single-node and
three-node clusters, including allocation ceilings. Its profile SHA-256 remains
`b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`;
the full exact-binary receipt is retained in the companion JSON. This is a local
dirty-worktree regression pass, not clean exact-tag publication evidence.

Observed mean admitted heads-batch processing time fell from 0.619 to 0.507 ms
for list and 1.485 to 1.195 ms for sync, weighted by completed batches across the
three nodes/windows. These include processing/scheduling effects and exclude
the observer callbacks, as defined by the existing instrumentation. Recents
also fell from 0.812 to 0.685 ms despite no direct recents-path change, so these
wall-time changes are not isolated function-speed measurements. They support
reduced shared pressure, not elimination of transient saturation. The final
container checkpoint showed no CPU throttling or memory/OOM events.

## Validation and delivery

Related unit packages (`pkg/db/message`, `pkg/channel/store`,
`pkg/cluster/channels`, `internal/usecase/conversation`, `internal/usecase/message`,
`internal/app`), focused race, Linux reverse-read tests, harness compilation and
named go-format/flow-doc-contracts checks passed. No repository-wide test pass
or clean exact-tag release qualification is claimed.

Raw artifacts live under `/tmp/conversation-peak-read`; hashes, complete window
results, build/source identities, gate receipt and cleanup are recorded in the
companion JSON. The owned local container `wk-conversation-peak-20260912`
(`90ceced0154656c1f05cf787d9f0a6ea2dc9558b4c79669d637d07c097d37d7a`) was
removed after verifying no server processes remained. Unrelated containers and
the shared build cache were preserved. No merge, push or publication is included.
