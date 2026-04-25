# Slot 集群数据获取与 Leader 路由

> 本文档描述各节点如何获得 Slot 层的集群数据（HashSlotTable、SlotAssignment），以及消息到达后如何定位到 Slot Raft Leader 和 Channel ISR Leader。

## 1. 概述

WuKongIM 集群的消息路由依赖两类关键数据：

| 数据               | 来源              | 作用                                              |
| ------------------ | ----------------- | ------------------------------------------------- |
| **HashSlotTable**  | Controller 持久化 | `channelID → HashSlot → SlotID` 的映射表          |
| **SlotAssignment** | Controller 下发   | 每个物理 Slot 的期望副本节点列表                  |
| **Slot LeaderID**  | 本地 Raft 状态    | 每个物理 Slot 当前 Raft Leader                    |
| **ChannelRuntimeMeta** | Slot Raft 存储 | 频道的 ISR Leader / Replicas / Epoch 等运行时元数据 |

一条消息从到达任意节点到写入正确的 Channel Leader，需要经过**三级路由**：

```
消息到达任意节点
  │
  ├─ 1. Key → HashSlot → SlotID（查 HashSlotTable，纯本地计算）
  │
  ├─ 2. SlotID → Slot Raft Leader（查本地 MultiRaft Runtime 状态）
  │
  └─ 3. Slot Leader → ChannelRuntimeMeta → Channel ISR Leader
       （从 Slot 层读取或本地缓存获取）
```

## 2. 节点启动与集群数据获取

### 2.1 启动流程

**代码位置**：`pkg/cluster/cluster.go`

```
Cluster.Start()
  │
  ├─ 1. startTransportLayer()
  │     创建 TCP Server、连接池、RPC Mux、StaticDiscovery
  │     注册 Raft 消息、Forward RPC、Controller RPC、ManagedSlot RPC 处理器
  │
  ├─ 2. startControllerRaftIfLocalPeer()
  │     仅 Controller 节点执行：
  │     → newControllerHost() 加载 Controller 元数据存储
  │     → ensureControllerHashSlotTable() 确保 HashSlotTable 存在
  │     → router.UpdateHashSlotTable(table) 立即更新本地路由
  │     → host.Start() 启动 Controller Raft
  │
  ├─ 3. startMultiraftRuntime()
  │     启动 MultiRaft 引擎，创建 Worker 线程池和 Ticker
  │
  ├─ 4. startControllerClient()
  │     创建 slotAgent，准备与 Controller Leader 通信
  │
  ├─ 5. startObservationLoop()
  │     启动周期性观察循环（默认 200ms 一次）
  │
  └─ 6. seedLegacySlotsIfConfigured()
       向后兼容：打开预配置的静态 Slot
```

### 2.2 初始 HashSlotTable

节点创建时即构造一张默认的 `HashSlotTable`：

```go
// cluster.go:107
router: NewRouter(
    NewHashSlotTable(cfg.effectiveHashSlotCount(), int(cfg.effectiveInitialSlotCount())),
    cfg.NodeID, nil,
)
```

默认表将 `HashSlotCount` 个逻辑分片均匀分配到 `InitialSlotCount` 个物理 Slot：

```
HashSlotCount=16384, InitialSlotCount=3:
  Slot 1 → HashSlot [0, 5461]
  Slot 2 → HashSlot [5462, 10922]
  Slot 3 → HashSlot [10923, 16383]
```

此默认表仅用于启动阶段，很快会被 Controller 下发的权威版本覆盖。

## 3. 观察循环：集群数据的持续同步

**代码位置**：`pkg/cluster/cluster.go` · `pkg/cluster/agent.go` · `pkg/cluster/observer.go`

### 3.1 循环架构

```
observerLoop (每 200ms 触发一次)
  │
  ├─ observeOnce()
  │   ├─ agent.HeartbeatOnce()     // 上报心跳 + 获取 HashSlotTable
  │   ├─ agent.SyncAssignments()   // 拉取 SlotAssignment 列表
  │   ├─ agent.ApplyAssignments()  // 协调本地 Slot 状态
  │   └─ observeHashSlotMigrations() // 处理迁移任务
  │
  └─ controllerTickOnce()（仅 Controller Leader 执行）
      └─ controller.Tick()         // 生成调度决策
```

### 3.2 心跳上报与 HashSlotTable 获取

**代码位置**：`pkg/cluster/controller_client.go` · `pkg/cluster/controller_handler.go`

```
节点                                    Controller Leader
  │                                           │
  │  Report(AgentReport{                      │
  │    NodeID, Timestamp,                     │
  │    HashSlotTableVersion,  ←── 本地版本    │
  │    SlotRuntimeViews[]     ←── 各 Slot 状态│
  │  })                                       │
  │ ──────────────────────────────────────▸    │
  │                                           │ 1. Propose 心跳到 Controller Raft
  │                                           │ 2. 比较 HashSlotTableVersion
  │    ◀──────────────────────────────────    │
  │  Response{                                │
  │    HashSlotTableVersion,                  │
  │    HashSlotTable (if version differs) ←── │ 仅在版本不同时下发完整表
  │  }                                        │
  │                                           │
  │  applyHashSlotTablePayload()              │
  │  → DecodeHashSlotTable(data)              │
  │  → updateRuntimeHashSlotTable(table)      │
  │     ├─ router.UpdateHashSlotTable()  ←── 原子更新路由
  │     └─ 通知各 Slot StateMachine      ←── 更新 Hash Slot 归属
```

**关键点**：
- HashSlotTable 通过心跳响应**增量下发**——仅当版本号不一致时发送完整表
- 更新路由使用 `atomic.Pointer` 原子替换，读取无锁

### 3.3 Slot Assignment 同步

**代码位置**：`pkg/cluster/agent.go` · `pkg/cluster/assignment_cache.go`

```
agent.SyncAssignments()
  │
  ├─ 正常路径：
  │   client.RefreshAssignments()
  │   → Controller RPC: controllerRPCListAssignments
  │   → 返回 []SlotAssignment + HashSlotTable（附带）
  │   → cache.SetAssignments(assignments)
  │
  └─ 降级路径（Controller 不可达）：
      controllerMeta.ListAssignments()  ← 从本地 Controller 元数据读取
      → cache.SetAssignments(assignments)
```

`SlotAssignment` 结构：

```go
type SlotAssignment struct {
    SlotID         uint32    // 物理 Slot ID
    DesiredPeers   []uint64  // 期望的副本节点列表
    ConfigEpoch    uint64    // 分配版本号
    BalanceVersion uint64    // 重平衡版本号
}
```

### 3.4 协调本地 Slot 状态

**代码位置**：`pkg/cluster/reconciler.go` · `pkg/cluster/slot_manager.go`

```
agent.ApplyAssignments()
  → reconciler.Tick()
    │
    ├─ 遍历 Assignments：如果 DesiredPeers 包含本节点
    │   → ensureManagedSlotLocal(slotID, peers)
    │      ├─ Slot 已存在 → 更新 Peers 列表
    │      ├─ 有磁盘状态 → OpenSlot() 从日志恢复
    │      ├─ 有 Bootstrap 权限 → BootstrapSlot() 初始化新 Raft 组
    │      └─ 仅有 RuntimeView → OpenSlot() 作为 Learner 加入
    │
    ├─ 遍历本地已有 Slot：如果不在 Assignments 中
    │   → runtime.CloseSlot() 关闭
    │
    └─ 执行 ReconcileTask（由 Controller 下发）
        → AddLearner → WaitForCatchUp → PromoteLearner
        → TransferLeader → RemoveOldVoter
        → 上报 TaskResult 给 Controller
```

### 3.5 数据获取时序图

```
┌────────┐           ┌────────────────┐           ┌────────────┐
│  Node  │           │  Controller    │           │  MultiRaft │
│ (Agent)│           │    Leader      │           │  Runtime   │
└───┬────┘           └───────┬────────┘           └─────┬──────┘
    │                        │                          │
    │──── Heartbeat ────────▸│                          │
    │  (NodeID, SlotViews,   │                          │
    │   HSTableVersion)      │                          │
    │                        │                          │
    │◀─── Response ─────────│                          │
    │  (HSTable if updated)  │                          │
    │                        │                          │
    │ router.UpdateHashSlotTable()                      │
    │                        │                          │
    │──── ListAssignments ──▸│                          │
    │                        │                          │
    │◀─── Assignments[] ────│                          │
    │                        │                          │
    │ cache.SetAssignments()                            │
    │                        │                          │
    │ reconciler.Tick()                                 │
    │                        │                          │
    │──── ensureManagedSlotLocal() ────────────────────▸│
    │     OpenSlot / BootstrapSlot                      │
    │                        │                          │
    │◀──── Slot Ready ─────────────────────────────────│
    │                        │                          │
    │     Now: Slot Raft running,                       │
    │     LeaderID tracked by raft consensus            │
    └────────────────────────┴──────────────────────────┘
```

## 4. 消息路由：定位 Slot Raft Leader

### 4.1 路由三步

**代码位置**：`pkg/cluster/router.go` · `pkg/cluster/hashslot/hashslottable.go`

```
channelID = "group_abc"
    │
    ▼  第一步：Key → HashSlot（CRC32 哈希）
HashSlot = CRC32("group_abc") % HashSlotCount
         = 53246 % 16384 = 4094
    │
    ▼  第二步：HashSlot → SlotID（查表）
SlotID = HashSlotTable.Lookup(4094) = 2
    │
    ▼  第三步：SlotID → LeaderID（查 Raft 状态）
LeaderID = runtime.Status(SlotID=2).LeaderID = NodeID(3)
```

核心代码：

```go
// router.go
func (r *Router) SlotForKey(key string) multiraft.SlotID {
    table := r.hashSlotTable.Load()    // 原子读取，无锁
    return table.Lookup(r.HashSlotForKey(key))
}

func (r *Router) LeaderOf(slotID multiraft.SlotID) (multiraft.NodeID, error) {
    status, err := r.runtime.Status(slotID)  // 读取 Raft 状态
    if status.LeaderID == 0 {
        return 0, ErrNoLeader
    }
    return status.LeaderID, nil
}
```

### 4.2 Slot Leader 状态如何维护

每个物理 Slot 运行一个 etcd/raft RawNode。Leader 信息由 Raft 协议自动维护：

```
MultiRaft Runtime
  │
  ├─ Ticker goroutine（每 tick 调用 rawNode.Tick()）
  │   → 触发选举超时 / 心跳
  │
  ├─ Worker goroutines（处理 Ready）
  │   → processReady() 中调用 refreshStatus()
  │   → 从 rawNode.Status().Lead 更新 slot.status.LeaderID
  │
  └─ runtime.Status(slotID)
      → 返回最新的 {LeaderID, Term, Role, ...}
```

### 4.3 本地 vs 远端：Propose 与 Forward

**代码位置**：`pkg/cluster/cluster.go` · `pkg/cluster/forward.go`

```
ProposeWithHashSlot(ctx, slotID, hashSlot, cmd)
  │
  ├─ leaderID := router.LeaderOf(slotID)
  │
  ├─ if router.IsLocal(leaderID):
  │   │  本地是 Leader → 直接提议
  │   └─ runtime.Propose(slotID, [hashSlot:2][cmd:N])
  │      → Raft 日志复制 → StateMachine.Apply()
  │      → future.Wait(ctx) 等待提交
  │
  └─ else:
      │  Leader 在远端 → RPC 转发
      └─ forwardToLeader(leaderID, slotID, payload)
         → fwdClient.RPCService(leaderID, rpcServiceForward, body)
         → 远端节点 handleForwardRPC()
         → 远端执行 runtime.Propose()
         → 返回结果
```

**重试机制**：

```go
retry := Retry{
    Interval:    50ms,
    MaxWait:     ForwardRetryBudget,
    IsRetryable: func(err error) bool {
        return errors.Is(err, ErrNotLeader)  // 仅 Leader 变更时重试
    },
}
```

如果转发目标不再是 Leader（选举刚发生），重试会重新查询 `LeaderOf(slotID)` 获取新 Leader。

## 5. Channel Leader 路由：从 Slot 到 Channel

找到 Slot Leader 后，还需要进一步定位 Channel 的 ISR Leader。

### 5.1 ChannelRuntimeMeta 的存储与查询

**代码位置**：`pkg/slot/proxy/store.go` · `pkg/slot/proxy/runtime_meta_rpc.go` · `pkg/slot/proxy/authoritative_rpc.go`

`ChannelRuntimeMeta` 存储在 Slot 的 Pebble 数据库中，按 HashSlot 分片。查询时需要路由到 Slot Raft Leader：

```
GetChannelRuntimeMeta(channelID, channelType)
  │
  ├─ slotID = cluster.SlotForKey(channelID)
  ├─ hashSlot = cluster.HashSlotForKey(channelID)
  │
  ├─ shouldServeSlotLocally(slotID)?
  │   ├─ YES → db.ForHashSlot(hashSlot).GetChannelRuntimeMeta(...)
  │   │        直接从本地 Pebble 读取
  │   │
  │   └─ NO → callAuthoritativeRPC(slotID, runtimeMetaRPCGet, ...)
  │            路由到 Slot Leader 节点查询
  │
  └─ 返回 ChannelRuntimeMeta{
         Leader, Replicas, ISR,
         ChannelEpoch, LeaderEpoch,
         MinISR, LeaseUntilMS, Status
     }
```

### 5.2 callAuthoritativeRPC：Leader 追踪

**代码位置**：`pkg/slot/proxy/authoritative_rpc.go`

当查询需要路由到 Slot Leader 时，使用 Leader 追踪循环：

```
callAuthoritativeRPC(slotID, serviceID, payload)
  │
  ├─ peers = cluster.PeersForSlot(slotID)  // 获取所有副本节点
  ├─ candidates = [peers...]
  │
  └─ for len(candidates) > 0:
       peer = candidates[0]
       resp = cluster.RPCService(peer, slotID, serviceID, payload)
       │
       ├─ resp.status == "ok"       → 返回结果
       ├─ resp.status == "not_found" → 返回 NotFound
       ├─ resp.status == "not_leader"
       │   └─ resp.leaderID != 0 → 将 leaderID 插入 candidates 头部，继续
       ├─ resp.status == "no_leader" → 记录错误，尝试下一个 peer
       └─ resp.status == "no_slot"   → 记录错误，尝试下一个 peer
```

### 5.3 channelMetaSync：主动元数据同步

**代码位置**：`internal/app/channelmeta.go`

除了被动查询，每个节点还通过 `channelMetaSync` 组件主动同步频道元数据到本地 ISR 运行时：

```
channelMetaSync.syncOnce()
  │
  ├─ observeHashSlotTableVersion()
  │   如果 HashSlotTable 版本变化 → 清空本地缓存
  │
  ├─ source.ListChannelRuntimeMeta()
  │   遍历所有 Slot，获取全量 ChannelRuntimeMeta
  │
  ├─ 筛选 Replicas 包含本节点的频道
  │   → cluster.ApplyMeta(meta)
  │     将元数据应用到本地 Channel ISR 运行时
  │
  └─ 清理不再归属本节点的频道
      → cluster.RemoveLocal(key)
```

**同步周期**：默认 1 秒，由 `refreshInterval` 配置。

**被动刷新**：当消息写入返回 `ErrStaleMeta` / `ErrNotLeader` 时，通过 `RefreshChannelMeta` 从 Slot 层权威源重新获取最新元数据：

```go
func (s *channelMetaSync) RefreshChannelMeta(ctx context.Context, id channel.ChannelID) (channel.Meta, error) {
    s.observeHashSlotTableVersion()
    meta, err := s.source.GetChannelRuntimeMeta(ctx, id.ID, int64(id.Type))
    return s.apply(meta)
}
```

### 5.4 完整路由链路

以一条消息从任意节点到达 Channel ISR Leader 的完整路径：

```
                       任意节点 (Node-1)
                            │
  ┌─────────────────────────┼─────────────────────────┐
  │ 第一级：Key → SlotID    │                         │
  │                         ▼                         │
  │  CRC32(channelID) % HashSlotCount = HashSlot      │
  │  HashSlotTable.Lookup(HashSlot) = SlotID          │
  │  （纯内存计算，O(1)）                             │
  └─────────────────────────┼─────────────────────────┘
                            │
  ┌─────────────────────────┼─────────────────────────┐
  │ 第二级：SlotID → Slot   │                         │
  │         Raft Leader     │                         │
  │                         ▼                         │
  │  runtime.Status(SlotID).LeaderID                  │
  │  ├─ Leader == 本地 → 直接处理                     │
  │  └─ Leader == 远端 → RPC 转发到 Slot Leader       │
  │  （读本地 Raft 状态，O(1)）                       │
  └─────────────────────────┼─────────────────────────┘
                            │
  ┌─────────────────────────┼─────────────────────────┐
  │ 第三级：Slot Leader →   │                         │
  │   Channel ISR Leader    │                         │
  │                         ▼                         │
  │  方式 A：本地缓存命中                             │
  │    channelMetaSync 预同步的 Meta → ISR Leader     │
  │                                                    │
  │  方式 B：缓存未命中或过期                         │
  │    GetChannelRuntimeMeta(channelID)                │
  │    → Slot Leader 的 Pebble 读取                   │
  │    → 返回 ISR Leader / Replicas / Epoch           │
  │                                                    │
  │  → 消息路由到 Channel ISR Leader 节点             │
  └────────────────────────────────────────────────────┘
```

## 6. Controller 侧：数据从何而来

### 6.1 Controller 状态机

**代码位置**：`pkg/controller/plane/statemachine.go` · `pkg/controller/plane/controller.go`

Controller 通过 Raft 共识维护集群的权威状态：

```
Controller Leader
  │
  ├─ 接收节点心跳 → applyNodeHeartbeat()
  │   → 更新节点状态（Alive / Suspect / Dead）
  │   → 更新 SlotRuntimeView
  │
  ├─ 定时评估超时 → applyTimeoutEvaluation()
  │   → Alive → Suspect（默认 3s 无心跳）
  │   → Suspect → Dead（默认 10s 无心跳）
  │
  └─ Tick() → Planner.NextDecision()
      │
      ├─ 发现未分配的 Slot → 生成 Bootstrap 任务
      │   → 选择 N 个健康节点作为 DesiredPeers
      │
      ├─ 发现 Dead 节点持有 Slot → 生成 Repair 任务
      │   → 选择新节点替换故障节点
      │
      └─ 所有 Slot 健康 → 尝试 Rebalance
          → 均衡各节点的 Slot 负载
```

### 6.2 数据下发路径

```
Controller Store (Raft 共识)
  │
  ├─ SlotAssignment → 通过 ListAssignments RPC 下发给 Agent
  │
  ├─ HashSlotTable → 通过心跳响应下发（版本不同时附带）
  │
  ├─ ReconcileTask → 通过 GetTask RPC 下发给执行节点
  │
  └─ ChannelRuntimeMeta → 由业务层写入 Slot FSM
      → 通过 channelMetaSync 或 RPC 同步到 Channel 运行时
```

## 7. Hash Slot 迁移

### 7.1 迁移阶段

**代码位置**：`pkg/cluster/hashslot/hashslottable.go` · `pkg/cluster/hashslot_migration.go`

当需要将某些 Hash Slot 从一个物理 Slot 迁移到另一个时：

```
                 PhaseSnapshot           PhaseDelta
Source Slot ─────── 快照导出 ─────── 增量转发 ────┐
                                                    │
                 PhaseSwitching          PhaseDone   │
Target Slot ◀────── 切换读取 ◀────── 迁移完成 ◀───┘
```

| 阶段           | 行为                                                       |
| -------------- | ---------------------------------------------------------- |
| PhaseSnapshot  | 源 Slot 导出 Hash Slot 数据快照，发送给目标 Slot           |
| PhaseDelta     | 源 Slot 的新写入通过 Delta Forwarder 同步转发到目标 Slot   |
| PhaseSwitching | 读取切换到目标 Slot，目标 Slot 开始服务该 Hash Slot 的读写 |
| PhaseDone      | 更新 HashSlotTable 映射，删除迁移记录                      |

### 7.2 StateMachine 的迁移感知

**代码位置**：`pkg/slot/fsm/statemachine.go`

Slot FSM 通过以下接口感知迁移状态：

```go
// 源 Slot：哪些 Hash Slot 需要转发增量到哪个目标 Slot
UpdateOutgoingDeltaTargets(map[uint16]multiraft.SlotID)

// 目标 Slot：哪些 Hash Slot 正在接收迁移数据
UpdateIncomingDeltaHashSlots([]uint16)

// 源 Slot 安装转发器：新写入自动转发给目标
SetDeltaForwarder(func(ctx, targetSlotID, cmd) error)
```

当 `HashSlotTable` 更新后，`updateRuntimeHashSlotTable` 会通知所有注册的 StateMachine 更新其 Hash Slot 归属，保证路由和数据写入的一致性。

## 8. 容错与降级

### 8.1 Controller 不可达

| 场景                         | 行为                                                  |
| ---------------------------- | ----------------------------------------------------- |
| Controller Leader 宕机       | Agent 尝试所有 Controller Peers，等待新 Leader 选出   |
| 所有 Controller 不可达       | 使用本地 `controllerMeta` 存储的 Assignment 降级运行  |
| HashSlotTable 获取失败       | 继续使用最后一次成功获取的版本                        |

### 8.2 Slot Leader 不可达

| 场景                         | 行为                                                  |
| ---------------------------- | ----------------------------------------------------- |
| 转发到 Slot Leader 返回 NotLeader | 重新查询 `LeaderOf(slotID)`，重试转发           |
| Slot 无 Leader（选举中）     | 返回 `ErrNoLeader`，上层重试                          |
| RPC 超时                     | 在 `ForwardRetryBudget` 内重试                        |

### 8.3 Channel Meta 过期

| 场景                         | 行为                                                  |
| ---------------------------- | ----------------------------------------------------- |
| 写入返回 `ErrStaleMeta`     | `sendWithMetaRefreshRetry` 从 Slot 权威源刷新后重试   |
| 写入返回 `ErrNotLeader`     | 同上，刷新 Meta 后路由到新的 ISR Leader               |
| `channelMetaSync` 同步失败  | 下一个同步周期（1s）自动重试                          |
| HashSlotTable 版本变化       | `channelMetaSync` 清空本地缓存，全量重新同步          |

## 9. 关键设计决策

### 9.1 为什么 HashSlotTable 通过心跳下发

- **增量高效**：只在版本不一致时下发完整表，正常心跳只传版本号。
- **无需额外连接**：复用已有的心跳 RPC，不引入新的通信通道。
- **最终一致即可**：HashSlotTable 变更（如迁移）本身有多阶段协议保证安全，节点间短暂的版本差异不影响正确性。

### 9.2 为什么 Slot Leader 信息来自本地 Raft 状态

- **零网络开销**：每个参与 Raft 的节点本地就知道 Leader 是谁（通过 Raft 选举和心跳）。
- **实时性最强**：Raft 选举完成后，所有参与节点立即感知新 Leader。
- **非参与节点**：通过 `callAuthoritativeRPC` 的 Leader 追踪循环发现 Leader，第一次请求可能命中非 Leader，但会被重定向。

### 9.3 为什么 Channel Meta 需要主动同步 + 被动刷新

- **主动同步**（`channelMetaSync`，1s 周期）：保证本地 Channel ISR 运行时有最新的 Meta，减少热路径上的远程查询。
- **被动刷新**（写入失败时触发）：应对 Meta 在同步周期间变更的情况，确保写入最终路由到正确的 ISR Leader。
- **两者互补**：主动同步覆盖大部分场景；被动刷新覆盖边界情况和竞态条件。
