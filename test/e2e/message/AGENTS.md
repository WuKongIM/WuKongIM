# message AGENTS

This file is for agents working inside `test/e2e/message`.

## Domain Purpose

This domain covers black-box message and conversation behavior for
`cmd/wukongim`.

## Scenarios

| Scenario | Purpose | Run |
| --- | --- | --- |
| `message_updates` | Opt-in concurrent edits and reads with abrupt Channel/physical Slot leader termination, restart, strict public retry codes, CAS/idempotency and final incremental-cache convergence in a 256-Hash-Slot three-node cluster. | `WK_E2E_MESSAGE_UPDATE_STABILITY=1 WK_E2E_MESSAGE_UPDATE_STABILITY_REPORT=/tmp/message-update-stability.json GOWORK=off go test -tags=e2e ./test/e2e/message/message_updates -count=1 -timeout=8m -v` |
| `conversation_qps` | Fixed QPS/P99 and zero-activation release gate plus fixed four-CPU Linux AMD64 mixed attribution with bounded per-second timing/drop buckets and slow-arrival samples, and opt-in mixed/hidden stress diagnosis for both conversation endpoints across single-node and three-node clusters; separate opt-in staircase/pprof capacity diagnosis and three-minute per-case CPU/RPC/trace analysis with optional frozen-baseline comparison and fixed backpressure attribution, alternating 180-second peak confirmation (including a fixed sync-only comparison)/400/420/440 QPS sync refinement (see scenario AGENTS.md, including sustained capacity confirmation). | `WK_E2E_CONVERSATION_QPS=1 WK_E2E_CONVERSATION_QPS_REPORT=/tmp/conversation-qps.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run TestConversationQPSReleaseGate -count=1 -timeout=12m -p=1 -v`<br>Capacity: `WK_E2E_CONVERSATION_CAPACITY=1 WK_E2E_CONVERSATION_CAPACITY_REPORT=/tmp/conversation-capacity/report.json GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_qps -run TestConversationQPSCapacity -count=1 -timeout=20m -p=1 -v` |
| `no_persist` | Prove ordinary transient HTTP sends to WKProto subscribers, receive flags, permission checks, and unchanged history in 256-Hash-Slot single-node and three-node clusters. | `GOWORK=off go test -tags=e2e ./test/e2e/message/no_persist -count=1 -timeout 3m -p=1` |
| `single_node_send` | Prove WKProto `SEND -> SENDACK`, person membership establishment, and zero repeat membership writes after `directory_ready` in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/single_node_send -count=1` |
| `javascript_web_quickstart` | Prove the published localhost-BFF JavaScript quickstart completes bidirectional durable messaging, SENDACK/receive, disconnect, reconnect, and offline sync in Chromium against a real 256-Hash-Slot single-node cluster with Token auth enabled; HTTP readiness precedes browser CONNECT with BFF-issued credentials. History must contain the offline message after bounded BFF projection recovery and the UI must display it once regardless of live/sync arrival order. Failure summaries include only capture bounds and fixed-spec line numbers. Failure evidence is bounded to three PNGs of at most 2 MiB each. | `WK_E2E_DOCS_JAVASCRIPT_WEB=1 GOWORK=off go test -tags=e2e ./test/e2e/message/javascript_web_quickstart -count=1 -timeout 10m -p=1 -v` |
| `easy_sdk_jsonrpc` | Prove source-aligned EasySDK iOS v1.1.0 binary/camelCase and Android v1.0.4 text/snake_case JSON-RPC profiles complete bidirectional messaging, acknowledgments, ping correlation, disconnect cleanup, and reconnect through public endpoints on a 256-slot single-node cluster without claiming SDK artifact execution. | `GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_jsonrpc -count=1 -timeout 2m -p=1` |
| `easy_sdk_docs_release` | Compile the literal bilingual Web EasySDK tutorial against an integrity-pinned npm package and prove Chromium messaging, cleanup, reconnect and connection deadlines on a 256-hash-slot single-node cluster. | `WK_E2E_EASYSDK_ARTIFACTS=/absolute/artifact-directory GOWORK=off go test -tags=e2e ./test/e2e/message/easy_sdk_docs_release -count=1 -timeout=5m -p=1 -v` |
| `conversation_directory` | Prove candidate-bounded pagination, activation priority, message-time ordering stability, monotonic badge state, and hide/remove/rejoin transitions through public APIs. | `GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory -count=1 -timeout 2m` |
| `conversation_directory_multi_node` | Prove Channel-Leader-grouped hydration, persisted-head reads without runtime activation, whole-page failure and original-cursor recovery, and UID membership reads through a four-node ingress outside the UID Slot replica set. | `GOWORK=off go test -tags=e2e ./test/e2e/message/conversation_directory_multi_node -count=1 -timeout 3m -p=1` |
| `legacy_conversation_sync` | Use explicit tokenless fixture authentication to prove the deprecated v2.2 `/conversation/sync` projects first and later WKProto messages into exact sender and offline-recipient person/group views in single-node and remote-Leader multi-node clusters. | `GOWORK=off go test -tags=e2e ./test/e2e/message/legacy_conversation_sync -count=1 -timeout 3m -p=1` |
| `webhook` | Prove single-node cluster post-commit callbacks and three-node `msg.before_send` admission, custom codes, mutation, timeout/error policies, and committed history through WKProto and HTTP; also verify authenticated fault handling, bounded per-node overload/recovery, callback counts, and public metrics; build the runnable Go callback and validate its decisions/history against a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/webhook -count=1 -timeout 2m -p=1` |
| `send_permission` | Prove `cmd/wukongim` enforces migrated legacy send-permission decisions through public channel-management and `/message/send` HTTP APIs. | `GOWORK=off go test -tags=e2e ./test/e2e/message/send_permission -count=1` |
| `terminal_disband` | Prove message-usecase permission admission rejects a disbanded source for ordinary, system-UID, and system-device sends, exposes terminal list/pull behavior, and performs no membership fanout. | `GOWORK=off go test -tags=e2e ./test/e2e/message/terminal_disband -count=1 -timeout 2m` |
| `stream_online` | Real JSON-RPC online stream events, person/group cross-node routing, finish/cancel/error, private visibility and offline snapshots in 256-hash-slot single-node and three-node clusters. | `WK_E2E_STREAM_REPORT=/tmp/wk-stream-online.json GOWORK=off go test -tags=e2e ./test/e2e/message/stream_online -count=1 -timeout=3m -p=1 -v` |
| `message_event_stream` | Prove `/message/event` buffers stream deltas in the Slot-leader cache, forwards from non-leader nodes, fails closed after Slot-leader cache loss, proposes one finish batch, exposes public metrics, and survives restart through `/channel/messagesync` event summaries. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_event_stream -count=1 -timeout 2m` |
| `recipient_authority` | Prove committed group SEND has zero recipient membership writes, membership-backed `/conversation/list` still hydrates the user view, and low-cardinality directory/hydration metrics are exposed, with an opt-in 100k subscriber stress path. | `GOWORK=off go test -tags=e2e ./test/e2e/message/recipient_authority -count=1` |
| `medium_recipient_hotpath` | Opt-in higher-fidelity local Cloud Medium gate plus a separate 30-minute, 5,000-channel permission-pressure soak. Both use a real three-node process cluster, WKProto sockets, Raft, Pebble, exact Presence convergence, SENDACK/RECV latency, zero measured membership writes, and bounded public pressure evidence. | Short gate: `WK_E2E_MEDIUM_RECIPIENT_HOTPATH=1 WK_E2E_MEDIUM_RECIPIENT_ENFORCE_ACCEPTANCE=1 GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath -run TestCloudMediumScaledRecipientHotPath -count=1 -timeout 5m -p=1 -v`<br>Soak: `WK_E2E_MEDIUM_RECIPIENT_PERMISSION_SOAK=1 WK_E2E_MEDIUM_RECIPIENT_SOAK_DURATION=30m WK_E2E_MEDIUM_RECIPIENT_GROUP_CHANNELS=5000 WK_E2E_MEDIUM_RECIPIENT_QPS=4500 GOWORK=off go test -tags=e2e ./test/e2e/message/medium_recipient_hotpath -run TestCloudMediumPermissionSoak -count=1 -timeout 40m -p=1 -v` |
| `cross_node_delivery` | Prove a static three-node `cmd/wukongim` cluster delivers person-channel messages across nodes in both directions, including explicit Slot/Channel two-replica topology and an opt-in same-host 2/2-versus-3/3 comparison at 2,000 SEND/s. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cross_node_delivery -count=1 -timeout 2m` |
| `cmd_sync` | Prove ordinary membership and CMD membership/log isolation, explicit CMD binding, `/message/sync` delivery, and `/message/syncack` draining in a single-node cluster. | `GOWORK=off go test -tags=e2e ./test/e2e/message/cmd_sync -count=1 -timeout 2m` |
| `message_retention` | Prove a static three-node `cmd/wukongim` cluster forwards manager retention requests to the channel leader, physically cleans retained local message rows when enabled, and consistently hides retained message sequences after leader restart. | `GOWORK=off go test -tags=e2e ./test/e2e/message/message_retention -count=1 -timeout 2m -p=1` |
| `channel_failover` | Prove a static three-node `cmd/wukongim` cluster preserves Channel quorum-acknowledged messages after one data node stops or pauses with TCP connections retained, rotates bounded scans across ten Slots, fails over affected channel leaders, checks continued writes and exact cross-ingress retries before the paused leader resumes, verifies sending through that node after resume and complete More-driven history pagination, and fails closed for new placement while a required replica is unavailable. | `GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 6m -p=1` |
| `bench_churn` | Prove wkbench identity-swap churn updates real group membership before the replacement UID sends in the next traffic window. | `GOWORK=off go test -tags=e2e ./test/e2e/message/bench_churn -count=1 -timeout 2m` |
| `channel_failover` | Prove a static three-node `cmd/wukongim` cluster preserves Channel quorum-acknowledged messages after one data node stops, fails over affected channel leaders, and fails closed for new placement while a required replica is unavailable. | `GOWORK=off go test -tags=e2e ./test/e2e/message/channel_failover -count=1 -timeout 3m -p=1` |
| `bench_churn` | Prove immediate cross-node Bench Token creation/rotation and exact rejection, including a non-replica, then identity-swap group churn with Gateway Token auth enabled. | `GOWORK=off go test -tags=e2e ./test/e2e/message/bench_churn -count=1 -timeout 2m` |
| `chat_lifecycle` | Prove a real three-node person Channel becomes naturally absent after five idle minutes, reheats through real traffic, preserves sequence/metadata continuity, runs a full version-zero sync after every login, and preserves person/group recipient sequence under cross-ingress bursts. | `GOWORK=off go test -tags=e2e ./test/e2e/message/chat_lifecycle -run 'Test(PersonChannel(NaturalReheat|CrossIngressBurstPreservesReceiveSequence)|GroupChannelCrossIngressBurstPreservesReceiveSequence)$' -count=1 -timeout 9m -p=1` |

The conversation release gate also retains driver GOMAXPROCS, bounded host/driver
counters around stress windows and per-second arrival/drop timing with at most
16 slow samples per endpoint. Workload and acceptance remain unchanged; no
profiles run in release windows.

## Maintenance Rules

The `message_updates` recovery scenario registers bounded process diagnostics
before initial HTTP readiness, covering startup exits as well as workload failures.

The `conversation_qps` scenario also contains the opt-in message-update
three-node diagnostic. Its `WK_E2E_MESSAGE_UPDATE_DELTA_PROFILE=1` variant
preserves all unprofiled windows and captures only changed-delta CPU/allocation
profiles afterward; see the scenario instructions for the fixed comparison.

- When adding a new message scenario, create
  `test/e2e/message/<scenario>/`.
- Give each scenario its own `AGENTS.md` and one primary
  `<scenario>_test.go`.
- If a scenario is added, removed, renamed, or its run command, steps, or
  diagnostics change, update this file and the scenario's `AGENTS.md` in the
  same change.
- Keep one-off helpers inside the scenario directory first. Only promote them
  to `test/e2e/suite` after real multi-scenario reuse appears.

- The Channel failover fixture uses 256 Hash Slots and explicitly disables Token authentication for its existing tokenless readiness client. SDK acceptance keeps Token authentication enabled.
- The `chat_lifecycle` fixture explicitly disables Token authentication for its
  tokenless lifecycle clients; its ordering and reheat results do not qualify SDK authentication.

## User and Channel send bans

`send_ban` verifies independent user/source-Channel restrictions, cross-ingress
cache freshness, WKProto and HTTP admission, atomic changes and CAS.
Run `GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -count=1 -timeout=8m -p=1 -v`.
The scenario writes a JSON report (override with `WK_E2E_SEND_BAN_REPORT`).
The separate opt-in `TestPermissionCallerBaseline` characterizes Issue #977
using fixed independent WKProto sessions, public per-node metric cuts and exact
history across four actual Slot/leader placements. See `send_ban/AGENTS.md` for
the frozen-binary invocation, resource limitations and separate profile phase;
it does not alter the existing 500 SEND/s gate or implement cross-caller cohorts.
Its independent `WK_E2E_SEND_BAN_100K=1` opt-in runs `TestHundredKGroupSendBan`
with a six-minute test bound and writes a `.100k.json` companion report. A setup
timeout is failed evidence, not a smaller-scale pass.
The 100k artifact retains setup cardinality, observed positive-control recipient
processing and exact complete history; main-scenario startup failures include
bounded process diagnostics.

The medium recipient pressure fixture explicitly disables Token authentication
for its existing tokenless clients, verifies that rendered setting, and reports
outbound Raft-lane payload bytes separately from total transport bytes. Missing
per-node coverage remains null; these bytes exclude framing/network overhead.

Chat lifecycle burst checks also retain exact per-sender message and session ACK
order for person/group traffic and emit `WKRC-SESSION-ORDER` evidence.

The send-ban matrix covers two already connected devices across all additional
source Channel types (3–12), with exact committed-history rejection checks.

Permission pressure evidence separately counts domain admission busy responses,
including local reads; a successful transport RPC cannot hide a rejected fact read.

The non-replica send-ban companion records missing/ban/allow transitions and
exact successful history with a 1h auxiliary cache; the trusted-device control
must also observe the Channel ban. Permission soak uses and verifies the current
product default of 128 append workers; the separate mixed-recipient gate remains
at eight, and historical eight-worker failures remain failed evidence.

Send-ban delivery evidence keeps a live receiver across user/Channel restrictions
and quorum loss, with positive controls and exact histories. Gateway distribution
evidence records one/many UID and Channel bursts, actual batch histograms and
per-request allow/deny alignment; these functional runs are not performance gates.

The send-ban remote admission fault uses three processes and two voters per
Slot to keep a non-replica ingress alive while UID quorum is lost. It records
192 failed-closed requests, bounded execution, busy responses and exact recovery.

The send-ban leader-transfer companion moves the user and Channel policy Slots
through public Manager operations, keeps concurrent sends closed, then verifies
cross-ingress unban/re-ban freshness and complete successful history.

Failure runtime diagnostics retain existing bounded gateway connection-close
reason counters to distinguish server overload closure from other EOF causes.
These snapshots do not claim per-shard queue telemetry or alter acceptance.

The permission-soak driver timing probe is opt-in and E2E-only. It separately
records scheduling lag, client enqueue time and socket write time, freezing
its bounded ten-second ring before failure diagnostics. Socket completion is
not server receipt; no workload or acceptance changes accompany this probe.

The send-ban parallel companion uses five real nodes and disjoint two-voter
Slots to observe concurrent permission envelopes on two remote leaders under
quorum loss. It retains direct admission-counter continuity, failed-closed
HTTP results and exact recovery history; its timings are not performance gates.

The permission-soak stall diagnostic records bounded oldest SENDACK ordinals
per connection and existing public channel-runtime LEO/HW snapshots. It is
opt-in, independent of the producer, canceled/joined before failure diagnostics,
and does not infer exact append phases from non-atomic observations.

Permission-soak failure stack filtering includes independent replication and
storage/commit workers with a fixed 256-KiB output bound; all-node coverage and
truncation must be checked before attributing a wait to one component.

The same diagnostic can retain Pebble background stacks with explicit input
and output truncation markers, and opt-in raw replication-stage histograms
from the two existing boundary scrapes. These remain sampled completed-stage
evidence, not a request trace; incomplete collection never becomes zero cost.

Permission-soak storage history is opt-in and reuses existing pressure scrapes;
it retains fixed metric/store selectors and 180 snapshots, with missing/error
states and sample times preserved. Host I/O correlation remains diagnostic.

Existing pressure/prime failure scrapes retain exact permission count,
duration count/sum and inflight families without additional requests or buckets.
These cumulative snapshots must not be presented as measured-window deltas.

The single-node SEND smoke registers a device Token through `/user/token` and
connects with it while retaining production-default gateway authentication.

The same fixed experiment has an opt-in candidate mode:
`WK_E2E_PERMISSION_COHORTS=1 WK_E2E_BINARY=/absolute/frozen/candidate WK_E2E_PERMISSION_COHORT_REPORT=/absolute/cohorts.json GOWORK=off go test -tags=e2e ./test/e2e/message/send_ban -run '^TestPermissionCallerCohorts$' -count=1 -timeout=4m -p=1 -v`.
It requires lower burst RPC/local-envelope and fresh-barrier counts, unchanged
sequential counts, independently drained public cohort ownership metrics,
completed ban/unban controls and exact full history. Historical baseline mode
remains executable with its frozen old binary. Source/build identity, whole-node
resource cuts and diagnostic limits remain the same; capacity gates are unchanged.

The quorum-loss admission companion keeps 192 failed-closed sends but separates
arrivals by 5 ms so sealed cohorts actually accumulate. Require positive receiver
or ingress-cohort busy, record both scopes separately, sample both hard ownership
bounds and drain to zero, then verify exact complete recovery history. This fault
fixture never changes the 500 SEND/s performance gates.

`WK_E2E_PERMISSION_STAGES=1` optionally retains count/sum/bucket samples for six
fixed public permission, append/wait, replication and storage-commit histogram
families in the existing before/after node scrapes. It adds no scrape or product
hook. Keep full label identities and absent series; histogram bucket bounds
describe scoped completed populations, not per-SEND spans or time spent waiting
before stage entry. Use the flag identically for old/new comparisons.

`WK_E2E_PERMISSION_TIMELINE=1` enables the bounded same-remote-Slot diagnostic
in the fixed baseline/cohort experiment. It preserves matched client timing and
queries all three public diagnostics endpoints after the 64-SEND burst. See the
scenario bounds and required stage-coverage failures; timings with tracing are
diagnostic and never replace the unchanged unprofiled qualification.

The fixed permission comparison optionally uses a prebuilt Darwin native CPU
probe for its three owned PIDs. The scenario instructions require getrusage
calibration, source identities, raw counters/timebase, same-process interval
validation and all retained old/new outcomes. Whole-node CPU integrals include
background work; this helper never changes the workload or performance gates.

The timeline negative receipt probe intentionally fails a post-window public
query, retaining all completed ACKs, safe partial outcomes and history. CPU
evidence failure paths also join ownership sampling and preserve partial receipts.

The opt-in `TestPermissionSequentialDiagnostics/same-slot-remote` separately
captures 64 sequential request timelines or six bounded three-node CPU/allocation
profiles during 256 additional sequential SENDs. See the scenario instructions
for mutually exclusive flags, sampling/overlap limits and exact history checks.
These diagnostic fixtures never qualify the unprofiled performance gates.

The opt-in `TestPermissionSequentialPrefixDiagnostics` keeps all four original
placements in order and diagnoses only `two-slots-one-remote-leader`. Set
`WK_E2E_PERMISSION_PREFIX_DIAGNOSTICS=1` with exactly one of the existing
sequential timeline/profile flags, a frozen product and a report path. Profile
mode retains 417 exact messages in that placement and 161 in the others;
timeline mode retains 161 in every placement and queries only its 64 sequential
requests. Existing capture, privacy, history, counts and policy bounds apply.
`WK_E2E_PERMISSION_PROFILE_OWNERSHIP=1` additionally runs the unchanged bounded
20-ms ingress ownership sampler only during the separate profile traffic phase,
retaining its observations and joining it at traffic completion. Paired captures
with that option absent diagnose the full public metrics scrape cost; they do
not subtract overhead or replace original unprofiled p99/whole-node CPU gates.
Missing old-product timeline stages remain independent failed coverage evidence.
