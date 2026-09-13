# Conversation read optimization and release regression evidence

Date: 2026-09-12. Local diagnostic evidence from a dirty worktree, not signed release evidence.

## Result

Persisted conversation previews now read one tail record first. Only an internal recovery/SyncOnce suffix expands to 64-record reverse batches. The current-Leader disk boundary, visibility floors, whole-request failures, and zero-runtime-activation semantics remain intact.

At equal offered rates and a 100-conversation page in a single-node cluster:

| Endpoint | Offered QPS | Alloc/request before → after (decimal MB) | Allocation reduction | P99 before → after |
| --- | ---: | ---: | ---: | ---: |
| `/conversation/list` | 800 | 2.32 → 1.52 | 34.2% | 31.6 → 18.7 ms |
| `/conversation/sync` | 240 | 7.42 → 5.82 | 21.5% | 23.0 → 17.0 ms |

The equal-load comparisons above use the same worker-sized queue before and after the product change. Final capacity bounds below use the corrected latency-budget queue. These measurements establish lower allocation and tail latency, not a proportional increase in peak QPS. Increasing the release offered rates is a stronger regression policy, not a 2×/4× product throughput claim.

## Capacity bounds

| Nodes | Endpoint (100 conversations) | 15-second confirmed offered QPS | Nearest observed rejected QPS | P99 at confirmation |
| ---: | --- | ---: | ---: | ---: |
| 1 | `/conversation/list` | 800 | 1200 | 21.2 ms |
| 1 | `/conversation/sync` | 240 | 360 | 25.6 ms |
| 3 | `/conversation/list` | 600 | 1200 | 33.2 ms |
| 3 | `/conversation/sync` | 360 | 480 | 228.6 ms |

Bounds describe this test window and offered-rate grid, not an exact saturation point or a long-soak guarantee. The final three-node list confirmation at 1,200 QPS returned refusal errors, so the bounded search halved to 600 QPS. Earlier independent 800 QPS confirmations passed; 600 QPS is a conservative confirmed lower bound, not an asserted ceiling or a measured product regression. Rejected stages may contain queue drops or explicit refusal responses; they never count as passing capacity.

## Release gate

| Endpoint | Page size | Offered QPS | 1-node P99 | 3-node P99 | 1-node alloc/request | 3-node alloc/request |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| `/conversation/list` | 25 | 400 | 3.3 ms | 7.9 ms | 0.40 MB | 0.63 MB |
| `/conversation/list` | 100 | 200 | 5.9 ms | 18.6 ms | 1.54 MB | 2.31 MB |
| `/conversation/list` | 200 | 100 | 9.5 ms | 23.7 ms | 3.09 MB | 4.56 MB |
| `/conversation/sync` | 25 | 80 | 33.1 ms | 31.1 ms | 3.68 MB | 5.57 MB |
| `/conversation/sync` | 100 | 60 | 13.1 ms | 51.0 ms | 5.86 MB | 8.94 MB |
| `/conversation/sync` | 200 | 40 | 17.7 ms | 46.6 ms | 8.76 MB | 13.50 MB |

All 26,400 scheduled release-gate requests succeeded, with zero errors/drops, zero active runtimes before/after every phase, zero runtime loads, and zero membership writes. Every response was validated against exact persisted message identities.

Release floors increased from list 100/50/25 to 400/200/100 QPS and sync 40/30/20 to 80/60/40 QPS. Reviewed topology-specific allocated-byte ceilings now additionally fail regressions that a low-load QPS/P99 pass could miss. The list ceilings are below the observed old allocation costs. Complete JSON evidence, source/profile/binary binding, zero-error rules, and publication dependencies remain mandatory.

## Method and limits

- Apple M4, 10 logical CPUs, 32 GiB, macOS arm64; each real server process uses GOMAXPROCS=2. Driver and cluster share one host. This is not a Linux hosted-runner or production-capacity qualification.
- Real single-node/three-node clusters, 256 hash slots, 12 initial physical slots, 600 generated groups, 24 readers, 200 groups per reader, three 256-byte messages per group. This is a read-only workload without concurrent writes or 100,000-member group qualification.
- Generated runtimes are safely unloaded through the authenticated benchmark eviction API before reads. All roles must remain absent; disk caches are warm. No process recovery or eviction occurs during measured reads.
- Capacity probes use 16 driver workers per node, a bounded arrival queue, ten-second doubling probes, one midpoint, and fifteen-second confirmation. Final gate/capacity runs bound the arrival queue to max(workers, ceil(offered QPS × P99 budget in seconds)); earlier diagnostic receipts used worker-sized queues. Gate phases use eight workers and fifteen seconds. Queuing and full response validation count toward latency; late completions cannot inflate QPS. No measured request retries.
- CPU and allocation pprof capture runs separately from capacity confirmation. Node profiles and driver CPU profiles are separate. Darwin process CPU counters are unavailable and encoded as null; Linux release runs require them. CPU samples contain substantial syscall/runtime attribution, so this evidence does not establish GC as the dominant CPU bottleneck.
- Allocation pprof attributed approximately 37% of list allocation and 31% of sync allocation to reverse row reads (cumulative). The new regression test reproduced 64 materialized records for one ordinary preview before the change and one afterwards; a 130-record internal suffix still reaches the correct ordinary message.
- The initial all-page baseline diagnostic was deliberately stopped after single-node evidence was collected. Its three-node setup failure is an intentional stop, not a passing run. The first optimized capacity run completed both single-node cases and the three-node list case, then stopped on a legacy HTTP 400 backpressure envelope. The next attempt revealed that the first-error-only log hid a second envelope. A 200-channel reduction reproduced both head-read and recent-message backpressure wrappers; bounded error samples now retain distinct errors. Focused tests recognize only those two exact observed legacy envelopes alongside HTTP 503 refusals; unrelated HTTP/transport/semantic errors remain fatal. Both three-node capacity cases were rerun with the correction. The JSON preserves the original receipts without rewriting partial-run complete flags; the final capacity table comes from the fresh combined run.
- A first strengthened-gate attempt rejected 13 dropped arrivals on the three-node list-25 case despite zero HTTP failures and P99 around 16 ms. The eight-entry waiting queue represented only 20 ms of arrivals at 400 QPS, below the 500 ms latency budget. A deterministic 80 ms arrival-burst regression failed with that queue and passed with the bounded latency-budget queue. Queue delay remains measured, and worker/queue values are checked by the real Workflow filter. A fresh combined run revalidated all 12 gate phases and four capacity cases using the corrected queue.
- Profiling overhead is excluded from capacity claims. Raw local profile paths remain in the diagnostic receipts; the opt-in test regenerates them.

## Validation and identities

- Relevant conversation/message/cluster-adapter/metrics unit packages passed; targeted stored-head and persisted-read race checks passed.
- Acceptance-policy, observed-backpressure, and real Workflow jq rejection tests passed. Named go-format and flow-doc-contracts checks passed; E2E package vet and git diff --check passed.
- Full scripts previously exposed unrelated existing unmanaged-goroutine findings; broad docs checks previously exposed an existing Manager login catalog mismatch. Those were reproduced outside this worktree and are not claimed fixed here.
- No GitHub Workflow, cloud capacity test, release publication, merge or push was performed for this optimization. Native-package/release requirements are unchanged.

Before server SHA-256: `0b9c088b95d297931de32bad050d71e4dbec3a7dc802d6707fb97984c09eb6f8`.
After/gate server SHA-256: `3375745aa5eb9a91cd1efa512bf0f6b265f337f407e16a397868542350f98895`.
Final release profile SHA-256: `b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`.
Base source commit: `13f9687192c316d3c6fa967c0f8ae593d60005e5`; source_dirty=true.

Run the commands in `test/e2e/message/conversation_qps/AGENTS.md`. Final combined diagnostic command (CI runs the fixed gate without the capacity flag):

```sh
WK_E2E_CONVERSATION_QPS=1 WK_E2E_CONVERSATION_CAPACITY_WITH_GATE=1 WK_E2E_CONVERSATION_QPS_REPORT=/tmp/conversation-qps-combined.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run '^TestConversationQPSReleaseGate$' -count=1 -timeout=20m -p=1 -v
```
