# Project Knowledge

## Channel Runtime

### Conversation working set
- Recent conversation sync is allowed to be working-set based; it does not need `version` to discover every historical conversation update.
- Active local channel runtimes must be capacity-managed per node with `WK_CLUSTER_MAX_CHANNELS` and optional idle eviction; 100k simultaneously active channels can amplify replica goroutines and heap even before message volume is high.
- Channel replica pooled execution must preserve per-channel single-writer ordering and all generation/epoch/fence checks; it is an execution-cache optimization, not a cluster semantic change.
- `ActiveAt` is a best-effort hint: updates may be batched, throttled, dropped, and merged from cache during `ListUserConversationActive`.
- Deleting a conversation clears current active visibility through `DeletedToSeq`; a later message with a larger sequence must be allowed to reactivate it.
- Delete without an explicit message sequence must first resolve the latest Channel Log sequence; if no sequence is available, do not install a zero delete barrier.
- Duplicate/stale delete barriers must not clear an `ActiveAt` written by a newer message.
- Legacy channel allowlist, denylist, and temporary-subscriber APIs are backed by namespaced slot subscriber lists until dedicated metadata tables exist.
- Legacy system UID APIs are backed by the namespaced slot subscriber list `__wk_internal_system_uids__`.
- Persisted system UID add/remove APIs must refresh node-local caches on peer nodes through node RPC.
- Message send permission checks live in `internal/usecase/message` before durable append; `pkg/channel` remains business-rule free.
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
- Delivery tag subscriber mutation versions are generated only by the authoritative subscriber store in the same Raft command/transaction as the subscriber rows, not by the tag cache.
- Channel management chunked subscriber writes must share one durable subscriber mutation version per logical operation; chunk boundaries do not create extra fences.

### Committed event replay
- Sendack waits for Channel Log quorum commit; delivery/conversation are async side effects recovered by committed replay from the durable message log.
- Committed replay cursor is a low-cost progress hint; losing it only causes duplicate replay, not message loss.
- Channel message retention is cluster-authoritative: leaders advance slot metadata `RetentionThroughSeq`; local stores may lag physically and must not use checkpoint `LogStartOffset` for retention.
- Manager history-message deletion advances channel `RetentionThroughSeq`; it must not directly delete message rows or skip channel leader/slot metadata semantics.

### Long-poll observability
- `RPCClientEvent{ServiceID:35, Result:"timeout"}` means the RPC deadline/context timed out; it is abnormal and not a normal long-poll wait expiry.
- Normal channel long-poll wait expiry is a successful long-poll response with `TimedOut:true`, and needs an explicit response observer before counting expected timeouts.
- Diagnostics hot-path events use `pkg/observability/sendtrace` and are consumed by `internal/observability/diagnostics`; recording must be bounded, non-blocking, and must not add high-cardinality Prometheus labels.
- Diagnostics `channel_key` debug matches use `channel/<channel_type>/<base64url(channel_id)>`; gateway send, sendack, and durable send events must carry it so channel-scoped sampling can still be queried by `client_msg_no`.
- Manager diagnostics routes require `cluster.diagnostics:r`; cluster-wide diagnostics queries include alive, suspect, and draining nodes, skip dead nodes, and return `partial` when results are incomplete.
- Manager diagnostics tracking rules are runtime-only, TTL-bound sampler overrides for future events; sender UID rules match messages sent by that UID and never expose `from_uid` in manager event DTOs.

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

### Node scale-in
- Manager-driven node scale-in drains a node to `ready_to_remove`; it must not call physical Slot removal or Kubernetes scale-down directly.
- Scale-in manager reads require `cluster.node:r` and `cluster.slot:r`; start/advance/cancel require `cluster.node:w` and `cluster.slot:w`.
- Node scale-in readiness must account for channel leaders, channel replicas, and active channel migration tasks before reporting `ready_to_remove`.

## Development Workflow

### E2E profiling
- API debug pprof routes are exposed only when `WK_HEALTH_DEBUG_ENABLE=true`; e2e profile scenarios should enable it with node config overrides and fetch `/debug/pprof/*` through the real API listener.

### Worktree testing
- When using project-local `.worktrees/*`, run Go tests with `GOWORK=off`; the parent `go.work` points at the main checkout and otherwise makes packages resolve under `.worktrees` incorrectly.
