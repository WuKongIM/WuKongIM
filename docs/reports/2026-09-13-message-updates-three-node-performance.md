# Message update three-node performance comparison

Status: completed, including the batching repair and final validation. This is
a same-host Linux ARM64 diagnostic,
not release qualification or a production capacity estimate.

The subsequent [query CPU/allocation optimization report](2026-09-13-message-updates-query-optimization.md)
records the next candidate and preserves this comparison as its starting point.

## Method

- Baseline: `0160c1f7d068b555c872df79ab6ab57f2cd4bc76`.
- Candidate: the uncommitted `codex/message-updates` implementation, with its
  exact source file hashes and both binary hashes captured in
  `tmp/message-update-soak/source-manifest.json` before execution.
- The same precompiled black-box harness for the completed baseline and
  unedited candidate comparisons. The edited variant has the separately
  recorded cold-recovery assertion correction described below. Three real product
  processes in an isolated Linux container, six assigned CPUs, 6 GiB memory,
  GOMAXPROCS=2 for each node and the driver. Loopback network between processes;
  database files reside in the container filesystem, not the host bind mount.
- 256 Hash Slots, 12 physical Slots, three replicas. 600 groups, 24 readers,
  three 256-byte messages per group, public HTTP setup and validation.
- Safely evict fixture channel runtimes before reading. Every conversation-only window
  requires zero runtime loads, resident runtimes, and membership writes. Delta
  windows explicitly recover the selected channel before measurement, then
  require zero additional loads/writes and stable measured residency.
- Three successive unprofiled 60-second windows: `/conversation/list` at
  200 QPS plus `/conversation/sync` at 60 QPS, page size 100, eight workers per
  endpoint. Scheduling and queue delay count toward latency; no measured
  retries. Response order and every returned message are checked.
- Candidate edited variant: change all 600 tails once before measurement,
  preserving payload size. Repeat the mixed windows, then measure changed and
  empty `/channel/messageupdates` responses separately, 100 QPS for 60 seconds
  each. Fixed cursor replay measures read cost; it is not SDK polling advice.
- CPU and allocation pprof collection occurs only during a separate ten-second
  workload after all unprofiled windows. CPU/allocations for mixed windows are
  counted once across both endpoints.

The original release profile and thresholds remain unchanged. Rejected windows
remain evidence even when diagnostic collection completes. This run does not
measure physical-host network latency, sustained concurrent edits, 100,000 live
notification recipients, failover under load, or maximum capacity.

## Preserved failed collection

`base-1` completed all three unprofiled windows without request failures, drops,
runtime loads, or membership writes. Its later eight-second CPU capture hit the
shared HTTP client's five-second timeout. Collection is incomplete and is not
used as the completed comparison. The harness now gives profile requests their
own 15-second timeout; measured requests retain the original five-second timeout.
The change and compiled harness hashes are in `harness-profile-fix.json`.

## Completed mixed-read comparison

Each P95/P99 below is the worst of the three unprofiled 60-second windows,
not a percentile calculated by averaging percentiles. CPU is the mean total
for all three servers per 60 seconds; allocation is total server allocation
divided by successful requests across both mixed endpoints.

| Variant | List P95 / P99 ms | Sync P95 / P99 ms | CPU seconds / minute | MB allocated / request |
| --- | ---: | ---: | ---: | ---: |
| Exact baseline (`base-2`) | 16.87 / 19.42 | 29.99 / 34.07 | 70.48 | 2.784 |
| Initial candidate (`candidate-1`) | 13.65 / 16.31 | 87.16 / 96.50 | 138.73 | 3.614 |
| Repaired candidate (`candidate-2`) | 16.71 / 19.41 | 32.43 / 35.95 | 109.82 | 3.391 |
| Repaired candidate, all tails edited (`edited-2`) | 16.69 / 19.81 | 32.68 / 36.16 | 117.01 | 3.816 |

All twelve windows in the four completed comparison runs sustained the fixed list/sync 200/60 QPS
load with zero errors, unexpected errors, queue drops, runtime loads, resident
runtimes, and membership writes. The minimum achieved rates for the repaired
candidate were 199.967 / 59.983 QPS. These offered rates are not maximum capacity.

The repair reduced the initial candidate's worst sync P99 by 62.7% and CPU by
20.8%. Compared with the baseline, the final candidate still uses **55.8% more
server CPU and 21.8% more allocated bytes per request** at this workload. Its
worst sync P99 is 5.5% higher. Passing this bounded load does not erase these
resource costs. No claim of zero regression or production-scale acceptance is
made. Each variant uses a fresh cluster; the three windows are consecutive
within that cluster, not three independently repeated deployments.

## Regression found and repaired

The initial candidate completed all three mixed windows, but sync P99 rose to
92–97 ms versus roughly 32–34 ms on the repeated baseline. The first window's
sum of `wukongim_transport_rpc_total` increments rose from 390,014 to 2,874,855
(the metric counts transport observations, not distinct business requests).

`overlayMessageReads` used a grouped fast path only when the entire result had
at most 200 records. A normal page of 100 conversations with three recent
messages exceeded that boundary and fell back to 100 sequential per-channel
overlay calls. Each call independently requested an authoritative Slot barrier.

`TestMessageUpdateWideRecentsStayBatched` reproduced this at the real batching
seam: expected two bounded batches for 300 records, observed 100. It failed
before the fix and passed afterward. The loop now processes cross-channel
chunks of at most 200 records, preserves per-page byte budgets, stops fetching
truncated rows, and falls back to seven-record chunks for oversized replacement
replies. Growth tests verify both forward and reverse continuation through the
same helper. No consistency barrier is cached or omitted.

The new RPC IDs also lacked descriptive transport aliases. The existing alias
coverage test now includes both IDs, failed on the original implementation, and
passes with fixed `slot message updates` / `message update hint` aliases.

## Related validation

Passed after the batching change:

- `go test ./pkg/cluster ./pkg/cluster/net ./test/e2e/message/conversation_qps -count=1`
- `go test -race ./pkg/cluster -run TestMessageUpdate -count=1`
- Integration-tagged `TestMessageUpdateSingleNodeClusterHTTPFlow` and
  `TestMessageUpdateThreeNodeQuorumAndLeaderTransfer` in app/cluster.
- Named `flow-doc-contracts` check: 80 compliant FLOW files, zero invalid;
  eight existing advisory length warnings. `git diff --check` passed.

Commands used `GOWORK=off`. Darwin's race linker emitted LC_DYSYMTAB warnings;
the race test itself passed. The earlier repository-wide Grafana coverage
failure documented in the prior report remains outside this focused validation.

## Corrected delta setup assertion

`edited-1` completed its three mixed windows and validated all 600 edited tails.
The first changed-page delta then recovered the selected cold channel through
its retained-original committed read: runtime loads changed from 600 to 601.
The diagnostic incorrectly applied the conversation-only zero-residency
invariant to this history-like path and stopped before delta measurement.
This is a harness assertion error, not evidence of a product read failure.

The corrected edited diagnostic records that first cold request's latency,
load delta and residency, then measures stable selected-channel delta reads.
It requires no additional loads or membership writes and unchanged residency
within each measured window. Conversation windows keep the original invariant.
The product binary and mixed load are unchanged; the edited harness change
and hash are recorded in `harness-delta-fix.json`. The failed collection remains
under `edited-1`; it is not reported as a completed run.

## Edited and delta results

All 600 retained tails were changed through `/message/update`, using the epoch
returned with their original history and CAS version zero. Every edited
conversation response was checked for payload, original sequence/client identity,
channel order, and complete page coverage. The completed `edited-2` run measured
117.01 CPU seconds per minute and 3.816 MB allocated per mixed request: **66.0%
more CPU and 37.1% more allocation than the baseline**, which has no edit feature.
Payload size remained 256 bytes. These figures include server background work.

The prior `edited-1` mixed windows were valid measurements even though collection
later stopped at the incorrect delta assertion. Their worst list/sync P99 values
were **33.23 / 63.96 ms**, higher than the completed repeat. They remain in the
machine-readable evidence and show same-host tail variability; the repeat does
not invalidate them. There were no request errors or drops in either set.

The selected cold channel's first changed-page query took 37.148 ms, loaded one
runtime (residency 0 → 1), and wrote no memberships. This is one observation,
not a cold-start percentile. Subsequent windows used the same selected channel,
round-robin ingress, eight workers, 100 scheduled QPS and 60 seconds each:

| Delta result | Achieved QPS | P95 / P99 ms | Allocated MB / request | CPU seconds |
| --- | ---: | ---: | ---: | ---: |
| One changed message | 99.983 | 26.59 / 28.86 | 0.171 | 32.99 |
| Empty update page | 99.983 | 19.67 / 21.21 | 0.127 | 29.37 |

Both windows had zero errors, drops, additional runtime loads or membership
writes, with residency stable at one. Payload, version, sequence, cursor and
coverage were validated on every response. After these windows, the one loaded
runtime was evicted before the independent conversation CPU/allocation capture.

## Evidence and delivery

- [Portable results and identities](2026-09-13-message-updates-three-node-performance.json)
  retains every measured window, both incomplete collections, per-service RPC
  counter deltas, fixed environment, binary hashes and source-manifest hashes.
- Raw JSON, logs, source manifests and bounded profiles remain in
  `tmp/message-update-soak/`. The final product binary SHA-256 is
  `5ff0d550760637f8298236271696d7a853662475c128d457f4a00094ab83283c`.
- Run order: initial baseline (profile timeout), initial candidate, completed
  baseline, repaired candidate, initial edited run (setup assertion), completed
  edited run. The report does not treat incomplete collection as full acceptance.
- Temporary containers exited and were removed; the clean detached baseline
  worktree was removed. The implementation remains uncommitted in
  `codex/message-updates`; it has not been merged or deployed.

The observed batching regression is repaired and the fixed-load comparison is
complete. Remaining work before a production capacity claim includes addressing
or budgeting the measured CPU/allocation cost, real cross-host network testing,
sustained concurrent edits, and live notification fanout. None of those is
established by these bounded same-host measurements.
