# Hash Slot 运维能力 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 `add-slot` / `remove-slot` / `rebalance` 运维命令，运维可以在不停机的前提下动态增减物理 Slot。

**Architecture:** 运维命令通过 Controller Raft 提交，Controller 计算 hash slot 再分配方案，调度迁移 Worker 执行。支持并行迁移多个 hash slot（默认最多 4 个），自动均衡分配。

**Tech Stack:** Go, etcd/raft, CLI

**Depends on:** `2026-04-14-hash-slot-migration.md` 完成

**Design doc:** `docs/wiki/design/slot-auto-scaling.md` §6.2-6.4, §8-9

---

## File Structure

### New files
- `pkg/cluster/rebalancer.go` — 再平衡算法（计算 hash slot 迁移方案）
- `pkg/cluster/rebalancer_test.go`

### Modified files
- `pkg/cluster/operator.go` — 新增 AddSlot / RemoveSlot / Rebalance / MigrateHashSlot 等方法
- `pkg/cluster/operator_test.go`
- `pkg/controller/plane/planner.go` — 跳过迁移中的物理 Slot
- `pkg/controller/plane/commands.go` — 新增 AddSlot / RemoveSlot 命令（CommandKind 14/15）
- `pkg/controller/plane/statemachine.go` — Apply AddSlot / RemoveSlot
- `pkg/cluster/cluster.go` — 注册迁移 Worker 的 Tick 驱动
- `pkg/cluster/managed_slots.go` — 感知迁移，避免同时做副本 Rebalance

---

### Task 1: 再平衡算法

**Files:**
- Create: `pkg/cluster/rebalancer.go`
- Create: `pkg/cluster/rebalancer_test.go`

- [ ] **Step 1: Write rebalancer tests**

```go
func TestComputeAddSlotPlan(t *testing.T) {
    // 4 physical slots, 256 hash slots (each owns 64)
    // Add slot 5 → each existing slot should give ~13 hash slots to slot 5
    table := NewHashSlotTable(256, 4)
    plan := ComputeAddSlotPlan(table, 5)
    
    assert len(plan) == 51 or 52 // ~256/5 ≈ 51
    // After applying plan, each slot should own ~51 hash slots
    for _, slotID := range []SlotID{1,2,3,4,5} {
        count := countAfterPlan(table, plan, slotID)
        assert count >= 50 && count <= 52
    }
}

func TestComputeRemoveSlotPlan(t *testing.T) {
    // 4 slots, remove slot 3 → distribute its 64 hash slots to slots 1,2,4
    table := NewHashSlotTable(256, 4)
    plan := ComputeRemoveSlotPlan(table, 3)
    
    assert len(plan) == 64 // all of slot 3's hash slots
    // After applying, slots 1,2,4 each own ~85
    for _, slotID := range []SlotID{1,2,4} {
        count := countAfterPlan(table, plan, slotID)
        assert count >= 84 && count <= 86
    }
}

func TestComputeRebalancePlan(t *testing.T) {
    // Slot 1 has 100 hash slots, slot 2 has 20 → rebalance
    table := NewHashSlotTableCustom(...)
    plan := ComputeRebalancePlan(table)
    // After applying, each should have ~60
}
```

- [ ] **Step 2: Run tests to verify fail**

Run: `go test ./pkg/cluster/ -run TestCompute -v`

- [ ] **Step 3: Implement rebalancer**

```go
// pkg/cluster/rebalancer.go
type MigrationPlan struct {
    HashSlot uint16
    From     multiraft.SlotID
    To       multiraft.SlotID
}

// ComputeAddSlotPlan: 计算添加新物理 Slot 时需要迁移哪些 hash slot
func ComputeAddSlotPlan(table *HashSlotTable, newSlotID multiraft.SlotID) []MigrationPlan {
    targetPerSlot := int(table.hashSlotCount) / (len(activeSlots) + 1)
    // 从拥有量 > targetPerSlot 的 Slot 取出多余的 hash slot
    // 分配给 newSlotID
    // 优先从拥有最多的 Slot 取
}

// ComputeRemoveSlotPlan: 计算移除物理 Slot 时的 hash slot 分配
func ComputeRemoveSlotPlan(table *HashSlotTable, removeSlotID multiraft.SlotID) []MigrationPlan {
    hashSlots := table.HashSlotsOf(removeSlotID)
    // 按轮询分配给剩余 Slot（优先给拥有最少的）
}

// ComputeRebalancePlan: 不增减 Slot，纯粹做负载均衡
func ComputeRebalancePlan(table *HashSlotTable) []MigrationPlan {
    targetPerSlot := int(table.hashSlotCount) / len(activeSlots)
    // 从超额 Slot 取 hash slot 给不足 Slot
}
```

- [ ] **Step 4: Run tests to verify pass**
- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/rebalancer.go pkg/cluster/rebalancer_test.go
git commit -m "feat: rebalancer computes hash slot migration plans for add/remove/rebalance"
```

---

### Task 2: Controller AddSlot / RemoveSlot 命令

**Files:**
- Modify: `pkg/controller/plane/commands.go`
- Modify: `pkg/controller/plane/statemachine.go`
- Create: `pkg/controller/plane/add_remove_slot_test.go`

- [ ] **Step 1: Add command types**

```go
const (
    CommandKindAddSlot    = 14
    CommandKindRemoveSlot = 15
)

type AddSlotRequest struct {
    NewSlotID  uint64
    Peers      []uint64  // DesiredPeers for new Raft group
}

type RemoveSlotRequest struct {
    SlotID uint64
}
```

- [ ] **Step 2: Implement Apply**

```go
func (sm *StateMachine) applyAddSlot(req *AddSlotRequest) error {
    // 1. 创建 SlotAssignment for new slot
    // 2. 计算迁移方案（ComputeAddSlotPlan）
    // 3. 存储迁移方案到 pending migrations
    // 4. 更新 HashSlotTable: 标记待迁移的 hash slot
}

func (sm *StateMachine) applyRemoveSlot(req *RemoveSlotRequest) error {
    // 1. 计算迁移方案（ComputeRemoveSlotPlan）
    // 2. 存储迁移方案
    // 3. 标记 slot 为 Draining（不再接受新 hash slot）
}
```

- [ ] **Step 3: Write tests**
- [ ] **Step 4: Run tests to verify pass**
- [ ] **Step 5: Commit**

```bash
git add pkg/controller/plane/
git commit -m "feat: controller AddSlot and RemoveSlot commands"
```

---

### Task 3: Operator 运维方法

**Files:**
- Modify: `pkg/cluster/operator.go`
- Modify: `pkg/cluster/operator_test.go`

- [ ] **Step 1: Add operator methods**

```go
// pkg/cluster/operator.go

func (c *Cluster) AddSlot(ctx context.Context) (multiraft.SlotID, error) {
    // 1. 分配新 SlotID
    // 2. 选择 DesiredPeers（与现有 Slot 相同的节点集）
    // 3. 提交 CommandKindAddSlot 到 Controller Raft
    // 4. 等待 Controller 确认
    // 5. 返回新 SlotID
}

func (c *Cluster) RemoveSlot(ctx context.Context, slotID multiraft.SlotID) error {
    // 1. 校验：slotID 存在且不在迁移中
    // 2. 提交 CommandKindRemoveSlot 到 Controller Raft
    // 3. 等待所有 hash slot 迁移完成
    // 4. 关闭 Raft 组
}

func (c *Cluster) Rebalance(ctx context.Context) ([]MigrationPlan, error) {
    // 1. 获取当前 HashSlotTable
    // 2. 计算再平衡方案
    // 3. 提交迁移任务
}

func (c *Cluster) MigrateHashSlot(ctx context.Context, hs uint16, target multiraft.SlotID) error {
    // 手动迁移单个 hash slot
}

func (c *Cluster) GetHashSlotTable() *HashSlotTable { ... }
func (c *Cluster) GetMigrationStatus() []*HashSlotMigration { ... }
func (c *Cluster) AbortMigration(ctx context.Context) error { ... }
```

- [ ] **Step 2: Write tests**

```go
func TestOperator_AddSlot(t *testing.T) {
    cluster := startTestCluster(t, 3, WithInitialSlots(4))
    newSlotID, err := cluster.AddSlot(ctx)
    assert no error
    assert newSlotID == 5
    
    // Wait for migrations to complete
    waitForNoActiveMigrations(t, cluster, 60*time.Second)
    
    // Verify: 5 slots, each with ~51 hash slots
    table := cluster.GetHashSlotTable()
    for slotID := 1; slotID <= 5; slotID++ {
        count := len(table.HashSlotsOf(SlotID(slotID)))
        assert count >= 49 && count <= 53
    }
}
```

- [ ] **Step 3: Run tests to verify pass**
- [ ] **Step 4: Commit**

```bash
git add pkg/cluster/operator.go pkg/cluster/operator_test.go
git commit -m "feat: AddSlot, RemoveSlot, Rebalance operator methods"
```

---

### Task 4: Planner 跳过迁移中的 Slot

**Files:**
- Modify: `pkg/controller/plane/planner.go`

- [ ] **Step 1: Update NextDecision to skip migrating slots**

在 `ReconcileSlot`（planner.go:22-91）和 `nextRebalanceDecision`（planner.go:107-166）中，增加检查：

```go
// 如果该 Slot 有任何 hash slot 正在迁移，跳过 Repair/Rebalance
if sm.HasActiveMigrations(slotID) {
    continue
}
```

- [ ] **Step 2: Write test**

```go
func TestPlanner_SkipsMigratingSlots(t *testing.T) {
    // Setup: slot 2 has an active migration
    // Planner should skip slot 2 for repair/rebalance
    state := testPlannerState()
    state.Migrations[2] = &HashSlotMigration{...}
    decision := planner.ReconcileSlot(state, 2)
    assert decision == nil
}
```

- [ ] **Step 3: Run test to verify pass**
- [ ] **Step 4: Commit**

```bash
git add pkg/controller/plane/planner.go
git commit -m "fix: planner skips slots with active hash slot migrations"
```

---

### Task 5: 并行迁移调度

**Files:**
- Modify: `pkg/cluster/slotmigration/worker.go`
- Modify: `pkg/cluster/cluster.go` — 注册 Worker Tick

- [ ] **Step 1: Implement parallel migration scheduling**

```go
const (
    MaxConcurrentMigrations    = 4  // 全局最多同时迁移 4 个 hash slot
    MaxConcurrentPerSlot       = 2  // 单个物理 Slot 同时最多迁出 2 个
    MigrationStallTimeout      = 10 * time.Minute
)

func (w *Worker) SchedulePlan(plan []MigrationPlan) {
    // 按照 MaxConcurrent 限制，分批调度
    // 不同源 Slot 的迁移可并行
    // 同一源 Slot 受 MaxConcurrentPerSlot 限制
}
```

- [ ] **Step 2: Wire Worker.Tick into cluster main loop**

在 `cluster.go` 的观察循环（200ms Tick）中调用 `migrationWorker.Tick()`。

- [ ] **Step 3: Write test for parallel scheduling**

```go
func TestWorker_ParallelMigration(t *testing.T) {
    // Plan with 8 migrations from 4 different source slots
    // Should execute 4 in parallel (one per source slot)
    // Then next 4 after first batch completes
}
```

- [ ] **Step 4: Run tests to verify pass**
- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/slotmigration/worker.go pkg/cluster/cluster.go
git commit -m "feat: parallel hash slot migration with concurrency limits"
```

---

### Task 6: 端到端 AddSlot / RemoveSlot 集成测试

**Files:**
- Create: `pkg/cluster/operator_integration_test.go`

- [ ] **Step 1: Write AddSlot integration test**

```go
func TestAddSlot_Integration(t *testing.T) {
    cluster := startTestCluster(t, 3, WithInitialSlots(4), WithHashSlotCount(64))

    // Write data to multiple channels
    for i := 0; i < 100; i++ {
        cluster.Store().CreateChannel(ctx, fmt.Sprintf("ch-%d", i), ...)
    }

    // Add a slot
    newSlotID, err := cluster.AddSlot(ctx)
    require.NoError(t, err)

    // Wait for all migrations to finish
    waitForNoActiveMigrations(t, cluster, 60*time.Second)

    // Verify all data still accessible
    for i := 0; i < 100; i++ {
        ch, err := cluster.Store().GetChannel(ctx, fmt.Sprintf("ch-%d", i), 1)
        require.NoError(t, err)
        assert.Equal(t, fmt.Sprintf("ch-%d", i), ch.ChannelID)
    }

    // Verify balanced distribution
    table := cluster.GetHashSlotTable()
    for slotID := 1; slotID <= 5; slotID++ {
        count := len(table.HashSlotsOf(SlotID(slotID)))
        t.Logf("Slot %d: %d hash slots", slotID, count)
        assert count >= 10 && count <= 15 // 64/5 ≈ 13
    }
}
```

- [ ] **Step 2: Write RemoveSlot integration test**

```go
func TestRemoveSlot_Integration(t *testing.T) {
    // Start with 5 slots, remove one
    // Verify data intact and redistributed
}
```

- [ ] **Step 3: Write Rebalance integration test**

```go
func TestRebalance_Integration(t *testing.T) {
    // Create imbalanced table (manually assign more hash slots to one slot)
    // Call Rebalance
    // Verify even distribution after
}
```

- [ ] **Step 4: Run all integration tests**

```bash
go test ./pkg/cluster/ -run TestAddSlot_Integration -v -count=1
go test ./pkg/cluster/ -run TestRemoveSlot_Integration -v -count=1
go test ./pkg/cluster/ -run TestRebalance_Integration -v -count=1
```

- [ ] **Step 5: Commit**

```bash
git add pkg/cluster/operator_integration_test.go
git commit -m "test: end-to-end integration tests for add-slot, remove-slot, rebalance"
```

---

### Task 7: HashSlotTable 心跳下发

**Files:**
- Modify: `pkg/cluster/cluster.go` — Agent 心跳携带 HashSlotTable version
- Modify: `pkg/controller/plane/commands.go` — HeartbeatResponse 增加 HashSlotTable
- Modify: `pkg/cluster/managed_slots.go` — 节点侧接收更新

- [ ] **Step 1: Extend heartbeat response**

```go
type HeartbeatResponse struct {
    // ...existing...
    HashSlotTableVersion uint64
    HashSlotTable        *HashSlotTable // nil if version matches
}
```

- [ ] **Step 2: Agent reports its local version**

Agent 心跳上报 `localHashSlotTableVersion`。Controller 比对版本，落后则下发全量。

- [ ] **Step 3: Node applies received table**

节点收到新 HashSlotTable 后调用 `router.UpdateHashSlotTable(table)`。

- [ ] **Step 4: Write test**

```go
func TestHashSlotTable_PropagatesViaHeartbeat(t *testing.T) {
    cluster := startTestCluster(t, 3)
    // Trigger a migration on controller
    // Wait for heartbeat cycle (200ms)
    // Verify all nodes have updated HashSlotTable version
}
```

- [ ] **Step 5: Run test to verify pass**
- [ ] **Step 6: Commit**

```bash
git add pkg/cluster/ pkg/controller/plane/
git commit -m "feat: HashSlotTable propagation via agent heartbeat"
```

---

### Task 8: 架构文档更新

**Files:**
- Modify: `docs/wiki/architecture/02-slot-layer.md` — 路由算法、Key 编码、hash slot 概念
- Modify: `docs/wiki/architecture/01-controller-layer.md` — 新增命令、迁移调度
- Modify: `docs/wiki/architecture/05-message-sending-flow.md` — 寻址流程从取模改为查表
- Modify: `docs/wiki/architecture/README.md` — 术语表新增 Hash Slot

- [ ] **Step 1: Update 02-slot-layer.md**

更新第 2 章（代码结构）、第 5 章（路由算法）、第 7 章（Managed Slots 生命周期）反映 HashSlotTable 和迁移流程。

- [ ] **Step 2: Update 01-controller-layer.md**

更新 Planner 决策列表、新增命令表（AddSlot, RemoveSlot, StartMigration 等）、迁移任务执行流程。

- [ ] **Step 3: Update 05-message-sending-flow.md**

更新寻址章节：`CRC32 % N` → `CRC32 % HashSlotCount → HashSlotTable → 物理 Slot`。

- [ ] **Step 4: Update README.md**

术语表新增：
- Hash Slot: 固定路由分区，哈希空间的最细粒度
- Physical Slot: Raft 组，托管一组 hash slot
- HashSlotTable: hash slot 到物理 Slot 的映射表

- [ ] **Step 5: Commit**

```bash
git add docs/wiki/architecture/
git commit -m "docs: update architecture docs for hash slot routing model"
```
