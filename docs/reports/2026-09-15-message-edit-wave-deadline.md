# Message-edit notification wave deadline repair

Date: 2026-09-15. Follow-up to the
[notification optimization](2026-09-15-message-edit-notification-optimization.md).
This is bounded local validation, not a release or a production latency guarantee.

## Confirmed failure mechanism

A diagnostic run of the real JavaScript SDK captured a 5,048.8 ms edit-to-visible
sample on the previous `c576ea7d3` implementation. The sender submitted through
node 3; node 1 ultimately delivered the hint to the receiving SDK.

| Boundary | Milliseconds after edit invocation |
| --- | ---: |
| Committed identity queued on node 3 | 832.3 |
| Ready dispatch begins | 1,021.8 |
| Pending metadata read completes | 1,097.9 |
| Presence lookup begins | 1,175.2 |
| Shared ready-wave deadline aborts dispatch | 1,226.4 |
| Durable repair rediscovers the identity on node 1 | 4,944.1 |
| EVENT write on node 1 | 4,944.6 |
| Receiving SDK exposes updated content | 5,048.8 |

The one-second deadline covers the entire scheduling wave, so this call inherited
only its remaining budget. On failure, the old worker dropped the known identity.
Node 3's intervening repair scans could not recover work owned by another node;
the owner rediscovered it roughly 3.7 seconds later. Another captured sample used
the same path and took 4.20 seconds. The driver recorded no event-loop delays over
its 150 ms observation threshold. The trace confirms the server-side recovery gap;
it does not establish what caused the preceding transient scheduling/I/O delay.

These diagnostic traces used synchronous bounded stage logging through a Go build
overlay. Logging can perturb timing; the maximum is a causal diagnostic sample,
not a controlled before/after performance baseline. The diagnostic period also
included building the next probe; it was not an isolated performance measurement.
Later diagnostic builds buffer at most 20,000 rows per node and flush at shutdown. No diagnostic hooks are compiled
into the ordinary product build. Client/server wall-clock correlation has small
clock-sampling differences; SDK duration percentiles use monotonic time.
The earlier untraced 3.1/7.9 second incidents cannot be assigned this exact cause.

## Repair and resource bounds

A ready entry now retains one retry flag alongside its existing body-free identity.
A failed call is eligible for one retry only when it returns a deadline error and
its shared wave has expired, while the supervising context remains live. This is
captured at call return, before joining other lanes. The retry uses the existing
bounded queue and receives a new scheduling wave after normal scheduler fairness.

Dependency errors while the wave is live, repeated wave expiration, queue overflow
and shutdown still fall back to durable recovery. Paging preserves the spent retry
flag. A queued newer committed version wins over an older retry and starts with its
own retry allowance. No payload, recipient list, extra worker, timer, independent
retry map or per-channel goroutine is retained.

The existing limits remain: 1,024 queued identities, at most four joined dispatch
lanes, eight visits of four pages per one-second ready wave, and bounded durable
repair turns. Retry can add one more wave-expiring attempt per queued version;
under sustained overload it can consume queue capacity and still fall back to the
scan. It does not promise a one-second delivery deadline. Durable progress and
version fences remain authoritative; duplicate hints remain advisory and safe.

CMD restrictions, HTTP/SDK contracts, history and conversation merge behavior are
unchanged. No SDK source change is required.

## Validation

Two completed SDK runs checked 512 edits each:

| Build | SDK-visible P95 | P99 | Maximum | Correct contents |
| --- | ---: | ---: | ---: | ---: |
| Ordinary product binary | 600.7 ms | 1,174.5 ms | 1,374.6 ms | 512/512 |
| Buffered diagnostic overlay | 623.3 ms | 714.1 ms | 764.1 ms | 512/512 |

The diagnostic run retained 11,171 rows from all three nodes with zero dropped
rows. It had no ready deadline failures, so that run alone does not exercise the
new retry branch. Deterministic regression tests establish the retry behavior;
the real SDK runs check the complete integration and content merge. Neither run
recorded a receiver sample above two seconds or a driver event-loop delay above
150 ms. These finite samples do not establish that every long-tail cause is gone.
No other task-owned test or build workload overlapped these two SDK measurements.

The unchanged three-node app pressure harness also passes (GOMAXPROCS=6):

| Check | Result |
| --- | ---: |
| 1,024 healthy edits across 16 concurrent channels, API P99 | 400.5 ms |
| 1,280 distinct pending identities after blocked delivery | All retained |
| Proven lower bound of identities overflowing the ready queue | 155 |
| Time to drain all 1,280 after unblocking | 45.4 s |
| 100,000-member offline-group task completion | 68.1 s |
| All 16 small-channel tasks complete while that group is still pending | 1,204.8 ms |

The large fixture measures durable subscriber paging and notification progress,
with zero online clients. The author joins through HTTP; other subscribers use
real authority-routed counted batches, without constructing their UID conversation
directories. This does not qualify 100,000 simultaneous online recipients. The
preceding candidate measured 39.5/62.0 seconds for backlog/group completion; these
runs show scheduling and recovery still work, not a throughput improvement claim.

Focused runtime/registry integration tests with the race detector and the app's
single-node cluster HTTP edit flow and non-Leader hint delivery tests pass.
The named `flow-doc-contracts` check passes (81 valid FLOW documents; nine existing
advisory warnings). The runtime race link step emitted the existing Darwin linker
LC_DYSYMTAB warning; the test process completed successfully.

The two wave-expiration regression tests were run before the fix and failed
because the first expired wave left zero pending identities. After the fix they
verify recovery without a discovery source and a maximum of one retry under
persistent expiration. Additional integration cases verify no retry after a parent
deadline and error classification before joining a slower lane.
Unit tests cover live-wave dependency errors,
newer-version coalescing, duplicate notifications and retry state across paging.
Existing tests cover queue capacity/body retention, fairness, joined concurrency,
shutdown and repair recovery.

Reproduce the focused regression:

```sh
GOWORK=off go test -race -tags=integration ./internal/runtime/messageupdates ./pkg/goroutine -count=1
```

The runs use a diagnostic copy of the retained
[SDK pressure driver](assets/message-edit-sdk-pressure.cjs), adding per-edit
boundary timestamps, driver event-loop observations and shutdown trace collection.
The same workload can be rerun using the retained driver with a normal `go build`
server binary and the unchanged SDK bundle from
`a038f874230a6fe5201c2faae61b6f2b20cb9ebc`. Set `WK_EDIT_SERVER_BIN`,
`WK_EDIT_SDK_ROOT`, `WK_EDIT_PRESSURE_REPORT` and `WK_EDIT_MIN_INTERVAL_MS=1000`.
It runs three real processes, 256 hash slots, eight physical Slots, three replicas,
32 authenticated SDK clients and 16 concurrent channels with 32 edits each.
Each edit waits for the current, non-stale receiver content before continuing.
The pressure harness's PASS checks correctness, not a latency SLO.

Machine-readable results, source and binary hashes, selected correlated traces,
and validation receipts are retained in the [evidence file](2026-09-15-message-edit-wave-deadline.json).
Raw local diagnostics remain under `.tmp/message-edit-tail/`; only diagnostic
build overlays contain the tagged tracing code. All owned server processes and
temporary cluster directories are cleaned up by the harness.
