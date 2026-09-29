# Project Knowledge

Keep only stable, cross-module facts that prevent incorrect designs or unsafe
operations. Repository rules belong in [AGENTS.md](../../AGENTS.md), domain terms
in [CONTEXT.md](../../CONTEXT.md), and module navigation in the applicable
[FLOW.md](FLOW_INDEX.md). Code, schemas, and tests remain authoritative for behavior.

Do not append task histories, individual benchmark results, release/version
inventories, or implementation walkthroughs here. Record those in the relevant
specification, runbook, report, or module documentation; link to them when needed.

## Cluster and authority

- All deployments use cluster semantics, including a single-node cluster.
  Distinguish 256 physical hash slots from logical Slot Raft Groups and node-local
  reactor partitions. Shipped initialization creates 12 logical groups; omitted
  or zero `cluster.initial_slot_count` derives one. Existing clusters use the
  persisted Controller count; changing this setting does not resize them.
- Node snapshot application serializes watches and readiness probes, rejecting
  older logical revisions before maintenance, placement or task side effects.
  Watch notifications trigger a current Controller read rather than replaying
  queued task progress.
  Equal revisions still refresh health and Controller leadership; logical revision
  does not version every health observation.
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
  Admission now permits at most 16 executing and 1024 waiting envelopes, with
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
- `DATA-FORMAT.json` identifies immutable node-root format and creator provenance;
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

- `demo/chatdemo` pins JS SDK `1.4.0-beta.1` and uses its edit/feed manager with
  custom epoch-preserving history and complete conversation-directory providers.
  Preserve stream metadata and reject mixed-epoch directory pages.
- UI editing is limited to the sender's acknowledged ordinary text messages;
  Product HTTP delegates business authorization to the caller's backend.
  Unknown edit outcomes retain the same draft until an idempotent retry resolves.
- The opt-in `demo/chatdemo` `test:integration` needs an explicitly supplied
  freshly built server and Playwright installation.

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
  when the caller never enters decoding. Keep sixteen executing envelopes, the
  waiting count/byte/time bounds, and fresh barriers unchanged together.

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
