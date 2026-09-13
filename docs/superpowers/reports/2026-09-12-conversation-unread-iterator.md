# Conversation unread-count iterator reuse

2026-09-12. Local dirty-worktree diagnostics; not release publication evidence.

## Change and correctness

`CountOrdinaryMessages` previously opened two independent sparse-index iterators,
one for each boundary. It now opens one bounded iterator under the existing
channel append ownership and repositions it for both ranks. If the first lookup
and fallback prove that the index is empty, the range contains only ordinary
positions and needs no second lookup. The rank helper also reuses the encoded
prefix when building seek keys.

The retained first ordinal remains the baseline after prefix deletion. Index
marker/backfill, internal SyncOnce filtering, caller-selected visibility/read
floor and frontier, corruption checks, cancellation and failure propagation
remain in place. The existing append-side rank query uses the same rank helper.
There is no schema change, cross-request unread cache, Channel runtime activation,
new fanout or admission-limit increase. Persisted conversation visibility and
history's committed visibility continue to be selected by their callers.

## Feedback loop and unit validation

The real storage-path allocation regression was run before the production edit:

```sh
GOWORK=off go test ./pkg/db/message \
  -run '^TestOrdinaryCountWarmAllocationBudget$' -count=1
```

It failed at 46 allocations per warm ordinary count against a budget of 32.
The same test passes after the change. New semantic coverage compares every
valid range against ordinary rows across incremental prefix retention, including
all-ordinary, all-internal and mixed indexes. It also covers retained ordinals
above one at the maximum sequence, malformed keys/values, zero/decreasing or
impossible ordinals, a warmed then unavailable store, and readers sharing a fixed
frontier with concurrent appends. Existing tests cover lazy bounded backfill,
reopen, suffix replacement/truncation, aborted writes, cancellation, portable
restore and history-size-independent warm reads.

Full unit packages passed: `pkg/db/message`, `pkg/cluster/channels`,
`internal/usecase/conversation`, `internal/usecase/message`,
`internal/infra/cluster`, and `internal/app`. Targeted unread-count race tests,
release-gate acceptance-policy units, Linux unread-count units, E2E vet, and the named `go-format` and
`flow-doc-contracts` checks passed. FLOW navigation was updated and regenerated.
Existing FLOW length advisories are retained to preserve documented invariants;
this change adds only the iterator-ownership contract. No repository-wide test
pass is claimed. The unavailable-store unit closes the
engine; it does not claim physical disk-fault injection.

## Local storage microbenchmark

Three one-second samples per case, Apple M4/Darwin ARM64, Go 1.25.0. Both versions
read the same 10,000-row fixture with the complete sparse index warm; setup is
outside timing. The mixed fixture marks every third record SyncOnce.

```sh
GOWORK=off go test -tags=integration ./pkg/db/message -run '^$' \
  -bench '^BenchmarkOrdinaryCount$' -benchmem -count=3 -benchtime=1s
```

| Index | Median ns/count, before → after | Allocations/count | Bytes/count |
| --- | ---: | ---: | ---: |
| Empty (ordinary records only) | 2,302 → 1,366 | 46 → 21 | 981 → 466 |
| Mixed records | 2,318 → 1,565 | 52 → 27 | 1,077 → 603 |

This measures about 41%/32% less local count time and 54%/48% fewer allocations.
It is not an equivalent endpoint QPS gain or a cross-platform capacity comparison.

## Cluster measurement protocol

The unchanged release profile covers 12 endpoint/page/topology cases. The same
binary subsequently runs the existing four capacity staircases with independent
180-second confirmations. The diagnostic-only
`WK_E2E_CONVERSATION_CAPACITY_FINE_SYNC=1` additionally tests three-node page-100
sync at 400, 420 and 440 QPS for ten seconds each. Passing candidates are confirmed
from highest to lowest for 180 seconds; rejected attempts remain in `fine_attempts`.
Only a passing sustained attempt sets `fine_confirmed_qps`; the original confirmed
baseline and its attempts remain separate. The release thresholds do not change.

Fixture and environment match the preceding metadata-batching report: 600 groups,
24 users, 200 groups/user, three 256-byte messages/group, 256 hash slots, 12 initial
physical Slots, warm disk caches, read-only measurement after safe runtime eviction,
round-robin ingress, bounded queues and exact response validation. Local Docker
Desktop Linux ARM64 has 10 vCPUs; the container has a 6 GiB memory limit, no CPU
quota, GOMAXPROCS=2/server and 10/driver. It uses `golang:1.25.11-bookworm`.
Public metrics must show zero runtime loads/active runtimes/membership writes;
all gate allocation, error, drop, completion and P99 criteria remain unchanged.

```sh
WK_E2E_CONVERSATION_QPS=1 \
WK_E2E_CONVERSATION_CAPACITY_WITH_GATE=1 \
WK_E2E_CONVERSATION_CAPACITY_LONG_CONFIRM=1 \
WK_E2E_CONVERSATION_CAPACITY_FINE_SYNC=1 \
WK_E2E_CONVERSATION_QPS_REPORT=/out/gate-capacity.json \
WK_E2E_BINARY=/out/wukongim-linux GOWORK=off \
  go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSReleaseGate$' -count=1 -timeout=45m -p=1 -v
```

`/out` maps to `/tmp/conversation-unread-iterator` on the host. Production sources
are mounted read-only. The build uses the existing host dependency cache read-only;
a harmless Go cache-stat write warning is preserved in the build log. An earlier
network-dependent build was stopped before testing and its exact container removed.

## Cluster results

The combined run completed in 1,915.9 seconds. **All 12 fixed gate cases passed**
with 26,400 successful requests, zero errors/drops/activation/membership writes,
and unchanged allocation ceilings. All four original capacity cases obtained a
passing three-minute lower bound, but three-node list had to halve its offered
rate after a rejected long confirmation. Overall Go PASS does not erase rejected
capacity attempts.

| Nodes | Endpoint, page 100 | Original staircase's confirmed QPS | Actual QPS | P99 ms |
| ---: | --- | ---: | ---: | ---: |
| 1 | list | 800 | 800.0 | 19.8 |
| 1 | sync | 240 | 240.0 | 22.0 |
| 3 | list | 600 | 600.0 | 16.2 |
| 3 | sync | 360 | 359.9 | 50.4 |

Three-node list's **1200 QPS / 180-second** attempt had **one HTTP 503**
`channel: backpressured` response among 216,000 scheduled requests. P99 was
36.3 ms and there were no queue drops, but the zero-error rule correctly rejected
it. The fallback 600 QPS confirmation passed. Intermediate 800/1000 QPS long
confirmations were not run; the halving result is **not evidence of a 50% capacity
regression**. The preceding metadata-batching diagnosis also had rejected
three-node high-load windows; these observations do not attribute the rare refusal
to this iterator change. No CPU-quota throttle, memory-limit event or OOM was
observed in this test cgroup. The cause of the rare refusal remains unproven.

The additional three-node sync refinement produced:

| Offered QPS | Duration | Actual QPS | P99 ms | Refusals | Drops | Verdict |
| ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 400 | 10 s | 398.6 | 59.3 | 0 | 0 | pass |
| 420 | 10 s | 418.4 | 112.0 | 0 | 0 | pass |
| 440 | 10 s | 434.3 | 208.9 | 4 | 0 | rejected |
| 420 | 180 s | 419.7 | 108.9 | 3 | 0 | rejected |
| **400** | **180 s** | **399.95** | **71.4** | **0** | **0** | **pass** |

The refused sync responses were the existing recognized HTTP 400 backpressure
head/recent-message envelopes. The original 480 QPS short probe also had 12
refusals and was rejected. Every attempt retained zero unexpected errors, runtime
loads, active runtimes and membership writes. **400 QPS is a newly verified local
lower bound**, not proof that this code change raised QPS by 11%: the previous
binary was not tested on the same fine grid. The driver uses 48 workers for
three-node capacity diagnosis versus eight in the fixed publication gate; this
result cannot be copied into release thresholds without corresponding validation.
No gate threshold or product admission limit was raised.

## Equal-load resource observations

The prior metadata-batching binary and this binary used the same fixture and
Linux environment. The following comparisons use successful 180-second windows
at the same offered rate; process CPU includes background server work.

| Nodes | Endpoint | Offered QPS | CPU ms/success, before → after | Bytes/success, before → after |
| ---: | --- | ---: | ---: | ---: |
| 1 | list | 800 | 1.981 → 1.809 | 1,451,164 → 1,400,150 |
| 1 | sync | 240 | 5.885 → 5.507 | 5,644,694 → 5,542,388 |
| 3 | sync | 360 | 10.754 → 10.449 | 8,296,088 → 8,194,762 |

These windows show about 8.7%, 6.4% and 2.8% lower CPU per successful request,
respectively, and 3.5%, 1.8% and 1.2% fewer allocated bytes. They are one pair of
local runs, not a production improvement guarantee. The 15-second fixed gate
shows three-node CPU/P99 largely unchanged; successful 360-QPS sync P99 was
48.0 ms before and 50.4 ms after. Three-node list has no successful equal-load
long comparison because its new 1200-QPS confirmation had a refusal; its
600-QPS fallback must not be compared to the old 1200-QPS result as a gain.

The companion JSON preserves the full 12-case gate, all rejected/confirmed
capacity attempts, finer probes, prior-binary identity and per-case comparisons.

## Identity and delivery

Source base: `13f9687192c316d3c6fa967c0f8ae593d60005e5`, source_dirty=true.

Binary SHA-256: `8a7a2a806dabee28bdcd1b7b814e076c415291fe0b7615c068e75fc318c3b51d`.

Profile SHA-256: `b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`.

Both exact owned containers were stopped and removed. No server processes
remained after the harness cleanup; existing Docker resources were preserved.
Raw logs, source/binary identities, baseline samples and the complete result JSON
remain available locally. No merge, push, cloud provisioning or release
publication is included.
