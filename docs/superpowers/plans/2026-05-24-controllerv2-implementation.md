# ControllerV2 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `pkg/controllerv2` as a parallel file-backed Controller Raft control plane with canonical `cluster-state.json`, full-file follower sync, and a minimal bootstrap planner.

**Architecture:** The implementation is split into focused packages: `state` owns durable JSON models and validation, `statefile` owns atomic persistence, `command` defines committed Raft operations, `fsm` applies commands and saves the final state file, `planner` produces bootstrap commands, `sync` mirrors full state files to non-controller nodes, `raft` wraps etcd/raft using `pkg/raftlog`, and `server` wires the modules without becoming a service layer. Controller voter nodes apply committed Raft entries with save-before-`MarkApplied` ordering; non-controller nodes install only validated leader payloads.

**Tech Stack:** Go 1.23, `go.etcd.io/raft/v3`, existing `pkg/raftlog`, standard library JSON/CRC/file APIs, `github.com/stretchr/testify` for tests.

---

## Reference Material

- Spec: `docs/superpowers/specs/2026-05-24-controllerv2-design.md`
- Existing controller flow: `pkg/controller/FLOW.md`
- Existing cluster flow: `pkg/cluster/FLOW.md`
- Existing Raft wrapper reference: `pkg/controller/raft/service.go`
- Existing raft log API: `pkg/raftlog/pebble_db.go`, `pkg/slot/multiraft/types.go`
- Project rules: `AGENTS.md`

Use @superpowers:test-driven-development for every code task. Use @superpowers:verification-before-completion before claiming completion.

## File Structure

Create these files:

- `pkg/controllerv2/FLOW.md` — package flow and boundaries for future readers.
- `pkg/controllerv2/state/types.go` — durable state structs, enums, English field comments.
- `pkg/controllerv2/state/errors.go` — typed errors for invalid/corrupt state.
- `pkg/controllerv2/state/normalize.go` — deterministic sorting and default normalization.
- `pkg/controllerv2/state/validate.go` — cluster-state invariant checks.
- `pkg/controllerv2/state/codec.go` — canonical JSON encode/decode and checksum helpers.
- `pkg/controllerv2/state/hashslots.go` — deterministic initial hash-slot table builder.
- `pkg/controllerv2/state/state_test.go` — model, validation, canonical JSON, checksum tests.
- `pkg/controllerv2/statefile/store.go` — atomic `cluster-state.json` save/load.
- `pkg/controllerv2/statefile/store_test.go` — round-trip, checksum, temp-file, replace tests.
- `pkg/controllerv2/command/command.go` — Raft command envelope and command kinds.
- `pkg/controllerv2/command/codec.go` — versioned JSON command codec for Raft entries.
- `pkg/controllerv2/command/codec_test.go` — command codec tests.
- `pkg/controllerv2/fsm/fsm.go` — command apply, save-before-publish semantics, degraded flag.
- `pkg/controllerv2/fsm/mutations.go` — mutation handlers and semantic reject/no-op helpers.
- `pkg/controllerv2/fsm/fsm_test.go` — FSM TDD tests.
- `pkg/controllerv2/planner/planner.go` — planner interfaces and decision types.
- `pkg/controllerv2/planner/bootstrap.go` — minimal bootstrap planner.
- `pkg/controllerv2/planner/bootstrap_test.go` — bootstrap planner tests.
- `pkg/controllerv2/sync/sync.go` — full-file sync request/response, server, client.
- `pkg/controllerv2/sync/sync_test.go` — sync tests.
- `pkg/controllerv2/raft/config.go` — Raft service config, peer, transport interfaces.
- `pkg/controllerv2/raft/service.go` — etcd/raft service run loop, propose, step, apply.
- `pkg/controllerv2/raft/status.go` — status/degraded structs.
- `pkg/controllerv2/raft/service_test.go` — in-memory three-voter tests.
- `pkg/controllerv2/server/server.go` — thin facade for future integration.
- `pkg/controllerv2/server/server_test.go` — facade wiring tests.

Modify these files:

- `AGENTS.md` — add `pkg/controllerv2` to directory structure after the package exists.

Do not modify production wiring in `pkg/cluster`, `internal/app`, or `cmd/wukongim` in this plan.

---

### Task 1: Scaffold ControllerV2 Package And Flow Doc

**Files:**
- Create: `pkg/controllerv2/FLOW.md`
- Create: package directories under `pkg/controllerv2/`
- Modify: `AGENTS.md`

- [ ] **Step 1: Create package directories**

Run:

```bash
mkdir -p pkg/controllerv2/{state,statefile,command,fsm,planner,sync,raft,server}
```

Expected: directories exist and contain no Go files yet.

- [ ] **Step 2: Write `pkg/controllerv2/FLOW.md`**

Create `pkg/controllerv2/FLOW.md` with this content:

```markdown
# pkg/controllerv2 Flow

## Responsibility

`pkg/controllerv2` is a parallel Controller implementation. Controller voter nodes apply committed Controller Raft commands to a canonical `cluster-state.json` file. Non-controller nodes do not join Controller Raft; they mirror the leader state file through full-file sync.

## Package Boundaries

| Package | Responsibility |
|---------|----------------|
| `state` | Durable JSON model, normalization, validation, checksum, initial hash-slot table. |
| `statefile` | Atomic load/save for `cluster-state.json`. |
| `command` | Versioned Raft command envelope. |
| `fsm` | Applies committed commands, persists final state, publishes snapshots after durable save. |
| `planner` | Pure planning. V1 only creates bootstrap assignment/task commands. |
| `sync` | Full-file leader sync for non-controller nodes. |
| `raft` | Controller Raft wrapper and apply loop. |
| `server` | Thin composition facade for tests and future integration. |

## Apply Order

```text
Raft commit -> semantic apply -> save cluster-state.json -> publish in-memory state -> MarkApplied
```

`Revision` is the logical cluster-state version. `AppliedRaftIndex` records which Raft entry produced the file. `pkg/raftlog` remains the authoritative local applied boundary.

## Non-Goals

- Do not replace `pkg/controller` in this package.
- Do not wire production cluster startup to v2 yet.
- Do not store high-frequency runtime observations in `cluster-state.json`.
```

- [ ] **Step 3: Update `AGENTS.md` directory structure**

Add this entry under `pkg/`:

```text
  controllerv2/            并行新版控制面：Raft apply 维护最终 cluster-state.json，含 state/statefile/command/fsm/planner/sync/raft/server
```

- [ ] **Step 4: Verify docs-only scaffold**

Run:

```bash
git diff --check -- pkg/controllerv2/FLOW.md AGENTS.md
```

Expected: no output.

- [ ] **Step 5: Commit scaffold**

```bash
git add pkg/controllerv2/FLOW.md AGENTS.md
git commit -m "chore(controllerv2): scaffold package docs"
```

---

### Task 2: Implement Durable State Model, Validation, Canonical JSON, And Hash Slots

**Files:**
- Create: `pkg/controllerv2/state/types.go`
- Create: `pkg/controllerv2/state/errors.go`
- Create: `pkg/controllerv2/state/normalize.go`
- Create: `pkg/controllerv2/state/validate.go`
- Create: `pkg/controllerv2/state/codec.go`
- Create: `pkg/controllerv2/state/hashslots.go`
- Test: `pkg/controllerv2/state/state_test.go`

- [ ] **Step 1: Write failing state tests**

Create `pkg/controllerv2/state/state_test.go` with tests covering canonical ordering, checksum validation, duplicate node rejection, controller voter validation, slot peer validation, hash-slot coverage, bootstrap task consistency, and initial hash-slot distribution.

Use this skeleton and fill helper fixtures in the same file:

```go
package state

import (
    "strings"
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestEncodeCanonicalSortsAndChecksums(t *testing.T) {
    st := testState()
    st.Nodes[0], st.Nodes[1] = st.Nodes[1], st.Nodes[0]
    st.Controllers[0], st.Controllers[1] = st.Controllers[1], st.Controllers[0]

    data, err := Encode(st)
    require.NoError(t, err)
    require.Contains(t, string(data), `"checksum":"crc32c:`)

    decoded, err := Decode(data)
    require.NoError(t, err)
    require.Equal(t, []uint64{1, 2, 3}, nodeIDs(decoded.Nodes))
    require.Equal(t, []uint64{1, 2, 3}, decoded.Slots[0].DesiredPeers)
}

func TestDecodeRejectsChecksumMismatch(t *testing.T) {
    data, err := Encode(testState())
    require.NoError(t, err)
    tampered := strings.Replace(string(data), `"revision":1`, `"revision":2`, 1)

    _, err = Decode([]byte(tampered))
    require.ErrorIs(t, err, ErrChecksumMismatch)
}

func TestValidateRejectsDuplicateNode(t *testing.T) {
    st := testState()
    st.Nodes = append(st.Nodes, st.Nodes[0])
    require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsControllerWithoutRole(t *testing.T) {
    st := testState()
    st.Nodes[0].Roles = []NodeRole{NodeRoleData}
    require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsSlotPeerWithoutDataRole(t *testing.T) {
    st := testState()
    st.Nodes[1].Roles = []NodeRole{NodeRoleControllerVoter}
    require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRejectsHashSlotGap(t *testing.T) {
    st := testState()
    st.HashSlots.Ranges[0].To = 7
    require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestValidateRequiresBootstrapTaskMatchesAssignment(t *testing.T) {
    st := testState()
    st.Tasks[0].TargetPeers = []uint64{1, 2}
    require.ErrorIs(t, st.Validate(), ErrInvalidState)
}

func TestBuildInitialHashSlotTableDistributesRanges(t *testing.T) {
    table, err := BuildInitialHashSlotTable(16, 16384)
    require.NoError(t, err)
    require.Len(t, table.Ranges, 16)
    require.Equal(t, uint32(1), table.Ranges[0].SlotID)
    require.Equal(t, uint16(0), table.Ranges[0].From)
    require.Equal(t, uint16(1023), table.Ranges[0].To)
    require.Equal(t, uint32(16), table.Ranges[15].SlotID)
    require.Equal(t, uint16(15360), table.Ranges[15].From)
    require.Equal(t, uint16(16383), table.Ranges[15].To)
}

func testState() ClusterState {
    now := time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC)
    table, _ := BuildInitialHashSlotTable(1, 16)
    return ClusterState{
        SchemaVersion:    CurrentSchemaVersion,
        ClusterID:        "wk-test",
        Revision:         1,
        AppliedRaftIndex: 1,
        UpdatedAt:        now,
        Config: ClusterConfig{SlotCount: 1, HashSlotCount: 16, ReplicaCount: 3},
        Controllers: []ControllerVoter{{NodeID: 1, Addr: "n1", Role: ControllerRoleVoter}, {NodeID: 2, Addr: "n2", Role: ControllerRoleVoter}},
        Nodes: []Node{
            {NodeID: 1, Name: "n1", Addr: "n1", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100},
            {NodeID: 2, Name: "n2", Addr: "n2", Roles: []NodeRole{NodeRoleControllerVoter, NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100},
            {NodeID: 3, Name: "n3", Addr: "n3", Roles: []NodeRole{NodeRoleData}, JoinState: NodeJoinStateActive, Status: NodeStatusAlive, CapacityWeight: 100},
        },
        Slots: []SlotAssignment{{SlotID: 1, DesiredPeers: []uint64{3, 1, 2}, ConfigEpoch: 1, PreferredLeader: 1}},
        HashSlots: table,
        Tasks: []ReconcileTask{{TaskID: "slot-1-bootstrap-1", SlotID: 1, Kind: TaskKindBootstrap, Step: TaskStepCreateSlot, TargetNode: 1, TargetPeers: []uint64{1, 2, 3}, ConfigEpoch: 1, Status: TaskStatusPending}},
    }
}

func nodeIDs(nodes []Node) []uint64 {
    out := make([]uint64, 0, len(nodes))
    for _, node := range nodes { out = append(out, node.NodeID) }
    return out
}
```

- [ ] **Step 2: Run state tests to verify failure**

Run:

```bash
go test ./pkg/controllerv2/state -run 'TestEncodeCanonical|TestDecodeRejects|TestValidate|TestBuildInitial' -count=1
```

Expected: FAIL because the package implementation does not exist.

- [ ] **Step 3: Implement `errors.go`**

Add typed errors:

```go
package state

import "errors"

var (
    ErrInvalidState     = errors.New("controllerv2/state: invalid state")
    ErrChecksumMismatch = errors.New("controllerv2/state: checksum mismatch")
    ErrUnsupportedSchema = errors.New("controllerv2/state: unsupported schema")
)
```

- [ ] **Step 4: Implement `types.go` with English comments**

Define exported structs/enums matching the spec. Include English comments on every exported type and key field. Required JSON field names: `schema_version`, `cluster_id`, `revision`, `applied_raft_index`, `updated_at`, `config`, `controllers`, `nodes`, `slots`, `hash_slots`, `tasks`, `checksum`.

`ReconcileTask` must include these fields and JSON tags:

```go
TaskID      string     `json:"task_id"`
SlotID      uint32     `json:"slot_id"`
Kind        TaskKind   `json:"kind"`
Step        TaskStep   `json:"step"`
SourceNode  uint64     `json:"source_node,omitempty"`
TargetNode  uint64     `json:"target_node,omitempty"`
TargetPeers []uint64   `json:"target_peers,omitempty"`
ConfigEpoch uint64     `json:"config_epoch,omitempty"`
Attempt     uint32     `json:"attempt"`
Status      TaskStatus `json:"status"`
LastError   string     `json:"last_error,omitempty"`
```

V1 `FailTask` keeps the task in the active `tasks` list with `status=failed`, increments `Attempt`, and writes bounded `LastError`. `CompleteTask` deletes the task. Completed task history is deferred.

Required constants:

```go
const CurrentSchemaVersion uint32 = 1

type NodeRole string
const (
    NodeRoleControllerVoter NodeRole = "controller_voter"
    NodeRoleData NodeRole = "data"
)
```

Define analogous string constants for node status, join state, controller role, task kind, task step, and task status.

- [ ] **Step 5: Implement normalization**

In `normalize.go`, add:

```go
func (s *ClusterState) Normalize()
func (s ClusterState) Clone() ClusterState
func (n Node) HasRole(role NodeRole) bool
func cloneUint64s(in []uint64) []uint64
```

Rules: sort controllers/nodes/slots/hash ranges/tasks; sort roles lexicographically; sort desired peers and target peers; default `CapacityWeight` to 1 when zero; force `UpdatedAt` to UTC. `Clone` must deep-copy `Controllers`, `Nodes`, `Node.Roles`, `Slots`, `SlotAssignment.DesiredPeers`, `HashSlots.Ranges`, `Tasks`, and `ReconcileTask.TargetPeers`.

- [ ] **Step 6: Implement validation**

In `validate.go`, add `func (s ClusterState) Validate() error` and private helpers. Enforce all invariants from the spec, including bootstrap task consistency with assignment.

- [ ] **Step 7: Implement canonical encode/decode and checksum**

In `codec.go`, add:

```go
func Encode(st ClusterState) ([]byte, error)
func Decode(data []byte) (ClusterState, error)
func Checksum(st ClusterState) (string, error)
```

Use standard `encoding/json`. For checksum calculation, omit the checksum field by setting it to empty and using an alias struct with `omitempty`, or a private wire struct that excludes it. Use `hash/crc32` Castagnoli and format `crc32c:%08x`.

- [ ] **Step 8: Implement initial hash-slot table**

In `hashslots.go`, add `BuildInitialHashSlotTable(slotCount uint32, hashSlotCount uint16) (HashSlotTable, error)`. Distribute contiguous ranges evenly over physical slots. Require both counts > 0 and `slotCount <= uint32(hashSlotCount)`.

- [ ] **Step 9: Run state tests**

Run:

```bash
go test ./pkg/controllerv2/state -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit state package**

```bash
git add pkg/controllerv2/state
git commit -m "feat(controllerv2): add durable state model"
```

---

### Task 3: Implement Atomic State File Store

**Files:**
- Create: `pkg/controllerv2/statefile/store.go`
- Test: `pkg/controllerv2/statefile/store_test.go`

- [ ] **Step 1: Write failing statefile tests**

Create tests for save/load round trip, checksum mismatch, leftover temp ignored, and atomic replacement preserving old file when write fails. Use a fault hook for deterministic failure.

Required test names:

```go
func TestStoreSaveLoadRoundTrip(t *testing.T)
func TestStoreLoadRejectsChecksumMismatch(t *testing.T)
func TestStoreLoadIgnoresLeftoverTempFile(t *testing.T)
func TestStoreSaveFailureKeepsPreviousFile(t *testing.T)
```

- [ ] **Step 2: Run statefile tests to verify failure**

Run:

```bash
go test ./pkg/controllerv2/statefile -count=1
```

Expected: FAIL because `Store` does not exist.

- [ ] **Step 3: Implement store API**

In `store.go`, implement:

```go
type Store struct {
    path string
    afterTempWrite func() error
}

type Option func(*Store)

func New(path string, opts ...Option) *Store
func WithAfterTempWriteHook(hook func() error) Option
func (s *Store) Path() string
func (s *Store) Load(ctx context.Context) (state.ClusterState, error)
func (s *Store) Save(ctx context.Context, st state.ClusterState) error
```

Save must write encoded bytes to a temp file in the same directory, fsync temp file, rename, then fsync parent directory. Load must call `state.Decode` on the main path and ignore temp files.

- [ ] **Step 4: Run statefile tests**

Run:

```bash
go test ./pkg/controllerv2/state ./pkg/controllerv2/statefile -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit statefile package**

```bash
git add pkg/controllerv2/statefile
git commit -m "feat(controllerv2): add atomic state file store"
```

---

### Task 4: Implement Command Codec And FSM Apply Semantics

**Files:**
- Create: `pkg/controllerv2/command/command.go`
- Create: `pkg/controllerv2/command/codec.go`
- Test: `pkg/controllerv2/command/codec_test.go`
- Create: `pkg/controllerv2/fsm/fsm.go`
- Create: `pkg/controllerv2/fsm/mutations.go`
- Test: `pkg/controllerv2/fsm/fsm_test.go`

- [ ] **Step 1: Write failing command codec tests**

Create `pkg/controllerv2/command/codec_test.go` with:

```go
func TestCommandEncodeDecodeRoundTrip(t *testing.T)
func TestCommandDecodeRejectsUnknownVersion(t *testing.T)
```

The round trip must cover `KindInitClusterState`, `KindUpsertSlotAssignmentAndTask`, `ExpectedRevision`, initial cluster payload, assignment, task, and hash slots.

- [ ] **Step 2: Implement command package**

Define:

```go
type Kind string
const (
    KindInitClusterState Kind = "init_cluster_state"
    KindUpsertNode Kind = "upsert_node"
    KindUpdateControllerVoters Kind = "update_controller_voters"
    KindUpsertSlotAssignmentAndTask Kind = "upsert_slot_assignment_and_task"
    KindCompleteTask Kind = "complete_task"
    KindFailTask Kind = "fail_task"
    KindReplaceHashSlotTable Kind = "replace_hash_slot_table"
)

type Command struct { ... }
type InitClusterState struct { ClusterID string; Config state.ClusterConfig; Controllers []state.ControllerVoter; Nodes []state.Node }
type TaskResult struct { TaskID string; SlotID uint32; Err string; FinishedAt time.Time }
func Encode(Command) ([]byte, error)
func Decode([]byte) (Command, error)
```

`Command` must include `Init *InitClusterState`. Do not pass initial cluster state through local FSM constructor parameters. The first durable `cluster-state.json` must be produced by a committed `KindInitClusterState` command.

Use a JSON envelope with `version:1` and `command`.

- [ ] **Step 3: Run command tests**

Run:

```bash
go test ./pkg/controllerv2/command -count=1
```

Expected: PASS.

- [ ] **Step 4: Write failing FSM tests**

Create `pkg/controllerv2/fsm/fsm_test.go` covering:

```go
func TestApplyInitClusterStateCreatesRevisionAndHashSlots(t *testing.T)
func TestApplyUpsertNodeNoopDoesNotIncrementRevisionButAdvancesAppliedIndex(t *testing.T)
func TestApplyAssignmentAndTaskWritesAtomically(t *testing.T)
func TestApplyStaleBootstrapForAssignedSlotNoops(t *testing.T)
func TestApplyStaleBootstrapForMissingSlotRejects(t *testing.T)
func TestApplyExpectedRevisionMismatchNonBootstrapRejects(t *testing.T)
func TestApplyExpectedRevisionMismatchFailTaskRejectsWhenTaskExists(t *testing.T)
func TestApplyInvalidPeersReturnsSemanticRejectAndAdvancesAppliedIndex(t *testing.T)
func TestApplyCompleteTaskRemovesTask(t *testing.T)
func TestApplyFailTaskKeepsFailedTaskWithBoundedError(t *testing.T)
func TestApplyFailTaskMissingTaskNoops(t *testing.T)
func TestApplyFailTaskRejectsMissingResultOrSlotMismatch(t *testing.T)
func TestApplySaveFailureDoesNotPublishState(t *testing.T)
func TestSnapshotReturnsDeepCopy(t *testing.T)
```

Assert `ApplyResult` fields: `Changed`, `Noop`, `Rejected`, `Reason`, `Revision`, `AppliedRaftIndex`.

- [ ] **Step 5: Implement FSM API**

In `fsm.go`, define:

```go
type ApplyResult struct {
    Changed bool
    Noop bool
    Rejected bool
    Reason string
    Revision uint64
    AppliedRaftIndex uint64
}

type StateMachine struct { ... }
func New(store *statefile.Store) (*StateMachine, error)
func (sm *StateMachine) Load(ctx context.Context) error
func (sm *StateMachine) Snapshot(ctx context.Context) state.ClusterState
func (sm *StateMachine) IsDegraded() bool
func (sm *StateMachine) Apply(ctx context.Context, raftIndex uint64, cmd command.Command) (ApplyResult, error)
```

Use a mutex. Copy state before mutation. Save before publishing. On save error, set degraded and return error. `Snapshot` must return `state.ClusterState.Clone()` so callers cannot mutate FSM-owned slices.

- [ ] **Step 6: Implement mutation handlers**

In `mutations.go`, implement handlers for `InitClusterState`, `UpsertNode`, `UpdateControllerVoters`, `ReplaceHashSlotTable`, `UpsertSlotAssignmentAndTask`, `CompleteTask`, and `FailTask`.

Important details:

- `InitClusterState` uses `cmd.Init.ClusterID`, `cmd.Init.Config`, `cmd.Init.Controllers`, and `cmd.Init.Nodes`; it sets `Revision=1`, sets `AppliedRaftIndex=raftIndex`, builds a complete hash-slot table, validates, and saves. If state already exists, identical init no-ops and different init returns a semantic reject.
- Semantic invalid business state returns `ApplyResult{Rejected:true}` with nil error and advances `AppliedRaftIndex`.
- No-op and semantic reject outcomes must still save the updated `AppliedRaftIndex` when `raftIndex > current.AppliedRaftIndex`.
- Expected revision CAS matrix:
  - `ExpectedRevision == nil`: apply after normal semantic validation.
  - `ExpectedRevision == current revision`: apply normally.
  - bootstrap command with revision mismatch and existing equivalent assignment or active equivalent bootstrap task: idempotent no-op.
  - bootstrap command with revision mismatch and any existing assignment or active task for the slot: obsolete no-op.
  - bootstrap command with revision mismatch and slot still missing: semantic reject so planner re-runs against current state.
  - non-bootstrap command with revision mismatch: prove idempotence for that handler or return semantic reject.
- `CompleteTask` deletes the active task.
- Define `MaxTaskLastErrorBytes = 1024`.
- `FailTask` requires non-nil `TaskResult` and non-empty `TaskID`; missing result or empty task ID is a semantic reject.
- `FailTask` keeps the task active with `status=failed`, increments `Attempt`, writes `LastError` truncated to `MaxTaskLastErrorBytes` without splitting UTF-8 runes, advances `AppliedRaftIndex`, and increments logical `Revision`.
- `FailTask` for an absent task is an obsolete no-op.
- `FailTask` with `TaskResult.SlotID != 0` and a slot that does not match the task slot is a semantic reject.

- [ ] **Step 7: Run FSM tests**

Run:

```bash
go test ./pkg/controllerv2/command ./pkg/controllerv2/fsm -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit command and FSM**

```bash
git add pkg/controllerv2/command pkg/controllerv2/fsm
git commit -m "feat(controllerv2): add command fsm apply"
```

---

### Task 5: Implement Bootstrap Planner

**Files:**
- Create: `pkg/controllerv2/planner/planner.go`
- Create: `pkg/controllerv2/planner/bootstrap.go`
- Test: `pkg/controllerv2/planner/bootstrap_test.go`

- [ ] **Step 1: Write failing planner tests**

Create tests:

```go
func TestBootstrapPlannerBlocksWhenInsufficientDataNodes(t *testing.T)
func TestBootstrapPlannerPicksLowestMissingSlot(t *testing.T)
func TestBootstrapPlannerSkipsSlotWithActiveTask(t *testing.T)
func TestBootstrapPlannerSpreadsReplicasAcrossEqualWeightNodes(t *testing.T)
func TestBootstrapPlannerUsesCapacityWeight(t *testing.T)
func TestBootstrapPlannerSpreadsPreferredLeader(t *testing.T)
func TestBootstrapPlannerCommandContainsExpectedRevisionAssignmentAndTask(t *testing.T)
```

- [ ] **Step 2: Implement planner interfaces**

In `planner.go`:

```go
type Planner interface { Next(context.Context, View) (Decision, error) }
type View struct { State state.ClusterState; Observations RuntimeObservations; Constraints Constraints; Now time.Time }
type Decision struct { Kind DecisionKind; Reason string; Command command.Command }
```

Define `DecisionKindNone`, `DecisionKindBlocked`, `DecisionKindCommand`.

- [ ] **Step 3: Implement bootstrap planner**

In `bootstrap.go`, implement `NewBootstrapPlanner()` and `Next`.

Rules:

- Eligible node: data role, active join state, alive status, positive capacity.
- Pick lowest missing slot `1..SlotCount` with no assignment and no active task.
- Select peers with integer cross-multiplication load comparison: `loadA*weightB < loadB*weightA`.
- Stable tie: `(slotID + nodeID) % candidateCount`, then `nodeID`.
- Final `DesiredPeers` sorted ascending.
- Preferred leader chosen from selected peers by current preferred-leader load, then stable tie.
- Return `command.KindUpsertSlotAssignmentAndTask` with `ExpectedRevision`, assignment, task, `TargetPeers`, and `ConfigEpoch`.

- [ ] **Step 4: Run planner tests**

Run:

```bash
go test ./pkg/controllerv2/planner -count=1
```

Expected: PASS.

- [ ] **Step 5: Run state/FSM/planner integration tests**

Run:

```bash
go test ./pkg/controllerv2/state ./pkg/controllerv2/statefile ./pkg/controllerv2/command ./pkg/controllerv2/fsm ./pkg/controllerv2/planner -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit planner**

```bash
git add pkg/controllerv2/planner
git commit -m "feat(controllerv2): add bootstrap planner"
```

---

### Task 6: Implement Full-File Sync

**Files:**
- Create: `pkg/controllerv2/sync/sync.go`
- Test: `pkg/controllerv2/sync/sync_test.go`

- [ ] **Step 1: Write failing sync tests**

Create tests:

```go
func TestServerReturnsNotModifiedForSameRevisionAndChecksum(t *testing.T)
func TestServerReturnsPayloadForSameRevisionDifferentChecksum(t *testing.T)
func TestClientInstallsNewerLeaderState(t *testing.T)
func TestClientInstallsLeaderStateWhenLocalMissing(t *testing.T)
func TestClientRepairsSameRevisionDifferentChecksum(t *testing.T)
func TestClientRejectsWrongClusterID(t *testing.T)
func TestClientRejectsBadChecksum(t *testing.T)
func TestClientRejectsLowerRevisionBeforeSave(t *testing.T)
func TestClientKeepsMemorySnapshotWhenLocalCorruptAndLeaderUnavailable(t *testing.T)
func TestClientPreservesLocalStateOnStaleLeader(t *testing.T)
func TestClientRetriesAdvertisedLeader(t *testing.T)
func TestClientPreservesLocalStateOnNotReady(t *testing.T)
```

Use in-process fake endpoints, not network sockets.

- [ ] **Step 2: Implement sync types and interfaces**

In `sync.go` define:

```go
type GetStateRequest struct { ClusterID string; LocalRevision uint64; LocalChecksum string }
type GetStateResponse struct { NotLeader bool; NotReady bool; StaleLeader bool; LeaderID uint64; NotModified bool; Revision uint64; Checksum string; Payload []byte }
type Endpoint interface { GetState(context.Context, GetStateRequest) (GetStateResponse, error) }
type PeerPicker interface { Endpoint(nodeID uint64) (Endpoint, bool); PeerIDs() []uint64 }
```

- [ ] **Step 3: Implement server**

Server accepts functions for leader ID, readiness, and state snapshot. It returns `NotLeader`, `NotReady`, `StaleLeader`, `NotModified`, or full payload according to the spec.

- [ ] **Step 4: Implement client**

Client loads local state from `statefile.Store`, probes leader then peers, validates response payload with `state.Decode`, confirms header revision/checksum equals decoded state, rejects lower revision before save, allows same-revision checksum repair, and saves via statefile.

Local file handling:

- If local file is missing, start with revision 0 and empty checksum, then install the first valid leader payload.
- If local file is corrupt but the client already has an in-memory snapshot, keep that memory snapshot and retry peers; do not overwrite it with invalid data.
- If local file is corrupt and no leader is reachable, return an error but leave any existing memory snapshot intact.
- Wrong cluster ID, unsupported schema, bad checksum, and lower revision payloads are rejected before `statefile.Store.Save`.

- [ ] **Step 5: Run sync tests**

Run:

```bash
go test ./pkg/controllerv2/sync -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit sync**

```bash
git add pkg/controllerv2/sync
git commit -m "feat(controllerv2): add full state sync"
```

---

### Task 7: Implement ControllerV2 Raft Wrapper

**Files:**
- Create: `pkg/controllerv2/raft/config.go`
- Create: `pkg/controllerv2/raft/status.go`
- Create: `pkg/controllerv2/raft/service.go`
- Test: `pkg/controllerv2/raft/service_test.go`

- [ ] **Step 1: Write failing Raft service tests**

Create `service_test.go` with an in-memory transport that routes `raftpb.Message` by node ID to `Service.Step`.

Tests:

```go
func TestThreeControllerVotersCommitStateFile(t *testing.T)
func TestThreeControllerVotersCommitInitClusterStateToIdenticalChecksum(t *testing.T)
func TestThreeControllerVotersRejectFollowerProposal(t *testing.T)
func TestMarkAppliedFailureDegradesServiceAndStopsApplyingLaterEntries(t *testing.T)
func TestRestartAfterStateFileSaveBeforeMarkAppliedReplaysIdempotently(t *testing.T)
func TestStartupReplaysWhenStateBehindRaftlog(t *testing.T)
func TestStartupRebuildsMissingStateFromCompleteLog(t *testing.T)
func TestStartupFailsDegradedWhenRequiredEntriesUnavailable(t *testing.T)
func TestStartupRejectsCorruptStateWithoutCompleteHistory(t *testing.T)
```

Use temp dirs for each node, `raftlog.Open`, and `db.ForController()`.

- [ ] **Step 2: Implement Raft config and status**

In `config.go`:

```go
type Peer struct { NodeID uint64; Addr string }
type Transport interface { Send(context.Context, []raftpb.Message) error }
type Config struct { NodeID uint64; Peers []Peer; AllowBootstrap bool; Storage multiraft.Storage; StateMachine *fsm.StateMachine; Transport Transport; TickInterval time.Duration }
```

Validate node ID, peers, storage, state machine, and transport. Default tick interval to 100ms, election tick 10, heartbeat tick 1, pre-vote/check-quorum true.

In `status.go`, define role, leader, term, applied index, degraded, and error reason.

- [ ] **Step 3: Implement Service skeleton**

In `service.go`, implement:

```go
func NewService(cfg Config) (*Service, error)
func (s *Service) Start(ctx context.Context) error
func (s *Service) Stop() error
func (s *Service) Propose(ctx context.Context, cmd command.Command) error
func (s *Service) Step(ctx context.Context, msg raftpb.Message) error
func (s *Service) LeaderID() uint64
func (s *Service) Status() Status
```

Use a single run loop like existing `pkg/controller/raft.Service`. Keep RawNode confined to the run loop.

- [ ] **Step 4: Implement startup and Ready processing**

Startup:

- Load storage initial state.
- Load FSM state file.
- Compare `state.AppliedRaftIndex` and storage applied index.
- Bootstrap only when allowed, no persistent state exists, and local node has the smallest peer ID.
- If persistent Raft state already exists, never re-bootstrap peers; configured peers are only for brand-new `AllowBootstrap` startup.
- Create RawNode with configured peers.

Startup recovery algorithm:

1. Read `storage.InitialState(ctx)` to get `raftApplied`.
2. Try `StateMachine.Load(ctx)`.
3. If state load succeeds:
   - if `state.AppliedRaftIndex == raftApplied`, use `raftApplied` as the applied boundary;
   - if `state.AppliedRaftIndex > raftApplied`, use `raftApplied` as replay start and rely on idempotent apply until raftlog catches up;
   - if `state.AppliedRaftIndex < raftApplied`, before creating `RawNode`, scan entries `state.AppliedRaftIndex+1..raftApplied` from storage and replay normal command entries into FSM until the state file reaches `AppliedRaftIndex == raftApplied`; if any required entry is unavailable, decode fails, or apply returns a non-local deterministic failure, mark degraded and fail start.
4. If state file is missing or corrupt:
   - if the log contains complete history from the init entry, rebuild state by replaying entries into FSM first, producing a valid state file, then start `RawNode` with the rebuilt applied boundary;
   - if the log has been compacted or required entries are unavailable, mark degraded and fail start.
5. Brand-new empty log plus `AllowBootstrap=true` is the only empty-state startup exception.
6. Never treat logical `Revision` as a Raft replay boundary.

Ready processing:

- Persist HardState/Entries/Snapshot through storage before sending messages.
- Apply committed normal entries by decoding `command.Command` and calling `fsm.Apply(ctx, entry.Index, cmd)`.
- Treat semantic rejects/no-ops as nil apply errors.
- Call `storage.MarkApplied` only after FSM apply succeeds.
- If `MarkApplied` fails, mark degraded, fail pending proposal, and stop applying later entries.

- [ ] **Step 5: Implement proposal tracking**

Track proposed entry indexes and response channels. Follower `Propose` returns `ErrNotLeader`. Leader proposal waits until apply result or context cancellation.

- [ ] **Step 6: Run Raft tests**

Run:

```bash
go test ./pkg/controllerv2/raft -count=1
```

Expected: PASS.

- [ ] **Step 7: Run all ControllerV2 package tests**

Run:

```bash
go test ./pkg/controllerv2/... -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Raft wrapper**

```bash
git add pkg/controllerv2/raft
git commit -m "feat(controllerv2): add raft service"
```

---

### Task 8: Implement Thin Server Facade And Planner Tick Wiring

**Files:**
- Create: `pkg/controllerv2/server/server.go`
- Test: `pkg/controllerv2/server/server_test.go`
- Modify: `pkg/controllerv2/FLOW.md`

- [ ] **Step 1: Write failing server facade tests**

Create tests:

```go
func TestServerTickPlannerProposesBootstrapCommand(t *testing.T)
func TestServerLocalStateReturnsSnapshotCopy(t *testing.T)
func TestServerSyncOnceDelegatesToSyncClient(t *testing.T)
```

Use fake proposer and fake sync client; do not require real Raft in these unit tests.

- [ ] **Step 2: Implement server facade**

Define:

```go
type Proposer interface { Propose(context.Context, command.Command) error; LeaderID() uint64 }
type SyncClient interface { SyncOnce(context.Context) (state.ClusterState, error) }
type Server struct { ... }
func New(cfg Config) (*Server, error)
func (s *Server) TickPlanner(ctx context.Context) error
func (s *Server) LocalState() state.ClusterState
func (s *Server) SyncOnce(ctx context.Context) error
```

`TickPlanner` builds `planner.View` from local state, runs planner, and proposes only when decision kind is command.

- [ ] **Step 3: Update `FLOW.md` with facade flow**

Add a short section:

```text
Planner tick: LocalState -> planner.Next -> Raft Propose -> FSM Apply -> statefile save.
Non-controller sync: SyncOnce -> leader GetState -> statefile save -> LocalState update.
```

- [ ] **Step 4: Run server tests**

Run:

```bash
go test ./pkg/controllerv2/server -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit server facade**

```bash
git add pkg/controllerv2/server pkg/controllerv2/FLOW.md
git commit -m "feat(controllerv2): add server facade"
```

---

### Task 9: Final Integration, Documentation, And Verification

**Files:**
- Modify as needed: `pkg/controllerv2/FLOW.md`
- Modify as needed: `docs/development/PROJECT_KNOWLEDGE.md`
- Modify as needed: `docs/development/CODE_QUALITY.md`

- [ ] **Step 1: Run package verification**

Run:

```bash
go test ./pkg/controllerv2/... -count=1
```

Expected: PASS.

- [ ] **Step 2: Run related package verification**

Run:

```bash
go test ./pkg/controllerv2/... ./pkg/raftlog ./pkg/slot/multiraft -count=1
```

Expected: PASS.

- [ ] **Step 3: Run broader targeted verification**

Run:

```bash
go test ./pkg/controller/... ./pkg/cluster ./pkg/controllerv2/... -count=1
```

Expected: PASS. If existing unrelated tests fail, capture exact failures and investigate before claiming completion.

- [ ] **Step 4: Check formatting and static consistency**

Run:

```bash
gofmt -w pkg/controllerv2
git diff --check
go test ./pkg/controllerv2/... -count=1
```

Expected: no diff-check output and tests pass.

- [ ] **Step 5: Review AGENTS/FLOW consistency**

Run:

```bash
rg -n "controllerv2|standalone|single-node" AGENTS.md pkg/controllerv2/FLOW.md docs/superpowers/specs/2026-05-24-controllerv2-design.md
```

Expected: `controllerv2` is documented; no new standalone/bypass-cluster deployment wording was introduced.

- [ ] **Step 6: Commit final cleanup**

If any documentation cleanup was needed:

```bash
git add pkg/controllerv2/FLOW.md docs/development/PROJECT_KNOWLEDGE.md docs/development/CODE_QUALITY.md
git commit -m "docs(controllerv2): finalize flow notes"
```

Skip commit if no files changed.

- [ ] **Step 7: Final status check**

Run:

```bash
git status --short
git log --oneline -8
```

Expected: only intentional uncommitted files remain, preferably none. Recent commits should show the controllerv2 implementation sequence.

---

## Execution Notes For Subagents

- Each worker must check `git status --short` before editing and must not revert unrelated changes.
- Each worker owns only the files listed in its task.
- If a worker sees unexpected edits in owned files that it did not make, stop and report before continuing.
- Keep all new comments in English for exported types, key struct fields, and config fields.
- Tests should be unit-speed. Do not add integration tests requiring Docker or long sleeps.
- Do not wire `pkg/controllerv2` into production startup in this plan.
