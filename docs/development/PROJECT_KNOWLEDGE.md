# Project Knowledge

## Internal

- `internal` is the promoted send-to-sendack kernel: gateway SEND maps to `usecase/message.SendBatch`, appends through `infra/cluster.ChannelAppender`, and returns SENDACK after `pkg/cluster` / `pkg/channel` append.
- `internal` single-node deployments must use single-node cluster config. Do not add send or storage paths that bypass cluster semantics.
- Cluster backup is a signed logical hash-slot/channel cut stored in two immutable repositories; it never copies old node directories or Raft/runtime state, and restore always targets a fresh generation with the same `hash_slot_count`.
- `internal/app` seeds message IDs from the effective cluster node ID: `Config.Cluster.NodeID` when set, otherwise top-level `Config.NodeID`.
- Browser-facing manager APIs encode 64-bit `message_id` values as decimal JSON strings; web filters, keys, and display code must keep them as strings end to end.
- The Manager Web production bundle is generated into `internal/access/manager/webui/dist`, committed in full, and embedded in `cmd/wukongim`; production must not require a separate web process or a frontend build during ordinary Go compilation.
- Manager message payloads are raw bytes encoded as Base64 in JSON; web views decode valid printable UTF-8 (including non-ASCII text) and keep binary payloads in Base64 form.
- `cmd/wukongim` is the promoted product entrypoint. Controller, the new
  cluster runtime, the multi-reactor channel runtime, and the new business
  kernel are canonical under `pkg/controller`, `pkg/cluster`, `pkg/channel`,
  and `internal`; the former v1 server runtime tree has been removed.
- Runnable `wukongim` helper-script configs live under `scripts/wukongim/` as `.toml`; `.toml.example` files are samples only and should not be script defaults.
- `wukongim` bottleneck attribution uses Prometheus `/metrics` when `WK_METRICS_ENABLE=true`; compare gateway async SEND, Channel runtime reactor/worker queue plus in-flight peak, and storage commit request-vs-batch metrics split by `leader_append` / `follower_apply` lane. `/bench/v1/snapshot` remains a benchmark setup counter surface.
- `WK_CLUSTER_CHANNEL_STORE_APPEND_WORKERS` and `WK_CLUSTER_CHANNEL_STORE_APPLY_WORKERS` cap Channel runtime blocking store worker concurrency only; use them after worker in-flight peaks and storage lane tails show commit-coordinator pressure, never as a durability shortcut.
- Channel runtime ordinary follower progress ACKs are safe only because they are sent after follower durable apply; Pull `AckOffset` remains the fallback, and leader HW still advances through normal quorum checks.
- `pkg/channel` high-channel idle scale depends on parked followers: caught-up followers should wake through PullHint plus send-timeout-bounded recovery probes, not short-interval empty pull polling.
- `cluster/channels` caches append ChannelRuntimeMeta with epoch and leader fences; Slot metadata remains authoritative and stale append errors invalidate the cache once before retry.
- `internal` presence stores owner-local `OwnerRoute` projections for authority/touch; concrete gateway session handles must stay out of authority routes and live only in owner-local session records used for conflict close actions.
- `internal/runtime/delivery` is the no-gateway/no-cluster benchmark boundary for online fanout, owner push batching, and recipient-owner recvack tracking.
- `internal` webhook delivery is a node-local best-effort post-commit side effect with bounded queues and finite retry. Large offline fanout should use batch observer/chunking, and webhook failure must not affect SENDACK, durable append, conversation active admission, or owner delivery.
- Channelappend post-commit pool admission and per-channel backlog must stay bounded and independent from foreground append admission; saturation is observed and dropped so best-effort conversation/delivery work cannot pin writer-advance workers, delay durable SEND/SENDACK, or return `ErrChannelBusy` for an otherwise admissible send.
- Conversation-active cache churn may evict clean rows during memory-only admission; dirty persistence stays exclusively on periodic, pressure-woken, or handoff flush workers.
- Local Cloud Analysis should use the run's Cloud View `RemoteAddr` as a best-effort same-destination egress hint; transparent routing can give public echo services another IPv4. Keep pinned-TLS MCP health authoritative and preserve the echo fallback for runs without Cloud View.

## Gateway Runtime

- `pkg/gateway/core` async CONNECT auth and SEND dispatch are owned by `RuntimeOptions` and ants-backed executors; do not reintroduce session-scoped async worker config compatibility.

## Channel Runtime

- Node-local Channel reactor routing must avalanche stable hashes before a power-of-two modulus; raw FNV-64a low bits collapse sequential canonical person-channel IDs onto a subset of reactors. Reactor partitions are not cluster hash slots or persisted ownership.
- Channel runtime data replicas are selected by Channel placement, not by Slot metadata peers; Slot route peers describe metadata ownership only.

### Conversation working set
- Recent conversation sync is allowed to be working-set based; it does not need `version` to discover every historical conversation update.
- Active local channel runtimes must be capacity-managed per node with `WK_CLUSTER_MAX_CHANNELS` and optional idle eviction; 100k simultaneously active channels can amplify replica goroutines and heap even before message volume is high.
- Channel replica pooled execution must preserve per-channel single-writer ordering and all generation/epoch/fence checks; it is an execution-cache optimization, not a cluster semantic change.
- Channel replica execution defaults to `pooled`; `dedicated` remains an explicit rollback mode for legacy per-replica workers.
- Leader-side lane tracking for cold channel wake-up requires follower-advertised lane membership with a local generation; leader ready flags alone must not suppress `ReconcileProbe`.
- Follower `ApplyFetch` must be idempotent for duplicate already-applied record prefixes so long-poll replay can still emit cursor ACKs and avoid replication stalls.
- `ActiveAt` is a best-effort hint: updates may be batched, throttled, dropped, and merged from the kind-aware conversation active cache during `ListConversationActiveView`.
- Legacy `UserConversation*` and `CMDConversation*` APIs in `pkg/db/meta` are source compatibility shims only; they must map to the unified kind-aware conversation table.
- Conversation active flush attempts must carry a bounded context because Slot proposal futures rely on caller deadlines for stale or uncommitted proposals.
- Conversation active admission is memory-only: it may evict clean rows or return cache pressure, but it must never perform durable I/O or wait for the serialized flush lane.
- Bounded conversation-active admission must locate clean eviction victims through an exact clean index and coalesce duplicate addresses before taking the cache lock; never scan the full dirty cache under that lock.
- Conversation active pressure uses one coalesced async worker wakeup, bounded flush attempts, and 80%/70% high/dirty-low watermarks; clean rows below the dirty watermark are the reusable eviction reserve.
- Conversation active projection failure is observed independently and must not block recipient delivery, later large-channel pages, or subscriber snapshot caching.
- Recipient delivery plans preserve complete UID-authority fences and batch presence RPC by actual leader; only stale/not-ready groups may batch-resolve fresh targets and retry once, without replaying successful siblings.
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
- In internal, channel-scoped `SyncOnce` sends keep the source channel log, persist the `SyncOnce` marker in Channel runtime records, project `ConversationKindCMD`, and are skipped by ordinary conversation hydration.
- `/message/send` request-scoped `subscribers` 要求 `sync_once=1` 且 `channel_id` 为空；`channel_type` 被忽略，内部派生 temp `____cmd` channel。
- Durable request-scoped subscriber sends write the derived temp cmd channel and carry exact `MessageScopedUIDs`; NoPersist request-scoped sends use a transient message ID and realtime delivery.
- Message-scoped delivery tags are ephemeral: they must not replace reusable channel-level delivery tag refs, and their exact subscriber snapshot is not recoverable from durable log replay alone.
- Remote delivery-submit for message-scoped sends must fail closed when the owner node cannot prove scoped-submit support; do not fall back to conversation-only delivery.
- CMD offline sync persists read progress in CMD-kind conversation rows (`uid -> readSeq` per command/source channel) rather than per-message subscriber snapshots; request-scoped recipients and delivery tag UID pages are the authoritative intent sources.

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
- Physical-Slot PreferredLeader diagnostics retain state changes, including one non-match-to-match recovery event, suppress steady matches, and resample an unchanged decision signature at most every 30 seconds; event counts are not reconcile rates, which stay in low-cardinality Prometheus counters.
- PreferredLeader strict-check diagnostics use leader and term only from the owning Slot worker's fresh Raft status; a pre-worker timeout or error keeps them unknown instead of reusing the eligibility precheck.
- Diagnostics `channel_key` debug matches use `channel/<channel_type>/<base64url(channel_id)>`; gateway send, sendack, and durable send events must carry it so channel-scoped sampling can still be queried by `client_msg_no`.
- Manager diagnostics routes require `cluster.diagnostics:r`; cluster-wide diagnostics queries include alive, suspect, and draining nodes, skip dead nodes, and return `partial` when results are incomplete.
- Manager diagnostics tracking rules are runtime-only, TTL-bound sampler overrides for future events; sender UID rules match messages sent by that UID and never expose `from_uid` in manager event DTOs.
- Manager Monitor reads `/manager/monitor/metrics`; all-node scope fans out through node RPC and aggregates real dashboard collector series. Sum counters/gauges, recompute fail/fan-out ratios from deltas, and use max node P99 latency as the cluster P99 approximation.

### Send stress performance
- Send stress regression was concentrated in the leader durable path, not gateway ACK handling: `gateway.write_sendack` is microsecond-scale, while `store.commit.pebble_sync` and durable mutex/append waits dominate.
- Same-channel append batching and commit-coordinator batching often add waits without building large batches, so a sync still tends to carry only about one request and a few records.
- An out-of-order channelappend completion waiting for an earlier append sequence is pending but not runnable; only the missing completion callback can close the ordered-drain gap, so dormant gaps must not reactivate the writer.
- Throughput-mode send stress can also be capped before durable send by `gateway.async_dispatch_wait`; the acceptance preset uses a larger bounded async SEND worker pool so queued SEND frames can reach the durable path concurrently.
- Single-node cluster send stress is still a cluster-mode path; benchmark metadata should use the harness node IDs and `MinISR=1`, not hard-coded three-node replicas.
- In the three-node quorum send stress, lowering long-poll max wait alone does not remove the bottleneck; the current hot path is per-channel async SEND serialization plus leader quorum wait after local durable append.
- Local three-node durable-QPS results are invalid for software capacity attribution when all nodes share a nearly full filesystem; record `storage-preflight.tsv` and separate the backing device before tuning commit workers or shards.
- A separate high-channel three-node send stress should keep the original benchmark unchanged; raising channel cardinality alone has only a small QPS effect while leader quorum latency remains high.
- SEND batching is layered: gateway shards by raw `ChannelID + ChannelType` only to preserve entry ordering and collect micro-batches; message usecase groups adjacent canonical same-channel sends; `pkg/channel.AppendBatch` performs one replica append with contiguous seqs.
- Remote channel append forwarding supports one-channel batch RPC; falling back to per-message forwarding loses the durable/follower batching benefit for clients connected to non-leader nodes.
- Single SEND/Append entrypoints are compatibility wrappers; durable send and app channel append internals should route through batch-of-one to avoid split correctness/performance paths.
- `pkg/channel` append is local-runtime only: cluster must ensure/apply authoritative ChannelMeta first and forward non-leader appends to the resolved channel leader.
- In wukongim three-node Channel runtime activation, `routing.Route.Leader` is the observed Slot Raft leader for metadata proposals; `routing.Route.PreferredLeader` is the control-plane data-plane placement target for initial Channel runtime leader selection.
- wukongim single hot-channel SEND stress is sensitive to gateway async SEND shard count: too many default shards shrink per-channel queue headroom and can close sessions with `async_dispatch_queue_full` before Channel runtime saturates.
- wukongim 1000-channel three-node real-QPS stress with 4096 online users needs about 2048 gateway async SEND dispatch workers; 1024 workers creates per-shard SEND head-of-line blocking before Channel runtime is fully saturated.
- SENDACK must only follow a crash-safe durable message commit; message append `NoSync` is unsafe for this guarantee and must not be exposed as runtime/user configuration. Durable QPS work should optimize message DB grouped commits, not acknowledge before fsync.

## Cluster Membership

- `wkcli node` is an operations client for public manager HTTP only; dynamic
  node process startup remains seed-join driven and outside the CLI.

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
- Slot preferred leaders are Controller soft intent and Raft remains authoritative. When Controller tasks are idle, an independent background seam may transfer only from the fenced actual leader to the exact recently-active preferred voter after the latest applied intent and fresh Raft progress prove it is still current and caught up through commit. Start/snapshot apply never wait for this seam; strict checks default to 250ms, each pass rotates across at most four checks, preserves an existing manual transfer, and retains the valid actual leader without fallback for stale, ineligible, inactive, or lagging preferences.
- Manager Slot inventory keeps control intent and live Raft status separate: `runtime.preferred_leader_id` is Controller intent; `runtime.leader_id`, voter membership, quorum, sync, and leader-match require live Raft evidence, while selected-node detail also uses `node_log.leader_id` and `node_log.role`. Missing evidence stays unknown/unreported.
- Manager node-list Slot `leader_count` is also actual Slot Raft leadership from runtime status, not the Controller preferred leader count.
- Production controller planning writes must go through Controller Raft proposals; direct Store writes are only for local tests or tools.
- Bootstrap planning rotates the preferred `Task.TargetNode` across DesiredPeers by SlotID. That voter campaigns first after initial membership applies, but Raft eligibility and quorum remain authoritative; bootstrap completes with exact voter membership and any live leader in that voter set, without a post-election forced transfer.
- V2 UID authority targets must use distributed Slot identity `(hash slot, slot id, leader node, leader term, config epoch)` as the hard fence. Node-local `AuthorityEpoch` is diagnostic/order metadata only and must not be used as a cross-node hard fence.

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
- Loaded Channel metadata must advance monotonically by `(Epoch, LeaderEpoch)`; an equal fence may refresh authority fields only for the same channel identity and leader.
- PullHint is only a wakeup/refresh trigger. Unloaded followers must resolve authoritative metadata before opening storage through a dedicated bounded cold-activation pool; loaded newer-fence recovery uses its separate authoritative refresh pool.

### Node scale-in
- Manager-driven node scale-in drains a node to `ready_to_remove`; it must not call physical Slot removal or Kubernetes scale-down directly.
- Scale-in manager reads require `cluster.node:r` and `cluster.slot:r`; start/advance/cancel require `cluster.node:w` and `cluster.slot:w`.
- Node scale-in readiness must account for channel leaders, channel replicas, and active channel migration tasks before reporting `ready_to_remove`.

## Plugin Subsystem

- Plugin runtime is node-local and disabled by default; plugin-user bindings are Slot Raft metadata keyed by UID.
- Phase 1 supports `.wkp`/go-pdk core methods and host RPCs, but stream RPCs return explicit unimplemented errors.
- Plugin sends must go through `message.App.Send`; PersistAfter runs only on the channel owner node.
- Plugin migration changes should rerun the microbenchmark baseline in `docs/development/PLUGIN_BENCHMARK_BASELINE.md`, especially Send hook selection, host RPC mapping, PersistAfter, HTTP forward, and NoPersist realtime delivery.
- Plugin wire contracts live in `pkg/plugin/pluginproto`; keep protobuf field numbers compatible with `github.com/WuKongIM/go-pdk` and do not add new imports of old `internal/usecase/plugin/pluginproto`.
- The node-local plugin process host lives in `pkg/plugin/pluginhost`; internal app wiring adapts it to `internal/usecase/plugin` without depending on old plugin runtime code.

## Development Workflow

### E2E profiling
- API `/debug...` routes are exposed only when `WK_DEBUG_API_ENABLE=true`; e2e profile scenarios should enable it with node config overrides and fetch `/debug/pprof/*` through the real API listener.

### Worktree testing
- The chat Demo is embedded under the product API listener at `/demo/`; it defaults to the page origin for HTTP APIs and discovers WebSocket addresses through `/route`.
- When using project-local `.worktrees/*`, run Go tests with `GOWORK=off`; the parent `go.work` points at the main checkout and otherwise makes packages resolve under `.worktrees` incorrectly.
- Repository-wide Go gates must use `GOWORK=off` plus explicit roots (`cmd`, `internal`, `pkg`, `scripts`, `docker`, or `test/e2e`); root `./...` ignores `.gitignore` and can include local `tmp` or `web/node_modules` packages.
- `internal/gateway` now ships only the `gnet` transport; connection callbacks are serialized by actor shards and there is no `stdnet` fallback or per-connection writer goroutine.
- wk-sim performance investigations must follow the `docs/development/PERF_TRIAGE.md` runbook: collect evidence, classify, hypothesize, then run one-variable experiments.
- Node log output is split by `internal/log`: `app.log` contains info and above, `warn.log` contains warnings, `error.log` contains errors, and `debug.log` exists when debug logging is enabled.
- Bench APIs are benchmark-only `/bench/v1/*` routes gated by `WK_BENCH_API_ENABLE`; remotely reachable deployments must set the sensitive `WK_BENCH_API_TOKEN` bearer capability, and mutations go through benchdata plus user/channel usecase boundaries.
- Cloud Simulation billable creation and unattended cleanup use separate GitHub Environments; AccessKey mode requires a complete Repository Secret pair, while OIDC mode uses exact workflow-conditioned subjects. Cleanup must never require a reviewer because it is the lease backstop.
- Cloud Simulation may classify a run as released only after the locator matches the authenticated cloud account and region and the provider returns an empty exact-tag inventory; analysis then stops before Codex runs.
- Cloud Simulation destroy and sweep must prove the active credential matches the retained provider account before interpreting cleanup inventory or mutating resources.
- Cloud Simulation compute inventory readiness does not prove guest SSH readiness; Provision must probe the simulator jump and every private target, retry only bounded SSH transport failures, and log the exact bootstrap stage before transferring or installing the bundle.
- Cloud Simulation simple onboarding stores a complete Alibaba AccessKey pair as Repository Secrets and discovers non-secret provider config before billable creation; `scripts/cloud-sim/setup.sh` remains the optional hardened OIDC onboarding path.
- Cloud Simulation CloudShell tool downloads are checksum-pinned, IPv4/HTTP 1.1, bounded, resumable under the user cache, and missing Go prefers the Alibaba Golang mirror; do not add an unverified GitHub Release proxy.
- Cloud Simulation live Codex analysis uses `scripts/cloud-sim/analyze.sh` with local ChatGPT authentication; GitHub receives no OpenAI or Codex credential and hands off only a request-correlated RSA-OAEP-encrypted, run-scoped token.
- `wkbench-diagnostic-summary/v1` is a strict producer/consumer contract: any field change must update the report producer, cloudanalysis parser, MCP DTO, and producer-shaped fixtures together because unknown fields are rejected.
- Cloud Simulation workers must set an explicit bounded WKProto client capacity profile; omitting it activates the generic 8192-entry per-client queues and can exhaust simulator memory at stability scale.
- wkbench warmup records typed session-operation failures and continues, while timed measurements continue individual message failures; send, sendack, recv, and recvack errors, including SENDACK rejection and receive payload mismatch, must retain their session and operation code so configured error-rate limits decide the verdict instead of failing the phase, phase cancellation is excluded from error counters, and structural harness failures remain fail-fast.
- wkbench warmup may extend early cold-channel operations to the warmup duration, but all in-flight waits must share a final deadline at warmup end plus the original traffic operation timeout; coordinator polling must include the same tail and report child-deadline exhaustion as `phase_timeout`.
- Cloud Simulation Codex tool subprocesses inherit no caller environment and run under strict filesystem/network permission profiles; Codex auth and the live Analysis Token stay in the parent Codex/MCP process, tools cannot read them, and model-authored diagnosis text never enters Draft PR metadata.
- Cloud Simulation local analysis ignores project exec rules and rejects deployed `.codex/config.toml` or `.codex/hooks.json` before Codex starts so repository configuration cannot widen its permission profiles.
- Cloud Simulation cleanup reconstructs temporary ingress deadlines from provider security rules; sweeps preserve unexpired local Analysis Windows and close expired, malformed, or duplicate windows.
- Cloud Simulation normal completion uses a non-diagnostic Finalization Schedule plus local `finalize.sh`: retry an explicit in-progress workload while the lease permits, run exact cleanup even after diagnosis/remediation failure, then require structured provider-confirmed empty inventory.
- Cloud Simulation stability topology uses 256 physical hash slots mapped to 10 logical Slot Raft Groups; bootstrap gates both values separately.
- Cloud Simulation Bootstrap Gate accepts a non-zero actual Slot Raft leader that belongs to the current voter set when quorum and peer sync are healthy; `PreferredLeader` mismatch is placement evidence, not a health failure.
- Conversation-active flush evidence must distinguish selected, acknowledged-persisted, cooldown-skipped, actually cleared, version-conflicted retry rows, and superseded stale snapshots; a successful store call does not prove that version-fenced dirty markers were cleared, while a failed cross-Slot store call leaves durable progress unknown.
- Conversation-active cooldown must classify the current dirty version's ReadSeq advance, not a historical cached ReadSeq; after a persisted snapshot conflicts with a newer cache version, only a ReadSeq beyond that snapshot remains sender-dirty. Bounded flush selection must cover each live dirty address before repeating, and dirty-age indexes must be bounded by live dirty rows rather than cumulative updates.
- Standard Cloud Simulation verdicts require a 48h/168h reviewed small, medium, or large profile plus empirical 30m storage calibration; shorter durations are diagnostic evidence only.
- Any node OOM increment or WuKongIM process start-time change during a Cloud Simulation run invalidates performance and storage calibration; use an intentional phase-boundary profiling rerun for root-cause isolation, then a separate passive calibration after remediation.
- Phase-ending process loss may occur after the last coarse scrape; query at 5-second steps through 90 seconds after the phase end, and compare heap `inuse_space` with `alloc_space` so transient allocation spikes are not mistaken for retained memory.
- Cloud node hosts persist `wukongim.service` cgroup v1/v2 memory peak, effective limit, swap, and cumulative OOM events through node-exporter textfiles; Bootstrap Gate must prove this evidence is readable on all three nodes before workload start.
- Workflow input `public_observation` optionally enables a Run-Lease-bounded `0.0.0.0/0:19443` Cloud View on sim; real Demo/WS use and Manager writes annotate benchmark purity, while authenticated gate probes are excluded and must prove Manager, Demo, WS, and all seven Prometheus targets.
- wkbench requires server bench mode (`WK_BENCH_API_ENABLE=true`) and must prepare target data only through `/bench/v1/*`; it must not use Manager APIs for benchmark setup.
- wkbench traffic with `recv_ack=true` must drain delivered recv frames and send protocol recvack even when receive verification is `none`; otherwise delivery retry will keep re-pushing accepted routes until they expire.
- `wkcli sim` online users must start client PING heartbeats as soon as each CONNECT succeeds; slow full-pool connection phases must not leave early sessions idle until all users connect.
- `pkg/client` WKProto client batch-send changes should run `scripts/bench-pkg-client-baseline.sh`; hard allocation guards and recorded baseline numbers live in `docs/development/PKG_CLIENT_BENCHMARK_BASELINE.md`.
- Committed delivery routing treats transient channel status errors such as `channel: not ready` as retry signals; warn only after retries are exhausted.
- `wkbench dev-sim` must run warmup after prepare/connect and before measured run windows; warmup must touch every assigned channel at least once, and warmup counters are a baseline that must not be counted in `/status` measured traffic.
- `wkbench dev-sim` `/status` distinguishes the configured steady-state online pool (`connected_users`) from the latest sampled live count (`active_users`) and reconnect churn (`reconnected_users`) so online flapping is visible during triage.
- `wkbench` run-phase polling must budget deterministic churn reconnect pacing in addition to measured traffic duration; otherwise low connect rates turn healthy scheduled churn into a false `phase_timeout`.
- `wkbench` worker state retains the complete Assignment, but status JSON must expose only assignment identity so polling cost is independent of Plan/Scenario size; legacy expanded status responses remain decodable.
- A bounded worker status request that times out before the phase deadline is `worker_status`; at the phase deadline, use one independent final probe so an active response is `phase_completion` while another blocked response remains `worker_status`.
- `wkbench` must prove every assigned worker reached exact `run_id + assignment_id` terminal stop before report collection, including non-fail-fast timeouts; a stopped identity is immutable and cannot be reused, `assignment_id` changes on same-ID reruns, stop serializes assignment work, outlives the HTTP caller, joins retries, waits hook exit, and tears down runner resources. Unconfirmed stop writes only minimal `worker_stop_failed` evidence; successful metrics/report reads use the same two-part identity.
- Timed `wkbench` warmup and run polling must also budget the largest declared SENDACK/RECV timeout after scheduling stops; the base control-plane grace is not an operation-tail budget.
- Mixed `wkbench` traffic must retain drained RECV frames only for channel types with receive verification; buffering unverified group fanout because person traffic verifies will create an unconsumed simulator backlog and invalidate the run.
- `wkbench` connection rate limits attempt start times; do not sleep a full interval after each handshake, because per-connection handshake latency accumulates into false connect-phase timeouts at large online counts.
- In three-node real-QPS SEND benchmarks, the promoted transport runtime's channel append RPCs need a larger service pool than generic RPCs; Channel runtime store append/apply defaults should stay capped near the shared message DB commit coordinator instead of scaling unbounded with CPU count.
- Stage 2 package promotion uses promoted names in default evidence and human-readable output (`channel`, `transport`) while keeping raw Prometheus inputs and legacy aliases such as `wukongim_channelv2_*`, `component="channelv2"`, and `channelv2_metrics_summary.tsv` compatible.
- Stage 2 package promotion has physically moved the canonical runtimes to `pkg/channel`, `pkg/cluster`, `pkg/controller`, and `pkg/transport`; the old implementations have been removed, and new imports must not target `pkg/*v2`.
- Promoted production roots must not import old runtime paths; `pkg/slot/proxy` has no legacy imports.
- `pkg/cluster.Node` satisfies `pkg/slot/proxy.Cluster` plus the optional hash-slot proposer port; Slot proxy RPC handler registration goes through `pkg/cluster.Node.RegisterRPC`.
- In local three-node real-QPS runs, message DB commit shards are an experimental default-off knob: 3000, 4000, and 16k evidence all show that multiple coordinators on one physical store fragment group commits and increase sync tail; prefer one coordinator with bounded store append/apply workers unless nodes use independently proven storage parallelism.
- Stage 2 package promotion extracted protocol-facing channel ID helpers to `pkg/protocol/channelid`; v1 and v2 server packages must not add new imports of old `internal/runtime/channelid`.
