# Conversation mixed-load and hidden-page validation

The corrected candidate passes the expanded local release gate: 12 base cases, seven stress windows, 20 endpoint results and 43,800 successful requests, with zero errors, drops, runtime activation or membership writes. At the separate 600-list/200-sync offered load for ten minutes, it records 60 errors among 480,000 scheduled requests; this is a rejected high-load window, not confirmed zero-error capacity.

Testing exposed and corrected a pagination bug in the prior prefix optimization. Durable Channel ID keys compare encoded length before bytes; legacy sync sorts by string value. For `b` and `aa`, the old prefix incorrectly selected `b` on page one. Sync now collects the same bounded 1,000-candidate metadata set, sorts it in legacy order, and only then hydrates the visible prefix. Canonical list retains its existing index/cursor order. No storage schema, admission limit or public endpoint was added.

Every attempted metadata, head or recent-message read failure still fails the whole response. Messages remain current-Leader persisted reads without Channel runtime activation. Directory-invisible candidates replenish the prefix; unread/type filters and empty recents still run after paging without replenishment.

## Mixed load: 600 list QPS and 200 sync QPS for 600 seconds

Page size 100, 24 workers per endpoint (48 total), a shared start, bounded scheduled arrivals, no measured retry, and all 24 users across three ingress nodes. CPU and allocations below belong to the whole shared window, not to each endpoint.

| Variant | Endpoint | Actual QPS | Errors | P99 ms |
| --- | --- | ---: | ---: | ---: |
| old | /conversation/list | 599.59 | 240 | 72.63 |
| old | /conversation/sync | 199.79 | 117 | 70.27 |
| new | /conversation/list | 599.70 | 178 | 51.66 |
| new | /conversation/sync | 199.89 | 63 | 58.36 |
| corrected | /conversation/list | 599.92 | 44 | 22.48 |
| corrected | /conversation/sync | 199.97 | 16 | 39.14 |

| Variant | Shared CPU ms / successful request | Allocated MB / successful request |
| --- | ---: | ---: |
| old | 5.208 | 3.569 |
| new | 4.487 | 3.086 |
| corrected | 4.580 | 3.141 |

`old` is the pre-prefix candidate; `new` is the superseded prefix candidate; `corrected` includes metadata sorting and stronger exact-page validation. These are chronological runs, not repeated counterbalanced capacity confirmations. The corrected harness does more identity checking, so do not attribute every cross-run difference solely to production code.

The old combined diagnostic stopped after its completed mixed window because 117 exact legacy HTTP 400 request-admission refusals were initially unrecognized. The raw report remains `complete=false`, with its original error counters. An observed-envelope regression test was added and passed; these refusals remain errors and the window still fails. The old hidden stage was then collected separately on a fresh cluster. The superseded new mixed harness did not check exact base-page IDs, so its numbers cannot qualify the corrected candidate.

## Hidden pages

Each window offers 20 QPS for 30 seconds with 48 workers. Public hide commands run outside measurement, followed by fixture runtime eviction. All six corrected windows validate exact ordered IDs, types and all recent message identities with zero read errors or activation. Page 1 uses size 100; page 2 uses size 50. The 90%-hidden second page is intentionally empty.

| Layout | Page | Old P99 ms | Superseded P99 ms | Corrected P99 ms | Corrected batches / request | Corrected RPC / request |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| hidden_50_percent | 1 | 64.93 | 144.17 | 137.15 | 8 | 106.00 |
| hidden_50_percent | 2 | 62.34 | 150.73 | 134.58 | 8 | 106.00 |
| hidden_90_percent | 1 | 60.79 | 90.68 | 89.99 | 3 | 65.33 |
| hidden_90_percent | 2 | 38.38 | 74.80 | 75.20 | 3 | 46.67 |
| hidden_after_99_visible | 1 | 67.50 | 82.88 | 399.91 | 101 | 237.33 |
| hidden_after_99_visible | 2 | 64.58 | 82.72 | 405.56 | 101 | 237.33 |

Hidden-page performance regresses relative to the full-batch baseline. In the true legacy-order 99-visible/100-hidden/one-visible layout, a prefix missing one visible item causes 101 hydration batches and about 237 RPCs per request. The superseded implementation traversed native length order instead and did not exercise this intended sequential gap. The next performance priority is reducing these small refill batches while preserving accurate paging and whole-request errors. The release threshold is not raised to hide this regression.

## Release gate v2

The original six endpoint/page cases on both single-node and three-node clusters remain unchanged. The three-node gate additionally requires:

- Simultaneous list 200 QPS and sync 60 QPS for 60 seconds, eight workers each.
- The six hidden page windows at 20 QPS for 15 seconds, eight workers each.
- Zero errors, drops, activation, residency or membership writes; at least 95% of offered QPS completed within the window; P99 at most 500 ms.
- Shared-window allocation ceilings of 5 MB/request for mixed traffic, 12 MB for 50% hidden, 7.5 MB for 90% hidden and 12 MB for the 99-visible gap. Mixed resources are counted once across both endpoints.

The local gate completed 43,800 requests with maximum P99 443.27 ms. All 20 endpoint results passed. The gate uses conservative offered rates below the refused high-load diagnostic; the six new hidden floors were exercised at the same offered rate before gate validation. The allocation ceilings retain headroom over observed complete-window allocation costs; unchanged base-case ceilings still apply.

The reusable Workflow now has a 24-minute job deadline and an 18-minute test deadline. Its v2 receipt filter requires all 12 base cases and the exact seven stress windows, both mixed endpoints, complete identity binding, correct rates/workers/durations, and valid metrics. Unit contracts reject missing windows, duplicate pages, missing endpoints, forged pass flags, errors and resource misattribution. Both existing publishers depend on this gate. No Workflow was dispatched or artifact published in this task.

## Evidence and limitations

- Linux ARM64 Docker on a 10-vCPU local VM, a 6-GiB container limit, Go 1.25.11, driver GOMAXPROCS 10 and server GOMAXPROCS 2 per node. The three-node fixture uses 256 hash slots, 12 physical Slots, 600 channels, 24 readers and three 256-byte messages per channel.
- Warm storage caches and zero Channel runtimes are intentional. This does not qualify cold disks, 100,000-member groups or production capacity. No builds, profiling or other task-owned load tests overlapped measured windows.
- Dirty worktree source is based on `13f9687192c316d3c6fa967c0f8ae593d60005e5`. Embedded Go VCS metadata names the enclosing repository revision, not the dirty source contents; exact binaries and frozen source/harness manifests are recorded in the JSON evidence.
- The previous 420-QPS single-endpoint peak measurements belong to the superseded candidate. This turn revalidates corrected mixed traffic and the release matrix, without repeating that peak confirmation.
- Formal publication still requires clean exact-tag Linux AMD64 evidence and the normal signed package/public-client checks. Local ARM64 reports intentionally do not satisfy that production receipt identity filter.
- The initial non-executable old-binary copy failed before load; that preflight failure is retained separately. Superseded and refused runs are retained, never retried until green.

| Binary | SHA-256 |
| --- | --- |
| old | `1c6c13446eb19092a8f47f38a58fd561a0fc7812e30c1fbaae4bf5cc0c3babfd` |
| new | `5f829a57f86dcb3a954c216f51777b72e0cba09616f6009a5c3453f1a1f37c8d` |
| corrected | `11368a5fe32dc76373918b8d954d2f3c80e0e3c312e0252acad5f03c316d46de` |

Validation includes a reproduced failing variable-length ordering test against the old prefix, passing corrected conversation and application tests, QPS-policy and Workflow receipt contracts, and the named `go-format` and `flow-doc-contracts` checks. The generated FLOW index was refreshed after documentation changed.

Full derived metrics, raw artifact paths/hashes, frozen manifests, gate receipt and cleanup evidence are in [the JSON report](2026-09-13-conversation-resilience.json). Raw attempts and logs are retained under `/tmp/conversation-resilience/`. At measurement time, the work remained in the task worktree and had not yet been merged or pushed.
