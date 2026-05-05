# Controller Log Compaction Risk Tests Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add focused regression coverage for the remaining Controller Raft log compaction risks: real multi-node snapshot delivery and ConfChangeV2 snapshot metadata.

**Architecture:** Keep the production compaction code unchanged unless a failing test exposes a bug. Add tests in `pkg/controller/raft/service_test.go` using the existing `testEnv` harness, real raftlog/controller meta stores, and etcd raft APIs.

**Tech Stack:** Go, etcd raft `RawNode`, existing controller raft test harness, `go test`.

---

## File Structure

- Modify: `pkg/controller/raft/service_test.go`
  - Add one multi-node regression test that lets a stopped follower catch up from a real Raft snapshot after leader-side compaction.
  - Add one focused ConfChangeV2 regression test that applies a V2 conf change, compacts, and inspects the durable snapshot `ConfState`.

## Task 1: Multi-node Controller snapshot catch-up test

- [ ] **Step 1: Write failing test**

Add `TestServiceLaggingFollowerRestoresControllerSnapshot`:

```go
func TestServiceLaggingFollowerRestoresControllerSnapshot(t *testing.T) {
    env := newTestEnv(t, []uint64{1, 2, 3})
    defer env.stopAll()
    compactionCfg := func(cfg *Config) {
        cfg.LogCompaction = LogCompactionConfig{Enabled: true, EnabledSet: true, TriggerEntries: 1, CheckInterval: time.Nanosecond}
    }
    env.startNodeWithConfig(t, 1, nil, compactionCfg)
    env.startNodeWithConfig(t, 2, nil, compactionCfg)
    env.startNodeWithConfig(t, 3, nil, compactionCfg)
    leaderID := env.waitForLeader(t, []uint64{1, 2, 3})
    laggingID := firstNonLeader([]uint64{1, 2, 3}, leaderID)
    env.stopNode(laggingID)

    leader := env.nodes[leaderID]
    for nodeID := uint64(10); nodeID < 15; nodeID++ {
        require.NoError(t, leader.service.Propose(context.Background(), nodeJoinCommand(nodeID)))
    }
    leaderSnap := waitForControllerSnapshotIndex(t, leader.logDB.ForController(), 4)
    require.GreaterOrEqual(t, leaderSnap.Metadata.Index, uint64(4))

    env.startNodeWithConfig(t, laggingID, nil, compactionCfg)
    for nodeID := uint64(10); nodeID < 15; nodeID++ {
        waitForControllerNode(t, env.nodes[laggingID].meta, nodeID)
    }
    followerSnap := waitForControllerSnapshotIndex(t, env.nodes[laggingID].logDB.ForController(), leaderSnap.Metadata.Index)
    require.GreaterOrEqual(t, followerSnap.Metadata.Index, leaderSnap.Metadata.Index)
}
```

Use small helpers in the same test file if needed:

```go
func firstNonLeader(ids []uint64, leaderID uint64) uint64 { ... }
func waitForControllerNode(t *testing.T, store *controllermeta.Store, nodeID uint64) { ... }
```

- [ ] **Step 2: Run test to verify it fails or passes for current behavior**

Run: `go test ./pkg/controller/raft -run TestServiceLaggingFollowerRestoresControllerSnapshot -count=1`

Expected: ideally PASS if production already handles real snapshot delivery; if FAIL, inspect whether the failure reveals a real bug or test harness timing issue.

- [ ] **Step 3: Fix production only if the failing test exposes a real bug**

If production changes are required, keep them minimal and localized to `pkg/controller/raft/service.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pkg/controller/raft -run TestServiceLaggingFollowerRestoresControllerSnapshot -count=1`

Expected: PASS.

## Task 2: ConfChangeV2 snapshot ConfState test

- [ ] **Step 1: Write failing test**

Add `TestServiceCompactionSnapshotUsesConfChangeV2State`:

```go
func TestServiceCompactionSnapshotUsesConfChangeV2State(t *testing.T) {
    ctx := context.Background()
    store, err := controllermeta.Open(filepath.Join(t.TempDir(), "meta"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, store.Close()) })
    sm := slotcontroller.NewStateMachine(store, slotcontroller.StateMachineConfig{})

    logDB, err := raftstorage.Open(filepath.Join(t.TempDir(), "raft"))
    require.NoError(t, err)
    t.Cleanup(func() { require.NoError(t, logDB.Close()) })
    storageView := newStorageAdapter(logDB.ForController())
    _, _, _, err = storageView.load(ctx)
    require.NoError(t, err)

    rawNode, err := raft.NewRawNode(&raft.Config{ID: 1, ElectionTick: 10, HeartbeatTick: 1, Storage: storageView.memory, MaxSizePerMsg: math.MaxUint64, MaxCommittedSizePerReady: math.MaxUint64, MaxInflightMsgs: 256})
    require.NoError(t, err)
    require.NoError(t, rawNode.Bootstrap([]raft.Peer{{ID: 1}}))
    drainReady(t, rawNode, storageView)

    cc := raftpb.ConfChangeV2{Changes: []raftpb.ConfChangeSingle{{Type: raftpb.ConfChangeAddNode, NodeID: 2}}}
    data, err := cc.Marshal()
    require.NoError(t, err)
    ready := raft.Ready{CommittedEntries: []raftpb.Entry{{Index: 2, Term: 1, Type: raftpb.EntryConfChangeV2, Data: data}}}
    service := &Service{cfg: Config{StateMachine: sm}}
    latestConfState := raftpb.ConfState{Voters: []uint64{1}}
    applied, err := service.applyReadyState(ctx, rawNode, ready, storageView, &latestConfState, nil)
    require.NoError(t, err)
    require.Equal(t, uint64(2), applied)

    require.NoError(t, service.compactControllerLog(ctx, storageView, applied, latestConfState))
    snap, err := logDB.ForController().Snapshot(ctx)
    require.NoError(t, err)
    require.Equal(t, []uint64{1, 2}, sortedPeers(snap.Metadata.ConfState.Voters))
}
```

If raw raft setup needs real committed bootstrap entries first, add a small `drainReady` helper that persists/advances bootstrap Ready before applying the synthetic ConfChangeV2.

- [ ] **Step 2: Run test to verify it fails or passes for current behavior**

Run: `go test ./pkg/controller/raft -run TestServiceCompactionSnapshotUsesConfChangeV2State -count=1`

Expected: PASS if current implementation already handles ConfChangeV2 correctly; FAIL if helper setup or production code is wrong.

- [ ] **Step 3: Fix production only if needed**

If production changes are required, keep them minimal and maintain existing ConfChange/ConfChangeV2 handling in `applyReadyState`.

- [ ] **Step 4: Run targeted tests**

Run:

```bash
go test ./pkg/controller/raft -run 'TestServiceLaggingFollowerRestoresControllerSnapshot|TestServiceCompactionSnapshotUsesConfChangeV2State' -count=1
```

Expected: PASS.

## Task 3: Verification and commit

- [ ] **Step 1: Run package tests**

Run: `go test ./pkg/controller/raft -count=1`

Expected: PASS.

- [ ] **Step 2: Inspect diff**

Run: `git diff -- pkg/controller/raft/service_test.go`

Expected: only intended tests/helpers plus any minimal production fix if needed.

- [ ] **Step 3: Commit**

```bash
git add pkg/controller/raft/service_test.go pkg/controller/raft/service.go docs/superpowers/plans/2026-05-06-controller-log-compaction-risk-tests.md
git commit -m "test(controllerraft): cover compaction snapshot risks"
```
