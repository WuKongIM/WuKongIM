# Project Knowledge

## Internalv2

- `internalv2` phase 1 is a parallel send-to-sendack skeleton: gateway SEND maps to `usecase/message.SendBatch`, appends through `infra/cluster.ChannelAppender`, and returns SENDACK after `pkg/clusterv2` / `pkg/channelv2` append.
- `internalv2` single-node deployments must use single-node cluster config. Do not add send or storage paths that bypass clusterv2 semantics.
- `internalv2/app` seeds message IDs from the effective clusterv2 node ID: `Config.Cluster.NodeID` when set, otherwise top-level `Config.NodeID`.

## Channel Runtime

### Conversation working set
- Recent conversation sync is allowed to be working-set based; it does not need `version` to discover every historical conversation update.
- Active local channel runtimes must be capacity-managed per node with `WK_CLUSTER_MAX_CHANNELS` and optional idle eviction; 100k simultaneously active channels can amplify replica goroutines and heap even before message volume is high.
- Channel replica pooled execution must preserve per-channel single-writer ordering and all generation/epoch/fence checks; it is an execution-cache optimization, not a cluster semantic change.
- Channel replica execution defaults to `pooled`; `dedicated` remains an explicit rollback mode for legacy per-replica workers.
- Leader-side lane tracking for cold channel wake-up requires follower-advertised lane membership with a local generation; leader ready flags alone must not suppress `ReconcileProbe`.
- Follower `ApplyFetch` must be idempotent for duplicate already-applied record prefixes so long-poll replay can still emit cursor ACKs and avoid replication stalls.
- `ActiveAt` is a best-effort hint: updates may be batched, throttled, dropped, and merged from cache during `ListUserConversationActive`.
- Deleting a conversation clears current active visibility through `DeletedToSeq`; a later message with a larger sequence must be allowed to reactivate it.
- Delete without an explicit message sequence must first resolve the latest Channel Log sequence; if no sequence is available, do not install a zero delete barrier.
- Duplicate/stale delete barriers must not clear an `ActiveAt` written by a newer message.
- Legacy channel allowlist, denylist, and temporary-subscriber APIs are backed by namespaced slot subscriber lists until dedicated metadata tables exist.
- Legacy system UID APIs are backed by the namespaced slot subscriber list `__wk_internal_system_uids__`.
- Persisted system UID add/remove APIs must refresh node-local caches on peer nodes through node RPC.
- Message send permission checks live in `internal/usecase/message` before durable append; `pkg/channel` remains business-rule free.
- Durable message send route selection lives in `internal/runtime/channelplane`; `message.App` only builds durable batches and applies committed side effects, it no longer owns slot/channel leader refresh or remote redirect.
- `RouteGeneration` is the authoritative route identity for channel runtime metadata and peer RPC fencing; stale route records must be treated as a different append route even if the channel ID is unchanged.
- Channel status permissions currently include group `Ban`/`Disband` and sender person-channel `SendBan`.
- `NoPersist` sends still pass validation and send permissions, then skip durable append/committed events and return success with zero message ID/seq.
- `SyncOnce` 持久化发送写入原频道派生的 `____cmd`，但发送权限仍基于原始频道检查。
- `/message/send` request-scoped `subscribers` 要求 `sync_once=1` 且 `channel_id` 为空；`channel_type` 被忽略，内部派生 temp `____cmd` channel。
- Durable request-scoped subscriber sends write the derived temp cmd channel and carry exact `MessageScopedUIDs`; NoPersist request-scoped sends use a transient message ID and realtime delivery.
- Message-scoped delivery tags are ephemeral: they must not replace reusable channel-level delivery tag refs, and their exact subscriber snapshot is not recoverable from durable log replay alone.
- Remote delivery-submit for message-scoped sends must fail closed when the owner node cannot prove scoped-submit support; do not fall back to conversation-only delivery.
- CMD offline sync persists/updates conversation intents (`uid -> readSeq` per command channel) rather than per-message subscriber snapshots; request-scoped recipients and delivery tag UID pages are the authoritative intent sources.

### Long-poll leader lease refresh
- A channel leader metadata refresh that only renews `LeaseUntil` must preserve existing leader-side lane sessions and follower cursors.
- Clearing the lane cursor on a lease-only refresh can make the next replication fetch start from offset `0`, preventing follower progress and HW from advancing for the next append.
- A same leader/epoch/ISR lease-only refresh must not trigger replica leader reconcile or clear `CommitReady`; otherwise a pending checkpoint window can turn successful quorum appends into `channel: not ready`.
- In-flight leader append effects must be fenced by the current renewed same-leader lease, not only the lease captured when the effect was queued; otherwise a durable write can land while runtime LEO stays behind and the next append fails as corrupt.
- If leader recovery/reconcile already adopted an in-flight append's durable LEO before that effect result returns, the result should complete the original waiter idempotently instead of treating the lower base offset as corruption.
- Expired remote channel leader leases must be repaired by evaluating the current leader first; only renew the lease if that leader can still prove it is safe.
- ChannelRuntimeMeta upserts must be monotonic: stale channel/leader epochs or shorter same-epoch leases are no-ops.
- ChannelRuntimeMeta write fences are versioned; set, renew, reset, and clear must advance `WriteFenceVersion`, and monotonic upserts must not clear or regress an existing fence.
- Runtime-meta RPC v2 carries `WriteFence*` fields; v1 must remain supported for mixed-version fallback, and responders must match the request codec version.
- Channel replica migration treats `learner = Replicas - ISR`; learners receive replication but cannot affect quorum, write availability, or leader repair promotion until promoted into `ISR`.
- Migration write fences fail closed; TTL expiry requires an authoritative reset/clear with a higher `WriteFenceVersion`, not a local reopen.
- Migration reconcile probes with newer channel or leader epochs must refresh authoritative metadata on the target before proof checks; never relax epoch fences to hide stale local runtime state.

### Delivery tag
- Delivery tags are the unified subscriber-partition snapshot mechanism for all channel delivery paths; large channels are only the highest-pressure case.
- Channel delivery tags are channel-leader authoritative: only the channel leader may create or update them; other nodes may only fetch leader-built partitions and cache them locally.
- Non-leader delivery tag caches contain only the local node partition, never the full channel subscriber set.
- Delivery tags cache subscriber partitions, not online routes; normal membership changes keep tagKey stable and invalidate caches by incrementing tagVersion.
- Delivery tag ordinary membership changes keep tagKey stable and increment tagVersion; leader incarnation changes may create a new tagKey to avoid stale-cache collisions.
- Delivery tag cache hits require both tagKey and tagVersion to match the current channel ref; older stale requests must never evict a newer local tag ref, and newer mismatches force a refetch from the channel leader.
- Delivery tag route ACK/retry ownership belongs to the target partition node after tag handoff, not to the channel leader.
- Delivery tags use PartitionTopologyVersion checks and TTL cleanup for topology changes and cold-cache cleanup.
- Delivery tag cache eviction must be stale-safe: an older request must never evict a newer local tag ref.
- Delivery tag invalidation must be fenced by a durable subscriber mutation version in the authoritative slot store; pending invalidate is only a hint.
- Delivery tag topology checks use the cluster hash-slot table version plus exact per-UID-slot authority refs `{slotID, leaderNodeID, configEpoch, balanceVersion}`; never collapse multi-slot refs to a max version.
- Delivery tag topology checks are on the delivery hot path; prefer node-local cached slot assignments and only refresh controller assignments on cache miss.
- Delivery tag subscriber mutation versions are generated only by the authoritative subscriber store in the same Raft command/transaction as the subscriber rows, not by the tag cache.
- Channel management chunked subscriber writes must share one durable subscriber mutation version per logical operation; chunk boundaries do not create extra fences.

### Delivery routing
- Remote delivery push runs one target node inline; only multi-node targets use bounded parallel workers.
- Delivery push implementations must report intentionally skipped sender-origin routes as `Dropped`; silently omitting a pre-bound route leaves delivery actor ack bindings/inflight routes uncleared.

### Committed event replay
- Sendack waits for Channel Log quorum commit; delivery/conversation are async side effects recovered by committed replay from the durable message log.
- Committed replay cursor is a low-cost progress hint; losing it only causes duplicate replay, not message loss.
- Committed replay subscribes to committed events and should replay dirty channels before falling back to a full persisted channel-key scan.
- Channel store persisted key listing must skip by encoded channel-key prefix; decoding every message row turns committed replay full scans into CPU/GC pressure.
- Channel message retention is cluster-authoritative: leaders advance slot metadata `RetentionThroughSeq`; local stores may lag physically and must not use checkpoint `LogStartOffset` for retention.
- Manager history-message deletion advances channel `RetentionThroughSeq`; it must not directly delete message rows or skip channel leader/slot metadata semantics.

### Long-poll observability
- `RPCClientEvent{ServiceID:35, Result:"timeout"}` means the RPC deadline/context timed out; it is abnormal and not a normal long-poll wait expiry.
- Normal channel long-poll wait expiry is a successful long-poll response with `TimedOut:true`, and needs an explicit response observer before counting expected timeouts.
- Transport RPC calls do not wait for a per-call write ACK; write failures must close the mux connection so pending RPCs wake through the reader shutdown path.
- Diagnostics hot-path events use `pkg/observability/sendtrace` and are consumed by `internal/observability/diagnostics`; recording must be bounded, non-blocking, and must not add high-cardinality Prometheus labels.
- Diagnostics `channel_key` debug matches use `channel/<channel_type>/<base64url(channel_id)>`; gateway send, sendack, and durable send events must carry it so channel-scoped sampling can still be queried by `client_msg_no`.
- Manager diagnostics routes require `cluster.diagnostics:r`; cluster-wide diagnostics queries include alive, suspect, and draining nodes, skip dead nodes, and return `partial` when results are incomplete.
- Manager diagnostics tracking rules are runtime-only, TTL-bound sampler overrides for future events; sender UID rules match messages sent by that UID and never expose `from_uid` in manager event DTOs.
- Manager Monitor reads `/manager/monitor/metrics`; all-node scope fans out through node RPC and aggregates real dashboard collector series. Sum counters/gauges, recompute fail/fan-out ratios from deltas, and use max node P99 latency as the cluster P99 approximation.

### Send stress performance
- Send stress regression was concentrated in the leader durable path, not gateway ACK handling: `gateway.write_sendack` is microsecond-scale, while `store.commit.pebble_sync` and durable mutex/append waits dominate.
- Same-channel append batching and commit-coordinator batching often add waits without building large batches, so a sync still tends to carry only about one request and a few records.
- Throughput-mode send stress can also be capped before durable send by `gateway.async_dispatch_wait`; the acceptance preset uses a larger bounded async SEND worker pool so queued SEND frames can reach the durable path concurrently.
- Single-node cluster send stress is still a cluster-mode path; benchmark metadata should use the harness node IDs and `MinISR=1`, not hard-coded three-node replicas.
- In the three-node quorum send stress, lowering long-poll max wait alone does not remove the bottleneck; the current hot path is per-channel async SEND serialization plus leader quorum wait after local durable append.
- A separate high-channel three-node send stress should keep the original benchmark unchanged; raising channel cardinality alone has only a small QPS effect while leader quorum latency remains high.
- SEND batching is layered: gateway shards by raw `ChannelID + ChannelType` only to preserve entry ordering and collect micro-batches; message usecase groups adjacent canonical same-channel sends; `pkg/channel.AppendBatch` performs one replica append with contiguous seqs.
- Remote channel append forwarding supports one-channel batch RPC; falling back to per-message forwarding loses the durable/follower batching benefit for clients connected to non-leader nodes.
- Single SEND/Append entrypoints are compatibility wrappers; durable send and app channel append internals should route through batch-of-one to avoid split correctness/performance paths.
- `pkg/channelv2` append is local-runtime only: clusterv2 must ensure/apply authoritative ChannelMeta first and forward non-leader appends to the resolved channel leader.

## Cluster Membership

### Discovery baseline
- Static `WK_CLUSTER_NODES` remain a discovery baseline; controller node snapshots overlay them so early empty metadata reads do not break existing static clusters.
- Static `WK_CLUSTER_NODES` addresses must be unique advertised endpoints; `0.0.0.0` is only a listen bind address and must not be published as a peer address.
- Static cluster heartbeats must publish `WK_CLUSTER_ADVERTISE_ADDR` or the local `WK_CLUSTER_NODES` address; never publish the bound listener address such as `[::]:7000`.
- Discovery must ignore controller metadata addresses that are unspecified bind endpoints and keep the static node address instead.
- Dynamic node join adds ordinary data nodes only; Controller Raft voter changes remain explicit future operator work.
- Dynamic join startup retries JoinCluster synchronously before app HTTP/gateway services start; non-blocking joining readiness is future lifecycle work.
- Controller state-machine join conflicts are stale no-ops; Join RPC prechecks still return explicit conflict errors to callers.
- Active data nodes added by dynamic join do not automatically receive Slot replicas or Leaders; operators must create and start a manager node onboarding job for explicit resource allocation.

## Controller

### Hash-slot migration gate
- Dynamic physical Slot add/remove/rebalance creates hash-slot migrations and requires `WK_CLUSTER_HASH_SLOT_MIGRATION_ENABLED=true`; the default is disabled and manager add-slot reports `hash slot migration disabled`.

### Planning writes
- Slot preferred leaders are controller soft intent. Raft remains authoritative; leader-transfer tasks must target observed current voters only.
- Production controller planning writes must go through Controller Raft proposals; direct Store writes are only for local tests or tools.
- Bootstrap planning rotates `Task.TargetNode` across DesiredPeers by SlotID, and cluster execution transfers the initialized Slot Leader to that target to avoid concentrated initial leader placement.

### Controller Raft transport
- Manager Controller log views read node-local Controller Raft entries through `pkg/cluster`; UI/API must describe them as local node logs, not a global authoritative log.
- Controller Raft status/log diagnostics are node-local reads; remote reads must target the requested node directly instead of using leader-centric Controller client routing.
- Controller Raft shares the cluster transport server, so its wire message type must stay distinct from slot Raft and observation-hint message types.
- Inbound Controller Raft frames must be addressed to the local node and originate from a different node; drop looped or misrouted frames before calling `RawNode.Step`.
- Controller read RPCs can see `not leader` while Raft elects or fails over; keep those retryable read failures out of ERROR logs.

### Controller Raft compaction
- Controller Raft snapshot restore starts from the snapshot index and replays post-snapshot entries; never skip replay by using a later persisted applied index after importing snapshot data.
- `pkg/raftlog` persists Raft snapshot payloads as external chunks; Pebble keeps only metadata and snapshot manifests.

### Slot Raft compaction
- Slot Raft snapshot restore follows the same boundary as Controller Raft: restore snapshot data first, then replay committed entries after the snapshot index.
- After Slot Raft log compaction exists, membership changes must refresh the snapshot ConfState so newly added learners can install a snapshot and catch up.
- Large Slot Raft snapshots are chunked only in `pkg/cluster` raft transport; receivers reassemble chunks into the original `MsgSnap` before calling `multiraft.Runtime.Step`.

### Local storage
- `pkg/db` is the single local storage library: `message` owns channel logs and `meta` owns hash-slot metadata.
- Ordinary new `pkg/db/meta` tables should use the meta table runtime registry; custom code is reserved for cache, guard, monotonic, or multi-record state-machine semantics.
- A single-node deployment is still a single-node cluster; do not add storage or business paths that bypass cluster semantics.

### Node scale-in
- Manager-driven node scale-in drains a node to `ready_to_remove`; it must not call physical Slot removal or Kubernetes scale-down directly.
- Scale-in manager reads require `cluster.node:r` and `cluster.slot:r`; start/advance/cancel require `cluster.node:w` and `cluster.slot:w`.
- Node scale-in readiness must account for channel leaders, channel replicas, and active channel migration tasks before reporting `ready_to_remove`.

## Plugin Subsystem

- Plugin runtime is node-local and disabled by default; plugin-user bindings are Slot Raft metadata keyed by UID.
- Phase 1 supports `.wkp`/go-pdk core methods and host RPCs, but stream RPCs return explicit unimplemented errors.
- Plugin sends must go through `message.App.Send`; PersistAfter runs only on the channel owner node.

## Development Workflow

### E2E profiling
- API debug pprof routes are exposed only when `WK_HEALTH_DEBUG_ENABLE=true`; e2e profile scenarios should enable it with node config overrides and fetch `/debug/pprof/*` through the real API listener.

### Worktree testing
- When using project-local `.worktrees/*`, run Go tests with `GOWORK=off`; the parent `go.work` points at the main checkout and otherwise makes packages resolve under `.worktrees` incorrectly.
- `internal/gateway` now ships only the `gnet` transport; connection callbacks are serialized by actor shards and there is no `stdnet` fallback or per-connection writer goroutine.
- wk-sim performance investigations must follow the project-local `.codex/skills/wukongim-perf-triage/SKILL.md` flow and `docs/development/PERF_TRIAGE.md` runbook: collect evidence, classify, hypothesize, then run one-variable experiments.
- Node log output is split by `internal/log`: `app.log` contains info and above, `warn.log` contains warnings, `error.log` contains errors, and `debug.log` exists when debug logging is enabled.
- Bench APIs are unauthenticated benchmark-only `/bench/v1/*` routes gated by `WK_BENCH_API_ENABLE`; mutations go through benchdata plus user/channel usecase boundaries.
- wkbench requires server bench mode (`WK_BENCH_API_ENABLE=true`) and must prepare target data only through `/bench/v1/*`; it must not use Manager APIs for benchmark setup.
- wkbench traffic with `recv_ack=true` must drain delivered recv frames and send protocol recvack even when receive verification is `none`; otherwise delivery retry will keep re-pushing accepted routes until they expire.
- Committed delivery routing treats transient channel status errors such as `channel: not ready` as retry signals; warn only after retries are exhausted.
- `wkbench dev-sim` must run warmup after prepare/connect and before measured run windows; warmup must touch every assigned channel at least once, and warmup counters are a baseline that must not be counted in `/status` measured traffic.
- `wkbench dev-sim` `/status` distinguishes the configured steady-state online pool (`connected_users`) from the latest sampled live count (`active_users`) and reconnect churn (`reconnected_users`) so online flapping is visible during triage.
