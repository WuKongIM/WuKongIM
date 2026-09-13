# Conversation read allocation optimization: second pass

Date: 2026-09-12. These are local dirty-worktree diagnostics, not release publication evidence.

## Changes

- Message storage shares immutable encoded key prefixes/catalog bytes across matching key/Channel identities through the existing warm cache. Mutable entries and independently closable leases are still recreated; stale leases cannot revive. The cache remains limited to 32,768 entries and now also evicts on a 16 MiB encoded-backing-array budget. This byte limit excludes cache struct/LRU overhead, which remains bounded by entry count; it is not a whole-process memory limit.
- The synchronous legacy conversation adapter transfers message-usecase-owned Payload/StreamData instead of cloning them again. The message usecase still detaches buffers from readers/event storage, and the conversation usecase still defensively clones at its port before reversing messages. No message or full response is cached across requests.
- Current-Leader disk reads, membership/visibility/retention checks, whole-request failure, runtime admission and response limits are unchanged.

## Isolated before/after measurements

Three microbenchmark samples each; numbers below describe the relevant component, not whole HTTP requests.

| Operation | Bytes/op before → after | Allocations/op before → after |
| --- | ---: | ---: |
| Warm Channel acquire/close | 2,928 → 1,568 | 36 → 5 |
| Legacy reader + adapter, 10 messages, 256 B each base and stream payload | 16,992 → 11,872 | 51 → 31 |
| Legacy reader + adapter, 10 messages, 4 KiB each base and stream payload | 170,593 → about 88,673 | 51 → 31 |

The key reuse regression first failed on rebuilding the backing prefix after a real lease close/reacquire. The ten-message reader regression first failed at 51 allocations against a 35-allocation budget; it passes after removing the adapter copies. Separate tests mutate one result and verify source buffers and a subsequent result stay isolated. Eviction, identity replacement, byte accounting and stale-lease fencing are covered.

## Equal-load release matrix

The baseline is the final combined run documented in [the preceding optimization report](2026-09-12-conversation-qps-optimization.md). Both runs use identical profile SHA, queue/worker bounds, endpoint/page/rate cases and fixture sizes; they are separate runs on the same local host. Timing variation is retained rather than treated as a guaranteed latency improvement.

| Nodes | Endpoint | Page | QPS | Alloc/request before → after (decimal MB) | Reduction | P99 before → after (ms) |
| ---: | --- | ---: | ---: | ---: | ---: | ---: |
| 1 | `/conversation/list` | 25 | 400 | 0.398 → 0.380 | 4.6% | 3.3 → 4.3 |
| 1 | `/conversation/list` | 100 | 200 | 1.536 → 1.462 | 4.8% | 5.9 → 7.7 |
| 1 | `/conversation/list` | 200 | 100 | 3.088 → 2.941 | 4.8% | 9.5 → 10.6 |
| 1 | `/conversation/sync` | 25 | 80 | 3.677 → 3.493 | 5.0% | 33.1 → 13.6 |
| 1 | `/conversation/sync` | 100 | 60 | 5.857 → 5.559 | 5.1% | 13.1 → 15.1 |
| 1 | `/conversation/sync` | 200 | 40 | 8.758 → 8.307 | 5.2% | 17.7 → 18.4 |
| 3 | `/conversation/list` | 25 | 400 | 0.635 → 0.617 | 2.9% | 7.9 → 7.2 |
| 3 | `/conversation/list` | 100 | 200 | 2.311 → 2.238 | 3.2% | 18.6 → 12.6 |
| 3 | `/conversation/list` | 200 | 100 | 4.565 → 4.418 | 3.2% | 23.7 → 17.3 |
| 3 | `/conversation/sync` | 25 | 80 | 5.575 → 5.389 | 3.3% | 31.1 → 20.1 |
| 3 | `/conversation/sync` | 100 | 60 | 8.939 → 8.641 | 3.3% | 51.0 → 31.1 |
| 3 | `/conversation/sync` | 200 | 40 | 13.503 → 13.044 | 3.4% | 46.6 → 37.6 |

All 26,400 scheduled gate requests succeeded. All 12 phases passed the unchanged QPS/P99/allocation limits, exact persisted-message validation, zero errors/drops, zero runtime activation and zero membership mutations.

## Capacity diagnostics

| Nodes | Endpoint (page 100) | Confirmed offered QPS | Observed rejected QPS | Confirmation P99 (ms) |
| ---: | --- | ---: | ---: | ---: |
| 1 | `/conversation/list` | 800 | 1200 | 78.0 |
| 1 | `/conversation/sync` | 240 | 360 | 79.5 |
| 3 | `/conversation/list` | 800 | 1200 | 28.6 |
| 3 | `/conversation/sync` | 360 | 480 | 235.6 |

These are 15-second confirmed lower bounds on the sampled grid, not exact maximum QPS or long-soak guarantees. A refused confirmation can cause the existing bounded search to halve the candidate, so a different confirmed rate between runs alone does not prove a throughput gain or regression. Refused stages remain in the JSON evidence and are not passing load tests.

## Two-stage sync read assessment

A direct reuse of first-stage heads is not sufficient to replace recent-message reads: recent sync separately prepares membership and terminal state, applies the client cursor and visibility floor, reads a bounded page with continuation and lookahead, and enriches stream-event state. The current conversation hydration port does not export a reusable authority-fenced message-page result. Eliminating that second stage safely would require a separate explicit contract and tests for changing Leader/retention/membership plus event-only updates. This pass preserves both stages.

## Validation and limits

- Full related unit packages passed: `pkg/db/message`, `pkg/channel/store`, `pkg/cluster/channels`, `internal/usecase/conversation`, `internal/usecase/message`. Focused app composition/legacy reader tests passed.
- Targeted storage-lifecycle and app payload-ownership race tests passed. Named `go-format` and `flow-doc-contracts` checks passed, along with `git diff --check`.
- Real single-node/three-node clusters, 256 hash slots, 600 groups, 24 readers, 200 groups per user and three 256-byte messages per group; GOMAXPROCS=2 per server. Warm disk caches and a read-only load with unloaded runtimes. Driver and servers share the local macOS arm64 host. No cloud, Linux release run, concurrent-write or 100k-member qualification.
- This pass uses existing allocation profiles to select changes and isolated microbenchmarks plus real HTTP load to verify them. No new CPU-profile claim is made; Darwin process CPU remains unavailable.
- Existing release thresholds were not relaxed or raised. No merge, push, workflow dispatch or publication was performed.

## Reproduction

```sh
GOWORK=off go test ./pkg/db/message -run "^$" -bench "^BenchmarkChannelWarmReacquire$" -benchmem -count=3
GOWORK=off go test ./internal/app -run "^TestConversationLegacyMessageReader" -bench "^BenchmarkConversationLegacyMessageReader$" -benchmem -count=3
WK_E2E_CONVERSATION_QPS=1 WK_E2E_CONVERSATION_CAPACITY_WITH_GATE=1 WK_E2E_CONVERSATION_QPS_REPORT=/tmp/conversation-qps-round2/combined.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run "^TestConversationQPSReleaseGate$" -count=1 -timeout=20m -p=1 -v
```

## Identities

Base source: `13f9687192c316d3c6fa967c0f8ae593d60005e5`, source_dirty=true.
Before server SHA-256: `3375745aa5eb9a91cd1efa512bf0f6b265f337f407e16a397868542350f98895`.
After server SHA-256: `74b12fee4737d9aa5f7b545f63fcb181227376cb15807fa3e1057135d3594301`.
Unchanged profile SHA-256: `b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`.
