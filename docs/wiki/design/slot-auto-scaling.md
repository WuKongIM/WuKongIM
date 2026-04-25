# Slot 层动态扩缩容设计

> 状态：Design Proposal / 待评审
> 范围：L2 Slot 元数据层、L1 Controller 控制层、路由层
> 目标：Slot 数量从启动时固定改为运行时可由运维动态增减，支持在线数据迁移、负载再平衡。

## 1. 背景与问题

### 1.1 现状

1. **SlotCount 启动时固定**：`Config.SlotCount` 在集群初始化时确定，整个生命周期不可变（`pkg/cluster/config.go:25`）。
2. **CRC32 取模路由**：`SlotForKey(key) = CRC32(key) % SlotCount + 1`（`pkg/cluster/router.go:23-29`）。SlotCount 一变，所有 Key 映射全变。
3. **Key 前缀带 SlotID**：Pebble Key 格式 `[keyspace:1][slotID:8][tableID:4][业务字段...]`（`pkg/slot/meta/codec.go`）。路由单元和复制单元绑死。
4. **现有 Rebalance 只动副本**：`managed_slots.go` 只做副本在节点间的迁移，不涉及数据在 Slot 间重新分配。

### 1.2 问题

- 集群节点增加后，Slot 数量不变，新节点无法分担元数据负载。
- 早期部署预设 SlotCount 过大浪费资源，过小后期无法扩展。
- 热点 Slot 无法拆分，冷 Slot 无法合并。
- 没有不增减 Slot 数量就能做负载再平衡的手段。

### 1.3 根本原因

**路由单元和复制单元是同一个东西。** Key 通过 `CRC32 % SlotCount` 直接映射到一个物理 Slot（Raft 组），Slot 既是路由的终点也是数据的容器。这意味着任何路由拓扑变更都等于 Raft 组拓扑变更，代价极大。

## 2. 核心思想：解耦路由单元与复制单元

引入两个独立的概念：

| 概念 | 定义 | 数量 | 生命周期 |
|------|------|------|----------|
| **Hash Slot**（路由单元） | 哈希空间的固定分区，`CRC32(key) % HashSlotCount` | 固定（如 256） | 永不变 |
| **物理 Slot**（复制单元） | 一个 Raft 组，托管一组 hash slot 的数据 | 动态 | 运维可增减 |

```
                  固定映射                      动态映射（Controller 管理）
Key ──CRC32──▶ Hash Slot ──映射表──▶ 物理 Slot（Raft 组）
               (0~255)              (运行时可变)
```

- Hash slot 数量固定且足够多（默认 256），决定了分片的最细粒度。
- 物理 Slot 数量可变，每个物理 Slot 拥有若干个 hash slot。
- 数据的 Pebble Key 以 **hash slot ID** 为前缀（而非物理 Slot ID）。
- 迁移的粒度是单个 hash slot——小、快、可并行。

### 2.1 与上一版设计（直接哈希范围 Split/Merge）的对比

| 维度 | 直接哈希范围 | 固定 Hash Slot |
|------|-------------|---------------|
| 迁移单元 | 半个 Slot（大，慢） | 单个 hash slot（小，快） |
| 迁移并行度 | 同一时刻只能做一个 | 多个 hash slot 可同时迁移 |
| 扩容方式 | 只能从一个 Slot 对半拆 | 从多个 Slot 各取几个 hash slot，负载更均匀 |
| 缩容方式 | 只能合并到相邻 Slot | 分散到任意 Slot |
| 再平衡 | 必须增减 Slot 数量 | 只挪 hash slot，物理 Slot 数不变 |
| 数据倾斜 | 对半切不保证均匀 | 按 hash slot 粒度精确调整 |
| 迁移写放大 | 源 Slot 整个迁出范围双写 | 只有被迁移的 hash slot 双写 |

## 3. 数据模型

### 3.1 Hash Slot 映射表（Controller Raft 状态机管理）

```go
type HashSlotTable struct {
    Version     uint64                         // 单调递增，每次变更 +1
    Assignment  [HashSlotCount]SlotID          // hashSlot → 物理 SlotID
    Migrations  map[uint16]*HashSlotMigration  // 正在迁移中的 hash slot
}

type HashSlotMigration struct {
    HashSlot   uint16
    Source     SlotID           // 迁出方（当前 Owner）
    Target     SlotID           // 迁入方
    Phase      MigrationPhase   // Snapshot / Delta / Switching / Done
    StartedAt  int64
}

type MigrationPhase uint8
const (
    PhaseSnapshot  MigrationPhase = iota  // 全量快照传输中
    PhaseDelta                             // 增量追赶中
    PhaseSwitching                         // 切流中
    PhaseDone                              // 完成，待清理
)
```

- `Assignment` 是一个固定大小数组（256 个条目），总共 256 × 4 = 1KB，全量下发无压力。
- `Migrations` 记录正在进行的迁移，用于迁移恢复和状态跟踪。

**不变量**：
- `Assignment` 中的每个 SlotID 必须指向一个活跃的物理 Slot。
- 一个 hash slot 同一时刻最多有一个 Migration。
- 迁移完成前 `Assignment[hs]` 保持为 Source（读写仍路由到源），Switching 阶段原子切换。

### 3.2 Pebble Key 编码

```
当前：[keyspace:1][slotID:8][tableID:4][业务字段...]
新：  [keyspace:1][hashSlot:2][tableID:4][业务字段...]
```

- `hashSlot` 用 uint16 大端编码（2 字节），支持最多 65535 个 hash slot。
- 物理 Slot ID **不出现在 Key 中**。数据属于 hash slot，物理 Slot 只是托管者。
- Key 反而比之前短了 6 字节（slotID:8 → hashSlot:2）。
- 同一物理 Slot 拥有的多个 hash slot 的数据在 Pebble 中**不连续**（按 hashSlot 前缀各自聚集），但这对 Pebble 的前缀迭代没有问题。

### 3.3 物理 Slot 与 Hash Slot 的关系

```
物理 Slot-1（Raft 组）拥有 hash slot: {0, 1, 2, ..., 63}
物理 Slot-2（Raft 组）拥有 hash slot: {64, 65, ..., 127}
物理 Slot-3（Raft 组）拥有 hash slot: {128, 129, ..., 191}
物理 Slot-4（Raft 组）拥有 hash slot: {192, 193, ..., 255}

添加物理 Slot-5 → 从每个现有 Slot 各取 13 个 hash slot 给 Slot-5
移除物理 Slot-4 → 把 {192..255} 分散给 Slot-1/2/3
再平衡 → 从 Slot-1 挪 5 个 hash slot 给 Slot-3，物理 Slot 数量不变
```

## 4. 路由

### 4.1 路由算法

```go
func (r *Router) SlotForKey(key string) SlotID {
    hs := uint16(crc32.ChecksumIEEE([]byte(key)) % HashSlotCount)
    return r.hashSlotTable.Load().Assignment[hs]
}

func (r *Router) HashSlotForKey(key string) uint16 {
    return uint16(crc32.ChecksumIEEE([]byte(key)) % HashSlotCount)
}
```

- `HashSlotCount` 固定（默认 256），`CRC32 % 256` 得到 hash slot。
- 查 `Assignment[hs]` 得到物理 SlotID，O(1) 数组下标访问。
- `hashSlotTable` 是原子指针，整体替换，读路径无锁。

### 4.2 映射表下发

复用 Agent 心跳通道（200ms 周期）：

```go
type HeartbeatResponse struct {
    // ...existing fields...
    HashSlotTableVersion uint64
    HashSlotTable        *HashSlotTable  // 版本落后时携带全量（~1KB）
}
```

## 5. 物理 Slot 的 StateMachine 改动

### 5.1 命令携带 Hash Slot

```go
// 改前
type Command struct {
    SlotID  SlotID   // 物理 Slot ID
    Index   uint64
    Data    []byte
}

// 改后
type Command struct {
    SlotID    SlotID   // 物理 Slot ID（Raft 组标识）
    HashSlot  uint16   // 该命令属于哪个 hash slot
    Index     uint64
    Data      []byte
}
```

### 5.2 Apply 逻辑

```go
func (m *stateMachine) ApplyBatch(ctx context.Context, cmds []Command) ([][]byte, error) {
    wb := m.db.NewWriteBatch()
    for _, cmd := range cmds {
        // 1. 验证 hash slot 归属
        if !m.ownsHashSlot(cmd.HashSlot) {
            // 不拥有的 hash slot：可能是迁移完成后的残留命令，跳过
            continue
        }
        // 2. 正常 Apply
        decoded := decodeCommand(cmd.Data)
        decoded.apply(wb, cmd.HashSlot)  // 用 hashSlot 替代原来的 slotID

        // 3. 如果该 hash slot 正在迁出，同时转发给 Target
        if mig := m.getMigration(cmd.HashSlot); mig != nil && mig.Phase <= PhaseDelta {
            m.forwardToTarget(mig.Target, cmd)
        }
    }
    return results, wb.Commit()
}
```

### 5.3 快照

一个物理 Slot 生成 Raft Snapshot 时，需要收集它拥有的所有 hash slot 的数据：

```go
func (m *stateMachine) Snapshot() ([]byte, error) {
    snapshot := m.db.pebble.NewSnapshot()
    defer snapshot.Close()
    for _, hs := range m.ownedHashSlots {
        // 迭代 [keyspace][hs] 前缀下的所有数据
        iterateHashSlotData(snapshot, hs, writer)
    }
    return writer.Finish()
}
```

迁移单个 hash slot 时，只导出该 hash slot 的数据（小得多）：

```go
func (m *stateMachine) ExportHashSlot(hs uint16) ([]byte, error) {
    snapshot := m.db.pebble.NewSnapshot()
    defer snapshot.Close()
    iterateHashSlotData(snapshot, hs, writer)
    return writer.Finish()
}
```

## 6. 迁移流程

### 6.1 单个 Hash Slot 迁移（核心流程）

把 hash slot `H` 从物理 Slot `A` 迁移到物理 Slot `B`：

```
Phase 1: Snapshot
  A 对 hash slot H 的数据生成 Pebble Snapshot
  → 通过 RPC 流式发送给 B（按 4MB chunk）
  → B 通过 IngestExternalFile 原子导入（Key 不变，因为 Key 前缀是 hashSlot 而非 SlotID）
  → 记录快照时 A 的 RaftApplyIndex = X

Phase 2: Delta
  A 的 StateMachine Apply 时，凡 HashSlot == H 的命令：
    本地 Apply + 转发给 B 的 Raft（Propose）
  B 的 StateMachine Apply 转发命令时通过 (sourceSlotID, sourceIndex) 去重
  当 B 追上 A（lag < 100 且稳定 500ms）→ 通知 Controller

Phase 3: Switching
  Controller 通过 Raft 提交 CmdFinalizeHashSlotMigration：
    Assignment[H] = B  （原子切换）
    Version++
  A 收到新 Table 后，对 hash slot H 的写入返回 ErrRerouted
  B 收到新 Table 后，开始正常接受 hash slot H 的写入
  客户端收到 ErrRerouted → 拉新 Table → 重试（已有 sendWithMetaRefreshRetry 机制）

Phase 4: Done
  A 异步 DeleteRange 清理 hash slot H 的数据
  上报 Controller，清除 Migration 记录
```

**关键优势**：Key 前缀是 `hashSlot:2` 而非 `slotID:8`，所以数据从 A 迁到 B 后 Key 不需要改写。Pebble IngestExternalFile 可以直接导入。

### 6.2 AddSlot（添加物理 Slot）

运维执行：`wk operator add-slot`

```
1. Controller 创建新物理 Slot（Raft 组），等待 Bootstrap 完成
2. Controller 执行再平衡算法，计算需要从现有 Slot 迁移哪些 hash slot 给新 Slot
   （目标：迁移后各物理 Slot 拥有的 hash slot 数量尽量均匀）
3. 对选中的每个 hash slot 执行 §6.1 的迁移流程
4. 可并行迁移多个 hash slot（来自不同源 Slot 的互不冲突）
```

示例：4 个物理 Slot 各拥有 64 个 hash slot，添加第 5 个 Slot：
- 从每个现有 Slot 各迁出 ~13 个 hash slot（总共 ~51 个）给新 Slot
- 4 路并行迁移（每个源 Slot 同时迁出自己的 hash slot）
- 每个 hash slot 只有约 1/256 的总数据量，单个迁移很快

### 6.3 RemoveSlot（移除物理 Slot）

运维执行：`wk operator remove-slot --slot <slotID>`

```
1. Controller 计算被移除 Slot 的所有 hash slot 应分配给哪些目标 Slot
   （目标：分配后各 Slot 负载尽量均匀）
2. 对每个 hash slot 执行迁移
3. 可并行迁移到不同的目标 Slot
4. 全部迁移完成后，关闭该物理 Slot 的 Raft 组
```

### 6.4 Rebalance（再平衡，不增减物理 Slot）

运维执行：`wk operator rebalance`

```
1. Controller 计算当前各物理 Slot 的负载（hash slot 数量 / 数据量 / QPS）
2. 找出 hash slot 需要从哪些重负载 Slot 迁往轻负载 Slot
3. 执行迁移
```

这是前一版设计完全做不到的能力。

## 7. 组件协作总览

```
┌──────────────────────────────────────────────────────────────────┐
│                  L1 · Controller                                 │
│                                                                  │
│  ┌──────────────┐   ┌────────────────┐   ┌──────────────────┐  │
│  │ HashSlotTable │   │ Planner        │   │ MigrationManager │  │
│  │ (Raft 状态机) │◀──│ (再平衡算法)   │──▶│ (调度迁移任务)   │  │
│  └──────┬───────┘   └────────────────┘   └────────┬─────────┘  │
│         │                                          │             │
└─────────┼──────────────────────────────────────────┼─────────────┘
          │ 心跳 piggyback 下发                       │ 迁移任务下发
          ▼                                          ▼
┌──────────────────────────────────────────────────────────────────┐
│                  每个节点                                         │
│                                                                  │
│  ┌───────────────────┐      ┌──────────────────────────────┐    │
│  │ HashSlotTable 缓存 │      │ MigrationWorker              │    │
│  │ (原子指针，无锁读)  │      │ (快照 / 增量 / 切流)          │    │
│  └─────────┬─────────┘      └──────────┬───────────────────┘    │
│            │                            │                        │
│            ▼                            ▼                        │
│  ┌───────────────────────────────────────────────────────┐      │
│  │ Router.SlotForKey(key)                                │      │
│  │   CRC32(key) % 256 → hashSlot → Assignment → SlotID  │      │
│  └───────────────────────────────────────────────────────┘      │
│                         │                                        │
│           ┌─────────────┼──────────────┐                        │
│           ▼             ▼              ▼                        │
│     ┌──────────┐  ┌──────────┐  ┌──────────┐                   │
│     │ Slot-1   │  │ Slot-2   │  │ Slot-3   │  ...              │
│     │ hs:{0-63}│  │hs:{64-127}│ │hs:{128..}│                   │
│     │ (Raft组) │  │ (Raft组)  │  │ (Raft组) │                   │
│     └──────────┘  └──────────┘  └──────────┘                   │
│           │             │              │                        │
│           └─────────────┼──────────────┘                        │
│                         ▼                                        │
│              ┌──────────────────┐                                │
│              │ Pebble (共享)    │                                │
│              │ Key: [ks][hs:2]..│                                │
│              └──────────────────┘                                │
└──────────────────────────────────────────────────────────────────┘
```

## 8. Controller 改动

### 8.1 新增命令

| 命令 | 触发 | 作用 |
|------|------|------|
| `CmdAddSlot` | `add-slot` | 分配新 SlotID + Assignment，触发 hash slot 再分配 |
| `CmdRemoveSlot` | `remove-slot` | 标记目标 Slot 进入移除流程 |
| `CmdStartMigration` | Controller 自动 | 标记 hash slot 迁移开始 |
| `CmdFinalizeMigration` | 迁移追平后 | 原子切换 Assignment[hs] |
| `CmdAbortMigration` | 超时或运维取消 | 回滚到迁移前 |
| `CmdRebalance` | `rebalance` | 计算再平衡方案并启动迁移 |

### 8.2 Planner 改动

现有决策链：`Repair → Rebalance`

新增约束：迁移中的物理 Slot **跳过** Repair 和 Rebalance，避免副本迁移和数据迁移叠加。

### 8.3 再平衡算法

```go
func computeRebalancePlan(table *HashSlotTable, slots []SlotInfo) []Migration {
    // 1. 计算目标：每个物理 Slot 应拥有 HashSlotCount / len(slots) 个 hash slot
    // 2. 找出超额 Slot（拥有量 > 目标）和不足 Slot（拥有量 < 目标）
    // 3. 从超额 Slot 中选 hash slot 迁往不足 Slot
    // 4. 优先迁移数据量小的 hash slot（减少迁移耗时）
    // 5. 返回迁移列表
}
```

## 9. 运维 CLI

```bash
# 查看当前 hash slot 分布
wk operator hash-slot-table
# 输出：
# Slot-1: hash slots [0-63]    (64 slots, 2.1GB, 1200 QPS)
# Slot-2: hash slots [64-127]  (64 slots, 1.8GB, 900 QPS)
# Slot-3: hash slots [128-191] (64 slots, 3.2GB, 2100 QPS)
# Slot-4: hash slots [192-255] (64 slots, 1.5GB, 600 QPS)

# 添加物理 Slot
wk operator add-slot
# → 自动从各 Slot 迁移 hash slot 给新 Slot

# 移除物理 Slot
wk operator remove-slot --slot 4
# → 自动把 Slot-4 的 hash slot 分散给其他 Slot

# 再平衡（不增减 Slot）
wk operator rebalance
# → 从过载 Slot 迁几个 hash slot 到轻载 Slot

# 手动迁移指定 hash slot
wk operator migrate-hash-slot --hash-slot 150 --to 2

# 查看迁移进度
wk operator migration-status

# 取消正在进行的迁移
wk operator abort-migration
```

## 10. 代码改动范围

```
pkg/cluster/
├── slotcontroller/
│   ├── hashslottable.go     [新增] HashSlotTable 数据结构
│   ├── planner.go           [改动] 跳过迁移中 Slot + 再平衡算法
│   ├── commands.go          [改动] 新增迁移相关命令
│   └── statemachine.go      [改动] Apply 新命令
│
├── router.go                [改动] CRC32 % N → CRC32 % HashSlotCount + 查表
├── config.go                [改动] 新增 HashSlotCount 配置
├── managed_slots.go         [改动] 感知 HashSlotTable 变更
├── operator.go              [改动] 新增 add-slot / remove-slot / rebalance
│
└── slotmigration/           [新增] 迁移执行器
    ├── worker.go            [新增] 迁移主循环（可并行多个 hash slot）
    ├── snapshot.go          [新增] 单个 hash slot 快照导出/导入
    ├── delta.go             [新增] 增量双写转发 + 去重
    └── progress.go          [新增] 进度跟踪与上报

pkg/slot/
├── meta/
│   ├── codec.go             [改动] Key 前缀 slotID:8 → hashSlot:2
│   ├── shard_spans.go       [改动] 按 hashSlot 组织数据范围
│   ├── snapshot.go          [改动] 支持按 hash slot 子集导出
│   └── db.go                [改动] ForSlot → ForHashSlots（按 hash slot 集合）
│
├── fsm/
│   ├── statemachine.go      [改动] 验证 hash slot 归属 + 双写转发
│   └── migration_cmds.go   [新增] 迁移相关命令编解码
│
└── proxy/
    └── store.go             [改动] 携带 hashSlot + 感知 ErrRerouted

pkg/replication/multiraft/
├── types.go                 [改动] Command 增加 HashSlot 字段
└── slot.go                  [改动] applyCommittedEntries 传递 HashSlot

internal/
├── usecase/message/retry.go [改动] shouldRefreshAndRetry 加入 ErrRerouted
└── app/channelmeta.go       [改动] 缓存加 HashSlotTableVersion 校验
```

## 11. 迁移期间的读写行为

### 11.1 正在迁移的 hash slot

| 阶段 | 写入 | 读取 |
|------|------|------|
| Snapshot | 路由到源 Slot，正常服务 | 路由到源 Slot，正常服务 |
| Delta | 路由到源 Slot，Apply + 转发到 Target | 路由到源 Slot（数据最全） |
| Switching | 源 Slot 返回 ErrRerouted，客户端重试到 Target | 同上 |
| Done | 路由到 Target，正常服务 | 路由到 Target |

### 11.2 未迁移的 hash slot

完全不受影响。同一物理 Slot 上的其他 hash slot 的读写照常进行。

### 11.3 对 Channel 层的影响

无。Channel 层只关心 `ChannelRuntimeMeta.Leader`（ISR Leader 在哪个节点），这个元数据存在 Slot 中。迁移完成后元数据已经在目标 Slot 里，`channelMetaSync` 感知到 HashSlotTable 版本变更后自动从正确的 Slot 拉取。

## 12. 容错

### 12.1 故障场景

| 场景 | 处理 |
|------|------|
| 快照传输中源 Slot Leader 崩溃 | 新 Leader 恢复后重新生成快照 |
| 目标 Slot Leader 崩溃 | 新 Leader 丢弃未完成的导入，源重新推送 |
| 增量转发超时（默认 10 分钟） | Controller 自动 Abort |
| Switching 时 Controller Leader 切换 | FinalizeMigration 是 Raft 命令，已提交则生效 |
| 运维误操作 | 数据已迁移不会丢；可反向迁移恢复 |

### 12.2 回滚

Finalize 之前任意阶段可回滚：
1. Controller 下发 `CmdAbortMigration`。
2. 源 Slot 停止双写。
3. 目标 Slot DeleteRange 清理导入数据。
4. HashSlotTable 清除 Migration 记录。

### 12.3 并发限制

| 限制 | 默认值 | 说明 |
|------|--------|------|
| 同时迁移的 hash slot 数 | 4 | 可并行，但不过载 |
| 同一物理 Slot 同时迁出的 hash slot 数 | 2 | 避免源 Slot 写放大过大 |
| 单个 hash slot 迁移超时 | 10min | 超时自动 Abort |
| 迁移期间禁止副本 Rebalance | - | 避免两种迁移叠加 |

## 13. Hash Slot 数量的选择

| HashSlotCount | 4 物理 Slot 时每 Slot | 16 物理 Slot 时每 Slot | 适用场景 |
|---------------|----------------------|----------------------|----------|
| 64 | 16 | 4 | 小集群（≤8 节点） |
| 256 | 64 | 16 | **推荐默认值**，中等规模 |
| 1024 | 256 | 64 | 大集群（≥32 节点） |

**256 作为默认值的理由**：
- 足以支持最多 ~50 个物理 Slot（每 Slot 至少 5 个 hash slot 才有意义）。
- 映射表大小 256 × 4 = 1KB，全量下发无压力。
- 单个 hash slot 的数据量 = 总量 / 256，迁移速度快。
- `HashSlotCount` 在集群创建时设定，后续不可变（但物理 Slot 数量可变，这才是重点）。

## 14. 实施步骤

1. **Key 编码改动**：`codec.go` 中 `slotID:8` → `hashSlot:2`，同步改写所有 encode/decode 和 shard_spans。
2. **HashSlotTable 数据结构**：实现 Assignment 数组、Lookup、再平衡算法 + 单元测试。
3. **Router 改造**：`SlotForKey` 从取模改为查表；HashSlotTable 通过 Agent 心跳下发。
4. **StateMachine 改造**：Command 加 HashSlot 字段、Apply 时验证归属、快照按 hash slot 集合生成。
5. **Controller 命令**：新增 Add/Remove/Migrate/Finalize/Abort 命令。
6. **迁移执行器**：实现 `slotmigration` 包（per hash slot 快照、增量转发、进度上报、并行调度）。
7. **客户端适配**：ErrRerouted 处理、channelmeta 缓存 Version 校验。
8. **运维 CLI**：hash-slot-table / add-slot / remove-slot / rebalance / migrate-hash-slot。
9. **集成测试**：多节点完整流程 + 并行迁移 + Leader 切换场景。

## 15. 与现有文档的衔接

实现完成后需更新：
- `../architecture/02-slot-layer.md`：路由算法、Key 编码、hash slot 概念
- `../architecture/01-controller-layer.md`：新增命令、迁移调度
- `../architecture/05-message-sending-flow.md`：寻址流程
- `../architecture/README.md`：术语表新增 Hash Slot
