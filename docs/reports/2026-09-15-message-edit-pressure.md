# Message editing: bounded three-node validation

Date: 2026-09-15. This records the approved SDK CI merge and local message-edit
validation. No cloud resources were purchased and no SDK/server release was made.

## Artifacts and scope

- Server: `111bdee669e402f2c6dd0c285fbea315ea77007d` (merged #960).
- SDK: `a038f874230a6fe5201c2faae61b6f2b20cb9ebc` (merged #28).
  Both the PR CI and merge-triggered main CI passed.
- Server binary SHA-256:
  `b47105463afb2bed4d52c4006b333b6361e7ff47b99103a08787fdbe52bc43dc`.
- SDK rebuilt from the merged tree; UMD SHA-256:
  `df3c2d8fa30d4113ac328f5b0969a1ea26445ed3622f535d33b184e61b784599`.
- Same Darwin/ARM64 host, 10 logical CPUs, Go 1.25.11, Node 22.12.0.
  Workloads ran sequentially. Process scenarios use GOMAXPROCS=2 per server;
  the app integration fixture uses GOMAXPROCS=6 for all three nodes and its driver.
- All clusters use 256 Hash Slots and three replicas. The recovery scenario uses
  12 physical Slots; pressure scenarios use eight.

These are finite correctness and latency experiments, not production capacity
qualification. The app fixture runs actual Raft, storage, authoritative subscriber
paging, Presence lookup and notification progress in three nodes inside one Go
process. Its HTTP handler latency excludes the network, and its CPU/memory include
the test driver. The SDK experiment uses three real server processes, 32 isolated
SDK VM clients, real WebSockets, token authentication and the example BFF. Browser
render time is not included. The 100,000-member group fixture has offline members;
it does not represent 100,000 online sockets. Payloads are tiny synthetic text,
not a large-payload capacity mix.

## Abrupt leader termination and recovery

The existing process E2E passed in 293.24 seconds. Actual Channel and physical Slot
authorities were identified through Manager before SIGKILL. Each elected from node
3 to node 1 while its former leader was down. Every writer/reader made progress.
The scenario checked histories, exact lookup, both conversation interfaces,
incremental pagination and old idempotent retries, then drained to exact final
versions. Restart preserved content epoch, unread counts and conversation order.

- 2,575 successful edits, 864 successful idempotent retries.
- 4,445 successful reads across history, exact lookup, list, legacy sync and delta.
- 502 explicitly counted transient attempts during failure/recovery; none in the
  initial healthy window. These attempts are not counted as successful reads.
- All 12 final message versions converged (215–218); final empty delta verified.

See the raw recovery receipt in the [companion JSON](2026-09-15-message-edit-pressure.json). This scenario does not assert
online notification delivery through the crash.

## SDK concurrency

16 channels ran concurrently, each with one editor and a receiver connected to a
different node. Each pair completed 32 edits and waited for the receiver's actual
message-update listener before its next edit. All 512 versions matched, final
conversation previews matched version 32, unread/order stayed unchanged, and no
SDK background errors were reported.

First run, unprofiled, 20.02-second measured window:

| Measurement | p50 | p95 | p99 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| BFF edit HTTP round trip | 84.5 ms | 182.0 ms | 263.7 ms | 288.4 ms |
| Edit invocation → receiving SDK cache update | 499.9 ms | 753.4 ms | 850.5 ms | 7,899.9 ms |

The maximum is retained; the p99 must not conceal it. HTTP timings include the
BFF's author/time-policy exact lookup. Visible latency includes SDK coalescing,
EVENT delivery, incremental read and merge. This is a closed-loop workload, not a
fixed offered-QPS or saturation test.

Server CPU increased by 1.76, 1.76 and 1.83 CPU-seconds over the measured window
(about 0.27 CPU cores in aggregate). Sampled per-process peak RSS was approximately
173–174 MiB. This excludes the Node/VM/BFF driver and does not prove steady-state
memory bounds.

A second unprofiled run completed another 512 edits in 18.76 seconds: BFF p95/p99
199.9/254.3 ms; SDK-visible p95/p99 775.0/912.2 ms, maximum 1,069.9 ms. Added
bounded EVENT/feed trace capture found no sample above two seconds. The first
7.9-second sample has no phase trace and was not reproduced; its cause remains
unresolved. Do not describe the repeat as a fix.

## Queue overflow diagnostic

The test committed 1,280 edits of distinct originals through one API node while
all notification dispatchers were gated by the test. Durable state and normal edit
commits remained active. All 1,280 pending rows existed before release. A fast pop
requires a dispatcher attempt; subtracting every blocked attempt on all nodes from
the distinct commits conservatively proves overflow beyond the origin's 1,024-item
queue. Released dispatch calls use the production usecase and storage ports.

Attempt 1, unprofiled, failed its original 90-second drain budget. It proved at
least 237 overflowed identities. The fixture did not record the final remaining
count, so that failure alone cannot establish permanently lost work. The receipt
remains `complete:false`; it is not replaced by a later successful diagnostic.

| Attempt 1 HTTP handler timing | p50 | p95 | p99 | Samples |
| --- | ---: | ---: | ---: | ---: |
| 16 channels × 64 edits | 215.5 ms | 303.6 ms | 339.5 ms | 1,024 |
| 1 channel × 1,280 distinct edits, dispatch gated | 380.7 ms | 424.7 ms | 439.7 ms | 1,280 |

Attempt 2 added bounded backlog samples and dispatch counts, captured
CPU profile, and extended **observation** to 180 seconds. It separately records
whether the original 90-second budget was met. This changed neither production
queue limits nor scheduling budgets. Repeated authoritative pending reads are
included in this diagnostic's load and are not free observations.

Attempt 2 proved at least 230 overflowed identities, then drained all 1,280 pending
rows in **105.19 seconds**, missing the 90-second budget. Recorded backlog fell
from 1,280 to 1,023 at 25.7 seconds, 625 at 56.6 seconds, 225 at 87.5 seconds and
zero at 105.2 seconds. This distinguishes slow recovery from a permanently stalled
queue in this run. It does not exclude observer-induced contention.

Across the whole profiled run, real dispatch calls averaged about 52 ms (including
healthy and diagnostic phases, duplicate work and nine dispatch failures). The
CPU profile was dominated by runtime/system calls, not an identified message-body
CPU hot spot; it does not isolate Raft/storage wait latency. The app process plus
driver used 160.09 CPU-seconds over 361.63 seconds and peaked at 636 MiB RSS. These
figures include the subsequent, incomplete full membership provisioning attempt.

## Large-recipient fairness and setup limitation

Attempt 2's full `/channel` creation with 100,000 members exhausted the fixture's
six-minute overall context, returning HTTP 400 `context deadline exceeded`. The
notification fairness portion never started. Both its `complete:false` receipt
and successful queue-drain evidence are retained. This is a separate membership
provisioning limitation, not proof that edit notification paging failed.

The final notification fixture creates the channel/author membership and original
through public HTTP. It then commits the other 99,999 subscriber rows in batches
of at most 1,000 through the real authority-routed, counted cluster mutation port,
checking every changed count. Offline recipients' UID conversation-directory rows
are intentionally omitted; notification enumeration reads actual persisted
subscribers. No fake recipient source or direct local DB write is used. This
isolates notification pagination and **does not qualify full 100k-member channel
creation, member history access or UID directory projection**. The final fixture
has an eight-minute context and a separate 240-second large-notification drain
observation limit.


The final unprofiled fixture passed in 344.66 seconds:

- Again proved queue overflow (at least 239 distinct identities). All pending
  notifications drained in **99.37 seconds**, again outside the original 90-second
  budget. Healthy handler p95/p99 was 294.6/334.5 ms; gated single-channel handler
  p95/p99 was 423.0/436.0 ms.
- 100,000 subscriber records were prepared in **8.95 seconds** through the
  isolated fixture described above.
- All 16 small-channel notification checkpoints completed **2.07 seconds** after
  the large edit started, while the large target was still pending. Small-edit
  HTTP maximum was 157.2 ms. This demonstrates inter-channel progress under the
  large recipient workload.
- Large-target pending state finally cleared after **144.75 seconds**. This is
  completion of subscriber traversal/checkpointing with offline recipients, not
  100,000 client acknowledgments. Dispatch failures were retried; the wrapper's
  incomplete-success call count is not an exact subscriber-page total because
  an operation can commit before its caller sees a timeout.
- Whole-process peak RSS was about 647 MiB. After the large phase, Go heap in use
  was 259 MiB and 1,117 goroutines remained versus 986 before healthy load. These
  snapshots do not establish peak goroutine count or long-duration leak freedom.

## Assessment and next action

The bounded correctness, queue convergence and inter-channel fairness checks
completed. **Notification latency is not qualified for a broad release**:

1. A 1,280-message single-channel backlog takes about 100 seconds to recover in
   this fixture; the original 90-second observation budget failed repeatedly.
2. Full traversal for 100,000 offline recipients takes about 145 seconds. Fast
   small-channel progress does not make the large channel's completion fast.
3. One SDK sample took 7.9 seconds despite a much smaller p99. The repeat did not
   reproduce it, so its root cause remains open.
4. Full 100k-member HTTP provisioning did not finish within the first fixture's
   remaining context. The isolated fanout test does not resolve that finding.

Next, add bounded phase timing for ready-queue wait, durable discovery wait,
authoritative reads, recipient lookup, progress commit and EVENT-to-SDK merge.
Use it to isolate the slow path, test reduced pending-observation load, and verify
an optimization without enlarging the queue or hiding earlier failed windows.
A deployment-representative Linux run with realistic payloads and online large
recipients is still required before making capacity or release claims.

Validation also passed `GOWORK=off go test -tags=integration
./internal/runtime/messageupdates -count=1` and the JavaScript syntax check. All
owned app nodes and server processes were stopped after the runs. SDK CI PR #28's
clean worktree and original local task branch were removed after merge containment
was verified.

Only diagnostic tests, experiment assets and documentation changed in this branch;
production code, APIs and CMD behavior did not change. No product Changelog entry
is required for this diagnostic-only work.

## Reproduction

From the server worktree, build once:

```sh
GOWORK=off go build -o /absolute/output/wukongim ./cmd/wukongim
```

Recovery, using the existing scenario:

```sh
WK_E2E_BINARY=/absolute/output/wukongim \
WK_E2E_MESSAGE_UPDATE_STABILITY=1 \
WK_E2E_MESSAGE_UPDATE_STABILITY_REPORT=/absolute/output/recovery.json \
GOWORK=off go test -tags=e2e ./test/e2e/message/message_updates -count=1 -timeout=8m -v
```

App pressure (opt-in; default integration runs skip this):

```sh
WK_MESSAGE_UPDATE_PRESSURE=1 \
WK_MESSAGE_UPDATE_PRESSURE_REPORT=/absolute/output/pressure.json \
GOMAXPROCS=6 GOWORK=off go test -tags=integration ./internal/app \
  -run '^TestMessageUpdateThreeNodePressure$' -count=1 -timeout=10m -v
```

For separate whole-process profiling, compile with `go test -tags=integration -c`
and invoke that test binary with `-test.cpuprofile=/absolute/output/pressure.pprof`.
Do not mix profiled results into unprofiled latency claims.

Rebuild the merged SDK with `npm run build`, then run the supplied experiment:

```sh
WK_EDIT_SERVER_BIN=/absolute/output/wukongim \
WK_EDIT_SDK_ROOT=/absolute/path/to/WuKongIMJSSDK \
WK_EDIT_PRESSURE_REPORT=/absolute/output/sdk-pressure.json \
node docs/reports/assets/message-edit-sdk-pressure.cjs
```

Every experiment owns and tears down its cluster. The companion receipt includes
source/context digests and distinguishes completed validation from failed budgets.
