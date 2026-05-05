# Controller Raft Log Compaction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build default-on Controller Raft snapshot compaction with safe restart restore and minimum `raft.Ready.Snapshot` handling.

**Architecture:** Controller metadata owns snapshot bytes via `pkg/controller/plane.StateMachine`; Controller Raft owns compaction timing, Raft snapshot metadata, startup restore, and Ready snapshot restore; `pkg/raftlog` persists snapshots and trims covered entries. Config flows from `cmd/wukongim` through `internal/app` and `pkg/cluster` into `pkg/controller/raft`, preserving explicit `Enabled=false`.

**Tech Stack:** Go, etcd raft `RawNode`/`MemoryStorage`, Pebble-backed `pkg/raftlog`, Controller metadata snapshots, `go test`.

---

## Spec And Risk Context

Read first:

- `docs/superpowers/specs/2026-05-05-controller-log-compaction-design.md`
- `pkg/controller/FLOW.md`
- `pkg/controller/raft/service.go`
- `pkg/controller/raft/config.go`
- `pkg/controller/plane/statemachine.go`
- `pkg/controller/meta/snapshot.go`
- `pkg/raftlog/pebble_writer.go`

Critical constraints from review:

- When a persisted snapshot exists, start `RawNode` with `Applied=snapshot.Metadata.Index`, not with the persisted `AppliedIndex`, so post-snapshot committed entries replay into Controller metadata.
- Handle `ready.Snapshot` before advancing Raft: persist snapshot, restore state machine, mark the snapshot index applied, then continue normal committed entry application.
- Run local compaction only after `rawNode.Advance(ready)`.
- Persist local snapshot through `raftlog.Save(PersistentState{Snapshot})` before mutating `MemoryStorage` with `CreateSnapshot`/`Compact`.
- Track latest applied conf state from `rawNode.ApplyConfChange`, including `EntryConfChangeV2`.
- Do not fail proposals or stop the raft loop on local compaction failures; do fail on normal apply/restore/MarkApplied errors.
- Preserve unrelated dirty worktree changes.

## File Structure

- Modify `pkg/controller/plane/statemachine.go`: add snapshot/restore methods around `controllermeta.Store`.
- Modify `pkg/controller/plane/controller_test.go` or create `pkg/controller/plane/statemachine_snapshot_test.go`: state-machine snapshot tests.
- Modify `pkg/raftlog/pebble_test.go`: controller-scope snapshot trim coverage.
- Modify `pkg/controller/raft/config.go`: compaction config type, defaults, validation helpers.
- Modify `pkg/controller/raft/service.go`: startup restore, Ready snapshot restore, conf-state tracking, compaction loop.
- Modify `pkg/controller/raft/service_test.go`: Controller Raft compaction/recovery tests.
- Modify `pkg/cluster/config.go`: cluster-level compaction config and defaults.
- Modify `pkg/cluster/controller_host.go`: pass compaction config into controller service; add a package-local `newControllerRaftService` factory var for safe pass-through tests without exporting `controllerraft.Service` internals.
- Modify `pkg/cluster/config_test.go` and/or `pkg/cluster/controller_host_test.go`: cluster default/pass-through tests.
- Modify `internal/app/config.go`: app-level compaction config, explicit flags, defaults, validation.
- Modify `internal/app/build.go`: map app config to cluster config.
- Modify `internal/app/config_test.go`: config example key coverage and app default validation tests.
- Modify `cmd/wukongim/config.go`: parse new `WK_CLUSTER_CONTROLLER_LOG_COMPACTION_*` keys.
- Modify `cmd/wukongim/config_test.go`: config parsing and env override tests.
- Modify `wukongim.conf.example`: document new keys with English comments.
- Modify `pkg/controller/FLOW.md`: document snapshot compaction support.
- Optionally modify `docs/development/PROJECT_KNOWLEDGE.md`: add concise Controller Raft compaction rule if implementation reveals durable knowledge.

---

### Task 1: Add Controller State-Machine Snapshot API

**Files:**
- Modify: `pkg/controller/plane/statemachine.go`
- Create: `pkg/controller/plane/statemachine_snapshot_test.go`

- [ ] **Step 1: Write failing snapshot/restore round-trip test**

Create `pkg/controller/plane/statemachine_snapshot_test.go`:

```go
package plane

import (
    "context"
    "path/filepath"
    "testing"
    "time"

    "github.com/WuKongIM/WuKongIM/pkg/cluster/hashslot"
    controllermeta "github.com/WuKongIM/WuKongIM/pkg/controller/meta"
    "github.com/stretchr/testify/require"
)

func TestStateMachineSnapshotRestoreRoundTrip(t *testing.T) {
    ctx := context.Background()
    source, err := controllermeta.Open(filepath.Join(t.TempDir(), "source"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, source.Close()) })

    now := time.Unix(100, 0)
    require.NoError(t, source.UpsertNode(ctx, controllermeta.ClusterNode{NodeID: 1, Addr: "127.0.0.1:7000", Status: controllermeta.NodeStatusAlive, JoinedAt: now, LastHeartbeatAt: now, CapacityWeight: 1}))
    require.NoError(t, source.UpsertAssignmentTask(ctx,
        controllermeta.SlotAssignment{SlotID: 1, DesiredPeers: []uint64{1}, ConfigEpoch: 2, PreferredLeader: 1},
        controllermeta.ReconcileTask{SlotID: 1, Kind: controllermeta.TaskKindBootstrap, Step: controllermeta.TaskStepAddLearner, TargetNode: 1, Status: controllermeta.TaskStatusPending},
    ))
    require.NoError(t, source.UpsertOnboardingJob(ctx, controllermeta.NodeOnboardingJob{
        JobID:            "job-1",
        TargetNodeID:     2,
        Status:           controllermeta.OnboardingJobStatusPlanned,
        CreatedAt:        now,
        UpdatedAt:        now,
        PlanVersion:      1,
        Plan:             controllermeta.NodeOnboardingPlan{TargetNodeID: 2},
        CurrentMoveIndex: -1,
    }))
    require.NoError(t, source.SaveHashSlotTable(ctx, hashslot.NewHashSlotTable(8, 1)))

    snap, err := NewStateMachine(source, StateMachineConfig{}).Snapshot(ctx)
    require.NoError(t, err)
    require.NotEmpty(t, snap)

    target, err := controllermeta.Open(filepath.Join(t.TempDir(), "target"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, target.Close()) })
    require.NoError(t, NewStateMachine(target, StateMachineConfig{}).Restore(ctx, snap))

    node, err := target.GetNode(ctx, 1)
    require.NoError(t, err)
    require.Equal(t, uint64(1), node.NodeID)
    assignment, err := target.GetAssignment(ctx, 1)
    require.NoError(t, err)
    require.Equal(t, []uint64{1}, assignment.DesiredPeers)
    task, err := target.GetTask(ctx, 1)
    require.NoError(t, err)
    require.Equal(t, controllermeta.TaskKindBootstrap, task.Kind)
    job, err := target.GetOnboardingJob(ctx, "job-1")
    require.NoError(t, err)
    require.Equal(t, uint64(2), job.TargetNodeID)
    table, err := target.LoadHashSlotTable(ctx)
    require.NoError(t, err)
    require.Equal(t, uint16(8), table.HashSlotCount())
}

func TestStateMachineRestoreRejectsCorruptSnapshot(t *testing.T) {
    store, err := controllermeta.Open(filepath.Join(t.TempDir(), "store"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, store.Close()) })

    err = NewStateMachine(store, StateMachineConfig{}).Restore(context.Background(), []byte("not-a-controller-snapshot"))
    require.Error(t, err)
}
```

If `HashSlotTable` does not expose `HashSlotCount()`, use the actual accessor from `pkg/cluster/hashslot` after inspecting the type.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/controller/plane -run 'TestStateMachineSnapshot|TestStateMachineRestore' -count=1`

Expected: FAIL because `StateMachine.Snapshot` and `StateMachine.Restore` do not exist.

- [ ] **Step 3: Implement snapshot/restore methods**

In `pkg/controller/plane/statemachine.go`, add methods near `Apply`:

```go
// Snapshot exports the durable Controller state represented by this state machine.
func (sm *StateMachine) Snapshot(ctx context.Context) ([]byte, error) {
    if sm == nil || sm.store == nil {
        return nil, controllermeta.ErrClosed
    }
    return sm.store.ExportSnapshot(ctx)
}

// Restore replaces the durable Controller state with a previously exported snapshot.
func (sm *StateMachine) Restore(ctx context.Context, data []byte) error {
    if sm == nil || sm.store == nil {
        return controllermeta.ErrClosed
    }
    return sm.store.ImportSnapshot(ctx, data)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/controller/plane -run 'TestStateMachineSnapshot|TestStateMachineRestore' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/controller/plane/statemachine.go pkg/controller/plane/statemachine_snapshot_test.go
git commit -m "feat(controller): add state machine snapshot restore"
```

If the worktree has unrelated user changes, do not include them in the commit.

---

### Task 2: Add Controller-Scope Raftlog Snapshot Coverage

**Files:**
- Modify: `pkg/raftlog/pebble_test.go`

- [ ] **Step 1: Write controller-scope trim test**

Append to `pkg/raftlog/pebble_test.go`:

```go
func TestPebbleControllerSnapshotTrimsCoveredEntries(t *testing.T) {
    ctx := context.Background()
    db, err := Open(filepath.Join(t.TempDir(), "raft"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, db.Close()) })

    store := db.ForController()
    require.NoError(t, store.Save(ctx, multiraft.PersistentState{
        HardState: &raftpb.HardState{Term: 3, Vote: 1, Commit: 8},
        Entries:   benchEntries(1, 8, 3, 8),
    }))
    snap := raftpb.Snapshot{Data: []byte("controller-meta"), Metadata: raftpb.SnapshotMetadata{Index: 6, Term: 3, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
    require.NoError(t, store.Save(ctx, multiraft.PersistentState{Snapshot: &snap}))

    first, err := store.FirstIndex(ctx)
    require.NoError(t, err)
    require.Equal(t, uint64(7), first)
    last, err := store.LastIndex(ctx)
    require.NoError(t, err)
    require.Equal(t, uint64(8), last)
    gotSnap, err := store.Snapshot(ctx)
    require.NoError(t, err)
    require.Equal(t, snap.Metadata.Index, gotSnap.Metadata.Index)

    entries, err := store.Entries(ctx, 7, 9, 0)
    require.NoError(t, err)
    require.Len(t, entries, 2)
}
```

Use the repository's existing assertion style in this file; if it does not use `require`, either add the import or convert assertions to `if` checks.

- [ ] **Step 2: Run test**

Run: `go test ./pkg/raftlog -run TestPebbleControllerSnapshotTrimsCoveredEntries -count=1`

Expected: PASS, proving no production change is needed for raftlog.

- [ ] **Step 3: Commit**

```bash
git add pkg/raftlog/pebble_test.go
git commit -m "test(raftlog): cover controller snapshot trimming"
```

---

### Task 3: Add Controller Raft Compaction Config Types

**Files:**
- Modify: `pkg/controller/raft/config.go`
- Modify: `pkg/controller/raft/service_test.go` or create `pkg/controller/raft/config_test.go`

- [ ] **Step 1: Write config default/validation tests**

Create `pkg/controller/raft/config_test.go` if no better home exists:

```go
package raft

import (
    "testing"
    "time"

    "github.com/stretchr/testify/require"
)

func TestNormalizeLogCompactionConfigDefaultsEnabled(t *testing.T) {
    got := NormalizeLogCompactionConfig(LogCompactionConfig{})
    require.True(t, got.Enabled)
    require.Equal(t, uint64(10000), got.TriggerEntries)
    require.Equal(t, 30*time.Second, got.CheckInterval)
}

func TestNormalizeLogCompactionConfigPreservesExplicitDisabled(t *testing.T) {
    got := NormalizeLogCompactionConfig(LogCompactionConfig{Enabled: false, EnabledSet: true})
    require.False(t, got.Enabled)
}

func TestValidateLogCompactionConfigRejectsEnabledInvalidValues(t *testing.T) {
    require.Error(t, ValidateLogCompactionConfig(LogCompactionConfig{Enabled: true, TriggerEntries: 0, CheckInterval: time.Second}))
    require.Error(t, ValidateLogCompactionConfig(LogCompactionConfig{Enabled: true, TriggerEntries: 1, CheckInterval: 0}))
}
```

If unexported explicit fields in an exported config type are not desirable, use exported `EnabledSet bool` or a constructor-style helper. Prefer a simple explicit flag because `Enabled bool` alone cannot distinguish unset from explicit false.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/controller/raft -run TestNormalizeLogCompactionConfig -count=1`

Expected: FAIL because config type/helper does not exist.

- [ ] **Step 3: Implement config type and helpers**

In `pkg/controller/raft/config.go`:

```go
const (
    defaultLogCompactionTriggerEntries = uint64(10000)
    defaultLogCompactionCheckInterval  = 30 * time.Second
)

// LogCompactionConfig controls local Controller Raft snapshot compaction.
type LogCompactionConfig struct {
    // Enabled controls whether this node creates local Controller Raft snapshots.
    Enabled bool
    // EnabledSet records whether Enabled was explicitly configured.
    EnabledSet bool
    // TriggerEntries is the applied-entry delta required before taking another snapshot.
    TriggerEntries uint64
    // CheckInterval is the minimum interval between compaction checks.
    CheckInterval time.Duration
}
```

Add helpers:

```go
func NormalizeLogCompactionConfig(cfg LogCompactionConfig) LogCompactionConfig {
    if !cfg.EnabledSet {
        cfg.Enabled = true
    }
    if cfg.TriggerEntries == 0 {
        cfg.TriggerEntries = defaultLogCompactionTriggerEntries
    }
    if cfg.CheckInterval == 0 {
        cfg.CheckInterval = defaultLogCompactionCheckInterval
    }
    return cfg
}

func ValidateLogCompactionConfig(cfg LogCompactionConfig) error {
    if !cfg.Enabled {
        return nil
    }
    if cfg.TriggerEntries == 0 {
        return fmt.Errorf("%w: controller log compaction trigger entries must be > 0", ErrInvalidConfig)
    }
    if cfg.CheckInterval <= 0 {
        return fmt.Errorf("%w: controller log compaction check interval must be > 0", ErrInvalidConfig)
    }
    return nil
}
```

Add to `Config`:

```go
// LogCompaction controls local Controller Raft snapshot compaction.
LogCompaction LogCompactionConfig
```

In `validateCore`, normalize before validation where possible. If mutation inside validation is avoided, normalize in `NewService` or `Start` and store the normalized value in `s.cfg.LogCompaction` before the run loop starts.

- [ ] **Step 4: Run config tests**

Run: `go test ./pkg/controller/raft -run 'TestNormalizeLogCompactionConfig|TestValidateLogCompactionConfig' -count=1`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/controller/raft/config.go pkg/controller/raft/config_test.go
git commit -m "feat(controllerraft): add log compaction config"
```

---

### Task 4: Support Snapshot Restore In Controller Raft Startup And Ready Processing

**Files:**
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`

- [ ] **Step 1: Write startup replay test with preexisting snapshot**

Add a test to `pkg/controller/raft/service_test.go` that proves post-snapshot entries replay after restart without relying on the compaction implementation from Task 5:

```go
func TestServiceStartRestoresSnapshotAndReplaysPostSnapshotEntries(t *testing.T) {
    env := newTestEnv(t, []uint64{1})
    t.Cleanup(env.stopAll)

    node := env.nodes[1]
    require.NoError(t, os.MkdirAll(node.dir, 0o755))

    // Build snapshot data at index 1 containing node 2.
    snapStore, err := controllermeta.Open(filepath.Join(t.TempDir(), "snapshot-meta"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, snapStore.Close()) })
    require.NoError(t, snapStore.UpsertNode(context.Background(), controllermeta.ClusterNode{NodeID: 2, Addr: "127.0.0.1:7002", Status: controllermeta.NodeStatusAlive, JoinedAt: time.Unix(1, 0), LastHeartbeatAt: time.Unix(1, 0), CapacityWeight: 1}))
    snapData, err := snapStore.ExportSnapshot(context.Background())
    require.NoError(t, err)

    entryData, err := encodeCommand(slotcontroller.Command{
        Kind: slotcontroller.CommandKindNodeJoin,
        NodeJoin: &slotcontroller.NodeJoinRequest{NodeID: 3, Addr: "127.0.0.1:7003", JoinedAt: time.Unix(2, 0), CapacityWeight: 1},
    })
    require.NoError(t, err)

    logDB, err := raftstorage.Open(filepath.Join(node.dir, "controller-raft"))
    require.NoError(t, err)
    snap := raftpb.Snapshot{Data: snapData, Metadata: raftpb.SnapshotMetadata{Index: 1, Term: 1, ConfState: raftpb.ConfState{Voters: []uint64{1}}}}
    hs := raftpb.HardState{Term: 1, Vote: 1, Commit: 2}
    require.NoError(t, logDB.ForController().Save(context.Background(), multiraft.PersistentState{
        HardState: &hs,
        Snapshot:  &snap,
        Entries:   []raftpb.Entry{{Index: 2, Term: 1, Type: raftpb.EntryNormal, Data: entryData}},
    }))
    require.NoError(t, logDB.ForController().MarkApplied(context.Background(), 2))
    require.NoError(t, logDB.Close())

    env.startNode(t, 1, nil)
    require.Eventually(t, func() bool {
        _, err := env.nodes[1].meta.GetNode(context.Background(), 3)
        return err == nil
    }, 5*time.Second, 10*time.Millisecond)
}
```

This test must assert node 3 exists after start. Node 3 is only present in entry 2, so using `Applied=state.AppliedIndex` instead of `Applied=snapshot.Index` will fail by restoring only node 2 and skipping replay.

- [ ] **Step 2: Write Ready snapshot restore test with post-snapshot committed entry**

Prefer a focused helper-level test around a new helper named `restoreReadySnapshot(ctx, snap, storageView, latestConfState)`:

```go
func (s *Service) restoreReadySnapshot(ctx context.Context, snap raftpb.Snapshot, storageView *storageAdapter, latestConfState *raftpb.ConfState) (uint64, error) {
    if raft.IsEmptySnap(snap) {
        return 0, nil
    }
    if err := s.cfg.StateMachine.Restore(ctx, append([]byte(nil), snap.Data...)); err != nil {
        return 0, err
    }
    *latestConfState = cloneConfState(snap.Metadata.ConfState)
    return snap.Metadata.Index, nil
}
```

Then test the helper plus one post-snapshot committed entry:

```go
func TestServiceIncomingReadySnapshotRestoresControllerMeta(t *testing.T) {
    // Create snapshot bytes from a source controller meta store containing node 9.
    // Create target service state machine over an empty store and a real controller raftlog.
    // Call storageView.persistReady(ctx, raft.Ready{Snapshot: snap}) for snapshot index 5 with voters {1,2}.
    // Call service.restoreReadySnapshot(ctx, snap, storageView, &latestConfState).
    // MarkApplied(5), then apply a committed EntryNormal at index 6 that adds node 10, then MarkApplied(6).
    // Assert target meta has node 9 from the snapshot and node 10 from the post-snapshot entry.
    // Assert storage.InitialState(ctx).AppliedIndex == 6 and latestConfState.Voters == []uint64{1,2}.
}
```

If helper-level testing is awkward, cover this through a two-node lagging follower scenario, but keep it fast and deterministic.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./pkg/controller/raft -run 'TestServiceStartRestoresSnapshotAndReplaysPostSnapshotEntries|TestServiceIncomingReadySnapshotRestoresControllerMeta' -count=1`

Expected: FAIL due snapshot unsupported and missing helper behavior.

- [ ] **Step 4: Change startup load to restore snapshots safely**

In `Service.Start`:

- Normalize and validate compaction config before creating `RawNode`.
- Call `storageView.load(ctx)` and keep returned snapshot.
- Remove the `ErrSnapshotUnsupported` startup return.
- If snapshot is not empty, call `s.cfg.StateMachine.Restore(ctx, snapshot.Data)` before setting `started=true`.
- Use an effective applied index:

```go
appliedIndex := state.AppliedIndex
if !raft.IsEmptySnap(snapshot) {
    appliedIndex = snapshot.Metadata.Index
}
```

- Pass `Applied: appliedIndex` to `raft.Config`.
- Ensure `storageAdapter.load` initializes `loadedMemoryStorage` from the snapshot conf state when a snapshot exists:

```go
loadedConfState := state.ConfState
if !raft.IsEmptySnap(snap) {
    loadedConfState = snap.Metadata.ConfState
}
loaded := newLoadedMemoryStorage(memory, loadedConfState)
```

- Keep `loadedMemoryStorage.ApplySnapshot` updating its cached conf state from `snapshot.Metadata.ConfState`.
- Change `run` to receive the startup state it needs. Prefer a small startup context:

```go
type runStartupState struct {
    State    multiraft.BootstrapState
    Snapshot raftpb.Snapshot
}

go s.run(rawNode, storageView, runStartupState{State: state, Snapshot: snapshot}, s.stopCh, s.doneCh, s.transport)
```

Then initialize `latestConfState` and `lastSnapshotIndex` from this startup context inside `run`. Do not reference `state` or `snapshot` inside `run` unless they are passed in.

- [ ] **Step 5: Add Ready snapshot handling in processReady**

Modify `processReady` flow carefully:

1. Keep `storageView.persistReady(ctx, ready)` before transport and apply, because Ready persistence must happen before message send/apply.
2. If `ready.Snapshot` is non-empty after persistence, restore the Controller state machine from `ready.Snapshot.Data`.
3. Set `lastApplied = ready.Snapshot.Metadata.Index` and update latest conf state from `ready.Snapshot.Metadata.ConfState`.
4. After committed entries apply, call `MarkApplied(lastApplied)` if advanced.
5. Then call `rawNode.Advance(ready)`.
6. Only after `Advance`, call local compaction check.

Pseudo-structure:

```go
lastApplied := uint64(0)
if !raft.IsEmptySnap(ready.Snapshot) {
    if err := s.cfg.StateMachine.Restore(context.Background(), append([]byte(nil), ready.Snapshot.Data...)); err != nil { ...fatal... }
    lastApplied = ready.Snapshot.Metadata.Index
    latestConfState = cloneConfState(ready.Snapshot.Metadata.ConfState)
}
for _, entry := range ready.CommittedEntries { ... }
if lastApplied > 0 { storage.MarkApplied(ctx, lastApplied) }
rawNode.Advance(ready)
s.updateLeader(rawNode)
maybeCompact(...)
```

- [ ] **Step 6: Track conf changes including V2**

In committed entry handling:

```go
case raftpb.EntryConfChange:
    var cc raftpb.ConfChange
    if err := cc.Unmarshal(entry.Data); err != nil { ... }
    latestConfState = cloneConfState(*rawNode.ApplyConfChange(cc))
case raftpb.EntryConfChangeV2:
    var cc raftpb.ConfChangeV2
    if err := cc.Unmarshal(entry.Data); err != nil { ... }
    latestConfState = cloneConfState(*rawNode.ApplyConfChange(cc))
```

Initialize `latestConfState` before the loop from the `runStartupState`, preferring snapshot metadata when snapshot exists. If practical, add a regression test where a snapshot has a different `ConfState` from persisted bootstrap state and verify `InitialState` returns the snapshot conf state.

- [ ] **Step 7: Run targeted tests**

Run: `go test ./pkg/controller/raft -run 'TestServiceStartRestoresSnapshotAndReplaysPostSnapshotEntries|TestServiceIncomingReadySnapshotRestoresControllerMeta' -count=1`

Expected: PASS.

- [ ] **Step 8: Run existing controller raft tests**

Run: `go test ./pkg/controller/raft -count=1`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/controller/raft/service.go pkg/controller/raft/service_test.go
git commit -m "feat(controllerraft): restore controller snapshots"
```

---

### Task 5: Implement Local Controller Raft Log Compaction

**Files:**
- Modify: `pkg/controller/raft/service.go`
- Modify: `pkg/controller/raft/service_test.go`

- [ ] **Step 1: Write threshold compaction test**

Add to `pkg/controller/raft/service_test.go`:

```go
func TestServiceCompactsControllerLogAfterAppliedThreshold(t *testing.T) {
    env := newTestEnv(t, []uint64{1})
    t.Cleanup(env.stopAll)
    env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
        cfg.LogCompaction = LogCompactionConfig{Enabled: true, EnabledSet: true, TriggerEntries: 1, CheckInterval: time.Nanosecond}
    })
    env.waitForLeader(t, []uint64{1})

    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    require.NoError(t, env.nodes[1].service.Propose(ctx, slotcontroller.Command{Kind: slotcontroller.CommandKindNodeJoin, NodeJoin: &slotcontroller.NodeJoinRequest{NodeID: 2, Addr: "127.0.0.1:7002", JoinedAt: time.Unix(1, 0), CapacityWeight: 1}}))

    snap := waitForControllerSnapshotIndex(t, env.nodes[1].logDB.ForController(), 1)
    first, err := env.nodes[1].logDB.ForController().FirstIndex(context.Background())
    require.NoError(t, err)
    require.Equal(t, snap.Metadata.Index+1, first)
}
```

Add the helper in `pkg/controller/raft/service_test.go` rather than assuming it exists:

```go
func waitForControllerSnapshotIndex(t *testing.T, store multiraft.Storage, min uint64) raftpb.Snapshot {
    t.Helper()

    var snap raftpb.Snapshot
    require.Eventually(t, func() bool {
        got, err := store.Snapshot(context.Background())
        if err != nil {
            return false
        }
        if got.Metadata.Index < min {
            return false
        }
        snap = got
        return true
    }, 5*time.Second, 10*time.Millisecond)
    return snap
}
```

Use the actual controller raftlog storage interface type in the helper signature after inspecting `pkg/slot/multiraft.Storage`.

- [ ] **Step 2: Write disabled compaction test**

```go
func TestServiceControllerLogCompactionCanBeDisabled(t *testing.T) {
    env := newTestEnv(t, []uint64{1})
    t.Cleanup(env.stopAll)
    env.startNodeWithConfig(t, 1, nil, func(cfg *Config) {
        cfg.LogCompaction = LogCompactionConfig{Enabled: false, EnabledSet: true, TriggerEntries: 1, CheckInterval: time.Nanosecond}
    })
    env.waitForLeader(t, []uint64{1})
    // Propose two or more mutating commands so the enabled path would cross the threshold.
    // Assert Snapshot(ctx).Metadata.Index remains zero after bounded polling.
    // Assert FirstIndex remains at the pre-compaction floor and entries remain readable.
}
```

- [ ] **Step 3: Write non-fatal compaction failure test**

Use package-level test hooks to inject failure without changing public APIs. These hooks must be unexported variables in a production `.go` file because production compaction code calls them; tests only set/reset them:

```go
var controllerSnapshotExportHook func() error
var controllerSnapshotSaveHook func() error
```

Better: introduce narrow unexported hooks only in `pkg/controller/raft` tests if necessary:

```go
var compactControllerLogHook func() error
```

Test expectation:

```go
func TestServiceCompactionFailureDoesNotFailProposalLoop(t *testing.T) {
    // Inject one compaction failure.
    // Propose command A; compaction fails in warning path.
    // Propose command B; it still succeeds.
    // Remove hook.
    // Propose command C or another harmless mutating command to force a new Ready/compaction check.
    // Wait for snapshot retry to succeed.
}
```

Do not rely on sleeping alone for retry: after clearing the failure hook, drive another proposal so `processReady` reaches the compaction check again.

- [ ] **Step 3b: Write restart-after-service-compaction test**

Add an end-to-end regression that uses the actual compaction path:

```go
func TestServiceRestartRestoresSnapshotCreatedByCompaction(t *testing.T) {
    // Start a single-node cluster with TriggerEntries=1 and a tiny CheckInterval.
    // Propose node 2 and wait until a controller snapshot exists.
    // Propose node 3 after the snapshot, then stop and restart the service.
    // Assert both node 2 and node 3 are present after restart.
}
```

This complements the pre-seeded startup test by proving service-created snapshots are restart-safe.

- [ ] **Step 4: Run tests to verify they fail**

Run: `go test ./pkg/controller/raft -run 'TestServiceCompactsControllerLogAfterAppliedThreshold|TestServiceControllerLogCompactionCanBeDisabled|TestServiceCompactionFailureDoesNotFailProposalLoop|TestServiceRestartRestoresSnapshotCreatedByCompaction' -count=1`

Expected: FAIL because compaction is not implemented.

- [ ] **Step 5: Implement compaction state and helper**

Add unexported state in `run`:

```go
compaction := newControllerLogCompactor(s.cfg.LogCompaction)
lastSnapshotIndex := startup.Snapshot.Metadata.Index
latestConfState := effectiveStartupConfState(startup.State, startup.Snapshot)
```

Here `startup` is the `runStartupState` passed from `Start`; do not close over local `state` or `snapshot` variables that are out of scope.

Implement helper in `service.go` or new `compaction.go` if the file becomes too large:

```go
type controllerLogCompactor struct {
    cfg             LogCompactionConfig
    lastCheck       time.Time
    lastSnapshotIdx uint64
    now             func() time.Time
}

func (c *controllerLogCompactor) shouldCompact(applied uint64) bool { ... }
```

Implement snapshot creation after `rawNode.Advance(ready)`:

```go
func (s *Service) compactControllerLog(ctx context.Context, storageView *storageAdapter, applied uint64, confState raftpb.ConfState) error {
    data, err := s.cfg.StateMachine.Snapshot(ctx)
    if err != nil { return err }
    term, err := storageView.memory.Term(applied)
    if err != nil { return err }
    snap := raftpb.Snapshot{Data: data, Metadata: raftpb.SnapshotMetadata{Index: applied, Term: term, ConfState: cloneConfState(confState)}}
    if err := storageView.storage.Save(ctx, multiraft.PersistentState{Snapshot: &snap}); err != nil { return err }
    if _, err := storageView.memory.CreateSnapshot(applied, &snap.Metadata.ConfState, snap.Data); err != nil && !errors.Is(err, raft.ErrSnapOutOfDate) { return err }
    if err := storageView.memory.Compact(applied); err != nil && !errors.Is(err, raft.ErrCompacted) { return err }
    return nil
}
```

Check actual error names from `go.etcd.io/raft/v3`; use `raft.ErrSnapOutOfDate` and `raft.ErrCompacted` if exported there.

- [ ] **Step 6: Ensure ordering is safe**

In `processReady`, local compaction must happen after:

```go
rawNode.Advance(ready)
s.updateLeader(rawNode)
failInflightProposalsOnLeaderLoss(...)
```

Do not call `failTracked`, `setError`, or return error for local compaction warnings. Log warning with `s.cfg.Logger.Named("raft").Warn(...)` if logger exists.

- [ ] **Step 7: Run targeted tests**

Run: `go test ./pkg/controller/raft -run 'TestServiceCompactsControllerLogAfterAppliedThreshold|TestServiceControllerLogCompactionCanBeDisabled|TestServiceCompactionFailureDoesNotFailProposalLoop|TestServiceRestartRestoresSnapshotCreatedByCompaction' -count=1`

Expected: PASS.

- [ ] **Step 8: Run full controller raft package tests**

Run: `go test ./pkg/controller/raft -count=1`

Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add pkg/controller/raft/service.go pkg/controller/raft/service_test.go
git commit -m "feat(controllerraft): compact local controller log"
```

---

### Task 6: Wire Cluster And App Config

**Files:**
- Modify: `pkg/cluster/config.go`
- Modify: `pkg/cluster/controller_host.go`
- Modify: `pkg/cluster/config_test.go`
- Modify: `pkg/cluster/controller_host_test.go`
- Modify: `internal/app/config.go`
- Modify: `internal/app/build.go`
- Modify: `internal/app/config_test.go`

- [ ] **Step 1: Add cluster config tests**

In `pkg/cluster/config_test.go` add tests for defaults and explicit disabled. Shape:

```go
func TestConfigApplyDefaultsEnablesControllerLogCompaction(t *testing.T) {
    cfg := validTestConfig()
    cfg.ControllerLogCompaction = controllerraft.LogCompactionConfig{}
    cfg.applyDefaults()
    require.True(t, cfg.ControllerLogCompaction.Enabled)
    require.Equal(t, uint64(10000), cfg.ControllerLogCompaction.TriggerEntries)
    require.Equal(t, 30*time.Second, cfg.ControllerLogCompaction.CheckInterval)
}

func TestConfigApplyDefaultsPreservesControllerLogCompactionDisabled(t *testing.T) {
    cfg := validTestConfig()
    cfg.ControllerLogCompaction = controllerraft.LogCompactionConfig{Enabled: false, EnabledSet: true}
    cfg.applyDefaults()
    require.False(t, cfg.ControllerLogCompaction.Enabled)
}
```

Use existing test helpers in that file instead of inventing `validTestConfig` if a helper already exists.

- [ ] **Step 2: Add controller host pass-through test**

Do not inspect `host.service.cfg`: `cfg` is unexported from `pkg/controller/raft` and `pkg/cluster` should not gain a production accessor just for this test. Instead add a package-local factory variable in `pkg/cluster/controller_host.go`:

```go
var newControllerRaftService = controllerraft.NewService
```

Use it in `newControllerHost`:

```go
service := newControllerRaftService(controllerraft.Config{
    // existing fields...
    LogCompaction: cfg.ControllerLogCompaction,
})
```

Then in `pkg/cluster/controller_host_test.go`, create or extend a test that captures the config:

```go
func TestNewControllerHostPassesLogCompactionConfig(t *testing.T) {
    cfg := validTestConfig()
    cfg.ControllerMetaPath = filepath.Join(t.TempDir(), "controller-meta")
    cfg.ControllerRaftPath = filepath.Join(t.TempDir(), "controller-raft")
    cfg.ControllerReplicaN = 1
    cfg.Nodes = []NodeConfig{{NodeID: cfg.NodeID, Addr: "127.0.0.1:0"}}
    cfg.ControllerLogCompaction = controllerraft.LogCompactionConfig{Enabled: false, EnabledSet: true, TriggerEntries: 12, CheckInterval: time.Second}

    discovery := NewStaticDiscovery(cfg.Nodes)
    layer := newTransportLayer(cfg, discovery, nil)
    requireNoErr(t, layer.Start(
        "127.0.0.1:0",
        func([]byte) {},
        func([]byte) {},
        func(context.Context, []byte) ([]byte, error) { return nil, nil },
        func(context.Context, []byte) ([]byte, error) { return nil, nil },
        func(context.Context, []byte) ([]byte, error) { return nil, nil },
    ))
    t.Cleanup(layer.Stop)
    cfg.Nodes[0].Addr = layer.server.Listener().Addr().String()

    var captured controllerraft.Config
    oldFactory := newControllerRaftService
    newControllerRaftService = func(cfg controllerraft.Config) *controllerraft.Service {
        captured = cfg
        return controllerraft.NewService(cfg)
    }
    t.Cleanup(func() { newControllerRaftService = oldFactory })

    host, err := newControllerHost(cfg, layer)
    require.NoError(t, err)
    t.Cleanup(host.Stop)
    require.False(t, captured.LogCompaction.Enabled)
    require.Equal(t, uint64(12), captured.LogCompaction.TriggerEntries)
}
```

Use the existing `validTestConfig()` and `newTransportLayer(...)` pattern from `TestControllerHostStartElectsSingleLocalPeer`; do not invent `testControllerHostConfig` or `newTestTransportLayer`.

- [ ] **Step 3: Add app config tests**

In `internal/app/config_test.go`, add:

```go
func TestConfigDefaultsControllerLogCompaction(t *testing.T) { ... }
func TestConfigPreservesControllerLogCompactionDisabled(t *testing.T) { ... }
func TestConfigRejectsInvalidControllerLogCompaction(t *testing.T) { ... }
func TestClusterRuntimeConfigIncludesControllerLogCompaction(t *testing.T) { ... }
```

- [ ] **Step 4: Run config tests to verify they fail**

Run:

```bash
go test ./pkg/cluster -run 'TestConfig.*ControllerLogCompaction|TestNewControllerHostPassesLogCompactionConfig' -count=1
go test ./internal/app -run 'TestConfig.*ControllerLogCompaction|TestClusterRuntimeConfigIncludesControllerLogCompaction' -count=1
```

Expected: FAIL because fields do not exist.

- [ ] **Step 5: Implement `pkg/cluster` config**

In `pkg/cluster/config.go` import `controllerraft` if not already imported.

Add to `Config`:

```go
// ControllerLogCompaction controls local Controller Raft snapshot compaction.
ControllerLogCompaction controllerraft.LogCompactionConfig
```

In `applyDefaults`:

```go
c.ControllerLogCompaction = controllerraft.NormalizeLogCompactionConfig(c.ControllerLogCompaction)
```

If helper is unexported, either export it as `NormalizeLogCompactionConfig` or duplicate a small cluster-local normalization that calls exported defaults. Prefer exporting from `pkg/controller/raft` so direct users behave consistently.

In `validate` after defaults:

```go
if err := controllerraft.ValidateLogCompactionConfig(c.ControllerLogCompaction); err != nil {
    return err
}
```

- [ ] **Step 6: Pass config through controller host**

In `pkg/cluster/controller_host.go`, add to `controllerraft.Config`:

```go
LogCompaction: cfg.ControllerLogCompaction,
```

- [ ] **Step 7: Implement `internal/app` config**

In `internal/app/config.go`, import `controllerraft` if acceptable. If avoiding dependency from app config to controller raft is preferred, define an app-local struct with identical fields and map it in `build.go`.

Recommended app-local type:

```go
// ControllerLogCompactionConfig controls local Controller Raft snapshot compaction.
type ControllerLogCompactionConfig struct {
    // Enabled controls whether this node creates local Controller Raft snapshots.
    Enabled bool
    // TriggerEntries is the applied-entry delta required before taking another snapshot.
    TriggerEntries uint64
    // CheckInterval is the minimum interval between compaction checks.
    CheckInterval time.Duration

    enabledSet bool
    triggerEntriesSet bool
    checkIntervalSet bool
}
```

Add to `ClusterConfig`:

```go
// ControllerLogCompaction controls local Controller Raft snapshot compaction.
ControllerLogCompaction ControllerLogCompactionConfig
```

Add setter:

```go
// SetControllerLogCompactionExplicitFlags records which Controller log compaction values were explicitly configured.
func (c *ClusterConfig) SetControllerLogCompactionExplicitFlags(enabledSet, triggerEntriesSet, checkIntervalSet bool) { ... }
```

In `ApplyDefaultsAndValidate`:

```go
if !c.Cluster.ControllerLogCompaction.enabledSet {
    c.Cluster.ControllerLogCompaction.Enabled = true
}
if c.Cluster.ControllerLogCompaction.Enabled {
    if c.Cluster.ControllerLogCompaction.TriggerEntries <= 0 && c.Cluster.ControllerLogCompaction.triggerEntriesSet { return fmt.Errorf(...) }
    if c.Cluster.ControllerLogCompaction.CheckInterval <= 0 && c.Cluster.ControllerLogCompaction.checkIntervalSet { return fmt.Errorf(...) }
}
if c.Cluster.ControllerLogCompaction.TriggerEntries == 0 {
    c.Cluster.ControllerLogCompaction.TriggerEntries = 10000
}
if c.Cluster.ControllerLogCompaction.CheckInterval == 0 {
    c.Cluster.ControllerLogCompaction.CheckInterval = 30 * time.Second
}
```

Ensure negative `CheckInterval` is rejected when explicitly set, even before defaulting.

- [ ] **Step 8: Map app config to cluster runtime config**

In `internal/app/build.go`, add:

```go
ControllerLogCompaction: controllerraft.LogCompactionConfig{
    Enabled:        c.ControllerLogCompaction.Enabled,
    EnabledSet:     true,
    TriggerEntries: c.ControllerLogCompaction.TriggerEntries,
    CheckInterval:  c.ControllerLogCompaction.CheckInterval,
},
```

Import `controllerraft` if needed.

- [ ] **Step 9: Run config tests**

Run:

```bash
go test ./pkg/cluster -run 'TestConfig.*ControllerLogCompaction|TestNewControllerHostPassesLogCompactionConfig' -count=1
go test ./internal/app -run 'TestConfig.*ControllerLogCompaction|TestClusterRuntimeConfigIncludesControllerLogCompaction' -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add pkg/cluster/config.go pkg/cluster/controller_host.go pkg/cluster/config_test.go pkg/cluster/controller_host_test.go internal/app/config.go internal/app/build.go internal/app/config_test.go
git commit -m "feat(config): wire controller log compaction"
```

---

### Task 7: Parse WK Config Keys And Update Example

**Files:**
- Modify: `cmd/wukongim/config.go`
- Modify: `cmd/wukongim/config_test.go`
- Modify: `internal/app/config_test.go`
- Modify: `wukongim.conf.example`

- [ ] **Step 1: Add command config tests**

In `cmd/wukongim/config_test.go`, add or extend tests:

```go
func TestLoadConfigParsesControllerLogCompaction(t *testing.T) {
    dir := t.TempDir()
    path := writeConf(t, dir, "wukongim.conf",
        "WK_NODE_ID=1",
        "WK_NODE_DATA_DIR="+filepath.Join(dir, "node-1"),
        "WK_CLUSTER_LISTEN_ADDR=127.0.0.1:7000",
        "WK_CLUSTER_INITIAL_SLOT_COUNT=1",
        `WK_CLUSTER_NODES=[{"id":1,"addr":"127.0.0.1:7000"}]`,
        `WK_GATEWAY_LISTENERS=[{"name":"tcp-wkproto","network":"tcp","address":"127.0.0.1:5100","transport":"stdnet","protocol":"wkproto"}]`,
        "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=false",
        "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES=25",
        "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL=2s",
    )
    cfg, err := loadConfig(path)
    require.NoError(t, err)
    require.False(t, cfg.Cluster.ControllerLogCompaction.Enabled)
    require.Equal(t, uint64(25), cfg.Cluster.ControllerLogCompaction.TriggerEntries)
    require.Equal(t, 2*time.Second, cfg.Cluster.ControllerLogCompaction.CheckInterval)
}

func TestLoadConfigPrefersEnvironmentVariablesForControllerLogCompaction(t *testing.T) { ... }
```

Use existing helper names in `config_test.go`; do not duplicate helper functions.

- [ ] **Step 2: Add example key coverage**

In `internal/app/config_test.go`, add the three new keys to `supportedConfigExampleKeys`:

```go
"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED",
"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES",
"WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL",
```

- [ ] **Step 3: Run tests to verify they fail**

Run:

```bash
go test ./cmd/wukongim -run 'TestLoadConfig.*ControllerLogCompaction' -count=1
go test ./internal/app -run TestConfigExampleDocumentsSupportedWKKeys -count=1
```

Expected: FAIL until parser/example are updated.

- [ ] **Step 4: Parse keys in `buildAppConfig`**

In `cmd/wukongim/config.go`, near existing controller timeout parsing:

```go
controllerLogCompactionEnabled, err := parseBool(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED")
if err != nil { return app.Config{}, err }
controllerLogCompactionTriggerEntries, err := parseUint64(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES")
if err != nil { return app.Config{}, err }
controllerLogCompactionCheckInterval, err := parseDuration(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL")
if err != nil { return app.Config{}, err }
```

Set in `app.Config`:

```go
ControllerLogCompaction: app.ControllerLogCompactionConfig{
    Enabled:        controllerLogCompactionEnabled,
    TriggerEntries: controllerLogCompactionTriggerEntries,
    CheckInterval:  controllerLogCompactionCheckInterval,
},
```

After building cfg, record explicit flags:

```go
cfg.Cluster.SetControllerLogCompactionExplicitFlags(
    stringValue(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED") != "",
    stringValue(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES") != "",
    stringValue(v, "WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL") != "",
)
```

- [ ] **Step 5: Update config example**

In `wukongim.conf.example`, near controller timeouts:

```conf
# Enables local Controller Raft log snapshot compaction. Enabled by default.
WK_CLUSTER_CONTROLLER_LOG_COMPACTION_ENABLED=true
# Number of newly applied Controller Raft entries after the last snapshot before creating a new snapshot.
WK_CLUSTER_CONTROLLER_LOG_COMPACTION_TRIGGER_ENTRIES=10000
# Minimum interval between Controller Raft compaction checks.
WK_CLUSTER_CONTROLLER_LOG_COMPACTION_CHECK_INTERVAL=30s
```

- [ ] **Step 6: Run config tests**

Run:

```bash
go test ./cmd/wukongim -run 'TestLoadConfig.*ControllerLogCompaction' -count=1
go test ./internal/app -run TestConfigExampleDocumentsSupportedWKKeys -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/wukongim/config.go cmd/wukongim/config_test.go internal/app/config_test.go wukongim.conf.example
git commit -m "feat(config): expose controller log compaction settings"
```

---

### Task 8: Update Controller Flow Documentation

**Files:**
- Modify: `pkg/controller/FLOW.md`
- Optional Modify: `docs/development/PROJECT_KNOWLEDGE.md`

- [ ] **Step 1: Update `pkg/controller/FLOW.md`**

Edit the Raft section:

- Remove/replace any statement that Controller snapshots are unsupported.
- Add a bullet under storage/Raft config:

```markdown
| Controller log compaction | 默认开启；按 applied entry 增量导出 controller meta snapshot，写入 Controller Raft snapshot，并裁剪旧本地 entries | raft/service.go |
```

- Add flow note:

```markdown
Controller Raft snapshot compaction:
  ① apply committed entries and mark applied
  ② after RawNode.Advance, if applied delta reaches threshold, export meta snapshot
  ③ persist raft snapshot to raftlog, then compact MemoryStorage
  ④ startup restores snapshot first, then replays post-snapshot entries
  ⑤ Ready.Snapshot is restored before marking its index applied
```

Use Chinese prose consistently with the rest of the file, but keep code/config names exact.

- [ ] **Step 2: Add project knowledge only if useful**

If implementation establishes a durable rule, append a concise bullet under `## Controller`:

```markdown
### Controller Raft compaction
- Controller Raft snapshot restore starts from the snapshot index and replays post-snapshot entries; never skip replay by using a later persisted applied index after importing snapshot data.
```

Keep the document short.

- [ ] **Step 3: Commit**

```bash
git add pkg/controller/FLOW.md docs/development/PROJECT_KNOWLEDGE.md
git commit -m "docs(controller): describe raft log compaction"
```

Only include `docs/development/PROJECT_KNOWLEDGE.md` if you changed it.

---

### Task 9: Verification

**Files:**
- No code changes expected.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./pkg/controller/plane ./pkg/controller/raft ./pkg/raftlog ./pkg/cluster ./internal/app ./cmd/wukongim -count=1
```

Expected: PASS.

- [ ] **Step 2: Optionally run broader relevant tests**

Run:

```bash
go test ./internal/... ./pkg/... -count=1
```

Expected: PASS if run.

This is optional because the repository already has unrelated dirty worktree changes and this command can be slow. If it is too slow or fails in unrelated dirty areas, capture exact failing package/test and run the smallest relevant subset again. The mandatory verification for this feature is Step 1.

- [ ] **Step 3: Inspect git diff**

Run:

```bash
git status --short
git diff --stat
git diff -- docs/superpowers/specs/2026-05-05-controller-log-compaction-design.md docs/superpowers/plans/2026-05-05-controller-log-compaction.md
```

Expected: only intended files for this feature are changed, plus any pre-existing unrelated user changes remain untouched.

- [ ] **Step 4: Final commit if previous tasks were not committed**

If task-level commits were skipped, commit the complete feature carefully:

```bash
git add pkg/controller/plane pkg/controller/raft pkg/raftlog pkg/cluster internal/app cmd/wukongim wukongim.conf.example pkg/controller/FLOW.md docs/superpowers/specs/2026-05-05-controller-log-compaction-design.md docs/superpowers/plans/2026-05-05-controller-log-compaction.md
git commit -m "feat(controller): compact raft log with snapshots"
```

Do not include unrelated dirty files.
