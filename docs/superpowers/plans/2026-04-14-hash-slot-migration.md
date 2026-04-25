# Hash Slot 迁移执行器 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现单个 hash slot 从一个物理 Slot 在线迁移到另一个物理 Slot 的完整流程（快照 + 增量双写 + 切流），并通过 Controller 命令驱动。

**Architecture:** 迁移分四阶段：Snapshot（全量导出）→ Delta（增量双写）→ Switching（原子切流）→ Done（清理）。迁移 Worker 在节点侧执行，Controller 管理状态推进和 HashSlotTable 版本变更。

**Tech Stack:** Go, Pebble, etcd/raft, Transport RPC

**Depends on:** `2026-04-14-hash-slot-foundation.md` 完成

**Design doc:** `docs/wiki/design/slot-auto-scaling.md` §6

---

## File Structure

### New files
- `pkg/cluster/slotmigration/worker.go` — 迁移主循环、状态机
- `pkg/cluster/slotmigration/snapshot.go` — 单 hash slot 快照导出/导入
- `pkg/cluster/slotmigration/delta.go` — 增量双写转发 + 去重
- `pkg/cluster/slotmigration/progress.go` — 进度跟踪与上报
- `pkg/cluster/slotmigration/worker_test.go` — 单元测试
- `pkg/slot/fsm/migration_cmds.go` — 迁移相关 FSM 命令（IngestSnapshot, ApplyDelta, EnterFence）

### Modified files
- `pkg/cluster/hashslottable.go` — 增加 Migration 字段、Phase 管理
- `pkg/controller/plane/commands.go` — 新增迁移命令
- `pkg/controller/plane/statemachine.go` — Apply 迁移命令
- `pkg/storage/controllermeta/store.go` — HashSlotTable 持久化（Pebble Key: `hash_slot_table`）
- `pkg/slot/fsm/statemachine.go` — Apply 时检测迁移中的 hash slot，执行双写转发
- `pkg/cluster/cluster.go` — 注册迁移 RPC handler
- `pkg/cluster/managed_slots.go` — 感知迁移状态

---

### Task 1: HashSlotTable 迁移状态扩展

**Files:**
- Modify: `pkg/cluster/hashslottable.go`
- Modify: `pkg/cluster/hashslottable_test.go`

- [ ] **Step 1: Write tests for migration state**

```go
func TestHashSlotTable_Migration(t *testing.T) {
    table := NewHashSlotTable(256, 4)
    table.StartMigration(64, 2, 5) // hash slot 64: slot 2 → slot 5
    mig := table.GetMigration(64)
    assert mig.Source == 2
    assert mig.Target == 5
    assert mig.Phase == PhaseSnapshot

    table.AdvanceMigration(64, PhaseDelta)
    assert table.GetMigration(64).Phase == PhaseDelta

    table.FinalizeMigration(64) // Assignment[64] = 5, migration cleared
    assert table.Lookup(64) == 5
    assert table.GetMigration(64) == nil
}
```

- [ ] **Step 2: Run tests to verify fail**

Run: `go test ./pkg/cluster/ -run TestHashSlotTable_Migration -v`

- [ ] **Step 3: Implement migration state in HashSlotTable**

```go
type MigrationPhase uint8
const (
    PhaseSnapshot  MigrationPhase = iota
    PhaseDelta
    PhaseSwitching
    PhaseDone
)

type HashSlotMigration struct {
    HashSlot uint16
    Source   multiraft.SlotID
    Target   multiraft.SlotID
    Phase    MigrationPhase
}

// Add to HashSlotTable:
// migrations map[uint16]*HashSlotMigration
func (t *HashSlotTable) StartMigration(hs uint16, source, target multiraft.SlotID) { ... }
func (t *HashSlotTable) AdvanceMigration(hs uint16, phase MigrationPhase) { ... }
func (t *HashSlotTable) FinalizeMigration(hs uint16) { ... }
func (t *HashSlotTable) AbortMigration(hs uint16) { ... }
func (t *HashSlotTable) GetMigration(hs uint16) *HashSlotMigration { ... }
func (t *HashSlotTable) ActiveMigrations() []*HashSlotMigration { ... }
```

- [ ] **Step 4: Run tests to verify pass**
- [ ] **Step 5: Commit**

---

### Task 2: 单 Hash Slot 快照导出/导入

**Files:**
- Create: `pkg/cluster/slotmigration/snapshot.go`
- Create: `pkg/cluster/slotmigration/snapshot_test.go`

- [ ] **Step 1: Write snapshot test**

```go
func TestExportImportSingleHashSlot(t *testing.T) {
    db := openTestDB(t)
    // Write data to hash slot 5
    wb := db.NewWriteBatch()
    wb.UpsertChannel(5, Channel{ChannelID: "ch1", ChannelType: 1})
    wb.UpsertUser(5, User{UID: "u1"})
    wb.Commit()

    // Export hash slot 5
    data, err := ExportHashSlot(db, 5)
    assert no error
    assert data.Stats.EntryCount > 0

    // Import into clean db (simulating target slot)
    db2 := openTestDB(t)
    err = ImportHashSlot(db2, data)
    assert no error

    // Verify data is readable in db2
    ch, err := db2.ForHashSlot(5).GetChannel("ch1", 1)
    assert ch.ChannelID == "ch1"
}
```

- [ ] **Step 2: Run test to verify fail**
- [ ] **Step 3: Implement ExportHashSlot / ImportHashSlot**

```go
// pkg/cluster/slotmigration/snapshot.go
func ExportHashSlot(db *metadb.DB, hashSlot uint16) (*metadb.SlotSnapshot, error) {
    return db.ExportHashSlotSnapshot([]uint16{hashSlot})
}

func ImportHashSlot(db *metadb.DB, snap *metadb.SlotSnapshot) error {
    return db.ImportHashSlotSnapshot(*snap)
}
```

这层薄封装方便后续加入 RPC 传输和 chunk 分片。

- [ ] **Step 4: Run test to verify pass**
- [ ] **Step 5: Commit**

---

### Task 3: FSM 迁移命令

**Files:**
- Create: `pkg/slot/fsm/migration_cmds.go`
- Modify: `pkg/slot/fsm/command.go` — 注册新命令到 commandDecoders
- Modify: `pkg/slot/fsm/statemachine.go` — Apply 时感知迁移

- [ ] **Step 1: Define migration command types**

```go
// pkg/slot/fsm/migration_cmds.go
const (
    cmdTypeApplyDelta      = 20  // 目标 Slot Apply 源转发的增量命令
    cmdTypeEnterFence      = 21  // 进入切流窗口
)

type applyDeltaCmd struct {
    SourceSlotID   multiraft.SlotID
    SourceIndex    uint64          // 幂等去重 key
    OriginalCmd    []byte          // 原始命令 data
    HashSlot       uint16
}

func (c *applyDeltaCmd) apply(wb *metadb.WriteBatch, hashSlot uint16) error {
    // 解码并 apply 原始命令
    decoded := decodeCommand(c.OriginalCmd)
    return decoded.apply(wb, hashSlot)
}
```

- [ ] **Step 2: Add migration forwarding to StateMachine**

在 `statemachine.go` 的 `ApplyBatch` 中增加迁移双写逻辑：

```go
// 在正常 apply 之后
if m.isMigrating(cmd.HashSlot) {
    mig := m.getMigration(cmd.HashSlot)
    if mig.Phase <= PhaseDelta {
        m.forwardDelta(mig.Target, cmd) // 异步转发
    }
}
```

- [ ] **Step 3: Write tests for delta forwarding**

```go
func TestStateMachine_ForwardsDeltaDuringMigration(t *testing.T) {
    // Setup source SM with hash slot 5 in migration
    // Apply a command to hash slot 5
    // Verify forwardDelta was called with correct target
}
```

- [ ] **Step 4: Run tests to verify pass**
- [ ] **Step 5: Commit**

```bash
git add pkg/slot/fsm/migration_cmds.go pkg/slot/fsm/command.go pkg/slot/fsm/statemachine.go
git commit -m "feat: FSM migration commands and delta forwarding"
```

---

### Task 4: 迁移 Worker 状态机

**Files:**
- Create: `pkg/cluster/slotmigration/worker.go`
- Create: `pkg/cluster/slotmigration/progress.go`
- Create: `pkg/cluster/slotmigration/worker_test.go`

- [ ] **Step 1: Define Worker interface and state machine**

```go
// pkg/cluster/slotmigration/worker.go
type Worker struct {
    cluster    *raftcluster.Cluster
    db         *metadb.DB
    migrations map[uint16]*activeMigration
}

type activeMigration struct {
    hashSlot   uint16
    source     multiraft.SlotID
    target     multiraft.SlotID
    phase      MigrationPhase
    snapshotAt uint64  // source Raft ApplyIndex at snapshot time
    progress   *Progress
}

func (w *Worker) StartMigration(hs uint16, source, target multiraft.SlotID) error { ... }
func (w *Worker) Tick() { ... }  // 推进所有活跃迁移
func (w *Worker) AbortMigration(hs uint16) error { ... }
```

- [ ] **Step 2: Implement phase transitions in Tick**

```go
func (w *Worker) tickMigration(m *activeMigration) {
    switch m.phase {
    case PhaseSnapshot:
        // Export hash slot from source, send to target via RPC
        // On completion → PhaseDelta
    case PhaseDelta:
        // Check delta lag via progress tracker
        // If lag < threshold for stableWindow → PhaseSwitching
    case PhaseSwitching:
        // Notify controller to finalize (atomic assignment switch)
        // On confirmation → PhaseDone
    case PhaseDone:
        // Delete source data, report to controller, remove from active
    }
}
```

- [ ] **Step 3: Implement Progress tracker**

```go
// pkg/cluster/slotmigration/progress.go
type Progress struct {
    SourceApplyIndex    uint64
    TargetApplyIndex    uint64
    DeltaLag            int64
    StableWindowStart   time.Time
    BytesTransferred    int64
}

func (p *Progress) IsStable(threshold int64, window time.Duration) bool {
    return p.DeltaLag < threshold &&
           time.Since(p.StableWindowStart) > window
}
```

- [ ] **Step 4: Write tests**

```go
func TestWorker_MigrationPhaseTransitions(t *testing.T) {
    // Test: PhaseSnapshot → PhaseDelta → PhaseSwitching → PhaseDone
}

func TestProgress_IsStable(t *testing.T) {
    p := &Progress{DeltaLag: 50, StableWindowStart: time.Now().Add(-time.Second)}
    assert p.IsStable(100, 500*time.Millisecond) == true
}
```

- [ ] **Step 5: Run tests to verify pass**
- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/slotmigration/
git commit -m "feat: migration worker with phase state machine and progress tracking"
```

---

### Task 5: Controller HashSlotTable 持久化 + 迁移命令

**Files:**
- Modify: `pkg/storage/controllermeta/store.go` — 新增 HashSlotTable 持久化方法
- Modify: `pkg/controller/plane/commands.go` — 新增 Migration 命令类型
- Modify: `pkg/controller/plane/statemachine.go` — Apply 迁移命令 + HashSlotTable 存取
- Create: `pkg/controller/plane/migration_test.go`

> **前置依赖：** Controller 的 StateMachine 使用 `controllermeta.Store`（Pebble）持久化数据。
> HashSlotTable 需要通过该 Store 持久化，以便 Controller Leader 切换后能从 Pebble 恢复。

- [ ] **Step 0: Add HashSlotTable persistence to controllermeta.Store**

```go
// pkg/storage/controllermeta/store.go
func (s *Store) SaveHashSlotTable(table *HashSlotTable) error {
    data := table.Encode()
    return s.db.Set([]byte("hash_slot_table"), data, pebble.Sync)
}

func (s *Store) LoadHashSlotTable() (*HashSlotTable, error) {
    data, closer, err := s.db.Get([]byte("hash_slot_table"))
    // ...
    return DecodeHashSlotTable(data)
}
```

- [ ] **Step 1: Add command types**

```go
// pkg/controller/plane/commands.go
const (
    // ... existing ...
    CommandKindStartMigration    = 10
    CommandKindAdvanceMigration  = 11
    CommandKindFinalizeMigration = 12
    CommandKindAbortMigration    = 13
)

type MigrationRequest struct {
    HashSlot uint16
    Source   uint64  // SlotID
    Target   uint64  // SlotID
    Phase    uint8
}
```

- [ ] **Step 2: Implement Apply for migration commands**

在 `statemachine.go` 中增加 case：

```go
case CommandKindStartMigration:
    return sm.applyStartMigration(cmd.Migration)
case CommandKindFinalizeMigration:
    return sm.applyFinalizeMigration(cmd.Migration)
// ...
```

`applyFinalizeMigration` 是关键：原子更新 HashSlotTable 的 Assignment[hs] = Target，Version++。

- [ ] **Step 3: Write tests**
- [ ] **Step 4: Run tests to verify pass**
- [ ] **Step 5: Commit**

```bash
git add pkg/controller/plane/
git commit -m "feat: controller commands for hash slot migration lifecycle"
```

---

### Task 6: 端到端迁移集成测试

**Files:**
- Create: `pkg/cluster/migration_integration_test.go`

- [ ] **Step 1: Write integration test**

```go
func TestMigrateHashSlot_EndToEnd(t *testing.T) {
    cluster := startTestCluster(t, 3) // 3 nodes, 4 physical slots, 256 hash slots

    // 1. Write data to a channel that hashes to hash slot X
    channelID := "test-channel"
    hashSlot := HashSlotForKey(channelID, 256)
    sourceSlot := cluster.Router().SlotForKey(channelID)

    cluster.Store().CreateChannel(ctx, channelID, ...)
    
    // 2. Start migration: hash slot X from sourceSlot to targetSlot
    targetSlot := pickDifferentSlot(sourceSlot)
    cluster.StartMigration(hashSlot, sourceSlot, targetSlot)

    // 3. Wait for migration to complete
    waitForMigrationDone(t, cluster, hashSlot, 30*time.Second)

    // 4. Verify: data is now readable from target slot
    newSlot := cluster.Router().SlotForKey(channelID)
    assert newSlot == targetSlot

    ch, err := cluster.Store().GetChannel(ctx, channelID, 1)
    assert ch.ChannelID == channelID
    
    // 5. Verify: writes during migration were not lost
    // (write more data during step 3 and verify it's present)
}
```

- [ ] **Step 2: Run integration test**

Run: `go test ./pkg/cluster/ -run TestMigrateHashSlot_EndToEnd -v -count=1`

- [ ] **Step 3: Fix any issues**
- [ ] **Step 4: Commit**

```bash
git add pkg/cluster/migration_integration_test.go
git commit -m "test: end-to-end hash slot migration integration test"
```
