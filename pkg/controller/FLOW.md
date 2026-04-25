# pkg/controller 流程文档

## 1. 职责定位

集群控制面，负责 Slot 副本的分配、节点健康检测、故障自动修复与负载均衡。基于 Raft 共识保证多 Controller 节点间状态一致。
**不负责**: 消息读写（由 channel 负责）、元数据存储（由 slot 负责）。

## 2. 子包分工

| 子包 | 入口/核心类型 | 职责 |
|------|-------------|------|
| `meta/` | `meta.Store` | Pebble KV 持久化：Node / Assignment / Task / Membership 的 CRUD；`RuntimeView` 结构仍保留但 steady-state 读路径已转为 leader 本地 observation |
| `raft/` | `raft.NewService()` → `Service` | Raft 共识服务：事件循环、提案处理、日志持久化、Leader 选举 |
| `plane/` | `plane.NewController()` → `Controller` | 控制面逻辑：StateMachine 命令应用 + Planner 调度决策 + Controller.Tick 编排 |

## 3. 对外接口

```go
// raft/service.go — Raft 提案入口
Service.Propose(ctx, Command) error   // 提交命令到 Raft（仅 Leader 可执行）
Service.LeaderID() uint64             // 当前 Leader
Service.Start(ctx) / Stop()           // 生命周期

// plane/controller.go — 调度入口
Controller.Tick(ctx) error            // 周期调用，仅 Leader 执行决策

// plane/statemachine.go — Raft 提交后的命令应用
StateMachine.Apply(ctx, Command) error
```

## 4. 关键类型

| 类型 | 文件 | 说明 |
|------|------|------|
| `ClusterNode` | meta/types.go | 节点：NodeID, Addr, Status(Alive/Suspect/Dead/Draining), LastHeartbeatAt, CapacityWeight |
| `SlotAssignment` | meta/types.go | Slot分配：SlotID, DesiredPeers, ConfigEpoch, BalanceVersion |
| `SlotRuntimeView` | meta/types.go | Slot运行时：CurrentPeers, LeaderID, HasQuorum, ObservedConfigEpoch |
| `ReconcileTask` | meta/types.go | 调和任务：Kind(Bootstrap/Repair/Rebalance), Step, SourceNode, TargetNode, Attempt, Status |
| `Command` | plane/commands.go | 命令信封：Kind + Report/Op/Advance/Assignment/Task/Migration/AddSlot/RemoveSlot/NodeStatusUpdate |

**节点状态转移:**
```
Unknown → Alive ←→ Suspect(心跳>3s) → Dead(心跳>10s)
             ↕ (运维操作)
          Draining
```

## 5. 核心流程

### 5.1 命令提案与应用

入口: `raft/service.go:214 Propose` → `raft/service.go:304 run` → `plane/statemachine.go:47 Apply`

```
  ① 客户端调用 Propose(cmd)
  ② 事件循环检查是否 Leader → 非 Leader 返回 ErrNotLeader
  ③ encodeCommand(cmd) → JSON (raft/service.go:564)
  ④ rawNode.Propose(data)
  ⑤ processReady: 持久化 → transport.Send → 等待多数确认
  ⑥ CommittedEntries: decodeCommand → StateMachine.Apply(cmd)
  ⑦ 通知提案者 (resp channel)
```

### 5.2 StateMachine 命令

入口: `plane/statemachine.go:47 Apply`

```
NodeHeartbeat (statemachine.go:79):
  兼容命令；仍可查找/创建节点并更新 Addr/Heartbeat/Weight/RuntimeView，但 steady-state 观测路径已不再持续提案该命令

OperatorRequest (statemachine.go:113):
  MarkDraining → Status=Draining
  Resume → Status=Alive + 原子删除所有Repair任务 (store.UpsertNodeAndDeleteRepairTasks)

NodeStatusUpdate (statemachine.go):
  按批读取目标节点 → 校验可选 expected prior status → 应用 Alive/Suspect/Dead/Draining 边沿状态 → 持久化节点

EvaluateTimeouts (statemachine.go:142):
  兼容扫描逻辑仍保留在状态机中，但 steady-state 控制流已不再周期性提案该命令

TaskResult (statemachine.go:168):
  成功(Err=nil) → 删除任务
  失败 → Attempt++, <MaxAttempts(3)则Retrying+指数退避, 否则Failed

AssignmentTaskUpdate (statemachine.go:200):
  Repair任务先检查SourceNode是否恢复(已Alive→过时跳过) → 原子持久化Assignment+Task

StartMigration / AdvanceMigration / AbortMigration:
  读取 HashSlotTable → 更新迁移状态 → 保存回 controllermeta

FinalizeMigration:
  读取 HashSlotTable → 将 hash slot 最终切换到 Target → 若 Source 已无 hash slot 且无剩余迁移，则原子删除对应 SlotAssignment/Task → 保存回 controllermeta

AddSlot:
  创建新 SlotAssignment → 调用 hash-slot 再平衡算法 → 为迁入新 Slot 的 hash slot 建立 Snapshot 阶段迁移记录

RemoveSlot:
  调用 hash-slot 再平衡算法 → 为被移除 Slot 上的 hash slot 建立 Snapshot 阶段迁移记录
  SlotAssignment 先保留，待最后一个 hash slot FinalizeMigration 后自动删除
```

### 5.3 Planner 调度决策

入口: `plane/planner.go:93 NextDecision`

```
第一遍 — 遍历所有 Slot (1~SlotCount):
  ReconcileSlot (planner.go:22):
    ① 无仲裁 → Degraded(等待)
    ② 有进行中Task → 检查 taskRunnable(Pending直接可执行, Retrying等NextRunAt)
    ③ 无Assignment且无RuntimeView → Bootstrap:
       selectBootstrapPeers → 选 ReplicaN 个最低负载 Alive 节点 (planner.go:168)
    ④ DesiredPeers 中有 Dead/Draining → Repair:
       firstPeerNeedingRepair (planner.go:199) → selectRepairTarget (planner.go:209)
  找到第一个需要处理的 → 立即返回

第二遍 — 无紧急任务时尝试 Rebalance (planner.go:107):
  ① slotLoads 计算每节点 Slot 数 (planner.go:237)
  ② loadExtremes 找 min/max 节点 (planner.go:247)
  ③ maxLoad - minLoad < 阈值(默认2) → 无需均衡
  ④ 找候选: 在maxNode上且不在minNode上, 有仲裁, 无失败任务
  ⑤ 迁移中的物理 Slot(source/target 任一侧)跳过 Repair/Rebalance，避免副本迁移和 hash-slot 数据迁移叠加
  ⑥ 按 BalanceVersion 排序(最久未动优先) → 生成 Rebalance 任务
```

### 5.4 Controller.Tick 编排

入口: `plane/controller.go:40 Tick`

```
  ① isLeader() → 非 Leader 直接返回
  ② snapshot() → 从 Store 加载 durable Nodes/Assignments/Tasks，并从 Controller Leader 本地 observation snapshot 取 RuntimeViews
  ③ 若 leader 仍处于 warmup（尚未收到新鲜观测）则跳过本轮规划
  ④ Planner.NextDecision(state) → Decision{SlotID, Assignment, Task}
  ⑤ SlotID == 0 → 无需操作
  ⑥ 持久化:
     有 Assignment+Task → store.UpsertAssignmentTask (原子)
     仅 Assignment → store.UpsertAssignment
     仅 Task → store.UpsertTask
```

### 5.5 任务步骤推进

每个 ReconcileTask 按以下顺序逐步推进（由外部任务执行器驱动，每步完成后上报 TaskResult）:
```
AddLearner → CatchUp → Promote → TransferLeader → RemoveOld
```

## 6. 存储层要点

- **记录前缀**: `n`(Node) / `m`(Membership) / `a`(Assignment) / `v`(RuntimeView) / `t`(Task) → `meta/store.go`
- **二进制编解码**: 第一字节版本号，大端序整数，varint 变长字段 → `meta/codec.go`
- **原子操作**: `UpsertNodeAndDeleteRepairTasks` / `UpsertAssignmentTask` → 保证跨记录一致性
- **快照**: Magic("WKCS") + Version + Entries + CRC32 → `meta/snapshot.go`

## 7. Raft 配置

| 参数 | 值 | 位置 |
|------|-----|------|
| tickInterval | 100ms | raft/service.go |
| electionTick | 10 (= 1s) | raft/service.go |
| heartbeatTick | 1 (= 100ms) | raft/service.go |
| MaxInflightMsgs | 256 | raft/service.go |
| CheckQuorum / PreVote | true / true | raft/service.go |
| Bootstrap 触发 | 无持久化状态 + AllowBootstrap + 最小PeerID | raft/service.go:170 |
| Raft Logger | `wklog` 结构化日志，模块 `controller.raft`，附带 `raftScope=controller` / `nodeID` / `raftEvent`；heartbeat/read-index/probe 类噪声按 Debug 输出 | raft/logging.go |

## 8. 避坑清单

- **仅 Leader 规划**: `Controller.Tick` 第一行检查 `isLeader()`，Follower 上调用是空操作。不要在 Follower 上直接写 Store。
- **Repair 过时检测**: `statemachine.go:repairTaskObsolete` 在应用 Repair 任务前检查 SourceNode 是否已恢复为 Alive。跳过则避免不必要的迁移。
- **Attempt 匹配**: `applyTaskResult` 用 Attempt 字段防止过期的 TaskResult 影响新一轮任务。Attempt 不匹配时静默忽略。
- **Draining 不受观测恢复影响**: `NodeStatusUpdate` / `applyNodeHeartbeat` 都不能把 Draining 自动恢复为 Alive，必须通过 OperatorResumeNode 显式恢复。
- **健康状态改为边沿复制**: steady-state 不再周期性 `EvaluateTimeouts`；由 leader 本地 deadline scheduler 只在状态跨边沿时提案 `NodeStatusUpdate`。
- **规划依赖 leader 本地 observation**: RuntimeView 不再是 steady-state 的 replicated metadata。新 leader warmup 期间必须 fail-closed，优先延迟 Repair/Rebalance，避免误判。
- **指数退避上限**: `retryDelay` 中 shift 上限为 30，防止溢出。重试延迟 = base × 2^(attempt-1)。
- **Command 序列化为 JSON**: `raft/service.go:encodeCommand` 使用 JSON（非二进制），TaskAdvance.Err 序列化为 string 再反序列化为 `errors.New`。
- **Leader 丢失时清理**: `raft/service.go:failInflightProposalsOnLeaderLoss` 在每次状态检查后清理所有 pending 提案，返回 ErrNotLeader。
