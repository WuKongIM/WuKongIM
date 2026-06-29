# pkg/clusterv2 Flow

## Responsibility

`pkg/clusterv2` is a parallel cluster runtime composition root. It wires ControllerV2 state, Slot Multi-Raft metadata storage, typed node RPC, and ChannelV2 log replication behind a small public API.

The root `Node` stays thin: it owns lifecycle, readiness, public API delegation, and snapshot fan-out only. Foreground routing, Slot propose, ChannelV2 replication, Controller state mapping, and node RPC each live in focused subpackages.

## Source Reading Path

- Start with `api.go`, `config.go`, and `node.go` for the public surface.
- Read `node_lifecycle.go`, `node_defaults.go`, `node_snapshot.go`, and `node_loops.go` for root runtime wiring details.
- Read `default_slots.go`, `default_slot_leaders.go`, and `default_slot_proposer.go` for the default Slot path.
- Root `Node` tests follow the same split: lifecycle, defaults, snapshot, channel, and shared helpers.
- In `channels`, read `service.go` first, then `meta.go`, `slot_meta.go`, `placement.go`, and `transport.go`.
- In `control`, `snapshot.go` is the read model, while `snapshot_validate.go` and `snapshot_clone.go` hold model mechanics.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `control` | Controller abstraction, root ControllerV2 facade adapter, snapshot adapter, Raft RPC, and state sync RPC. |
| `routing` | Atomic HashSlot -> Slot -> Leader read model for hot paths. |
| `net` | Typed node-to-node RPC and discovery glue; Go package name is `clusternet`. |
| `slots` | Slot Multi-Raft runtime open/bootstrap/reconcile/status. |
| `propose` | Slot metadata propose path and leader forwarding. |
| `channels` | ChannelV2 service construction, metadata resolve/ensure, append leader forwarding, and replication transport. |
| `observe` | Low-frequency background loops and readiness snapshots. |

## Route Authority And Node RPC Surface

`Node.RegisterRPC` and `Node.CallRPC` expose a narrow typed node-to-node RPC
surface for upper-layer internalv2 adapters. Handlers registered before the
default transport starts are replayed during transport construction; later
registrations are installed idempotently. Internalv2 delivery uses this surface
for owner-node push RPC and partition-leader fanout RPC; clusterv2 only routes
the payload and does not inspect delivery DTOs. Internalv2 manager connection
pages also use this surface to read owner-node online connection inventory from
peer nodes without adding manager-specific logic to clusterv2. Internalv2
manager distributed log pages use the same surface to ask a selected peer to
read its own local Controller or Slot Raft log page, and manager channel list
pages use it to ask a selected peer to scan its local Slot metadata pages.
Internalv2 manager Controller Raft status and manual compaction use the same
surface for node-scoped operations; clusterv2 exposes only the selected node's
local operation and does not fan out or interpret manager policy. Internalv2
manager Slot Raft manual compaction uses the same node-scoped surface through
`Node.LocalCompactSlotRaftLog`, which delegates only to the selected node's
local Slot Multi-Raft runtime. Internalv2 manager message retention forwarding
uses the same surface to carry one logical ChannelV2 compaction-boundary
request to the channel leader; clusterv2 transports the payload only, while the
receiving leader revalidates runtime metadata and Slot metadata fences.
Internalv2 seed-join and readiness probes also use this typed RPC surface:
clusterv2 routes `RPCNodeLifecycle` payloads only, and the app-level node RPC
adapter validates join tokens, cluster IDs, and management lifecycle policy.
Manager Slot leader transfer enters clusterv2 through
`Node.RequestSlotLeaderTransfer`, which only foreground-checks and delegates
the already-validated intent to the control runtime. When the
receiving node is not the Controller leader, the existing control task RPC
forwarding path carries the creation request to the Controller leader.
Lifecycle write primitives for node join, activation, scale-in leaving, and
future removed tombstone operations enter clusterv2 through `Node.JoinNode`,
`Node.ActivateNode`, `Node.MarkNodeLeaving`, and `Node.MarkNodeRemoved`, which
only foreground-check and delegate the validated lifecycle intent to the control
runtime. Drain safety for removal stays in upper management usecases; clusterv2
only exposes the lower-level lifecycle primitive. The `removed` state remains a
durable control-plane tombstone; clusterv2 does not physically delete node
identity or decide that a leaving node is safe to remove. `MarkNodeRemoved`
transports an optional `state_revision` fence supplied by the management
safe-to-remove check to ControllerV2, but it does not compute that safety
itself. The fence guards changed remove writes; already-removed tombstones
remain idempotent in ControllerV2. Non-leader Controller runtimes forward those
writes through the generic control-write RPC path as `join_node`,
`activate_node`, `mark_node_leaving`, or `mark_node_removed`.
Staged Slot replica move intent enters clusterv2 through
`Node.RequestSlotReplicaMove`, which only foreground-checks and delegates the
already-planned intent to the control runtime. It uses the same generic
control-write path: the clusterv2 control runtime forwards `slot_replica_move`
creation to the Controller leader and keeps the durable assignment unchanged
until the later ControllerV2 commit command. The
default transport-backed
typed RPC client uses a larger per-priority write queue than the generic
transport default so short foreground RPC fanout bursts are absorbed before
local enqueue backpressure is returned to the send path. It also opens multiple
outbound connections per peer and shards typed RPCs by service class, keeping
foreground ChannelV2 append forwarding off the same connection used by follower
pull and pull-hint traffic.

`WatchRouteAuthorities` publishes hash-slot authority changes derived from the
installed routing table. Each `RouteAuthority` carries
`(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch, RouteRevision,
AuthorityEpoch)`. `LeaderTerm` comes from the observed Slot Raft leader and
`ConfigEpoch` comes from the control-plane Slot assignment, so upper layers can
use `(HashSlot, SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)` as the
distributed authority identity. `AuthorityEpoch` is only a node-local
observation sequence retained for diagnostics and compatibility; it must not be
used as a distributed fence. `RouteKey`, `RouteKeys`, and `RouteHashSlot`
include the distributed identity fields plus the local epoch. `RouteKeys`
resolves all keys against one installed routing snapshot and returns results in
input order, allowing upper layers to batch UID authority lookups without
repeatedly loading the foreground route table. Real publication paths remember
the last distributed identity published per hash slot and suppress duplicate
events for the same `(SlotID, LeaderNodeID, LeaderTerm, ConfigEpoch)`, so a
local `AuthorityEpoch` is not manufactured for already-published identities.
Default Slot leader observation
treats `Leader=0` as an unknown observation and keeps the last known non-zero
Slot leader and term in the foreground router until a new non-zero leader is
observed. This prevents transient Raft status gaps from briefly removing an
otherwise valid route; stale leaders are still fenced by downstream Slot/Channel
leadership checks.

## Start Flow

```text
New(Config)
  -> validate v2-only config
  -> create Router and Discovery
  -> apply optional WithProposer / WithChannels overrides

Start(ctx)
  -> initialize default node RPC transport / ControllerV2 runtime / proposer / ChannelV2 service when no override was provided
  -> initialize a real Slot Multi-Raft runtime for default propose
  -> seed node RPC discovery from configured Controller voters, or from seed-join
     seed addresses for mirror nodes, until the first control snapshot arrives
  -> start default transport and injected lifecycle resources
  -> start ControllerV2-backed Controller or injected Controller
  -> wait for a valid initial control snapshot
  -> routing.UpdateControlSnapshot(snapshot)
  -> discovery.Update(control node addresses plus seed-join seed addresses when configured)
  -> slots.Reconcile(snapshot)
  -> start Controller watch loop for later snapshots
  -> start the default Slot leader observation loop when the default Slot runtime is active
  -> mark ChannelV2 ready and start the tick loop
  -> mark node started
  -> start low-frequency ControllerV2 health reporting
```

`Start` requires cluster semantics even for one node. A single-node cluster uses a ControllerV2-backed single-voter control runtime instead of a bypass path. Multi-voter default startup uses `pkg/transportv2` one-way service messages for ControllerV2 Raft traffic and RPC responses only for state-sync requests. The ControllerV2 Raft receive handler bounds local `Step` enqueue time and may drop messages when the local Step queue is saturated; Raft retransmission is relied on instead of allowing one-way notify goroutines to accumulate indefinitely.

`Node.Start` only establishes local-node readiness: the node has a valid local control snapshot, installed routes, reconciled local Slot runtime state, and started local ChannelV2 resources. Package tests use `WaitClusterReady` for converged local control snapshots, and tests that specifically require distributed Controller write readiness should add the separate Controller proposal probe gate. Slot and Channel append tests should add their own Slot leader or Channel metadata gates when those paths are part of the assertion.

Seed-join mirror nodes keep `Config.Control.Voters` empty so they do not become
Controller voters before admission. During default runtime wiring, the
configured `Join.Seeds` addresses are converted into temporary state-sync peer
IDs used only for transport discovery and ControllerV2 mirror sync. After the
first real control snapshot arrives, normal membership discovery is installed
while those seed peers remain available for later mirror refreshes.
Once a seed-join mirror sees its own membership state become `active`, it still
does not host Slot replicas in Stage 2. It therefore refreshes foreground Slot
routes by batching `RPCSlotStatus` reads to the existing desired Slot peers and
installing the observed actual leader and term. While the mirrored local state
is still `joining`, it does not install preferred leaders and public readiness
continues to be activation-only.

ControllerV2 changes enter clusterv2 as strongly typed `controllerv2.ClusterState` events. `pkg/clusterv2/control` maps those events to `control.Snapshot`; `Node` then compares node, Slot, task, and hash-slot domains before touching discovery, Slot runtime reconciliation, or foreground routing.

When a control snapshot contains active bootstrap, leader-transfer, or staged
slot-replica-move tasks, the Node runs task executors after Slot
reconciliation. Executors only report participant progress, fenced phase
advancement, fenced commit, or fenced completion through the control task
writer facade; they do not mutate ControllerV2 state directly. Task writes
from non-leader Controller runtimes are forwarded to the current Controller
leader. Leader-transfer execution calls Slot Raft `TransferLeadership` from
the current Slot leader and completes once the observed actual leader is any
legal non-source Slot Raft leader; the requested `target_node` is preferred,
not a strict completion requirement. Slot replica movement first opens the
target's local learner runtime, then advances Slot Raft membership through
add-learner, promote-learner, remove-voter, and finally commits the durable
assignment only after observed voters match the target peer set.

`Config.Control.RaftObserver` is passed through to the default ControllerV2
runtime so composition roots can expose Controller Raft ingress queue metrics
without changing control-plane semantics.
`Config.Control.TaskTransitionObserver` is also passed through to the default
ControllerV2 runtime. Clusterv2 only transports the already-durable task edges;
it does not store audit history, inspect legacy `pkg/controller` state, or
reinterpret task lifecycle policy.

Default ChannelV2 message, Slot metadata, and Slot Raft Pebble-backed stores
are exposed through `Node.StorageMetricsSnapshot` as low-cardinality
`channel_log`, `meta`, and `raft` snapshots. Pebble types remain behind the
storage packages; clusterv2 only publishes the neutral metrics shape used by
composition roots.

### Health Report Loop

The node health report loop sends compact runtime evidence through
`control.ReportNode`: `status`, `runtime_ready`, observed control revision,
observed Slot revision, and a node-local report sequence. Each report uses a
bounded per-report context, and `runtime_ready` is false while the node is
stopping. ControllerV2 fills the leader-side report timestamp, stores the report
durably, and control snapshots derive `fresh`, `stale`, or `missing` health from
the configured TTL.

## Stop Flow

```text
Stop(ctx)
  -> mark stopping and reject new foreground calls
  -> stop low-frequency ControllerV2 health reporting
  -> stop Controller watch loop
  -> stop ChannelV2 tick loop
  -> stop ChannelV2 physical retention cleanup loop
  -> close hosted ChannelV2 service
  -> stop ControllerV2-backed Controller or injected Controller
  -> stop injected lifecycle resources in reverse order
```

## Propose Hot Path

```text
Node.Propose
  -> propose.Service
  -> routing.Router atomic table lookup
  -> encode [version:1][hashSlot:2][command]
  -> if local leader: SlotRuntime.Propose
  -> else: clusternet RPCSlotForwardPropose
  -> remote ForwardHandler re-checks local Slot leadership
  -> SlotRuntime.Propose
```

The propose path returns typed not-ready/no-leader/not-leader errors and does not synchronously call Controller APIs.

## Slot Metadata Facade Flow

`node_meta.go` exposes small metadata facades used by `internalv2` adapters.
Channel metadata, subscriber rows, and legacy `channel_latest` rows route by
channel ID. Subscriber point lookups and subscriber-set non-emptiness reads use
the same channel-owned Slot metadata route for message permission checks.
`GetChannelRuntimeMeta` reads authoritative channel runtime metadata from the
channel's current hash-slot route, and `AdvanceChannelRetentionThroughSeq`
proposes a fenced Slot FSM command that only advances the channel message
retention boundary. Manager history deletion must use this metadata advance
instead of deleting ChannelV2 message rows directly.
Physical message cleanup is a separate node-local background loop and is
disabled by default. One `RunChannelRetentionGCOnce` pass reads a bounded page
from the local message catalog, loads the authoritative Slot metadata boundary,
and delegates boundary adoption plus physical trim safety checks to the
ChannelV2 retention runtime. The Node loop only keeps the catalog cursor and
per-pass counts; HW/checkpoint/LEO/MinISR consistency remains inside ChannelV2.
`UpsertChannelLatestBatch` first resolves each channel's real hash slot, then
groups rows by physical Slot and submits bounded batch commands carrying
per-row hash slots. `ScanChannelsSlotPage`,
`ScanChannelRuntimeMetaSlotPage`, and `ScanUsersSlotPage` are read-only manager
facades that scan metadata rows owned by one physical Slot, merging the Slot's
hash-slot shards into one legacy-compatible ordered page.

UID-owned reverse tables route by UID. `UpsertUserChannelMemberships` and
`DeleteUserChannelMemberships` group the requested UIDs by `RouteKey(uid)` hash
slot and submit one Slot proposal per touched hash slot. Reads such as
`ListUserChannelMembershipPage` also route by UID and read the current local
metadata shard for that UID hash slot.

Plugin binding rows are UID-owned for writes and Receive hook lookups.
`BindPluginUser`, `UnbindPluginUser`, and `ListPluginBindingsByUID` route by
UID hash slot. `ListPluginBindingsByPluginNo` is the plugin-centric manager
scan path: it walks the installed hash-slot route table, asks each hash slot's
current Slot leader for one `plugin_binding` secondary-index candidate, merges
those candidates by UID with a heap, and returns an opaque cursor carrying the
last emitted `(plugin_no, uid)`. The scan never reads only the manager node's
local DB as a cluster-wide answer, and it does not materialize all bindings for
one plugin before paging.

Kind-aware UID-owned conversation rows are the active recent-conversation path.
`UpsertConversationStatesBatch`, `TouchConversationActiveAtBatch`, and
`HideConversationsBatch` route each row by `RouteKey(uid)`, group rows by
physical Slot, and submit bounded Slot FSM commands that carry each row's real
UID hash slot plus its `ConversationKind`. `TouchConversationActiveAtBatch`
marks those proposals as background admission work because they are retryable
active-cache projection flushes; user-facing UID metadata writes keep the
default foreground class. The Slot FSM then applies each conversation state,
active patch, or delete barrier to that UID-owned hash slot and logical kind,
preserving `SparseActive`, read/delete visibility floors, and the active
ordering anchor in one metadata mutation. Hide requests advance `DeletedToSeq`
and clear `active_at` through the same Slot ownership path. Reads such as
`GetConversationState`, `GetConversationStates`, and
`ListConversationActivePage` route by UID and require a
`ConversationKind` so normal and CMD projections stay isolated. Active pages
scan the local conversation active index for that UID hash slot and selected
kind with the `(active_at, channel_id, channel_type)` cursor; kind is part of
the scan scope, not the cursor. Legacy `channel_latest` remains a channel-owned
projection for old callers and is not the recent-conversation active path.

## ChannelV2 Flow

```text
Node.AppendChannel / AppendChannelBatch
  -> channels.Service
  -> Append: EnsureChannelMeta from append-only ChannelMetaEnsurer when available
      -> SlotMetaSource reads authoritative ChannelRuntimeMeta from Slot metadata storage
      -> if missing: derive initial replicas/leader from Slot placement and persist through RuntimeMetaWriter
      -> reread final authoritative ChannelRuntimeMeta when local Slot state has caught up; otherwise return the deterministic initial Meta after a successful write
      -> if local node is channel leader: ApplyMeta to local ChannelV2 runtime, then Append locally
      -> else: RPCChannelAppend / RPCChannelAppendBatch forward to the resolved channel leader
  -> local reactor and store worker pools
  -> clusterv2 channel RPC client
  -> remote channel RPC handler
  -> follower reactor Pull / Apply / Ack
```

Append forwarding uses a channel-key shard on the typed node RPC transport, and
the default transportv2 wiring gives append, follower pull, and pull-hint
traffic separate service queues plus weighted priorities. Foreground channel
mutation services also use a larger default service concurrency: ChannelV2
append-forward handlers mostly wait on ChannelV2 append/quorum futures, and
internalv2 `RPCChannelAuthoritySend` handlers wait on channelappend futures.
ChannelV2, channelappend writers, and DB pools keep the real storage
backpressure boundary. This keeps foreground write pressure visible separately
from replication traffic and reduces head-of-line blocking when they share peer
connections. If a forwarded append times out and the origin
node is also a channel replica, `channels.Service` may perform a bounded local
committed-message lookup. Recovery reports success only for message ids whose
durable row is visible under local HW; missing or uncommitted rows keep the
original forward error, preserving the normal quorum durability boundary.
The origin node records both aggregate `forward_append` latency and
`forward_append_rpc` for the typed RPC round trip. The leader-side append
forward handler records `forward_append_remote`, which includes request decode
and the remote `channels.Service` append path. These sub-stages separate
origin-side dispatch/transport wait from the leader's actual append/quorum
work when diagnosing forwarded SEND p99.

`Node.ReadChannelCommitted` is a narrow read facade for internalv2 HTTP message
sync. It opens the Node-created default ChannelV2 store for the requested
channel, resolves the authoritative `ChannelRuntimeMeta`, applies
`RetentionThroughSeq + 1` as the minimum visible message sequence, and then
delegates to `channelv2/store.ReadCommitted`; it does not replace ChannelV2
append, replication, or metadata routing. Callers that override the
ChannelV2 service without using the Node-created default store do not
automatically get this read facade.

`Node.ReadChannelLastVisible` is the channel-owned routed read facade used by
conversation list display. It resolves authoritative ChannelRuntimeMeta for the
channel, reads the local store only when this node is the ChannelV2 leader, and
otherwise forwards a typed RPC to the resolved leader. The leader-side handler
validates local channel leadership before reading its local store with a reverse
limit-1 committed read and applying the maximum of the caller's visibility
floor and `RetentionThroughSeq`. Channel not found or no visible tail returns
`ok=false`; route, not-ready, not-leader, and stale-route errors propagate to
the caller.

`WithProposer` and `WithChannels` are public override options for tests, smoke harnesses, and app-level composition. If callers do not provide them, `Node.Start` creates a default ControllerV2 runtime, proposer, and ChannelV2 service, backs ChannelV2 with the message DB under `DataDir/channellog`, registers ChannelV2 replication/append-forward handlers on the default node RPC transport, and owns the ChannelV2 tick loop plus default store factory cleanup. The default proposer is backed by a real local Slot Multi-Raft runtime, durable Slot Raft log storage under `DataDir/slotraft`, metadata FSM storage under `DataDir/slotmeta`, and clusterv2 typed RPC transport for multi-replica Slot Raft traffic.
`Config.Slots.Observer` is passed to the default Slot Multi-Raft runtime so composition roots can expose scheduler pressure without changing Slot processing semantics.
`Config.Slots.LogCompaction` is also passed through to the default Slot
Multi-Raft runtime so composition roots can tune local Slot Raft snapshot
compaction without changing proposal or apply semantics. Slot Raft transport
batches use a versioned binary frame that carries raw `raftpb.Message` bytes
instead of JSON encoding, avoiding base64 expansion on the metadata replication
path. The default Slot Raft transport declares `ReadyMessagePayloadOwner`
because it synchronously encodes the batch before `Send` returns, so
`pkg/slot/multiraft` can avoid deep-copying large entry and snapshot payloads on
this production path.

## Distributed Log Inspection Flow

`Node.LocalControllerLogEntries` reads the local ControllerV2 Raft WAL through
the control facade and returns newest-first, cursor-paginated entry summaries.
`Node.LocalSlotLogEntries` reads the local default Slot Raft DB for one physical
Slot, joins runtime commit/applied watermarks when available, and decodes Slot
FSM proposal payloads into JSON-friendly inspection fields. Slot log inspection
also reads the Multi-Raft proposal envelope's `created_at_ms` timestamp when
present. These methods are read-only diagnostics for manager UI pages; they do
not route writes, replay entries, or mutate Raft storage.

`Node.LocalControllerRaftStatus` and `Node.LocalCompactControllerRaftLog` are
separate node-local ControllerV2 Raft management facades. Status reports local
role, term, commit/apply, and durable log/snapshot watermarks. Manual
compaction calls the local ControllerV2 runtime's materialized-state snapshot
path and returns whether a snapshot was created, skipped, or failed. Cluster
fan-out is owned by `internalv2/usecase/management`, not clusterv2.

`channels.Service` keeps a combined runtime interface because the public ChannelV2 `Cluster` surface and replication `transport.Server` surface are separate. `StaticMetaSource` is available for tests and smoke runs. `SlotMetaSource` adapts authoritative `pkg/db/meta` `ChannelRuntimeMeta` records into ChannelV2 metadata for production wiring. `ResolveChannelMeta` remains read-only; `EnsureChannelMeta` is the append-only path that may create the initial ChannelRuntimeMeta through the Slot-owned metadata writer before any ChannelV2 append is attempted. `SlotMetaSource` emits low-cardinality metadata resolve sub-stages for Slot meta read, initial placement/build, missing-meta write/propose, aggregate create/write, and final reread so cold activation tail latency can be attributed before pprof. In the default runtime, `meta_create_propose` wraps the Slot metadata writer call; `meta_create_propose_local` and `meta_create_propose_forward` split origin-side routing, `meta_create_slot_propose_submit` times local `Runtime.Propose`, and `meta_create_slot_propose_wait` times the subsequent Multi-Raft future wait. The default proposer also bridges the append stage observer into `pkg/slot/multiraft`, allowing the same ChannelV2 stage histogram to report `meta_create_slot_control_wait`, `meta_create_slot_raft_commit_wait`, `meta_create_slot_fsm_apply`, `meta_create_slot_fsm_commit`, and `meta_create_slot_mark_applied`.

Initial ChannelV2 placement is data-plane placement, not Slot metadata
placement. Slot routing identifies the authoritative metadata Slot and its
leader/peers; the default ChannelV2 placement resolver chooses replicas from
effective-active data-role nodes using deterministic rendezvous ranking and
uses the route preferred leader only when that node is selected as a ChannelV2
replica. Placement intentionally ignores `NodeStatus` until health reports have
freshness; joining, leaving, and removed data nodes remain visible but are not
new placement candidates. Existing `ChannelRuntimeMeta` rows remain
authoritative for established channels.

ChannelV2 PullHint RPCs carry only a slim metadata reference and wakeup fields.
An unloaded or newer-fence follower creates reactor-owned `PendingMeta` and
uses `Pull{NeedMeta=true}` to fetch active metadata from the channel leader
runtime before applying any records. The service layer does not resolve Slot
metadata for PullHint receive; client append admission still uses
`EnsureChannelMeta`.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

When `Config.Channel.ReactorCount` is left at zero, clusterv2 derives a CPU-aware ChannelV2 reactor count from `GOMAXPROCS` with a minimum of four partitions. Explicit positive values are preserved for deployments that need to pin the runtime shape. `Config.Channel.StoreAppendWorkers`, `Config.Channel.StoreApplyWorkers`, and `Config.Channel.RPCWorkers` cap the blocking leader-append, follower-apply, and replication RPC worker pools independently; zero keeps ChannelV2's reactor-derived defaults, which give store pools extra workers but cap them to avoid overdriving the shared message DB commit coordinator, and never changes durable commit or quorum ACK rules. `Config.Channel.StoreAppendBatchMaxWait` can shorten the store-append worker's cross-channel coalescing wait; zero keeps the ChannelV2 worker default. `Config.Storage.CommitShards` can route message DB commit requests across partition-hashed coordinators while preserving synchronous physical commits and per-channel append locking; zero keeps the single-coordinator default. `Config.Channel.AppendBatchMaxRecords`, `Config.Channel.AppendBatchMaxWait`, `Config.Channel.AppendBatchAdaptiveFlush`, and `Config.Channel.AppendBatchColdMaxWait` pass through to the hosted ChannelV2 runtime; zero values and the default disabled adaptive flag keep the ChannelV2 defaults. `Config.Channel.Observer` is passed to the default ChannelV2 service so composition roots can expose reactor mailbox, append batch, and worker pool metrics without changing channel append semantics.

## Non-Goals For V1

- Do not replace or modify `pkg/cluster`.
- Do not add compatibility with old cluster data or old cluster config.
- Do not add hash-slot migration, onboarding, drain, scale-in, or full operator APIs.
- Do not add bypass branches that treat a single-node cluster as anything other than a cluster.

## V1 Limitations

- ControllerV2 integration supports ControllerV2-backed runtime startup, single-node cluster bootstrap, static multi-voter bootstrap, mirror sync, and multi-voter Raft transport wiring through `pkg/transportv2`. Dynamic production operator workflows remain outside this package-level slice.
- Slot coverage now uses the real default Slot runtime for default propose in single-node clusters and static multi-node clusters. Destructive Slot cleanup remains disabled.
- ChannelV2 append forwarding and first-append metadata creation require a configured Slot-backed ChannelMetaSource and Forward client; without them, pre-applied local runtime state is required and non-leader appends return ChannelV2 typed errors.
- Channel RPC codecs use a version byte plus JSON payload as a temporary v1 format; replace with binary codecs before optimizing this data path.
- Observe loops are intentionally small and low-frequency; foreground write paths only read atomic route/channel state.
