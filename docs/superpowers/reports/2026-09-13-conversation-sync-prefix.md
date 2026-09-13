# Legacy conversation sync: hydrate only the requested page prefix

> Superseded correctness assumption: subsequent mixed/hidden validation found
> that the durable directory compares length-prefixed IDs, whereas legacy sync
> uses string ordering. The original prefix candidate can select wrong pages
> for variable-length IDs. Its throughput numbers remain raw diagnostic
> measurements, not qualification of the corrected implementation. See the
> conversation-resilience report for correction and validation.

Status: implementation and authorized validation complete. This report is local dirty-worktree diagnostic
and regression evidence, not clean exact-tag release qualification.

## Change and contract

`SyncLegacy` now asks the activation-ordered membership directory for only the
missing visible prefix through the requested page, in batches of at most 200.
It stops when that prefix is complete, the directory ends, or the existing
1,000 raw-candidate budget is exhausted. Default page size remains 100 and the
maximum remains 500. Overflow-safe page arithmetic prevents huge page numbers
from wrapping into an earlier page.

Tombstones, disbanded Channels, imported hidden memberships and inactive empty
memberships do not occupy visible page positions, so they may cause bounded
replenishment. Explicitly activated empty conversations do occupy positions.
Unread/excluded-type filters and empty recent-message results still apply after
page selection and never refill the selected page. Client cursor overrides,
activation/ChannelID/ChannelType ordering, response limits and recent-message
semantics remain unchanged. Unpaged requests retain the full bounded walk.

Every attempted directory, head or recent read still fails the entire request
on error. Heads beyond the needed prefix are not attempted, so failures in those
unread Channels no longer fail the requested page. This is not a cross-request
snapshot or a consistency guarantee during concurrent directory mutations.

No Channel runtime is activated; reads still use current-Leader persisted LEO.
There is no new cache, retry, waiting queue, concurrency increase or membership
write. Hidden-heavy small pages can need more directory round trips because each
batch requests only the remaining visible positions; the 1,000-candidate and
five-second bounds remain in force. Page two still has to build the visible
prefix through page two; this change does not introduce a new public cursor.

## Validation plan and evidence

The old source failed the real `SyncLegacy` read-count regression: first page
100 over 1,200 memberships hydrated 1,000 candidates. The new source hydrates
100. Tests cover page two, normalized page sizes, overflow, scan exhaustion,
hidden/deleted/empty membership, post-page filters, client overrides, empty
recents and needed-versus-unread failures. A deterministic multi-fixture
comparison checks selected candidate pages against the full bounded walk.

Related usecase, composition-root and gate-policy unit tests, the conversation
race suite, Linux E2E harness compilation and named format/FLOW checks are
recorded separately. Final validation receipts and artifact hashes are recorded in the companion JSON.

## Controlled load comparison

Two Linux ARM64 binaries are built from the same dirty checkout with Go 1.25.11
and CGO enabled. The old binary uses a one-file overlay restoring the preceding
`legacy_sync.go`; all earlier persisted-read optimizations remain in both.
Exact hashes and the frozen governing context are in the companion JSON and
`/tmp/conversation-sync-prefix/` artifacts.

All processes share one local 10-vCPU Linux VM. The task container has a 6-GiB
memory cap and no CPU quota; these are not three independent production hosts.
Cgroup counters recorded no CPU throttling or OOM events.

Three counterbalanced pairs run in order old-1, new-1, new-2, old-2, old-3, new-3.
Every invocation starts a fresh three-node cluster with 256 hash slots, 12
physical Slots, three replicas, 600 groups, 24 users and 200 memberships per
user. Server GOMAXPROCS is 2 per node, driver GOMAXPROCS is 10, and 48 driver
workers offer sync page-100 requests at 420 QPS for one uninterrupted 180-second
window. Warm disk caches are intentional. There are no measured retries,
profiles, rate changes or concurrent heavy validation jobs.

The fixed sync-only diagnostic preset omits the unrelated list-1200 window.
The unchanged 12-case list/sync release gate runs separately with its original
8 workers, rates, latency and allocation ceilings. Passing local runs do not
raise release thresholds or establish production capacity.

## Results

| Pair | Old errors / P99 ms | New errors / P99 ms |
| --- | --- | --- |
| 1 | 2 / 80.09 | 0 / 31.26 |
| 2 | 0 / 83.19 | 0 / 32.81 |
| 3 | 5 / 84.90 | 0 / 31.58 |

The new binary passed 3/3 uninterrupted 420-QPS windows;
the old binary passed 1/3. Each variant received 226,800
scheduled requests. Old/new HTTP refusals were 7/0,
matching persisted admission-rejection counters. All six windows had zero drops,
unexpected failures, Channel activation, membership writes and byte-budget failures.
Admissions and completions balanced, with no remaining in-flight reads.

| Metric | Old | New | Reduction |
| --- | --- | --- | --- |
| CPU ms per successful request | 10.523 | 7.734 | 26.5% |
| Allocated bytes per successful request | 8,041,600 | 6,104,458 | 24.1% |
| Median of the three P99 samples, ms | 83.19 | 31.58 | 62.0% |
| Head items per scheduled request | 200.00 | 100.00 | about 50% |
| Recent-query items per scheduled request | 100.00 | 100.00 | unchanged |
| Mean admitted head-batch hold, ms | 1.190 | 0.422 | diagnostic |

CPU/allocation totals include work spent on refused requests and are amortized
by successful requests. The work reduction and latency improvement recur across
pairs. Zero refusals in three new windows support this bounded load result;
they do not guarantee zero overload at every future load or establish maximum
QPS. The unchanged fixture spreads both 100 and 200 candidates across the same
leaders/Slots, so the main gain is smaller read batches rather than eliminating
whole RPC destinations. Recents remain complete.

## Gate, checks and delivery

The unchanged release gate passed all 12 cases and
26,400 requests, with zero errors, drops,
Channel activation and membership writes. Maximum gate P99 was
57.93 ms, below the unchanged 500-ms limit.
All allocation and completion thresholds passed.

- Related conversation, app wiring/adapter and gate-policy unit packages passed.
- Conversation race tests and Linux conversation unit tests passed.
- Candidate equivalence covers 240 fixed fixture/page combinations; focused
  public-usecase regressions preserve post-page and failure semantics.
- Linux E2E harness compilation, named `go-format` and `flow-doc-contracts`,
  and whitespace checks passed.
- The exact task-owned Docker container was removed after verifying no server
  processes remained. Unrelated containers, shared cache and dirty worktrees
  were preserved.

The checkout base is `13f9687192c316d3c6fa967c0f8ae593d60005e5`.
Go embeds the enclosing repository revision
`d6dbbfc057f2ab6043cda0df2e6cbc1427f2aca6` in both binaries; neither revision
alone identifies the dirty candidate or the old overlay. Exact binary hashes,
source/overlay hashes and build metadata are retained in the companion JSON
and `/tmp/conversation-sync-prefix/`. These are local regression results;
clean exact-tag publication qualification remains separate.
