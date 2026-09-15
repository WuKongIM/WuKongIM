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
  command without an inspection view reports `unsupported`, not `corrupt`;
  inspection support must accompany new operator-visible command types.

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
- Slot FSM batch conflicts preserve Raft order: a rejected range is subdivided
  with each prefix durable before its suffix executes. Unknown physical commit
  outcomes are not automatically retried as definitely unwritten operations.
- `pkg/db` owns node-local storage: `message` owns Channel logs and `meta` owns
  hash-slot metadata. Borrowed decoder/iterator buffers must become owned before
  escaping or advancing the iterator; checksum and corruption checks remain intact.

## History, conversations, and commands

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
  SyncOnce recovery positions. Sequence minus unread count cannot reconstruct a
  read boundary. Hiding advances `deleted_to_seq` and clears activation without
  removing membership; a newer message can make the conversation visible again.
- `message.PageReader` owns bounded ordinary-page selection, visibility floors,
  SyncOnce filtering, and continuation. Latest reads use bounded reverse scans;
  limits count visible records. Exact lookup must not scan only recent history.
  Missing person membership before the first persistent SEND can mean an empty
  page; missing group membership, tombstones, and infrastructure failures cannot.
- Ordinary and CMD logs have separate sequence spaces. Recoverable SyncOnce
  commands require recipient CMD binding before SEND; request-scoped recipients
  do not create discovery membership. `NoPersist` is online-only and supplies no
  durable sequence or offline recovery, including `NoPersist + SyncOnce`.
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

## Delivery and extension boundaries

- Send permissions belong in `internal/usecase/message` before append;
  `pkg/channel` stays business-rule free. Mutable recipient metadata and delivery
  tags are authoritative at their owning Slot/Channel leaders. Remote caches must
  not become permanent subscriber or ownership authority.
- Durable commit and delivery are separate outcomes. Committed replay recovers
  asynchronous effects; its cursor is only a progress hint, and losing it may
  duplicate replay. Delivery, RECVACK, webhook completion, and business execution
  are not implied by SENDACK.
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
- Migration exclusions, conflict choices, and lossy mappings require explicit,
  capture-bound decisions. Preserve original bytes and independently rebuild proofs;
  diagnostics or majority copies alone do not certify historical ACKs. Changed or
  unused approvals fail. Cutover after resumed source writes requires a new stopped
  generation. Follow the [migration runbook](../superpowers/runbooks/v2-to-v3-migration.md)
  and [offline rehearsal guide](../../scripts/migration/README.md).

## Performance, release, and automation

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
- Release completion includes signed native package publication and exact-version
  public APT/RPM verification. Server and CLI artifacts share build identity;
  required same-tag acceptance gates precede publication. Follow
  [RELEASING.md](RELEASING.md); Docker or GitHub assets alone are incomplete.
- Public documentation lives in `docs-site/`; `docs/` is engineering knowledge.
  Publish bilingual routes together and derive current versions from their canonical
  manifests/Changelog. Historical SDK or benchmark receipts do not certify newer
  artifacts. Keep public contracts separate from private interface inventories.
- Read [workflow contracts](../../.github/workflows/README.md) before invoking
  Actions. Issue/Review Agent control files remain protected. Authorization, signed
  generation identity, and exact source evidence cannot be replaced by event hints
  or model output. Named checks and path selection are defined only by
  [Review Agent policy](../../.github/review-agent/policy.json); focused Skill tests
  are cataloged only in [.agents/skill-tests.json](../../.agents/skill-tests.json).
- Paid cloud creation requires exact start authorization and a bounded cost envelope.
  Deployment, diagnosis, status, or cleanup does not authorize buying resources.
  Preserve immutable Lease expiry and exact resource identity. A run is released
  only after authenticated account/region inventory proves its exact resources are
  gone; unreachable services are not cleanup evidence. Live Analysis uses bounded
  access, and local Codex credentials never move to GitHub or cloud hosts. See
  [Cloud Simulation](../superpowers/runbooks/cloud-simulation.md) and the
  [chat-lifecycle skill](../../.agents/skills/wukongim-chat-lifecycle/SKILL.md).
