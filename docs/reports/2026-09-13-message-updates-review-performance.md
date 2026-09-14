# Message updates: server review and performance validation

2026-09-13 · `codex/message-updates` · base `0160c1f7d068b555c872df79ab6ab57f2cd4bc76`.
All changes remain in the task worktree. This report supersedes the initial
implementation pass's noop-read and cross-restore write limitations.

## Standards

The read-only Standards review used AGENTS.md, applicable FLOW navigation,
SCHEMA_COMPATIBILITY.md and the code-review smell baseline. The initial three
findings and one performance observation were fixed:

- Aggregate multi-Slot replies are checked against the retained-byte budget
  before saving them; excess work is canceled. Up to four bounded in-flight
  replies additionally occupy temporary memory.
- Notification cursor and offline-import validation now accept the source
  subscriber store's full 65,535-byte UID range.
- Hint routes split by encoded JSON bytes as well as count. An individual hint
  that cannot fit is dropped without blocking other recipients.
- Notification paging reads body-free pending identity plus retention, avoiding
  complete payload hydration on each 128-recipient page.

The follow-up ReadIndex review found three further lifecycle/accounting defects;
all were repaired and verified by a final read-only follow-up:

- Taken controls recheck terminal admission under the Slot lock before insertion.
- Transient Ready persistence failures release callers but retain unconfirmed
  request accounting while RawNode still owns the request.
- New leaders cannot issue ReadIndex before a durable current-term commit. This
  avoids etcd's separate precommit read queue, which is not cleared on every
  term reset. Safe ReadIndex is explicitly configured.

Standards: **7 findings/observations addressed; no residual finding identified
in the bounded follow-up scope.**

## Spec

The independent Spec review used the accepted design and API contract. Both
reported defects were fixed:

- Delta responses count the final payload after revalidation finds a newer edit.
  If growth fills the page, continuation stays before the omitted indexed row
  and retains the round's upper bound. Tests verify both the byte limit and
  complete next-page coverage.
- Hot-Slot preference always leaves background scan capacity, including when a
  node owns only two Hash Slots. Cold notification/retention work cannot be
  permanently hidden by the hot Slot.

Spec: **2 findings addressed; no residual finding identified in the follow-up
scope.** SDK rollout and production capacity are still explicitly outside the
completed local verification.

## Restore write isolation

A real HTTP regression first reproduced a stale-generation edit returning 200.
`/message/update` now requires decimal-string `expected_content_epoch`, copied
from `X-WK-Content-Epoch` of the displayed content. The usecase checks it and
passes it to the Slot command; the serving proposer rechecks it while holding
restore admission through enqueue. Thus a forwarded request delayed across
restore cannot overwrite a numerically equal version in restored data.
Mismatch returns 409 `content_epoch_conflict`. Reload and make a new editing
decision/request ID. The serving-node admission test and HTTP test passed.

## Measurement method

Apple M4, Go 1.25.11, single-node cluster with 256 Hash Slots and 8 physical Slots.
Identical existing-API fixture:32 person channels, four messages per channel;
one channel has 128 KiB messages, the others 1 KiB. Sequential baseline and candidate
runs avoid competing server fixtures. Each case warms four requests, then
measures 64 requests at concurrency 1 and 8 through the in-process HTTP handler.
This excludes network RTT, TLS and SDK overhead. The edited fixture replaces
each of the 32 tails with 128 KiB and is not an equal-payload baseline comparison.

Allocations include background node work and recorder/JSON response costs.
Commit deltas are observed physical-Slot totals, so edited runs may include
notification progress. The final baseline and ReadIndex numbers are unprofiled; the prior noop
candidate was profiled, so its exact slowdown ratios are not controlled. Its
64/512/1,024 read-induced Slot commits independently establish the extra work.
The prior candidate's CPU profile includes startup and
fixture construction and is unsuitable for per-endpoint CPU attribution; it
shows syscall/scheduler/GC work, not a production capacity measurement.
The first baseline attempt timed out while preparing the fixture at 30 seconds;
the shared preparation deadline was raised to 2 minutes, and both fixtures then
completed without measured request failures.

## Unedited query comparison

All times are milliseconds; each row had 64 successful requests and zero errors.
The optimized production reader uses fresh ReadIndex plus durable apply;
custom embedding ports without that method retain a conservative noop fallback.

| Scenario | Concurrency | Baseline p95 | Noop p95 | ReadIndex p95 | Baseline / ReadIndex RPS | Noop / ReadIndex Slot commits |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| history_small | 1 | 0.09 | 36.93 | 0.12 | 15854.3 / 10541.6 | 64 / 0 |
| history_small | 8 | 0.27 | 59.67 | 0.23 | 73761.1 / 56300.9 | 64 / 0 |
| history_large | 1 | 2.88 | 42.81 | 4.39 | 705.8 / 613.4 | 64 / 0 |
| history_large | 8 | 7.98 | 66.89 | 7.98 | 1704.5 / 1657.2 | 64 / 0 |
| conversation_list_32 | 1 | 1.35 | 74.93 | 1.24 | 1140.5 / 1133.0 | 512 / 0 |
| conversation_list_32 | 8 | 5.57 | 139.78 | 5.37 | 3660.9 / 3015.0 | 512 / 0 |
| conversation_sync_32 | 1 | 4.04 | 147.50 | 3.75 | 546.4 / 556.2 | 1024 / 0 |
| conversation_sync_32 | 8 | 8.25 | 228.62 | 9.57 | 1894.4 / 1433.9 | 1024 / 0 |

The severe tens/hundreds-of-milliseconds noop regression is removed. The new
metadata checks still allocate more than the baseline: for concurrency 1,
small history 251→366 allocations, list 4,641→5,834, and sync 9,266→11,846 per request.
These short samples show remaining cost; they are not evidence of unchanged
throughput or stable p99. In particular, concurrent list/sync RPS remains below
the baseline in this run. A release gate needs longer repeated windows on Linux
and actual multi-node traffic.

## Large edited tails

| Scenario | Concurrency | p95 ms | p99 ms | RPS | Allocated bytes/request | Slot commits |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| history_small | 1 | 0.43 | 0.81 | 3376.2 | 1089254 | 0 |
| history_small | 8 | 0.77 | 1.15 | 17605.0 | 1143626 | 0 |
| history_large | 1 | 2.56 | 5.15 | 700.7 | 6338582 | 0 |
| history_large | 8 | 10.64 | 21.05 | 1275.0 | 6782177 | 1 |
| conversation_list_32 | 1 | 8.98 | 31.48 | 187.7 | 38322443 | 1 |
| conversation_list_32 | 8 | 25.90 | 34.46 | 446.9 | 35576248 | 0 |
| conversation_sync_32 | 1 | 11.92 | 64.58 | 120.7 | 67700003 | 2 |
| conversation_sync_32 | 8 | 37.69 | 44.78 | 258.1 | 66866804 | 0 |
| delta_changed | 1 | 0.18 | 0.48 | 6816.9 | 1229264 | 0 |
| delta_changed | 8 | 2.27 | 2.58 | 9769.6 | 1247729 | 0 |
| delta_empty | 1 | 0.03 | 0.10 | 35412.9 | 21609 | 0 |
| delta_empty | 8 | 0.17 | 0.23 | 87129.1 | 21829 | 0 |

A 32-conversation response with 128 KiB tails is about 4 MiB of payload before JSON.
It causes tens of MiB of allocation per request through serialization and read
layers. Use smaller preview pages for such payloads and capacity-test the chosen
limit; the bounded page contract does not imply cheap response construction.

## Validation commands

All Go commands use `GOWORK=off`. Logs and profiles are in worktree `tmp/`.
All checks below passed. The final follow-up review verified the three ReadIndex
repairs; it did not rerun tests. Tests were executed by the implementation task.

- Full related suites: meta/transfer, Slot FSM/proxy/Multi-Raft, cluster, app,
  API/gateway/node entries, message/conversation usecases, repair worker and
  delivery. Passed after correcting the new test fixture's vote-persistence step.
- Race: `go test -race ./pkg/slot/multiraft ./pkg/slot/proxy ./pkg/cluster ./internal/runtime/messageupdates ./internal/usecase/message ./internal/infra/delivery -run 'ReadBarrier|MessageUpdate|RepairBudget|RepairSlots|FencesEditEpoch' -count=1`.
- Real clusters: `go test -tags=integration ./internal/app ./pkg/cluster ./pkg/slot/multiraft -run 'TestMessageUpdateSingleNodeClusterHTTPFlow|TestMessageUpdateThreeNodeQuorumAndLeaderTransfer|TestReadBarrier' -count=1 -timeout=2m`.
- Performance: `WK_MESSAGE_UPDATE_PERF=1 go test -tags=integration ./internal/app -run '^TestMessageUpdateReadComparison$' -count=1 -timeout=4m -v`; add `WK_MESSAGE_UPDATE_PERF_EDIT=1` for edited tails and delta cases. The same fixture was copied into a detached baseline worktree for comparison.
- ReadIndex tests cover no additional log writes, durable-apply waiting, term
  changes, quorum loss, cancellation, shutdown and transient failure accounting.
- A 100,000-recipient paging test completes 782 bounded pages without any payload
  read. This uses fake routing; it does not claim 100,000 real online sessions.
- Named `flow-doc-contracts` passed (80 compliant FLOW files, eight nonblocking
  length warnings); `git diff --check` passed.
- Long-UID frame splitting, full UID checkpoint snapshot round-trip, delta
  growth/continuation and cold-Slot discovery have focused regression tests.

## Delivery limits

No SDK changes, cloud procurement, deployment or merge was performed. Full
repository testing from the initial pass still has the independently reproduced
baseline Grafana metric-coverage failure; this pass reran related suites, not
the entire repository. Production acceptance still requires a multi-node
baseline comparison with longer load windows, reconnect bursts, sustained edits,
real 100,000-member online fanout, CPU/allocation profiles, rejection rates and
queue occupancy. The API/schema and SDK contract are documented separately.

## Follow-up three-node evidence

The [three-node comparison](2026-09-13-message-updates-three-node-performance.md)
adds actual Linux processes, cross-node RPC, repeated one-minute windows, edited
tails and delta reads. It found and repaired a 300-record overlay batching
regression. Use that report for the current multi-node results and remaining
CPU/allocation costs; the earlier single-node measurements below retain their
original scope.
