# Automatic Control-Plane Group Management Design

## Summary

WuKongIM currently manages control-plane `multiraft` groups through static
configuration. Each node receives the full `cluster.groups` list, validates it,
and opens or bootstraps those groups during startup. This keeps the system
simple, but it pushes replica placement, failure repair, and rebalance work to
manual operations.

This design keeps `GroupCount` and `SlotForKey -> GroupID` routing stable while
replacing static per-group peer configuration with a controller-driven
placement system. Cluster node membership remains statically configured. Group
replica membership becomes automatically managed by a dedicated controller
running on top of an independent bootstrap Raft quorum.

## Goals

- Remove static business-group peer assignment from cluster configuration.
- Keep `GroupCount` fixed and preserve existing key-to-group routing.
- Keep cluster node membership statically configured.
- Maintain a fixed target replica count `ReplicaN` for every managed group.
- Continue serving a group as long as quorum remains available.
- Automatically repair missing replicas after node failure.
- Automatically rebalance groups after node recovery or node addition.
- Make reconfiguration progress explicit, serialized, and recoverable across
  controller leader changes.

## Non-Goals

- No dynamic `GroupCount`.
- No automatic node discovery or automatic node membership changes.
- No external control plane such as etcd or an operator.
- No automatic recovery for groups that have already lost quorum.
- No rewrite of `pkg/replication/multiraft` core execution semantics.
- No change to business routing semantics in `metastore`, `channel`, `user`,
  or subscriber paths.

## Current State

### Static group configuration

`internal/app.Config.Cluster.Groups` defines every control-plane group with an
explicit `GroupID` and `Peers`. Validation requires the configured ids to cover
`1..GroupCount` exactly and ensures peers are members of the configured node
set.

### Static startup lifecycle

`raftcluster.Cluster.Start()` creates transport and `multiraft.Runtime`, then
iterates the configured groups and calls `OpenGroup` or `BootstrapGroup` for
each one. The execution layer already supports runtime operations such as
`OpenGroup`, `CloseGroup`, `ChangeConfig`, and `TransferLeadership`, but the
cluster layer currently treats the group set as immutable after startup.

### Stable hash routing

`Router.SlotForKey(key)` maps a business key to a `GroupID` via CRC32 modulo
`GroupCount`. This routing is deterministic for a fixed `GroupCount` and is
already used throughout the control-plane metadata store. The design must keep
this behavior unchanged.

## Decision

The system will introduce a dedicated `GroupController` that owns the desired
replica placement of all managed control-plane groups.

The architecture splits into:

- bootstrap controller raft:
  - a small dedicated Raft quorum used only to persist controller state and
    elect the controller leader
- `GroupController`:
  - the only component allowed to decide desired peers for a managed group
- `GroupAgent` on every node:
  - reports local facts and executes controller-approved actions locally
- managed business groups:
  - dynamically opened, closed, and reconfigured according to controller
    assignments

This design explicitly preserves static routing while centralizing placement and
membership decisions in a single controller.

## Architecture

### 1. Routing remains fixed

Business code continues to route `key -> GroupID` using the existing
`SlotForKey` logic. The controller does not change the group number of any key.
It only changes which nodes host the group.

### 2. A controller becomes the placement authority

The controller becomes the source of truth for:

- desired peers of each managed group
- fault repair decisions
- rebalance decisions
- migration progress
- safety gating for configuration changes

Nodes and agents do not perform independent global peer selection.

### 3. Managed groups gain a dynamic lifecycle

The cluster layer must evolve from:

- static bootstrap of all groups from config

to:

- bootstrap an independent controller raft from config
- start a node-local `GroupAgent`
- fetch or watch assignments from the controller
- dynamically open missing managed groups
- dynamically close groups that no longer belong to the local node

`multiraft` remains the execution engine. The required change is above it in
`raftcluster` and app composition.

## Controller Metadata

Controller metadata must live in a dedicated controller state machine rather
than inside existing business metadata tables such as `ChannelRuntimeMeta`.

The controller manages placement and task progress, not channel business state.

### `ClusterNode`

- `NodeID`
- `Addr`
- `Status`
  - `Alive`
  - `Suspect`
  - `Dead`
  - `Draining`
- `LastHeartbeatAt`
- `CapacityWeight`

`CapacityWeight` defaults to `1` in v1.

### `GroupAssignment`

- `GroupID`
- `DesiredPeers []NodeID`
- `ConfigEpoch`
- `BalanceVersion`

This is the desired state.

### `GroupRuntimeView`

- `GroupID`
- `CurrentPeers []NodeID`
- `LeaderID`
- `HealthyVoters`
- `HasQuorum`
- `ObservedConfigEpoch`
- `LastReportAt`

This is the observed state, reported by agents.

### `ReconcileTask`

- `GroupID`
- `Kind`
  - `Bootstrap`
  - `Repair`
  - `Rebalance`
- `Step`
  - `AddLearner`
  - `CatchUp`
  - `Promote`
  - `TransferLeader`
  - `RemoveOld`
- `SourceNode`
- `TargetNode`
- `Attempt`
- `LastError`

Tasks make reconfiguration progress explicit and restart-safe.
`Bootstrap` is reserved for first-time creation of a managed business group
that has never been formed before.

## Controller Inputs

The controller state machine only accepts:

### Node and agent reports

- node heartbeats
- observed group runtime views
- local execution results

### Controller-issued commands

- assignment updates
- reconcile task transitions
- node status transitions such as `Alive`, `Dead`, and `Draining`

This separation keeps desired state and observed state distinct and lets a new
controller leader resume in-flight work after failover.

## Node State Model

The node state model is:

- `Alive`
- `Suspect`
- `Dead`
- `Draining`

Rules:

- heartbeat present: `Alive`
- brief timeout: `Suspect`
- timeout past failure threshold: `Dead`
- operator-driven evacuation: `Draining`

There is no separate `Recovered` state in v1.

- a previously `Dead` or `Suspect` node becomes `Alive` again when heartbeats
  are re-established and the controller accepts it back into scheduling
- a `Draining` node becomes `Alive` again only through the explicit
  `ResumeNode(nodeID)` operator action

`Suspect` must not trigger immediate migration. This avoids placement churn
from transient network or process jitter.

## Placement Policy

v1 uses a simple fixed-replica placement policy.

### Fixed replica count

Every managed group targets the same `ReplicaN`.

### Candidate filtering

When selecting a target node:

- exclude current peers
- exclude `Dead` nodes
- exclude `Draining` nodes

### Balancing heuristic

Among remaining `Alive` nodes, choose the node with the fewest assigned groups.
Use a stable tie-break to avoid unnecessary oscillation.

### Priority ordering

The controller must prioritize:

1. repair for groups that still have quorum
2. draining-induced migration
3. rebalance after recovery or expansion

Rebalance must never preempt an active repair path.

## Startup Flow

### Current flow

Today the cluster startup path:

1. starts transport
2. starts `multiraft.Runtime`
3. opens or bootstraps every configured business group

### Target flow

The new flow becomes:

1. start transport
2. derive the controller peer set by sorting `WK_CLUSTER_NODES` by `NodeID`
   ascending and taking the first `WK_CLUSTER_CONTROLLER_REPLICA_N` nodes
3. if the local node belongs to that derived controller peer set, open the
   dedicated controller raft replica
4. if the local node belongs to that derived controller peer set, start the local
   controller raft service
5. on the controller leader only, run `GroupController` decision logic
6. on every node, start `multiraft.Runtime` for managed business groups
7. on every node, start local `GroupAgent`
8. register and heartbeat with the controller leader
9. fetch local assignments
10. open local managed groups dynamically

This requires `raftcluster` to support runtime-managed groups in addition to
the independent controller raft bootstrap path.

Node-role split in v1 is explicit:

- nodes in the derived controller peer set host the controller Raft replica and
  are eligible to become controller leader
- only the controller leader runs placement and reconcile decision logic
- non-controller peers run `GroupAgent` plus a lightweight controller client,
  but never host controller state

## Managed Group Bootstrap Flow

Brand-new managed groups need an explicit first-creation path distinct from
repair and rebalance.

When a business group id `1..GroupCount` has no existing runtime view and no
persisted assignment history:

1. the controller leader creates an initial `GroupAssignment` for that group
2. the controller leader creates a `ReconcileTask{Kind: Bootstrap}`
3. the task selects the initial `DesiredPeers` using the same placement rules
   as v1 repair targets
4. each assigned peer agent ensures local storage and state-machine handles
   exist
5. each assigned peer with empty persisted Raft state calls local
   `BootstrapGroup(groupID, voters)` using the controller-provided peer set
6. once runtime views show the group exists and a leader has been elected, the
   controller marks the bootstrap task complete

Responsibility is split as follows:

- the controller leader decides the initial peer set and bootstrap epoch
- the controller leader is the single authority that authorizes first-time
  bootstrap by committing the `Bootstrap` task
- assigned peer agents perform the local bootstrap call when local state is
  empty
- later lifecycle changes for the same group use `Repair` or `Rebalance`,
  never `Bootstrap` again

## Reconfiguration Flow

v1 supports exactly one safe migration sequence.

### Repair sequence

When a group still has quorum but is missing a healthy replica:

1. controller selects a new target node
2. `AddLearner(target)`
3. wait until the learner catches up
4. `PromoteLearner(target)` or `AddVoter(target)`
5. if leadership sits on the node that will be removed, first
   `TransferLeadership`
6. `RemoveVoter(source)`
7. commit the new desired peer set
8. mark the reconcile task complete

### Rebalance sequence

Rebalance uses the same sequence. The only difference is the trigger:

- `Repair`: failed or missing replica
- `Rebalance`: group distribution skew above threshold

### Hard safety constraints

- never perform a bulk peer replacement
- replace at most one peer per task
- never remove an old voter before the new peer is caught up and promoted
- keep migration steps explicit and idempotent

## Quorum-Loss Semantics

If a managed group has already lost quorum:

- mark the group `Degraded`
- stop automatic membership change
- expose alerts and manual recovery interfaces

v1 does not attempt automatic rescue in this state. Without quorum, the system
does not have a safe source of truth for the latest log, and automatic rebuild
would risk divergence.

This design intentionally separates:

- quorum-preserving automatic repair
- quorum-lost manual recovery

## Agent Responsibilities

### The controller decides

- desired peers
- repair timing
- rebalance timing
- whether a task can advance to the next step

### The agent executes

- local heartbeats
- local group reports
- local open/close/config-change actions approved by the controller
- result reporting

### The agent does not decide

- global target-node choice
- rebalance policy
- placement ownership

## Operational Interfaces

The system should expose at least:

- `MarkNodeDraining(nodeID)`
- `ResumeNode(nodeID)`
- `ListGroupAssignments()`
- `ForceReconcile(groupID)`
- `TransferGroupLeader(groupID, nodeID)`
- `RecoverGroup(groupID, strategy)`

`RecoverGroup` is reserved for manual quorum-loss recovery and is out of the
automatic v1 path.

## Failure Semantics

### Controller unavailable

- already-open managed groups continue running
- no new automatic reconfiguration decisions are made
- agents keep reporting but do not invent new configuration changes

### Node flapping

- mark `Suspect` first
- do not reassign immediately
- transition to `Dead` only after the failure timeout

### Task failure

- persist the task
- retry with backoff
- surface an operator-visible failure state after retry exhaustion

### Controller leader failover

- new controller leader resumes from persisted assignments and tasks
- replayed steps must be idempotent

## Configuration Evolution

The long-term configuration surface should become:

- `WK_CLUSTER_GROUP_COUNT`
- `WK_CLUSTER_NODES`
- `WK_CLUSTER_CONTROLLER_REPLICA_N`
- `WK_CLUSTER_GROUP_REPLICA_N`

Business-group static peer lists are removed. Only the controller bootstrap path
remains static.

### Bootstrap controller config

v1 uses exactly one dedicated controller raft quorum outside the business
`multiraft` group namespace.

Managed business group ids remain exactly:

- managed business group ids: `1..GroupCount`

`WK_CLUSTER_CONTROLLER_REPLICA_N` defines the size of that dedicated
controller raft quorum.

Rules:

- `WK_CLUSTER_NODES` must contain unique `NodeID` values
- `WK_CLUSTER_CONTROLLER_REPLICA_N` must be greater than `0`
- `WK_CLUSTER_CONTROLLER_REPLICA_N` must be less than or equal to
  `len(WK_CLUSTER_NODES)`
- the controller peer set is derived deterministically by sorting
  `WK_CLUSTER_NODES` by `NodeID` ascending and taking the first
  `WK_CLUSTER_CONTROLLER_REPLICA_N` nodes
- `WK_CLUSTER_GROUP_COUNT` still defines only managed business groups
- a node does not need to belong to the derived controller peer set to join the
  cluster as a managed-group worker
- controller raft state must be persisted separately from managed-group
  `multiraft` storage; v1 should use a dedicated controller-raft storage path
  or a dedicated subdirectory under the node data directory

### Startup validation changes

Current validation around static business `cluster.groups` is replaced by:

- `WK_CLUSTER_NODES` must be present and contain the local node
- `WK_CLUSTER_NODES` must contain unique `NodeID` values
- `WK_CLUSTER_GROUP_COUNT` must be greater than `0`
- `WK_CLUSTER_CONTROLLER_REPLICA_N` must be greater than `0`
- `WK_CLUSTER_CONTROLLER_REPLICA_N` must be less than or equal to
  `len(WK_CLUSTER_NODES)`
- `WK_CLUSTER_GROUP_REPLICA_N` must be greater than `0`
- static business-group peer lists are no longer required

### First-boot and restart behavior

For the dedicated controller raft:

- if persistent local controller-raft state exists, open it and trust persisted
  controller membership rather than re-deriving a new peer set from config
- otherwise derive the initial controller peer set from `WK_CLUSTER_NODES` and
  `WK_CLUSTER_CONTROLLER_REPLICA_N`
- on first cluster boot, only the smallest `NodeID` in that derived controller
  peer set is allowed to execute the controller-raft bootstrap operation
- the remaining derived controller peers start in join-wait mode and do not
  issue a concurrent bootstrap
- once controller-raft state is persisted, future restarts must not silently
  reshape controller membership from config drift; controller-peer changes must
  happen through explicit controller-raft reconfiguration

For managed business groups:

- do not bootstrap them from static config
- wait for controller assignment and then open or bootstrap as directed by the
  controller

## Affected Areas

### New packages

- `pkg/cluster/groupcontroller`
- `pkg/cluster/controllerraft`
- `pkg/storage/controllermeta`

### Modified packages

- `pkg/cluster/raftcluster`
- `internal/app/config.go`
- `internal/app/build.go`

### Expected minimal-change areas

- `pkg/replication/multiraft`
- existing `SlotForKey` routing semantics
- business metadata routing in `metastore`

## Testing Strategy

### Unit tests

- placement calculation
- repair and rebalance trigger rules
- node status transitions
- task step idempotency

### Controller FSM tests

- heartbeat timeout transitions
- `Suspect -> Dead -> Alive`
- `Draining -> Alive` through `ResumeNode`
- assignment version progression

### Multi-node integration tests

- three nodes, replica count three, one node failure keeps quorum and service
- automatic replica repair after a node becomes `Dead`
- automatic rebalance after node recovery or node addition
- draining migrates groups away without unsafe bulk movement

### Fault tests

- controller leader change during in-flight reconcile
- agent restart during task execution
- leader transfer during migration when leadership starts on the node being
  removed

## Migration Strategy

The migration must be incremental.

### Phase 1: introduce the controller without control

- keep existing static `cluster.groups`
- bootstrap the dedicated controller raft
- collect node heartbeats and observed runtime views only

### Phase 2: recommendation-only mode

- compute candidate assignments
- do not execute changes
- validate placement math and observations

### Phase 3: repair-only mode

- allow automatic missing-replica repair after node failure
- do not enable rebalance yet

### Phase 4: enable rebalance

- rebalance after node recovery or node addition
- remove static business-group peer configuration

## Explicit v1 Exclusions

- automatic recovery after quorum loss
- dynamic `GroupCount`
- automatic cluster node discovery
- capacity-aware placement using disk, CPU, or traffic models
- multi-group transactional migrations

## Conclusion

The recommended design is:

- fixed `GroupCount`
- fixed `ReplicaN`
- static node membership
- controller-owned desired placement
- agent-executed local actions
- single-step safe membership transitions
- repair before rebalance
- quorum-first semantics with manual recovery after quorum loss

This path preserves the existing routing model and reuses the current
`multiraft` execution layer while adding the missing control-plane placement
authority the project currently lacks.
