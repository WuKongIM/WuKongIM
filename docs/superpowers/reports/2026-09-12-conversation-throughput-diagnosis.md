# Conversation throughput diagnosis

2026-09-12. Local Linux ARM64 dirty-worktree evidence, not release publication evidence. This pass adds an opt-in maintained diagnostic scenario and identifies bottlenecks. It makes no further production-path optimization, admission-limit change, merge or push.

## Sustained results

The test completed in 1,291.4 seconds. Each endpoint/topology first used bounded capacity probes, then three consecutive 60-second windows at its confirmed offered rate. Metrics scrapes separate the windows. CPU/allocation profiles and five-second Go traces ran afterward under separate load and are excluded from sustained results.

| Nodes | Endpoint, page 100 | Offered QPS | Actual QPS, minutes 1 / 2 / 3 | P99 ms, minutes 1 / 2 / 3 | Mean cluster CPU cores | CPU ms/request |
| ---: | --- | ---: | --- | --- | ---: | ---: |
| 1 | `/conversation/list` | 800 | 799.9 / 800.0 / 800.0 | 26.5 / 21.6 / 22.6 | 1.57 | 1.96 |
| 1 | `/conversation/sync` | 240 | 240.0 / 240.0 / 240.0 | 31.1 / 24.7 / 25.1 | 1.39 | 5.79 |
| 3 | `/conversation/list` | 1200 | 1199.6 / 1199.6 / 1199.3 | 50.0 / 48.3 / 45.4 | 4.43 | 3.70 |
| 3 | `/conversation/sync` | 360 | 359.6 / 359.6 / 359.5 | 97.1 / 103.3 / 255.6 | 4.72 | 13.10 |

All 12 sustained windows passed with zero errors, drops, additional runtime loads, active runtimes and membership mutations. Every response was validated against expected persisted message identities. The final three-node sync window reached P99 255.6 ms: it passed the 500 ms limit, but the variation must remain visible.

The nearest observed rejected offered rates were single-node list 1,200, single-node sync 360, three-node list 1,600 and three-node sync 480 QPS. These overload probes are rejected evidence, not passing tests. Confirmed rates are sampled lower bounds, not exact maxima. Diagnostic `complete` means collection completed; inspect each window's verdict. This run passed all sustained windows.

## Findings and next implementation

### 1. Batch per-channel metadata preparation in sync

Three-node server RPC counts per successful request, third sustained minute:

| RPC service | list | sync |
| --- | ---: | ---: |
| User channel membership | 0.667 | 67.333 |
| Channel metadata | 0 | 66.208 |
| Runtime metadata batch | 8 | 16 |
| Permission metadata batch | 8 | 8 |
| Conversation heads | 2 | 2 |
| Recent message reads (`channel committed reads` service name) | 0 | 2 |

Counts sum RPCs served across nodes. They exclude local calls and do not imply 67 serial network round trips: preparation already uses bounded workers. Background Controller/Slot Raft calls remain separate in the JSON evidence.

`prepareSyncChannelMessages` obtains membership and channel state separately for each selected channel. With 100 selected channels, roughly two thirds of each lookup class is remote in this fixture. Third-minute mean **server handler** durations were only 0.0105 ms for membership and 0.00384 ms for channel metadata. These are not client RTT or queue-delay measurements: the current observer records service-task execution duration.

The independent five-second node-1 sync trace recorded 150.2 aggregate goroutine-seconds of blocking under `prepareSyncChannelMessages`: 76.9 under membership and 73.3 under channel-state reads. Transport client calls appear in these wait paths. These are summed goroutine waits, not per-request latency or CPU percentages. Together with the call counts, this supports reducing RPC fanout before raising admission limits; it does not attribute all wait to the network itself.

Recommended next change: add narrow batch-read ports for selected channels; group authoritative membership/channel-state requests by physical Slot and route to its current Leader with bounded batches and concurrency. Assemble per-channel preparation from those results. Preserve visibility, tombstone/terminal-state, retention and cursor checks, and fail the whole request on node/disk failures. Do not cache membership or authority across requests or reuse earlier hydration results without a proven freshness contract.

Acceptance: response/error semantics and zero runtime activation stay intact; point RPC counts fall to a bound determined by touched Slots/batches. Repeat equal-load Linux three-minute tests, compare P99 and CPU/allocations, then use finer 50–100 QPS steps near the observed boundary. Keep fixed release thresholds unchanged pending separately reviewed evidence.

### 2. Reduce unread-count iterator work shared by both endpoints

Single-node CPU profiles attribute 25.44% of list CPU and 16.59% of sync CPU cumulatively to `ChannelLog.CountOrdinaryMessages`. Weighted across the three server profiles, those shares are 15.51% and 9.30%. These are measured cost shares, not predicted optimization gains.

The count path queries the non-business-message index twice for boundary ranks. Even an ordinary-only channel with an empty internal-message index constructs/seeks/closes two iterators. A focused change can use one bounded iterator for both ranks and establish an empty index once, preserving the append lock, retained-prefix baseline, context/errors and index validation. Verify ordinary/internal mixtures, retention boundaries, absent indices and concurrent writes before repeating HTTP load. No unread-result TTL cache is needed.

### 3. Early pagination requires semantic proof

On a single-node cluster, `listLegacyConversationCandidates` accounts for 60.96% of sync CPU cumulatively. This fixture builds 200 candidate heads before selecting 100 conversations. This includes unread counts; do not add overlapping CPU percentages.

Correction to the earlier discussion: legacy sync sorts by membership `ActivatedAt`, then channel ID/type, not latest-message time. Deleted/hidden/no-visible-message filtering happens during candidate construction, and unread/exclusion/cursor rules further affect selection. Truncating the raw directory to N entries can change returned pages. Retain current behavior until an early-stop design proves ordering, filtering and failure semantics.

## CPU, driver and disk interpretation

Single-node list uses about 1.57 CPU cores with GOMAXPROCS=2; sync uses 1.39. Three-node cases use about 4.43 and 4.72 cores across six server scheduler slots. CPU/storage-iterator work is substantial; multi-node sync also has clear RPC waiting. Profiles were captured at passing rates, so this does not isolate every component of the exact overload boundary.

Separate driver CPU profiles consumed approximately 0.49/0.42 cores for single-node list/sync and 1.07/0.87 for three-node list/sync. This does not indicate a CPU-saturated driver in the shared ten-vCPU VM. GOMAXPROCS is not dedicated CPU allocation.

Selected read paths had no syscall-blocking samples in the inspected single-node list/sync and three-node list traces. This warm-cache fixture does not support blaming physical disk waits, but cannot qualify cold disk, large histories or concurrent compaction/writes. Trace totals include idle/background goroutines and are not request latency.

## Environment, validation and reproduction

- Local Docker Desktop Linux ARM64 VM: 10 vCPUs, 8,217,968,640 bytes RAM. Container: 6 GiB memory limit, no CPU quota, pids limit 4096. Image `golang:1.25.11-bookworm`, Go 1.25.11.
- Each server GOMAXPROCS=2; driver GOMAXPROCS=10, 16 workers per node. Single-node and three-node clusters, 256 hash slots and 12 initial physical Slots.
- Fixed fixture: 600 groups, 24 readers in three cohorts, 200 groups/user, three 256-byte messages/group. Read-only measurement, warm disk caches, fixture runtimes safely evicted before reads.
- This is a short sustained diagnostic, not a production soak, mixed read/write, high-member-count, cold-disk or clean Linux AMD64 release qualification. Earlier macOS absolute capacity must not be compared as a code optimization gain.
- The diagnostic is opt-in; the fixed 12-case release gate remains separate. Native compile/opt-out, acceptance-policy unit tests, E2E vet and named `go-format` passed. Real Linux E2E diagnosis passed both topologies. No unrelated repository-wide test claim is made.
- The exact temporary diagnostic container was stopped and removed. Existing Docker resources were preserved. Raw CPU/alloc/trace files and log remain at `/tmp/conversation-qps-diagnosis`; hashes and raw metric receipt are in the companion JSON. `/out/` receipt paths refer to that directory's container mount.

```sh
WK_E2E_CONVERSATION_DIAGNOSIS=1 \
WK_E2E_CONVERSATION_DIAGNOSIS_REPORT=/tmp/conversation-diagnosis/report.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps \
  -run '^TestConversationQPSDiagnosis$' -count=1 -timeout=35m -p=1 -v

go tool pprof -top -cum /tmp/conversation-qps-diagnosis/1n-list-100-node1-cpu.pprof
go tool trace -pprof=sync /tmp/conversation-qps-diagnosis/3n-sync-100-node1.trace > /tmp/sync-wait.pprof
go tool pprof -top -cum /tmp/sync-wait.pprof
```

Base source: `13f9687192c316d3c6fa967c0f8ae593d60005e5`, source_dirty=true.

Binary SHA-256: `6dd5578d390a42f906a4893e82955a53bbfd9699f996778d0b56220e18d125a4`.

Profile SHA-256: `b29f053bd0f8dfb7437a48edba020c0bd2ff6dc788ac303e91a07abe4576cba3`.
