# Membership-Backed Conversation Performance Acceptance

Date: 2026-08-06

Base: `a864512460d92bef63f4fc0c43d2b427e6ae04aa`

## Decision

**GO for the membership-backed conversation directory and its steady-state
SEND write-amplification objective.**

The dedicated three-node directory gate and the 100,000-member group gate pass.
A 20,000-message, 500 QPS Cloud Medium-shaped local workload also passes with
zero membership mutation rows during the measured SEND window. The same local
host does not yet pass the broader 4,500 QPS recipient hot-path latency gate;
the evidence points to network/delivery scheduling work outside the
conversation directory, not conversation DB fanout.

## Defect Found During Acceptance

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

## Remaining 4,500 QPS Limit

After the readiness fix, the full local 4,500 QPS run completed without a
disconnect and kept ordinary membership mutations at zero. It offered and
ingressed 4,500 messages/s but completed about 3,621/s; SENDACK P99 was 1.67
seconds, above the existing one-second acceptance limit, and RECV P99 was 1.73
seconds. A separate profile-enabled control produced the same conclusion at
about 3,700 completions/s and 1.58-second SENDACK P99.

This workload also represents roughly 353,000 recipient rows/s on one host
running all three nodes and all clients. Gateway, Channel RPC, append, and
post-commit queues were far below saturation. Concurrent two-second CPU
profiles were dominated by network `writev`, event-loop polling, and Go
scheduler wait/wakeup work, with no conversation or membership CPU hotspot.
Therefore this run remains a **NO-GO for claiming absolute 4,500 QPS Cloud
Medium capacity from this local host**, but it does not block the conversation
directory decision.

## Verification

- `GOWORK=off go test ./pkg/slot/proxy -count=1`: passed.
- `GOWORK=off go test -tags=integration ./pkg/slot/proxy -count=1`: passed.
- Focused E2E acceptance/unit contracts for the medium recipient gate: passed.
- 20,000-message, 500 QPS strict medium-recipient acceptance: passed.
- Three-node 25/100/200 directory performance acceptance: passed.
- 100,000-member group conversation/SEND invariant: passed.
- `git diff --check`: passed.

## Next Boundary

Treat the 4,500 QPS recipient path as a separate transport/delivery capacity
investigation. Reproduce it on representative multi-host resources or isolate
the network write/scheduler path with a same-host A/B benchmark before changing
queue sizes or latency thresholds. The conversation directory itself should
retain the exact zero-write and bounded hydration-operation gates added here.
