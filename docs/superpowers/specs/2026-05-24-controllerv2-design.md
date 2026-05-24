# ControllerV2 File-Backed Raft Control Plane Design

**Date:** 2026-05-24
**Status:** Design approved by user, pending implementation plan

## Overview

Add a new parallel controller implementation under `pkg/controllerv2`.

The first version does not replace the existing `pkg/controller` and does not attach to the current `pkg/cluster` startup path. It builds a clean control-plane core where Controller voter nodes use Raft to maintain one final `cluster-state.json` file. Every state-changing command enters the Controller Raft log first. When a command is committed, the state machine applies it and atomically rewrites the JSON state file. That file is the final durable control-plane state for that node.

In a five-node cluster, for example, three nodes may be Controller voters and form the Controller Raft group. The remaining non-controller nodes do not join Raft. They periodically or reactively pull the full `cluster-state.json` from the current Controller leader, validate it, and atomically replace their local copy. Controller nodes provide strong consistency through Raft. Non-controller nodes provide eventually consistent local mirrors of the final state file.

## Goals

- Create `pkg/controllerv2` as a parallel implementation that can coexist with `pkg/controller`.
- Make the committed Controller Raft log the only write-order source for cluster state changes.
- Make `cluster-state.json` the final durable state after each successful Raft apply.
- Store desired cluster state and recoverable reconcile tasks in the state file:
  - cluster identity and schema version
  - Controller voter membership
  - node membership and durable health status
  - physical slot assignments
  - hash-slot routing table
  - active reconcile tasks
- Exclude high-frequency runtime observations from the state file.
- Use canonical JSON with versioning, deterministic ordering, and checksum validation.
- Provide full-file synchronization for non-controller nodes.
- Reserve a clear planner boundary and implement a minimal bootstrap planner for missing slot assignments.
- Keep architecture modular enough to add repair, rebalance, leader placement, onboarding, and placement policies later.

## Non-Goals

- Replacing existing `pkg/controller` in this phase.
- Wiring `pkg/controllerv2` into `pkg/cluster`, `internal/app`, or the production binary startup path.
- Implementing repair, rebalance, leader transfer, or node onboarding planners.
- Implementing incremental state-file sync or non-voter Raft followers.
- Storing raw heartbeat reports, runtime slot leaders, runtime quorum views, or planner scoring internals in `cluster-state.json`.
- Building a long-lived audit log in the state file. Active tasks are stored; completed task history is deferred.

## Core Semantics

### Controller Voter Nodes

Controller voter nodes run a dedicated Controller Raft group. A state-changing operation follows this path:

```text
API / control loop
  -> Raft Propose(Command)
  -> Raft Commit
  -> FSM Apply(Command)
  -> update in-memory ClusterState
  -> atomically save cluster-state.json
  -> applied revision becomes externally visible
```

A Controller voter must not expose a newly applied revision until its state file save succeeds. If saving the file fails, the local Controller service enters a degraded state and must not serve that stale file as authoritative leader data.

### Non-Controller Nodes

Non-controller nodes do not receive Controller Raft log entries. Their local state follows this path:

```text
load local cluster-state.json if present
  -> ask Controller leader for current state
  -> if leader revision is newer, pull full JSON payload
  -> validate schema / cluster_id / revision / checksum
  -> atomically replace local cluster-state.json
  -> publish local in-memory snapshot to upper layers
```

This gives non-controller nodes eventual consistency. They may be temporarily stale during leader changes or network failures, but they never intentionally install a partial or corrupt state file.

## Package Design

### `pkg/controllerv2/state`

Owns the durable model and canonical JSON encoding.

Primary types:

```go
type ClusterState struct {
    SchemaVersion uint32
    ClusterID     string
    Revision      uint64
    UpdatedAt     time.Time
    Config        ClusterConfig
    Controllers   []ControllerVoter
    Nodes         []Node
    Slots         []SlotAssignment
    HashSlots     HashSlotTable
    Tasks         []ReconcileTask
    Checksum      string
}
```

Supporting types:

- `ClusterConfig`: desired physical slot count, hash-slot count, replica count, default capacity, and future policy placeholders.
- `ControllerVoter`: Controller Raft voter node ID and RPC address.
- `Node`: durable cluster membership record.
- `NodeRole`: represented as a capability set, not a single enum. A node can have both `controller_voter` and `data` roles.
- `SlotAssignment`: desired physical slot peers, config epoch, and preferred leader.
- `HashSlotTable`: versioned hash-slot-to-physical-slot routing ranges.
- `ReconcileTask`: active task needed to converge data-plane state to desired assignment.

Responsibilities:

- Validate cluster invariants.
- Normalize default values.
- Sort every repeated collection deterministically.
- Encode canonical JSON.
- Decode and validate state files.
- Calculate and verify checksum over canonical JSON excluding the checksum field itself.
- Provide copy/snapshot helpers so planner and FSM can avoid sharing mutable state.

Important invariants:

- `SchemaVersion` must be supported.
- `ClusterID` must be non-empty and stable.
- `Revision` is monotonically increasing on successful state changes.
- Node IDs must be unique and non-zero.
- Controller voter IDs must refer to existing nodes with `controller_voter` role.
- Slot IDs must be unique and within configured physical slot range.
- Slot peers must be unique active data-capable nodes.
- Active task IDs must be unique.
- At most one active reconcile task may exist for a slot in v1.

### `pkg/controllerv2/statefile`

Owns safe file persistence.

Main API:

```go
type Store struct { /* path, fsync policy, logger */ }

func (s *Store) Load(ctx context.Context) (state.ClusterState, error)
func (s *Store) Save(ctx context.Context, st state.ClusterState) error
func (s *Store) Path() string
```

Save algorithm:

```text
normalize and canonical encode state
  -> calculate checksum and encode final canonical JSON
  -> write to cluster-state.json.tmp.<pid>.<counter>
  -> fsync temp file
  -> rename temp file to cluster-state.json
  -> fsync parent directory
```

Load algorithm:

```text
read cluster-state.json
  -> decode JSON
  -> verify schema version
  -> verify checksum
  -> validate invariants
  -> return normalized ClusterState
```

Failure handling:

- A leftover temp file is ignored by `Load`.
- A checksum mismatch returns a typed corrupt-state error.
- `Save` must not remove the last valid state file until the new file has been fully written and renamed.
- Unit tests should use real temp directories so rename behavior is exercised.

### `pkg/controllerv2/command`

Defines the durable Raft command boundary. The command format is independent from the old `pkg/controller/plane` package.

Initial command kinds:

- `InitClusterState`: create the first state file from bootstrap config.
- `UpsertNode`: add or update durable node membership.
- `UpdateControllerVoters`: update Controller voter membership metadata. Actual dynamic Raft membership changes can be deferred, but the state model must represent the intended voters.
- `UpsertSlotAssignmentAndTask`: atomically write desired slot assignment plus its converge task.
- `CompleteTask`: remove or complete an active task after data-plane convergence.
- `FailTask`: mark a task failed with bounded error text.
- `ReplaceHashSlotTable`: replace the hash-slot routing table.

Command envelope fields:

```go
type Command struct {
    Kind             Kind
    ExpectedRevision *uint64
    IssuedAt         time.Time
    Node             *state.Node
    Controllers      []state.ControllerVoter
    Assignment       *state.SlotAssignment
    Task             *state.ReconcileTask
    TaskResult       *TaskResult
    HashSlots        *state.HashSlotTable
}
```

`ExpectedRevision` is optional but recommended for planner-generated commands. Stale planner commands that become obsolete should no-op deterministically when possible.

### `pkg/controllerv2/fsm`

Owns Raft command application and final-state file updates.

Main API:

```go
type StateMachine struct { /* in-memory state + statefile.Store */ }

func NewStateMachine(store *statefile.Store, cfg Config) (*StateMachine, error)
func (sm *StateMachine) Apply(ctx context.Context, cmd command.Command) (ApplyResult, error)
func (sm *StateMachine) Snapshot(ctx context.Context) (state.ClusterState, error)
func (sm *StateMachine) Load(ctx context.Context) error
```

Apply rules:

- Decode and validate command before mutation.
- Copy the current in-memory state.
- Apply a pure mutation to the copy.
- If the command is a deterministic no-op, do not increment revision and do not rewrite the file.
- If state changed, increment `Revision`, update `UpdatedAt`, normalize, validate, and save via `statefile.Store`.
- Swap the in-memory snapshot only after the file save succeeds.

No-op examples:

- Bootstrap command for a slot that already has an assignment.
- CompleteTask for a task that is already absent.
- UpsertNode containing identical data.

Invalid-command examples:

- Slot assignment peers do not have active `data` role.
- Desired peer count does not match `ReplicaCount`.
- Task references a different slot than its assignment.
- Hash-slot ranges overlap or leave holes.

Invalid commands return errors and should be visible in tests and metrics. Obsolete commands should no-op to tolerate leader changes and retries.

### `pkg/controllerv2/planner`

Owns pure planning. It must not write files, call Raft, or perform RPC.

Main API:

```go
type Planner interface {
    Next(ctx context.Context, view View) (Decision, error)
}

type View struct {
    State        state.ClusterState
    Observations RuntimeObservations
    Constraints  Constraints
    Now          time.Time
}

type Decision struct {
    Kind    DecisionKind
    Reason  string
    Command command.Command
}
```

The first implementation provides `BootstrapPlanner` only. Later planners can be composed by priority:

```text
CompositePlanner.Next(view):
  1. BootstrapPlanner
  2. RepairPlanner
  3. RebalancePlanner
  4. LeaderPlanner
  5. OnboardingPlanner
```

Reserved extension points:

- `RuntimeObservations`: future leader-local observations of slot leaders, voters, quorum, and config epoch.
- `PlacementPolicy`: future rack/zone/region anti-affinity and isolation rules.
- `SafetyChecker`: future quorum, epoch, task-conflict, and migration-lock checks.
- `RateLimiter`: future max decisions per tick or per node.
- `Scorer`: future weighted capacity and leader-load scoring.

### `pkg/controllerv2/sync`

Owns full-file synchronization for non-controller nodes.

Server API shape:

```go
type GetStateRequest struct {
    ClusterID     string
    LocalRevision uint64
}

type GetStateResponse struct {
    NotLeader     bool
    LeaderID      uint64
    NotModified   bool
    Revision      uint64
    Checksum      string
    Payload       []byte
}
```

Client behavior:

- Load local state if present.
- Ask known Controller leader first; otherwise probe Controller peers.
- If response is `NotLeader`, retry the advertised leader when present.
- If `NotModified`, keep the local file unchanged.
- If payload is present, validate it with `state.Decode`, then save it with `statefile.Store`.
- Publish a local in-memory snapshot after successful save.

V1 always uses full payloads when the leader revision is newer. Incremental patch sync is intentionally deferred.

### `pkg/controllerv2/raft`

Owns the Controller Raft service wrapper.

Responsibilities:

- Start a Controller Raft group for voter nodes.
- Encode/decode `command.Command` entries.
- Apply committed commands through `fsm.StateMachine`.
- Expose leader ID and local status.
- Support small in-memory integration tests for three Controller voters.

Implementation can reuse existing lower-level infrastructure where appropriate:

- `pkg/raftlog` for persistent Raft log storage.
- Existing etcd raft usage patterns from `pkg/controller/raft` and `pkg/slot/multiraft`.
- Existing transport primitives only if doing so does not couple v2 to old Controller command types.

V1 does not need dynamic Raft membership changes. `UpdateControllerVoters` updates desired metadata in `cluster-state.json`; actual Raft ConfChange support can be added later.

Raft apply metadata:

- `cluster-state.json` stores logical cluster state, not node-local Raft bookkeeping.
- The applied Raft index remains in the Raft log storage layer through `pkg/raftlog.MarkApplied`.
- Commands must be idempotent enough that replaying already reflected entries against a loaded state file results in deterministic no-ops.
- Logical `Revision` changes only when logical cluster state changes. It is not the Raft log index.

### `pkg/controllerv2/server`

Provides the v2 facade used by tests and future `pkg/cluster` integration.

Possible API:

```go
type Server struct { /* raft, fsm, planner, sync */ }

func (s *Server) Start(ctx context.Context) error
func (s *Server) Stop() error
func (s *Server) Propose(ctx context.Context, cmd command.Command) error
func (s *Server) TickPlanner(ctx context.Context) error
func (s *Server) LocalState() state.ClusterState
func (s *Server) SyncOnce(ctx context.Context) error
```

The facade is intentionally thin. It wires modules together but should not contain planner policy, state mutation logic, or file encoding details.

## State File Schema

The authoritative file is JSON. Example:

```json
{
  "schema_version": 1,
  "cluster_id": "wk-cluster",
  "revision": 42,
  "updated_at": "2026-05-24T10:00:00Z",
  "config": {
    "slot_count": 16,
    "hash_slot_count": 16384,
    "replica_count": 3
  },
  "controllers": [
    { "node_id": 1, "addr": "10.0.0.1:11110", "role": "voter" }
  ],
  "nodes": [
    {
      "node_id": 1,
      "name": "node-1",
      "addr": "10.0.0.1:11110",
      "roles": ["controller_voter", "data"],
      "join_state": "active",
      "status": "alive",
      "capacity_weight": 100
    }
  ],
  "slots": [
    {
      "slot_id": 1,
      "desired_peers": [1, 2, 3],
      "config_epoch": 1,
      "preferred_leader": 1
    }
  ],
  "hash_slots": {
    "version": 1,
    "slot_count": 16384,
    "ranges": [
      { "from": 0, "to": 1023, "slot_id": 1 }
    ]
  },
  "tasks": [
    {
      "task_id": "slot-1-bootstrap-1",
      "slot_id": 1,
      "kind": "bootstrap",
      "step": "create_slot",
      "target_node": 1,
      "attempt": 0,
      "status": "pending"
    }
  ],
  "checksum": "crc32c:00000000"
}
```

Canonical rules:

- JSON object fields use fixed struct field order from Go encoding.
- `controllers` sorted by `node_id`.
- `nodes` sorted by `node_id`.
- `roles` sorted lexicographically.
- `desired_peers` sorted ascending before persistence.
- `slots` sorted by `slot_id`.
- `hash_slots.ranges` sorted by `from`.
- `tasks` sorted by `slot_id`, then `task_id`.
- Timestamps use UTC RFC3339Nano.
- Checksum is calculated over canonical bytes with the checksum field empty or omitted; the implementation must choose one rule and test it explicitly.

## Bootstrap Planner Detailed Flow

The bootstrap planner creates initial slot assignments for missing physical slots. It does not execute data-plane actions and does not directly write the state file.

### Inputs

The planner reads a `planner.View` containing:

- `ClusterState.Config.SlotCount`
- `ClusterState.Config.ReplicaCount`
- active nodes with `data` role
- existing slot assignments
- existing active tasks
- current revision
- current time

Eligible data node rule:

```text
roles contains data
join_state == active
status == alive
capacity_weight > 0
```

If eligible data nodes are fewer than `ReplicaCount`, the planner returns a blocked decision and no command.

### Slot Selection

The planner scans expected physical slots `1..SlotCount` and picks the lowest slot ID that has no assignment and no active task.

This is deterministic and simple to reason about:

```text
expected slots: 1..16
existing assignments: 1, 2, 3
next bootstrap slot: 4
```

V1 generates at most one bootstrap command per planner tick. Batch bootstrap can be added later with a `MaxBootstrapSlotsPerTick` setting.

### Peer Selection

The planner chooses `ReplicaCount` peers using deterministic weighted load balancing:

1. Count existing slot replica load per eligible data node.
2. Sort candidates by normalized load, where lower is better.
3. Use `capacity_weight` so higher-capacity nodes can carry more replicas.
4. Use a stable slot-based tie-breaker to avoid always favoring smaller node IDs.
5. Sort final `desired_peers` ascending before writing the command.

A simple v1 scoring function is sufficient:

```text
score = replica_load / capacity_weight
 tie = stable(slot_id, node_id)
```

The exact integer implementation should avoid floating-point nondeterminism.

### Preferred Leader Selection

The planner assigns `preferred_leader` from the selected peers.

V1 rule:

- Count current preferred-leader load from existing assignments.
- Pick the selected peer with the lowest preferred-leader load.
- Tie-break deterministically by stable slot-based order, then node ID.

The preferred leader is a desired bootstrap target, not an observed runtime leader.

### Generated Command

The planner returns an `UpsertSlotAssignmentAndTask` command:

```go
command.Command{
    Kind: command.KindUpsertSlotAssignmentAndTask,
    ExpectedRevision: &currentRevision,
    Assignment: &state.SlotAssignment{
        SlotID:          4,
        DesiredPeers:    []uint64{1, 3, 5},
        ConfigEpoch:     1,
        PreferredLeader: 3,
    },
    Task: &state.ReconcileTask{
        TaskID:     "slot-4-bootstrap-1",
        SlotID:     4,
        Kind:       state.TaskKindBootstrap,
        Step:       state.TaskStepCreateSlot,
        TargetNode: 3,
        Status:     state.TaskStatusPending,
    },
}
```

The assignment and task are committed atomically in one Raft command. This prevents a state file that has an assignment without a task or a task without the desired assignment.

### Duplicate Protection

Duplicate bootstrap protection exists in three layers:

- Planner: skip slots that already have an assignment or active task.
- Command: include `ExpectedRevision` for planner-generated decisions.
- FSM: re-check slot assignment, active task, peer eligibility, peer count, and task/assignment consistency before mutating state.

If a command becomes stale because another leader already bootstrapped the slot, the FSM should no-op without increasing revision.

## Future Planner Expansion

The package boundary is designed so later planners do not disturb the state file, Raft wrapper, or sync protocol.

Future planners:

- `RepairPlanner`: creates repair tasks when desired peers include dead, draining, or missing nodes.
- `RebalancePlanner`: moves replicas based on capacity and skew thresholds.
- `LeaderPlanner`: creates leader transfer tasks based on preferred leader skew and runtime observations.
- `OnboardingPlanner`: creates reviewed node onboarding plans and stepwise migration tasks.
- `PlacementPlanner`: enforces rack/zone/region anti-affinity.

Future observation package:

```text
pkg/controllerv2/observation
  -> leader-local runtime view cache
  -> node heartbeat aggregation
  -> observation TTL and freshness checks
  -> immutable snapshots for planner.View
```

Observation data stays out of `cluster-state.json` unless it becomes a low-frequency durable decision such as node `status=dead`.

## Failure Handling

### Controller Voter Restart

Startup sequence:

1. Load `cluster-state.json` if it exists and is valid.
2. Start Controller Raft storage.
3. Read the applied boundary from Raft log storage.
4. Replay committed log entries after the applied boundary.
5. Serve state only after FSM and Raft status are coherent.

The state file is the final logical state after apply, while `pkg/raftlog` remains responsible for the node-local applied-index boundary. This keeps non-controller full sync clean: they receive logical state without pretending to own Controller Raft progress.

### State File Corruption

- Controller voter: refuse to serve corrupt state; recover by replaying Raft log where possible.
- Non-controller: reject corrupt local state, keep last in-memory snapshot if available, and retry full sync from leader.
- Tests must cover checksum mismatch and invalid schema.

### File Save Failure During Apply

- Do not swap in-memory state to the new revision.
- Return an apply error.
- Mark local Controller service degraded.
- Avoid serving stale state as the current leader state.

### Leader Change During Planning

Planner decisions are proposals, not direct writes. If leadership changes between planning and commit, stale commands are handled by FSM no-op checks and optional `ExpectedRevision`.

### Non-Controller Sync Failure

- Keep the last valid local file.
- Continue retrying peers.
- Expose stale revision information for future metrics.
- Do not install a payload with wrong cluster ID, unsupported schema, non-increasing revision, or invalid checksum.

## Testing Strategy

### Unit Tests: `state`

- Canonical encoding is stable across unordered inputs.
- Checksum validates and detects mutation.
- Duplicate node IDs are rejected.
- Controller voters must map to controller-capable nodes.
- Slot peers must be unique active data nodes.
- Hash-slot ranges reject overlaps, gaps, and out-of-range values.

### Unit Tests: `statefile`

- Save and Load round trip.
- Save uses atomic replacement and preserves old valid file if temp write fails.
- Leftover temp files are ignored.
- Checksum mismatch returns a typed corruption error.
- Unsupported schema version is rejected.

### Unit Tests: `fsm`

- `InitClusterState` creates revision 1 state.
- `UpsertNode` changes state and increments revision.
- Identical `UpsertNode` is a no-op.
- `UpsertSlotAssignmentAndTask` writes assignment and task atomically.
- Stale bootstrap command for an already assigned slot no-ops.
- Invalid assignment peers return error and do not rewrite state file.
- `CompleteTask` removes the active task and increments revision.

### Unit Tests: `planner`

- Bootstrap planner returns blocked when eligible data nodes are fewer than replica count.
- Bootstrap planner picks the lowest missing slot.
- Bootstrap planner skips slots with active tasks.
- Peer selection spreads replicas across equal-weight nodes.
- Capacity weight influences peer selection deterministically.
- Preferred leader spreads across selected peers.
- Generated command includes expected revision, assignment, and task.

### Unit Tests: `sync`

- Same revision returns `NotModified`.
- Newer leader state is validated and saved.
- Wrong cluster ID is rejected.
- Bad checksum is rejected.
- Not-leader response causes retry to advertised leader.

### Small Integration Tests

- Three Controller voter services commit one command and eventually have identical `cluster-state.json` revision and checksum.
- Three Controller voters plus two non-controller sync clients converge to the same revision and checksum.
- Bootstrap planner on the Controller leader creates assignments and tasks for all configured slots over repeated ticks.

## Implementation Phasing

### Phase 1: Local State Core

- Add `state` models, validation, canonical JSON, and checksum.
- Add `statefile` atomic save/load.
- Add FSM apply tests without real Raft.

### Phase 2: Commands and Bootstrap Planner

- Add command envelope and mutation handlers.
- Add planner interface and bootstrap planner.
- Test planner-to-FSM command flow locally.

### Phase 3: Sync Protocol

- Add in-process sync server/client abstractions.
- Add full-file sync tests.
- Keep transport binding thin so later `pkg/cluster` integration can adapt it.

### Phase 4: Raft Wrapper Skeleton

- Add Controller Raft service around command apply.
- Add three-voter in-memory or temp-dir integration tests.
- Do not expose it as the production controller yet.

### Phase 5: Documentation

- Add `pkg/controllerv2/FLOW.md` when package code is created.
- Update `AGENTS.md` directory structure only when `pkg/controllerv2` exists in the tree.
- Record any broadly useful project rules in `docs/development/PROJECT_KNOWLEDGE.md` only if discovered during implementation.

## Implementation Decisions for V1

- Raft applied index is persisted by `pkg/raftlog`, not inside `cluster-state.json`.
- Checksum is calculated over canonical JSON with the `checksum` field omitted.
- Sync tests start with in-process client/server interfaces before binding to the existing RPC mux.
- Completed tasks are deleted from the active `tasks` list. Bounded task history is deferred.
