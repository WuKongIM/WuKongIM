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
the payload and does not inspect delivery DTOs. The default transport-backed
typed RPC client uses a larger per-priority write queue than the generic
transport default so short foreground RPC fanout bursts are absorbed before
local enqueue backpressure is returned to the send path. It also opens multiple
outbound connections per peer and shards typed RPCs by service class, keeping
foreground ChannelV2 append forwarding off the same connection used by follower
pull and pull-hint traffic.

`WatchRouteAuthorities` publishes hash-slot authority changes derived from the
installed routing table. Each `RouteAuthority` carries
`(HashSlot, SlotID, LeaderNodeID, RouteRevision, AuthorityEpoch)` so callers can
fence authority-side state. `AuthorityEpoch` changes when the observed authority
identity changes for a hash slot and is included in `RouteKey` / `RouteHashSlot`
results.

## Start Flow

```text
New(Config)
  -> validate v2-only config
  -> create Router and Discovery
  -> apply optional WithProposer / WithChannels overrides

Start(ctx)
  -> initialize default node RPC transport / ControllerV2 runtime / proposer / ChannelV2 service when no override was provided
  -> initialize a real Slot Multi-Raft runtime for default propose
  -> seed node RPC discovery from configured Controller voters until the first control snapshot arrives
  -> start default transport and injected lifecycle resources
  -> start ControllerV2-backed Controller or injected Controller
  -> wait for a valid initial control snapshot
  -> routing.UpdateControlSnapshot(snapshot)
  -> discovery.Update(control node addresses)
  -> slots.Reconcile(snapshot)
  -> start Controller watch loop for later snapshots
  -> start the default Slot leader observation loop when the default Slot runtime is active
  -> mark ChannelV2 ready and start the tick loop
  -> mark node started
```

`Start` requires cluster semantics even for one node. A single-node cluster uses a ControllerV2-backed single-voter control runtime instead of a bypass path. Multi-voter default startup uses `pkg/transport` one-way service messages for ControllerV2 Raft traffic and RPC responses only for state-sync requests. The ControllerV2 Raft receive handler bounds local `Step` enqueue time and may drop messages when the local Step queue is saturated; Raft retransmission is relied on instead of allowing one-way notify goroutines to accumulate indefinitely.

`Node.Start` only establishes local-node readiness: the node has a valid local control snapshot, installed routes, reconciled local Slot runtime state, and started local ChannelV2 resources. Package tests use `WaitClusterReady` for converged local control snapshots, and tests that specifically require distributed Controller write readiness should add the separate Controller proposal probe gate. Slot and Channel write tests should add their own Slot leader or Channel metadata gates when those paths are part of the assertion.

ControllerV2 changes enter clusterv2 as strongly typed `controllerv2.ClusterState` events. `pkg/clusterv2/control` maps those events to `control.Snapshot`; `Node` then compares node, Slot, task, and hash-slot domains before touching discovery, Slot runtime reconciliation, or foreground routing.

`Config.Control.RaftObserver` is passed through to the default ControllerV2
runtime so composition roots can expose Controller Raft ingress queue metrics
without changing control-plane semantics.

## Stop Flow

```text
Stop(ctx)
  -> mark stopping and reject new foreground calls
  -> stop Controller watch loop
  -> stop ChannelV2 tick loop
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

Append forwarding uses a channel-key shard on the typed node RPC transport so
foreground append traffic does not share one ordered connection with follower
pull or pull-hint traffic. If a forwarded append times out and the origin node
is also a channel replica, `channels.Service` may perform a bounded local
committed-message lookup. Recovery reports success only for message ids whose
durable row is visible under local HW; missing or uncommitted rows keep the
original forward error, preserving the normal quorum durability boundary.

`WithProposer` and `WithChannels` are public override options for tests, smoke harnesses, and app-level composition. If callers do not provide them, `Node.Start` creates a default ControllerV2 runtime, proposer, and ChannelV2 service, backs ChannelV2 with the message DB under `DataDir/channellog`, registers ChannelV2 replication/append-forward handlers on the default node RPC transport, and owns the ChannelV2 tick loop plus default store factory cleanup. The default proposer is backed by a real local Slot Multi-Raft runtime, durable Slot Raft log storage under `DataDir/slotraft`, metadata FSM storage under `DataDir/slotmeta`, and clusterv2 typed RPC transport for multi-replica Slot Raft traffic.

`channels.Service` keeps a combined runtime interface because the public ChannelV2 `Cluster` surface and replication `transport.Server` surface are separate. `StaticMetaSource` is available for tests and smoke runs. `SlotMetaSource` adapts authoritative `pkg/db/meta` `ChannelRuntimeMeta` records into ChannelV2 metadata for production wiring. `ResolveChannelMeta` remains read-only; `EnsureChannelMeta` is the append-only path that may create the initial ChannelRuntimeMeta through the Slot-owned metadata writer before any ChannelV2 append is attempted. `SlotMetaSource` emits low-cardinality metadata resolve sub-stages for Slot meta read, initial placement/build, missing-meta write/propose, aggregate create/write, and final reread so cold activation tail latency can be attributed before pprof. In the default runtime, `meta_create_propose` wraps the Slot metadata writer call; `meta_create_propose_local` and `meta_create_propose_forward` split origin-side routing, `meta_create_slot_propose_submit` times local `Runtime.Propose`, and `meta_create_slot_propose_wait` times the subsequent Multi-Raft future wait. The default proposer also bridges the append stage observer into `pkg/slot/multiraft`, allowing the same ChannelV2 stage histogram to report `meta_create_slot_control_wait`, `meta_create_slot_raft_commit_wait`, `meta_create_slot_fsm_apply`, `meta_create_slot_fsm_commit`, and `meta_create_slot_mark_applied`.

Initial ChannelV2 placement is data-plane placement, not Slot metadata
placement. Slot routing identifies the authoritative metadata Slot and its
leader/peers; the default ChannelV2 placement resolver chooses replicas from
alive data nodes using deterministic rendezvous ranking and uses the route
preferred leader only when that node is selected as a ChannelV2 replica.
Existing `ChannelRuntimeMeta` rows remain authoritative for established
channels.

ChannelV2 PullHint RPCs carry only a slim metadata reference and wakeup fields.
An unloaded or newer-fence follower creates reactor-owned `PendingMeta` and
uses `Pull{NeedMeta=true}` to fetch active metadata from the channel leader
runtime before applying any records. The service layer does not resolve Slot
metadata for PullHint receive; client append admission still uses
`EnsureChannelMeta`.

Bench runtime controls flow from internalv2 HTTP through `internalv2/infra/cluster`, `pkg/clusterv2.Node`, `pkg/clusterv2/channels.Service`, and finally the hosted ChannelV2 runtime. These routes are benchmark-only observation/cleanup controls and do not replace the gateway SEND activation path.

When `Config.Channel.ReactorCount` is left at zero, clusterv2 derives a CPU-aware ChannelV2 reactor count from `GOMAXPROCS` with a minimum of four partitions. Explicit positive values are preserved for deployments that need to pin the runtime shape. `Config.Channel.StoreAppendWorkers` and `Config.Channel.StoreApplyWorkers` cap the blocking leader-append and follower-apply worker pools independently; zero keeps ChannelV2's reactor-derived defaults and never changes durable commit or quorum ACK rules. `Config.Channel.AppendBatchMaxRecords` and `Config.Channel.AppendBatchMaxWait` pass through to the hosted ChannelV2 runtime; zero keeps the ChannelV2 defaults. `Config.Channel.Observer` is passed to the default ChannelV2 service so composition roots can expose reactor mailbox, append batch, and worker pool metrics without changing channel write semantics.

## Non-Goals For V1

- Do not replace or modify `pkg/cluster`.
- Do not add compatibility with old cluster data or old cluster config.
- Do not add hash-slot migration, onboarding, drain, scale-in, or full operator APIs.
- Do not add bypass branches that treat a single-node cluster as anything other than a cluster.

## V1 Limitations

- ControllerV2 integration supports ControllerV2-backed runtime startup, single-node cluster bootstrap, static multi-voter bootstrap, mirror sync, and multi-voter Raft transport wiring through `pkg/transport`. Dynamic production operator workflows remain outside this package-level slice.
- Slot coverage now uses the real default Slot runtime for default propose in single-node clusters and static multi-node clusters. Destructive Slot cleanup remains disabled.
- ChannelV2 append forwarding and first-append metadata creation require a configured Slot-backed ChannelMetaSource and Forward client; without them, pre-applied local runtime state is required and non-leader appends return ChannelV2 typed errors.
- Channel RPC codecs use a version byte plus JSON payload as a temporary v1 format; replace with binary codecs before optimizing this data path.
- Observe loops are intentionally small and low-frequency; foreground write paths only read atomic route/channel state.
