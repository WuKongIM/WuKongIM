# Conversation sync metadata batching

2026-09-12. Local dirty-worktree diagnostics; not release publication evidence.

## Change

The preceding Linux diagnosis found about 67 remote membership and 66 remote
channel-state point calls per page-100 `/conversation/sync` request in the
three-node fixture. Message preparation now batches those facts before reading
recent messages:

- One exact-key ordinary-membership request to the current UID Slot leader,
  limited to 200 requested channel keys. The additive operation uses the existing
  membership RPC service and rejects invalid/truncated/oversized requests. Found
  rows are unique; absent memberships remain explicit. Old peers reject the new
  operation rather than serving stale local facts or silently bypassing authority.
- Terminal channel state reuses the existing authoritative permission-metadata
  batch service, grouped by physical Slot with at most four workers. It bypasses
  the SEND permission cache. No new service ID or cross-request result cache.
- The message usecase keeps normalization, visibility floors, tombstone and
  disband policy shared between single and batch pulls. Batch results retain
  input alignment and failures are resolved before any message read. A new
  person conversation without membership still returns empty without a read;
  missing group membership still fails. Channel metadata not-found keeps the
  previous policy, while node/disk failures propagate.
- The adapters validate returned identities/cardinality and never downgrade
  transport failures to missing rows. Optional point-only implementations retain
  compatible authoritative reads; production Node exposes both batch ports.
- The existing persisted conversation path, history's committed path, response
  limits, serving-node admission, membership writes and runtime activation rules
  are unchanged. No unread-index change is mixed into this experiment.

## Validation

The new usecase regression was run before implementation and failed with
`point membership read`, proving that the real batch call site still performed
point reads. It passes with the batch path. Tests cover missing group/person
membership, tombstones, disband, disk failures, short results, visibility/cursor
preservation, maximum floors and input-order error precedence.

The proxy tests exercise remote and local UID authority, exact sparse rows,
duplicate request keys, visibility-marker encoding, one RPC for a batch,
cancellation, unavailable nodes, read failures, unexpected response statuses,
codec truncation/bounds and mismatched/duplicate returned identities. The app
composition test connects the real usecase, metadata adapters, persisted page
reader and legacy conversation adapter, verifying both batch ports and no
committed fallback or membership writes.

Full unit packages passed: `internal/usecase/message`, `internal/infra/cluster`,
`pkg/slot/proxy`, `pkg/cluster`, and `internal/app`. Targeted race tests passed.
The E2E diagnostic compile/opt-out and named `go-format`/`flow-doc-contracts`
checks passed. The FLOW index was regenerated. Advisory FLOW length
warnings are retained to preserve existing invariants and the new batch contract;
no unrelated module rewrite was made.

## Measurement protocol

The equal-load test uses the preceding Linux report's page-100 offered rates:
single-node list/sync 800/240 QPS and three-node list/sync 1200/360 QPS, each in
three successive 60-second windows. The new diagnostic-only
`WK_E2E_CONVERSATION_DIAGNOSIS_FIXED_BASELINE=1` marks the receipt and skips
capacity discovery; its rate fields are comparison loads, not newly discovered
capacity. The first window may include cache warming that previously occurred
in capacity probes. CPU/allocations and Go traces are separate profiled phases.

The unchanged release gate and optional short capacity staircase are evaluated
separately. Warm-cache read-only traffic, exact persisted-message validation,
zero errors/drops/activation/membership writes, and all existing QPS/P99/allocation
criteria apply. Driver and servers share the local Docker Desktop Linux ARM64
VM (10 vCPUs, about 8 GiB RAM), with container limit 6 GiB, no CPU quota,
GOMAXPROCS=2 per server and the same fixture: 600 groups, 24 users, 200 groups/user,
three 256-byte messages/group, 256 hash slots and 12 initial physical Slots.

## Equal-load observations (completed)

The fixed-load diagnostic completed all four cases in 1,020.9 seconds. It is
**not an all-windows pass**: both three-node cases had one rejected window.

| Nodes | Endpoint | Offered QPS | P99 ms, minutes 1 / 2 / 3 | Window verdicts |
| ---: | --- | ---: | --- | --- |
| 1 | list | 800 | 33.4 / 217.7 / 22.1 | pass / pass / pass |
| 1 | sync | 240 | 61.6 / 27.0 / 23.6 | pass / pass / pass |
| 3 | list | 1200 | 1715.5 / 64.8 / 47.6 | rejected / pass / pass |
| 3 | sync | 360 | 70.9 / 1155.1 / 90.0 | pass / rejected / pass |

The rejected list window had 24 recognized HTTP 503 refusals and 4,873 queue
drops; the rejected sync window had 59 recognized legacy HTTP 400 backpressure
responses and 1,018 queue drops. No unexpected errors, membership writes or
runtime loads occurred. The separate three-node sync CPU profile workload also
had 11 recognized refusals and is not a passing throughput measurement.

Passing three-node sync windows show the following server RPCs per successful
request. These are served calls, not network RTTs; background Raft RPCs are excluded.

| Service | Before | After |
| --- | ---: | ---: |
| User membership (includes directory lookup) | 67.333 | 1.333 |
| Point channel metadata | 66.208 | 0 |
| Permission metadata batch | 8 | 16 |
| Runtime metadata batch | 16 | 16 |
| Conversation heads | 2 | 2 |
| Recent message reads | 2 | 2 |
| **Total listed read RPCs** | **161.542** | **37.333** |

The ~77% call-count reduction is confirmed. CPU in passing three-node sync
windows was about 4.04 cores versus 4.69–4.77 previously, but rejected windows
prevent treating this as a stable throughput or P99 gain. The independent
five-second node-1 trace attributes 2.95 aggregate goroutine-seconds to batch
preparation, versus 150.2 under old point preparation. Fewer concurrent waiters
change that aggregate; this is **not** a 98% request-latency improvement.

At the pressure observation, the test cgroup had no CPU-quota throttling,
memory-limit event or OOM. Recorded per-node GC pauses summed to about
0.20–0.21 seconds during the rejected sync minute. These signals do not identify
the cause of the tail spikes. Skipped capacity warmup can affect the first
window but cannot by itself explain sync's second-window failure. Do not label
these failures as proven infrastructure issues or silently discard them.

## Fixed gate and sustained capacity confirmation (completed)

The subsequent combined run passed in 1,344.4 seconds. All 12 fixed gate cases
passed their unchanged QPS/P99/allocation floors: 26,400 successful requests,
zero errors/drops/runtime loads/active runtimes/membership writes. The four
capacity cases each passed a separate **180-second** confirmation after bounded
warmup/staircase probes. Earlier rejected diagnostic windows remain above and in
the JSON; they are not overwritten by these later passes.

| Nodes | Endpoint, page 100 | Confirmed offered QPS | Actual QPS | P99 ms | CPU ms/success, previous diagnosis → new confirmation |
| ---: | --- | ---: | ---: | ---: | ---: |
| 1 | `/conversation/list` | 800 | 800.0 | 22.9 | 1.96 → 1.98 |
| 1 | `/conversation/sync` | 240 | 240.0 | 24.8 | 5.79 → 5.89 |
| 3 | `/conversation/list` | 1200 | 1199.9 | 37.6 | 3.70 → 3.74 |
| 3 | `/conversation/sync` | 360 | 359.9 | 48.0 | 13.10 → 10.75 |

The confirmed offered rates remain 800/240 QPS for single-node list/sync and
1200/360 for three-node list/sync. In the new three-node sync staircase, 480 QPS
was rejected (actual 456.7 QPS, P99 522.3 ms); 360 passed the three-minute
confirmation. The previous staircase also confirmed 360. This coarse grid does
not establish the exact peak or a stable QPS increase. Finer probes may locate
a higher passing rate, but have not been run; no release thresholds were raised.

CPU values are whole-server-process totals per successful request, including
background work. They compare equal offered rates, identical fixture/profile
and 180 seconds of successful measurement on the same local Linux environment,
but different runs/warmup sequences. The passing long confirmation shows lower
sync CPU, while the earlier rejected windows prevent a broad tail-latency
reliability claim. Keep the warm-cache/read-only and shared-VM limitations.

## Reproduction and identities

```sh
WK_E2E_CONVERSATION_DIAGNOSIS=1 \
WK_E2E_CONVERSATION_DIAGNOSIS_FIXED_BASELINE=1 \
WK_E2E_CONVERSATION_DIAGNOSIS_REPORT=/tmp/conversation-sync-batch/diagnosis.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSDiagnosis$' -count=1 -timeout=35m -p=1 -v

WK_E2E_CONVERSATION_QPS=1 \
WK_E2E_CONVERSATION_CAPACITY_WITH_GATE=1 \
WK_E2E_CONVERSATION_CAPACITY_LONG_CONFIRM=1 \
WK_E2E_CONVERSATION_QPS_REPORT=/tmp/conversation-sync-batch/gate-capacity.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSReleaseGate$' -count=1 -timeout=40m -p=1 -v
```

Both recorded runs used `WK_E2E_BINARY=/out/wukongim-linux` inside the isolated
Linux container. `/out` maps to `/tmp/conversation-sync-batch` on the host.
The exact owned container was stopped and removed after measurement; existing
Docker resources were preserved. CPU/alloc/trace artifacts and hashes remain
locally. No cloud resources, merge, push or publication were used.

Source base: `13f9687192c316d3c6fa967c0f8ae593d60005e5`, source_dirty=true.

Binary SHA-256: `31fb78e75adb3c85e2230a243b02f68c6235aa61185f4cd575e9896518dc1c8d`.

Profile SHA-256: `b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`.


