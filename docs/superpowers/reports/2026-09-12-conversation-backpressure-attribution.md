# Conversation backpressure attribution

2026-09-12. Local dirty-worktree diagnostics; not release publication evidence.

## Scope and controlled comparison

The preceding iterator-reuse run confirmed one local three-minute sync load at
400 QPS, but rejected 420/440 QPS sync and a 1200-QPS list confirmation. This run
compares the old and new unread-count implementations under identical observation
and load, then attributes refusals to their serving-node stage.

Both comparison binaries contain the same new bounded instrumentation. A Go
build overlay replaces only `pkg/db/message/non_business_index.go` for the old
variant using the source saved before iterator reuse. Every other production
source, compiler, fixture and profile is shared. Exact original and rebuilt
binary hashes plus the overlay/source hashes are recorded in the companion JSON.
These are instrumented rebuilds, not reruns of the unchanged prior binaries.
The pair is run old then new, without randomizing order; it is evidence for this
local fixture, not a statistically established production capacity gain.

## Observability and validation

An optional Channel observer is preserved through the app's composite observer
into `wukongim_conversation_persisted_*` metric families. Fixed heads/recents/other
kinds and accepted/rejected or ok/error/byte_budget outcomes expose:

- Immediate serving-node admission decisions and the shared batch limit.
- In-flight accepted batches and occupancy sampled at admission.
- Processing wall time while holding the read slot and batch item counts.

There is no serving-node waiting queue. The existing limit remains 16 batches;
accepted work is still serial within each batch and every terminal path releases
its slot. Occupancy is a concurrent sample, while the rejection counter records
the actual admission decision. Hold time excludes pre-admission routing/RPC and
the admission-observer callback; completion-observer overhead is also excluded.
Byte-budget failures remain distinct from admission refusals. No Channel/UID or
error-string labels, new request cache, active-reader increase or retry path is
introduced. Runtime activation, persisted visibility, whole-request failures and
all release thresholds remain unchanged.

The regression test was run before implementation and failed because the
serving-node refusal produced zero observer callbacks. It now passes and proves
no storage call on overflow plus balanced admission/completion on read failure.
Recent-message coverage also checks cancellation before admission, slot
release on store failure, and byte-budget rejection after successful admission. Metrics tests verify in-flight balance, refusal counts,
zero series, bounded unknown labels and disabled-observer safety. App composition
tests verify the optional signals reach every supporting child observer.

Full unit packages passed for `pkg/metrics`, `pkg/cluster/channels` and
`internal/app`; targeted race, acceptance-policy units, E2E compile/opt-out and
vet passed. Named `go-format` and `flow-doc-contracts` checks passed. Existing
FLOW length advisories are retained to preserve current invariants rather than
rewrite unrelated navigation. No repository-wide test pass is claimed.

## Measurement protocol

Both variants run the same three-node cluster and page-100 fixture: 600 groups,
24 users, 200 groups/user, three 256-byte messages/group, 256 hash slots and 12
initial physical Slots. All fixture runtimes are safely evicted before reads.
The Linux ARM64 Docker environment has 10 vCPUs, a 6 GiB container limit, no CPU
quota, GOMAXPROCS=2 per server and 10 for the driver. Each comparison uses 48
driver workers with the existing bounded arrival queue.

Fixed offered loads are list 1200 QPS, sync 400 QPS and sync 420 QPS. Each records
three successive 60-second windows; bounded metrics scrapes/draining separate
the windows, so this is not one uninterrupted 180-second load. There is no
halving or retry-until-pass. Every refusal/drop remains a rejected window.
Unexpected failures, runtime activation or membership writes abort collection.
Complete collection does not mean all windows passed.

The driver additionally records scheduling/queue wait and HTTP-request time.
Their P99 values are not additive. Request time includes client connection wait,
network, server processing and response validation; a high driver-wait value
locates queued latency but does not prove driver CPU is the root cause.
Bounded per-node CPU/alloc profiles and five-second execution traces run in
separate phases excluded from capacity claims. Aggregate trace wait times include
idle/background goroutines and cannot be read as request latency.

```sh
WK_E2E_CONVERSATION_BACKPRESSURE=1 \
WK_E2E_CONVERSATION_BACKPRESSURE_REPORT=/out/VARIANT/report.json \
WK_E2E_BINARY=/out/VARIANT-instrumented GOWORK=off \
  go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSBackpressure$' -count=1 -timeout=18m -p=1 -v
```

Use Linux with process CPU metrics. `VARIANT` is `old` or `new`; `/out` maps to
`/tmp/conversation-backpressure`. Source and dependency mounts are read-only.

## Results

Each table row aggregates three 60-second unprofiled windows. P99 is the range
of the three window P99 values, not a pooled percentile. “Drops” means the bounded
driver arrival queue could not submit scheduled requests. HTTP refusals and drops
both fail acceptance, even when the diagnostic Go test completes successfully.

| Endpoint / offered QPS | Variant | HTTP refusals | Driver drops | Window P99 range | Passing windows |
| --- | --- | ---: | ---: | ---: | ---: |
| list / 1200 | old | 0 | 127 | 55.2–567.6 ms | 2/3 |
| list / 1200 | new | 4 | 0 | 34.7–293.3 ms | 2/3 |
| sync / 400 | old | 9 | 23 | 115.9–628.0 ms | 0/3 |
| sync / 400 | new | 0 | 0 | 68.2–96.5 ms | 3/3 |
| sync / 420 | old | 24 | 0 | 132.7–187.7 ms | 0/3 |
| sync / 420 | new | 4 | 0 | 85.9–166.3 ms | 2/3 |

All 41 HTTP refusals across the 18 windows have corresponding serving-node
admission rejections: old sync-400 9, old sync-420 24, new list-1200 4 and new
sync-420 4. The new list rejections were four heads batches on node 3. The new
sync-420 rejections were node 1 heads (2), node 1 recents (1), and node 2 heads
(1). Byte-budget failures, unexpected errors, runtime loads, active runtimes and
membership writes were zero throughout the unprofiled comparison. Every node
and kind had balanced accepted/completed batches and zero in-flight batches
after each window; the observed shared limit remained 16.

Time-integrated processing occupancy per node was 0.90–1.22 slots for old list
and 0.65–0.89 for new list; old sync-400 was 0.94–1.28 and new sync-400 was
0.66–0.82. These are 60-second averages derived from observed hold sums and omit
observer overhead, not maximum occupancy. Immediate rejects prove the shared
16-slot pool was full at particular admissions; low averages support transient
bursts rather than continuous saturation. Samples do not establish which
scheduler, RPC fanout or I/O event generated each burst.

Old list window 3 had no serving-node rejection but dropped 127 requests. Its
end-to-end P99 was 567.6 ms, driver scheduling/queue P99 521.3 ms and request-time
P99 61.8 ms. New list window 2 had both four admission refusals and elevated
driver wait (251.4 ms P99). Keeping these failure classes separate avoids treating
every overloaded window as a storage concurrency failure. Backend slowdowns can
also create driver queues; this is not proof the driver CPU alone is limiting.

Total server process CPU per successful request fell from 3.930 to 3.643 ms for
list-1200 (7.3%), 11.622 to 10.759 ms for sync-400 (7.4%), and 11.402 to 10.733 ms
for sync-420 (5.9%). CPU includes the entire measured server interval, including
failed requests and background work, divided by successful responses; it is not
isolated function time. At sync-400 the node/window heads hold means fell from
1.58–2.13 ms to 1.08–1.35 ms. This supports retaining iterator reuse, but the
remaining refusals prevent qualifying list-1200 or sync-420 as stable capacity.

In separate sync-400 node-1 CPU samples, cumulative `CountOrdinaryMessages` time
fell from 1.26 s / 12.21 s sampled CPU (10.32%) to 0.93 s / 11.58 s (8.03%). These
short profiled phases are excluded from latency/capacity acceptance. A focused
old-sync storage trace reported 89.29 ms aggregate synchronization delay over a
five-second capture: 77.47 ms mutex wait and 11.82 ms GC-start delay. That sample
does not support a dominant sustained storage-lock stall, and does not rule out
spikes outside the capture. Driver JSON validation is visible in the separate
CPU profile; validation was retained. Container counters showed no CPU throttling
or OOM events at the sampled checkpoint.

The normal eight-worker, unchanged-threshold release gate passed all 12 cases
on the new instrumented binary: 26,400 scheduled and successful requests, zero
errors/drops/runtime loads/membership writes, zero active runtimes, and maximum
window P99 59.18 ms. It includes page sizes 25/100/200 on single-node and three-node
clusters, and checks allocation ceilings. Profile SHA-256 is
`b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`;
the companion JSON retains the full gate receipt and raw-artifact hash.
Diagnostic 48-worker rates are not copied into release thresholds. This is a
local dirty-tree Linux ARM64 regression pass, not clean exact-tag publication
evidence.

```sh
WK_E2E_CONVERSATION_QPS=1 \
WK_E2E_CONVERSATION_QPS_REPORT=/out/release-gate.json \
WK_E2E_BINARY=/out/new-instrumented GOWORK=off \
  go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSReleaseGate$' -count=1 -timeout=12m -p=1 -v
```

## Decision

Keep the iterator optimization and the bounded metrics. No admission increase,
waiting queue, transparent retry or new scheduling policy is justified by this
single pair of runs. The remaining performance issue is not declared fixed.
Further work should correlate the already captured runnable/network waits with
request RPC fanout before selecting a targeted change. Repeat matched windows
with alternating variant order to distinguish a stable gain from local-machine
variance. The current evidence is sufficient to locate actual refusals at
admission, but insufficient to attribute every transient full-pool event to one
root cause.

## Delivery

The owned local Docker container `wk-conversation-pressure-20260912`
(`0848a0b3f18dfd3e5e5e4967702aec7f5043cd51aca988bf9b09d01fa0950775`)
was removed after checking that no server processes remained. Unrelated
containers and the existing build cache were preserved. Raw reports, binaries,
profiles and traces remain under `/tmp/conversation-backpressure`; the companion
JSON contains compact window results, attribution, the full gate receipt and
artifact hashes. No merge, push, cloud resources or release publication are
included.
