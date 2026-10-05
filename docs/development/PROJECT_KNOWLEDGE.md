# Project Knowledge

Person-directory projection must emit one membership for a self-channel and two
for distinct participants. Duplicate UID/channel identities reject the complete
physical-Slot membership command, leaving MQTT inbox preparation pending and
blocking unrelated person channels in that batch. Keep completion results aligned
with each task's distinct participant range; retained pending tasks recover through
the existing projector after an upgrade.


Keep only stable, cross-module facts that prevent incorrect designs or unsafe
operations. Repository rules belong in [AGENTS.md](../../AGENTS.md), domain terms
in [CONTEXT.md](../../CONTEXT.md), and module navigation in the applicable
[FLOW.md](FLOW_INDEX.md). Code, schemas, and tests remain authoritative for behavior.

Do not append task histories, individual benchmark results, release/version
inventories, or implementation walkthroughs here. Record those in the relevant
specification, runbook, report, or module documentation; link to them when needed.

## Cluster and authority

- MQTT aggregate storage counts one original plus reserved future replay body per
  protected source on each storage replica, independently of Session quotas.
  Funding covers all voters/learners before original dispatch; capacity receipts
  never vote for durability. Startup/restore debt closes admission until every
  storage node registers. Cancellation and retirement require exact durable
  evidence, including unknown outcomes. This is logical responsibility capacity,
  so operators still need WAL/compaction/metadata disk headroom. Only typed native
  controls are exempt; ordinary SyncOnce flags grant no exemption. See
  [the capacity contract](../specs/mqtt-storage-capacity.md).

- A Will origin journal may seal an issued same-boot attempt only after a
  synchronous whole-invocation proof that no original was submitted. Earlier
  ambiguous routing/storage effects invalidate the proof; positive committed
  receipts still win. Generic pressure, absent receipts and timeouts grant none.

- An MQTT Started Will can finish Rejected/Sealed only after its exact owning
  node durably forbids unissued append and current policy explicitly denies it.
  The terminal CAS keeps the original executor tuple/frozen body and reserves
  no successor, so a full journal does not block this safe discharge. Missing
  receipt, lease expiry, failed reads and issued/legacy/lost journals grant none
  of that proof. Phase 4 requires matched runtimes/tools and pre-feature rollback
  data; see [the contract](../specs/mqtt-will-sealed-rejection.md).

- MQTT original reads must prove anchor coverage through the complete accounted
  cursor, including a single-position exchange recovery. The compound plan/page
  port shares fresh Channel authority only within one call; final Session Owner,
  receive authorization and outer placement checks remain independent.

- MQTT subscription admission derives dependency contexts from the admitted
  Owner operation, preserving its cancellation and the minimum caller/operation
  deadline; synchronous scope checks separately cover the parent. Caller context
  values must not be assumed to survive this boundary. Diagnostic tracing must
  carry its own marker explicitly without replacing the Owner context or its
  fencing/cancellation behavior.

- All deployments use cluster semantics, including a single-node cluster.
  Distinguish 256 physical hash slots from logical Slot Raft Groups and node-local
  reactor partitions. Shipped initialization creates 12 logical groups; omitted
  or zero `cluster.initial_slot_count` derives one. Existing clusters use the
  persisted Controller count; changing this setting does not resize them.
- Slot Raft randomizes election waiting from `ElectionTick` through
  `2*ElectionTick-1` ticks. Defaults (50 ms, 40 ticks) therefore allow 2–3.95 s
  before campaigning; failover tests must cover this window plus bounded voting
  and durable-apply time rather than treating three seconds as an upper bound.
- Node snapshot application serializes watches and readiness probes, rejecting
  older logical revisions before maintenance, placement or task side effects.
  Watch notifications trigger a current Controller read rather than replaying
  queued task progress.
  Reconciled routes, Slot readiness and committed health publish before initial
  task setup. After startup, task changes wake the existing background owner
  through one coalesced notification; it reads fresh Controller state and retains
  serialized execution/cancellation. Task writes cannot hold snapshot publication
  or readiness probes, and Stop joins the owner before storage closes.
  Equal revisions still refresh health and Controller leadership; logical revision
  does not version every health observation.
- Node Stop and failed-start rollback release owned proposal/task adapters and
  Slot status readers with their runtimes. A subsequent Start rebuilds them;
  caller-injected adapters remain borrowed. Quorum RPC gateways belong to one
  transport server and must be registered again when that server is recreated.
  Recreated Slot proxies replace pending handlers before server registration;
  registering the previous handlers first would retain a closed metadata store.
- Controller Raft, state-sync and control/task ingress can arrive during startup.
  Resource publication and ingress pointer reads use the same state lock,
  released before Raft queue waits or FSM snapshots. Runtime Start/Stop calls
  remain sequential; inbound transport need not wait for Start to complete.
- Controller owns placement intent; observed Raft leadership is authoritative.
  `PreferredLeader` is not proof of the current leader, quorum, or replica health.
  Missing live evidence remains unknown. Controller planning writes use Raft proposals.
- Slot replicas own metadata; Channel placement selects message-data replicas.
  Neither local database presence nor a loaded Channel runtime proves ownership.
  UID metadata reads use the current Slot leader, even on non-replica ingress nodes.
- Distributed UID authority is fenced by hash slot, Slot ID, leader node, leader
  term, and config epoch. Node-local `AuthorityEpoch` is not a cross-node fence.
  Channel recovery compares the complete epoch/leader-term/fence authority.
- Static node addresses are a discovery baseline overlaid by Controller metadata.
  Advertised endpoints must be unique and reachable; wildcard listen addresses
  are not peer addresses. Dynamic join adds a data node; Slot allocation needs
  explicit onboarding and does not automatically change Controller voters.
- Physical hash-slot migration is disabled by default and requires
  `WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED=true`. Node scale-in must prove
  `safe_to_remove=true`, including Channel replicas, leaders, and migration tasks,
  before infrastructure removal. Manager does not perform Kubernetes scale-down.
- `/healthz` is liveness; `/readyz` includes write admission and new-Channel
  placement capacity. Existing Channels can still commit while full readiness
  fails. A two-replica group needs both replicas for writes.
- Slot proxy handlers register through `pkg/cluster.Node.RegisterRPC`.
  Node RPC identities must remain distinct from Channel replication services.
- Slot log inspection is separate from FSM decoding and application. A decoded
  command without an inspection view reports `unsupported`, not `corrupt`.
  Unknown Slot command types and unsupported Slot/Controller envelope versions
  also report `unsupported` without changing FSM rejection. Registered Slot
  commands have encoded inspection fixtures that check catalog coverage and
  message-body/token redaction; new command types need corresponding fixtures.

## Message durability and recovery

- Persistent SENDACK requires the exact proposal to be crash-safe locally and on
  an intersecting voter write quorum. Learners do not vote. Never acknowledge
  before synchronous durability or expose message append `NoSync` as configuration.
- Timeout or cancellation after admission is an unknown outcome, not proof that
  nothing was written. Retry with the original message identity. Raw idempotency
  indexes may include uncommitted proposals; success requires an exact payload
  and identity match in the current Leader's committed history.
- Failover preserves every observed suffix until exact chain evidence resolves
  it. Highest LEO, stale HW, or an unavailable replica cannot authorize truncation.
  Recovery converges compatible prefixes through bounded append-only repair,
  obtains fresh quorum proof, and writes a current-authority barrier before
  admitting writes. Installing authority under a migration fence does not clear it.
- Committed reads use current authority and recovered quorum HW. Cold or stale
  runtimes require bounded authoritative recovery; local `HW == LEO` alone proves
  nothing. Authority, corruption, and I/O failures must not become empty or
  successful partial history. A zero committed frontier is handled before a
  storage API whose zero maximum means unbounded.
- Controller and Slot snapshot restore starts from snapshot data, then replays
  committed entries after the snapshot index. A later stored applied watermark
  must not skip replay. WAL recovery may repair only an incomplete physical tail
  record in the newest segment; other corruption fails closed.
- Slot startup can stream verified snapshot chunks into bounded metadata
  batches before registering the Slot. A global physical-Slot pending marker
  fences interrupted installs; completion atomically publishes the snapshot
  watermark and clears the marker. Retry reinstalls the complete snapshot.
  Runtime snapshot replacement remains atomic. Recovery progress logs use
  `slot.recovery.progress`, never record keys or credentials, and throttle
  same-stage updates to five seconds; completion refers to the startup
  committed suffix, not gateway admission. A local applied index alone still
  does not authorize skipping snapshot recovery.
- Certified Slot startup additionally verifies physical metadata sequence continuity,
  database incarnation, cluster/node/Slot ownership, snapshot content and the exact
  Raft entry/configuration history. Business mutations and their proof share one
  batch; unclassified or older-writer mutations invalidate reuse. Known disjoint
  Slot writes invalidate only their own stale/absent proof, so multi-Slot upgrades
  and snapshotless neighbors do not cause repeated full recovery. Migration
  maintenance or uncertain ownership keeps global invalidation. An invalid
  startup seal durably clears even unopened Slots before resealing. Compaction publishes
  the replacement anchor after its durable Raft snapshot. Logs distinguish
  `checkpoint_reuse` and `checkpoint_fallback`. Snapshotless legacy recovery
  retains its watermark behavior; it gains no certificate until a real snapshot
  establishes a new anchor. Small E2E success does not satisfy the three-million-
  user Linux 2/4 GiB restart gate.
- The 2026-09-27 three-million-user/device gate reproduced baseline startup OOM
  at 2 GiB. Streaming recovery passed both caps; certified reuse used about
  126–128 MiB RSS and completed Slot recovery in 0.22–0.24 s, while full ready
  still took about 5.4 s. Normal, interrupted-install retry and complete row
  inventory passed. See `docs/reports/2026-09-27-startup-recovery.md` for scope,
  binary receipts, the fixed Pebble large-batch header pitfall and compatibility limits.
- Controller WAL prefix deletion must preserve a durable, verifiable CRC starting
  point for the first retained segment. Rolling CRC state crosses segment
  boundaries in the legacy format; intact retained bytes alone cannot validate
  from a zero seed after prefix removal. Versioned independent headers prevent
  that dependency for new segments. Legacy prefix compatibility requires a
  matching checked snapshot, durable metadata, and complete CRC-verified suffix;
  the exact verified header anchor must be durable before append or prefix deletion
  so interruption cannot invalidate recovery. It never rewrites checksums or
  repairs arbitrary corruption. Materialized
  JSON cannot replace Raft authority.
- Slot FSM batch conflicts preserve Raft order: a rejected range is subdivided
  with each prefix durable before its suffix executes. Unknown physical commit
  outcomes are not automatically retried as definitely unwritten operations.
- `pkg/db` owns node-local storage: `message` owns Channel logs and `meta` owns
  hash-slot metadata. Borrowed decoder/iterator buffers must become owned before
  escaping or advancing the iterator; checksum and corruption checks remain intact.

## History, conversations, and commands

- Runtime metadata reads use an 8,192-row / 8 MiB storage cache, invalidated by
  Hash-Slot generations on mutation entry/exit, including failed writes and
  snapshot/restore chunks. Hits/fills are disabled while mutations are active;
  late misses cannot republish stale rows;
  replica slices remain caller-owned. This never replaces Slot/Channel authority
  or permission checks; unrelated Hash Slots retain their cache entries.

- MessageDB sequence reads decode independent payloads and transfer them through
  compatibility and Channel adapters. Consuming conversions must not reuse the
  source DTO; returned payloads remain independent across reads and store closure.

- `wukongim_conversation_read_stage_duration_seconds` uses fixed scope/stage/result
  labels: list/sync `handler` includes response write but excludes outer middleware
  and client decode; `response` includes DTO/JSON work on successful usecase reads.
  Persisted/committed heads expose metadata, heads and attempted edit overlay;
  `edit_slot` measures serving barrier and storage/assembly/authority recheck for
  all edit readers. Totals overlap children and parallel Slot work; do not add
  their sums as request latency. The older directory-list timer keeps its original
  pre-response success boundary. Disabled observers do not read the clock.

- Successful message edits acknowledge durable content/CAS and pending notification
  state, not delivery to every recipient. The bounded ready queue accelerates
  dispatch; overflow/restart recovery uses durable pending scans. Each worker
  overlaps at most four independent notification targets, joins them before
  advancing its scan cursor, and retains the shared subscriber-page budget. UID
  endpoint lists resolve bounded pages through exact fenced target/leader batches.
  A failed ready call that exhausts its shared wave deadline gets one bounded
  ready retry; paging preserves that retry state. Dependency errors and repeated
  expiration use durable scanning, so transient scheduling exhaustion does not
  immediately force known work through cold-slot discovery. Notification
  checkpoint latency, EVENT arrival and SDK-visible content latency are distinct
  measurements. Large offline membership tests do not qualify equivalent online
  fanout; see the [bounded pressure report](../reports/2026-09-15-message-edit-pressure.md).

- A conversation is a response built from UID-owned membership and Channel
  messages, not a separate durable conversation row or message-time projection.
  `user_channel_membership` stores join/read/delete boundaries, activation,
  tombstones, and source version; CMD membership uses a separate directory.
- Steady-state SEND performs no recipient membership writes. The first persistent
  person SEND durably admits a generation-fenced source projection task; bounded
  asynchronous work establishes both UID memberships. SENDACK does not guarantee
  immediate directory or history visibility. Deletion fences delayed projection.
- Member add/remove changes subscribers before UID memberships/tombstones.
  Partial failure is returned for idempotent caller retry. Valid non-tombstoned
  membership authorizes ordinary history without rechecking subscribers; reads
  still enforce join, deletion, retention, and terminal Channel lifecycle bounds.
- Ordinary history and exact lookup use committed semantics. Conversation list
  and legacy sync deliberately read current-Leader persisted heads/recents without
  activating Channel runtimes or confirming quorum: failed SENDs can appear and
  previews can regress after failover. Any attempted read failure aborts the page;
  clients retain data and retry the original request/cursor.
- `activated_at` records explicit navigation/hide priority, not last-message time.
  Legacy sync sorts its bounded candidates by activation, string Channel ID, and
  type. Canonical list uses its encoded directory cursor; only `done=true` ends a
  pass. Clients own pinning and final display order.
- `read_seq` is a monotonic badge boundary, not a pull cursor or read receipt.
  Badge calculation also uses the user's committed sender sequence and excludes
  SyncOnce recovery positions. A bounded Channel storage proof may reuse complete
  sparse-index absence, never an unread result. SyncOnce staging invalidates it;
  restore discard/import generations fence reuse and closed storage still fails. Sequence minus unread count cannot reconstruct a
  read boundary. Hiding advances `deleted_to_seq` and clears activation without
  removing membership; a newer message can make the conversation visible again.
- `message.PageReader` owns bounded ordinary-page selection, visibility floors,
  SyncOnce filtering, and continuation. Latest reads use bounded reverse scans;
  limits count visible records. Scan adapters transfer all mutable message data;
  PageReader can filter in place and transfer pages through sync. Custom readers
  and wrappers retain defensive copying, including nested JSON event snapshots.
  Exact lookup must not scan only recent history.
  Missing person membership before the first persistent SEND can mean an empty
  page; missing group membership, tombstones, and infrastructure failures cannot.
- Ordinary and CMD logs have separate sequence spaces. Recoverable SyncOnce
  commands require recipient CMD binding before SEND; request-scoped recipients
  do not create discovery membership. `NoPersist` is online-only and supplies no
  durable sequence or offline recovery, including `NoPersist + SyncOnce`.
- Online delivery preserves the sender's entire `Setting` bitset, `Topic`, and
  `Expire` through both durable and transient envelopes into RECV. The receipt
  bit (`1 << 7`) is message metadata, independent of transport RECVACK tracking.
- CMD binding retries preserve existing start/ack boundaries; cross-Slot failures
  can be partial. Missing command logs alone mean empty history. Global CMD sync
  skips authoritatively disbanded sources without acknowledging them; other
  failures preserve the prior ACK generation. The configured command suffix must
  agree on all nodes; changing it does not migrate stored logs or bindings.
- Legacy SEND generates a nonblank `client_msg_no` when absent and preserves
  provided keys byte-for-byte. Read aliases `wk3-legacy-<message_id>` for older
  empty-key records are not SEND idempotency or event-mutation keys. Preserve
  RedDot, SyncOnce, lifetimes, and original timestamps across reads and replication;
  legacy StreamNo is not ClientMsgNo.
- Message edits use separate update APIs and Slot-owned CAS/idempotency with safe
  ReadIndex and durable-apply barriers. Original logs and CMD semantics remain
  unchanged. Post-commit notification identities wake a bounded worker queue;
  durable pending scans remain the recovery authority for overflow, errors and restart.
  SDKs merge content/version/cursor atomically and reset cached version
  comparisons when restore changes `X-WK-Content-Epoch`. See the
  [message-update contract](../specs/message-update-api.md).

- Large-group setup projects ordinary UID memberships in bounded physical-Slot
  commands (128 rows, 256 KiB, 64 KiB UID bytes, two concurrent proposals).
  Logical Hash Slots keep their ownership checks and migration filtering;
  batching must not replace upsert/rejoin semantics with person ensure.

## Delivery and extension boundaries

- [MQTT delivery scheduling](../specs/mqtt-delivery-scheduling.md) retains one
  task per exact activated Owner and uses a fixed bounded cohort. Wake hints
  coalesce without body queues; errors impose a retry floor even under wake
  floods. Fencing cannot discard pending End cleanup. Terminal Stop cancels and
  joins all calls before releasing records; timeout retains that run and grants
  no isolation proof. App fences admission first and keeps dependencies alive
  until join. [DeliveryCoordinator](../specs/mqtt-delivery-coordinator.md) opens
  one stream before admission, recovers old exchanges first and rotates one
  subscription/source per turn through existing Session-Slot pages. Only unfinished
  source scans retain bounded hints; authoritative accounting precedes new sending.
  Quota reply loss is recovered from durable state before isolation/feedback;
  denied accounting ends the exact lifetime after releasing scopes. Three-node
  coverage uses real discovery/accounting and a controlled sink. The
  [connection handoff](../specs/mqtt-connection-delivery.md) registers a task after
  CONNACK and handshake release. Bound ACK wakes after credit release; close
  preserves first intent before fencing/waking and never discards pending End.
  Lost hints rely on idle polling. Real Paho/256-Slot coverage joins automatic
  discovery, accounting, sending, takeover DUP and ACK/close cleanup; subscription
  setup invokes the real usecase directly. [Empty-group preparation](../specs/mqtt-empty-group-preparation.md)
  reuses the hosted Channel initializer only on confirmed runtime absence and
  rereads fresh Slot authority before source protection; denied subscriptions
  create no runtime and ambiguous creation errors never authorize protection.
  [Subscription entry](../specs/mqtt-subscription-entry.md) maps ordered SUB/UNSUB
  replies under one deadline and owner scope. Its request usecase waits only for
  explicit replay/drain pending work (64 attempts, 25ms spacing, 5s total by default).
  Possible mutation/pending intent marks errors Unconfirmed; late/unknown results
  close without a misleading negative or partial batch ACK. Real Paho verifies
  group SUB/UNSUB and ACK after removal. Inbox future source admission, offline
  scheduling and product composition remain outstanding.

- Subscriber join identity uses optional column 4 and table 5 System 1 per-hash-Slot
  allocation high water. Legacy empty rows mean incarnation 1; new joins allocate
  above 1 atomically with membership/count changes. Repeated adds preserve it;
  rejoin and channel recreation cannot reuse it. Channel mutation versions and
  UID projections are not receive-authority incarnations. Matched writers/tools,
  native snapshots and JSONL preserve live identities plus deleted high water;
  v2 migration retains legacy 1. See [the contract](../specs/mqtt-member-incarnation.md).
  [Receive authority](../specs/mqtt-receive-authority.md) uses RPC 106 kind 19: a fresh
  Slot barrier and one pinned channel/member/sequence snapshot, bypassing the live
  channel cache. Group receive requires actual membership and no disband, ignores
  send mutes and returns the stable join incarnation; self inbox binds admitted UID.
  Unavailability/corruption is not definitive denial. Rejoin rejects old exchanges
  and ends the exact Session while retaining debt; no listener/scheduler is enabled.
  Anchored plans reschedule native committed-frontier propagation even when idle
  or write-fenced: an ISR replica may hold the anchor while its HW still lags.
  The hint carries exact placement, no caller HW, and grants no recovery proof.

- The [MQTT IM access design](../specs/mqtt-im-access.md) defines the approved
  target contract; the default-off product listener now has single-node and three-node process interop coverage. Complete durable/Will/restore/scale acceptance remains pending. MQTT Session subscriptions are separate from IM
  membership; protocol ACKs are separate from read state; delivery obligations
  survive ordinary history cleanup within their explicit lifetime and limits.
  Reliable recovery must cover messages from every entry, not only MQTT sends.
  [Graceful owner retirement](../specs/mqtt-owner-retirement.md) is minted only
  after terminal owner drain and persisted after product workers join, while
  Gateway's close-callback loop remains live. Exact-owner RPC can consult one
  bounded immutable receipt for an older boot of the same node. Empty running
  registries, unknown effects, crash, elapsed leases and old Session state never
  supply that proof. Receipt files use version 1 below `mqtt/retired-owners/`;
  no existing table or RPC changes, and pre-feature boots have no implicit proof.
  [Crashed-boot recovery](../specs/mqtt-crashed-boot-retirement.md) can mint an
  older-boot receipt only under the owning node's exclusive generation lock.
  Other nodes still require its exact-owner RPC; unavailability or elapsed lease
  supplies no proof. [Process-outage acceptance](../specs/mqtt-owner-outage-acceptance.md)
  distinguishes refused takeover from quorum or general admission failure.
  [Qualified accounting](../specs/mqtt-qualified-accounting.md) preserves original
  charge membership/bytes when options or expiry later change: cursor table 24
  System 1 stores bounded ranges, optional columns 25–27 hold version/head/tail,
  command 82 operation 4 appends, and read kind 18 pins a coherent head. Admission
  and release debit exact receipts; empty ranges allocate nothing. SourceDrain
  releases one range per turn and retains explicit pending until its fixed end.
  Matched nodes/tools and pre-feature backup rollback are required.
  [Consumer accounting](../specs/mqtt-consumer-accounting.md) reads anchored
  original messages for online/offline Sessions, applies QoS/No Local/expiry,
  checks exact current intent/binding/permission, and commits one bounded range.
  It does not depend on window capacity or grant send/owner-isolation authority;
  failed evidence or revision races leave progress unchanged. Discovery/scheduling
  and product delivery remain required.
  [Window admission](../specs/mqtt-window-admission.md) derives payload/reference
  from anchored originals, applies current new-delivery QoS/No Local/expiry and
  consumes one exact original-charge receipt per turn. QoS 1 requires committed
  exchange readback. Original QoS 0 [preclaims once](../specs/mqtt-qos0-preclaim.md)
  before returning a candidate, so lost replies/takeover never retry it; completion
  is a verified no-op. QoS 1 downgraded to QoS 0 retains its private revision-bound
  debit until enqueue; that downgrade permits duplicates. No uncharged QoS 1
  resurrection, implicit ACK or send grant: the sender supplies permission/recovery
  ordering and app/runtime must supply scheduling. No new table or wire format.
  [QoS 0 gateway](../specs/mqtt-qos0-gateway.md) shares the send gate without
  PacketID/ACK binding or QoS 1 credit. Expired new candidates yield; begun QoS 1
  keeps the earlier native/MQTT/Will deadline with remaining zero when expired.
  App codec composition reserves 136 outbound properties/64 KiB while preserving
  inbound/peer packet bounds. Caller still owns private completion after enqueue.
  [Exchange recovery](../specs/mqtt-exchange-recovery.md) prepares one begun
  exchange in DeliveryOrder under exact ownership. Its retained cursor/binding
  supplies the original permission incarnation across unsubscribe/replacement;
  current permission and anchored content must still agree. Final point read
  accepts unrelated ACK link changes but rejects disappearance/new admission.
  It never ACKs, expires or changes an exchange. Page completion is not a complete
  connection-recovery proof; the sender owns serialization and final permission.
  [Explicit Session ending](../specs/mqtt-session-ending.md) isolates the exact
  observed owner before one atomic Session/Will end, even if quota accounting
  already ended the row. It preserves debt and the first end reason, never follows
  a successor, and gives the next connection a fresh lifetime. Expired active
  owners first record the original disconnect; automatic ending/cleanup scheduling
  remains required.
  [Session child reclamation](../specs/mqtt-session-reclamation.md) uses command 75
  and optional table-22 column 30. It deletes at most 64 intents/indexes per step,
  then atomically clears old cursor/accounting/inflight spans. Range masks make
  results independent of Raft apply grouping. The retained parent prevents old
  child recreation; source tombstones and detached Wills remain separate. The
  completion marker proves no owner isolation or shared-content GC. Discovery uses table-22
  index 3 and read kind 23, rejecting missing/incomplete System-1 coverage.
  Command 76 backfills at most 64 historical rows and its cursor atomically;
  ordinary writes maintain eligibility behind the cursor. Matched writers are
  required. The shared consumer cohort now builds coverage and schedules one
  cleanup page per fresh turn. Current Ended lifetimes require exact-owner End
  and a matching reread; replaced lifetimes never close the live successor.
  Three cursors/one coverage hint per Slot remain bounded, and public reclamation
  confirmations count observations rather than unique Sessions or physical GC.
  Fresh CONNECT must retain the reclamation marker when resetting delivery state;
  otherwise its lifecycle CAS rejects the attempted marker regression.
  CONNECT also rebases at most three definite revision rejections under the same
  isolated Owner/UID and unchanged lifetime decision, rechecking authorization
  while retaining one candidate and its original lease. Unknown outcomes never retry.
  The [connection sender](../specs/mqtt-sender.md) serializes recovery before new
  admission, makes fresh permission the final authoritative read before enqueue,
  and invokes End for definitive denial after releasing owner scopes. It keeps
  no bodies; one private already-enqueued QoS-0 completion may reconcile exact
  charges after ACK/renewal revisions or a lost reply. Gateway BindDelivery adapts
  the accepted connection to that sink contract; unknown writes close without
  retry. Production permission authority, discovery and scheduling remain required.
  Optional message column 21 preserves bounded versioned publication metadata;
  compatibility record codec 2 and proposal format 3 prevent lossy recovery.
  Ordinary expiry uses ingress time; Will uses the original source append time.
  Channel RPC 11 and quorum exchange 6 preserve it with content budgets and
  explicit rejection of lossy encodings. Exchange requires matched replicas;
  native storage hashes and older Channel RPC layouts remain unchanged.
  Send commands/envelopes and product append RPC 3 preserve metadata; native-only
  requests retain version 2. Business retries compare original content excluding
  only ingress time and preserve the original clock. Routed original committed
  reads retain HW/retention fences and omit history-edit overlays for retry proof.
  Owner-push RPC 2 preserves content, settings and original timestamp; envelopes
  with extended fields need upgraded owners, including native sends with time.
  Message JSONL carries optional metadata; preflight validates its source clock,
  import budgets count it, and both verify modes bind its exact SHA-256. Native
  JSONL/digests remain unchanged. Complete MQTT state transfer is unsupported:
  offline JSONL export rejects MQTT metadata, replay, Will and capacity evidence
  before preparing output, including ended/orphan state outside a requested Slot range.
  Preserve the source and use matched native backup/restore; publication metadata
  alone on ordinary message rows remains transferable.
  See the [publication contract](../specs/mqtt-publication-metadata.md).
  The local MQTT owner registry bounds pending/active/closing reservations and
  admitted scopes. Begin synchronously checks its monotonic lease; cancellation
  or deadline expiry never proves that admitted effects drained. Quiescence
  requires physical transport closure plus explicit scope completion. RPC 107
  echoes the exact owner and never treats another boot or network loss as proof.
  Gateway `CloseTransportAndWait` fences admission and joins the physical gnet
  CloseWithCallback independently of business cleanup. OnClose precedes residual
  writes/socket close and cannot supply proof. One lazy receipt survives canceled
  waits; submission or callback errors remain failure. See
  [close proof](../specs/gateway-transport-close-proof.md) and
  [owner execution](../specs/mqtt-owner-execution.md).
  The [PUBLISH entry](../specs/mqtt-publish-entry.md) takes UID from admitted owner
  execution, checks current ordinary-topic permission and reuses Send. QoS 0
  still persists; QoS 1 success needs committed ID/sequence. An uncertain Send
  can leave accepted Channel work running, so local scope drain is insufficient:
  MarkUncertain permanently retains isolation-unproved until a future valid
  recovery mechanism exists. Time or socket close must not clear this barrier.
  The [connection supervisor](../specs/mqtt-connection-supervisor.md) performs
  renewal and queued cleanup outside synchronous gateway close callbacks, which
  may otherwise wait for their own PUBLISH scope. First disconnect intent and
  its trusted monotonic observation survive retries. Stop fences admission and
  joins registered work without erasing failed cleanup; app also closes remaining
  unregistered Owners. The [owner sweeper](../specs/mqtt-owner-sweeping.md) drives
  the existing deadline heap independently of connection registration, with one
  joined loop and bounded visits/time. Close failure and uncertain effects retain
  capacity; its aggregate metrics report sampled state, never isolation proof.
  Restore requires a fresh registry/boot and supervisor.
  [Offline drain metadata](../specs/mqtt-offline-drain.md) allows only closed-intent
  window advancement and cancellation initialization under exact owner/revision
  fences; it never grants offline Admit/ACK or clears inflight exchanges.
  [Closed-source maintenance](../specs/mqtt-closed-source-maintenance.md) derives
  one exact parent/UID from routed reads and shares the foreground sealing stages
  without local Owner admission. It only handles durable closed intent and yields
  after one accounting range. [Pending removal recovery](../specs/mqtt-pending-removal-recovery.md)
  finishes UID checkpoints and exact Removing children without live admission.
  Nested drains inherit the captured Owner; pending subscription index 2 survives
  qualification removal, retaining retries after a failed final child CAS.
  Foreground completion accepts only an identical child already marked Removed.
  [Pending establishment recovery](../specs/mqtt-pending-establishment-recovery.md)
  dispatches Preparing through the same bounded cohort. Captured Offline
  preparation retains full intent and source starts; fresh permission and one
  final parent CAS precede Active. Definite denial ends only the exact Owner;
  unknown failures retain intent. `subscription_establishment_confirmed` counts
  timely successful observations, including retries, with no identity labels.
  [Unsubscribe fault acceptance](../specs/mqtt-unsubscribe-fault-acceptance.md)
  exercises committed-intent failure and failed final completion through real
  Paho/WK product processes. `subscription_removal_confirmed` is a fixed aggregate
  confirmation event, including retries, not a unique-subscription count. Gofail
  markers are inert in ordinary builds; instrumentation uses a temporary copy.
  A copy receiver must check its own durable HW before the strict committed-source
  reader; recovery likewise requires locally committed target anchors before
  planning. A remote request may legitimately be ahead. Typed copy/recovery
  readiness/pressure becomes replay pending. Unknown errors, cancellation and
  anchor write failures do not authorize request retry or SUBACK.
  Removing intent alone rebases definite CAS rejection at most twice, requiring
  the exact unchanged child and same Owner under a newer parent revision. Unknown
  replies and port-level conflict errors remain unconfirmed without retry.
  The [gateway entry](../specs/mqtt-gateway-entry.md) retains execution across
  CONNACK and rechecks before enqueue; close callbacks never join packet scopes.
  A constant-size decoded DISCONNECT receipt survives EOF before mailbox dispatch.
  Validate client reason/expiry before cancelling Will, and register normal intent
  before fencing renewal. The opt-in product listener composes subscription and
  delivery; complete recovery and failure/scale acceptance remain required.
  [Subscription orchestration](../specs/mqtt-subscription-orchestration.md) persists
  Preparing/Removing before projection and accepts only exact intent receipts.
  Resume preserves generation/operation; replacement preserves cursors. Counts
  use bounded pages plus parent revision CAS. Parent cancellation is checked
  synchronously, not only through asynchronous propagation. Projection receipts
  require a real distributed implementation; controlled test receipts are not proof.
  The [Session usecase](../specs/mqtt-session-acquisition.md) verifies device
  credentials without WK conflict actions, isolates the exact old owner, rereads
  authority, atomically commits Session/Will and activates only the local candidate.
  Will setup uses message's read-only person/group permission query, bypassing the
  SEND cache without creating directories or publishing; execution rechecks policy.
  Local lease time starts before proposal submission; durable milliseconds round
  up while the monotonic deadline never moves with response latency. Renewal
  preserves delivery/Will state and fences confirmed loss or invalid clocks.
  Expired active owners record abnormal disconnect at their recorded deadline
  before reconnect; delayed cleanup cannot restart Will or offline expiry.
  Deadline reconciliation fences the complete scanned owner and advances one
  lifecycle decision. Waiting Will deadlines use a coherent Session/Will read;
  Ready obligations detach atomically and survive later offline expiry. Scheduling
  must scan both Session and Will deadline indexes, never only Session expiry.
  The node-owned deadline worker rotates those indexes over locally led hash
  Slots with bounded pages/visits and joined per-call contexts. Complete cursors
  advance only over visited rows; future boundaries reset, failed rows remain
  durable, and lost ownership discards cursors. Stop must join before restore or
  dependency shutdown; restart cannot overlap an old run. It does not publish Wills.
  Product app registration and unreachable/restarted-owner isolation proof remain
  required; stored inactive state, boot mismatch or lease expiry alone is not proof.
  Source-owned binding tombstones prevent delayed prepares from resurrecting a
  subscription. Unknown source boundaries block reclamation; stored progress
  requires current remote authority and source protection before use.
  [Consumer progress](../specs/mqtt-consumer-progress.md) projects only contiguous
  cursor completion from a pinned Session/cursor read, coalescing unchanged floors
  without writes. Explicit ended/replaced lifetimes retain Removing responsibility;
  offline or missing state proves no completion. It never marks Removed, invents
  source-release acknowledgement or authorizes shared-content GC.
  [Binding removal](../specs/mqtt-binding-removal.md) separately revalidates remote
  end/drain proof, acknowledges the exact binding revision on the source Slot
  while retaining Removing, then revalidates before committing Removed. A changed
  binding invalidates that acknowledgement. Normal completion requires closed
  admission and a fully completed sealed cursor with no pending/inflight work;
  newer subscription generations cannot inherit old cleanup. Aggregate Channel
  protection remains independent, and retained tombstones preserve discovery.
  [SourceDrain](../specs/mqtt-source-drain.md) seals the durable accounting end
  after subscription admission closes, then releases only Pending-minus-Inflight
  quota through the existing window command. ACK gaps and exchanges remain.
  Interrupted preparation uses command 82 CancelInit (op 3), which requires
  closed/replaced intent and cannot reset a cursor; unknown starts still need
  replicated protection. Old peers reject op 3. Same-topic replacement and lost
  replies retain the original seal; a newer closure revision may equal stored intent.
  [Group projection](../specs/mqtt-group-projection.md) composes preparation,
  all-replica confirmation and drain. Confirmation pins a full accepted anchor,
  requires complete independent recovery from each eligible replica and rechecks
  fresh placement. Missing copy, partial import, scans and retirement cleanup
  stay pending; verified maintenance-only tails need no extra anchor. Long repair
  scans remain managed maintenance work; inbox admission is not implemented here.
  [Group drain discovery](../specs/mqtt-group-removal-discovery.md) reads the
  exact closed intent and at most two cursors. Existing cursors pin the original
  source without another protection call. Missing preparation registers unknown
  responsibility using the original subscription generation/revision, then seals;
  it cannot recreate a missing binding behind an initialized cursor. Unattended
  ended-Session discovery and native source deactivation remain separate work.
  [Retention planning](../specs/mqtt-replay-retention-planning.md) captures the
  committed anchor before reading the first strict consumer-floor page. New
  bindings register unknown responsibility before confirming their fresh tail;
  reversing these reads breaks admission safety. Strict snapshot reads reject
  inconsistent index witnesses and inspect at most limit+1 rows. Planning alone
  cannot delete content; replicated GC and retained repair proofs remain required.
  [Replay retirement](../specs/mqtt-replay-retirement.md) now has explicit proposal
  format 6 and Message System 15 journaling. It retires a complete committed
  anchor, retaining its exact digest/counters; equal prefixes cannot change the
  reference. Native quorum retry preserves the control intent, and incapable
  stores reject append/recovery. Pending decisions are not readable proofs or
  backup content. Journals survive original trim/restart, but do not materialize
  a pruned baseline or delete content. Product consumer admission remains unwired.
  [Typed retirement admission](../specs/mqtt-retirement-admission.md) checks the
  installed full authority and independently reloads captured/candidate anchors
  plus the latest decision. Stable source/Through commands preserve pending row
  identity; older retries reuse a newer committed decision without another append.
  [Reactor admission](../specs/mqtt-retirement-reactor.md) uses the bounded append
  queue and detached typed workers, preserving durable progress after observer
  cancellation without caching retry identities. [Routed admission](../specs/mqtt-retirement-routing.md)
  uses RPC 100 with full placement identity and fresh Slot checks around the reactor;
  successful local commits and idempotent retries request native voter refresh
  so idle replicas learn the decision without a later business append. The native
  sequencer supplies HW; scheduling is not a follower durability receipt. Missing
  refresh capability rejects local admission and post-refresh authority is checked.
  RPC 101 returns bounded historical selection under a stable fence and the exact
  captured proof. Serving-node binding, closed echoes and foreground gates survive
  gateway replacement. These internal ports trust ordered consumer permission;
  [Consumer production](../specs/mqtt-retirement-production.md) now reruns ordered
  retention each turn, validates a bounded whole-anchor selection and commits it
  through those ports. Continuations pin capture/floor, retaining increased
  permission conservatively while decreases or authority changes yield. App
  composes real Node ports. [Automatic maintenance](../specs/mqtt-retirement-scheduling.md)
  rotates copy/recovery/retirement through the same bounded worker; phase hints
  cannot mix. Reverse continuations pin capture/floor and strictly decrease.
  Commit counts are operation observations, never replica cleanup proof. Reopened
  three-node stores independently verify retired baselines. No new table/wire is needed.
  [Whole-anchor selection](../specs/mqtt-retirement-anchor-selection.md) scans at
  most 64 historical journals in one pinned view, below the captured anchor and
  consumer floor. Verified backward cursors survive restart/restore; a floor
  between anchors rounds down without requiring local shared bodies. This read
  supplies no permission to commit retirement or physically reclaim content.
  [Retired replay storage](../specs/mqtt-retired-replay-storage.md) applies only
  committed format-6 decisions. Table-2 System-2 links the exact cumulative
  baseline and a separate bounded deletion cursor; remaining suffixes retain
  their original counters/hashes. Repair/readiness distinguish retired obligation
  from retained bodies, and historical cuts reject newer retirement authority.
  Pruned backups use version 3; versions 1/2 keep their export bytes. Restore
  publishes the baseline/frontier only after suffix installation, deferring the
  redundant version-2 header frontier. [Recovery application](../specs/mqtt-retirement-recovery.md)
  explicitly uses RPC 99 v3: a pinned latest committed decision is applied before
  donor selection, with at most 64 primary rows per turn. Pending cleanup yields
  to later source passes, independently of finite journal-scan continuations.
  Versions 1/2 retain behavior; consumer admission and product wiring are pending.
  [Maintenance-tail planning](../specs/mqtt-maintenance-tail-planning.md) prevents
  idle copy/anchor/retirement feedback by proving every suffix position is an
  anchor or retirement, through captured HW in one snapshot (at most 64). Larger
  tails conservatively copy; later business copies all intervening controls. RPC 97
  reply v2 preserves the assertion; ordinary/error replies and requests stay v1.
  MQTT metadata facades route Session children by the frozen namespace/ClientID
  hash and source bindings by ordinary Channel ID or UID. Read RPC 106 requires
  a fresh Slot barrier and pinned primary/index snapshot; recovery explicitly
  selects a logical hash Slot. Writes require committed conditional results.
  [Inbox admission checkpoints](../specs/mqtt-inbox-admission-checkpoint.md) use table 26 System 1,
  command 74 and RPC 106 kind 21. Progress follows both canonical person UIDs;
  runtime deletion retains an invalidation with monotonic revision. Pinned reads
  expose the current directory generation separately from possibly stale progress.
  [Bounded admission turns](../specs/mqtt-inbox-admission-turn.md) reuse the pinned
  native Channel Ready marker at the same directory generation before scanning
  either UID. Each prepared/closed candidate commits progress independently;
  strict candidate witnesses fail on missing/stale rows. Real single-node cluster
  coverage joins the native projector, offline source preparation and first-message
  replay/accounting. [Prepared append fences](../specs/mqtt-append-route-fence.md)
  carry an optional exact route through fresh Slot reads and the durable sequencer;
  person directory deletion atomically advances that route. Only explicit fenced
  requests use Channel append codec 12, without lossy fallback.
  [Automatic append preparation](../specs/mqtt-inbox-appender.md) sits before the
  shared durable Appender under a private app composition gate. It admits native
  directory work once, advances bounded resumable pages and checks the exact final
  checkpoint/caller epochs before binding the current route. Offline first native
  SEND is covered; command/group paths and committed receipts retain their semantics.
  [Inbox establishment](../specs/mqtt-inbox-establishment.md) first commits UID
  qualification, then advances one bounded native directory page per call. Each
  candidate commits progress only after protected cursor preparation and all-replica
  replay confirmation. Lost replies and owner takeover retain starts; current
  intent and self-inbox permission gate the final receipt. Active option updates
  retain that qualification. Real single-node cluster coverage uses this path for
  an existing source, then admits a new source while offline without qualification
  fixtures. [Physical runtime deletion](../specs/mqtt-runtime-incarnation.md)
  retains table 3 System 1 authority floors; explicit recreation advances all
  authority versions, while late upserts and absent-runtime directory admission
  fail. Deletion atomically withdraws person-directory tasks/readiness and inbox
  checkpoints. Cold creation rereads actual committed versions even after success.
  [Runtime source reads](../specs/mqtt-runtime-source.md) use MQTT read kind 22 to
  pin that floor and the live runtime after a fresh Slot barrier. The exact key
  and floor distinguish physical recreation while ordinary routing changes do
  not; this is neither business-lifetime identity nor Will redispatch authority.
  Restore fencing, offline scheduling and full product composition remain required.
  [Inbox removal](../specs/mqtt-inbox-removal.md)
  use UID binding optional columns 29–32, separate from initial discovery.
  Empty initialization precedes monotonic ID/incarnation progress; completed
  normal removal needs its Session revision witness. Command 71 and binary
  snapshots retain it. Marked state requires matching writers/tools; it is not
  source-release or shared-content GC proof. InboxRemoval closes qualification
  before bounded cursor draining, commits per-source progress and retains exact
  inflight exchanges for ACK after unsubscribe. Cursorless bindings remain
  independent cleanup debt. App composes both halves; real single-node cluster
  integration covers backlog release and subsequent ACK without wire transport.
  See [Slot access](../specs/mqtt-slot-access.md); these APIs do not prove owner
  isolation, replica capability activation or safe restored-owner execution.
  Message System 12 materializes source-incarnation protection and copy receipt
  references. It clamps physical trim independently of logical history; protected
  reads retain HW and reject gaps. Local CAS is not replicated activation or copy
  proof. Every possible owner must support this guard before activation.
  Protected checkpoint absence/inconsistency is corruption across reads and
  mutations; raw writes cannot regress HW and suffix cuts share its commit lock.
  Explicit format-4 Channel controls now replicate initial source activation.
  System 13 retains the first manifest and protects its pending prefix; the
  covering HW commit creates System 12. Local CAS cannot fabricate the reserved
  `mqtt-log-v1:` generation. Committed backups require both projections and their
  exact identities. Channel's optional source facade now uses the ordered append
  queue for first activation and a checkpoint worker for committed confirmation.
  Repeated admission avoids another control, rechecks epoch/route/write fences
  and returns a separate subscription-start boundary. Source routing now uses
  Node/Channel RPC 93 and fresh Slot runtime-meta reads before/after activation;
  explicit caller epochs/route cannot be rewritten. The serving node never
  forwards again. Fresh point reads require codec 3 and quorum/apply confirmation,
  including absence; ordinary metadata cache/read semantics remain separate.
  Subscription projection and shared-copy transfer remain required; this
  capability cannot authorize SUBACK.
  Group source preparation now creates a recoverable unknown-boundary binding,
  reconfirms protection, saves one immutable start and initializes the Session
  cursor before activating the binding. Lost commit replies and owner resume
  retain that start; every completion rechecks intent and current permission.
  Its prepared result is not a subscription completion receipt. Inbox discovery,
  permission-incarnation ordering, shared recovery and removal remain required.
  Shared replay is message-domain table 2, never Slot message-body storage. Its
  atomic local copies normalize size hints and preserve original publication
  content; index 2 meters prefix ranges without reading bodies. Binary backup 2
  validates/rebuilds the shared keyspace without global-ID duplicates; native-only
  backup stays version 1. Replica storage is not cross-node durability or safe GC.
  Bounded replay transfer requires installed committed entry/proposal proofs and
  an independently accepted complete-content prefix. Native log digests omit some
  row fields, so the received page cannot supply its own recovery authority.
  Import can refill shared content after original-body trim, commits rows/meters
  and local progress atomically, and never advances source release or readiness.
  See [replay transfer](../specs/mqtt-replay-transfer.md); replicated accepted
  anchors and learner/migration scheduling remain runtime requirements.
  The Channel replay preparation facade checks recovered leader/epoch/route
  admission before and after a bounded checkpoint worker. It captures HW,
  confirms source protection and returns owned content without releasing history.
  Covered pages precede extensions so short-page replies can be retried. The Node
  facade and RPC 94 now perform fresh Slot reads before/after preparation, reject
  changed placement and preserve exact request/page fields across gateway swaps.
  A routed page alone proves no quorum copy. Copy coordination and body-free RPC
  95 independently derive the exact full-content prefix on current ISR voters,
  require the leader plus a strict MinISR majority, and recheck fresh membership.
  Receiver requests never advance HW; bounded copy receipts still require runtime
  admission into an accepted decision. Format-5 anchor controls now replicate a
  source/prefix/full-content digest and atomically retain canonical journal rows
  under Message System 14. Committed reads verify source and exact log proofs
  after original trimming; backups preserve committed entries and reject missing
  journals or mismatched proposal/entry versions. Quorum, restart and learner
  transfer preserve these controls. Typed sequencer admission now validates exact
  installed voters/learners and copy membership, chains accepted prefixes and
  derives a source/Through command identity. Pinned source/latest/command proofs
  keep retries independent of control IDs and original bodies; uncertain proposals
  retain their first row. An anchor-only tail cannot generate another idle anchor.
  The service now uses the reactor append queue and a typed append worker, with
  owned membership bytes and cancellation/lifecycle guards. Control completion
  advances durable progress without synthesizing a message or caching request IDs;
  observer cancellation cannot undo admitted durability. Node/cluster admission
  now rechecks fresh Slot authority around the reactor call; body-free RPC 96
  uses a closed 8 KiB codec and exact receipt echo. Received metadata always comes
  from the serving Slot read. Historical proofs keep their original authority;
  post-commit fencing withholds a reply without undoing durability. Planning now
  captures reactor HW and reads source/latest anchor in one pinned snapshot;
  a zero command skips only optional exact retry lookup. Node/RPC 97 surrounds
  this view with fresh Slot checks, preserving original proof bytes and a 4 KiB
  cap. Next ranges ignore local copy-ahead and idle anchor-only tails. Store repair
  now derives the expected prefix from each receiver's committed anchor inside the
  atomic import boundary; exports must reach that exact anchor within 256 rows /
  16 MiB. Original-body removal and restart preserve this proof; caller/donor hashes
  cannot substitute for it. Node/RPC 98 now routes exact target/donor intervals with
  fresh placement before/after and before import, separate four-slot receiver/donor
  admission and five-second deadlines. Stable migration write fences permit immutable
  recovery; changed fences suppress the receipt. Store planning now selects the next
  interval from one local frontier/journal view, verifies continuation coverage and
  scans at most 64 journals. Completion covers only the exact requested target;
  source copied-through never substitutes for missing replay content. Node/RPC 99
  composes this planner with target-owned recovery: four donors at most, 750 ms
  per donor inside five seconds, current leader/ISR before learners, and resumable
  donor rotation. Import retains its pre-import plan; only the next verified read
  reports completion. Replies carry exact request echoes and proofs within 4 KiB;
  RPC 98 carries bodies. Receiver admission and stable migration fences are reused.
  [Consumer reads](../specs/mqtt-anchored-consumer-reads.md) use Node/RPC 102,
  fresh placement/fence checks, a five-second deadline and separate four-slot
  storage admission without a queue. One pinned snapshot verifies the committed
  anchor and full-prefix endpoint before reading a bounded short page; original
  history is never a fallback. Each typed message is checked against its retained
  committed native entry/proposal; only explicit formats 4/5/6 mark a control.
  Payload lookalikes and SyncOnce alone cannot. RPC 102 v2 uses existing message
  codec 11, preserves immutable references and rejects v1 without downgrade.
  Replies echo the exact anchor and range. Reads
  grant no Session authorization, accounting, window admission or GC permission.
  Three-node integration verifies remote short pages after physical original trim.
  Active-source discovery uses `MQTTReadSourceOwners` (read kind 16, Node/RPC 106):
  pinned retention-index prefix seeks return at most 64 distinct Channel sources,
  independently of subscriber count. Preparing/Removing obligations remain visible;
  Removed/UID rows are excluded. Each sampled index witness must match its primary.
  Cursors follow encoded owner order, preserving source generations. This is a work
  hint, never a consumer-completion proof. Older read JSON stays unchanged.
  Replay workers use [tombstone discovery](../specs/mqtt-tombstone-source-discovery.md),
  `MQTTReadReplaySources` (kind 17), instead: bounded primary-prefix seeks include
  Removed bindings so final consumer departure cannot erase cleanup work. No new
  table/index/backfill is needed, and kind 16 keeps its existing meaning. The
  retention index still excludes Removed obligations. Old peers reject kind 17;
  do not downgrade. Source deactivation and safe tombstone pruning remain required.
  The replay usecase alternates bounded copy/anchor admission with round-robin
  replica recovery. Each target pins its accepted anchor and retains scan/donor
  hints across errors and newer anchors. Continued visits retain the detached
  continuation on failure; source/placement changes reset hints, never durable progress.
  Idle control tails do not create new anchors. App composition uses fresh Slot
  metadata and existing Node ports without a local authority fallback.
  One managed replay worker scans locally led hash Slots, retaining only one finite
  journal-scan continuation per Slot instead of a per-source cache. Work/errors
  yield to later sources; cold passes rotate phases, targets and donor hints.
  Partial budgets preserve unstarted entries, and source removal/Slot loss drops
  hints. Stop joins its exact run before restart/restore; three-node composition
  verifies automatic learner recovery. After original-prefix trim, native repair
  may advance only after a bounded follower probe matches an independent local
  exact identity under the unchanged frontier. LEO alone does not prove HW; the
  final proposal still carries missing commitment. This adds neither votes nor
  authority. Active migration probes attach optional
  replica-local readiness: a pinned read binds captured HW/latest committed anchor
  to independent replay coverage, with fresh complete placement/write-fence checks.
  Absent/unsupported evidence is explicit; source copied-through is never coverage.
  Ordinary diagnostics remain unchanged. Accepted-prefix planning pins the exact
  write fence while retaining recovered leader/data-plane authority; RPC 97 uses
  immutable recovery's fresh placement checks. Fenced coordinator turns only recover
  existing anchors, enabling background catch-up without admitting copies or writes.
  Planning uses native quorum read readiness, not `CommitReady`, which intentionally
  stays false after successful recovery under a write fence.
  Planned migration re-probes coverage before promotion/leader commit and before
  clearing the task fence. Failover selects a native leader first, then waits for
  replay recovery before clearing its fence. Missing coverage and native HW lag
  stay runnable without Slot writes. Graceful drain applies/probes source authority;
  active leader probes checkpoint recovered HW and request native tail propagation
  under the exact installed fence. This proves no source release or consumer GC.
  A real stopped-leader test verifies native selection, fenced content recovery
  from a surviving donor and resumed writes. Cached append routes refresh once
  on typed transport dial failure; ambiguous post-send outcomes keep their existing
  recovery rules. Candidate selection is explicit in this integration test.
  Explicit storage source release derives its boundary from an own committed
  anchor and verified local replay prefix under append/checkpoint ownership.
  It updates only System 12, preserves the anchor manifest receipt, revalidates
  older retries and allows replicas to skip intermediate local CAS revisions.
  Physical retention remains separately clamped. RPC 99 v2 explicitly requests
  source release after complete target recovery; v1 keeps ordinary recovery.
  The target rechecks fresh placement/fence immediately before storage release,
  and caller/server recheck before acknowledgement. Stable fences and learners
  are supported; cancelled or stale replies retain safe durable work for retry.
  The coordinator requests release on existing bounded replica visits and rejects
  absent/unsolicited acknowledgements. Consumer-proof GC and product lifecycle
  wiring remain required.
  Will obligations retain their original Session generation after replacement.
  Session transitions and old/new Will decisions commit atomically through the
  lifecycle command; quota termination resolves Will in its accounting commit.
  Generic CAS cannot change a referenced live Will or bypass its lifecycle. Server
  Will retry identities use message index 8 with publication metadata v2; native
  client index 4 remains separate and index 3 includes Will client-number history.
  Index 8 is removed with physical history deletion; a miss after an uncertain
  Will append is not proof of nonpublication. [System-16 receipts](../specs/mqtt-will-receipts.md)
  now retain identity/time/content SHA-256 atomically with keyed Will originals.
  Prefix trim preserves them and may derive a missing legacy receipt before
  deleting its original; suffix rollback removes only its matching receipts.
  Backup v4 includes committed receipts and their message-ID high water; all
  byte/stream imports preflight content, trim witnesses and target conflicts.
  [Routed reads](../specs/mqtt-will-receipt-routing.md) now use fresh Slot checks,
  recovered reactor HW, bounded checkpoint workers and exact-echo RPC 103.
  Three-node TCP/disk evidence covers renewal, original trim, restart and isolated
  warm-reader rejection. [Execution turns](../specs/mqtt-will-execution.md) claim
  exact detached rows. [Frozen preparation](../specs/mqtt-will-preparation.md)
  stores optional Will columns 35/36: Preparing, Prepared, Started and transformed
  body. Current policy gates resumable preparation; only a definite Started CAS
  grants dispatch through ordinary directory/append without repeated hooks.
  [Empty publication bodies](../specs/mqtt-empty-publication.md) are accepted only
  with validated MQTT/Will provenance. Webhooks may explicitly clear that body;
  nil replacement preserves it, and native payload validation remains unchanged.
  Real cluster coverage verifies empty QoS 0/1 commits/retries and frozen empty
  Will receipts after lost replies and permission revocation.
  [Started non-dispatch recovery](../specs/mqtt-will-started-recovery.md) joins an
  exact unissued -> Sealed journal transition with the owning node generation
  lock/older-boot fact, then fresh permission and a definite successor Slot CAS.
  [Attempt reclamation](../specs/mqtt-will-attempt-reclamation.md) runs only on
  journal pressure: one nonwaiting 16-attempt/750ms page requires fresh exact
  Published/Rejected or strictly newer execution rows. Missing, current and
  unknown records stay; cleanup supplies no retry proof and yields to a new turn.
  [Process acceptance](../reports/mqtt-will-pressure-races/README.md) covers the
  default 1,024-record cap in both topologies and applied CAS reply/canceled-page
  races. The latter retains the 750ms page limit with a separate 60s completion
  observation; one receipt takes 41.631s. No latency SLO, delayed uncommitted
  apply or failed authority-read starvation is qualified by these cases.
  [Proposal/commit/apply qualification](../reports/mqtt-will-raft-recovery/README.md)
  separates queued-before-RawNode, persisted-above-quorum-commit (three-node
  only), and committed-before-FSM Started commands. An unknown old claim grants
  no dispatch; a definite newer Started CAS may authorize one original while
  the old proposal remains queued. Captured-executor apply evidence and killed
  process counter prefixes remain distinct from other replicas and restarted
  counters. The cap-two/12-group fixture preserves 256 hash Slots; pressure is
  exercised after delayed FSM resolution, not concurrently against an in-flight
  older row. That slice leaves direct successor cuts and failed-read fairness
  unqualified. [Successor-CAS acceptance](../reports/mqtt-will-successor-recovery/README.md)
  adds exact second-generation queued, persisted/uncommitted and pre-FSM cuts,
  plus independent ClientIDs reaching actual full refusal/reclamation pages
  while the captured old/new reservations remain unresolved. Unknown reads retain
  evidence; this does not qualify failed-read fairness. Captured cap-two admission
  stays closed through killed-process counter collection and joined exit; only
  then may surviving nodes and the replacement boot use ordinary admission.
  Late pressure restores ordinary capacity after its unknown observation, so no
  liveness guarantee at a full unreclaimable journal follows. Paho manual ACKs
  flush in receive order: withholding an original also holds later pressure ACKs.
  Record that exact unfinished set and verify identity/PacketID/DUP on reconnect
  before final healthy quiet. Setup/convergence bounds are separate from runtime
  grant bounds and establish no latency SLO.
  [Failed authority-read fairness](../reports/mqtt-will-read-fairness/README.md)
  qualifies the existing overlapping cursor under actual Store deadline/canceled
  errors: a held 16-candidate page retains capacity; resuming cursor advancement
  reaches an independently calibrated page-external terminal and ordinary pending
  publication without restoring reads. Four cap-32/one-group cases keep 256 hash
  Slots, preserve current recovery and original PacketID/DUP replay, and retain
  killed-process effect prefixes. The exact private nonadvancing-cursor negative
  fails the business receipt after continued full refusal/errors. Completion
  observations do not extend runtime deadlines. This is not default-cap or
  multi-group scheduling, full-unreclaimable-cap liveness, issued terminal recovery
  or shared-storage admission; no business repair was needed.
  [Append admission](../specs/mqtt-will-append-admission.md) adds version-2
  Admitted -> AppendIssued at the origin router before local admission or remote
  forwarding. Its trusted callback never crosses RPC or reaches accepted writers;
  routing retries reuse only the exact issued attempt. Reserved/version-2 Admitted
  may seal; unknown AppendIssued/version-1 Admitted remain positive-only, including after
  source death. Only trusted synchronous whole-invocation non-submission can seal
  an exact same-boot issued attempt. Fresh target absence, later denial and elapsed grants resolve no
  unknown append. Single-node cluster composition verifies original identity/time after
  lost observation and revocation, including real Webhook replacement. Uncertain
  append-issued terminal recovery,
  replica receipt transfer and whole-channel deletion/restore remain pending.
  The optional `store.WillReceiptLookup` exposes
  checkpoint-pinned `channel.WillReceipt` values, including after trim/reopen;
  adapter absence/errors never grant retry authority. Expired task leases alone cannot discharge an
  append with an unknown outcome; see the
  [Will execution failure inventory](../specs/mqtt-will-idempotency.md#execution-recovery-constraints).
  Setup reserves its 79-byte identity tail. Server lookup requires the original
  committed proof and fails closed without its capability; prefix strings alone
  confer no server identity. Product execution and retained-proof lifecycle still
  need complete fencing and lifecycle wiring.
  Session/subscription Slot writes fence owner and revision atomically; repeated
  subscription options preserve delivery generation, and exact retry uses the
  child's own last-mutation revision. These storage primitives do not prove source
  protection or permit product activation. Per-source backlog accounting updates
  the Session quota aggregate atomically and can durably terminate an overflowing
  Session; it never advances window admission or contiguous completion. Window
  commands separately persist immutable references and ordered outstanding links.
  ACK frees only its exchange and keeps earlier gaps; lifecycle CAS cannot reset
  same-generation delivery counters/allocators. A stored window is not permission
  for an unfenced socket to send.
  [Acknowledgements](../specs/mqtt-outbound-acknowledgements.md) uses the current
  local Owner and authoritative Session/inflight point reads. At most three
  command-70 proposals share that scope after definite rejection, requiring a
  newer parent revision and the same immutable exchange. Unknown write outcomes
  never retry. Caller-captured cursor/PacketID/DeliveryOrder must all match; a reused ID conflicts. Completion needs no current subscription grant,
  so ordinary unsubscribe preserves old exchanges. Absence is an explicit read
  result, never a delivery receipt; lost replies reconcile without double writes.
  [Outbound gateway binding](../specs/mqtt-outbound-gateway.md) captures exact
  exchange identities before enqueue, retains no bodies, and bounds connection
  sends by Receive Maximum/1024. Concurrent sends yield; attempted orders cannot
  retransmit on the same connection. Negative PUBACK completes; unknown IDs add no
  credit. Writes and ACK failures close without discarding durable exchanges.
  This adapter trusts caller admission/content/permission proof. Product scheduling,
  listener composition and full recovery acceptance remain required.
- [MQTT inbox directory discovery](../specs/mqtt-inbox-directory.md) uses read kind 20 on RPC 106, routing by UID and scanning the existing membership primary key. Pages retain all types/tombstones, follow encoded length/bytes/type order, and never use mutable activation or personal visibility. Each page is pinned after a fresh authority barrier; this is not a multi-page snapshot. UID binding checkpoints use the same 4096-byte ID bound and preserve it through backup/restore; long checkpoints require matching binaries/tools. Future person sources still require committed UID registration followed by qualification/protection before first append; the current asynchronous directory task is insufficient. No new table is introduced.
- [Inbox source preparation](../specs/mqtt-inbox-source-preparation.md) prepares one canonical person channel for one durable UID qualification, including offline Sessions. Source responsibility commits unknown before the boundary capture; command 82 initializes a cursor using the current stored owner/revision. No local connection scope or socket authority is created. Closed qualification/intent or ended/replaced lifetime skips new admission while retaining old debt; an expiry timestamp alone is not termination proof. This primitive does not implement initial directory projection or the future first-append handshake.
- Replication peer queue length is not runnable work: Channel in-flight ownership
  and earlier exchange kinds may block every item. Scheduling checks the same
  selection barriers as batching, and exchange release wakes blocked classes.
  Otherwise empty owners can repeatedly reschedule and allocate without sending.

- [User/Channel Send Ban](../specs/user-channel-send-ban.md) separates UID-owned
  global restrictions from source-Channel restrictions (both person directions).
  Mandatory policies use fresh Slot barriers and request-scoped node-batched
  reads; TTL applies only to auxiliary membership facts. System identities and
  plugins cannot bypass bans. Explicit policy writes are atomic, versioned and
  independent; credential/ordinary metadata writes preserve them. Format 2
  requires matching binaries and new/imported data; no old-data migration is
  included. Cross-ingress, non-replica, leader loss and isolated-leader rejection
  and single/three-node restore have process-level evidence. Real plugin processes
  also verify bans, restricted hook mutation and the in-flight boundary. The PDK
  SendResp has no reason field: rejected SENDs return RPC errors with public
  protocol reason codes, not successful zero-ID responses. Remaining fault-matrix
  and performance qualification remain pending. See the [implementation report](../reports/2026-09-24-send-ban-implementation.md).

- A full SEND shard can coexist with low aggregate gateway queue pressure.
  The natural failure's single-session shards retained32 active records for
  about1.25s; another225–228 admissions exactly filled the256-item queue.
  Native waits were for append results. Preserve unfinished waits and freeze
  admission evidence before teardown; completed-only maxima miss active work.
  Linux engine `BytesPerSync=0` selects Pebble's512KiB default, not disabled
  range writeback. See the [shard timeline](../reports/2026-09-27-send-ban-shard-timeline.md).

- A same-binary 5,000-Channel / 4,500-SEND/s diagnostic passed with no injected
  delay and reproduced EOF 1.59 seconds into repeated 380-ms message-WAL
  completion holds. The rejected shard's 38 queued + 218 new admissions exactly
  reached 256 while a 32-record batch remained occupied. Real syncs still ran;
  injected sleeps and native syscalls are separate evidence. This establishes
  sufficiency of slow completion, not the physical cause of natural sync stalls
  or a production repair. See the [controlled window](../reports/2026-09-28-send-ban-sync-window.md).

- A full 30-minute kernel-observed SEND run passed despite sampled message-WAL
  page-writeback, journal and block-I/O waits. Bounded thread/FD snapshots locate
  kernel wait stages, but repeated snapshots can be different syscalls and the
  selected pressure peaks are not a whole-run latency distribution. A wait symbol
  alone does not establish the earlier EOF cause or qualify an uninstrumented
  run. See the [kernel observations](../reports/2026-09-28-send-ban-kernel-waits.md).

- Raising SEND batch cap from32 to128 did not remove the controlled WAL-hold
  EOF. Both runs kept256 queued records per shard; the larger active batch
  increases total outstanding work and is not an original-load qualification.
  Rejected32-arm batches held only15/18 records, while128-arm batches held128;
  all exact queue balances reached256 during occupied handlers. Individual Go
  wait spans are not entire handler durations. See the [batch boundary](../reports/2026-09-28-send-ban-batch-boundary.md).

- Compaction throughput alone does not explain sustained gateway queue closure.
  A same-binary diagnostic control completed30minutes, while16MiB/s compaction
  pacing still failed with slow WAL and a full session shard. Pacing also changed
  background progress; do not promote it as a repair or infer causality from one
  pair. Keep original-load, uninstrumented qualification separate. See the
  [controlled comparison](../reports/2026-09-27-send-ban-compaction-pace.md).

- Exact proposal phases locate a controlled slow round at about 1.145 seconds:
  about 378 ms before peer exchange, then 767 ms in exchange; local storage
  proceeds concurrently. All twelve selected peer submissions find sixteen
  observed open flights, and eighteen matched store calls spend 97.5–99.9% in
  commit-coordinator result waits. Such waits include queued and executing time,
  not a proven sequence of particular physical commits. There is no separate
  foreground HW disk commit after quorum. Any pipeline repair must count queued,
  executing and unpublished results in one fixed outstanding budget and retain
  fresh permission, Channel order, ACK order and durable-before-success. The
  natural physical I/O cause and R2/R6 qualification remain open. See the
  [proposal dependency evidence](../reports/2026-09-28-send-ban-durable-phases.md).

- Separating routed admission from result publication does not transfer all
  ownership to one drain. OrderedSubmitter joins delegated calls and callbacks;
  Group still owns writes that outlive a canceled routing result, while Gateway
  must retain capacity for completed results awaiting ordered ACK publication.
  The optional ordered module is not wired into product entries yet; its
  contract tests do not qualify the original SEND pressure run.

- Permission envelope admission is shared by local and remote reads. A typed
  busy result can travel in a successful transport RPC; transport error counters
  alone cannot prove admission success. Pressure evidence must separately retain
  the permission admission/busy histogram count delta. The 5,000-channel
  diagnostic has reproduced these rejections; sustained qualification is pending.
  Admission now permits at most 128 executing and 1024 waiting envelopes, with
  queued undecoded bytes capped at 16 MiB. Every concurrent send issues its own
  envelope, so the waiting count must cover a full worker burst (256 unpaced
  senders overflowed a 16-slot queue at 500 SEND/s in CI). Waiting precedes
  decoding, lasts at most 2 s or caller cancellation, and never reuses a pre-wait
  policy result or adds RPC retries. Full saturation still returns busy.

- Backup Controller CAS reads must observe this adapter's completed mutations
  before lease cleanup: a follower's older local snapshot can hide a committed
  archive-operation lease. The adapter waits boundedly for local visibility,
  without another RPC, and preserves no-op revisions and caller cancellation.
  A fresh format-2 data root may contain only a pre-mounted `backup-repository`
  directory/symlink; this exception never adopts an unregistered live database.

- Send permissions belong in `internal/usecase/message` before append;
  `pkg/channel` stays business-rule free. Mutable recipient metadata and delivery
  tags are authoritative at their owning Slot/Channel leaders. Remote caches must
  not become permanent subscriber or ownership authority.
- Durable commit and delivery are separate outcomes. Committed replay recovers
  asynchronous effects; its cursor is only a progress hint, and losing it may
  duplicate replay. Delivery, RECVACK, webhook completion, and business execution
  are not implied by SENDACK.
- Gateway receive ACK and session-close feedback use the Online Delivery
  runtime FeedbackHandler directly. App binds the optional port; gateway owns
  frame mapping and the runtime owns exact-session ACK cleanup. The temporary
  delivery usecase and committed-event compatibility entry have been retired.
- Online delivery preserves per-Channel order through bounded queues. Presence
  routes are volatile, UID-authority-fenced projections; concrete sessions remain
  owner-local. Authority changes require bounded reconstruction before an empty
  route can be treated as offline, never a scan of every session.
- Every online route, including advisory EVENT hints, must preserve device ID,
  flag and level along with the exact owner/session identity. The final session
  fence rejects a route with missing or stale device fields.
- Post-commit handoff reserves bounded capacity before append and may reject busy
  work before durability. Once committed, SENDACK completes independently of
  best-effort terminal delivery/plugin/webhook failures. Completion and capacity
  release must match the exact item, sequence, and attempt.
- Plugins are node-local and disabled by default; UID plugin bindings are Slot
  metadata and do not prove a compatible executable is running. Plugin sends use
  `message.App.Send`; PersistAfter runs on the Channel owner. Wire contracts live
  in `pkg/plugin/pluginproto` and preserve go-pdk field compatibility.
- `webhook.before_send` runs after permissions and Send plugins, before submission,
  for every send mode. Explicit denial and parent cancellation cannot fail open.
  Retries may repeat callbacks; handlers use sender/source/client-number identity,
  and callback approval does not prove a later commit.

## Operations and data safety

- Backup node RPC v2 carries explicit repository references, never credential
  ciphertext. Each target resolves the complete repository identity and exact
  credential revision from its local Controller mirror before any effect.
  Missing, stale or rotated credentials fail closed; v1 requests are rejected,
  so backup participants must be upgraded together.

- Product HTTP is a trusted service-side boundary; application backends own caller
  identity and authorization. Manager, Debug, Bench, and MCP are separate privileged
  surfaces. Bench setup uses gated `/bench/v1/*` APIs and a bearer capability when
  remotely reachable. Operations MCP uses its own token and read-only tool boundary.
- Gateway token authentication compares the stored UID/device Token exactly;
  Product HTTP credential updates have no expiry field and JWT `exp` is not
  interpreted. Application backends own expiry, rotation, and revocation. Device
  quit clears the stored Token and schedules matching handling-node connections
  for closure; verify revocation and live connection closure separately.
- Gateway direct/PROXY v1/v2 auto-detection accepts unverified peer address assertions
  when `proxy_protocol_trusted_cidrs` is empty. A nonempty list admits only configured
  proxy peers. These addresses are diagnostic inputs, not built-in token or message
  permission authority.
- Browser-facing Manager message IDs are decimal JSON strings to preserve 64-bit
  values. Manager message deletion advances the Channel retention boundary
  inclusively through the selected sequence, including earlier messages outside
  the displayed filter. Confirmations must make that scope clear.
- Backup configuration is Manager-owned Controller state. Saving a plan does not
  verify repository connectivity; only the exact successfully probed revision is
  schedulable. `COMPLETE` publishes an archive. Restore is a same-identity,
  whole-cluster maintenance operation requiring all current replicas to stage and
  verify data before activation. See [backup and restore](BACKUP_AND_RESTORE.md).
- Restore admission consumes its complete unchanged, unexpired archive lease in
  the same Controller CAS that publishes ActiveRestore. The active job protects
  its source archive through the existing in-use gate. Failed admission cleans
  up only unchanged captured lease authority; a secondary cleanup failure cannot
  turn an unknown commit into a definite conflict or mutation-retry permission.
  See [response contract](../specs/restore-admission-response.md).
- `DATA-FORMAT.json` identifies immutable node-root format and creator provenance;
  nonempty unregistered directories are rejected before writable engines open
  and must never be automatically adopted or rewritten.
  it does not certify all proposal/RPC capabilities. Format-changing features need
  matching runtimes and feature-specific deployment checks. Where required,
  rollback restores the complete previous generation, not old writers on new rows.
- `wkcli` is the public operator utility; `db` import writes offline stores.
  Original v2 migration uses complete immutable cold backups and a fresh native v3
  generation. Source capture, archive integrity, independent offline verification,
  runtime replica recovery, and API/SDK acceptance are separate proofs.
- Migration source preparation keeps its sealed `workflow/PREPARED` checkpoint;
  archive reconstruction uses `workflow/ARCHIVE_PREPARED`. Reusing one workspace
  must preserve both immutable receipts and still rebuild archive proofs.
- Migration exclusions, conflict choices, and lossy mappings require explicit,
  capture-bound decisions. Preserve original bytes and independently rebuild proofs;
  diagnostics or majority copies alone do not certify historical ACKs. Changed or
  unused approvals fail. Cutover after resumed source writes requires a new stopped
  generation. Follow the [migration runbook](../superpowers/runbooks/v2-to-v3-migration.md)
  and [offline rehearsal guide](../../scripts/migration/README.md).

## Performance, release, and automation

- Verify build source identity separately for nested Git worktrees: Go 1.25.11
  VCS detection recognizes `.git` directories, so a worktree's `.git` file can
  produce the outer repository's revision stamp. For exact-source E2E evidence,
  build a clean detached checkout with its own `.git` directory, verify revision
  and binary digest, and supply the binary explicitly through `WK_E2E_BINARY`.
- Bound queues, workers, retained bytes, fanout, and runtime residency at the
  expected scale. Conversation reads must not activate runtimes or mutate
  memberships. Runtime replica counts are not durable business-Channel counts;
  healthy idle eviction can lower them.
- Product goroutines launch through fixed `pkg/goroutine` tasks or audited,
  registered pools. Keep lifecycle ownership, cancellation, and shutdown joins
  explicit; do not add unmanaged goroutines or pools.
- Diagnose with metrics, then bounded profiles and one-variable experiments.
  Separate host, load-generator, and product costs. Offered QPS, short diagnostics,
  rejected windows, missing telemetry, OOMs, or process restarts cannot establish
  production capacity or release qualification. Keep exact source/artifact identity
  and required workload evidence; see [performance triage](PERF_TRIAGE.md).
- Fixed-arrival diagnostics must retain every planned ordinal, including driver
  queue residence, rejected dispatch and incomplete work. Bounded queues keep
  transient connection occupancy from silently changing the offered population;
  scheduled-to-completion monotonic latency still includes all queue delay.
  Preserve actual rendered TOML and full metric bodies for independent replay,
  normalizing only predeclared exact fixture addresses/paths. A failed same-binary
  repeatability control prevents attributing a candidate comparison; keep failed
  populations and original performance thresholds unchanged.
- Mixed SEND benchmark `stage-*` and `channel-*` diagnostics subtract registry
  snapshots taken after warmup and after measured handlers complete, before
  projector drain. `ResetTimer` alone never resets Prometheus counters. These
  completion-window observations have different populations (SEND items versus
  Channel batches); compare sample counts and never add stage percentiles.
  Metadata-create batch counters remain lifetime diagnostics, including warmup.
- PR append and mixed SEND gates retain their own bounded before/after counter
  windows through `pkg/bench/counterwindow` (integration-only). Append opts into
  physical-batch histograms and the existing 1/32 sampled replication observer;
  all three nodes share one registry. Setup/calibration are excluded, completion
  is not a passing verdict, and an earlier gate failure prevents later windows.
  Preserve original order, load, assertions and failure exits; see the
  [workflow catalog](../../.github/workflows/README.md#fixed-linux-send-diagnosis).
- The original mixed SEND qualification also arms a rolling trace and bounded
  one-second counter ring after warmup. Six over-400 ms completions in a planned
  500-arrival cohort trigger one export with two seconds of follow-up. The three
  60-second gate verdicts remain authoritative; profiler overhead is explicit.
  Go's 32 MiB/15-second retention settings are hints; exported trace bytes are
  independently capped at 64 MiB. See the workflow catalog for collection bounds.
- Release completion includes signed native package publication and exact-version
  public APT/RPM verification. Server and CLI artifacts share build identity;
  required same-tag acceptance gates precede publication. Follow
  [RELEASING.md](RELEASING.md); Docker or GitHub assets alone are incomplete.
- Public documentation lives in `docs-site/`; `docs/` is engineering knowledge.
  Publish bilingual routes together and derive current versions from their canonical
  manifests/Changelog. Historical SDK or benchmark receipts do not certify newer
  artifacts. Keep public contracts separate from private interface inventories.
- Public onboarding follows Docker single-node cluster → Chat Demo exchange →
  platform SDK integration. Tutorial screenshots depict real versioned runs;
  keep original captures and bilingual numbered captions aligned through
  [the capture guide](../../docs-site/TUTORIAL_SCREENSHOTS.md).
- Channel read RPCs classify typed temporary dependency transport failures before
  serialization using the existing not-ready code. Nested Slot-authority connection
  loss must not degrade into generic text and ordinary HTTP 400; unknown text,
  storage failures and caller cancellation do not acquire retryability.
- Read [workflow contracts](../../.github/workflows/README.md) before invoking
  Actions. Issue/Review Agent control files remain protected. Authorization, signed
  generation identity, and exact source evidence cannot be replaced by event hints
  or model output. Named checks and path selection are defined only by
  [Review Agent policy](../../.github/review-agent/policy.json); focused Skill tests
  are cataloged only in [.agents/skill-tests.json](../../.agents/skill-tests.json).
- Script integration `repoRoot(t)` also admits top-level tests to the bounded
  parallel scheduler. Resolve it before creating command contexts, starting
  processes, or allocating time-sensitive resources; queue time is not part of
  a process deadline. Output-retention fixtures must observe streamed evidence
  before injecting termination, independently of interpreter startup latency.
- Local repair and the Deployment Action share `scripts/cloud-deployment/deploy.sh`
  for host activation, bounded readiness, and typed outcomes. Behavioral tests
  execute that entry with fake adapters; Go `clouddeploy` owns pure contracts
  and validation, not a parallel test-only execution model.
- Paid cloud creation requires exact start authorization and a bounded cost envelope.
  Deployment, diagnosis, status, or cleanup does not authorize buying resources.
  Preserve immutable Lease expiry and exact resource identity. A run is released
  only after authenticated account/region inventory proves its exact resources are
  gone; unreachable services are not cleanup evidence. Live Analysis uses bounded
  access, and local Codex credentials never move to GitHub or cloud hosts. See
  [Cloud Simulation](../superpowers/runbooks/cloud-simulation.md) and the
  [chat-lifecycle skill](../../.agents/skills/wukongim-chat-lifecycle/SKILL.md).

- Transport observer state remains bounded to 8,192 source keys; absolute-state
  revisions survive delivery, unversioned updates follow arrival order, and
  shutdown drains admitted terminal states. Reuse state cells and delivery
  buffers so metrics do not allocate on every RPC state transition. State and
  bounded-label counters publish every 10 ms; shutdown drains admitted work.
  Versioned notifications may arrive out of order because callbacks run outside
  source locks; tests must compare physical state revisions, not arrival order.
  Transport duration histograms sample one in 32 observations independently of
  flush timing; call/admission/byte counters remain unsampled within the bounded
  key catalog. Observer overflow is reported separately from intentional sampling.
- Internal RPCs negotiate the reserved transport capability service over wire v1
  before using wire v2 budget/cancel frames. Explicit service-not-found permits
  v1 fallback; timeout or malformed negotiation never retries business work.
  Relative budgets start at receiver admission, avoiding wall-clock skew;
  callers enforce their own end-to-end deadline. Queued cancellation releases
  memory and FIFO capacity. Queue expiry watches the original request context;
  a service reuses one timer for the FIFO head's queue deadline when it is earlier
  than the caller deadline. Fixed per-service queue timeouts preserve deadline
  order; removing the head rearms against the next admission's original time.
  Timer callbacks must recheck the current head because Stop/Reset may race an
  already-started callback. Per-request cancellation watchers remain independent.
  Dequeue and executor admission still check expiry even if a timer callback is
  delayed; queue expiry must not cancel its parent context.
  Running cancellation is opt-in for read-only RPCs;
  started mutations use independent bounded execution and may commit after their
  caller times out. Timeout/cancel is never a rollback acknowledgement.
  Read handlers link service shutdown to their owned execution cancel function.
  An execution timeout already provides that ownership; a zero-timeout read
  with an external request context still needs its own child. Never use the
  caller's cancel function or let service Stop cancel the caller's parent context.
  Complete backup/restore RPCs retain a 48-hour execution envelope, repository
  probes five minutes, and Operations MCP one minute for bounded profiling.
  Explicit remote timeout/busy/stopped status preserves temporary failure types;
  arbitrary remote error text must never imply retryability.
- RPC queue entries embed their executor task and remain reachable until terminal
  cleanup and any racing expiry callback finish. Do not pool/recycle this owner
  merely because dequeue stopped its watcher: an already-started callback may
  still hold it. Co-allocation removes one object; it does not remove queue
  cancellation, FIFO, executor bounds, or retained-byte accounting. Queue
  cancellation callbacks capture only this owner; its service and original
  request context must remain immutable while callbacks can still run.
- Internal service `RespondBorrowed` callbacks may use handler response bytes only
  until the callback returns. The server synchronously encodes them into an owned
  wire buffer before request release; asynchronous `Reply` delivery still copies.
- Transport batches ready frames without a default timer delay. Slab admission
  counts retained backing capacity, separately from wire bytes; service retained
  memory includes queued and executing requests until their owner releases it.

## Performance evidence

- `scripts/transport-perf` provides a native Linux amd64, separate-host RPC
  repeatability gate. The frozen first scenario uses 10 s concurrent warmup,
  20 s measurement, client/server GOGC 400/100, and at least six same-version
  windows. Full-batch `(max-min)/median` limits are 3% throughput and 10% P99;
  these are engineering limits, not statistical significance. Invalid counters,
  mixed identities, containers, runtime overrides or overlapping windows fail.
  Passing qualifies only repeatability, never a candidate or production capacity.
- The first native six-window calibration completed 10,799,412 error-free calls
  but failed the full-batch gate: throughput spread 8.146%, P99 spread 52.779%.
  One three-second interval accounted for the visible collapse; aggregate GC/CPU
  counters do not identify its cause. Native hosts alone do not prove repeatability.
  Preserve every window and collect time-aligned host/network signals before
  attributing another small runtime change. See
  [native results](../reports/2026-09-22-rpc-native-results.md).

- The admitted-write listener followup confirmed a measurement sensitivity:
  changing only load-client GOGC from 100 to 400 reduced end-to-end P99 by
  38.57%/33.41% with the same server. Three-second warmup and client GC control
  still left one baseline A/A P99 change of +41.84%. Historical negative pairs
  remain evidence, but do not establish a stable server-only regression.
  Executor wakeup stacks account for about 90% of scheduler delay in both
  versions; this is neither CPU share nor a new regression cause. Keep the
  candidate rejected until a repeatable process-level gate exists, rather than
  picking favorable later batches. See
  [control-path measurement evidence](../reports/2026-09-22-rpc-control-path.md).

- The 2026-09-22 admitted-write listener candidate and its empty-deadline-heap
  timer refinement were rejected. Deferring ownership until successful mutation
  admission removed read/rejection allocation costs and saved about 304 B/one
  allocation per budgeted write RPC, but all four initial/followup single-connection
  unbudgeted-write P99 pairs regressed. The later three-version batch had slower
  baselines and no consistent refinement-over-candidate gain; retain all windows
  and do not use that batch to erase earlier regressions. Runtime stays at the
  baseline. Cancellation must leave the request table unlocked so unrelated
  inbound tracking/finish can progress. See
  [admitted-write evidence](../reports/2026-09-22-rpc-write-listener.md).

- The 2026-09-22 budgeted RPC lifecycle candidate was rejected. Reusing the
  inbound cancellation hook plus a queue-bounded caller-deadline heap saved
  about 288 B/one allocation per budgeted write RPC and improved local write
  throughput by about 2.6%, but budgeted reads lost 1.74%/0.56% throughput at
  one/sixteen connections. Single-connection read A/A varied only about
  ±0.026%; do not dismiss its loss as noise. Rejected calls added about 64 B
  and two allocations. Keep budgeted contexts on the standard watcher until
  a narrower design avoids the read/rejected-call ownership cost. See
  [budget lifecycle evidence](../reports/2026-09-22-rpc-budget-lifecycle.md).

- The 2026-09-22 queue-cancellation experiment reuses a single inbound queue
  listener only for requests without a caller time budget. Budgeted and generic
  contexts keep context.AfterFunc. Final callbacks use the existing managed RPC
  service task; owners are not pooled, and connection close must still invoke
  each request cancellation handle after canceling its parent. Ordinary writes
  saved about 304 B and one allocation per RPC. Single-connection throughput
  remained noisy despite improved medians and P99; do not claim full recovery of
  the original version regression. Failed admissions instead add about 64 B and
  two allocations, so the benefit is workload-dependent. See
  [queue-listener evidence](../reports/2026-09-22-rpc-cancel-listener.md).
- The 2026-09-22 old/current version × connection-topology comparison kept
  the old client fixed: current throughput fell 19.78% with one connection and
  17.77% with sixteen, across eight consistently negative version pairs. The old
  single-connection executor wake-up stack already accounted for about 90% of
  scheduling delay; that share alone is not a new regression cause. Two sixteen-
  connection traces had mixed normalized wait changes. Separate server MemStats
  windows found about 689 B and eight added allocations per RPC in both
  topologies. Prioritize causal isolation of request tracking/queue cancellation
  allocation costs while preserving lifecycle guarantees; profiles do not prove
  they explain the entire regression. No runtime fix was made in this study. See
  [version/topology evidence](../reports/2026-09-22-rpc-version-topology.md).
- The 2026-09-22 queue/handoff study added a fixed-16-caller shared-connection
  benchmark. One active connection showed longer queue and executor handoff
  delays than sixteen; independent trace scheduling profiles support that
  direction. Use hashed rather than batch-aligned periodic lifecycle samples,
  and keep diagnostic binaries separate from timing acceptance. A Gosched after
  every four successful submissions had mixed paired throughput results and was
  reverted. This does not establish the cause or recovery of the original version
  regression. Same-process benchmarks include both endpoints and cannot replace
  independent-process acceptance. See
  [queue/handoff evidence](../reports/2026-09-22-rpc-queue-handoff.md).
- The 2026-09-22 inbound-parent experiment removed duplicate connection-context
  registration while preserving the explicit request table and close cancellation.
  Isolated tracking saved 30–42 ns with unchanged allocations, but the 64 B mutation
  network fixture changed median throughput by −0.06%, with mixed paired results
  and A/A changes of +0.34%/−2.36%. The candidate was reverted; connection tracking
  was only about 0.03% of sampled mutex delay. This is not recovery of the original
  throughput regression. The fixture had one caller per connection; higher shared
  connection concurrency remains unverified. See
  [inbound-parent evidence](../reports/2026-09-22-rpc-inbound-parent.md).
- The 2026-09-22 read-execution context experiment removed one redundant child
  context when CancelRunning and an execution timeout are enabled. The local
  Linux ARM64 lifecycle fixture saved 464 B and five allocations per read; the
  independent-process 64 B read fixture improved median throughput 3.99% and
  P99 32.92%, with all four pairs improving. Follow-up did not find a consistent
  regression in large-payload or mutation controls. This does not establish
  recovery of the original default-mutation cross-host throughput regression.
  See [read lifecycle evidence](../reports/2026-09-22-rpc-context-lifecycle.md).
- The 2026-09-22 cancellation-watcher experiment rejected detachment outside the
  queue lock (four paired throughput declines). Capturing only the existing owner
  saved 32 B per registered callback on Go 1.25.11 Linux ARM64, without removing
  an allocation. RPC throughput remained mixed; initial large-payload declines
  did not repeat consistently in a second batch. This is an allocation-byte
  improvement, not recovery of the original throughput regression. See
  [cancellation-watcher evidence](../reports/2026-09-22-rpc-cancel-watcher-local.md).
- Moving RPC queue-owner allocation ahead of admission was rejected in the
  2026-09-22 local experiment: successful-call throughput changed only +0.30%,
  within same-binary variation, while busy/canceled/stopped admission added
  160 B and one allocation per rejected call. Keep rejection-path allocation
  costs in optimization acceptance criteria. See
  [enqueue-allocation evidence](../reports/2026-09-22-rpc-enqueue-allocation.md).
- The 2026-09-22 independent-process ARM64 small-RPC comparison reproduced a
  consistent current-server regression with a fixed old client. Reusing a FIFO
  deadline timer reduced allocations and local small-payload P99, but throughput
  changes stayed within same-binary variation; the original x86 cross-host
  regression remains unresolved. See [queue-timer evidence](../reports/2026-09-22-rpc-small-local-queue-timer.md).
- The in-process large-payload RPC benchmark shares a client/server heap and is
  sensitive to GC settings. Payload copies dominate its allocation bytes;
  fewer queue-owner allocations alone do not establish higher throughput.
  Keep profiling separate from timing, include same-binary controls, and compare
  versions within an independent-process fixture before attributing small
  regressions. See [local follow-up](../reports/2026-09-22-rpc-large-payload-local-diagnosis.md).
- The 2026-09-22 native x86 cross-host RPC matrix used five interleaved trials
  across old/current client-server pairs. At 64B/16 callers, current/current
  throughput was 8.24% below old/old; old/current was 8.67% below, while
  current/old differed by only -0.29%. The primary regression direction is on
  the current server; queue/lifecycle allocation and synchronization profiles
  support investigation but do not establish one mechanism's causal share.
  Preserve cancellation, execution budgets, FIFO, retained-byte admission, and
  shutdown semantics in follow-up experiments. See
  `docs/reports/2026-09-22-rpc-cross-host-results.md`; mixed-version effects are
  not additive and different Lease measurements must not be pooled.

- A Worker Assignment is one immutable worker generation within a Workload
  Execution. `internal/bench/worker/assignment_lifecycle.go` owns admission,
  tasks, cancellation/join, terminal-cut acknowledgement, teardown and terminal
  evidence; HTTP only translates requests and responses. State is internal.
  Phase tasks outlive request cancellation; channel preparation follows its
  request; stop finalization outlives its waiter. Exact stopped-generation
  validation and metric capture share the replacement fence. Pre-close proof is
  retained before session teardown and cannot cross assignment generations.
  The generic owner adds no universal stop timeout or atomic live-status promise.

- Benchmark progress snapshots capture counters and gauges at one lock-protected
  instant without copying or sorting latency history or waiting for report
  aggregation. Lifecycle polling and terminal traffic projection share report
  counter/gauge merge rules across active and archived workload generations.
  Only report snapshots include full latency and error evidence.
- Benchmark Registry report snapshots copy every metric family at one lock-protected
  instant, then aggregate owned latency samples outside the producer lock.
  A separate collector mutex serializes large working copies. Exact SLO
  quantiles and raw sample retention remain unchanged; this reduces producer
  stalls. Exact summaries use typed standard-library sorting to reduce CPU and
  callback allocations; full sorting and duration-dependent legacy sample
  storage remain. Keep exact nearest-rank results distinct from diagnostic
  bucket upper bounds.

- Generic group `random_online` sender selection must use the scenario seed
  and logical channel/message indexes so scheduling and sending agree across
  call order and traffic partitions. Before the September 2026 fix, this accepted
  value silently used the first online member; historical runs using it cannot
  be described as randomized-sender coverage.

- Generic benchmark stage histograms use fixed buckets with exact counts/sums
  and explicitly marked percentile upper bounds; they do not change legacy SLO
  histograms or verdicts. SEND submission measures a client API call, not a wire
  timestamp; SENDACK waiting includes client frame matching. Full operation time
  includes configured receive verification and retries. Compare scheduler
  planned/dispatched/drop counters before interpreting a passing report as load
  attainment. The current matching client's SENDACK operation lock is a no-op.

- Observation HTTP body reads must preserve causal context cancellation, as
  header requests already do. Turning canceled reads into generic target failures
  makes normal coordinator stop lose terminal evidence; unrelated read errors
  must still fail even when cancellation happens concurrently.

- Five-second worker cuts retain configured hot SENDACK P99 threshold counts.
  Interval counts can trigger one independent diagnostic profile after an earlier
  throughput dip; profiles serialize per cluster and retain the original trigger
  bracket. Missing or delayed capture stays explicit. Profiles and finite host/
  metrics context explain failures but never relax the qualification verdict.

- 500-QPS seam qualification counts latency from scheduled arrival through completion.
  Service time alone cannot qualify a run. Each fixed window must meet throughput,
  latency, error, drop and completion bounds; every rejected window remains evidence.
- A closed early product-failure timeline can prove failure without satisfying a
  full-duration performance window. Missing duration cannot downgrade that proven
  failure or qualify an otherwise incomplete run.

- Online delivery queues are bounded globally and ordered by exact Channel ID/type.
  A fixed worker pool rotates ready Channels after each plan; historical Channel
  state is removed when drained. Fixed hash-to-worker routing caused a ten-second
  person-message observation timeout behind the 100,000-member canary.
- 500-QPS performance reuse is limited to three recent clean main qualifications
  with identical performance inputs; failed/retried/incomplete matching runs
  invalidate reuse. Public-metadata failure falls back to fresh tests, while
  correctness/unit/race and scheduled/manual performance always execute.
- Sustained 500-QPS warmup failures retain separate, non-qualifying arrival
  reports with complete counters and bounded anonymous failure categories;
  append and mixed SEND retain independent warmup pressure/storage boundaries.

## Embedded Demo message editing

- Chat Demo credential creation is checked by default for test accounts;
  unchecking it uses existing credentials without updating the server. Public
  deployment's optional exact `chat_ui_revision` serves only `/demo/` from a
  read-only bundle, leaving Product image, API and business source pins intact.

- `demo/chatdemo` pins JS SDK `1.4.0-beta.1` and uses its edit/feed manager with
  custom epoch-preserving history and complete conversation-directory providers.
  Preserve stream metadata and reject mixed-epoch directory pages.
- UI editing is limited to the sender's acknowledged ordinary text messages;
  Product HTTP delegates business authorization to the caller's backend.
  Unknown edit outcomes retain the same draft until an idempotent retry resolves.
- The opt-in `demo/chatdemo` `test:integration` needs an explicitly supplied
  freshly built server and Playwright installation.

- Chat Demo send retries reuse the original SDK `SendPacket` and `clientMsgNo`
  only after an explicit failed SENDACK; a missing acknowledgement remains pending.
  Mobile Back preserves the message view and draft in page memory. Drafts are not
  durable, and reconnect retry ownership remains with the SDK.

## RPC host diagnostics

- Durable-quorum Channel commits do not populate the reactor's legacy
  recent-record cache. Quorum replication/repair owns its payload retention;
  compatibility Pull cache misses use durable storage. Legacy append caching
  remains enabled. This removes duplication, not the quorum retry evidence.

- Host-sampled RPC runs are diagnostic evidence, not repeatability qualification.
  `scripts/transport-perf` records monotonic measurement anchors; cross-host
  origins differ and need the existing boundary RPC brackets plus an explicit
  drift assumption. Historical reports lacking anchors cannot be aligned later.
- TCP/interface counters cover a network namespace; CPU/PSI/softnet cover the
  host. Keep counter deltas on their sampled intervals, missing values explicit,
  and surviving-thread scheduler deltas labeled as lower bounds. A correlation
  does not establish a transport defect or infrastructure root cause.

- [MQTT product composition](../specs/mqtt-product-runtime.md) reuses the Gateway
  and starts bounded Session, replay and delivery workers after cluster readiness.
  `mqtt.enable` also enables future person-source preparation on shared native
  sends. Incomplete Stop retains message/cluster dependencies. Restore joins
  CONNECT acquisition, workers and Owners, persists exact boot retirement, then
  [reconstructs a fresh MQTT generation](../specs/mqtt-restore-reactivation.md)
  before maintenance clears. Stable Gateway/RPC dispatch uses atomic generation
  publication; accepted connections pin their original handler. Terminal Owner,
  connection and delivery runtimes are never reused; unknown effects block join.
  Restore teardown proves local Owner quiescence without issuing a new durable
  Session disconnect against fenced peers; ordinary Stop keeps that mutation.
  Configuration and online interop do not establish unavailable-owner recovery,
  safe uncertain Will redispatch, full storage reclamation or scale readiness.

- [Will scheduling](../specs/mqtt-will-scheduling.md) scans authoritative recovery
  pages across currently led hash Slots and admits at most four body-free keys.
  Waiting/future tasks are skipped; execution rereads/claims authority and never
  infers a safe redispatch from lease expiry or an absent receipt. Stop joins
  scanner and cohort. MQTT DISCONNECT records valid cleanup intent without a
  new owner scope, preserving normal cancellation across EOF/fencing/pressure.

- [Consumer maintenance](../specs/mqtt-consumer-maintenance.md) discovers existing
  source-binding recovery keys on led hash Slots, then accounts one original
  content page independently of online state or receive credit. Current authority,
  owner/revision fences, definite revocation and exact cleanup remain usecase
  policy. Ended quota rows recover lost replies before source removal. ACK progress
  projects only contiguous completion; removed Channel bindings do not prove UID,
  cursor/inflight cleanup or content GC. Recovery terminal pages may retain the
  request cursor; capacity pressure still resumes from the last admitted row.
  Fixed aggregate metrics count turn observations, including repeated ending
  confirmations, not unique Sessions. Pending subscription recovery shares that
  cohort and adds one cursor per Slot (at most 512 for 256 Slots). Timestamps are
  scan hints, not work identity; Preparing is skipped. Stop joins all work.

- [Offline subscription preparation](../specs/mqtt-pending-establishment-recovery.md)
  shares group source boundaries and bounded inbox discovery with foreground
  projection, while requiring a captured unexpired Offline Session and complete
  Preparing child. It retains replay confirmation and grants no activation,
  network execution or isolation proof. Inbox establishment passes its exact
  Owner/child into nested source preparation; ordinary future-source admission
  retains its separate same-lifetime ownership behavior. Worker dispatch and
  final activation remain required before automatic establishment is available.

- [Ended UID qualification retirement](../specs/mqtt-qualification-retirement.md)
  uses the same bounded consumer cohort. Only fresh explicit parent ending or a
  newer Session generation closes old qualification; offline time and lease
  expiry alone do not. Removing and Removed are separate source-owned CAS steps,
  preserving discovery/drain checkpoints and zero Channel protection. Retained
  tombstones fence late preparation. This grants no Channel release, owner
  isolation, cursor/inflight reclamation or content GC. `qualification_removed`
  observes confirmed Applied removals separately from ACK/Channel progress.

- [MQTT idle delivery](../specs/mqtt-idle-delivery.md) uses a ten-second quiet
  hint only after a complete idle pass. Exact local Owner fencing precedes a
  skipped read; any control/source wake invalidates it atomically. The existing
  scheduler bounds source interests and receives node-local post-commit/anchor
  hints. Missed/remote hints and changed authority recover through full refresh;
  hints cannot authorize content or send. Oversized inbox source sets keep polling.
- MQTT subscription diagnostics: `wukongim_mqtt_subscription_closures_total`
  materializes 34 fixed operation/reason series per node. These count entry close
  requests, not unique connections, CAS non-write proof or isolation. Fenced
  admitted scopes may report cancellation. See
  [failure inventory](../specs/mqtt-subscription-diagnostics.md).

- `TestColdGroupSubscriptionAdmission` isolates subscription admission steps in
  one real three-node cluster with 64 new persistent Sessions/groups and no
  foreground retries or publications. It emits bounded success/failure JSON.
  Candidate and error-only-probe runs pass; the intermittent initial SUBSCRIBE
  error is still unreproduced by this focused loop, so process-startup conditions
  remain an open distinction. See [evidence](../reports/mqtt-cold-subscribe-admission.json).

- [First-startup subscription admission](../specs/mqtt-first-subscribe-admission.md)
  reproduces the initial three-node empty-group five-second timeout. Stage probes
  identify serial replica waits and repeated positively completed preparation and
  replay-plan reads within one packet. One request now retains only the prepared
  source/start under its exact Owner, UID and full Preparing child; every turn
  rereads intent and authorization. Confirm uses its fresh validated plan for
  one effect-fenced copy/anchor and fresh coverage, then joins at most four
  supervised replica calls before the final placement check. Every replica is
  still required, hard errors do not become pending through mixed results, and
  a new packet prepares again. See [evidence](../reports/mqtt-first-subscribe-admission/README.md).

- Replay planning is a read-only admission boundary. Confirm and nested Step now
  preserve typed NotReady/Backpressured as bounded pending, with synchronous
  cancellation taking precedence. Unknown, stale, corrupt and anchor-write
  outcomes gain no retry. The deterministic planning regressions, full related race suites and all 16
  subscribe/unsubscribe interruption process cases pass; causal relation to the
  intermittent SUBSCRIBE/EOF failures is still unproven. See
  [evidence](../reports/mqtt-plan-readiness.json).

- Ended-record cleanup must not simply delete Removed source bindings:
  `CompareAndSwapMQTTSourceBinding` uses the retained row to reject resurrection,
  while an absent row permits a revision-zero Preparing create. Any future
  physical tombstone retirement needs a durable rejection boundary for delayed
  creates. Session child cleanup, detached Wills and ClientID/UID binding have
  separate retention obligations; current code has not implemented that cleanup.

- Ordinary group member preparation projects UID-owned membership through
  logical Hash-Slot commands with at most eight supervised proposal workers per
  call; cancellation joins admitted writes and accounting includes their confirmed
  completions. This accelerates provisioning without changing source-version or
  join visibility semantics; see [proposal scheduling](../specs/ordinary-membership-proposal-scheduling.md).

- MQTT subscription completion retains the captured projection receipt and uses
  at most three proposals after definite CAS rejection by an advanced parent
  revision, with fresh same-Owner/full-child evidence and activation permission.
  Port errors/lost replies never retry; only exact Removed intent confirms a
  competing remover's completion. See [orchestration](../specs/mqtt-subscription-orchestration.md).

- [MQTT in-flight churn](../specs/mqtt-churn-pressure.md) reproduced nested
  confirmation receiver pressure after a newer anchor appeared between planning
  reads. Direct and nested replica recovery both retain pending intent on typed
  readiness/backpressure; cancellation takes precedence. A later turn rereads
  authority and storage, while anchor/unknown failures grant no retry signal.

- MQTT new-intent quota/proposal and group cursor Init contention is bounded to
  three attempts. Only read-only newer parent evidence or definite CAS rejection
  permits fresh same-Owner/exact-child checks; unknown outcomes stop. Cursor Init
  retains the first durable protected boundary. SourceDrain validates a newer
  coherent accounting snapshot before yielding, preserving its frozen end and
  rejecting regression, foreign identity or corrupt charges.

- MQTT SourceDrain sealing contention uses no inline write retry. Only a definite
  source CAS rejection followed by three fresh point reads may retain pending
  removal: strict same-binding progress or the identical fixed seal, unchanged
  full Removing child/Owner and nonregressing cursor with the same accounting
  frontier. Unknown effects, changed intent and release/retirement remain failures.
  See [sealing contention](../specs/mqtt-source-drain-contention.md).
- [MQTT storage partition acceptance](../reports/mqtt-storage-partition/README.md)
  uses live three-node, 256-hash-Slot clusters and test-owned bidirectional TCP
  relays. Node-local TCP/public listeners remain live. Reserved gauges precede
  physical preparation; only the positive-charge post-commit witness can prove
  that the delayed commit finished before the reply path was healed. Completion
  requires persistent replay, independent ACK protection and zero debt after
  retirement/reopened delivery. This finite scenario does not qualify membership
  changes, arbitrary corruption, all partition permutations or other platforms.
- The independent `/streamdemo/` uses pinned `easyjssdk@2.0.5`: online `Message` and `CustomEvent` only, with one full `/channel/messagesync` read on connection/reconnection for offline recovery. `/message/eventsync` is not its live transport. The chat demo has no streaming UI; its stream-message edit exclusion remains a server contract.
- Stream Demo readiness requires both SDK peers. Authentication failure retains
  a retry/new-session entrance; resetting clears only this tab's identity and
  message view. Public `stream_ui_revision` can select a read-only UI bundle
  while preserving configured metadata and the guarded model relay.
- `/supportdemo/` embeds only its read-only UI. Its separate loopback Node business
  process owns support sessions and generation leases; handoff joins cancelled
  stream snapshots before allowing human acceptance. Support ownership is not a
  built-in Channel permission or a Product HTTP authentication guarantee.
- Real model generation is a demo-owned OpenAI-compatible SSE producer; its local Node relay is loopback-only and separate from the Product API. API keys are request-scoped and omitted from storage/logs/history. Model deltas still pass through `/message/event` and SDK EVENT delivery, with complete snapshots on finish, cancellation and failure.
- Product stream EVENT dispatch first proves the committed stream base identity, then publishes accepted public events to authoritative current subscribers and exact fenced owner sessions. Four request-owned fanouts, 128-member pages, 512-route pages, 256 KiB RPC frames and a five-second dispatch budget bound pressure; no token queues, per-member goroutines, RECVACK state or offline token log are created. Producers serialize events within each message and clients deduplicate event IDs; UTF-8 `text_offset` reconciles deltas with recovered snapshots. Delivery failures cannot undo accepted event storage; finished projections reject late reopening and final history remains the recovery source.

- `/agentdemo/` embeds only its read-only task assistant UI. The loopback Node
  backend bounds task execution, validates a three-tool allowlist and requires
  explicit approval before creating demo todos. SDK messages persist tool traces;
  real-time reply deltas use SDK events, with history reads only on load/reconnect.
  Business state and model keys remain in memory; a process restart requires a
  new demo session.

- Product HTTP `/` redirects to `/demos/`, a stateless embedded catalog linking
  `/demo/`, `/streamdemo/`, `/supportdemo/`, `/agentdemo/`, `/mqttdemo/` and `/livedemo/`. The catalog does
  not create sessions, connect an SDK or invoke models. Its loopback preview
  redirects each entrance to the independently running Demo process.

- `node demo/start.mjs` owns a fresh loopback-only 256-hash-slot single-node
  cluster and the Demo business processes. It uses a new run directory, strips
  inherited product overrides, and stops only its own child process groups.
  Startup health probes do not create user credentials or model requests.

- Public Demo deployment uses the opt-in `demo/deployment.mjs` renderer and
  `demo/deployment.json.example`; `WK_DEMO_*` values override file settings.
  One public origin configures API metadata, browser-origin checks, WSMUX and
  MQTT routes. The TLS frontdoor shares the Product network namespace with the
  five loopback business helpers. WSMUX `/ws` strips to `/`, MQTT keeps `/mqtt`,
  and the model relay receives a loopback Origin only after public-origin
  validation. Static bundles alone cannot supply these business processes.
  Product image and helper/UI source must use the same immutable revision.
  This deployment path does not alter one-command launcher defaults.

- All six Demo UIs expose a home link. Catalog redirects carry a `home`
  parameter across origins and reloads; it accepts only the same origin or
  loopback catalog URLs. Direct embedded entries use their same-origin catalog;
  direct Node entries publish the configured Product API catalog in page metadata.

- The MQTT smart-store Demo keeps independent device and staff MQTT clients
  inside the browser, connected directly to the Product MQTT WebSocket listener.
  Its loopback backend provisions identities and Channel membership only. Alarms
  and recovery use the store group; commands use the device's person topic and
  execution receipts target the authenticated sender. A PUBACK proves commitment,
  not execution. Persistent group subscriptions recover offline alerts without
  HTTP history substitution. SDK-compatible text payloads carry bounded business
  metadata; stable message IDs and command IDs deduplicate distinct responsibilities.

- The [live Demo](../../demo/livedemo/README.md) uses ordinary group type 2,
  provisioned membership and room denylist operations; Live type 9 and SDK SUB
  do not supply that room contract. Browser SDK barrage/likes use NoPersist and
  no SyncOnce; self ACK and independent audience delivery are separate evidence.
  The loopback BFF owns role capabilities and versioned current snapshots. Unknown
  mute writes retain immutable intent before any opposite action; confirmed policy
  and pending status remain separate. Notification progress is private metadata,
  with a distinct stable event ID per public snapshot version. Failed notification
  does not roll back saved state; reconnect restores state without history replay.
  Bounded in-memory rooms and credentials expire or become invalid after restart.

- Transport byte observations preserve frame kind and scheduling lane through
  batched writes and the bounded observer drain. Fixed direction/lane counters
  separate outbound Slot/Controller Raft payload bytes from other traffic;
  they exclude wire/network overhead. Pressure evidence requires the Raft
  series from every node at both measured-window boundaries; older binaries
  retain null rather than relabeling total transport traffic.

- Gateway SEND batching retains each session's contiguous directory-ready prefix
  in input order. A cold head fences later items, hooks retain that order, and
  terminal results publish through the session chain. The append runtime owns
  same-Channel order and bounded parallel work across independent Channels;
  usecase scheduling must not fragment a ready prefix into durable single-item
  waits. The gateway joins a batch before dispatching that session's next batch.

- Gateway ordering-shard count depends on worker count, total queue capacity
  and SEND batch cap. Lowering only the batch cap can increase shard count and
  shrink each session's burst budget. Validate the full configuration together;
  a lower-cap pressure pass does not qualify changing the default cap alone.

- Send-permission admission assigns execution capacity and removes the waiting
  position under one mutex before waking the caller. A runnable assigned caller
  already owns an execution position; cancellation/timeout must return it even
  when the caller never enters decoding. Keep 128 executing envelopes (at most 512 Slot workers), the
  waiting count/byte/time bounds, and fresh barriers unchanged together.

- Slot ReadIndex coalescing covers only contiguous read controls already taken
  by one Raft worker. One newly issued quorum proof confirms those callers;
  later arrivals or an intervening control require another proof. Each caller
  retains its own cancellation, 256-per-Slot pending position, term fence and
  durable-apply wait, including canceled requests not yet quorum-confirmed.

- Runtime Channel metadata creation collects for at most 20 ms, with the same
  32-item early dispatch, 64-item batch, 256-identity per-Slot queue and two
  in-flight batches. First group SENDs and their followers count as hot in the
  lifecycle workload; a 500 ms collection window alone exceeds its 400 ms P99
  budget. Group setup commits business metadata without warming append runtime.

- Optional message `SubmitBatchEach` joins permission/directory/hook preparation
  before returning and transfers only append completion. Its injected admission
  must bound accepted work and preserve canonical Channel order. Preparation
  advances on submission, not completion; deadlines close only after preparation
  and all results join. Callers still own cross-batch ACK order and reservations.
  Default Gateway composition now wires this port through the bounded submitter.

- Optional Gateway deferred handlers retain configured global/shard reservations
  through preparation return, completion/error handling and actual session-ordered
  publication across batches. Results waiting for an earlier ACK remain counted.
  Per-session chains never hold the shard mutex while writing, clear completed
  links and retire empty lanes. Default product composition activates this port.

- Default Gateway SEND composition separates ordered permission preparation from
  durable completion. The node-owned Channel submitter serializes overlapping
  canonical Channels; Gateway retains its original record/byte reservations
  until ordered physical-session ACK publication. Stop/restore must join both
  owners before closing or restarting append dependencies. This mechanism alone
  is not evidence that the 5,000-channel/4,500-SEND/s qualification passes.

- Ordered submission can coalesce already-ready independent jobs within existing
  micro-batch targets. One execution counts as one busy worker; original callbacks
  retain their capacity and canonical Channel dependency until return. Both the
  pipeline-only and coalescing candidates still fail the controlled WAL-stall
  scenario; neither is evidence that the original high-load EOF is repaired.

- First-rejection diagnostics must include published records awaiting release
  fences, not only active ACK lanes. Under the controlled 380ms WAL floor, exact
  retained Gateway heads joined to queued quorum jobs and Coordinator waits;
  254–255 of 256 records awaited results. This rules out ACK ordering as the
  dominant retained population in that capture, not the cause of natural I/O
  stalls. A separate zero-hold permission-admission failure remains unresolved.

- Gateway SEND batch caps are not physical commit caps. The exact physical
  diagnostic found unlimited configured commit group counts/bytes, nearly empty
  post-collection coordinator queues, and full quorum-worker execution while
  hundreds of accepted proposals had not begun. Moving admitted work through a
  bounded completion owner must retain original record/byte budgets and durable
  quorum proof; increasing group caps does not address that observed boundary.

- Durable-round continuations share the synchronous round's proof state machine.
  Terminal publication waits for all planned submissions to transfer ownership;
  duplicate/late callbacks never add votes. The round has no waiting goroutine
  per proposal. Callers must retain bounded admission through completion; the
  current log/worker still uses the synchronous wrapper until explicit wiring.

- Channel worker ownership is atomically reserved before enqueue and retained
  through result publication for every task sharing an asynchronous quorum pool.
  Sampling deferred plus queue depth cannot enforce the shared bound under
  concurrent admission or during queue-to-executor handoff.
- Send-policy management audits use atomic apply results for previous/current
  values and versions, never a preceding GET. Manager attribution uses its verified
  principal; backend APIs record an unknown operator plus actual socket peer.
  Timeout/unavailability records `outcome_unknown` without policy proof. Logs
  omit credentials, payloads and raw downstream errors.

- Permission execution capacity must cover quorum-read RTT as well as request
  rate. The 500 SEND/s CI exposed deadline exhaustion with sixteen envelopes
  when barriers slowed. Sixty-four also add serial waves to clustered arrivals;
  128 remain bounded, with 1 MiB per wire request
  and reply. Independent slow-barrier local/remote burst regressions preserve
  each caller's fresh barrier and cancellation. Queue count/bytes and the two-second
  wait bound remain unchanged; cross-caller aggregation is tracked by #977.

- Native package lifecycle bootstrap includes package installation before systemd PID 1. A bootstrap timeout does not prove a service start or installed package. Failure probes retain stage, PID 1, jobs and journals with five-second command bounds and 64 KiB output caps; unchanged 300-second bootstrap/900-second main validation deadlines remain separate from failure cleanup.

- PR #998 main integration preserves existing main RPC 91/92 and Slot commands
  67/68/69. Only unpublished MQTT metadata/owner RPCs move to 106/107 and
  Session/subscription/cursor commands to 80/81/82; other MQTT IDs stay unchanged.
  Pre-integration development Raft logs cannot be replayed as this catalog.
  Use pre-MQTT data or a separately verified migration; matched peers/tools and
  the existing cold rollout remain required. See
  [integration evidence](../reports/mqtt-main-integration/README.md).

- Public MQTT documentation lives in `docs-site` as a bilingual first-message
  tutorial and `/api/client-protocols/mqtt` topic. Its Node.js/MQTT.js example
  verifies actual two-way reception separately from PUBACK. Topic IDs use
  canonical unpadded base64url; HTTP payloads use ordinary Base64 of the same
  raw IM bytes. Pages retain default-off development-preview and incomplete
  Linux/fault/load qualification boundaries; configuration stays in one reference.
