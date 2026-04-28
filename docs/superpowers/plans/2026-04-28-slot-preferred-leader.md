# Slot Preferred Leader Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add controller-owned soft preferred Slot leaders so Slot leadership converges toward a balanced distribution without weakening Raft safety.

**Architecture:** Controller metadata stores preferred leader intent and per-Slot leader-transfer cooldown. Runtime observations expose current voters so the planner and executor only transfer leadership to an observed voter. Planner creates lightweight leader-transfer tasks for skew correction or safe preference convergence; cluster reconciliation executes them through existing managed Slot transfer RPC while Raft remains the final authority.

**Tech Stack:** Go, WuKongIM controller metadata/state machine, cluster controller RPC codecs, slot/multiraft runtime status, managed Slot reconciliation, manager usecase/HTTP DTOs, Go unit and integration tests.

---

## Source Documents

- Spec: `docs/superpowers/specs/2026-04-28-slot-preferred-leader-design.md`
- Package flow docs to check and update when behavior changes:
  - `pkg/controller/FLOW.md`
  - `pkg/cluster/FLOW.md`
  - `pkg/slot/FLOW.md` only if public `multiraft.Status` semantics are documented there

## Required Skills During Execution

- @superpowers:test-driven-development for every behavior change.
- @superpowers:subagent-driven-development because this plan has independent code slices and subagents are available.
- @superpowers:verification-before-completion before claiming the branch is complete.

## File Structure

Create or modify these files:

- `pkg/controller/meta/types.go` — add `SlotAssignment.PreferredLeader`, `SlotAssignment.LeaderTransferCooldownUntil`, `SlotRuntimeView.CurrentVoters`, and `TaskKindLeaderTransfer`.
- `pkg/controller/meta/codec.go` — versioned assignment/runtime-view encoding, validation, normalization, new task kind validation.
- `pkg/controller/meta/store.go` — add atomic helper to update an assignment and delete its task for leader-transfer cooldown completion/failure.
- `pkg/controller/meta/store_test.go` — metadata codec/store tests for old/new assignment layouts, runtime voters, and atomic cooldown+task delete.
- `pkg/controller/plane/types.go` — add planner config for leader skew and cooldown durations.
- `pkg/controller/plane/planner.go` — select preferred leaders on bootstrap/repair/rebalance, add leader-placement decisions, skip unsafe/cooldown slots.
- `pkg/controller/plane/controller.go` — persist leader-transfer decisions and pass config defaults.
- `pkg/controller/plane/commands.go` — add `PreferredLeader` to `AddSlotRequest` command boundary.
- `pkg/controller/plane/statemachine.go` — AddSlot preferred leader validation, leader-transfer task result self-clear, cooldown updates.
- `pkg/controller/plane/controller_test.go` — planner and state machine tests.
- `pkg/controller/plane/add_remove_slot_test.go` — AddSlot preferred leader tests.
- `pkg/slot/multiraft/types.go` — add `Status.CurrentVoters`.
- `pkg/slot/multiraft/slot.go` — populate `CurrentVoters` from `RawNode.Status().Config.Voters.IDs()`.
- `pkg/slot/multiraft/step_test.go` or `pkg/slot/multiraft/api_test.go` — runtime status voter tests.
- `pkg/cluster/managed_slots.go` — add managed Slot status voters and test hooks.
- `pkg/cluster/codec_managed.go` — encode/decode `CurrentVoters` in managed Slot status responses.
- `pkg/cluster/codec_managed_test.go` — managed Slot codec tests.
- `pkg/cluster/readiness.go` — include `CurrentVoters` in `buildRuntimeView`.
- `pkg/cluster/runtime_observation_reporter.go` — clone/compare current voters in observation deltas.
- `pkg/cluster/observation_cache.go` — compare and copy current voters.
- `pkg/cluster/codec_control.go` — encode/decode new assignment/runtime fields in controller RPC/control payloads.
- `pkg/cluster/codec_control_test.go` — controller RPC codec tests.
- `pkg/cluster/agent.go` — extend `assignmentTaskState` with runtime view and node metadata when executing tasks.
- `pkg/cluster/reconciler.go` — leader-transfer task ownership, deterministic checker, runtime-view handoff, and node eligibility handoff.
- `pkg/cluster/slot_executor.go` — execute `TaskKindLeaderTransfer` without membership changes and revalidate stale-task target eligibility.
- `pkg/cluster/slot_executor_test.go` — leader-transfer executor tests.
- `pkg/cluster/cluster.go` — map `TaskKindLeaderTransfer` to stable observer/metric names.
- `pkg/cluster/operator.go` — fetch strict runtime observations and choose AddSlot preferred leaders using the same actual/preferred leader-load policy as bootstrap.
- `pkg/cluster/operator_test.go` — AddSlot preferred leader selection tests.
- `pkg/cluster/cluster_integration_test.go` — integration tests for initial distribution, AddSlot, single-node no-op, and restart convergence if practical.
- `pkg/cluster/FLOW.md` and `pkg/controller/FLOW.md` — document preferred-leader and leader-transfer task flow.
- `pkg/metrics/controller.go` — add leader-transfer metric labels and Slot leader skew gauge.
- `pkg/metrics/registry_test.go` — controller metric registration and label tests.
- `internal/app/observability.go` — compute leader skew from observed runtime views and active data nodes.
- `internal/app/observability_test.go` — observability hook and leader skew metric tests.
- `internal/usecase/management/slots.go` — expose preferred leader, current voters, leader match, transfer pending.
- `internal/usecase/management/tasks.go` — string mapping for `TaskKindLeaderTransfer`.
- `internal/access/manager/slots.go` — JSON DTO fields.
- `internal/access/manager/tasks.go` — task kind DTO mapping if separate.
- `internal/access/manager/server_test.go` and/or `internal/usecase/management/slots_test.go` — manager visibility tests.
- `docs/development/PROJECT_KNOWLEDGE.md` — add a short note if implementation confirms an important durable rule not already documented.

## Implementation Notes

- Do not change Raft election internals.
- Do not call `TransferLeadership` unless current leader and current voter set are known.
- A leader-transfer target must be in `DesiredPeers`, observed `CurrentVoters`, and an active/alive/data-capable controller node at both planning time and execution time.
- `PreferredLeader` changes do not increment `ConfigEpoch`; `ConfigEpoch` remains membership epoch.
- `TaskKindLeaderTransfer` is opportunistic. Terminal failure must delete the task and set cooldown instead of leaving `TaskStatusFailed` as a durable blocker.
- Existing bootstrap/repair/rebalance task failure semantics stay unchanged.
- Existing `TaskStepTransferLeader` can be reused for `TaskKindLeaderTransfer`.
- Single-node clusters may fill `PreferredLeader`, but must not create leader-transfer tasks.
- Before editing `pkg/cluster`, `pkg/controller`, or `pkg/slot`, read the package `FLOW.md` first.

---

### Task 1: Controller Metadata Fields and Codecs

**Files:**
- Modify: `pkg/controller/meta/types.go`
- Modify: `pkg/controller/meta/codec.go`
- Modify: `pkg/controller/meta/store.go`
- Test: `pkg/controller/meta/store_test.go`

- [ ] **Step 1: Read controller flow and current metadata tests**

Run:

```bash
sed -n '1,240p' pkg/controller/FLOW.md
rg -n "SlotAssignment|SlotRuntimeView|TaskKind|encodeGroupAssignment|encodeGroupRuntimeView|validTaskKind" pkg/controller/meta pkg/controller/plane
```

Expected: confirm current assignment/runtime/task codec shape before editing.

- [ ] **Step 2: Write failing assignment codec test for new fields**

Add this test to `pkg/controller/meta/store_test.go`:

```go
func TestStoreAssignmentCodecRoundTripPreferredLeaderAndCooldown(t *testing.T) {
    store := openTestStore(t)
    ctx := context.Background()
    cooldown := time.Unix(123, 456)

    require.NoError(t, store.UpsertAssignment(ctx, SlotAssignment{
        SlotID:                      7,
        DesiredPeers:                []uint64{3, 1, 2},
        PreferredLeader:             2,
        LeaderTransferCooldownUntil: cooldown,
        ConfigEpoch:                 9,
        BalanceVersion:              4,
    }))

    got, err := store.GetAssignment(ctx, 7)
    require.NoError(t, err)
    require.Equal(t, []uint64{1, 2, 3}, got.DesiredPeers)
    require.Equal(t, uint64(2), got.PreferredLeader)
    require.Equal(t, cooldown.UnixNano(), got.LeaderTransferCooldownUntil.UnixNano())
    require.Equal(t, uint64(9), got.ConfigEpoch)
    require.Equal(t, uint64(4), got.BalanceVersion)
}
```

Add validation coverage for all assignment write families:

```go
func TestStoreRejectsPreferredLeaderOutsideDesiredPeersAcrossWritePaths(t *testing.T) {
    store := openTestStore(t)
    ctx := context.Background()
    invalid := SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2}, PreferredLeader: 3}
    task := ReconcileTask{SlotID: 1, Kind: TaskKindBootstrap, Step: TaskStepAddLearner, TargetNode: 1}
    table := hashslot.NewHashSlotTable(8, 1)

    require.ErrorIs(t, store.UpsertAssignment(ctx, invalid), ErrInvalidArgument)
    require.ErrorIs(t, store.UpsertAssignmentTask(ctx, invalid, task), ErrInvalidArgument)
    require.ErrorIs(t, store.UpsertAssignmentsAndSaveHashSlotTable(ctx, []SlotAssignment{invalid}, table), ErrInvalidArgument)
    require.ErrorIs(t, store.UpsertAssignmentTaskAndSaveHashSlotTable(ctx, invalid, task, table), ErrInvalidArgument)
    _, err := store.GuardedUpsertOnboardingJob(ctx, sampleOnboardingJob("job-a", time.Unix(1, 0)), nil, &invalid, &task)
    require.ErrorIs(t, err, ErrInvalidArgument)
}
```

- [ ] **Step 3: Run failing assignment codec test**

Run:

```bash
go test ./pkg/controller/meta -run 'TestStoreAssignmentCodecRoundTripPreferredLeaderAndCooldown|TestStoreRejectsPreferredLeaderOutsideDesiredPeersAcrossWritePaths' -count=1
```

Expected: FAIL because fields do not exist.

- [ ] **Step 4: Add metadata fields and validation**

In `pkg/controller/meta/types.go`, extend structs:

```go
type TaskKind uint8

const (
    TaskKindUnknown TaskKind = iota
    TaskKindBootstrap
    TaskKindRepair
    TaskKindRebalance
    TaskKindLeaderTransfer
)

type SlotAssignment struct {
    SlotID                      uint32
    DesiredPeers                []uint64
    PreferredLeader             uint64
    LeaderTransferCooldownUntil time.Time
    ConfigEpoch                 uint64
    BalanceVersion              uint64
}

type SlotRuntimeView struct {
    SlotID              uint32
    CurrentPeers        []uint64
    CurrentVoters       []uint64
    LeaderID            uint64
    HealthyVoters       uint32
    HasQuorum           bool
    ObservedConfigEpoch uint64
    LastReportAt        time.Time
}
```

In `pkg/controller/meta/codec.go`:

```go
func normalizeGroupAssignment(assignment SlotAssignment) SlotAssignment {
    assignment.DesiredPeers = normalizeUint64Set(assignment.DesiredPeers)
    return assignment
}

func normalizeGroupRuntimeView(view SlotRuntimeView) SlotRuntimeView {
    view.CurrentPeers = normalizeUint64Set(view.CurrentPeers)
    view.CurrentVoters = normalizeUint64Set(view.CurrentVoters)
    return view
}

func validTaskKind(kind TaskKind) bool {
    return kind >= TaskKindBootstrap && kind <= TaskKindLeaderTransfer
}
```

Add assignment validation after normalization:

```go
func validateAssignmentState(assignment SlotAssignment, invalid error) error {
    if assignment.PreferredLeader == 0 {
        return nil
    }
    idx := sort.Search(len(assignment.DesiredPeers), func(i int) bool {
        return assignment.DesiredPeers[i] >= assignment.PreferredLeader
    })
    if idx == len(assignment.DesiredPeers) || assignment.DesiredPeers[idx] != assignment.PreferredLeader {
        return invalid
    }
    return nil
}

func normalizeAndValidateAssignmentForPersistence(assignment SlotAssignment, invalid error) (SlotAssignment, error) {
    if assignment.SlotID == 0 {
        return SlotAssignment{}, invalid
    }
    assignment = normalizeGroupAssignment(assignment)
    if err := validateRequiredPeerSet(assignment.DesiredPeers, invalid); err != nil {
        return SlotAssignment{}, err
    }
    if err := validateAssignmentState(assignment, invalid); err != nil {
        return SlotAssignment{}, err
    }
    return assignment, nil
}
```

Use `normalizeAndValidateAssignmentForPersistence` from every assignment write path:

- `UpsertAssignment`
- `UpsertAssignmentTask`
- `UpsertAssignmentsAndSaveHashSlotTable`
- `UpsertAssignmentTaskAndSaveHashSlotTable`
- `UpsertAssignmentAndDeleteTask`
- `normalizeAndValidateAssignmentForOnboarding`, used by `UpsertOnboardingJobAssignmentTask` and `GuardedUpsertOnboardingJob`

Call `validateAssignmentState` from v2 decode as corruption validation. This keeps `PreferredLeader == 0 || PreferredLeader in DesiredPeers` true no matter whether the assignment is written through controller planning, AddSlot/hash-slot flows, onboarding, or direct tests.

- [ ] **Step 5: Add versioned assignment codec**

Keep old assignment decode compatible. Add constants near `recordVersion` usage:

```go
const (
    assignmentRecordVersionV1 byte = recordVersion
    assignmentRecordVersionV2 byte = 2
    runtimeViewRecordVersionV1 byte = recordVersion
    runtimeViewRecordVersionV2 byte = 2
)
```

Update `encodeGroupAssignment` to write v2:

```go
func encodeGroupAssignment(assignment SlotAssignment) []byte {
    assignment = normalizeGroupAssignment(assignment)

    data := make([]byte, 0, 56)
    data = append(data, assignmentRecordVersionV2)
    data = binary.BigEndian.AppendUint64(data, assignment.ConfigEpoch)
    data = binary.BigEndian.AppendUint64(data, assignment.BalanceVersion)
    data = binary.BigEndian.AppendUint64(data, assignment.PreferredLeader)
    data = appendInt64(data, assignment.LeaderTransferCooldownUntil.UnixNano())
    data = appendUint64Slice(data, assignment.DesiredPeers)
    return data
}
```

Update `decodeGroupAssignment` with a `switch data[0]`:

```go
switch data[0] {
case assignmentRecordVersionV1:
    // existing layout: configEpoch, balanceVersion, desiredPeers
case assignmentRecordVersionV2:
    // configEpoch, balanceVersion, preferredLeader, cooldownUnixNano, desiredPeers
    // zero cooldown unix decodes as time.Time{}
default:
    return SlotAssignment{}, ErrCorruptValue
}
```

Use `time.Unix(0, cooldownUnixNano)` only when `cooldownUnixNano != 0`.

- [ ] **Step 6: Run assignment codec test**

Run:

```bash
go test ./pkg/controller/meta -run TestStoreAssignmentCodecRoundTripPreferredLeaderAndCooldown -count=1
```

Expected: PASS.

- [ ] **Step 7: Write failing old assignment decode test**

Add this test in `pkg/controller/meta/store_test.go` near codec tests:

```go
func TestDecodeGroupAssignmentV1DefaultsPreferredLeaderFields(t *testing.T) {
    body := make([]byte, 0, 32)
    body = append(body, recordVersion)
    body = binary.BigEndian.AppendUint64(body, 3)
    body = binary.BigEndian.AppendUint64(body, 4)
    body = appendUint64Slice(body, []uint64{1, 2, 3})

    got, err := decodeGroupAssignment(encodeGroupKey(recordPrefixAssignment, 9), body)
    require.NoError(t, err)
    require.Equal(t, uint64(0), got.PreferredLeader)
    require.True(t, got.LeaderTransferCooldownUntil.IsZero())
    require.Equal(t, []uint64{1, 2, 3}, got.DesiredPeers)
}
```

- [ ] **Step 8: Run old decode test**

Run:

```bash
go test ./pkg/controller/meta -run TestDecodeGroupAssignmentV1DefaultsPreferredLeaderFields -count=1
```

Expected: PASS after Step 5. Fix if it fails.

- [ ] **Step 9: Write failing runtime-view current voters test**

Add:

```go
func TestStoreRuntimeViewCodecRoundTripCurrentVoters(t *testing.T) {
    store := openTestStore(t)
    ctx := context.Background()
    now := time.Unix(10, 20)

    require.NoError(t, store.UpsertRuntimeView(ctx, SlotRuntimeView{
        SlotID:              3,
        CurrentPeers:        []uint64{1, 2, 3, 4},
        CurrentVoters:       []uint64{1, 2, 3},
        LeaderID:            2,
        HealthyVoters:       3,
        HasQuorum:           true,
        ObservedConfigEpoch: 6,
        LastReportAt:        now,
    }))

    got, err := store.GetRuntimeView(ctx, 3)
    require.NoError(t, err)
    require.Equal(t, []uint64{1, 2, 3, 4}, got.CurrentPeers)
    require.Equal(t, []uint64{1, 2, 3}, got.CurrentVoters)
}
```

If store method names differ, use existing runtime-view test helpers and keep the assertion exact.

- [ ] **Step 10: Implement runtime-view v2 codec and validation**

Update `encodeGroupRuntimeView` to write v2 with `CurrentVoters`. Decode v1 as old layout with `CurrentVoters=nil`.

Validation rule in `validateRuntimeViewState`:

```go
if len(view.CurrentVoters) > 0 {
    if err := validateCanonicalPeerSet(view.CurrentVoters, invalid); err != nil {
        return err
    }
    if view.LeaderID != 0 && !containsSortedUint64(view.CurrentVoters, view.LeaderID) {
        return invalid
    }
}
```

Keep existing leader-in-`CurrentPeers` validation for backward compatibility.

- [ ] **Step 11: Add atomic assignment update + task delete store helper**

In `pkg/controller/meta/store.go`, add:

```go
func (s *Store) UpsertAssignmentAndDeleteTask(ctx context.Context, assignment SlotAssignment, slotID uint32) error {
    if err := s.ensureOpen(); err != nil {
        return err
    }
    if err := s.checkContext(ctx); err != nil {
        return err
    }
    assignment = normalizeGroupAssignment(assignment)
    if assignment.SlotID == 0 || assignment.SlotID != slotID {
        return ErrInvalidArgument
    }
    if err := validateRequiredPeerSet(assignment.DesiredPeers, ErrInvalidArgument); err != nil {
        return err
    }
    if err := validateAssignmentState(assignment, ErrInvalidArgument); err != nil {
        return err
    }

    s.mu.Lock()
    defer s.mu.Unlock()
    return s.writeBatchLocked([]batchWrite{
        {key: encodeGroupKey(recordPrefixAssignment, assignment.SlotID), value: encodeGroupAssignment(assignment)},
        {key: encodeGroupKey(recordPrefixTask, slotID), delete: true},
    })
}
```

If `batchWrite` uses a different delete flag, follow the existing delete-task batch pattern.

- [ ] **Step 12: Run metadata package tests**

Run:

```bash
go test ./pkg/controller/meta -count=1
```

Expected: PASS.

- [ ] **Step 13: Commit Task 1**

Run:

```bash
git add pkg/controller/meta/types.go pkg/controller/meta/codec.go pkg/controller/meta/store.go pkg/controller/meta/store_test.go
git commit -m "feat: persist slot preferred leader metadata"
```

Expected: commit succeeds.

---

### Task 2: Runtime Current Voter Observation and RPC Codecs

**Files:**
- Modify: `pkg/slot/multiraft/types.go`
- Modify: `pkg/slot/multiraft/slot.go`
- Test: `pkg/slot/multiraft/step_test.go` or `pkg/slot/multiraft/api_test.go`
- Modify: `pkg/cluster/managed_slots.go`
- Modify: `pkg/cluster/codec_managed.go`
- Test: `pkg/cluster/codec_managed_test.go`
- Modify: `pkg/cluster/readiness.go`
- Modify: `pkg/cluster/runtime_observation_reporter.go`
- Modify: `pkg/cluster/observation_cache.go`
- Modify: `pkg/cluster/codec_control.go`
- Test: `pkg/cluster/codec_control_test.go`

- [ ] **Step 1: Write failing multiraft status voter test**

Add a test in `pkg/slot/multiraft/step_test.go` or a nearby status test file:

```go
func TestRuntimeStatusIncludesCurrentVoters(t *testing.T) {
    rt := newTestRuntime(t, 1)
    slotID := SlotID(1)
    openSingleNodeLeader(t, rt, slotID)

    st, err := rt.Status(slotID)
    require.NoError(t, err)
    require.Equal(t, []NodeID{1}, st.CurrentVoters)
}
```

Use existing test helpers for runtime creation and single-node bootstrap. If the package uses custom assertions instead of `require`, follow local style.

- [ ] **Step 2: Run failing multiraft test**

Run:

```bash
go test ./pkg/slot/multiraft -run TestRuntimeStatusIncludesCurrentVoters -count=1
```

Expected: FAIL because `Status.CurrentVoters` does not exist.

- [ ] **Step 3: Populate `Status.CurrentVoters`**

In `pkg/slot/multiraft/types.go`:

```go
type Status struct {
    SlotID        SlotID
    NodeID        NodeID
    LeaderID      NodeID
    CurrentVoters []NodeID
    Term          uint64
    CommitIndex   uint64
    AppliedIndex  uint64
    Role          Role
}
```

In `pkg/slot/multiraft/slot.go`, update `refreshStatus`:

```go
func currentVotersFromRaftStatus(st raft.Status) []NodeID {
    ids := st.Config.Voters.IDs()
    voters := make([]NodeID, 0, len(ids))
    for id := range ids {
        voters = append(voters, NodeID(id))
    }
    sort.Slice(voters, func(i, j int) bool { return voters[i] < voters[j] })
    return voters
}
```

Then inside the mutex:

```go
g.status.CurrentVoters = currentVotersFromRaftStatus(st)
```

Add `sort` import.

- [ ] **Step 4: Run multiraft status test**

Run:

```bash
go test ./pkg/slot/multiraft -run TestRuntimeStatusIncludesCurrentVoters -count=1
```

Expected: PASS.

- [ ] **Step 5: Write failing managed slot status codec test**

In `pkg/cluster/codec_managed_test.go`, add:

```go
func TestManagedSlotStatusResponseRoundTripCurrentVoters(t *testing.T) {
    body, err := encodeManagedSlotResponse(managedSlotRPCResponse{
        LeaderID:      2,
        CurrentVoters: []uint64{1, 2, 3},
        CommitIndex:   10,
        AppliedIndex:  9,
    })
    require.NoError(t, err)

    got, err := decodeManagedSlotResponse(body)
    require.NoError(t, err)
    require.Equal(t, []uint64{1, 2, 3}, got.CurrentVoters)
}
```

- [ ] **Step 6: Implement managed status voters**

In `pkg/cluster/managed_slots.go`:

```go
type managedSlotRPCResponse struct {
    // existing fields
    CurrentVoters []uint64 `json:"current_voters,omitempty"`
}

type managedSlotStatus struct {
    LeaderID      multiraft.NodeID
    CurrentVoters []multiraft.NodeID
    CommitIndex   uint64
    AppliedIndex  uint64
}
```

In local status:

```go
CurrentVoters: append([]multiraft.NodeID(nil), status.CurrentVoters...),
```

In RPC response handling, convert between `[]uint64` and `[]multiraft.NodeID`.

In `pkg/cluster/codec_managed.go`, append/read the `CurrentVoters` slice in a backward-compatible way. If the codec uses flags and variable sections, add the slice after existing indices and make decode accept old responses with no trailing voters as nil.

- [ ] **Step 7: Run managed codec tests**

Run:

```bash
go test ./pkg/cluster -run 'TestManagedSlot.*CurrentVoters|TestManagedSlotCodec' -count=1
```

Expected: PASS.

- [ ] **Step 8: Write failing runtime view build test**

In `pkg/cluster/readiness_test.go`, extend or add:

```go
func TestBuildRuntimeViewIncludesCurrentVoters(t *testing.T) {
    view := buildRuntimeView(time.Unix(1, 0), 7, multiraft.Status{
        LeaderID:      2,
        CurrentVoters: []multiraft.NodeID{1, 2, 3},
    }, []multiraft.NodeID{1, 2, 3, 4}, 5)

    require.Equal(t, []uint64{1, 2, 3}, view.CurrentVoters)
    require.Equal(t, []uint64{1, 2, 3, 4}, view.CurrentPeers)
}
```

- [ ] **Step 9: Include voters in runtime views and observation copies**

Update `pkg/cluster/readiness.go`:

```go
currentVoters := make([]uint64, 0, len(status.CurrentVoters))
for _, voter := range status.CurrentVoters {
    currentVoters = append(currentVoters, uint64(voter))
}
view.CurrentVoters = currentVoters
```

Update clone/compare code in:

- `pkg/cluster/runtime_observation_reporter.go`
- `pkg/cluster/observation_cache.go`

Any equality function that compares `CurrentPeers` must compare `CurrentVoters` too.

- [ ] **Step 10: Update controller RPC runtime/assignment codecs**

In `pkg/cluster/codec_control.go`:

- Convert Slot assignment and runtime-view records to length-delimited, internally versioned records before adding fields. The current functions concatenate variable-length records, so optional trailing fields cannot be decoded safely from lists, observation deltas, or hash-slot-table assignment payloads.
- Add `PreferredLeader` and `LeaderTransferCooldownUntil` to v2 assignment records.
- Add `CurrentVoters` to v2 runtime-view records.
- Decode old v1 records inside a record body and set the new fields to zero values. Empty `CurrentVoters` means leader transfer fails closed.

Use these helpers:

```go
const (
    controllerAssignmentRecordV1 byte = 1
    controllerAssignmentRecordV2 byte = 2
    controllerRuntimeViewRecordV1 byte = 1
    controllerRuntimeViewRecordV2 byte = 2
)

func appendAssignment(dst []byte, assignment controllermeta.SlotAssignment) []byte {
    record := make([]byte, 0, 64)
    record = append(record, controllerAssignmentRecordV2)
    record = binary.BigEndian.AppendUint32(record, assignment.SlotID)
    record = appendUint64Slice(record, assignment.DesiredPeers)
    record = binary.BigEndian.AppendUint64(record, assignment.ConfigEpoch)
    record = binary.BigEndian.AppendUint64(record, assignment.BalanceVersion)
    record = binary.BigEndian.AppendUint64(record, assignment.PreferredLeader)
    record = appendInt64(record, unixNanoOrZero(assignment.LeaderTransferCooldownUntil))
    return appendBytes(dst, record)
}

func consumeAssignment(body []byte) (controllermeta.SlotAssignment, []byte, error) {
    record, rest, err := readBytes(body)
    if err != nil {
        return controllermeta.SlotAssignment{}, nil, err
    }
    assignment, err := decodeAssignmentRecord(record)
    if err != nil {
        return controllermeta.SlotAssignment{}, nil, err
    }
    return assignment, rest, nil
}

func decodeAssignmentRecord(record []byte) (controllermeta.SlotAssignment, error) {
    if len(record) == 0 {
        return controllermeta.SlotAssignment{}, ErrInvalidConfig
    }
    switch record[0] {
    case controllerAssignmentRecordV1:
        return decodeAssignmentRecordV1(record[1:])
    case controllerAssignmentRecordV2:
        return decodeAssignmentRecordV2(record[1:])
    default:
        // Backward compatibility for old unframed callers/tests that pass the
        // v1 body directly to decodeAssignmentRecord during migration.
        return decodeAssignmentRecordV1(record)
    }
}

func decodeAssignmentRecordV1(body []byte) (controllermeta.SlotAssignment, error) {
    // slotID, desiredPeers, configEpoch, balanceVersion; require no trailing bytes.
}

func decodeAssignmentRecordV2(body []byte) (controllermeta.SlotAssignment, error) {
    // slotID, desiredPeers, configEpoch, balanceVersion, preferredLeader, cooldownUnixNano; require no trailing bytes.
}
```

Apply the same pattern to `appendRuntimeView` / `consumeRuntimeView`:

```go
func appendRuntimeView(dst []byte, view controllermeta.SlotRuntimeView) []byte {
    record := make([]byte, 0, 80)
    record = append(record, controllerRuntimeViewRecordV2)
    record = binary.BigEndian.AppendUint32(record, view.SlotID)
    record = appendUint64Slice(record, view.CurrentPeers)
    record = appendUint64Slice(record, view.CurrentVoters)
    record = binary.BigEndian.AppendUint64(record, view.LeaderID)
    record = binary.BigEndian.AppendUint32(record, view.HealthyVoters)
    if view.HasQuorum { record = append(record, 1) } else { record = append(record, 0) }
    record = binary.BigEndian.AppendUint64(record, view.ObservedConfigEpoch)
    record = appendInt64(record, unixNanoOrZero(view.LastReportAt))
    return appendBytes(dst, record)
}

func consumeRuntimeView(body []byte) (controllermeta.SlotRuntimeView, []byte, error) {
    record, rest, err := readBytes(body)
    if err != nil {
        return controllermeta.SlotRuntimeView{}, nil, err
    }
    view, err := decodeRuntimeViewRecord(record)
    if err != nil {
        return controllermeta.SlotRuntimeView{}, nil, err
    }
    return view, rest, nil
}

func decodeRuntimeViewRecord(record []byte) (controllermeta.SlotRuntimeView, error) {
    if len(record) == 0 {
        return controllermeta.SlotRuntimeView{}, ErrInvalidConfig
    }
    switch record[0] {
    case controllerRuntimeViewRecordV1:
        return decodeRuntimeViewRecordV1(record[1:])
    case controllerRuntimeViewRecordV2:
        return decodeRuntimeViewRecordV2(record[1:])
    default:
        return decodeRuntimeViewRecordV1(record)
    }
}
```

Update every direct caller:

- `encodeAgentReport` and `decodeAgentReport`
- `encodeRuntimeObservationReport` and `decodeRuntimeObservationReport`
- `encodeRuntimeViews`, `consumeRuntimeViews`, and `decodeRuntimeViews`
- `encodeAssignments`, `decodeAssignments`, and `decodeAssignmentsWithHashSlotTable`
- `encodeObservationDeltaResponse` and `decodeObservationDeltaResponse`

Do not append optional fields directly to the old unframed record layout.

Write or extend tests in `pkg/cluster/codec_control_test.go`:

```go
func TestControllerCodecAssignmentRoundTripPreferredLeader(t *testing.T) {
    cooldown := time.Unix(10, 20)
    body := appendAssignment(nil, controllermeta.SlotAssignment{
        SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2,
        LeaderTransferCooldownUntil: cooldown, ConfigEpoch: 4, BalanceVersion: 5,
    })

    got, rest, err := consumeAssignment(body)
    require.NoError(t, err)
    require.Empty(t, rest)
    require.Equal(t, uint64(2), got.PreferredLeader)
    require.Equal(t, cooldown.UnixNano(), got.LeaderTransferCooldownUntil.UnixNano())
}

func TestControllerCodecRuntimeViewRoundTripCurrentVoters(t *testing.T) {
    now := time.Unix(10, 20)
    body := appendRuntimeView(nil, controllermeta.SlotRuntimeView{
        SlotID: 1, CurrentPeers: []uint64{1, 2, 3, 4}, CurrentVoters: []uint64{1, 2, 3},
        LeaderID: 2, HealthyVoters: 3, HasQuorum: true, ObservedConfigEpoch: 7, LastReportAt: now,
    })

    got, rest, err := consumeRuntimeView(body)
    require.NoError(t, err)
    require.Empty(t, rest)
    require.Equal(t, []uint64{1, 2, 3}, got.CurrentVoters)
}

func TestControllerCodecDecodeLegacyAssignmentRecordDefaultsPreferredLeader(t *testing.T) {
    legacy := make([]byte, 0, 48)
    legacy = binary.BigEndian.AppendUint32(legacy, 1)
    legacy = appendUint64Slice(legacy, []uint64{1, 2, 3})
    legacy = binary.BigEndian.AppendUint64(legacy, 4)
    legacy = binary.BigEndian.AppendUint64(legacy, 5)

    got, err := decodeAssignmentRecord(legacy)
    require.NoError(t, err)
    require.Zero(t, got.PreferredLeader)
    require.True(t, got.LeaderTransferCooldownUntil.IsZero())
}

func TestControllerCodecDecodeLegacyRuntimeViewRecordDefaultsCurrentVoters(t *testing.T) {
    legacy := make([]byte, 0, 64)
    legacy = binary.BigEndian.AppendUint32(legacy, 1)
    legacy = appendUint64Slice(legacy, []uint64{1, 2, 3})
    legacy = binary.BigEndian.AppendUint64(legacy, 2)
    legacy = binary.BigEndian.AppendUint32(legacy, 3)
    legacy = append(legacy, 1)
    legacy = binary.BigEndian.AppendUint64(legacy, 7)
    legacy = appendInt64(legacy, time.Unix(10, 20).UnixNano())

    got, err := decodeRuntimeViewRecord(legacy)
    require.NoError(t, err)
    require.Nil(t, got.CurrentVoters)
}
```

- [ ] **Step 11: Run cluster codec and observation tests**

Run:

```bash
go test ./pkg/cluster -run 'TestControllerCodec.*(Assignment|RuntimeView)|TestBuildRuntimeView|TestRuntimeObservation|TestObservationCache|TestManagedSlot' -count=1
```

Expected: PASS.

- [ ] **Step 12: Commit Task 2**

Run:

```bash
git add pkg/slot/multiraft pkg/cluster/managed_slots.go pkg/cluster/codec_managed.go pkg/cluster/codec_managed_test.go pkg/cluster/readiness.go pkg/cluster/readiness_test.go pkg/cluster/runtime_observation_reporter.go pkg/cluster/observation_cache.go pkg/cluster/codec_control.go pkg/cluster/codec_control_test.go
git commit -m "feat: observe slot current voters"
```

Expected: commit succeeds.

---

### Task 3: Planner Preferred Leader Selection and Leader-Transfer Decisions

**Files:**
- Modify: `pkg/controller/plane/types.go`
- Modify: `pkg/controller/plane/planner.go`
- Modify: `pkg/controller/plane/commands.go`
- Modify: `pkg/controller/plane/statemachine.go`
- Test: `pkg/controller/plane/controller_test.go`
- Test: `pkg/controller/plane/add_remove_slot_test.go`

- [ ] **Step 1: Write failing bootstrap preferred leader tests**

In `pkg/controller/plane/controller_test.go`, replace or extend bootstrap target tests with:

```go
func TestPlannerBootstrapPersistsPreferredLeaderAndUsesAsTarget(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 4, ReplicaN: 3, LeaderSkewThreshold: 1})
    state := testState(aliveNode(1), aliveNode(2), aliveNode(3))

    decision, err := planner.ReconcileSlot(context.Background(), state, 2)
    require.NoError(t, err)
    require.NotNil(t, decision.Task)
    require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
    require.Equal(t, decision.Assignment.PreferredLeader, decision.Task.TargetNode)
}
```

Add a distribution test:

```go
func TestPlannerBootstrapBalancesPreferredLeaderLoad(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 6, ReplicaN: 3, LeaderSkewThreshold: 1})
    state := testState(aliveNode(1), aliveNode(2), aliveNode(3))
    leaderLoads := map[uint64]int{}

    for slotID := uint32(1); slotID <= 6; slotID++ {
        decision, err := planner.ReconcileSlot(context.Background(), state, slotID)
        require.NoError(t, err)
        require.NotZero(t, decision.Assignment.PreferredLeader)
        leaderLoads[decision.Assignment.PreferredLeader]++
        state.Assignments[slotID] = decision.Assignment
    }

    require.LessOrEqual(t, maxLoad(leaderLoads)-minLoad(leaderLoads, []uint64{1, 2, 3}), 1)
}
```

Use local helper names if they already exist; otherwise add small unexported helpers in the test file.

- [ ] **Step 2: Run failing bootstrap planner tests**

Run:

```bash
go test ./pkg/controller/plane -run 'TestPlannerBootstrap(PersistsPreferredLeader|BalancesPreferredLeaderLoad)' -count=1
```

Expected: FAIL because `PreferredLeader` is not set by planner.

- [ ] **Step 3: Add planner config and leader-load helpers**

In `pkg/controller/plane/types.go`:

```go
type PlannerConfig struct {
    SlotCount              uint32
    ReplicaN               int
    RebalanceSkewThreshold int
    LeaderSkewThreshold    int
    LeaderTransferCooldown time.Duration
    LeaderTransferFailureCooldown time.Duration
    MaxTaskAttempts        int
    RetryBackoffBase       time.Duration
}
```

In `NewPlanner`, default `LeaderSkewThreshold` to 1 and cooldowns to 30s if zero.

In `planner.go`, add helpers:

```go
func leaderLoads(state PlannerState) map[uint64]int {
    loads := make(map[uint64]int)
    for slotID, assignment := range state.Assignments {
        if view, ok := state.Runtime[slotID]; ok && view.LeaderID != 0 {
            loads[view.LeaderID]++
            continue
        }
        if assignment.PreferredLeader != 0 {
            loads[assignment.PreferredLeader]++
        }
    }
    return loads
}

func choosePreferredLeader(peers []uint64, loads map[uint64]int) uint64 {
    var target uint64
    var targetLoad int
    for _, peer := range peers {
        load := loads[peer]
        if target == 0 || load < targetLoad || (load == targetLoad && peer < target) {
            target, targetLoad = peer, load
        }
    }
    return target
}
```

- [ ] **Step 4: Set preferred leader during bootstrap**

In `ReconcileSlot` bootstrap branch:

```go
loads := leaderLoads(state)
preferred := choosePreferredLeader(peers, loads)
decision.Assignment = controllermeta.SlotAssignment{
    SlotID:          slotID,
    DesiredPeers:    peers,
    PreferredLeader: preferred,
    ConfigEpoch:     1,
}
decision.Task = &controllermeta.ReconcileTask{
    SlotID:     slotID,
    Kind:       controllermeta.TaskKindBootstrap,
    Step:       controllermeta.TaskStepAddLearner,
    TargetNode: preferred,
}
```

Keep `bootstrapTargetPeer` only if still used by legacy tests; otherwise remove it and update tests.

- [ ] **Step 5: Run bootstrap planner tests**

Run:

```bash
go test ./pkg/controller/plane -run 'TestPlannerBootstrap(PersistsPreferredLeader|BalancesPreferredLeaderLoad|RotatesTargetNodeAcrossSlots)' -count=1
```

Expected: new tests PASS. If the old rotate test conflicts with the new design, update it to assert balanced preferred leader load instead of slotID modulo behavior.

- [ ] **Step 6: Write failing repair/rebalance preferred leader tests**

Add tests with concrete state setup:

```go
func TestPlannerRepairPreservesPreferredLeaderWhenStillDesired(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
    state := testState(
        aliveNode(1), deadNode(2), aliveNode(3), aliveNode(4),
        withAssignment(1, 1, 2, 3), withPreferredLeader(1, 3),
        withRuntimeView(1, []uint64{1, 2, 3}, 1, true),
    )

    decision, err := planner.ReconcileSlot(context.Background(), state, 1)
    require.NoError(t, err)
    require.Equal(t, uint64(3), decision.Assignment.PreferredLeader)
    require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerRepairReplacesPreferredLeaderWhenSourceRemoved(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
    state := testState(
        aliveNode(1), deadNode(2), aliveNode(3), aliveNode(4),
        withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2),
        withRuntimeView(1, []uint64{1, 2, 3}, 1, true),
    )

    decision, err := planner.ReconcileSlot(context.Background(), state, 1)
    require.NoError(t, err)
    require.NotEqual(t, uint64(2), decision.Assignment.PreferredLeader)
    require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerRepairReplacesPreferredLeaderWhenStillDesiredButIneligible(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 3})
    state := testState(
        aliveNode(1), drainingNode(2), deadNode(3), aliveNode(4),
        withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2),
        withRuntimeView(1, []uint64{1, 2, 3}, 1, true),
    )

    decision, err := planner.ReconcileSlot(context.Background(), state, 1)
    require.NoError(t, err)
    require.NotEqual(t, uint64(2), decision.Assignment.PreferredLeader)
    require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}

func TestPlannerRebalanceSetsPreferredLeaderWhenOldPreferredRemoved(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 2, ReplicaN: 2, RebalanceSkewThreshold: 1})
    state := testState(
        aliveNode(1), aliveNode(2), aliveNode(3),
        withAssignment(1, 1, 2), withPreferredLeader(1, 1), withRuntimeView(1, []uint64{1, 2}, 1, true),
        withAssignment(2, 1, 2), withPreferredLeader(2, 2), withRuntimeView(2, []uint64{1, 2}, 2, true),
    )

    decision := planner.nextRebalanceDecision(state)
    require.NotZero(t, decision.SlotID)
    require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
}
```

Each test should assert:

```go
require.Contains(t, decision.Assignment.DesiredPeers, decision.Assignment.PreferredLeader)
```

and the preservation/replacement expectation.

- [ ] **Step 7: Implement preferred leader preservation/replacement**

Add helper:

```go
func (p *Planner) preferredLeaderForPeers(state PlannerState, current controllermeta.SlotAssignment, nextPeers []uint64) uint64 {
    if containsPeer(nextPeers, current.PreferredLeader) {
        if node, ok := state.Nodes[current.PreferredLeader]; ok && nodeSchedulableForData(node) {
            return current.PreferredLeader
        }
    }
    if current.PreferredLeader != 0 {
        loads := leaderLoads(state)
        delete(loads, current.PreferredLeader)
        return choosePreferredLeader(nextPeers, loads)
    }
    return choosePreferredLeader(nextPeers, leaderLoads(state))
}
```

Do not preserve an old preferred leader only because it is still in `DesiredPeers`; it must still be an active/alive/data-capable node. Use it in repair and rebalance assignment creation. Do not increment `ConfigEpoch` solely for preferred-leader changes; keep existing epoch increments for peer changes.

- [ ] **Step 8: Write failing leader rebalance tests**

Add tests in `controller_test.go`:

```go
func TestPlannerLeaderRebalanceCreatesTransferTaskForSkew(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3, LeaderSkewThreshold: 1})
    state := testState(
        aliveNode(1), aliveNode(2), aliveNode(3),
        withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeViewVoters(1, []uint64{1,2,3}, 1, true),
        withAssignment(2, 1, 2, 3), withPreferredLeader(2, 2), withRuntimeViewVoters(2, []uint64{1,2,3}, 1, true),
        withAssignment(3, 1, 2, 3), withPreferredLeader(3, 3), withRuntimeViewVoters(3, []uint64{1,2,3}, 1, true),
    )

    decision, err := planner.NextDecision(context.Background(), state)
    require.NoError(t, err)
    require.NotNil(t, decision.Task)
    require.Equal(t, controllermeta.TaskKindLeaderTransfer, decision.Task.Kind)
    require.Equal(t, controllermeta.TaskStepTransferLeader, decision.Task.Step)
    require.NotEqual(t, uint64(1), decision.Task.TargetNode)
}

func TestPlannerLeaderRebalanceSkipsWhenAnyAssignedSlotMissingRuntimeView(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 3, ReplicaN: 3, LeaderSkewThreshold: 1})
    state := testState(
        aliveNode(1), aliveNode(2), aliveNode(3),
        withAssignment(1, 1, 2, 3), withPreferredLeader(1, 2), withRuntimeViewVoters(1, []uint64{1,2,3}, 1, true),
        withAssignment(2, 1, 2, 3), withPreferredLeader(2, 2), withRuntimeViewVoters(2, []uint64{1,2,3}, 1, true),
        withAssignment(3, 1, 2, 3), withPreferredLeader(3, 3),
    )

    decision, err := planner.NextDecision(context.Background(), state)
    require.NoError(t, err)
    require.Nil(t, decision.Task)
}
```

Add skip tests for:

- single voter;
- missing `CurrentVoters`;
- target not in `CurrentVoters`;
- target node missing, dead, suspect, draining, joining, or controller-only rather than active/alive/data;
- `LeaderTransferCooldownUntil` in the future;
- balanced-but-wrong preference where a transfer would exceed threshold.

- [ ] **Step 9: Implement leader rebalance decision**

In `NextDecision`, after replica rebalance returns empty:

```go
if decision := p.nextLeaderPlacementDecision(state); decision.Task != nil {
    return decision, nil
}
return Decision{}, nil
```

Implement helpers:

```go
func (p *Planner) nextLeaderPlacementDecision(state PlannerState) Decision
func (p *Planner) nextLeaderSkewCorrection(state PlannerState, loads map[uint64]int) Decision
func (p *Planner) nextPreferenceConvergence(state PlannerState, loads map[uint64]int) Decision
func leaderTransferObservationsComplete(state PlannerState) bool
func leaderTransferSafeCandidate(state PlannerState, assignment controllermeta.SlotAssignment, view controllermeta.SlotRuntimeView, target uint64) bool
func resultingLeaderSkewAllowed(state PlannerState, slotID uint32, from, to uint64, threshold int) bool
```

`nextLeaderPlacementDecision` must fail closed before computing leader loads unless every assigned, non-migrating managed Slot has a strict runtime view:

```go
func leaderTransferObservationsComplete(state PlannerState) bool {
    for slotID, assignment := range state.Assignments {
        if state.slotMigrating(slotID) || state.slotLocked(slotID) || len(assignment.DesiredPeers) <= 1 {
            continue
        }
        view, ok := state.Runtime[slotID]
        if !ok || view.LeaderID == 0 || len(view.CurrentVoters) == 0 || !view.HasQuorum {
            return false
        }
    }
    return true
}
```

This guard prevents leader rebalance from using mixed actual/preferred load counts when strict runtime observations are incomplete.

`leaderTransferSafeCandidate` must fail closed on target node eligibility:

```go
node, ok := state.Nodes[target]
if !ok || !nodeSchedulableForData(node) {
    return false
}
```

Create task:

```go
task := &controllermeta.ReconcileTask{
    SlotID:     assignment.SlotID,
    Kind:       controllermeta.TaskKindLeaderTransfer,
    Step:       controllermeta.TaskStepTransferLeader,
    SourceNode: view.LeaderID,
    TargetNode: target,
}
```

Set `decision.Assignment.PreferredLeader = target` for skew correction and for preference convergence if the assignment had no preferred leader. Do not create a task when target equals current leader.

- [ ] **Step 10: Add AddSlot preferred leader command tests**

In `pkg/controller/plane/add_remove_slot_test.go`, add:

```go
func TestStateMachineAddSlotPersistsPreferredLeader(t *testing.T) {
    store := openControllerStore(t)
    sm := NewStateMachine(store, StateMachineConfig{})
    ctx := context.Background()
    require.NoError(t, store.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))

    require.NoError(t, sm.Apply(ctx, Command{
        Kind: CommandKindAddSlot,
        AddSlot: &AddSlotRequest{NewSlotID: 3, Peers: []uint64{1, 2, 3}, PreferredLeader: 2},
    }))

    assignment, err := store.GetAssignment(ctx, 3)
    require.NoError(t, err)
    require.Equal(t, uint64(2), assignment.PreferredLeader)
    task, err := store.GetTask(ctx, 3)
    require.NoError(t, err)
    require.Equal(t, uint64(2), task.TargetNode)
}

func TestStateMachineAddSlotRejectsMissingPreferredLeader(t *testing.T) {
    store := openControllerStore(t)
    sm := NewStateMachine(store, StateMachineConfig{})
    ctx := context.Background()
    require.NoError(t, store.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 2)))

    err := sm.Apply(ctx, Command{
        Kind: CommandKindAddSlot,
        AddSlot: &AddSlotRequest{NewSlotID: 3, Peers: []uint64{1, 2, 3}},
    })
    require.ErrorIs(t, err, controllermeta.ErrInvalidArgument)
}
```

- [ ] **Step 11: Implement AddSlot preferred leader validation**

In `commands.go`:

```go
type AddSlotRequest struct {
    NewSlotID       uint64
    Peers           []uint64
    PreferredLeader uint64
}
```

In `applyAddSlot`, validate `PreferredLeader`:

```go
preferred := req.PreferredLeader
if preferred == 0 {
    return controllermeta.ErrInvalidArgument
}
if !containsUint64(req.Peers, preferred) {
    return controllermeta.ErrInvalidArgument
}
assignment.PreferredLeader = preferred
task.TargetNode = preferred
```

Do not call `firstBootstrapTarget` or any old minimum-peer/default bootstrap target from the AddSlot state-machine boundary. Old AddSlot RPC payloads that decode `PreferredLeader=0` fail closed; the cluster operator path added in Task 6 must supply the balanced preferred leader from strict assignment/runtime observations.

- [ ] **Step 12: Run planner tests**

Run:

```bash
go test ./pkg/controller/plane -run 'TestPlanner.*Preferred|TestPlannerLeader|TestStateMachineAddSlotPersistsPreferredLeader|TestPlannerRepair|TestPlannerRebalance' -count=1
```

Expected: PASS.

- [ ] **Step 13: Commit Task 3**

Run:

```bash
git add pkg/controller/plane/types.go pkg/controller/plane/planner.go pkg/controller/plane/commands.go pkg/controller/plane/statemachine.go pkg/controller/plane/controller_test.go pkg/controller/plane/add_remove_slot_test.go
git commit -m "feat: plan balanced slot preferred leaders"
```

Expected: commit succeeds.

---


### Task 4: Leader-Transfer Task Result Lifecycle

**Files:**
- Modify: `pkg/controller/plane/statemachine.go`
- Modify: `pkg/controller/plane/types.go` if cooldown config belongs in state machine config instead of planner config
- Test: `pkg/controller/plane/controller_test.go`
- Modify: `pkg/controller/meta/store.go` if Task 1 did not already add the atomic helper

- [ ] **Step 1: Write failing success cooldown test**

In `pkg/controller/plane/controller_test.go`, add:

```go
func TestStateMachineLeaderTransferSuccessDeletesTaskAndSetsCooldown(t *testing.T) {
    store := openControllerStore(t)
    sm := NewStateMachine(store, StateMachineConfig{LeaderTransferCooldown: 30 * time.Second})
    ctx := context.Background()

    require.NoError(t, store.UpsertAssignment(ctx, controllermeta.SlotAssignment{
        SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2,
    }))
    require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
        SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader,
        SourceNode: 1, TargetNode: 2, Status: controllermeta.TaskStatusPending,
    }))

    now := time.Unix(100, 0)
    require.NoError(t, sm.Apply(ctx, Command{Kind: CommandKindTaskResult, Advance: &TaskAdvance{SlotID: 1, Now: now}}))

    _, err := store.GetTask(ctx, 1)
    require.ErrorIs(t, err, controllermeta.ErrNotFound)
    assignment, err := store.GetAssignment(ctx, 1)
    require.NoError(t, err)
    require.Equal(t, now.Add(30*time.Second).UnixNano(), assignment.LeaderTransferCooldownUntil.UnixNano())
}
```

- [ ] **Step 2: Write failing terminal failure self-clear test**

Add:

```go
func TestStateMachineLeaderTransferFailureExhaustionDeletesTaskAndSetsFailureCooldown(t *testing.T) {
    store := openControllerStore(t)
    sm := NewStateMachine(store, StateMachineConfig{
        MaxTaskAttempts:               3,
        RetryBackoffBase:              time.Second,
        LeaderTransferFailureCooldown: time.Minute,
    })
    ctx := context.Background()
    require.NoError(t, store.UpsertAssignment(ctx, controllermeta.SlotAssignment{
        SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2,
    }))
    require.NoError(t, store.UpsertTask(ctx, controllermeta.ReconcileTask{
        SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader,
        TargetNode: 2, Attempt: 2, Status: controllermeta.TaskStatusRetrying, NextRunAt: time.Unix(99, 0), LastError: "previous",
    }))

    now := time.Unix(100, 0)
    require.NoError(t, sm.Apply(ctx, Command{Kind: CommandKindTaskResult, Advance: &TaskAdvance{
        SlotID: 1, Attempt: 2, Now: now, Err: errors.New("current voters unknown"),
    }}))

    _, err := store.GetTask(ctx, 1)
    require.ErrorIs(t, err, controllermeta.ErrNotFound)
    assignment, err := store.GetAssignment(ctx, 1)
    require.NoError(t, err)
    require.Equal(t, now.Add(time.Minute).UnixNano(), assignment.LeaderTransferCooldownUntil.UnixNano())
}
```

- [ ] **Step 3: Run failing lifecycle tests**

Run:

```bash
go test ./pkg/controller/plane -run 'TestStateMachineLeaderTransfer(Success|FailureExhaustion)' -count=1
```

Expected: FAIL because special leader-transfer lifecycle is not implemented.

- [ ] **Step 4: Add state machine cooldown config defaults**

In the state machine config type, add:

```go
LeaderTransferCooldown        time.Duration
LeaderTransferFailureCooldown time.Duration
```

In config defaulting code, use:

```go
if cfg.LeaderTransferCooldown == 0 {
    cfg.LeaderTransferCooldown = 30 * time.Second
}
if cfg.LeaderTransferFailureCooldown == 0 {
    cfg.LeaderTransferFailureCooldown = 30 * time.Second
}
```

- [ ] **Step 5: Implement leader-transfer task result handling**

In `applyTaskResult` after loading task and attempt match:

```go
if task.Kind == controllermeta.TaskKindLeaderTransfer {
    return sm.applyLeaderTransferTaskResult(ctx, task, advance)
}
```

Implement:

```go
func (sm *StateMachine) applyLeaderTransferTaskResult(ctx context.Context, task controllermeta.ReconcileTask, advance TaskAdvance) error {
    assignment, err := sm.store.GetAssignment(ctx, task.SlotID)
    if errors.Is(err, controllermeta.ErrNotFound) {
        return sm.store.DeleteTask(ctx, task.SlotID)
    }
    if err != nil {
        return err
    }

    if advance.Err == nil {
        assignment.LeaderTransferCooldownUntil = advance.Now.Add(sm.cfg.LeaderTransferCooldown)
        return sm.store.UpsertAssignmentAndDeleteTask(ctx, assignment, task.SlotID)
    }

    task.Attempt++
    if int(task.Attempt) >= sm.cfg.MaxTaskAttempts {
        assignment.LeaderTransferCooldownUntil = advance.Now.Add(sm.cfg.LeaderTransferFailureCooldown)
        return sm.store.UpsertAssignmentAndDeleteTask(ctx, assignment, task.SlotID)
    }

    task.Status = controllermeta.TaskStatusRetrying
    task.LastError = advance.Err.Error()
    task.NextRunAt = advance.Now.Add(sm.retryDelay(task.Attempt))
    return sm.store.UpsertTask(ctx, task)
}
```

Keep old behavior for bootstrap/repair/rebalance.

- [ ] **Step 6: Run lifecycle tests**

Run:

```bash
go test ./pkg/controller/plane -run 'TestStateMachineLeaderTransfer(Success|FailureExhaustion|MarksTaskFailedAfterRetryExhaustion)' -count=1
```

Expected: PASS and existing non-leader-transfer failure test still expects `TaskStatusFailed`.

- [ ] **Step 7: Commit Task 4**

Run:

```bash
git add pkg/controller/plane/statemachine.go pkg/controller/plane/types.go pkg/controller/plane/controller_test.go pkg/controller/meta/store.go
git commit -m "feat: self-clear slot leader transfer tasks"
```

Expected: commit succeeds.

---

### Task 5: Cluster Reconciler and Executor Leader Transfer

**Files:**
- Modify: `pkg/cluster/agent.go`
- Modify: `pkg/cluster/reconciler.go`
- Modify: `pkg/cluster/slot_executor.go`
- Modify: `pkg/cluster/slot_manager.go` if helper validation belongs there
- Test: `pkg/cluster/slot_executor_test.go`
- Test: `pkg/cluster/reconciler_test.go`

- [ ] **Step 1: Add failing executor test for direct leader transfer**

In `pkg/cluster/slot_executor_test.go`, add:

```go
func TestSlotExecutorExecuteLeaderTransferOnlyTransfersLeadership(t *testing.T) {
    cluster := &Cluster{}
    var transferred bool
    executor := newSlotExecutorWithFuncs(cluster, slotExecutorFuncs{
        prepareSlot: func(multiraft.SlotID, []uint64) { t.Fatal("prepareSlot should not be called for leader transfer") },
        transferLeadership: func(_ context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
            require.Equal(t, multiraft.SlotID(1), slotID)
            require.Equal(t, multiraft.NodeID(2), target)
            transferred = true
            return nil
        },
        waitForSpecificLeader: func(_ context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
            require.Equal(t, multiraft.SlotID(1), slotID)
            require.Equal(t, multiraft.NodeID(2), target)
            return nil
        },
    })

    err := executor.Execute(context.Background(), assignmentTaskState{
        assignment: controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}},
        runtimeView: controllermeta.SlotRuntimeView{SlotID: 1, LeaderID: 1, CurrentVoters: []uint64{1, 2, 3}},
        hasRuntimeView: true,
        nodes: map[uint64]controllermeta.ClusterNode{
            2: {NodeID: 2, Role: controllermeta.NodeRoleData, JoinState: controllermeta.NodeJoinStateActive, Status: controllermeta.NodeStatusAlive},
        },
        task: controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader, TargetNode: 2},
    })
    require.NoError(t, err)
    require.True(t, transferred)
}
```

If `slotExecutorFuncs` does not yet have those fields, this test should fail at compile time.

- [ ] **Step 2: Extend executor dependencies and assignment task state**

In `pkg/cluster/agent.go`:

```go
type assignmentTaskState struct {
    assignment  controllermeta.SlotAssignment
    runtimeView controllermeta.SlotRuntimeView
    hasRuntimeView bool
    nodes       map[uint64]controllermeta.ClusterNode
    task        controllermeta.ReconcileTask
}
```

In `pkg/cluster/slot_executor.go`, add function fields:

```go
transferLeadership func(context.Context, multiraft.SlotID, multiraft.NodeID) error
waitForSpecificLeader func(context.Context, multiraft.SlotID, multiraft.NodeID) error
```

Default them to `cluster.managedSlots().transferLeadership` and a new `waitForSpecificLeader` helper.

- [ ] **Step 3: Implement leader-transfer execution safety checks**

Add sentinel error in `pkg/cluster/errors.go` or `slot_executor.go`:

```go
var ErrLeaderTransferSafetyCheck = errors.New("raftcluster: leader transfer safety check failed")
```

In `slotExecutor.Execute`:

```go
case controllermeta.TaskKindLeaderTransfer:
    if !assignment.hasRuntimeView || assignment.runtimeView.LeaderID == 0 || len(assignment.runtimeView.CurrentVoters) == 0 {
        return fmt.Errorf("%w: leader or current voters unknown", ErrLeaderTransferSafetyCheck)
    }
    if !assignmentContainsPeer(assignment.assignment.DesiredPeers, assignment.task.TargetNode) {
        return fmt.Errorf("%w: target not in desired peers", ErrLeaderTransferSafetyCheck)
    }
    if !assignmentContainsPeer(assignment.runtimeView.CurrentVoters, assignment.task.TargetNode) {
        return fmt.Errorf("%w: target not in current voters", ErrLeaderTransferSafetyCheck)
    }
    node, ok := assignment.nodes[assignment.task.TargetNode]
    if !ok || !controllerNodeEligibleForLeaderTransfer(node) {
        return fmt.Errorf("%w: target node not eligible", ErrLeaderTransferSafetyCheck)
    }
    if assignment.runtimeView.LeaderID == assignment.task.TargetNode {
        return nil
    }
    if err := e.transferLeadership(ctx, slotID, multiraft.NodeID(assignment.task.TargetNode)); err != nil {
        return err
    }
    return e.waitForSpecificLeader(ctx, slotID, multiraft.NodeID(assignment.task.TargetNode))
```

Do not run `prepareSlot`, add learner, promote, or remove voter for this kind.

Add the local node eligibility helper in `pkg/cluster/slot_executor.go` or `pkg/cluster/reconciler.go`:

```go
func controllerNodeEligibleForLeaderTransfer(node controllermeta.ClusterNode) bool {
    return node.Status == controllermeta.NodeStatusAlive &&
        node.Role == controllermeta.NodeRoleData &&
        node.JoinState == controllermeta.NodeJoinStateActive
}
```

Also add executor tests that return `ErrLeaderTransferSafetyCheck` and do not call `transferLeadership` when the target node is missing, dead, suspect, draining, joining, or controller-only.

- [ ] **Step 4: Implement `waitForSpecificLeader` helper**

In `pkg/cluster/slot_manager.go`:

```go
func (m *slotManager) waitForSpecificLeader(ctx context.Context, slotID multiraft.SlotID, target multiraft.NodeID) error {
    deadline := time.Now().Add(m.cluster.timeoutConfig().ManagedSlotLeaderMove)
    for time.Now().Before(deadline) {
        leaderID, err := m.currentLeader(slotID)
        if err == nil && leaderID == target {
            return nil
        }
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-time.After(m.cluster.managedSlotLeaderMovePollInterval()):
        }
    }
    return ErrLeaderNotStable
}
```

- [ ] **Step 5: Run executor tests**

Run:

```bash
go test ./pkg/cluster -run 'TestSlotExecutorExecuteLeaderTransfer|TestSlotExecutorExecuteRepairAndRebalanceStepOrder|TestSlotExecutorExecuteBootstrapTransfersLeaderToTarget' -count=1
```

Expected: PASS.

- [ ] **Step 6: Write failing reconciler ownership tests**

In `pkg/cluster/reconciler_test.go`, add tests with fixture setup that creates an assignment, a runtime view, a task, and a fake execution hook:

```go
func TestReconcilerLeaderTransferExecutesOnCurrentLeader(t *testing.T) {
    fixture := newReconcilerFixture(t, 1,
        withNodes(aliveDataNode(1), aliveDataNode(2), aliveDataNode(3)),
        withAssignment(1, []uint64{1, 2, 3}),
        withRuntimeView(1, 1, []uint64{1, 2, 3}),
        withTask(controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader, TargetNode: 2}),
    )
    fixture.expectExecute(controllermeta.TaskKindLeaderTransfer)

    require.NoError(t, fixture.reconciler.Tick(context.Background()))
    require.Equal(t, 1, fixture.executeCalls())
}

func TestReconcilerLeaderTransferUnknownLeaderUsesDeterministicChecker(t *testing.T) {
    fixture := newReconcilerFixture(t, 1,
        withNodes(aliveDataNode(1), aliveDataNode(2), aliveDataNode(3)),
        withAssignment(1, []uint64{1, 2, 3}),
        withRuntimeView(1, 0, nil),
        withTask(controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader, TargetNode: 2}),
    )
    fixture.expectExecuteError(ErrLeaderTransferSafetyCheck)

    require.NoError(t, fixture.reconciler.Tick(context.Background()))
    require.Equal(t, 1, fixture.taskResultCalls())
}

func TestReconcilerLeaderTransferDoesNotExecuteOnNonLeaderWhenLeaderKnown(t *testing.T) {
    fixture := newReconcilerFixture(t, 2,
        withNodes(aliveDataNode(1), aliveDataNode(2), aliveDataNode(3)),
        withAssignment(1, []uint64{1, 2, 3}),
        withRuntimeView(1, 1, []uint64{1, 2, 3}),
        withTask(controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader, TargetNode: 3}),
    )

    require.NoError(t, fixture.reconciler.Tick(context.Background()))
    require.Zero(t, fixture.executeCalls())
}

func TestReconcilerLeaderTransferKnownDeadLeaderFallsBackToChecker(t *testing.T) {
    fixture := newReconcilerFixture(t, 2,
        withNodes(deadDataNode(1), aliveDataNode(2), aliveDataNode(3)),
        withAssignment(1, []uint64{1, 2, 3}),
        withRuntimeView(1, 1, []uint64{1, 2, 3}),
        withTask(controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindLeaderTransfer, Step: controllermeta.TaskStepTransferLeader, TargetNode: 3}),
    )
    fixture.expectExecuteError(ErrLeaderTransferSafetyCheck)

    require.NoError(t, fixture.reconciler.Tick(context.Background()))
    require.Equal(t, 1, fixture.taskResultCalls())
}
```

Assert that the execution hook receives `TaskKindLeaderTransfer` only on the intended node/checker.

- [ ] **Step 7: Update `shouldExecuteTask` for leader transfer**

In `pkg/cluster/reconciler.go`:

```go
case controllermeta.TaskKindLeaderTransfer:
    view, hasView := viewByGroup[assignment.SlotID]
    if hasView && leaderTransferExecutorEligible(assignment, view, nodes, view.LeaderID) {
        return view.LeaderID == localNodeID
    }
    return deterministicLeaderTransferChecker(assignment.DesiredPeers, nodes) == localNodeID
```

If the method signature cannot access `viewByGroup`, split leader-transfer ownership into a new method called from the main loop where `view` is available.

Add ownership helper:

```go
func leaderTransferExecutorEligible(assignment controllermeta.SlotAssignment, view controllermeta.SlotRuntimeView, nodes map[uint64]controllermeta.ClusterNode, nodeID uint64) bool {
    if nodeID == 0 || !assignmentContainsPeer(assignment.DesiredPeers, nodeID) {
        return false
    }
    if len(view.CurrentVoters) > 0 && !assignmentContainsPeer(view.CurrentVoters, nodeID) {
        return false
    }
    node, ok := nodes[nodeID]
    return ok && controllerNodeEligibleForLeaderTransfer(node)
}
```

The deterministic checker should pick the lowest alive active data desired peer, falling back to the lowest desired peer only so stale tasks can report a retryable safety failure rather than pending forever. Use the checker whenever the observed leader is unknown or not eligible/executable, otherwise a stale non-zero `LeaderID` can strand the task forever.

- [ ] **Step 8: Pass runtime view into executor**

When constructing `assignmentTaskState` in `reconciler.Tick`:

```go
execErr := a.cluster.executeReconcileTask(ctx, assignmentTaskState{
    assignment: assignment,
    runtimeView: view,
    hasRuntimeView: hasView,
    nodes: nodeByID,
    task: task,
})
```

Ensure unknown view on the checker returns `ErrLeaderTransferSafetyCheck`, causing `reportTaskResult` to advance retry state.

- [ ] **Step 9: Run reconciler tests**

Run:

```bash
go test ./pkg/cluster -run 'TestReconcilerLeaderTransfer|TestSlotExecutorExecuteLeaderTransfer' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit Task 5**

Run:

```bash
git add pkg/cluster/agent.go pkg/cluster/reconciler.go pkg/cluster/slot_executor.go pkg/cluster/slot_manager.go pkg/cluster/errors.go pkg/cluster/slot_executor_test.go pkg/cluster/reconciler_test.go
git commit -m "feat: execute slot leader transfer tasks"
```

Expected: commit succeeds.

---

### Task 6: Cluster Operator AddSlot and Planner Wiring

**Files:**
- Modify: `pkg/cluster/operator.go`
- Modify: `pkg/cluster/codec_control.go`
- Test: `pkg/cluster/operator_test.go`
- Test: `pkg/cluster/codec_control_test.go`

- [ ] **Step 1: Write failing AddSlot request codec test**

In `pkg/cluster/codec_control_test.go`, add:

```go
func TestControllerCodecAddSlotRequestRoundTripPreferredLeader(t *testing.T) {
    body, err := encodeControllerRequest(controllerRPCRequest{
        Kind: controllerRPCAddSlot,
        AddSlot: &slotcontroller.AddSlotRequest{NewSlotID: 4, Peers: []uint64{1, 2, 3}, PreferredLeader: 2},
    })
    require.NoError(t, err)

    got, err := decodeControllerRequest(body)
    require.NoError(t, err)
    require.Equal(t, uint64(2), got.AddSlot.PreferredLeader)
}
```

- [ ] **Step 2: Update AddSlot RPC/control codec**

In `pkg/cluster/codec_control.go`, encode/decode `AddSlotRequest.PreferredLeader` after peers. Decode old payloads with preferred leader zero for wire compatibility only; the state machine rejects zero so old clients fail closed instead of persisting an unbalanced leader.

Run:

```bash
go test ./pkg/cluster -run TestControllerCodecAddSlotRequestRoundTripPreferredLeader -count=1
```

Expected: PASS.

- [ ] **Step 3: Write failing operator AddSlot preferred leader test**

In `pkg/cluster/operator_test.go`, add or extend tests around `nextSlotDefinition`:

```go
func TestNextSlotDefinitionChoosesBalancedPreferredLeaderFromActualRuntime(t *testing.T) {
    c := &Cluster{}
    assignments := []controllermeta.SlotAssignment{
        {SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2},
        {SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 3},
    }
    views := []controllermeta.SlotRuntimeView{
        {SlotID: 1, CurrentVoters: []uint64{1, 2, 3}, LeaderID: 1, HasQuorum: true},
        {SlotID: 2, CurrentVoters: []uint64{1, 2, 3}, LeaderID: 1, HasQuorum: true},
    }

    slotID, peers, preferred, err := nextSlotDefinition(c, assignments, views)
    require.NoError(t, err)
    require.Equal(t, multiraft.SlotID(3), slotID)
    require.Equal(t, []uint64{1, 2, 3}, peers)
    require.NotEqual(t, uint64(1), preferred)
}

func TestNextSlotDefinitionFallsBackToPreferredLeaderWhenRuntimeMissing(t *testing.T) {
    c := &Cluster{}
    assignments := []controllermeta.SlotAssignment{
        {SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1},
        {SlotID: 2, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 1},
    }

    _, _, preferred, err := nextSlotDefinition(c, assignments, nil)
    require.NoError(t, err)
    require.NotEqual(t, uint64(1), preferred)
}
```

- [ ] **Step 4: Implement operator preferred leader selection**

Update `AddSlot` to read strict assignments and strict runtime observations before building the request:

```go
assignments, err := c.ListSlotAssignmentsStrict(ctx)
if err != nil {
    return 0, err
}
views, err := c.ListObservedRuntimeViewsStrict(ctx)
if err != nil {
    return 0, err
}
newSlotID, peers, preferred, err := nextSlotDefinition(c, assignments, views)
if err != nil {
    return 0, err
}
```

Do not use `listSlotAssignmentsForOperator` for AddSlot anymore. AddSlot is a controller write path; stale assignment fallback can choose a wrong `newSlotID`, peer set, or leader-load count.

Update `nextSlotDefinition` to return `(slotID, peers, preferredLeader, error)`:

```go
func nextSlotDefinition(c *Cluster, assignments []controllermeta.SlotAssignment, views []controllermeta.SlotRuntimeView) (multiraft.SlotID, []uint64, uint64, error) {
    // keep existing slotID/peer selection
    preferred := chooseAddSlotPreferredLeader(assignments, views, peers)
    return multiraft.SlotID(maxSlotID + 1), peers, preferred, nil
}

req := slotcontroller.AddSlotRequest{NewSlotID: uint64(newSlotID), Peers: peers, PreferredLeader: preferred}
```

`chooseAddSlotPreferredLeader` must use the bootstrap leader-load policy:

```go
func chooseAddSlotPreferredLeader(assignments []controllermeta.SlotAssignment, views []controllermeta.SlotRuntimeView, peers []uint64) uint64 {
    viewBySlot := make(map[uint32]controllermeta.SlotRuntimeView, len(views))
    for _, view := range views {
        viewBySlot[view.SlotID] = view
    }
    loads := make(map[uint64]int)
    for _, assignment := range assignments {
        if view, ok := viewBySlot[assignment.SlotID]; ok && view.LeaderID != 0 {
            loads[view.LeaderID]++
            continue
        }
        if assignment.PreferredLeader != 0 {
            loads[assignment.PreferredLeader]++
        }
    }
    return chooseLowestLoadedPeer(peers, loads)
}
```

Do not fall back to non-strict local runtime caches for this write path; returning `ErrObservationNotReady` is safer than choosing from stale leader counts.

- [ ] **Step 5: Run operator tests**

Run:

```bash
go test ./pkg/cluster -run 'TestNextSlotDefinition(ChoosesBalancedPreferredLeaderFromActualRuntime|FallsBackToPreferredLeaderWhenRuntimeMissing)|TestAddSlot|TestControllerCodecAddSlotRequestRoundTripPreferredLeader' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 6**

Run:

```bash
git add pkg/cluster/operator.go pkg/cluster/operator_test.go pkg/cluster/codec_control.go pkg/cluster/codec_control_test.go
git commit -m "feat: choose preferred leader for added slots"
```

Expected: commit succeeds.

---

### Task 7: Manager Visibility, Task Naming, and Metrics

**Files:**
- Modify: `internal/usecase/management/slots.go`
- Modify: `internal/usecase/management/tasks.go`
- Test: `internal/usecase/management/slots_test.go` or existing management tests
- Modify: `internal/access/manager/slots.go`
- Modify: `internal/access/manager/tasks.go`
- Test: `internal/access/manager/server_test.go`
- Modify: `pkg/cluster/cluster.go`
- Test: `pkg/cluster/observer_hooks_test.go`
- Modify: `pkg/metrics/controller.go`
- Test: `pkg/metrics/registry_test.go`
- Modify: `internal/app/observability.go`
- Test: `internal/app/observability_test.go`

- [ ] **Step 1: Write failing management slot mapping test**

Add or extend a test in `internal/usecase/management/slots_test.go`:

```go
func TestSlotFromAssignmentViewIncludesPreferredLeaderAndVoters(t *testing.T) {
    assignment := controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1, 2, 3}, PreferredLeader: 2}
    view := controllermeta.SlotRuntimeView{SlotID: 1, CurrentPeers: []uint64{1, 2, 3}, CurrentVoters: []uint64{1, 2, 3}, LeaderID: 1, HasQuorum: true}

    got := slotFromAssignmentView(assignment, view, true)
    require.Equal(t, uint64(2), got.Assignment.PreferredLeader)
    require.Equal(t, []uint64{1, 2, 3}, got.Runtime.CurrentVoters)
    require.False(t, got.State.LeaderMatch)
}
```

If `SlotState.LeaderMatch` is a string instead of bool in implementation, assert the chosen field exactly.

- [ ] **Step 2: Add management fields**

In `internal/usecase/management/slots.go`:

```go
type SlotState struct {
    Quorum string
    Sync string
    LeaderMatch bool
    LeaderTransferPending bool
}

type SlotAssignment struct {
    DesiredPeers []uint64
    PreferredLeader uint64
    ConfigEpoch uint64
    BalanceVersion uint64
}

type SlotRuntime struct {
    CurrentPeers []uint64
    CurrentVoters []uint64
    LeaderID uint64
    // existing fields
}
```

Set `LeaderMatch` to `assignment.PreferredLeader != 0 && assignment.PreferredLeader == view.LeaderID`. If preferred leader is zero, use `false` and let UI show unknown.

Use task data in `GetSlot` to set `LeaderTransferPending` when current task kind is `leader_transfer`.

- [ ] **Step 3: Add task kind string mapping**

In management task mapping code:

```go
case controllermeta.TaskKindLeaderTransfer:
    return "leader_transfer"
```

Do this anywhere task kind is converted to strings.

- [ ] **Step 4: Add manager DTO fields and tests**

In `internal/access/manager/slots.go`:

```go
type SlotStateDTO struct {
    Quorum string `json:"quorum"`
    Sync string `json:"sync"`
    LeaderMatch bool `json:"leader_match"`
    LeaderTransferPending bool `json:"leader_transfer_pending"`
}

type SlotAssignmentDTO struct {
    DesiredPeers []uint64 `json:"desired_peers"`
    PreferredLeaderID uint64 `json:"preferred_leader_id"`
    ConfigEpoch uint64 `json:"config_epoch"`
    BalanceVersion uint64 `json:"balance_version"`
}

type SlotRuntimeDTO struct {
    CurrentPeers []uint64 `json:"current_peers"`
    CurrentVoters []uint64 `json:"current_voters"`
    LeaderID uint64 `json:"leader_id"`
    // existing fields
}
```

Update `internal/access/manager/server_test.go` expected JSON for slot list/detail.

- [ ] **Step 5: Add controller task names and leader-transfer metric labels**

In `pkg/cluster/cluster.go`, include the new task kind in observer names:

```go
func controllerTaskKindName(kind controllermeta.TaskKind) string {
    switch kind {
    case controllermeta.TaskKindBootstrap:
        return "bootstrap"
    case controllermeta.TaskKindRepair:
        return "repair"
    case controllermeta.TaskKindRebalance:
        return "rebalance"
    case controllermeta.TaskKindLeaderTransfer:
        return "leader_transfer"
    default:
        return "unknown"
    }
}

func controllerTaskResult(err error) string {
    switch {
    case err == nil:
        return "ok"
    case errors.Is(err, context.DeadlineExceeded):
        return "timeout"
    case errors.Is(err, ErrLeaderTransferSafetyCheck):
        return "safety_check"
    default:
        return "fail"
    }
}
```

In `pkg/metrics/controller.go`:

```go
var (
    controllerTaskKinds   = []string{"bootstrap", "repair", "rebalance", "leader_transfer"}
    controllerTaskResults = []string{"ok", "fail", "timeout", "safety_check"}
)

type ControllerMetrics struct {
    // existing fields
    slotLeaderSkew prometheus.Gauge
}

func (m *ControllerMetrics) SetSlotLeaderSkew(skew int) {
    if m == nil {
        return
    }
    m.slotLeaderSkew.Set(float64(skew))
}
```

Register gauge:

```go
slotLeaderSkew: prometheus.NewGauge(prometheus.GaugeOpts{
    Name:        "wukongim_controller_slot_leader_skew",
    Help:        "Max-minus-min Slot leader count skew across active data nodes.",
    ConstLabels: labels,
}),
```

This covers the spec metrics as:

- requested transfers: `wukongim_controller_decisions_total{type="leader_transfer"}`;
- successful transfers: `wukongim_controller_tasks_completed_total{type="leader_transfer",result="ok"}`;
- failed transfers by reason: the same completed counter with `result="timeout"`, `result="safety_check"`, or `result="fail"`;
- skew: `wukongim_controller_slot_leader_skew`.

- [ ] **Step 6: Add leader skew refresh**

In `internal/app/observability.go`, update task kind mapping and metrics refresh:

```go
func controllerTaskKind(kind controllermeta.TaskKind) string {
    switch kind {
    case controllermeta.TaskKindBootstrap:
        return "bootstrap"
    case controllermeta.TaskKindRepair:
        return "repair"
    case controllermeta.TaskKindRebalance:
        return "rebalance"
    case controllermeta.TaskKindLeaderTransfer:
        return "leader_transfer"
    default:
        return "unknown"
    }
}

func (a *App) refreshControllerMetrics() {
    // existing counts
    a.metrics.Controller.SetSlotLeaderSkew(slotLeaderSkew(clusterState.nodes, clusterState.views))
}
```

Add helper:

```go
func slotLeaderSkew(nodes []controllermeta.ClusterNode, views []controllermeta.SlotRuntimeView) int {
    activeData := make(map[uint64]int)
    for _, node := range nodes {
        if node.Status == controllermeta.NodeStatusAlive &&
            node.Role == controllermeta.NodeRoleData &&
            node.JoinState == controllermeta.NodeJoinStateActive {
            activeData[node.NodeID] = 0
        }
    }
    if len(activeData) == 0 {
        return 0
    }
    for _, view := range views {
        if _, ok := activeData[view.LeaderID]; ok && view.LeaderID != 0 {
            activeData[view.LeaderID]++
        }
    }
    min, max := -1, 0
    for _, count := range activeData {
        if min == -1 || count < min {
            min = count
        }
        if count > max {
            max = count
        }
    }
    return max - min
}
```

- [ ] **Step 7: Add metrics and observer tests**

Add or extend tests:

```go
func TestControllerMetricsIncludeLeaderTransferAndLeaderSkew(t *testing.T) {
    reg := New(1, "node-1")
    reg.Controller.ObserveDecision("leader_transfer", time.Millisecond)
    reg.Controller.ObserveTaskCompleted("leader_transfer", "safety_check")
    reg.Controller.SetTaskActive(map[string]int{"leader_transfer": 1})
    reg.Controller.SetSlotLeaderSkew(2)

    families, err := reg.Gather()
    require.NoError(t, err)
    completed := requireMetricFamily(t, families, "wukongim_controller_tasks_completed_total")
    requireMetricLabels(t, findMetricByLabels(t, completed, map[string]string{"type": "leader_transfer", "result": "safety_check"}), map[string]string{"type": "leader_transfer", "result": "safety_check"})
    skew := requireMetricFamily(t, families, "wukongim_controller_slot_leader_skew")
    require.Equal(t, float64(2), skew.GetMetric()[0].GetGauge().GetValue())
}

func TestControllerTaskKindNameLeaderTransfer(t *testing.T) {
    require.Equal(t, "leader_transfer", controllerTaskKindName(controllermeta.TaskKindLeaderTransfer))
}

func TestSlotLeaderSkewCountsActiveDataNodesWithZeroLeaders(t *testing.T) {
    nodes := []controllermeta.ClusterNode{aliveDataNode(1), aliveDataNode(2), aliveDataNode(3)}
    views := []controllermeta.SlotRuntimeView{{SlotID: 1, LeaderID: 1}, {SlotID: 2, LeaderID: 1}, {SlotID: 3, LeaderID: 2}}
    require.Equal(t, 2, slotLeaderSkew(nodes, views))
}
```

- [ ] **Step 8: Run manager and observability tests**

Run:

```bash
go test ./internal/usecase/management ./internal/access/manager ./pkg/metrics ./internal/app ./pkg/cluster -run 'Test.*Slot|Test.*Task|TestControllerMetricsIncludeLeaderTransferAndLeaderSkew|TestControllerTaskKindNameLeaderTransfer|TestSlotLeaderSkewCountsActiveDataNodesWithZeroLeaders|TestObserverHooksOnTaskResult' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 7**

Run:

```bash
git add internal/usecase/management/slots.go internal/usecase/management/tasks.go internal/usecase/management/*_test.go internal/access/manager/slots.go internal/access/manager/tasks.go internal/access/manager/server_test.go pkg/cluster/cluster.go pkg/cluster/observer_hooks_test.go pkg/metrics/controller.go pkg/metrics/registry_test.go internal/app/observability.go internal/app/observability_test.go
git commit -m "feat: expose slot preferred leader status"
```

Expected: commit succeeds.

---

### Task 8: Integration Tests, Flow Docs, and Final Verification

**Files:**
- Modify: `pkg/cluster/cluster_integration_test.go`
- Modify: `pkg/cluster/FLOW.md`
- Modify: `pkg/controller/FLOW.md`
- Modify when `multiraft.Status` semantics are documented there: `pkg/slot/FLOW.md`
- Modify when the durable rule is not already recorded: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Write integration test for initial leader distribution**

In `pkg/cluster/cluster_integration_test.go`, add:

```go
func TestClusterInitialSlotLeadersAreNearEven(t *testing.T) {
    nodes := startThreeNodesWithController(t, 9, 3)
    defer stopNodes(nodes)

    leaders := waitForAllStableLeaders(t, nodes, 9)
    counts := map[multiraft.NodeID]int{}
    for _, leader := range leaders {
        counts[leader]++
    }
    require.LessOrEqual(t, maxLeaderCount(counts)-minLeaderCount(counts, []multiraft.NodeID{1, 2, 3}), 1)
}
```

Use existing helper naming and keep this test under the integration tag rather than the normal unit suite.

- [ ] **Step 2: Write integration test for restart convergence**

Add:

```go
func TestClusterSlotLeaderReturnsTowardPreferredAfterRestartDrift(t *testing.T) {
    nodes := startThreeNodesWithController(t, 3, 3)
    defer stopNodes(nodes)

    assignments := snapshotAssignments(t, nodes, 3)
    preferredBySlot := map[uint32]uint64{}
    for _, assignment := range assignments {
        preferredBySlot[assignment.SlotID] = assignment.PreferredLeader
    }

    for slotID, preferred := range preferredBySlot {
        if preferred == 0 {
            t.Fatalf("slot %d missing preferred leader", slotID)
        }
    }

    // Restart one current leader and wait for controller to converge without losing quorum.
    slotID := uint64(1)
    oldLeader := waitForStableLeader(t, nodes, slotID)
    restartNode(t, nodes, int(oldLeader-1))
    waitForStableLeader(t, nodes, slotID)

    require.Eventually(t, func() bool {
        leader := waitForStableLeader(t, nodes, slotID)
        return leader == multiraft.NodeID(preferredBySlot[uint32(slotID)]) || clusterLeaderSkewWithin(t, nodes, 3, 1)
    }, 30*time.Second, 200*time.Millisecond)
}
```

This test belongs under the integration tag because it restarts nodes and waits for asynchronous controller convergence.

- [ ] **Step 3: Write single-node no-transfer integration/unit test**

Add this unit planner test:

```go
func TestPlannerLeaderRebalanceSkipsSingleVoterSlot(t *testing.T) {
    planner := NewPlanner(PlannerConfig{SlotCount: 1, ReplicaN: 1, LeaderSkewThreshold: 1})
    state := testState(
        aliveNode(1),
        withAssignment(1, 1), withPreferredLeader(1, 1),
        withRuntimeViewVoters(1, []uint64{1}, 1, true),
    )

    decision, err := planner.NextDecision(context.Background(), state)
    require.NoError(t, err)
    require.Nil(t, decision.Task)
}
```

Also add this integration test when the cluster harness already has task-list helpers:

```go
func TestSingleNodeClusterDoesNotCreateLeaderTransferTasks(t *testing.T) {
    node := startSingleNodeCluster(t, 3)
    defer node.stop()

    require.Eventually(t, func() bool {
        tasks := listTasks(t, node)
        for _, task := range tasks {
            if task.Kind == controllermeta.TaskKindLeaderTransfer {
                return false
            }
        }
        return true
    }, 5*time.Second, 100*time.Millisecond)
}
```

- [ ] **Step 4: Run focused integration tests**

Run:

```bash
go test -tags=integration ./pkg/cluster -run 'TestClusterInitialSlotLeadersAreNearEven|TestClusterSlotLeaderReturnsTowardPreferredAfterRestartDrift|TestSingleNodeClusterDoesNotCreateLeaderTransferTasks' -count=1
```

Expected: PASS. If integration runtime is too long for the development loop, run the unit alternatives in this plan and record the exact skipped integration command in the final handoff.

- [ ] **Step 5: Update FLOW docs**

Update `pkg/controller/FLOW.md`:

- `SlotAssignment` now has `PreferredLeader` and `LeaderTransferCooldownUntil`.
- Planner has a post-replica-rebalance leader-placement path.
- `TaskKindLeaderTransfer` terminal failures self-clear with cooldown.

Update `pkg/cluster/FLOW.md`:

- Runtime observation includes `CurrentVoters`.
- Reconciler executes leader-transfer tasks only with known leader/current voters; deterministic checker reports retryable safety failures.
- Observer task names and controller metrics include `leader_transfer`, safety-check failures, and Slot leader skew.

Update `pkg/slot/FLOW.md` only if it documents `multiraft.Status`; add that status includes current voters.

- [ ] **Step 6: Add project knowledge note if useful**

If implementation confirms the durable rule, append one short bullet to `docs/development/PROJECT_KNOWLEDGE.md`:

```markdown
- Slot preferred leaders are controller soft intent. Raft remains authoritative; leader-transfer tasks must target observed current voters only.
```

Do not add this if the same rule is already present.

- [ ] **Step 7: Run full targeted test suite**

Run:

```bash
go test ./pkg/controller/meta ./pkg/controller/plane ./pkg/slot/multiraft ./pkg/cluster ./pkg/metrics ./internal/app ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: PASS.

- [ ] **Step 8: Run full unit suite if time allows**

Run:

```bash
go test ./...
```

Expected: PASS. If this is too slow or fails in unrelated packages, record exact failing package/output before handoff.

- [ ] **Step 9: Commit Task 8**

Run:

```bash
git add pkg/cluster/cluster_integration_test.go pkg/cluster/FLOW.md pkg/controller/FLOW.md pkg/slot/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "test: cover slot preferred leader convergence"
```

Expected: commit succeeds. If no docs/project-knowledge changes were needed, omit those paths.

- [ ] **Step 10: Final verification before completion**

Run:

```bash
git status --short
go test ./pkg/controller/meta ./pkg/controller/plane ./pkg/slot/multiraft ./pkg/cluster ./pkg/metrics ./internal/app ./internal/usecase/management ./internal/access/manager -count=1
```

Expected: clean worktree except intended commits, and tests PASS.
