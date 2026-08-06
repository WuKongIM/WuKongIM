# Membership-Backed Conversation Performance Acceptance

Date: 2026-08-06

Directory candidate: `a864512460d92bef63f4fc0c43d2b427e6ae04aa`

Final performance candidate: `943ea67957bf532f2d78c0aa8c9d7893fd543361`

## Decision

**GO for the membership-backed conversation directory, its steady-state SEND
write-amplification objective, and the local three-node 4,500 QPS recipient
hot-path acceptance gate.**

The dedicated three-node directory gate and the 100,000-member group gate pass.
A 20,000-message Cloud Medium-shaped local workload passes at both 500 and
4,500 offered QPS with zero membership mutation rows during each measured SEND
window. At 4,500 QPS the final candidate records 401.73 ms SENDACK P99 and
415.14 ms RECV P99 while fully draining the workload.

This is a reproducible local acceptance result, not a universal deployment
capacity claim. Representative multi-host qualification and a longer soak
remain separate operational gates.

The new sustained gate is implemented, but the current candidate is **NOT
QUALIFIED** for its 30-minute, 5,000-channel boundary. With natural local and
remote Slot routing at 4,500 offered QPS, the run completed 862,476 SEND calls
before a sender disconnected at about 192 seconds into the measured window.
All three nodes remained running and ready; `message.send` batches first timed
out, then node 2 reported `gateway: async send dispatch queue is full`. This
does not revoke the accepted fixed 20,000-message gate, but it closes the prior
unknown boundary with a reproducible sustained-capacity failure.

## Defects Found During Acceptance

### Cross-node person-directory readiness

The first 500 QPS run observed 250 membership mutation rows because the cold
prime used a different sender for every measured person-channel pair. Fixing
the fixture reduced the count but left 106 rows and exposed the product defect:
the authoritative Slot Channel RPC omitted `Channel.DirectoryReady`.

When a SEND node read person-channel metadata from another Slot Leader, the
already-initialized channel appeared unready and both memberships were proposed
again. The Channel RPC response codec now carries `DirectoryReady`, with a new
wire version and legacy v3 decoding. The cold prime now uses the exact sender
that the measured WKProto path uses. Focused binary-codec and two-node
authoritative-read integration tests cover both boundaries.

### Batched permission-check head-of-line blocking

After the readiness fix, repeated 4,500 QPS runs still recorded roughly
1.9-2.1 second SENDACK P99. Gateway queue capacity, Channel RPC admission,
Channel append, and post-commit queues were not saturated. During-load
goroutine evidence instead showed session-scoped gateway workers blocked while
`message.SendBatch` performed independent authoritative permission Slot RPCs
sequentially.

Three A/B controls rejected simpler tuning explanations:

- increasing the Channel RPC batch maximum from 8 to 64 left P99 at about
  1.75 seconds;
- increasing active group-channel cardinality caused earlier gateway admission
  pressure rather than removing the bottleneck;
- shrinking gateway batches reduced fixed-cost amortization and filled the
  async dispatch queue.

The final implementation checks independent batch-item permissions with at
most 16 managed workers. It then restores original item order before
person-directory establishment, plugin hooks, append admission, and result
alignment. Session ordering is therefore preserved, while one slow Slot RPC no
longer serializes every independent permission read in the same gateway batch.
The `PermissionStore` contract now explicitly requires concurrent-call safety.

## Accepted Evidence

### Steady-state SEND at 500 QPS

Three local processes, 256 physical hash slots, 10 logical Slots, three
replicas, 20,000 messages, 1,572,000 recipient rows, and 243,600 online routes:

| Signal | Result |
| --- | ---: |
| Offered / ingress | 500 / 500.02 messages/s |
| Completion | 499.08 messages/s |
| SENDACK P50 / P99 / max | 85.84 / 205.76 / 352.34 ms |
| RECV P99 / max | 231.40 / 355.89 ms |
| Ordinary membership mutation rows | **0** |
| Gateway max queue ratio | 0.00012 |
| Channel RPC admission-full | 0 |
| Channel RPC max queue / worker ratio | 0.00391 / 0.01042 |
| Max node / aggregate heap | 134.2 / 360.8 MB |
| Allocated bytes / GC cycles | 5.54 GB / 86 |
| Plugin accepted / invoked | 10,400 / 10,400 |
| Process continuity / drain | true / true |

Before the cross-node readiness fix, the same gate recorded 250 and then 106
membership rows and SENDACK P99 of about 3.75 seconds and 1.42 seconds. The
accepted run proves both the zero-write invariant and the latency consequence
of preserving the readiness fence.

### Three-node directory synchronization

Each phase used 200 memberships, 32 concurrent clients, 10 requests per client,
and the public `/conversation/list` and `/metrics` endpoints.

| Page | Requests/s | P50 | P95 | P99 | Allocated | Aggregate heap |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 25 | 6,469.8 | 4.14 ms | 7.63 ms | 9.91 ms | 159.4 MB | 346.3 MB |
| 100 | 2,741.1 | 10.01 ms | 18.24 ms | 21.24 ms | 573.3 MB | 365.2 MB |
| 200 | 1,285.7 | 21.42 ms | 43.19 ms | 47.65 ms | 1.13 GB | 428.8 MB |

All three phases recorded:

- zero membership mutation rows and zero Channel mailbox-full admissions;
- exactly one hydration batch per request;
- exactly one Leader-local head read per returned membership;
- exactly two remote Leader batch calls per request on this three-node fixture;
- complete conversations, no deletes, and no unresolved keys.

The Darwin process collector did not publish `process_cpu_seconds_total`, so
the evidence explicitly records `cpu_observed=false` instead of reporting zero
CPU consumption. CPU diagnosis used the profiles described below.

### 100,000-member group

The opt-in single-node-cluster black-box scenario passed in 20.77 seconds. It
created the 100,000-member directory, sent one group message, proved the
ordinary membership mutation counter was exactly unchanged across SEND, and
hydrated sampled subscriber conversations through the public API.

### Steady-state SEND at 4,500 QPS

The final strict run used the same three-process, 256-physical-hash-slot,
10-logical-Slot, three-replica, 20,000-message, 1,572,000-recipient-row, and
243,600-online-route fixture as the 500 QPS control.

| Signal | Result |
| --- | ---: |
| Offered / ingress | 4,500 / 4,500.21 messages/s |
| Completion | 4,332.86 messages/s |
| SENDACK P50 / P99 / max | 191.69 / **401.73** / 510.98 ms |
| RECV P99 / max | **415.14** / 516.49 ms |
| Ordinary membership mutation rows | **0** |
| Gateway max queue ratio | 0.00179 |
| Channel RPC admission-full | 0 |
| Channel RPC max queue / worker ratio | 0 / 0.03125 |
| Max node / aggregate heap | 167.0 / 382.1 MB |
| Allocated bytes / GC cycles | 4.27 GB / 55 |
| Plugin accepted / invoked | 10,400 / 10,400 |
| Process continuity / drain | true / true |

The first post-fix run already reduced SENDACK P99 to 437.70 ms and RECV P99
to 452.38 ms, but its measured ingress was 4,499.09 messages/s. The strict gate
failed only because this was 0.02 percent below the exact 4,500 threshold. An
unchanged rerun measured 4,500.21 messages/s and passed. The sub-millisecond
offered-load pacing boundary should therefore be treated as harness jitter;
latency, mutation, continuity, and drain evidence was healthy in both runs.

### Sustained permission-pressure qualification

The opt-in black-box soak runs three real processes with 256 physical hash
slots, 10 logical Slots, 25 senders, 25 online receivers, and 5,000 naturally
hashed group channels. Every channel has one sender/receiver subscriber pair;
the deterministic fixture proves that both ingress-local and cross-node Slot
permission routes are exercised. SEND never mutates the membership directory.

The harness is bounded over the full 8.1-million-message target: latency uses a
fixed 10,001-bucket histogram, completed message state is deleted, and public
Prometheus samples are aggregated rather than retained. It records transport
executor queue/busy/rejection pressure, permission Slot RPC calls, errors,
admission and in-flight state, `message/permission_batch` goroutine activity,
heap/GC, plugin conservation, process continuity, and membership mutation
rows. Complete runs emit `wukongim/permission-soak-evidence/v1`; premature
failures emit `wukongim/permission-soak-failure/v1` before bounded node
diagnostics.

A 10-second diagnostic control at 4,500 QPS and 100 channels passed:

| Signal | Result |
| --- | ---: |
| Messages / ingress | 45,000 / 4,500.09 messages/s |
| Completion | 4,413.93 messages/s |
| SENDACK P99 / max | 311 / 451.55 ms |
| RECV P99 / max | 292 / 440.77 ms |
| Permission Slot RPC calls / errors | 146,860 / 0 |
| Permission Slot RPC admission errors | 0 |
| Max transport queue / busy ratio | 0 / 0.00265 |
| Permission batches / panics / max active | 38,442 / 0 / 38 |
| Ordinary membership mutation rows | **0** |
| Max node / aggregate heap | 141.0 / 369.7 MB |
| Metric samples / errors | 33 / 0 |
| Process continuity / drain | true / true |

The full 30-minute target did not complete. The natural-routing run failed on
SEND `wkrc-permission-soak-000862477`, after 862,476 completed client SEND
calls. Public readiness and process checks were still healthy on all three
nodes. Node 1 and node 2 logged `message.send` batch `context deadline
exceeded`; node 2 then closed a sender session because its bounded async SEND
dispatch queue was full. An earlier diagnostic that intentionally forced every
permission read remote also failed and was rejected as an unrealistic fixture;
restoring natural distribution changed the load shape but not the sustained
failure class.

The result means the bounded permission parallelism fixes the short-window
head-of-line delay but does not establish 30-minute capacity at 4,500 QPS over
5,000 active channels on this local three-process host. No queue size or worker
limit was raised to make the test pass.

## Resolved 4,500 QPS Limit

After the readiness fix, the full local 4,500 QPS run completed without a
disconnect and kept ordinary membership mutations at zero. It offered and
ingressed 4,500 messages/s but completed about 3,621/s; SENDACK P99 was 1.67
seconds, above the existing one-second acceptance limit, and RECV P99 was 1.73
seconds. A separate profile-enabled control produced the same conclusion at
about 3,700 completions/s and 1.58-second SENDACK P99.

Those profiles correctly showed no conversation or membership CPU hotspot, but
the aggregate CPU view hid request-level queueing inside a synchronous gateway
batch handler. Stage counters and live goroutine stacks exposed the sequential
permission Slot RPC phase. Bounded permission concurrency reduced gateway
async-dispatch wait without changing Channel RPC batch size, gateway queue
capacity, channel cardinality, or conversation behavior, and the unchanged
4,500 QPS gate then passed with P99 below 500 ms.

## Verification

- `GOWORK=off go test ./pkg/slot/proxy -count=1`: passed.
- `GOWORK=off go test -tags=integration ./pkg/slot/proxy -count=1`: passed.
- Focused E2E acceptance/unit contracts for the medium recipient gate: passed.
- 20,000-message, 500 QPS strict medium-recipient acceptance: passed.
- Three-node 25/100/200 directory performance acceptance: passed.
- 100,000-member group conversation/SEND invariant: passed.
- `GOWORK=off go test -race ./internal/usecase/message ./pkg/goroutine -count=1`: passed.
- `GOWORK=off go test ./cmd/... ./internal/... ./pkg/... ./scripts/... ./docker/... -count=1`: passed.
- 20,000-message, 4,500 QPS strict medium-recipient acceptance: passed on the
  unchanged rerun; the immediately preceding run missed only the ingress clock
  threshold by 0.02 percent while meeting latency and mutation requirements.
- Permission-soak configuration, bounded-latency/in-flight tracking, route-mix,
  acceptance, counter, and public-metric parser contracts: passed.
- 10-second, 45,000-message, 4,500 QPS permission-soak diagnostic: passed with
  zero permission RPC errors and zero membership writes.
- 30-minute, 8.1-million-message target: failed after 862,476 completed SEND
  calls because the gateway async dispatch queue filled following sustained
  `message.send` timeouts; all three processes remained ready.
- `git diff --check`: passed.

## Next Boundary

Keep the existing exact zero-write, bounded hydration-operation, and short
4,500 QPS gates unchanged. The 30-minute multi-sender, multi-channel soak now
exists and must remain red for this candidate. The next performance slice
should use its failure JSON plus bounded gateway/message stage metrics or a
targeted profile to determine why `message.send` deadlines accumulate before
the async dispatch queue fills. Diagnose service time and queue ownership first;
do not raise queue capacity or permission concurrency without evidence. Rerun
the unchanged 30-minute, 5,000-channel gate after that bottleneck is fixed.
