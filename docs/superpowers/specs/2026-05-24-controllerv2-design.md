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
  -> build next ClusterState
  -> atomically save cluster-state.json
  -> publish in-memory ClusterState
  -> applied revision becomes externally visible
```

A Controller voter must not expose a newly applied revision until its state file save succeeds. If saving the file fails, the local Controller service enters a degraded state and must not serve that stale file as authoritative leader data.

Apply durability protocol:

```text
decode committed entry
  -> apply deterministic semantic checks
  -> build next ClusterState in memory
  -> set next.AppliedRaftIndex = entry.Index
  -> fsync cluster-state.json through statefile.Store.Save
  -> swap the FSM in-memory snapshot
  -> call pkg/raftlog.MarkApplied(entry.Index)
  -> notify proposal waiter / committed observer
```

`MarkApplied` must never happen before the new state file is durably saved. This ordering deliberately favors replay safety over skipping work. If the state file save succeeds but `MarkApplied` fails, restart may replay the same entry; commands must therefore be deterministic and idempotent where stale retries are expected. If the state file save fails, the in-memory snapshot is not swapped and `MarkApplied` is not called. `AppliedRaftIndex` is file provenance, not the logical `Revision`.

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
    SchemaVersion    uint32
    ClusterID        string
    Revision         uint64
    AppliedRaftIndex uint64
    UpdatedAt        time.Time
    Config           ClusterConfig
    Controllers      []ControllerVoter
    Nodes            []Node
    Slots            []SlotAssignment
    HashSlots        HashSlotTable
    Tasks            []ReconcileTask
    Checksum         string
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
- `AppliedRaftIndex` is the Controller Raft log index whose apply produced this file. Non-controller nodes store it as source provenance only and never treat it as local Raft progress.
- Node IDs must be unique and non-zero.
- Controller voter IDs must refer to existing nodes with `controller_voter` role.
- Slot IDs must be unique and within configured physical slot range.
- Slot peers must be unique active data-capable nodes.
- Hash-slot ranges must fully cover `0..HashSlotCount-1` with no gaps or overlaps when `HashSlotCount > 0`.
- Hash-slot range targets must be within `1..SlotCount`; they do not need an assignment yet while bootstrap tasks are still pending.
- Active task IDs must be unique.
- At most one active reconcile task may exist for a slot in v1.
- Bootstrap tasks must match their slot assignment: `TargetPeers == Assignment.DesiredPeers`, `ConfigEpoch == Assignment.ConfigEpoch`, and `TargetNode == Assignment.PreferredLeader`.

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

CAS behavior:

- `ExpectedRevision == nil`: apply command against the current state after normal semantic validation.
- `ExpectedRevision == current revision`: apply normally.
- `ExpectedRevision != current revision` for bootstrap commands:
  - if the target slot already has an equivalent assignment and no conflicting active task, return an idempotent no-op;
  - if the target slot already has any assignment or active task, return an obsolete no-op so the planner can re-evaluate later;
  - if the target slot is still missing, return a deterministic semantic reject because node/config inputs may have changed.
- `ExpectedRevision != current revision` for non-bootstrap commands: command-specific handlers must either prove idempotence or return a deterministic semantic reject.

Semantic rejects are committed-log outcomes, not local I/O failures. They must be recorded in `ApplyResult` and allowed to advance the applied index.

### `pkg/controllerv2/fsm`

Owns Raft command application and final-state file updates.

Main API:

```go
type StateMachine struct { /* in-memory state + statefile.Store */ }

func NewStateMachine(store *statefile.Store, cfg Config) (*StateMachine, error)
func (sm *StateMachine) Apply(ctx context.Context, raftIndex uint64, cmd command.Command) (ApplyResult, error)
func (sm *StateMachine) Snapshot(ctx context.Context) (state.ClusterState, error)
func (sm *StateMachine) Load(ctx context.Context) error
```

Apply rules:

- Decode and validate command before mutation.
- Copy the current in-memory state.
- Apply a pure mutation to the copy.
- If the command changes logical state, increment `Revision` and update `UpdatedAt`.
- If the command is a deterministic no-op or semantic reject, do not increment `Revision`.
- If `raftIndex > current.AppliedRaftIndex`, set `AppliedRaftIndex=raftIndex`, normalize, validate, and save via `statefile.Store` even when logical state did not change.
- If replay sees `raftIndex <= current.AppliedRaftIndex`, the entry is already reflected in the loaded file and may return an idempotent no-op without rewriting.
- Swap the in-memory snapshot only after the file save succeeds.
- Return an `ApplyResult` that distinguishes `Changed`, `Noop`, and `Rejected`.

No-op examples:

- Bootstrap command for a slot that already has an assignment.
- CompleteTask for a task that is already absent.
- UpsertNode containing identical data.

Invalid-command examples:

- Slot assignment peers do not have active `data` role.
- Desired peer count does not match `ReplicaCount`.
- Task references a different slot than its assignment.
- Hash-slot ranges overlap or leave holes.

Committed entries must not wedge Raft apply because of deterministic business validation. Semantic rejects are represented as `ApplyResult{Rejected:true, Reason:...}` with a nil error so the Raft wrapper can mark the log index applied. Only local non-deterministic failures, such as state-file I/O failure, checksum encoder failure, or context cancellation during durable save, return a non-nil error and put the local service into degraded mode. Obsolete commands should no-op to tolerate leader changes and retries.

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
    LocalChecksum string
}

type GetStateResponse struct {
    NotLeader     bool
    NotReady      bool
    StaleLeader   bool
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
- If response is `NotReady` or `StaleLeader`, keep the local file unchanged and retry later or probe other peers.
- If `NotModified`, keep the local file unchanged. The server may only return `NotModified` when both local revision and local checksum match its current state.
- If payload is present, validate it with `state.Decode`, confirm the decoded revision/checksum match response headers, then save it with `statefile.Store`.
- Publish a local in-memory snapshot after successful save.
- Same-revision checksum repair is the only case where a client may replace a local file without increasing revision.

V1 always uses full payloads when the leader revision is newer. Incremental patch sync is intentionally deferred.

Sync serving rules:

- A node serves state payloads only while it is the current Controller leader and its FSM is not degraded.
- If the node is leader but has not finished applying committed entries to a serviceable state, it returns `NotReady`.
- If `LocalRevision > leaderRevision`, the server returns `StaleLeader` rather than `NotModified`.
- If `LocalRevision == leaderRevision` but `LocalChecksum != leaderChecksum`, the server returns the full payload so the client can repair a forked or damaged mirror.
- A client never installs a payload with a lower revision than its current valid local file.

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

- `cluster-state.json` stores logical cluster state plus `AppliedRaftIndex` provenance for the Raft entry that produced the file.
- The authoritative applied boundary still remains in the Raft log storage layer through `pkg/raftlog.MarkApplied`.
- Commands must be idempotent enough that replaying already reflected entries against a loaded state file results in deterministic no-ops.
- Logical `Revision` changes only when logical cluster state changes. It is not the Raft log index and may remain stable while `AppliedRaftIndex` advances through no-op or rejected entries.
- The Raft wrapper calls `MarkApplied` only after `fsm.Apply` returns a nil error for the committed entry. `ApplyResult.Rejected` and `ApplyResult.Noop` still allow `MarkApplied`.
- V1 does not enable ControllerV2 log compaction. If compaction is added later, the Raft snapshot payload must contain a full state snapshot sufficient to rebuild `cluster-state.json`.

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
  "applied_raft_index": 128,
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
      { "from": 0, "to": 1023, "slot_id": 1 },
      { "from": 1024, "to": 2047, "slot_id": 2 },
      { "from": 2048, "to": 3071, "slot_id": 3 },
      { "from": 3072, "to": 4095, "slot_id": 4 },
      { "from": 4096, "to": 5119, "slot_id": 5 },
      { "from": 5120, "to": 6143, "slot_id": 6 },
      { "from": 6144, "to": 7167, "slot_id": 7 },
      { "from": 7168, "to": 8191, "slot_id": 8 },
      { "from": 8192, "to": 9215, "slot_id": 9 },
      { "from": 9216, "to": 10239, "slot_id": 10 },
      { "from": 10240, "to": 11263, "slot_id": 11 },
      { "from": 11264, "to": 12287, "slot_id": 12 },
      { "from": 12288, "to": 13311, "slot_id": 13 },
      { "from": 13312, "to": 14335, "slot_id": 14 },
      { "from": 14336, "to": 15359, "slot_id": 15 },
      { "from": 15360, "to": 16383, "slot_id": 16 }
    ]
  },
  "tasks": [
    {
      "task_id": "slot-1-bootstrap-1",
      "slot_id": 1,
      "kind": "bootstrap",
      "step": "create_slot",
      "target_node": 1,
      "target_peers": [1, 2, 3],
      "config_epoch": 1,
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
- Checksum is calculated over canonical bytes with the `checksum` field omitted.

`InitClusterState` creates the initial complete hash-slot table. V1 uses deterministic even distribution across physical slot IDs `1..SlotCount`. This table is desired routing metadata. During initial bootstrap it may point at physical slots whose assignments are not created yet; future production integration must treat those slots as not ready until assignment exists and the bootstrap task is complete.

## Bootstrap Planner Detailed Flow

The bootstrap planner creates initial slot assignments for missing physical slots. It does not execute data-plane actions and does not directly write the state file.

It does not create the hash-slot routing table. `InitClusterState` creates a complete deterministic hash-slot table first, and the bootstrap planner only fills in the physical slot assignments and bootstrap tasks needed to make those routes usable.

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
node A is less loaded than node B when:
  replica_load(A) * capacity_weight(B) < replica_load(B) * capacity_weight(A)

if normalized load is equal:
  tie = (slot_id + node_id) mod candidate_count, then node_id
```

The implementation uses integer cross-multiplication and a stable slot-based tie-breaker to avoid floating-point nondeterminism and permanent bias toward smaller node IDs.

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
        TaskID:      "slot-4-bootstrap-1",
        SlotID:      4,
        Kind:        state.TaskKindBootstrap,
        Step:        state.TaskStepCreateSlot,
        TargetNode:  3,
        TargetPeers: []uint64{1, 3, 5},
        ConfigEpoch: 1,
        Status:      state.TaskStatusPending,
    },
}
```

The assignment and task are committed atomically in one Raft command. This prevents a state file that has an assignment without a task or a task without the desired assignment.

`TargetNode` is the coordinator and preferred bootstrap target. The executor must read `TargetPeers` and `ConfigEpoch` from the task and verify they still match the slot assignment before acting. This avoids task meaning drifting if a future command changes the assignment.

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
4. Compare `state.AppliedRaftIndex` with the raftlog applied boundary.
5. Replay committed log entries after the smaller safe boundary when needed.
6. Serve state only after FSM and Raft status are coherent.

Recovery matrix:

- `state.AppliedRaftIndex == raftlog.AppliedIndex`: normal startup; replay entries after that index.
- `state.AppliedRaftIndex > raftlog.AppliedIndex`: state-file save succeeded but `MarkApplied` did not. Replay from `raftlog.AppliedIndex+1`; entries already reflected by the state file must no-op until raftlog catches up.
- `state.AppliedRaftIndex < raftlog.AppliedIndex`: this violates the normal save-before-mark protocol. V1 attempts a deterministic rebuild by replaying entries from `state.AppliedRaftIndex+1` through `raftlog.AppliedIndex` only if those entries are still available. If required entries are unavailable, startup fails degraded and requires operator repair.
- state file missing or corrupt: V1 rebuilds from the first available Raft log entry only when the log still contains a complete history from `InitClusterState`. If the log has been compacted and no ControllerV2 snapshot payload exists, startup fails degraded.

The state file is the final logical state after apply, while `pkg/raftlog` remains responsible for the authoritative node-local applied-index boundary. Non-controller sync stores `AppliedRaftIndex` as source provenance only; it never uses it as local Raft progress.

### State File Corruption

- Controller voter: refuse to serve corrupt state until it is rebuilt from complete Raft log history or a future ControllerV2 snapshot.
- Non-controller: reject corrupt local state, keep last in-memory snapshot if available, and retry full sync from leader.
- Tests must cover checksum mismatch and invalid schema.

### File Save Failure During Apply

- Do not swap in-memory state to the new revision.
- Return an apply error.
- Mark local Controller service degraded.
- Avoid serving stale state as the current leader state.

### MarkApplied Failure After Save

`MarkApplied` failure happens after `cluster-state.json` has already been durably saved and the in-memory snapshot has been swapped. The service cannot roll this back.

- Return an apply-boundary error to the proposal waiter instead of reporting full success.
- Mark the local Controller service degraded for authoritative serving and sync payloads.
- Stop applying later committed entries until the applied boundary is repaired or the node restarts.
- Keep the durable state file in place; restart follows the `state.AppliedRaftIndex > raftlog.AppliedIndex` recovery path and replays reflected entries idempotently.
- A future implementation may add a bounded background retry for `MarkApplied`, but V1 can fail fast and rely on restart recovery.

### Leader Change During Planning

Planner decisions are proposals, not direct writes. If leadership changes between planning and commit, stale commands are handled by FSM no-op checks and optional `ExpectedRevision`.

### Non-Controller Sync Failure

- Keep the last valid local file.
- Continue retrying peers.
- Expose stale revision information for future metrics.
- Do not install a payload with wrong cluster ID, unsupported schema, lower revision, or invalid checksum.
- If payload revision equals the local revision, install it only when checksum differs, it came from the current ready leader, and validation succeeds.
- If payload revision is higher than the local revision, install it after normal validation.

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
- `InitClusterState` creates a complete deterministic hash-slot table.
- `UpsertNode` changes state and increments revision.
- Identical `UpsertNode` is a no-op.
- `UpsertSlotAssignmentAndTask` writes assignment and task atomically.
- Stale bootstrap command for an already assigned slot no-ops.
- Invalid assignment peers return a semantic reject, advance `AppliedRaftIndex`, and do not change logical `Revision`.
- `CompleteTask` removes the active task and increments revision.
- A no-op committed entry advances `AppliedRaftIndex` when it is newer than the loaded file.
- Save failure does not swap in-memory state and does not allow `MarkApplied`.

### Unit Tests: `planner`

- Bootstrap planner returns blocked when eligible data nodes are fewer than replica count.
- Bootstrap planner picks the lowest missing slot.
- Bootstrap planner skips slots with active tasks.
- Peer selection spreads replicas across equal-weight nodes.
- Capacity weight influences peer selection deterministically.
- Preferred leader spreads across selected peers.
- Generated command includes expected revision, assignment, and task.

### Unit Tests: `sync`

- Same revision and same checksum returns `NotModified`.
- Same revision with different checksum returns full payload.
- Newer leader state is validated and saved.
- Lower leader revision returns `StaleLeader` and does not replace the local file.
- Wrong cluster ID is rejected.
- Bad checksum is rejected.
- Not-leader response causes retry to advertised leader.
- `NotReady` response preserves the local file and retries later.

### Small Integration Tests

- Three Controller voter services commit one command and eventually have identical `cluster-state.json` revision and checksum.
- Three Controller voters plus two non-controller sync clients converge to the same revision and checksum.
- Bootstrap planner on the Controller leader creates assignments and tasks for all configured slots over repeated ticks.
- Restart after state-file save succeeds but `MarkApplied` fails replays the entry idempotently and converges.

## Implementation Phasing

### Phase 1: Local State Core

- Add `state` models, validation, canonical JSON, and checksum.
- Add `statefile` atomic save/load.
- Add FSM apply tests without real Raft.
- Add English comments for exported types, key struct fields, and all configuration fields.
- Keep docs and test names aligned with the project deployment language: use `single-node cluster` instead of standalone/bypass-cluster wording.

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

- `pkg/raftlog` persists the authoritative node-local applied boundary.
- `cluster-state.json` also persists `AppliedRaftIndex` as state-file provenance and replay hint; non-controller nodes never use it as local Raft progress.
- Checksum is calculated over canonical JSON with the `checksum` field omitted.
- Sync tests start with in-process client/server interfaces before binding to the existing RPC mux.
- Completed tasks are deleted from the active `tasks` list. Bounded task history is deferred.
