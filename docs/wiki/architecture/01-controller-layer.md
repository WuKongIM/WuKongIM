# L1 · Controller 控制层

> 一致性算法：**Raft**（基于 etcd/raft v3）
> 职责：维护集群拓扑、调度 Slot 层副本分布、维护 HashSlotTable，并编排修复 / 迁移 / 扩缩容任务

## 1. 概述

Controller 是整个分布式系统的"大脑"。它运行一个独立的 Raft Quorum（通常 3 或 5 节点），不依赖 Slot 层或 Channel 层的可用性。它的核心功能是：

1. **节点健康管理**：跟踪所有节点的心跳，维护 Alive / Suspect / Dead / Draining 四个状态。
2. **副本放置决策**：为每个 physical Slot 计算期望副本分布（SlotAssignment）。
3. **HashSlotTable 管理**：维护 `hashSlot -> physical Slot` 映射，并持久化迁移状态。
4. **修复与运维调度**：支持 Repair / Rebalance，以及 AddSlot / RemoveSlot / StartMigration / AbortMigration。
5. **全局可观测**：聚合所有节点上报的 RuntimeView，提供集群状态查询。

## 2. 代码结构

```
pkg/
├── controller/
│   ├── raft/                   # Controller Raft 引擎
│   ├── plane/                  # Planner / StateMachine / Commands
│   └── meta/                   # 控制面 Pebble 持久化
│
└── cluster/
    ├── cluster.go              # 启动、观察循环、Controller RPC
    ├── agent.go                # 节点 Agent（心跳 / 同步 / 执行任务）
    ├── controller_client.go    # Controller RPC 客户端
    ├── operator.go             # AddSlot / RemoveSlot / Rebalance API
    ├── hashslottable.go        # HashSlotTable 对外桥接
    └── hashslot_migration.go   # 迁移观察与提交流程
```

## 3. Raft 引擎（controllerraft）

### 3.1 启动流程

```
Cluster.Start()
  → startControllerRaftIfLocalPeer()   // cluster.go:106-155
      → 打开 Pebble 存储（controllermeta.Store）
      → 打开 RaftStorage（日志存储）
      → 创建 StateMachine
      → 启动 controllerraft.Service
      → 只有在 Controller Peers 中的节点才启动
```

> **Bootstrap 规则**：只有 NodeID 最小的 Peer 执行初始 Bootstrap（`config.go` 中的 `isSmallestPeer` 检查），其余节点以 Join 模式等待。

### 3.2 核心参数

| 参数            | 默认值 | 说明                              | 代码位置                   |
| --------------- | ------ | --------------------------------- | -------------------------- |
| TickInterval    | 100ms  | Raft tick 周期                    | `controllerraft/service.go:22` |
| ElectionTick    | 10     | 选举超时倍数（= 1s）             | `controllerraft/service.go:23` |
| HeartbeatTick   | 1      | 心跳间隔倍数（= 100ms）          | `controllerraft/service.go:24` |
| PreVote         | true   | 防止网络分区时不必要的选举扰动    | `controllerraft/service.go:154` |

### 3.3 Proposal 流程

```
caller → Propose(ctx, cmd)                    // service.go:212-254
  → 如果当前节点是 Leader：直接写入 Raft log
  → 如果不是 Leader：返回 NotLeader 错误 + LeaderID hint
  → proposal 添加到 inflight 跟踪表
  → 等待 future 完成或 ctx 超时

Raft Ready → processReady()                   // service.go:306-437
  → 持久化 entries + hardState
  → 发送 messages 到 peers
  → 应用 committed entries → StateMachine.Apply()
  → 解析 future 并返回结果
```

### 3.4 Leader 故障转移

- Leader 失联后，etcd/raft 自动选举新 Leader。
- `failInflightProposalsOnLeaderLoss()`（`service.go:702`）会中止所有等待中的 proposal。
- 客户端通过 `NotLeader` 响应中的 LeaderID 重新定向请求。

## 4. 控制面决策（slotcontroller）

### 4.1 核心循环

Controller Tick 只在 Controller Leader 上执行：

```go
// controller.go:40-74
func (c *Controller) Tick(ctx context.Context) error {
    if !c.isLeader() {
        return nil    // 只有 Leader 做决策
    }
    state := c.snapshot()   // 聚合 Nodes / Assignments / Views / Tasks
    decision := c.planner.NextDecision(state)
    if decision != nil {
        c.store.UpsertAssignment(decision.Assignment)
        c.store.UpsertTask(decision.Task)
    }
    return nil
}
```

Tick 的调用频率由观察循环控制（`cluster.go:194-217`），默认每 **200ms** 触发一次。

### 4.2 Planner 算法

Planner 按照以下优先级执行决策：

```
1. 修复（Repair）：优先级最高
   - 遍历所有 Slot，检查是否有副本在 Dead/Draining 节点上
   - 找到第一个需要修复的 Slot → 选新目标节点 → 生成 RepairTask

2. HashSlot 迁移保护：跳过正在迁移的 physical Slot，避免副本重平衡与 hash slot 迁移互相干扰

3. 重平衡（Rebalance）：修复完成后才执行
   - 计算各节点负载（承载的 Slot 数量）
   - maxLoad - minLoad > RebalanceSkewThreshold（默认 2）时触发
   - 从最重节点选一个 Slot 迁移到最轻节点

4. 运维命令（AddSlot / RemoveSlot / Rebalance）
   - `AddSlot`：创建新的 physical Slot，计算迁移计划并写入 HashSlotTable
   - `RemoveSlot`：把被移除 Slot 承载的 hash slot 迁走，再删除 Assignment
   - `StartMigration / AbortMigration / FinalizeMigration`：推进单个 hash slot 的迁移生命周期
```

**关键代码**：`planner.go:93-166`

#### 副本选择策略

- **Bootstrap**：选择负载最低的 N 个健康节点（`planner.go:168-197`）
- **Repair**：选择负载最低且不在当前 DesiredPeers 中的健康节点（`planner.go:209-235`）
- **Rebalance**：选择负载差超过阈值时从高负载节点迁出（`planner.go:107-166`）

### 4.3 状态机（statemachine）

状态机处理控制面命令，每次通过 Raft 提交后执行。除基础的节点状态与任务命令外，还负责 HashSlotTable 的增量变更：

| 命令                        | 作用                            | 代码位置           |
| --------------------------- | ------------------------------- | ------------------ |
| NodeHeartbeat               | 更新节点状态、持久化 RuntimeView | `statemachine.go:79-111` |
| OperatorRequest             | 手动 Drain / Resume 节点        | `statemachine.go:113-140` |
| EvaluateTimeouts            | 检查心跳超时、转移节点状态      | `statemachine.go:142-166` |
| TaskResult                  | 处理任务完成/失败，指数退避重试  | `statemachine.go:168-198` |
| AssignmentTaskUpdate        | 持久化 Assignment 与 Task       | `statemachine.go:200-220` |
| Start / Advance / Finalize / AbortMigration | 推进单个 hash slot 迁移状态 | `statemachine.go` |
| AddSlot / RemoveSlot        | 更新 Assignment、生成迁移计划、持久化新表 | `statemachine.go` |

#### 节点状态转移

```
              3s 无心跳         10s 无心跳
   Alive  ──────────────▸  Suspect  ──────────────▸  Dead
     ▲                        │
     │   收到心跳              │ 收到心跳
     └────────────────────────┘

   手动操作：
   Alive / Suspect / Dead  ──[drain]──▸  Draining
   Draining               ──[resume]──▸  Alive
```

**超时参数**：
- SuspectTimeout = 3s（`statemachine.go:26`）
- DeadTimeout = 10s（`statemachine.go:27`）

### 4.4 任务执行流程

```
Controller Leader (Planner)
  │ 1. 生成 ReconcileTask（Bootstrap / Repair / Rebalance）
  ▼
Controller StateMachine
  │ 2. 通过 Raft 持久化 Task + Assignment
  ▼
Node Agent (slotAgent)                           // agent.go:89-249
  │ 3. Agent 周期性通过 RPC 获取 Task
  │ 4. 选择执行器节点（优先 SourceNode，回退到 Leader）
  │ 5. 在本地 MultiRaft Runtime 执行：
  │    AddLearner → CatchUp → Promote → TransferLeader → RemoveOld
  │ 6. 报告结果到 Controller
  ▼
Controller StateMachine
  │ 7. 成功 → 删除 Task
  │    失败 → 重试（指数退避，最多 3 次）
  ▼
完成
```

**任务步骤详解**：

| 步骤            | 说明                                        |
| --------------- | ------------------------------------------- |
| AddLearner      | 将目标节点作为 Learner 加入 Raft Slot      |
| CatchUp         | 等待 Learner 日志追上 Leader 的 CommitIndex |
| Promote         | 将 Learner 提升为 Voter                     |
| TransferLeader  | 如果源节点是 Leader，先转移 Leadership     |
| RemoveOld       | 将源节点从 Voter 移除                      |

**安全保证**（单步变更原则）：
- 每次任务最多替换一个副本
- 新副本必须追上日志后才提升为 Voter
- 不会在 Quorum 丢失时执行自动修复

### 4.5 重试机制

- MaxTaskAttempts = 3（默认）
- 退避公式：`RetryBackoffBase * (2 ^ (attempt - 1))`
- RetryBackoffBase = 1s（默认）
- 超过最大尝试次数后，Task 标记为 Failed，需要人工介入

## 5. Agent（节点侧）

每个节点都运行一个 `slotAgent`，职责：

1. **周期上报**：每 200ms 通过 RPC 向 Controller Leader 发送心跳（`observeOnce()` → `HeartbeatOnce()`），同时上报本地 `HashSlotTableVersion`
2. **同步分配**：拉取最新的 SlotAssignment，对比本地状态，Open/Close 相应的 ManagedSlot
3. **表同步**：当 heartbeat 或 assignment 刷新返回更新后的 `HashSlotTable` 时，立即应用到本地 router/runtime
4. **执行任务**：获取 Controller 分配给本节点的 ReconcileTask，并在本地执行副本修复或配合 hash slot 迁移

**RPC 接口**（`controller_client.go:85-164`）：

| 方法                 | 用途                              |
| -------------------- | --------------------------------- |
| Report()             | 发送心跳 + RuntimeView + 本地表版本 |
| RefreshAssignments() | 拉取所有 SlotAssignment          |
| GetTask()            | 获取待执行的 ReconcileTask        |
| ReportTaskResult()   | 上报任务执行结果（成功/失败）     |
| ListNodes()          | 查询集群节点状态                  |
| Operator()           | 发起 Drain / Resume / Rebalance   |
| ForceReconcile()     | 强制触发某个 Slot 的修复调度      |

**Leader 发现**：客户端优先使用缓存的 LeaderID，如果收到 NotLeader 响应，则根据 hint 重定向（`controller_client.go:201-256`）。

## 6. 持久化（controllermeta）

所有控制面数据都持久化在 **Pebble** KV 存储中（`store.go:400+` 行）：

| 数据             | Key 格式                     | 说明                           |
| ---------------- | ---------------------------- | ------------------------------ |
| ClusterNode      | `node/<nodeID>`              | 节点状态、地址、容量权重       |
| SlotAssignment  | `assignment/<slotID>`        | 期望副本集 + ConfigEpoch       |
| SlotRuntimeView | `runtime_view/<slotID>`      | 当前实际状态 + LeaderID        |
| ReconcileTask   | `task/<slotID>`              | 迁移进度 + 重试信息            |

## 7. 配置参考

### 7.1 集群级配置（raftcluster/config.go）

| 参数                  | 类型          | 默认值       | 说明                              |
| --------------------- | ------------- | ------------ | --------------------------------- |
| NodeID                | uint64        | 必填         | 当前节点 ID                       |
| ControllerMetaPath    | string        | 必填         | Controller 元数据 Pebble 路径     |
| ControllerRaftPath    | string        | 必填         | Controller Raft 日志存储路径      |
| ControllerReplicaN    | int           | len(Nodes)   | Controller Raft 副本数            |
| HashSlotCount         | uint16        | 256          | 固定逻辑分片数量，用于 Key 哈希   |
| InitialSlotCount      | uint32        | 必填         | 初始 physical Slot 数量          |
| SlotReplicaN          | int           | 必填         | 每个 physical Slot 的目标副本数  |

### 7.2 Planner 配置（slotcontroller/types.go:23-29）

| 参数                     | 类型           | 默认值 | 说明                                  |
| ------------------------ | -------------- | ------ | ------------------------------------- |
| HashSlotCount           | uint16         | -      | HashSlotTable 管理的逻辑分片总数     |
| InitialSlotCount        | uint32         | -      | 初始 physical Slot 总数              |
| ReplicaN                 | int            | -      | 期望副本数                            |
| RebalanceSkewThreshold   | int            | 2      | 负载差超过此值才触发重平衡            |
| MaxTaskAttempts          | int            | 3      | 任务最大重试次数                      |
| RetryBackoffBase         | time.Duration  | 1s     | 重试退避基数                          |

### 7.3 超时配置

| 参数                        | 默认值 | 说明                              |
| --------------------------- | ------ | --------------------------------- |
| SuspectTimeout              | 3s     | 标记节点为 Suspect 的超时         |
| DeadTimeout                 | 10s    | 标记节点为 Dead 的超时            |
| controllerRequestTimeout    | 2s     | Controller RPC 请求超时           |
| controllerObservationInterval | 200ms | Agent 上报与 Tick 的周期          |
| controllerLeaderWaitTimeout | 10s    | 等待 Controller Leader 就绪超时   |

## 8. 容错与安全保证

| 场景                      | 行为                                                          |
| ------------------------- | ------------------------------------------------------------- |
| Controller 不可用         | 现有 Slot 继续运行，但不会有新的调度决策                      |
| Controller Leader 切换    | 新 Leader 从持久化状态恢复，继续未完成的 Task                 |
| 节点短暂失联              | 先 Suspect（3s），再 Dead（10s），避免抖动触发不必要的迁移   |
| 任务执行失败              | 指数退避重试，最多 3 次，超限后标记 Failed 等待人工处理      |
| Quorum 丢失               | Slot 标记为 Degraded，不执行自动修复，需人工恢复             |
| 并发修复安全              | 每次最多替换一个副本，不进行批量副本替换                     |
