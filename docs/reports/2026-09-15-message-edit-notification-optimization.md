# Message-edit notification optimization

Date: 2026-09-15. Implementation and bounded local validation following the
[pressure baseline](2026-09-15-message-edit-pressure.md). This is not a release
or production capacity qualification. The SDK source remains the merged
`a038f874230a6fe5201c2faae61b6f2b20cb9ebc` revision.

## Evidence and changes

The stage diagnostic uses three real cluster nodes and the same authoritative
storage, subscriber, Presence and hint ports as the application. Only test wrappers
record time. Across 32 person messages and one 1,024-member offline group, the
baseline's 41 dispatch calls spent 2.860 of 3.894 seconds in durable progress
commits (73%). Presence/hint time averaged 19.23 ms. The UID-list adapter called
one point lookup per UID even though its existing target adapter supports batching.

The repair makes two product changes:

- Resolve UID lists in pages of at most 256 inputs, group by the complete observed
  authority target, and reuse the existing leader-batch endpoint lookup, including
  stale-target retry and explicit failure propagation. A regression test proves
  128 UIDs on one remote leader use one RPC instead of 128. Duplicate UID result
  multiplicity, offline entries, identity validation and whole-call failure are
  preserved. Presence/hint time in the stage probe fell to 0.71 ms; progress
  commits remained approximately 67 ms.
- Join at most four independent notification dispatches at once, for both the
  committed-ready path and durable repair. Ready work retains its 1,024-entry
  body-free queue and eight visits of four pages per one-second wave. Repair
  retains 32 pages per four-second turn and 16 per target. Reserving each selected
  target's first call before sharing later-page tokens preserves the visited
  cursor prefix. Shutdown/restore joins the lanes. Each worker has at most four
  dispatch lanes, and existing Presence leader fanout is bounded to four per call.

No read barrier, Raft durability, progress CAS, message-update wire format or
CMD protection changes. Newer edits remain protected by the existing version and
previous-cursor conditions. Hint work stays recoverable through durable scanning.

An intermediate run with only UID batching and ready concurrency still needed
98.96 seconds to recover the backlog. This identified durable scan serialization
as a remaining bottleneck; the final change covers that path too. That exploratory
run overlapped a short focused regression test near the transition to the large
fixture; final measurements ran without another validation workload.

## Final app pressure result

Same host and harness: Darwin/ARM64, Go 1.25.11, three actual app nodes in one Go
process, GOMAXPROCS=6, 256 Hash Slots, eight physical Slots, three replicas.

| Measurement | Baseline | Candidate |
| --- | ---: | ---: |
| Drain 1,280 distinct pending edits | 99.37 s | 39.51 s |
| Meet original 90-second recovery target | No | Yes |
| Finish one 100,000-member offline group's pending task | 144.75 s | 61.99 s |
| Finish all 16 small-channel tasks while large task remains pending | 2.07 s | 1.28 s |
| Healthy edit handler p95 / p99 | 294.6 / 334.5 ms | 312.9 / 349.0 ms |

All 1,280 pending rows existed before the test released dispatch; at least 167
identities overflowed the ready queue. Every pending row eventually cleared.
One unsuccessful released dispatch attempt was counted and recovery still drained;
the pressure harness does not capture that attempt's error reason. All expected
HTTP edit responses passed. This does not demonstrate lower edit API latency.

The 100,000 members are offline, persisted via authoritative counted subscriber
batches; only the author has the complete HTTP membership setup. This isolates
notification paging and does not qualify full group creation or 100,000 online
connections. Pending completion is not SDK content visibility. HTTP-handler
measurements exclude the network. Whole-process CPU fell from 126.64 to 92.98
CPU-seconds; peak RSS was 647.3 versus 639.2 MiB, including the three nodes and
harness. These short runs do not establish steady-state memory behavior.

## SDK validation method

The experiment uses 32 isolated SDK VM clients, 16 channels, three real server
processes, token-authenticated WebSockets and the sample BFF. Each channel performs
32 edits and waits for the receiving listener. Final content, conversation preview,
unread count and ordering are checked; browser rendering is outside the measurement.

An exploratory repeat failed with `content_epoch_conflict` before transport. A
minimal SDK reset experiment reproduced the same rejection when a caller retained
an old acknowledgement that had been marked stale, while editing the replacement
from the update listener succeeded at the same epoch. The old pressure driver
retained such acknowledgement objects instead of following the sample UI's current
message. It also timestamped stale notifications as visible. The corrected driver
uses the latest listener object, waits for calibration, checks final non-stale
content and records only the first valid version arrival. It does not suppress
write errors or weaken the SDK's conflict guard. The failed live sample lacked
object-state tracing, so this is a demonstrated driver flaw, not proof of the exact
untraced failure's cause. Both the failed receipt and later tracing are retained.

Comparisons below use the same corrected driver. The paced case requires at least
one second between a channel's edit invocations; it remains closed-loop and may
slow further while awaiting a recipient. Calibration wait is recorded separately.
The uncapped run exercises faster closed-loop demand. Earlier exploratory SDK
numbers use the old driver and must not be used as the final latency comparison.

| Corrected-driver run (512 edits each) | BFF HTTP p95 / p99 | SDK visible p95 / p99 | Visible maximum |
| --- | ---: | ---: | ---: |
| Baseline, minimum 1,000 ms per channel | 252.0 / 295.1 ms | 631.9 / 695.9 ms | 734.0 ms |
| Candidate, minimum 1,000 ms per channel | 240.0 / 594.3 ms | 497.7 / 1,520.0 ms | 3,091.3 ms |
| Candidate, no additional interval | 260.3 / 337.6 ms | 626.9 / 783.2 ms | 946.2 ms |

All three runs passed content, final-preview, unread/order and background-error
assertions. None needed a measurable sender calibration wait above 0.025 ms.
The candidate paced run has four samples over two seconds, all at version 7.
EVENT arrival was 2,381–2,987 ms after edit invocation; subsequent feed requests
started about 100 ms later and completed in 1.2–5.7 ms. This places the observed
wait before the receiving EVENT callback, but alone does not separate server
scheduling/transport from a paused SDK driver event loop. The p95 improvement
must not conceal the worse p99. The earlier 7.9-second baseline sample still lacks
a phase trace; it cannot be declared fixed or assigned this sample's cause.

Two additional paced diagnostic repeats passed another 1,024 edits. Their maximum
visible latencies were 554.1 and 850.0 ms; neither reproduced a two-second sample
or an event-loop interval over 150 ms. Resource sampling took at most 20.2 and
22.6 ms. These later samples cannot rule out a driver pause in the earlier untraced
window. The tail remains unresolved; this change is not evidence to approve an
SDK release on a strict end-to-end latency target.

## Validation and source identity

Regression tests first failed on per-UID RPC amplification, serial ready/repair
dispatch, and the new lanes over-declaring the old singleton task. The final lanes
use the separate fixed-catalog `message/update_dispatch` burst identity, while
`message/update_worker` remains a singleton. The gated concurrency test also
checks registry accounting. This final accounting-only correction was made after
the main latency experiments; benchmark and delivery source hashes are separate
in the receipt rather than implying byte-identical binaries.

The following checks passed:

- Full unit packages for cluster adapters, delivery, Presence, users and messages.
- Presence unit/integration tests under the race detector.
- Message-update worker and goroutine registry unit/integration tests under the
  race detector, covering four-lane bounds, page/cursor budgets, stop/restart,
  durable repair and cold-slot fairness.
- Final app tests for single-node cluster HTTP editing and hints through a
  non-Leader API node.
- Managed-goroutine source contract; the policy-defined `flow-doc-contracts`
  check; generated FLOW index; JavaScript syntax and actual SDK workloads.

The prior process SIGKILL/CAS/read-recovery baseline remains in the earlier report;
this follow-up does not add new crash/restore acceptance evidence. There are no
cloud purchases, published packages, PR merges or SDK source modifications.

Raw successes, the failed exploratory SDK repeat, stage timings, source and
artifact hashes, frozen navigation digests, and focused test outputs are in the
[companion receipt](2026-09-15-message-edit-notification-optimization.json).

The final delivery binary also passed another 512 uncapped SDK edits after the
registry accounting correction: visible p95/p99 654.4/758.2 ms, maximum 944.6 ms;
BFF HTTP p95/p99 261.0/327.5 ms. No two-second sample or background error occurred.
Its exact binary SHA-256 is `06e0cb758376de28c2e8e94175666fc0a9f2e98637fea026d692630ad425a299`.
This final success does not erase the earlier paced long-tail observation.

## Reproduction

Run from this worktree. Use separate absolute report paths to preserve baselines.
The retained pressure fixture and SDK driver are opt-in diagnostics, not default CI.

```sh
GOWORK=off go test ./internal/infra/cluster ./internal/infra/delivery ./internal/usecase/presence ./internal/usecase/user ./internal/usecase/message -count=1
GOWORK=off go test -race -tags=integration ./internal/runtime/messageupdates ./pkg/goroutine -count=1
GOWORK=off go test -race -tags=integration ./internal/infra/cluster -run '^TestPresence' -count=1

WK_MESSAGE_UPDATE_STAGES=1 WK_MESSAGE_UPDATE_STAGES_REPORT=/absolute/stages.json GOWORK=off go test -tags=integration ./internal/app -run '^TestMessageUpdateDispatchStages$' -count=1 -timeout=2m -v
WK_MESSAGE_UPDATE_PRESSURE=1 WK_MESSAGE_UPDATE_PRESSURE_REPORT=/absolute/pressure.json GOMAXPROCS=6 GOWORK=off go test -tags=integration ./internal/app -run '^TestMessageUpdateThreeNodePressure$' -count=1 -timeout=10m -v

GOWORK=off go build -o /absolute/wukongim ./cmd/wukongim
WK_EDIT_SERVER_BIN=/absolute/wukongim WK_EDIT_SDK_ROOT=/absolute/WuKongIMJSSDK WK_EDIT_PRESSURE_REPORT=/absolute/sdk.json WK_EDIT_MIN_INTERVAL_MS=1000 node docs/reports/assets/message-edit-sdk-pressure.cjs
```

Omit `WK_EDIT_MIN_INTERVAL_MS` for the uncapped closed-loop case. Build the SDK's
merged bundle before use. Resource sampling includes the three server processes;
SDK-visible timing is measured in the Node VM driver, including its scheduling.
The next diagnostic should correlate edit commit, queue admission, dispatcher
start, hint send, and receiver EVENT arrival on the same slow sample, retaining
bounded driver event-loop evidence. Repeated passing runs alone cannot close the
observed latency tail.
