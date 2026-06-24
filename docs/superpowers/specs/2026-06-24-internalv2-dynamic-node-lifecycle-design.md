# internalv2 Dynamic Node Lifecycle Design

Date: 2026-06-24
Status: Proposed for review
Scope: internalv2, clusterv2, ControllerV2, Slot Multi-Raft, manager node lifecycle APIs

## 1. Purpose

internalv2 must support adding data nodes to a running cluster without editing
every node config and restarting the cluster. The design must also leave a
clean path for future node removal. This is a control-plane feature first: a
new node becomes a durable cluster member, proves it can sync cluster state, and
only then becomes eligible for Slot and Channel placement.

V1 supports dynamic add for data/mirror nodes only. Dynamic Controller Raft
voter add/remove is a separate later design because quorum membership changes
have different safety rules from data-plane capacity changes.

Single-node deployment remains a single-node cluster. No startup, routing,
storage, or send path should bypass cluster semantics.

## 2. Current State

The current codebase already has useful pieces:

- `pkg/controllerv2/state.Node` has durable `JoinState` values:
  `active`, `joining`, and `leaving`.
- `pkg/clusterv2/net.Discovery` is already an atomic node-address snapshot,
  and `clusterv2.Node.applySnapshot` updates discovery from control snapshots.
- Slot Multi-Raft already exposes membership changes at the low level:
  `AddLearner`, `PromoteLearner`, and `RemoveVoter`.
- ControllerV2 tasks already provide fenced execution for bootstrap and Slot
  leader transfer.

Important gaps:

- `cmd/wukongimv2` still requires static `WK_CLUSTER_NODES`.
- `clusterv2.Config.validateSlots` currently couples Slot replica count to
  Controller voter count instead of active data-node capacity.
- `pkg/clusterv2/control.Snapshot` does not expose node `join_state`, so
  internalv2 management currently hard-codes membership state as `active`.
- `pkg/clusterv2.Node` exposes only Slot leader-transfer writes, not node
  join, activate, leave, or Slot replica move writes.
- `pkg/clusterv2/slots.Runtime` does not expose `ChangeConfig`, even though
  `pkg/slot/multiraft.Runtime` supports it.
- There is no ControllerV2 task kind for Slot replica movement.

## 3. Goals

1. A new data node can start with seed addresses and a join token.
2. Existing nodes discover the joined node from ControllerV2 membership without
   restarts.
3. Joining, active, and leaving states are visible in internalv2 manager APIs.
4. A joining node is not schedulable until it proves state sync and local
   runtime readiness.
5. Resource movement is explicit, bounded, observable, and safe under large
   clusters, large groups, active channels, and many online users.
6. Future node removal uses the same lifecycle vocabulary and safety checks.
7. Controller voter changes are excluded from V1 and remain operator-explicit
   future work.

## 4. Non-Goals

- Do not dynamically add or remove Controller Raft voters in V1.
- Do not automatically rebalance all Slot or Channel ownership immediately
  after join.
- Do not remove physical Slots as part of node removal.
- Do not place migration logic in SEND, append, fetch, delivery, or presence
  hot paths.
- Do not rely on local-only shard scans for cluster-wide safety reports.
- Do not support concurrent node removal jobs in the first scale-in slice.

## 5. Core Model

Membership and health are separate.

Membership answers whether a node belongs to the cluster and what lifecycle
state it is in:

```text
joining -> active -> leaving -> removed
```

Health answers whether the node is currently usable:

```text
alive | suspect | down
```

`joining` nodes may appear in discovery so peers can connect to them, but they
must not be selected as Slot DesiredPeers or Channel replicas. `active` data
nodes may receive new placement. `leaving` nodes stay reachable for draining
and reads, but planners and placement code must exclude them from new work.

V1 does not have to physically delete removed nodes from `cluster-state.json`.
A tombstone or `removed` state is safer than deleting identity immediately,
because it prevents accidental reuse of a stale `node_id`.

## 6. Configuration

Add v2 config keys:

```conf
WK_CLUSTER_SEEDS=["127.0.0.1:7011","127.0.0.1:7012"]
WK_CLUSTER_ADVERTISE_ADDR=127.0.0.1:7014
WK_CLUSTER_JOIN_TOKEN=change-me
```

Semantics:

- `WK_CLUSTER_SEEDS` is the startup discovery set used before ControllerV2
  membership is available.
- `WK_CLUSTER_ADVERTISE_ADDR` is the node RPC address stored in ControllerV2
  membership for this node.
- `WK_CLUSTER_JOIN_TOKEN` authenticates join requests.
- `WK_CLUSTER_NODES` remains valid for static bootstrap and existing local
  scripts. After a control snapshot is installed, ControllerV2 membership is
  the runtime discovery source.

`WK_CLUSTER_HASH_SLOT_COUNT` should stay explicit in production configs and is
normally 256. Changing hash-slot count is outside this design.

`SlotConfig.ReplicaCount` validation must use the count of eligible data nodes
from the initial or current membership, not the Controller voter count.

## 7. Join Flow

New node startup:

```text
process start
  -> parse node id, advertise addr, seeds, join token
  -> start clusterv2 transport with seed discovery
  -> call JoinNode through one seed
  -> seed forwards to current Controller leader when needed
  -> Controller leader validates and writes joining membership
  -> joining node syncs ControllerV2 state file
  -> joining node starts local clusterv2 runtime in non-schedulable mode
  -> node passes readiness gates
  -> ActivateNode flips join_state to active
  -> active node can receive future Slot/Channel placement
```

Join validation:

- `node_id` must be non-zero.
- `advertise_addr` must be non-empty and must not collide with another active
  or joining node.
- Repeated join for the same `node_id + advertise_addr` is idempotent.
- Same `node_id` with a different address is rejected unless a future explicit
  replacement flow permits it.
- Same address with a different `node_id` is rejected.
- Cluster ID must match.
- Join token must match configured policy.
- Role defaults to data mirror, not Controller voter.
- Capacity weight defaults to one.

Activation validation:

- Node exists and is `joining`.
- Node is reachable through typed node RPC.
- Node has a current ControllerV2 mirror snapshot with matching cluster ID.
- Node reports local clusterv2 readiness: routes, channels, and required local
  runtime resources are ready.
- Node is not a Controller voter unless it was part of initial static bootstrap.

## 8. Control Plane Changes

Add node lifecycle write methods in `pkg/controllerv2.Runtime` and expose them
through `pkg/clusterv2/control.Runtime`:

```go
JoinNode(ctx, NodeJoinRequest) (NodeLifecycleResult, error)
ActivateNode(ctx, NodeActivateRequest) (NodeLifecycleResult, error)
MarkNodeLeaving(ctx, NodeLeavingRequest) (NodeLifecycleResult, error)
MarkNodeRemoved(ctx, NodeRemovedRequest) (NodeLifecycleResult, error)
```

These should propose ControllerV2 commands and reuse existing leader-forwarding
behavior used by task writes. The public clusterv2 `Node` should expose narrow
methods that internalv2 management can call, instead of letting internalv2
import ControllerV2 internals.

Extend `pkg/clusterv2/control.Node`:

```go
type Node struct {
    NodeID         uint64
    Addr           string
    Roles          []Role
    Status         NodeStatus
    JoinState      NodeJoinState
    CapacityWeight uint32
}
```

Manager node list should derive:

- `membership.join_state` from control snapshot.
- `membership.schedulable` from `role == data`, `join_state == active`, and
  `status == alive`.
- action hints from real wired capabilities, not hard-coded false once lifecycle
  routes are migrated.

## 9. Dynamic Discovery

Discovery should keep two layers:

```text
seed discovery: available before join and before first snapshot
membership discovery: authoritative after a valid control snapshot
```

`Resolve(node_id)` should first check the current membership snapshot, then
fall back to seed entries. `applySnapshot` already updates discovery from
control nodes, so the main change is to preserve seeds before the first
snapshot and to ensure newly joined data nodes enter the discovery snapshot as
soon as membership commits.

When a node transitions to removed, discovery should stop resolving that node
after existing drain safety has completed.

## 10. Onboarding And Slot Movement

V1 join only makes the node available. Moving existing Slot replicas is a
separate bounded operator action:

```text
POST /manager/nodes/:node_id/onboarding/plan
POST /manager/nodes/:node_id/onboarding/start
GET  /manager/nodes/:node_id/onboarding/status
POST /manager/nodes/:node_id/onboarding/advance
POST /manager/nodes/:node_id/onboarding/cancel
```

The first onboarding implementation should move Slot replicas only. It should
not migrate all historical Channel replicas automatically.

Add ControllerV2 task kind:

```text
slot_replica_move
```

Safe Slot replica move workflow:

```text
plan source and target for one Slot
  -> write desired assignment plus slot_replica_move task
  -> target opens local Slot runtime
  -> current Slot leader AddLearner(target)
  -> wait learner catch-up through Slot status
  -> PromoteLearner(target)
  -> transfer leadership if source is still leader and source is leaving
  -> RemoveVoter(source)
  -> complete task after observed voters match DesiredPeers
```

The executor should run through `pkg/clusterv2/tasks`, not through manager
handlers directly. Manager `advance` only creates a bounded number of tasks.

Initial limits:

- `max_slot_moves` default: 1
- `max_slot_moves` hard cap: 5
- one active task per physical Slot
- prefer moving Slots with highest source-node replica skew
- do not touch Slots with no quorum, unknown leader, failed active task, or
  apply gap that indicates the runtime is not caught up

## 11. Future Node Removal

Removal starts by marking the node `leaving`. That state means:

- no new Slot DesiredPeers
- no new Channel initial replicas
- keep node reachable for draining
- keep existing reads and replication paths alive

Removal status must fail closed. The node is safe to remove only when all of
the following are true:

- target node exists and is `leaving`
- target is not a Controller voter
- no Slot DesiredPeers include target
- no live Slot Raft leader is target
- no active ControllerV2 task references target
- no failed task blocks convergence
- full ChannelRuntimeMeta inventory confirms target is not leader, replica, or
  ISR for any channel
- target runtime summary confirms no active online sessions, closing sessions,
  or gateway sessions
- runtime summary and inventory are fresh, not unknown

Removal APIs should mirror legacy scale-in semantics:

```text
POST /manager/nodes/:node_id/scale-in/plan
POST /manager/nodes/:node_id/scale-in/start
GET  /manager/nodes/:node_id/scale-in/status
POST /manager/nodes/:node_id/scale-in/advance
POST /manager/nodes/:node_id/scale-in/cancel
```

The final `MarkNodeRemoved` should be explicit and allowed only after
`safe_to_remove=true`.

## 12. Channel Placement And Drain

ChannelV2 initial placement already derives candidates from alive data nodes.
After a new node becomes active, new channels can naturally include it through
rendezvous selection.

Existing channels require explicit migration later. Node removal cannot ignore
them. The manager scale-in report must scan authoritative
`ChannelRuntimeMeta` by physical Slot and count:

- channel leaders on target
- channel replicas on target
- channel ISR membership on target
- active channel migration tasks involving target

The first dynamic join release does not need to move historical channels. The
first safe remove release must include Channel inventory and drain gates before
reporting `safe_to_remove`.

## 13. Presence And Delivery

Presence authority follows hash-slot route changes published by clusterv2. A
node joining does not immediately move hash-slot authority, so it should not
scan or replay all online routes.

When future Slot movement changes route authority:

- route authority events must carry the same distributed fence fields already
  used by internalv2: hash slot, slot ID, leader node ID, leader term, and
  config epoch.
- old authority should drain through the existing authority handoff path.
- owner-local active sessions remain local to their gateway node until the
  connection closes or is explicitly drained.

Gateway admission should become drain-aware before removal is declared safe:

- active nodes accept new sessions.
- leaving nodes reject or stop accepting new sessions.
- removal waits for active and closing session counters to reach zero.

## 14. Observability

Expose low-cardinality metrics and manager fields:

- node lifecycle counts by join state and status
- join attempts by result
- activation attempts by result
- onboarding tasks active and failed
- slot replica move duration and failure reason
- leaving-node safety blocked reason counts
- discovery membership revision

Do not put node IDs or channel IDs into high-cardinality Prometheus labels
unless the metric already has a bounded per-node purpose. Manager detail pages
can show per-node and per-Slot details through APIs.

## 15. Performance Rules

- All lifecycle operations are operator-path operations, not hot-path SEND work.
- Onboarding and drain must advance in bounded batches.
- Full ChannelRuntimeMeta scans are acceptable for operator safety, but they
  must page by physical Slot and return partial/unknown as unsafe.
- Slot replica movement must observe apply/commit watermarks and avoid adding
  pressure when Slot Raft is already behind.
- Default manager views must stay lightweight; full-cardinality lifecycle
  inventories should be explicit detail pages or paginated reports.

## 16. Delivery Stages

### Stage 1: Read Model And Config Foundation

- Add seed, advertise address, and join token config parsing.
- Preserve static `WK_CLUSTER_NODES` bootstrap.
- Add join state and capacity to clusterv2 control snapshots.
- Fix manager node DTOs to show real lifecycle state.
- Decouple Slot replica validation from Controller voter count.

### Stage 2: Dynamic Join And Activation

- Add JoinNode and ActivateNode ControllerV2 writes.
- Add node RPC join handling and leader forwarding.
- Start new node from seeds, sync state, then activate.
- Add manager read surface for joining nodes.
- Add e2ev2 test for adding a fourth data node to a running three-node cluster.

### Stage 3: Slot Onboarding

- Add Slot replica move task kind and executor.
- Expose `ChangeConfig` through clusterv2 Slot runtime interfaces.
- Add bounded onboarding manager APIs.
- Verify message send continues while one Slot replica moves.

### Stage 4: Scale-In Preparation

- Add MarkNodeLeaving and scale-in status report.
- Stop new placement on leaving nodes.
- Add Slot leader transfer and Slot replica drain actions.
- Add fail-closed safety checks for unknown runtime data.

### Stage 5: Channel And Connection Drain

- Add full ChannelRuntimeMeta inventory for target node.
- Add bounded Channel migration tasks when the Channel task system is ready.
- Add gateway admission drain mode and remote runtime summary checks.
- Allow MarkNodeRemoved only after safe-to-remove report passes.

## 17. Test Plan

Focused unit tests:

- ControllerV2 FSM validates join, activate, leave, and removed transitions.
- Join is idempotent for the same node ID and address.
- Join rejects node ID/address conflicts.
- Snapshot mapping preserves join state and capacity.
- Manager node list reports real `joining`, `active`, and `leaving` states.
- Config parser handles seeds and advertise address.
- Slot replica move planner excludes joining, leaving, suspect, and down nodes.

clusterv2 tests:

- Node starts with seed discovery before first snapshot.
- Discovery updates when membership changes.
- Mirror node syncs ControllerV2 state after joining.
- Slot replica move executor performs AddLearner, PromoteLearner, and
  RemoveVoter in order.
- Route authority changes include updated config epoch after Slot movement.

internalv2 and e2ev2 tests:

- Manager join/onboarding routes enforce permissions and request validation.
- Running three-node cluster admits a fourth data node through seed join.
- New node becomes visible as joining, then active.
- New channels after activation may place replicas on the new data node.
- SEND -> SENDACK continues during a bounded Slot replica move.
- Leaving node is never reported safe while Slot, Channel, or connection state
  is unknown.

Operational verification:

- Run focused Go tests for `pkg/controllerv2`, `pkg/clusterv2`,
  `internalv2/usecase/management`, `internalv2/access/manager`, and e2ev2
  lifecycle packages.
- Use `wkcli sim` or existing v2 smoke scripts to verify real client traffic
  during onboarding.
- Watch Slot proposal apply gap, transport inflight, ChannelV2 worker queues,
  ControllerV2 task metrics, and DB commit latency during migration.

## 18. Open Decisions

The following decisions are intentionally fixed for V1:

- Dynamic join is data/mirror only.
- Controller voter changes are excluded.
- Historical Channel migration is not required for the first join release.
- Node removal requires fail-closed reports and should not physically delete
  identity until safe removal is proven.

Future designs can extend this with Controller voter membership changes,
automatic balanced onboarding, and durable node decommission jobs.
